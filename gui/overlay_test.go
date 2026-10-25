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
// channels and records the lines posted to one of them.
type overlaySite struct {
	posted []string
}

func (f *overlaySite) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/intel/v1/channels":
			_, _ = w.Write([]byte(`{"account":"Burzrujat","channels":[{"id":9,"name":"Other","post":false},{"id":12,"name":"Delve Intel","post":true}]}`))
		case r.URL.Path == "/api/intel/v1/channels/12/lines":
			var body struct {
				Lines []string `json:"lines"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.posted = append(f.posted, body.Lines...)
			_, _ = w.Write([]byte(`{"posted":1,"duplicates":0,"skipped":0}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no"}`))
		}
	})
}

// overlayServer is a GUI server whose config is paired with the
// fake site (and, when grab is set, asks for the clipboard grab).
func overlayServer(t *testing.T, site *httptest.Server, grab bool) *server {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "axon.json")
	cfg := watch.Config{Server: site.URL, Token: "esw_test", Grab: grab, Logs: filepath.Join(dir, "Chatlogs")}
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
	s := overlayServer(t, site, false)

	account, list, err := s.overlayChannels()
	if err != nil {
		t.Fatal(err)
	}
	if account != "Burzrujat" || len(list) != 1 || list[0].ID != 12 {
		t.Fatalf("account %q, channels %+v: want the one postable channel", account, list)
	}

	say, err := s.overlayPost(12, "Aaranen Hilanen\nAhdah\n")
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
	if _, err := s.overlayPost(9, "hello"); err == nil {
		t.Error("posted to a channel the account cannot post to")
	}
	if _, err := s.overlayPost(12, "  \n"); err == nil {
		t.Error("posted an empty paste")
	}
	if len(fake.posted) != 2 {
		t.Errorf("posted %q after the refusals", fake.posted)
	}
}

func TestOverlayGrabGate(t *testing.T) {
	fake := &overlaySite{}
	site := httptest.NewServer(fake.handler())
	defer site.Close()
	if s := overlayServer(t, site, false); s.overlayGrabEnabled() {
		t.Error("grab on with no flag in the config")
	}
	if s := overlayServer(t, site, true); !s.overlayGrabEnabled() {
		t.Error("grab off with the flag set")
	}
	// No config at all: off, never an error the window must handle.
	s, _ := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	if s.overlayGrabEnabled() {
		t.Error("grab on with no config")
	}
}

func TestOverlayEndpoint(t *testing.T) {
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	if res, _ := call(t, ts, "POST", "/api/overlay", "", map[string]any{}); res.StatusCode != http.StatusForbidden {
		t.Errorf("no key: status %d, want 403", res.StatusCode)
	}
	// On this platform there is no overlay window: the button is
	// answered plainly, not with silence.
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
}
