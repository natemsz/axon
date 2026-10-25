package gui

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/natemsz/axon/watch"
)

// The overlay: a small window that stays on top of the game, with a
// box to paste into and a Send button. It is the front door for what
// chat cannot carry — the EVE client truncates a local roster pasted
// into a channel at twenty names — and what is sent from it is
// posted through the same signed path as a watched line, spoken by
// the account's own character (watch.Poster), so the site reads it
// exactly as it reads a watched line.
//
// The window itself is Windows' (overlay_windows.go), in the tray's
// manner; elsewhere there is none, and overlaySupported says so. The
// posting it drives is in this file, so it is tested like the rest.

// GrabChord names the grab's hotkey, for the README and the
// overlay's own notes.
const GrabChord = "Ctrl+Alt+V"

// overlayActions are what the overlay window can ask of the server:
// which channels a paste can go to, a post (whose answer is in words
// for the window's status line), whether the grab is on, and a way
// to say what happened where the player will see it.
type overlayActions struct {
	channels    func() (string, []watch.Channel, error)
	post        func(channel int64, text string) (string, error)
	grabEnabled func() bool
	note        func(format string, args ...any)
}

// overlayDesk is the server's side of the overlay window.
func (s *server) overlayDesk() overlayActions {
	return overlayActions{
		channels:    s.overlayChannels,
		post:        s.overlayPost,
		grabEnabled: s.overlayGrabEnabled,
		note:        s.logf,
	}
}

// overlayClient makes the poster the saved config and this
// computer's key describe, as the sign-in check does.
func (s *server) overlayClient() (*watch.Poster, error) {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	cfg, err := watch.LoadConfig(path)
	if err != nil {
		return nil, apiError{"save a working config first: " + err.Error()}
	}
	key, err := watch.LoadKey(watch.KeyPath(path))
	if err != nil {
		return nil, apiError{err.Error()}
	}
	return &watch.Poster{Client: &watch.Client{Server: cfg.Server, Token: cfg.Token, Key: key, Agent: "axon/" + s.opts.Version}}, nil
}

func (s *server) overlayChannels() (string, []watch.Channel, error) {
	p, err := s.overlayClient()
	if err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return p.Postable(ctx)
}

// overlayGrabEnabled is the config's grab flag as it stands now, so
// saving the setting is enough — no restart — and a config that
// does not load leaves the grab off.
func (s *server) overlayGrabEnabled() bool {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	c, err := watch.LoadConfig(path)
	return err == nil && c.Grab
}

// overlayPost sends one paste and answers in words: what was posted
// where, or why not. The outcome also goes in the activity log,
// whichever way it went: a post from the overlay is never silent, in
// the window or here.
func (s *server) overlayPost(channel int64, text string) (string, error) {
	p, err := s.overlayClient()
	if err != nil {
		s.logf("overlay: not posted: %v", err)
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, target, err := p.Post(ctx, channel, text, time.Now())
	if err != nil {
		s.logf("overlay: not posted: %v", err)
		return "", err
	}
	say := postedText(target.Name, res)
	s.logf("overlay: %s", say)
	return say, nil
}

// postedText is a post's outcome in words, for the window's status
// line and the activity log alike.
func postedText(name string, res watch.PostResult) string {
	if name == "" {
		name = "the channel"
	}
	switch {
	case res.Posted > 0:
		s := plural(res.Posted, "new report")
		if res.Duplicates > 0 {
			s += ", " + plural(res.Duplicates, "duplicate")
		}
		if res.Skipped > 0 {
			s += ", " + plural(res.Skipped, "skipped")
		}
		return "Posted to " + name + ": " + s
	case res.Duplicates > 0:
		return "Already on " + name + ": " + plural(res.Duplicates, "duplicate")
	default:
		return "Nothing posted to " + name + ": the site skipped the line"
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// overlayShow is the page's Paste intel button: bring the overlay
// up, or say plainly that there is none here.
func (s *server) overlayShow(*http.Request) (any, error) {
	if !overlaySupported {
		return nil, apiError{"the overlay is part of Axon on Windows; here, paste into the site's own channel page instead"}
	}
	showOverlayWindow()
	return map[string]bool{"shown": true}, nil
}
