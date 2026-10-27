package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/natemsz/axon/watch"
)

// overlaySite is a stand-in EveSynapse: it lists the account's
// channels, answers previews, and records what is posted.
type overlaySite struct {
	posted  []string
	kind    string
	system  int64
	channel string
}

func (f *overlaySite) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/intel/v1/channels":
			_, _ = w.Write([]byte(`{"account":"Burzrujat","channels":[{"id":9,"name":"Other","post":false},{"id":12,"name":"Delve Intel","post":true},{"id":14,"name":"Catch Intel","post":true}]}`))
		case r.URL.Path == "/api/intel/v1/preview":
			_, _ = w.Write([]byte(`{"ok":true,"kind":"dscan","system_id":30002385,"system_name":"Teonusude","system_from":"scan"}`))
		case strings.HasPrefix(r.URL.Path, "/api/intel/v1/channels/") && strings.HasSuffix(r.URL.Path, "/lines"):
			var body struct {
				Lines  []string `json:"lines"`
				Kind   string   `json:"kind"`
				System int64    `json:"system"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.posted = append(f.posted, body.Lines...)
			f.kind, f.system, f.channel = body.Kind, body.System, r.URL.Path
			_, _ = w.Write([]byte(`{"posted":1,"duplicates":0,"skipped":0}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no"}`))
		}
	})
}

// overlayServer is a GUI server whose config is paired with the
// fake site.
func overlayServer(t *testing.T, site *httptest.Server, cfg watch.Config) *server {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "axon.json")
	cfg.Server, cfg.Token, cfg.Logs = site.URL, "esw_test", filepath.Join(dir, "Chatlogs")
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := watch.LoadOrCreateKey(watch.KeyPath(path)); err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServer(t, path)
	return s
}

func TestOverlayChannelsAndPost(t *testing.T) {
	fake := &overlaySite{}
	site := httptest.NewServer(fake.handler())
	defer site.Close()
	s := overlayServer(t, site, watch.Config{})

	account, list, err := s.overlayChannels()
	if err != nil {
		t.Fatal(err)
	}
	if account != "Burzrujat" || len(list) != 2 {
		t.Fatalf("account %q, channels %+v: want the postable channels", account, list)
	}
	say, err := s.overlayPost(12, "Aaranen Hilanen\nAhdah\n", watch.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(say, "Posted to Delve Intel") || !strings.Contains(say, "as Burzrujat (account character)") {
		t.Errorf("the window is told %q: want the outcome and who it was posted as", say)
	}
	if len(fake.posted) != 2 || fake.posted[0] != "Aaranen Hilanen" || fake.posted[1] != "Ahdah" {
		t.Errorf("posted %q, want the paste's own lines", fake.posted)
	}
	// A channel that cannot be posted to, and an empty paste, are
	// refused with the reason, and nothing more is sent.
	if _, err := s.overlayPost(9, "hello", watch.Overrides{}); err == nil {
		t.Error("posted to a channel the account cannot post to")
	}
	if _, err := s.overlayPost(12, "  \n", watch.Overrides{}); err == nil {
		t.Error("posted an empty paste")
	}
	if len(fake.posted) != 2 {
		t.Errorf("posted %q after the refusals", fake.posted)
	}
}

// A send goes to the config's overlay channel with the overrides the
// player set; the preview reads the kind here and asks the site for
// the system.
func TestOverlaySendAndPreview(t *testing.T) {
	fake := &overlaySite{}
	site := httptest.NewServer(fake.handler())
	defer site.Close()
	s := overlayServer(t, site, watch.Config{OverlayChannel: 14})

	if _, err := s.overlaySend("1DQ1-A Hostile One +2", watch.Overrides{Kind: "chat", System: 30004759}); err != nil {
		t.Fatal(err)
	}
	if fake.channel != "/api/intel/v1/channels/14/lines" || fake.kind != "chat" || fake.system != 30004759 {
		t.Errorf("sent to %s with kind %q system %d", fake.channel, fake.kind, fake.system)
	}
	pv := s.overlayPreviewOf("22456\tAlpha Sabre\tSabre\t1,200 km\n587\tRifter\tRifter\t5 AU\n")
	if pv.Kind != watch.KindDScan || pv.SystemName != "Teonusude" || pv.SystemFrom != "scan" {
		t.Errorf("preview %+v", pv)
	}
	if pv := s.overlayPreviewOf("  "); pv.Kind != "" || pv.SystemID != 0 {
		t.Errorf("an empty field previews as %+v", pv)
	}
	if !overlaySupported {
		// No clipboard here: an empty send says so plainly.
		if _, err := s.overlaySend("", watch.Overrides{}); err == nil || !strings.Contains(err.Error(), "clipboard") {
			t.Errorf("empty send without a clipboard: %v", err)
		}
	}
}

func TestOverlayKeysDefault(t *testing.T) {
	fake := &overlaySite{}
	site := httptest.NewServer(fake.handler())
	defer site.Close()
	k := overlayServer(t, site, watch.Config{}).overlayKeys()
	if k[actSubmit] != watch.DefaultShortcuts.Submit || k[actDashboard] != watch.DefaultShortcuts.Dashboard {
		t.Errorf("keys %v, want the defaults", k)
	}
	// No config at all: nothing to bind, never an error.
	s, _ := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	if s.overlayKeys() != nil {
		t.Error("keys with no config")
	}
}

func TestOverlayEndpoint(t *testing.T) {
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	if res, _ := call(t, ts, "POST", "/api/overlay", "", map[string]any{}); res.StatusCode != http.StatusForbidden {
		t.Errorf("no key: status %d, want 403", res.StatusCode)
	}
	res, out := call(t, ts, "POST", "/api/overlay", "k3y", map[string]any{})
	if overlaySupported {
		if res.StatusCode != http.StatusOK {
			t.Errorf("overlay here: %d %v", res.StatusCode, out)
		}
	} else if res.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(string), "Windows") {
		t.Errorf("overlay here: %d %v, want a plain no", res.StatusCode, out)
	}
	if _, st := call(t, ts, "GET", "/api/state", "k3y", nil); st["overlay"] != overlaySupported {
		t.Errorf("state overlay %v, want %v", st["overlay"], overlaySupported)
	}
	if res, _ := call(t, ts, "GET", "/api/overlay/state", "", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("overlay state with no key: %d, want 403", res.StatusCode)
	}
}
