// Command axon posts your in-game intel channels to an EveSynapse
// instance as you play. It reads the chat logs the EVE client writes and
// sends each new line of the channels it is told about to the site's
// intel channel API, where lines naming a system become reports on the
// map (package watch).
//
// One file per computer is both the installer and the program. Run
// from anywhere but its installed place, it installs itself (package
// install) and starts the installed copy. The installed copy opens
// Axon's window (package gui), and keeps watching in the background,
// in the tray on Windows, when the window is closed.
//
//	axon                      install, or open Axon's window
//	axon -config path.json    another config
//	axon -replay              also send what today's logs already hold
//	axon -watch               watch from the terminal, no window
//	axon -tray                start watching in the tray (Windows)
//	axon -url evesynapse-axon://open
//	                          open Axon's window (a link from the site)
//	axon -install             install (or update) from this file
//	axon -portable            run this file where it is, without installing
//	axon -uninstall           remove the installed Axon (the config stays)
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/natemsz/axon/gui"
	"github.com/natemsz/axon/internal/install"
	"github.com/natemsz/axon/internal/releasesig"
	"github.com/natemsz/axon/internal/sys"
	"github.com/natemsz/axon/internal/update"
	"github.com/natemsz/axon/watch"
)

var version = "dev"

// releaseSigningPub is the committed release-signing.pub: the
// public halves of the key axon releases are signed with, which
// the self-update checks release signatures against.
//
//go:embed release-signing.pub
var releaseSigningPub []byte

// maxLogFile is the size at which axon.log starts afresh.
const maxLogFile = 1 << 20

func main() {
	configPath := flag.String("config", "", "the config file (default: the installed one, else axon.json here or beside the program)")
	replay := flag.Bool("replay", false, "also send the lines already in the logs")
	logsDir := flag.String("logs", "", "the EVE chat log folder (default: found)")
	flag.Bool("gui", false, "open Axon's window (the default)")
	watchOnly := flag.Bool("watch", false, "watch from the terminal, without the window")
	tray := flag.Bool("tray", false, "start watching in the tray, the window a click away (Windows; elsewhere, as without it)")
	doInstall := flag.Bool("install", false, "install (or update) Axon from this file")
	portable := flag.Bool("portable", false, "run this file where it is, without installing it")
	uninstall := flag.Bool("uninstall", false, "remove the installed Axon; the config and log stay")
	installed := flag.Bool("installed", false, "(set by the installer) say that Axon was just installed")
	urlFlag := flag.String("url", "", "(set by the system) an "+install.URLScheme+":// link to act on: open Axon's window")
	flag.Parse()
	log.SetFlags(log.Ltime)
	link := isLink(*urlFlag) || (flag.NArg() == 1 && isLink(flag.Arg(0)))
	// A link from the site with Axon already running just brings its
	// window up, before anything else is touched.
	if link && gui.ShowRunning() {
		return
	}
	terminal := sys.AttachConsole()

	fail := func(err error) {
		log.Printf("axon: %v", err)
		if !terminal {
			sys.Alert(install.AppName, err.Error())
		}
		os.Exit(1)
	}
	layout, err := install.Default()
	if err != nil {
		fail(err)
	}
	exe, _ := os.Executable()

	switch {
	case *uninstall:
		if err := install.Uninstall(layout, gui.StopRunning, log.Printf); err != nil {
			fail(err)
		}
		if !terminal {
			sys.Alert(install.AppName, install.AppName+" is uninstalled. Your axon.json and axon.key are kept in "+filepath.Dir(layout.Config)+".")
		}
		return
	case *doInstall || (!link && shouldInstall(layout, exe, *portable)):
		if _, err := install.Install(layout, exe, version, gui.StopRunning, log.Printf); err != nil {
			fail(err)
		}
		if err := install.Launch(layout, "-installed"); err != nil {
			fail(fmt.Errorf("installed, but could not start it: %w", err))
		}
		return
	}

	path := findConfig(*configPath, layout, exe)
	_, missing := os.Stat(path)
	// Straight to the window unless -watch was asked for and a config
	// is there to watch with; with nothing to watch yet, the window
	// is where that gets fixed.
	if *watchOnly && !*tray && !errors.Is(missing, os.ErrNotExist) {
		err = run(path, *logsDir, *replay)
	} else {
		logFile := logTo(path, terminal)
		notice := ""
		if *installed {
			notice = install.Summary
		}
		updater, pending := pendingUpdate(*portable)
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		// Opened by a link, Axon starts as it does at sign-in (watching,
		// in the tray on Windows) with its window up.
		err = gui.Run(ctx, gui.Options{ConfigPath: path, Logs: *logsDir, Replay: *replay, Version: version, Tray: *tray || link, Show: link, Notice: notice, LogFile: logFile, Update: pending, Updater: updater})
	}
	if err != nil {
		fail(err)
	}
}

// pendingUpdate runs the self-update check for a window start:
// the checker to fetch an update with, and the newer release
// waiting, if any. Portable runs and dev builds do not update,
// and a check that fails (offline, an answer that does not read)
// is logged and forgotten: starting axon never waits on it for
// long and never fails for it.
func pendingUpdate(portable bool) (*update.Checker, *update.Release) {
	if portable || version == "dev" {
		return nil, nil
	}
	keys, err := releasesig.ParsePublicKeys(releaseSigningPub)
	if err != nil {
		log.Printf("self-update is off: %v", err)
		return nil, nil
	}
	checker := &update.Checker{Keys: keys}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	rel, err := checker.Check(ctx, version)
	if err != nil {
		log.Printf("update check: %v", err)
		return nil, nil
	}
	if rel != nil {
		log.Printf("update available: axon %s (this is %s)", rel.Version, version)
	}
	return checker, rel
}

// shouldInstall: the downloaded file, run as it is, installs itself.
// Not with any flag given (that is someone choosing how to run it),
// nor when built from source (go run, go build), nor once installed.
func shouldInstall(l install.Layout, exe string, portable bool) bool {
	if portable || flag.NFlag() > 0 || version == "dev" || exe == "" {
		return false
	}
	return !l.Holds(exe)
}

// isLink reports whether arg is an evesynapse-axon: link, which the
// system hands Axon when the site's link is clicked. Every such link
// opens Axon's window: evesynapse-axon://open is the one the site
// uses, and anything else after the scheme is ignored.
func isLink(arg string) bool {
	scheme, _, ok := strings.Cut(arg, ":")
	return ok && strings.EqualFold(scheme, install.URLScheme)
}

func findConfig(flagged string, l install.Layout, exe string) string {
	if flagged != "" {
		return flagged
	}
	if l.Holds(exe) {
		return l.Config
	}
	if _, err := os.Stat("axon.json"); err == nil {
		return "axon.json"
	}
	if exe != "" {
		return filepath.Join(filepath.Dir(exe), "axon.json")
	}
	return "axon.json"
}

// logTo writes the log to axon.log beside the config as well (and only
// there, with no terminal to show it), starting afresh once it grows
// past maxLogFile. It returns the file's path, "" when it cannot.
func logTo(configPath string, terminal bool) string {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return ""
	}
	path := filepath.Join(filepath.Dir(abs), "axon.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ""
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogFile {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return ""
	}
	if terminal {
		log.SetOutput(io.MultiWriter(f, os.Stderr))
	} else {
		log.SetOutput(f)
	}
	return path
}

func run(path, logsDir string, replay bool) error {
	cfg, err := watch.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("%s: %v (open Axon's window to fix it)", path, err)
	}
	dir := logsDir
	if dir == "" {
		dir = cfg.Logs
	}
	if dir == "" {
		candidates := watch.DefaultLogDirs()
		if dir = watch.FindLogDir(candidates); dir == "" {
			return fmt.Errorf("could not find EVE's chat logs (looked in %s): set \"logs\" in %s to the Chatlogs folder, or open Axon's window", strings.Join(candidates, ", "), path)
		}
	}
	key, err := watch.LoadKey(watch.KeyPath(path))
	if err != nil {
		return err
	}
	log.Printf("axon %s", version)
	w := &watch.Watcher{
		Dir:    dir,
		Client: &watch.Client{Server: cfg.Server, Token: cfg.Token, Key: key, Agent: "axon/" + version},
		Map:    cfg.Channels,
		Replay: replay,
		Combat: cfg.CombatOn(),
		Logf:   log.Printf,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return w.Run(ctx)
}
