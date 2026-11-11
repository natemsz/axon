package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/natemsz/axon/internal/update"
)

// latestJSON is a newest-release answer naming tag and carrying
// the three assets a check needs to see.
func latestJSON(tag string) []byte {
	asset := func(name string) map[string]string {
		return map[string]string{"name": name, "browser_download_url": "http://127.0.0.1/" + name}
	}
	body, _ := json.Marshal(map[string]any{
		"tag_name": tag,
		"assets": []map[string]string{
			asset("SHA256SUMS"),
			asset("SHA256SUMS.sig"),
			asset(update.AssetName(runtime.GOOS, runtime.GOARCH)),
		},
	})
	return body
}

func loggedUpdateCheck(s *server) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.log {
		if strings.HasPrefix(l.Text, "update check:") {
			return true
		}
	}
	return false
}

// The check fires after the window is up and must not hold
// anything up: starting it returns at once even when the answer
// is slow, nothing is offered while it is still out, and the
// update appears (in what the page's state and log poll report)
// once the answer lands.
func TestUpdateCheckRunsInBackground(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := sync.OnceFunc(func() { close(release) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(arrived) })
		<-release
		_, _ = w.Write(latestJSON("v0.4.5"))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(unblock) // LIFO: frees the handler before srv.Close

	s := &server{opts: Options{Version: "v0.4.4", Updater: &update.Checker{Latest: srv.URL}}}
	start := time.Now()
	s.checkForUpdate(context.Background())
	if d := time.Since(start); d > time.Second {
		t.Fatalf("starting the check blocked for %v", d)
	}
	<-arrived // the check is out on the network
	if v := s.updateVersion(); v != "" {
		t.Fatalf("update %q offered before the check answered", v)
	}
	unblock()
	deadline := time.Now().Add(2 * time.Second)
	for s.updateVersion() != "v0.4.5" {
		if time.Now().After(deadline) {
			t.Fatalf("the update never appeared; offered %q", s.updateVersion())
		}
		time.Sleep(5 * time.Millisecond)
	}
	out, err := s.logSince(httptest.NewRequest(http.MethodGet, "/api/log?after=0", nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(map[string]any)["update"]; got != "v0.4.5" {
		t.Errorf("log poll update = %v, want v0.4.5", got)
	}
}

// A check that fails is logged and forgotten: no update is
// offered and nothing is left waiting.
func TestUpdateCheckFailureForgotten(t *testing.T) {
	hit := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(hit) })
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	s := &server{opts: Options{Version: "v0.4.4", Updater: &update.Checker{Latest: srv.URL}}}
	s.checkForUpdate(context.Background())
	<-hit
	deadline := time.Now().Add(2 * time.Second)
	for !loggedUpdateCheck(s) {
		if time.Now().After(deadline) {
			t.Fatal("the failed check was never logged")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rel := s.foundUpdate(); rel != nil {
		t.Errorf("failed check left an update: %v", rel.Version)
	}
	if v := s.updateVersion(); v != "" {
		t.Errorf("failed check offered %q", v)
	}
}
