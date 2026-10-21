// Command axon posts your in-game intel channels to an EveSynapse
// instance as you play. It reads the chat logs the EVE client writes and
// sends each new line of the channels it is told about to the site's
// intel channel API, where lines naming a system become reports on the
// map (package watch).
//
// Make a token on the site's Intel channels page; it comes inside a
// ready axon.json. Save that next to this program and run it.
//
//	axon                      open the settings page in the browser
//	axon -config path.json    another config
//	axon -replay              also send what today's logs already hold
//	axon -gui                 open the settings page (the default)
//	axon -watch               watch from the console, no page
//	axon -tray                watch from the tray icon (Windows)
//
// Axon always opens straight to its settings page (package gui);
// watching starts from there, or at once with -tray or -watch.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"

	"github.com/natemsz/axon/gui"
	"github.com/natemsz/axon/watch"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "the config file (default: axon.json here, or beside the program)")
	replay := flag.Bool("replay", false, "also send the lines already in the logs")
	logsDir := flag.String("logs", "", "the EVE chat log folder (default: found)")
	settings := flag.Bool("gui", false, "open the settings page in the browser (the default)")
	watchOnly := flag.Bool("watch", false, "watch from the console, without opening the settings page")
	tray := flag.Bool("tray", false, "start watching from a tray icon, the settings a click away (Windows; elsewhere, as -gui)")
	flag.Parse()
	log.SetFlags(log.Ltime)
	path := findConfig(*configPath)
	_, missing := os.Stat(path)
	var err error
	// Straight to the GUI unless -watch was asked for and a config
	// is there to watch with; with nothing to watch yet, the page
	// is where that gets fixed.
	if *watchOnly && !*settings && !*tray && !errors.Is(missing, os.ErrNotExist) {
		err = run(path, *logsDir, *replay)
	} else {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		err = gui.Run(ctx, gui.Options{ConfigPath: path, Logs: *logsDir, Replay: *replay, Version: version, Tray: *tray})
	}
	if err != nil {
		log.Printf("axon: %v", err)
		pause()
		os.Exit(1)
	}
}

// pause keeps a console window opened by double-click on Windows up
// long enough to read why the program stopped.
func pause() {
	if runtime.GOOS != "windows" {
		return
	}
	fmt.Print("Press Enter to close.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func findConfig(flagged string) string {
	if flagged != "" {
		return flagged
	}
	if _, err := os.Stat("axon.json"); err == nil {
		return "axon.json"
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "axon.json")
	}
	return "axon.json"
}

func run(path, logsDir string, replay bool) error {
	cfg, err := watch.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("%s: %v (run axon -gui to fix it)", path, err)
	}
	dir := logsDir
	if dir == "" {
		dir = cfg.Logs
	}
	if dir == "" {
		candidates := watch.DefaultLogDirs()
		if dir = watch.FindLogDir(candidates); dir == "" {
			return fmt.Errorf("could not find EVE's chat logs (looked in %v): set \"logs\" in %s to the Chatlogs folder, or run axon -gui", candidates, path)
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
		Logf:   log.Printf,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return w.Run(ctx)
}
