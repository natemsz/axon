package watch

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A game log is held until it goes quiet, then sent gzipped with its
// session and listener; a stretch with no combat line is never sent,
// and a refusal that says when to come back holds the text until
// then.
func TestGameLogSentWhenQuiet(t *testing.T) {
	var got []string
	var queries []string
	status := http.StatusTooManyRequests
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		if status != http.StatusOK {
			w.Header().Set("Retry-After", "90")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"12 uploads an hour at most"}`))
			return
		}
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Errorf("body is not gzip: %v", err)
			return
		}
		b, _ := io.ReadAll(zr)
		got = append(got, string(b))
		_, _ = w.Write([]byte(`{"queued":true,"ahead":0}`))
	}))
	defer srv.Close()
	client := &Client{Server: srv.URL, Token: "t", HTTP: srv.Client()}

	dir := t.TempDir()
	path := filepath.Join(dir, "20261104_120000_90000001.txt")
	write := func(lines ...string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(strings.Join(lines, "\r\n") + "\r\n")
		_ = f.Close()
	}
	write("------------------------------------------------------------",
		"  Gamelog",
		"  Listener: Iwaira Itovuod",
		"------------------------------------------------------------",
		"[ 2026.11.04 12:00:01 ] (notify) Undocking from station.")
	c := newGameFollower(dir, true, func(string, ...any) {})
	t0 := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	c.scan(t0)
	c.scan(t0.Add(3 * time.Minute))
	if len(c.pending) != 0 {
		t.Fatalf("a stretch with no combat was queued: %d", len(c.pending))
	}
	fight := "[ 2026.11.04 12:03:01 ] (combat) <b>120</b> from <b>Bad Guy</b>[EVIL](Vexor) - Hammerhead II - Smashes"
	write(fight)
	c.scan(t0.Add(4 * time.Minute))
	if len(c.pending) != 0 {
		t.Fatal("text was queued before the log went quiet")
	}
	c.scan(t0.Add(4*time.Minute + SendQuiet))
	if len(c.pending) != 1 {
		t.Fatalf("want one post waiting, got %d", len(c.pending))
	}

	now := t0.Add(7 * time.Minute)
	if unreachable, _ := c.send(context.Background(), client, now); unreachable || len(c.pending) != 1 {
		t.Fatalf("a 429 dropped the text or read as unreachable: %v, %d waiting", unreachable, len(c.pending))
	}
	status = http.StatusOK
	c.send(context.Background(), client, now.Add(time.Minute))
	if len(queries) != 1 {
		t.Fatalf("sent again before Retry-After: %d posts", len(queries))
	}
	c.send(context.Background(), client, now.Add(2*time.Minute))
	if len(got) != 1 || len(c.pending) != 0 {
		t.Fatalf("want the text sent once Retry-After passed: %d sent, %d waiting", len(got), len(c.pending))
	}
	if !strings.Contains(queries[1], "/api/intel/v1/gamelog?") || !strings.Contains(queries[1], "listener=Iwaira+Itovuod") || !strings.Contains(queries[1], "session=20261104_120000_90000001.txt") {
		t.Errorf("query: %s", queries[1])
	}
	if !bytes.Contains([]byte(got[0]), []byte("Bad Guy")) || strings.Contains(got[0], "Listener:") {
		t.Errorf("text: %q", got[0])
	}
}
