// Command axon posts your in-game intel channels to an EveSynapse
// instance as you play. It reads the chat logs the EVE client writes and
// sends each new line of the channels it is told about to the site's
// intel channel API, where lines naming a system become reports on the
// map (package watch).
//
// Make a token on the site's Intel channels page; it comes inside a
// ready axon.json. Save that next to this program and run it.
//
//	axon                      read ./axon.json (or the one beside the program)
//	axon -config path.json    another config
//	axon -replay              also send what today's logs already hold
//
// With no config at all, axon writes an empty one to fill in.
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

	"github.com/natemsz/axon/watch"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "the config file (default: axon.json here, or beside the program)")
	replay := flag.Bool("replay", false, "also send the lines already in the logs")
	logsDir := flag.String("logs", "", "the EVE chat log folder (default: found)")
	flag.Parse()
	log.SetFlags(log.Ltime)
	if err := run(*configPath, *logsDir, *replay); err != nil {
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

func run(configPath, logsDir string, replay bool) error {
	path := findConfig(configPath)
	cfg, err := watch.LoadConfig(path)
	if errors.Is(err, os.ErrNotExist) {
		if werr := watch.WriteTemplate(path); werr != nil {
			return fmt.Errorf("no config, and could not write one at %s: %v", path, werr)
		}
		return fmt.Errorf("wrote an empty config at %s: fill in the site and your token (make one on the site's Intel channels page), then run again", path)
	}
	if err != nil {
		return fmt.Errorf("%s: %v", path, err)
	}
	dir := logsDir
	if dir == "" {
		dir = cfg.Logs
	}
	if dir == "" {
		candidates := watch.DefaultLogDirs()
		if dir = watch.FindLogDir(candidates); dir == "" {
			return fmt.Errorf("could not find EVE's chat logs (looked in %v): set \"logs\" in %s to the Chatlogs folder", candidates, path)
		}
	}
	log.Printf("axon %s", version)
	w := &watch.Watcher{
		Dir:    dir,
		Client: &watch.Client{Server: cfg.Server, Token: cfg.Token, Agent: "axon/" + version},
		Map:    cfg.Channels,
		Replay: replay,
		Logf:   log.Printf,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return w.Run(ctx)
}
