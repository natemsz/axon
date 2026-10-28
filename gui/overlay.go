package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/natemsz/axon/watch"
)

// The overlay: a slim, see-through bar that stays on top of the
// game, holding a paste field and a Send button and nothing else.
// It is the front door for what chat cannot carry: the EVE client
// cuts a long paste (a d-scan's celestials, a busy local) at its
// per-message limit, so the rows that identify the system never
// reach the channel. Sent from here, the paste goes whole, through
// the same signed path as a watched line, spoken by the character
// whose chat log changed most recently (watch.Poster).
//
// Nothing is sent that the player did not send. The clipboard is
// read once, at the moment the player sends with the field empty
// (the fast path: shortcut, Enter, done), and never otherwise. With
// text in the field, the overlay shows what it makes of it before
// Send: the kind, read here (watch.DetectKind), and the system, from
// the site's preview. The player may override either; overrides
// travel with the paste, and the site still reads it itself.
//
// The window is the platform's (overlay_windows.go); its page is
// page/overlay.html, served here like the settings page and keyed
// the same way. Everything it asks for is in this file, so it is
// tested like the rest.

// overlayDesk is what the overlay window needs of the server: its
// page's address, the shortcuts to register, what a shortcut does,
// and a way to say what happened where the player will see it.
type overlayDesk struct {
	url        string
	keys       func() map[shortcutAction]string
	onShortcut func(shortcutAction)
	note       func(format string, args ...any)
}

// overlayDesk is the server's side of the overlay window.
func (s *server) overlayDesk() overlayDesk {
	return overlayDesk{
		url:        "http://" + s.host + "/overlay.html?k=" + s.key,
		keys:       s.overlayKeys,
		onShortcut: s.onShortcut,
		note:       s.logf,
	}
}

// overlayConfig is the saved config as it stands now: the overlay
// follows a save with no restart.
func (s *server) overlayConfig() (watch.Config, error) {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	return watch.LoadConfig(path)
}

// overlayKeys is the config's shortcuts, by action; none when the
// config does not load.
func (s *server) overlayKeys() map[shortcutAction]string {
	c, err := s.overlayConfig()
	if err != nil {
		return nil
	}
	k := c.Keys()
	return map[shortcutAction]string{actSubmit: k.Submit, actDashboard: k.Dashboard, actMap: k.Map, actFeed: k.Feed, actSite: k.Site, actThreads: k.Threads}
}

// onShortcut does what a shortcut is for: the submission shortcut
// brings the overlay up with its field focused; the workbench shortcut
// is the master, bringing up the intel dock and the windows open last
// time (or putting them all away); the threads, map and feed shortcuts
// toggle their own intel window (intel.go); the site panel's shortcut
// opens the whole site's workbench on top of the game. Where there
// are no such windows, the site opens in the browser.
func (s *server) onShortcut(a shortcutAction) {
	if a == actSubmit {
		showOverlayWindow()
		return
	}
	if a == actDashboard && overlaySupported {
		s.intelMaster()
		return
	}
	if m, ok := intelWindowOf[a]; ok && s.openIntel(m, 0, true) {
		return
	}
	c, err := s.overlayConfig()
	if err != nil {
		s.logf("shortcut: no site to open: %v", err)
		return
	}
	url := strings.TrimRight(c.Server, "/") + sitePaths[a]
	if showSitePanel(url) {
		return
	}
	if err := OpenBrowser(url); err != nil {
		s.logf("shortcut: could not open %s: %v", url, err)
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
	// The chat log folder, settled the watcher's way: the one
	// given, else the config's, else the one found.
	dir := s.opts.Logs
	if dir == "" {
		dir = cfg.Logs
	}
	if dir == "" {
		dir = watch.FindLogDir(watch.DefaultLogDirs())
	}
	return &watch.Poster{Client: &watch.Client{Server: cfg.Server, Token: cfg.Token, Key: key, Agent: "axon/" + s.opts.Version}, LogDir: dir}, nil
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

// overlayTarget is the channel the overlay posts to: the config's
// choice when the account may still post there, else the first the
// account may post to.
func (s *server) overlayTarget() (watch.Channel, error) {
	_, list, err := s.overlayChannels()
	if err != nil {
		return watch.Channel{}, err
	}
	if c, cerr := s.overlayConfig(); cerr == nil && c.OverlayChannel > 0 {
		for _, ch := range list {
			if ch.ID == c.OverlayChannel {
				return ch, nil
			}
		}
	}
	return list[0], nil
}

// overlayPost sends one paste and answers in words: what was posted
// where, or why not. The outcome also goes in the activity log,
// whichever way it went: a post from the overlay is never silent, in
// the window or here.
func (s *server) overlayPost(channel int64, text string, ov watch.Overrides) (string, error) {
	p, err := s.overlayClient()
	if err != nil {
		s.logf("overlay: not posted: %v", err)
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, target, as, err := p.PostWith(ctx, channel, text, time.Now(), ov)
	if err != nil {
		s.logf("overlay: not posted: %v", err)
		return "", err
	}
	say := postedText(target.Name, res) + ", as " + as
	s.logf("overlay: %s", say)
	return say, nil
}

// overlaySend is the overlay's Send. With the field empty, the
// clipboard is read now, once: the player's send is the only thing
// that ever reads it.
func (s *server) overlaySend(text string, ov watch.Overrides) (string, error) {
	if strings.TrimSpace(text) == "" {
		clip, ok := clipboardText()
		if !ok || strings.TrimSpace(clip) == "" {
			return "", apiError{"The clipboard holds no text to send. Copy the scan or the line first, or paste it here."}
		}
		text = clip
	}
	// The post resolves the channel itself (0 is the first the
	// account may post to): one channel lookup per send, not two.
	channel := int64(0)
	if c, err := s.overlayConfig(); err == nil {
		channel = c.OverlayChannel
	}
	say, err := s.overlayPost(channel, text, ov)
	if err != nil && channel != 0 && strings.Contains(err.Error(), "is not one this account can post to") {
		// The chosen channel is gone: the first one still takes it.
		return s.overlayPost(0, text, ov)
	}
	return say, err
}

// overlayPreview is what the overlay shows before Send: the kind,
// read here, and the system the site would file the paste under.
type overlayPreview struct {
	Kind       string `json:"kind"`
	SystemID   int64  `json:"system_id,omitempty"`
	SystemName string `json:"system_name,omitempty"`
	SystemFrom string `json:"system_from,omitempty"`
	// Note says why no system is shown, when the site could not be
	// asked.
	Note string `json:"note,omitempty"`
}

func (s *server) overlayPreviewOf(text string) overlayPreview {
	pv := overlayPreview{Kind: watch.DetectKind(text)}
	if pv.Kind == "" {
		return pv
	}
	p, err := s.overlayClient()
	if err != nil {
		pv.Note = err.Error()
		return pv
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	site, err := p.Preview(ctx, text)
	if err != nil {
		pv.Note = "the site could not be asked for the system: " + err.Error()
		return pv
	}
	pv.SystemID, pv.SystemName, pv.SystemFrom = site.SystemID, site.SystemName, site.SystemFrom
	return pv
}

// --- The overlay page's API ---

// overlayState is what the overlay page shows besides its field:
// where a send goes, and the shortcut that brings it up.
func (s *server) overlayState(*http.Request) (any, error) {
	out := map[string]any{"channel": "", "submit": ""}
	if k := s.overlayKeys(); k != nil {
		if c, err := parseChord(k[actSubmit]); err == nil {
			out["submit"] = c.String()
		}
	}
	if t, err := s.overlayTarget(); err == nil {
		out["channel"] = t.Name
	} else {
		out["problem"] = err.Error()
	}
	return out, nil
}

func (s *server) overlayPreviewAPI(r *http.Request) (any, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return nil, apiError{"send {\"text\": \"...\"}"}
	}
	return s.overlayPreviewOf(in.Text), nil
}

func (s *server) overlaySendAPI(r *http.Request) (any, error) {
	var in struct {
		Text       string `json:"text"`
		Kind       string `json:"kind"`
		System     int64  `json:"system"`
		SystemName string `json:"system_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return nil, apiError{"send {\"text\": \"...\"}"}
	}
	say, err := s.overlaySend(in.Text, watch.Overrides{Kind: in.Kind, System: in.System, SystemName: strings.TrimSpace(in.SystemName)})
	if err != nil {
		return nil, err
	}
	return map[string]string{"said": say}, nil
}

// overlayClipboard is the overlay field's own Ctrl+V, for when the
// page's paste did not land: the player pressed paste, so the
// clipboard is read now, once, and given to the field. It is never
// read otherwise.
func (s *server) overlayClipboard(*http.Request) (any, error) {
	text, ok := clipboardText()
	if !ok {
		return nil, apiError{"the clipboard holds no text"}
	}
	return map[string]string{"text": text}, nil
}

// overlayRelease gives the keyboard back to whatever had it before
// the overlay was called up (the game); the overlay stays on screen.
func (s *server) overlayRelease(*http.Request) (any, error) {
	releaseOverlayFocus()
	return struct{}{}, nil
}

// overlayHide puts the overlay away until its shortcut brings it
// back.
func (s *server) overlayHide(*http.Request) (any, error) {
	hideOverlayWindow()
	return struct{}{}, nil
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
		return "Nothing posted to " + name + ": the site skipped it"
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// overlayShow is the settings page's Send intel button: bring the
// overlay up, or say plainly that there is none here.
func (s *server) overlayShow(*http.Request) (any, error) {
	if !overlaySupported {
		return nil, apiError{"the overlay is part of Axon on Windows for now; here, paste into the site's own channel page instead"}
	}
	showOverlayWindow()
	return map[string]bool{"shown": true}, nil
}
