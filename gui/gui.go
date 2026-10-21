// Package gui is axon's settings window: a page served on this
// computer only and opened in the browser. On it a player picks or
// imports a config, fills in the site and token, checks them, maps
// in-game channels to site channels, and starts and stops the watcher,
// whose log the page shows as it runs.
//
// The page is the only way in. The server listens on 127.0.0.1, answers
// only requests addressed to that host and port (so a web page cannot
// reach it by rebinding a name of its own to this computer), and every
// API call must carry the key the browser was opened with, in a header
// no other site's page can set without the server agreeing.
package gui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/natemsz/axon/watch"
)

//go:embed page
var page embed.FS

// Options are what the program was started with.
type Options struct {
	ConfigPath string // the config to open first
	Logs       string // -logs, which wins over the config's
	Replay     bool
	Version    string
	// Tray starts in the tray (where there is one): watching at once
	// with the config, the page not opened until asked for.
	Tray bool
	// Open opens the page in the browser; nil uses the system's.
	Open func(url string) error
}

// trayActions are what the tray icon's menu does.
type trayActions struct {
	open, toggle, quit func()
	running            func() bool
}

// ListenAddr is the settings page's fixed address on this computer.
// It never changes, so the EveSynapse site (and shortcuts) can link
// to it and tell whether Axon is running: see /api/ping and /open.
const ListenAddr = "127.0.0.1:47611"

// AppName is how Axon names itself to the site and the player.
const AppName = "EveSynapse Axon"

// maxLog is how many log lines the page can scroll back through.
const maxLog = 500

type line struct {
	N    int    `json:"n"`
	At   string `json:"at"`
	Text string `json:"text"`
}

type server struct {
	opts Options
	key  string
	host string
	quit context.CancelFunc

	mu      sync.Mutex
	path    string
	cancel  context.CancelFunc
	done    chan struct{}
	running string // the config path being watched, "" when stopped
	failed  string // why the last run stopped on its own
	log     []line
	seq     int
}

// openRunning opens the page of the Axon already listening on
// ListenAddr, reporting whether there was one.
func openRunning(open func(string) error) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get("http://" + ListenAddr + "/api/ping")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var body struct {
		App string `json:"app"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.App != AppName {
		return false
	}
	if open == nil {
		open = OpenBrowser
	}
	return open("http://"+ListenAddr+"/open") == nil
}

// Run serves the settings page until ctx ends or the page says quit,
// stopping the watcher if it runs.
func Run(ctx context.Context, opts Options) error {
	ln, err := net.Listen("tcp", ListenAddr)
	if err != nil {
		// The fixed address is taken: very likely Axon already
		// running. Open its page instead of starting a second copy.
		if openRunning(opts.Open) {
			return nil
		}
		return fmt.Errorf("cannot listen on %s: %w", ListenAddr, err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	ctx, quit := context.WithCancel(ctx)
	defer quit()
	s := &server{opts: opts, key: hex.EncodeToString(b), host: ln.Addr().String(), quit: quit, path: opts.ConfigPath}
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		s.stop()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	url := "http://" + s.host + "/?k=" + s.key
	open := func() {
		o := opts.Open
		if o == nil {
			o = OpenBrowser
		}
		if err := o(url); err != nil {
			log.Printf("could not open the browser (%v): open %s yourself", err, url)
		}
	}
	ready := make(chan struct{})
	go runTray(ctx, trayActions{
		open: open,
		toggle: func() {
			if s.status().Running != "" {
				s.stop()
			} else if _, err := s.begin(opts.Replay); err != nil {
				s.logf("could not start: %v", err)
				trayNote("Could not start: " + err.Error())
			}
		},
		quit:    quit,
		running: func() bool { return s.status().Running != "" },
	}, ready)
	<-ready
	log.Printf("axon %s: settings at %s", opts.Version, url)
	log.Printf("press Ctrl+C here, or Quit on the page, to stop")
	if opts.Tray && traySupported {
		showConsole(false)
		if _, err := s.begin(opts.Replay); err != nil {
			s.logf("could not start: %v", err)
			trayNote("Could not start watching: " + err.Error() + ". Double-click to open the settings.")
			open()
		} else {
			trayNote("Watching your intel channels. Right-click here for the menu.")
		}
	} else {
		open()
	}
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *server) routes() http.Handler {
	files, _ := fs.Sub(page, "page")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(files))
	icon := iconPNG(64)
	mux.HandleFunc("GET /icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(icon)
	})
	// /open and /api/ping need no key: /open only redirects this
	// browser to the page with its key (changing nothing), and
	// /ping only says Axon is running, so the EveSynapse site can
	// show its link to this page. Everything else stays keyed.
	mux.HandleFunc("GET /open", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/?k="+s.key, http.StatusFound)
	})
	mux.HandleFunc("GET /api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"app": AppName, "version": s.opts.Version})
	})
	api := map[string]func(*http.Request) (any, error){
		"GET /api/state":   s.state,
		"GET /api/log":     s.logSince,
		"POST /api/open":   s.open,
		"POST /api/import": s.importJSON,
		"POST /api/save":   s.save,
		"POST /api/test":   s.test,
		"POST /api/logs":   s.logs,
		"POST /api/browse": s.browse,
		"POST /api/start":  s.start,
		"POST /api/stop":   func(*http.Request) (any, error) { s.stop(); return s.status(), nil },
		"POST /api/quit":   func(*http.Request) (any, error) { s.quit(); return struct{}{}, nil },
		"POST /api/tray":   s.toTray,
	}
	for pattern, h := range api {
		mux.HandleFunc(pattern, s.guard(h))
	}
	return s.checkHost(mux)
}

// checkHost turns away requests addressed to any other name, which a
// page elsewhere could aim at this port.
func (s *server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.host {
			http.Error(w, "wrong host", http.StatusMisdirectedRequest)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// apiError is a refusal the page shows as is.
type apiError struct{ msg string }

func (e apiError) Error() string { return e.msg }

func (s *server) guard(h func(*http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Axon-Key")), []byte(s.key)) != 1 {
			http.Error(w, "no key", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		out, err := h(r)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			status := http.StatusInternalServerError
			var ae apiError
			if errors.As(err, &ae) {
				status = http.StatusBadRequest
			}
			w.WriteHeader(status)
			out = map[string]string{"error": err.Error()}
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return apiError{"bad request: " + err.Error()}
	}
	return nil
}

func (s *server) logf(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	log.Print(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.log = append(s.log, line{N: s.seq, At: time.Now().Format("15:04:05"), Text: text})
	if over := len(s.log) - maxLog; over > 0 {
		s.log = s.log[over:]
	}
}

type status struct {
	Running string `json:"running"`
	Failed  string `json:"failed"`
}

func (s *server) status() status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return status{Running: s.running, Failed: s.failed}
}

// loaded is a config as the page edits it: where it lives, what it
// says, and what is wrong with it.
type loaded struct {
	Path    string       `json:"path"`
	Exists  bool         `json:"exists"`
	Config  watch.Config `json:"config"`
	Problem string       `json:"problem,omitempty"`
}

func load(path string) loaded {
	l := loaded{Path: path, Config: watch.Template}
	c, err := watch.LoadConfig(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		l.Problem = "no config here yet: fill this in and save it"
	case err != nil && c.Server == "" && c.Token == "":
		l.Exists, l.Problem = true, err.Error()
	default:
		l.Exists, l.Config = true, c
		if err != nil {
			l.Problem = err.Error()
		}
	}
	if l.Config.Channels == nil {
		l.Config.Channels = map[string]int64{}
	}
	return l
}

func (s *server) state(*http.Request) (any, error) {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	abs, _ := filepath.Abs(path)
	return struct {
		Version string `json:"version"`
		Replay  bool   `json:"replay"`
		LogsArg string `json:"logsArg"`
		Tray    bool   `json:"tray"`
		loaded
		status
	}{s.opts.Version, s.opts.Replay, s.opts.Logs, traySupported, load(abs), s.status()}, nil
}

// toTray hides the console window (when it is the program's own) and
// leaves the icon; the page says the tab can be closed.
func (s *server) toTray(*http.Request) (any, error) {
	if !traySupported {
		return nil, apiError{"there is no tray icon on this system"}
	}
	hidden := showConsole(false)
	trayNote("Axon is still running. Double-click here to open its settings.")
	return map[string]bool{"hidden": hidden}, nil
}

func (s *server) open(r *http.Request) (any, error) {
	var in struct{ Path string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Path) == "" {
		return nil, apiError{"give the config file's path"}
	}
	abs, err := filepath.Abs(strings.TrimSpace(in.Path))
	if err != nil {
		return nil, apiError{err.Error()}
	}
	s.mu.Lock()
	s.path = abs
	s.mu.Unlock()
	return load(abs), nil
}

// importJSON reads a config's text (a file the page was handed, or
// what the site showed), for the form; nothing is saved.
func (s *server) importJSON(r *http.Request) (any, error) {
	var in struct{ Text string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	var c watch.Config
	if err := json.Unmarshal([]byte(in.Text), &c); err != nil {
		return nil, apiError{"that is not an axon.json: " + err.Error()}
	}
	if c.Channels == nil {
		c.Channels = map[string]int64{}
	}
	out := loaded{Config: c}
	if err := c.Check(); err != nil {
		out.Problem = err.Error()
	}
	return out, nil
}

func (s *server) save(r *http.Request) (any, error) {
	var in loaded
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	c := in.Config
	c.Server, c.Token, c.Logs = strings.TrimSpace(c.Server), strings.TrimSpace(c.Token), strings.TrimSpace(c.Logs)
	if err := c.Check(); err != nil {
		return nil, apiError{err.Error()}
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return nil, apiError{"choose where to save the config"}
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, apiError{err.Error()}
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return nil, apiError{"could not save: " + err.Error()}
	}
	s.mu.Lock()
	s.path = path
	s.mu.Unlock()
	s.logf("saved %s", path)
	return load(path), nil
}

// test signs in with the form's site and token and lists the
// account's channels.
func (s *server) test(r *http.Request) (any, error) {
	var in watch.Config
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	in.Server, in.Token = strings.TrimSpace(in.Server), strings.TrimSpace(in.Token)
	if err := in.Check(); err != nil {
		return nil, apiError{err.Error()}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	c := &watch.Client{Server: in.Server, Token: in.Token, Agent: "axon/" + s.opts.Version}
	account, list, err := c.Channels(ctx)
	if err != nil {
		return nil, apiError{err.Error()}
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	return map[string]any{"account": account, "channels": list}, nil
}

// logs settles the chat log folder (the one given, else the one
// found) and lists the channels its logs of the past week are from,
// to choose among when mapping.
func (s *server) logs(r *http.Request) (any, error) {
	var in struct{ Dir string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	candidates := watch.DefaultLogDirs()
	dir := strings.TrimSpace(in.Dir)
	found := false
	if dir == "" {
		dir, found = watch.FindLogDir(candidates), true
	}
	out := map[string]any{"dir": dir, "found": found, "candidates": candidates, "channels": []string{}}
	if dir == "" {
		return out, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, apiError{"cannot read " + dir + ": " + err.Error()}
	}
	since := time.Now().AddDate(0, 0, -7)
	seen := map[string]string{}
	for _, e := range entries {
		name, ok := watch.ChannelOf(e.Name())
		if !ok || e.IsDir() {
			continue
		}
		if info, err := e.Info(); err != nil || info.ModTime().Before(since) {
			continue
		}
		seen[strings.ToLower(name)] = name
	}
	names := make([]string, 0, len(seen))
	for _, n := range seen {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	out["channels"] = names
	return out, nil
}

func (s *server) browse(r *http.Request) (any, error) {
	var in struct{ Kind, From string }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	var path string
	var err error
	switch in.Kind {
	case "open":
		path, err = pick(r.Context(), pickOpen, in.From)
	case "save":
		path, err = pick(r.Context(), pickSave, in.From)
	case "folder":
		path, err = pick(r.Context(), pickFolder, in.From)
	default:
		return nil, apiError{"unknown kind " + strconv.Quote(in.Kind)}
	}
	if err != nil {
		return nil, apiError{err.Error()}
	}
	return map[string]string{"path": path}, nil
}

// start runs the watcher on the saved config, as the command line
// would.
func (s *server) start(r *http.Request) (any, error) {
	var in struct{ Replay bool }
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	return s.begin(in.Replay)
}

func (s *server) begin(replay bool) (any, error) {
	s.mu.Lock()
	path, busy := s.path, s.running != ""
	s.mu.Unlock()
	if busy {
		return nil, apiError{"already watching"}
	}
	path, _ = filepath.Abs(path)
	cfg, err := watch.LoadConfig(path)
	if err != nil {
		return nil, apiError{"save a working config first: " + err.Error()}
	}
	dir := s.opts.Logs
	if dir == "" {
		dir = cfg.Logs
	}
	if dir == "" {
		if dir = watch.FindLogDir(watch.DefaultLogDirs()); dir == "" {
			return nil, apiError{"could not find EVE's chat logs: choose the Chatlogs folder"}
		}
	}
	w := &watch.Watcher{
		Dir:    dir,
		Client: &watch.Client{Server: cfg.Server, Token: cfg.Token, Agent: "axon/" + s.opts.Version},
		Map:    cfg.Channels,
		Replay: replay,
		Logf:   s.logf,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.mu.Lock()
	s.cancel, s.done, s.running, s.failed = cancel, done, path, ""
	s.mu.Unlock()
	s.logf("watching with %s", path)
	go func() {
		defer close(done)
		err := w.Run(ctx)
		s.mu.Lock()
		s.running, s.cancel = "", nil
		if err != nil {
			s.failed = err.Error()
		}
		s.mu.Unlock()
		if err != nil {
			s.logf("stopped: %v", err)
			trayNote("Stopped watching: " + err.Error())
		} else {
			s.logf("stopped")
		}
	}()
	return s.status(), nil
}

// stop ends the watcher, if it runs, once it has sent what it holds.
func (s *server) stop() {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

func (s *server) logSince(r *http.Request) (any, error) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []line{}
	for _, l := range s.log {
		if l.N > after {
			out = append(out, l)
		}
	}
	return map[string]any{"lines": out, "running": s.running, "failed": s.failed}, nil
}
