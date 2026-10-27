package gui

import (
	"context"
	"encoding/json"
	"net/http"
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
var intelModules = []string{"threads", "map", "feed", "report"}

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
// opens its report, a report's system moves the map.
func (s *server) intelOpenAPI(r *http.Request) (any, error) {
	var in struct {
		Module string `json:"module"`
		Report int64  `json:"report"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validIntelModule(in.Module) {
		return nil, apiError{"open which window?"}
	}
	if !s.openIntel(in.Module, in.Report, false) {
		return nil, apiError{"the intel windows are part of Axon on Windows for now"}
	}
	return struct{}{}, nil
}

// intelWindowAPI is a page's own frame: drag it, or put it away.
func (s *server) intelWindowAPI(r *http.Request) (any, error) {
	var in struct {
		Module string `json:"module"`
		Cmd    string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validIntelModule(in.Module) {
		return nil, apiError{"which window?"}
	}
	switch in.Cmd {
	case "drag":
		intelWindowDo(in.Module, "drag")
	case "hide":
		intelWindowDo(in.Module, "hide")
	default:
		return nil, apiError{"drag or hide"}
	}
	return struct{}{}, nil
}
