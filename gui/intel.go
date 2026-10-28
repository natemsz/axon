package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// The intel windows: the site's intel workbench, drawn by Axon itself
// as separate windows a player opens on their own, instead of the site
// loaded in a panel. Each is a module:
//
//	threads  the recent threads, one row each; a row opens its report
//	map      the systems the threads are in, the open one centred
//	feed     the activity log
//	report   one report broken down (the expanded workbench)
//
// They are pages of Axon's own (page/intel/), drawn in the look of the
// game's own windows, see-through over the game. Their data comes from
// the site's intel API through this file, signed with this computer's
// credential, so no site sign-in is needed. They talk to each other
// directly (a BroadcastChannel in the pages): a thread opened in one
// moves the map in another. The windows themselves are the platform's
// (intelwin_windows.go).

// intelModules are the windows there are, in the order the settings
// page lists them.
var intelModules = []string{"threads", "map", "feed", "report", "dock"}

func validIntelModule(m string) bool {
	for _, x := range intelModules {
		if x == m {
			return true
		}
	}
	return false
}

// intelURL is a module's page, keyed like the settings page.
func (s *server) intelURL(module string, report int64) string {
	u := "http://" + s.host + "/intel/" + module + ".html?k=" + s.key
	if report > 0 {
		u += "&id=" + strconv.FormatInt(report, 10)
	}
	return u
}

// openIntel brings a module's window up (toggle puts it away when it
// is already showing that page).
func (s *server) openIntel(module string, report int64, toggle bool) bool {
	return showIntelWindow(module, s.intelURL(module, report), toggle)
}

// --- The pages' API ---

func (s *server) intelCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func (s *server) intelThreadsAPI(*http.Request) (any, error) {
	p, err := s.overlayClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.intelCtx()
	defer cancel()
	return p.Client.IntelThreads(ctx)
}

func (s *server) intelFeedAPI(r *http.Request) (any, error) {
	p, err := s.overlayClient()
	if err != nil {
		return nil, err
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	ctx, cancel := s.intelCtx()
	defer cancel()
	return p.Client.IntelFeed(ctx, after)
}

func (s *server) intelReportAPI(r *http.Request) (any, error) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 {
		return nil, apiError{"no report asked for"}
	}
	p, err := s.overlayClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.intelCtx()
	defer cancel()
	return p.Client.IntelReport(ctx, id)
}

// The universe payload, kept once fetched: it changes with the game's
// own data, not by the minute, and it is several megabytes.
var universe struct {
	mu   sync.Mutex
	body json.RawMessage
	at   time.Time
}

func (s *server) intelUniverseAPI(*http.Request) (any, error) {
	universe.mu.Lock()
	defer universe.mu.Unlock()
	if universe.body != nil && time.Since(universe.at) < 12*time.Hour {
		return universe.body, nil
	}
	p, err := s.overlayClient()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b, err := p.Client.Universe(ctx)
	if err != nil {
		return nil, apiError{err.Error()}
	}
	if !json.Valid(b) {
		return nil, apiError{"the site's map payload did not read"}
	}
	universe.body, universe.at = b, time.Now()
	return universe.body, nil
}

// intelOpenAPI opens a module's window from a page: a thread's row
// opens its report; the dock's buttons toggle their window.
func (s *server) intelOpenAPI(r *http.Request) (any, error) {
	var in struct {
		Module string `json:"module"`
		Report int64  `json:"report"`
		Toggle bool   `json:"toggle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validIntelModule(in.Module) {
		return nil, apiError{"open which window?"}
	}
	if !s.openIntel(in.Module, in.Report, in.Toggle) {
		return nil, apiError{"the intel windows are part of Axon on Windows for now"}
	}
	s.rememberOpen()
	return struct{}{}, nil
}

// intelWindowAPI is a page's own frame: drag it, put it away, or (the
// dock) size it.
func (s *server) intelWindowAPI(r *http.Request) (any, error) {
	var in struct {
		Module string `json:"module"`
		Cmd    string `json:"cmd"`
		W      int    `json:"w"`
		H      int    `json:"h"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validIntelModule(in.Module) {
		return nil, apiError{"which window?"}
	}
	switch in.Cmd {
	case "drag":
		intelWindowDo(in.Module, "drag")
	case "hide":
		intelWindowDo(in.Module, "hide")
		s.rememberOpen()
	case "size":
		intelWindowSize(in.Module, in.W, in.H)
	default:
		return nil, apiError{"drag, hide or size"}
	}
	return struct{}{}, nil
}

// --- The dock ---

// The dock is the intel windows' launcher: a slim bar of buttons, one
// per window, the overlay, the site panel and the quick options. The
// master shortcut (Intel workbench) brings it up with the windows that
// were open last time, and puts them all away again.

// intelPrefs are the quick options, kept beside the WebView profile.
// Opacity rides in the pages' own storage; these are the ones Axon
// itself acts on.
type intelPrefsT struct {
	Locked    bool     `json:"locked"`     // windows neither move nor size
	FollowEve *bool    `json:"follow_eve"` // step aside while EVE is minimised; default on
	Open      []string `json:"open"`       // the windows open when last put away
}

var intelPrefs struct {
	mu     sync.Mutex
	p      intelPrefsT
	loaded bool
}

func intelPrefsPath() string { return filepath.Join(profileBase(), "intel-prefs.json") }

func loadIntelPrefs() intelPrefsT {
	intelPrefs.mu.Lock()
	defer intelPrefs.mu.Unlock()
	if !intelPrefs.loaded {
		intelPrefs.loaded = true
		if b, err := os.ReadFile(intelPrefsPath()); err == nil {
			_ = json.Unmarshal(b, &intelPrefs.p)
		}
	}
	return intelPrefs.p
}

func saveIntelPrefs(p intelPrefsT) {
	intelPrefs.mu.Lock()
	intelPrefs.p, intelPrefs.loaded = p, true
	intelPrefs.mu.Unlock()
	if b, err := json.Marshal(p); err == nil {
		_ = os.MkdirAll(filepath.Dir(intelPrefsPath()), 0o700)
		_ = os.WriteFile(intelPrefsPath(), b, 0o600)
	}
}

func intelLocked() bool    { return loadIntelPrefs().Locked }
func intelFollowEve() bool { f := loadIntelPrefs().FollowEve; return f == nil || *f }

// rememberOpen keeps which windows are open, for the master shortcut
// to bring back.
func (s *server) rememberOpen() {
	p := loadIntelPrefs()
	var open []string
	for _, m := range intelOpenModules() {
		if m != "dock" {
			open = append(open, m)
		}
	}
	if len(open) > 0 {
		p.Open = open
		saveIntelPrefs(p)
	}
}

// intelMaster is the master shortcut: everything away when any of it
// is showing, else the dock with the windows open last time (the
// threads and the map, the first time).
func (s *server) intelMaster() {
	if open := intelOpenModules(); len(open) > 0 {
		s.rememberOpen()
		for _, m := range open {
			hideIntelWindow(m)
		}
		return
	}
	s.openIntel("dock", 0, false)
	want := loadIntelPrefs().Open
	if len(want) == 0 {
		want = []string{"threads", "map"}
	}
	for _, m := range want {
		if validIntelModule(m) && m != "dock" {
			s.openIntel(m, 0, false)
		}
	}
}

func (s *server) intelPrefsAPI(r *http.Request) (any, error) {
	p := loadIntelPrefs()
	if r.Method == http.MethodPost {
		var in struct {
			Locked    *bool `json:"locked"`
			FollowEve *bool `json:"follow_eve"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			return nil, apiError{"send the options to change"}
		}
		if in.Locked != nil {
			p.Locked = *in.Locked
		}
		if in.FollowEve != nil {
			p.FollowEve = in.FollowEve
		}
		saveIntelPrefs(p)
	}
	return map[string]bool{"locked": p.Locked, "follow_eve": p.FollowEve == nil || *p.FollowEve}, nil
}

// intelKeysAPI is each dock button's shortcut, for its tooltip, in the
// canonical form ("" when unbound).
func (s *server) intelKeysAPI(*http.Request) (any, error) {
	out := map[string]string{}
	keys := s.overlayKeys()
	for a, name := range map[shortcutAction]string{actDashboard: "dock", actThreads: "threads", actMap: "map", actFeed: "feed", actSubmit: "send", actSite: "site"} {
		if c, err := parseChord(keys[a]); err == nil {
			out[name] = c.String()
		} else {
			out[name] = ""
		}
	}
	return out, nil
}

// intelActionAPI is the dock's other buttons: the overlay, the site
// panel, and Axon's settings window.
func (s *server) intelActionAPI(r *http.Request) (any, error) {
	var in struct {
		Do string `json:"do"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		return nil, apiError{"do what?"}
	}
	switch in.Do {
	case "send":
		showOverlayWindow()
	case "site":
		s.onShortcut(actSite)
	case "settings":
		go s.window.show()
	default:
		return nil, apiError{"send, site or settings"}
	}
	return struct{}{}, nil
}
