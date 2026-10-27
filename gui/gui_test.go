package gui

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/natemsz/axon/watch"
)

func newTestServer(t *testing.T, path string) (*server, *httptest.Server) {
	t.Helper()
	s := &server{key: "k3y", path: path, quit: func() {}, window: &appWindow{fallback: func(string) error { return nil }}}
	ts := httptest.NewServer(nil)
	s.host = strings.TrimPrefix(ts.URL, "http://")
	ts.Config.Handler = s.routes()
	t.Cleanup(ts.Close)
	return s, ts
}

func call(t *testing.T, ts *httptest.Server, method, path, key string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, ts.URL+path, r)
	if key != "" {
		req.Header.Set("X-Axon-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func TestKeyRequired(t *testing.T) {
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	for _, key := range []string{"", "wrong"} {
		if res, _ := call(t, ts, "GET", "/api/state", key, nil); res.StatusCode != http.StatusForbidden {
			t.Errorf("key %q: status %d, want 403", key, res.StatusCode)
		}
	}
	if res, _ := call(t, ts, "GET", "/api/state", "k3y", nil); res.StatusCode != http.StatusOK {
		t.Errorf("right key: status %d", res.StatusCode)
	}
	// The page itself needs no key: it holds nothing.
	if res, _ := call(t, ts, "GET", "/", "", nil); res.StatusCode != http.StatusOK {
		t.Errorf("page: status %d", res.StatusCode)
	}
}

func TestPingAndOpenNeedNoKey(t *testing.T) {
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	// The site probes /api/ping to show its Axon link; no key, and
	// any origin may read the answer.
	res, out := call(t, ts, "GET", "/api/ping", "", nil)
	if res.StatusCode != http.StatusOK || out["app"] != AppName {
		t.Errorf("ping: %d %v", res.StatusCode, out)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("ping CORS: %q", res.Header.Get("Access-Control-Allow-Origin"))
	}
	// /open only redirects this browser to the keyed page.
	req, _ := http.NewRequest("GET", ts.URL+"/open", nil)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusFound || res2.Header.Get("Location") != "/?k=k3y" {
		t.Errorf("open: %d %q", res2.StatusCode, res2.Header.Get("Location"))
	}
	if ListenAddr != "127.0.0.1:47611" {
		t.Errorf("ListenAddr %q, want the fixed address the site links to", ListenAddr)
	}
}

func TestPairSavesCredentialAndKey(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/intel/v1/pair" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var in struct {
			Code      string `json:"code"`
			PublicKey string `json:"public_key"`
			Device    string `json:"device"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		pub, err := base64.StdEncoding.DecodeString(in.PublicKey)
		if in.Code != "K7QX-4MZD" || err != nil || len(pub) != ed25519.PublicKeySize || in.Device != "Test PC" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad pairing"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"token":"esw_dev","account":"Pilot"}`))
	}))
	defer site.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "axon.json")
	_, ts := newTestServer(t, path)
	res, out := call(t, ts, "POST", "/api/pair", "k3y", map[string]string{
		"Server": site.URL, "Code": "K7QX-4MZD", "Device": "Test PC",
	})
	if res.StatusCode != http.StatusOK || out["account"] != "Pilot" {
		t.Fatalf("pair: %d %v", res.StatusCode, out)
	}
	cfg, err := watch.LoadConfig(path)
	if err != nil || cfg.Server != site.URL || cfg.Token != "esw_dev" {
		t.Errorf("saved config %+v, %v", cfg, err)
	}
	if _, err := watch.LoadKey(watch.KeyPath(path)); err != nil {
		t.Errorf("device key beside the config: %v", err)
	}
	// A wrong code is shown as the site said it.
	if res, out := call(t, ts, "POST", "/api/pair", "k3y", map[string]string{
		"Server": site.URL, "Code": "XXXX-XXXX", "Device": "Test PC",
	}); res.StatusCode != http.StatusInternalServerError && res.StatusCode != http.StatusBadRequest {
		// watch.Pair returns APIError, which the GUI shows plainly.
		t.Errorf("wrong code: %d %v", res.StatusCode, out)
	}
}

func TestOtherHostRefused(t *testing.T) {
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	req, _ := http.NewRequest("GET", ts.URL+"/api/state", nil)
	req.Host = "evil.example:80"
	req.Header.Set("X-Axon-Key", "k3y")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("status %d, want 421", res.StatusCode)
	}
}

func TestSaveAndOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "axon.json")
	_, ts := newTestServer(t, path)

	_, out := call(t, ts, "GET", "/api/state", "k3y", nil)
	if out["exists"] != false || out["problem"] == "" {
		t.Errorf("state before saving: %v", out)
	}

	bad := watch.Config{Server: "https://", Token: "x"}
	if res, out := call(t, ts, "POST", "/api/save", "k3y", loaded{Path: path, Config: bad}); res.StatusCode != http.StatusBadRequest || out["error"] == nil {
		t.Errorf("saving an unfilled config: %d %v", res.StatusCode, out)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("an unfilled config was written")
	}

	good := watch.Config{Server: " https://es.example ", Token: "esw_abc", Channels: map[string]int64{"Delve.Intel": 12}}
	if res, out := call(t, ts, "POST", "/api/save", "k3y", loaded{Path: path, Config: good}); res.StatusCode != http.StatusOK {
		t.Fatalf("save: %d %v", res.StatusCode, out)
	}
	c, err := watch.LoadConfig(path)
	if err != nil || c.Server != "https://es.example" || c.Channels["Delve.Intel"] != 12 {
		t.Errorf("saved %+v, %v", c, err)
	}

	// A save from a page that sends no shortcuts keeps the file's.
	if err := os.WriteFile(path, []byte("{\"server\":\"https://es.example\",\"token\":\"esw_abc\",\"shortcuts\":{\"submit\":\"Alt+F2\"}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res, out := call(t, ts, "POST", "/api/save", "k3y", loaded{Path: path, Config: watch.Config{Server: "https://es.example", Token: "esw_abc"}}); res.StatusCode != http.StatusOK {
		t.Fatalf("save over a config with shortcuts set: %d %v", res.StatusCode, out)
	}
	c, err = watch.LoadConfig(path)
	if err != nil || c.Keys().Submit != "Alt+F2" {
		t.Errorf("a page save dropped the file's shortcuts: %+v, %v", c, err)
	}

	if st, _ := os.Stat(path); st != nil && st.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("config is readable by others: %v", st.Mode())
	}

	_, out = call(t, ts, "POST", "/api/open", "k3y", map[string]string{"path": path})
	cfg := out["config"].(map[string]any)
	if out["exists"] != true || cfg["token"] != "esw_abc" {
		t.Errorf("open: %v", out)
	}
}

func TestImport(t *testing.T) {
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	res, out := call(t, ts, "POST", "/api/import", "k3y", map[string]string{"text": `{"server":"https://es.example","token":"esw_x"}`})
	if res.StatusCode != http.StatusOK || out["problem"] != nil {
		t.Errorf("import: %d %v", res.StatusCode, out)
	}
	if res, _ := call(t, ts, "POST", "/api/import", "k3y", map[string]string{"text": "not json"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("import of junk: %d", res.StatusCode)
	}
}

func TestTestSignsIn(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer esw_ok" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":2,"name":"b","post":true},{"id":1,"name":"A","post":false}]}`))
	}))
	defer site.Close()
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	res, out := call(t, ts, "POST", "/api/test", "k3y", watch.Config{Server: site.URL, Token: "esw_ok"})
	if res.StatusCode != http.StatusOK || out["account"] != "Pilot" {
		t.Fatalf("test: %d %v", res.StatusCode, out)
	}
	if list := out["channels"].([]any); list[0].(map[string]any)["name"] != "A" {
		t.Errorf("channels not sorted by name: %v", list)
	}
	res, out = call(t, ts, "POST", "/api/test", "k3y", watch.Config{Server: site.URL, Token: "esw_no"})
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(string), "bad token") {
		t.Errorf("bad token: %d %v", res.StatusCode, out)
	}
}

func TestLogsListsChannels(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Delve Intel_20261021_112005_90000001.txt", "delve intel_20261021_120000_90000002.txt", "Local_20261021_112005.txt", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	_, out := call(t, ts, "POST", "/api/logs", "k3y", map[string]string{"dir": dir})
	got := out["channels"].([]any)
	if len(got) != 2 || !strings.EqualFold(got[0].(string), "delve intel") || got[1] != "Local" {
		t.Errorf("channels %v", got)
	}
}

func TestStartNeedsAConfig(t *testing.T) {
	s, ts := newTestServer(t, filepath.Join(t.TempDir(), "axon.json"))
	res, out := call(t, ts, "POST", "/api/start", "k3y", map[string]bool{"replay": false})
	if res.StatusCode != http.StatusBadRequest || !strings.Contains(out["error"].(string), "save a working config") {
		t.Errorf("start with no config: %d %v", res.StatusCode, out)
	}
	if s.status().Running != "" {
		t.Error("running with no config")
	}
	s.stop() // nothing running: must not block
}
