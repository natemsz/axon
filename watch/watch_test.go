package watch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// utf16le encodes s as the client writes its logs.
func utf16le(s string, withBOM bool) []byte {
	var b []byte
	if withBOM {
		b = append(b, 0xFF, 0xFE)
	}
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b
}

func TestChannelOf(t *testing.T) {
	for in, want := range map[string]string{
		"Delve Intel_20261021_112005_90000001.txt": "Delve Intel",
		"Local_20261021_112005.txt":                "Local",
		"a_b_c_20261021_112005_1.txt":              "a_b_c",
		"notes.txt":                                "",
	} {
		got, ok := ChannelOf(in)
		if got != want || ok != (want != "") {
			t.Errorf("ChannelOf(%q) = %q %v", in, got, ok)
		}
	}
}

func TestTailReadsWholeLinesAcrossWrites(t *testing.T) {
	var tl tail
	all := utf16le("Header\r\n[ 2026.10.21 11:20:05 ] Scout > Jita +2\r\n[ 2026.10.21 11:21:00 ] Sco", true)
	// Split mid-character, mid-line.
	first := tl.read(all[:7])
	tl.offset = 7
	rest := tl.read(all[7:])
	if len(first) != 0 {
		t.Errorf("no complete line yet: %q", first)
	}
	if len(rest) != 2 || rest[1] != "[ 2026.10.21 11:20:05 ] Scout > Jita +2" {
		t.Fatalf("lines: %q", rest)
	}
	more := tl.read(utf16le("ut > Amarr clr\r\n", false))
	if len(more) != 1 || more[0] != "[ 2026.10.21 11:21:00 ] Scout > Amarr clr" {
		t.Errorf("the line finished later: %q", more)
	}
	if IsMessage("Header") || !IsMessage(more[0]) {
		t.Errorf("furniture vs messages")
	}
}

// TestWatcherPostsNewLinesOnce: logs already there are skipped (no
// replay), new lines in followed channels go to their mapped channel
// once even when two characters log them, other channels are ignored.
func TestWatcherPostsNewLinesOnce(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer esw_test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"no"}`))
			return
		}
		switch r.URL.Path {
		case "/api/intel/v1/channels":
			_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":1,"name":"Delve Intel","post":true},{"id":2,"name":"Read Only","post":false},{"id":3,"name":"Site Name","post":true}]}`))
		default:
			var body struct{ Lines []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			got[r.URL.Path] = append(got[r.URL.Path], body.Lines...)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"posted":1}`))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	old := filepath.Join(dir, "Delve Intel_20261020_100000_1.txt")
	if err := os.WriteFile(old, utf16le("[ 2026.10.20 10:00:00 ] Old > Jita old news\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(old, past, past)

	w := &Watcher{Dir: dir, Client: &Client{Server: srv.URL, Token: "esw_test"}, Map: map[string]int64{"Other Name": 3, "Read Only": 2}}
	w.init()
	if err := w.Routes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.routes["read only"] != 0 {
		t.Errorf("a read-only channel is never posted to")
	}
	w.Scan()

	line := "[ 2026.10.21 11:20:05 ] Scout > 1DQ1-A +3\r\n"
	for _, f := range []string{"Delve Intel_20261021_110000_1.txt", "Delve Intel_20261021_110000_2.txt", "Other Name_20261021_110000_1.txt", "Corp_20261021_110000_1.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), utf16le("Channel header\r\n"+line, true), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	appendTo, _ := os.OpenFile(old, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = appendTo.Write(utf16le("[ 2026.10.21 11:22:00 ] Late > Jita +1\r\n", false))
	appendTo.Close()

	w.Scan()
	w.Send(context.Background())
	mu.Lock()
	defer mu.Unlock()
	want := map[string]bool{"[ 2026.10.21 11:20:05 ] Scout > 1DQ1-A +3": true, "[ 2026.10.21 11:22:00 ] Late > Jita +1": true}
	l := got["/api/intel/v1/channels/1/lines"]
	if len(l) != 2 || !want[l[0]] || !want[l[1]] || l[0] == l[1] {
		t.Errorf("channel 1 gets the new line once (two characters logged it) and the old log's appended line: %q", l)
	}
	if l := got["/api/intel/v1/channels/3/lines"]; len(l) != 1 {
		t.Errorf("a mapped name posts to its channel: %q", l)
	}
	if len(got) != 2 {
		t.Errorf("only followed channels post: %v", got)
	}
}

// A log already in the folder at the first scan is baselined at its
// end no matter how fresh its modification time is: its existing
// lines are old news and never post. Lines appended afterwards do.
func TestWatcherBaselinesLogsPresentAtFirstScan(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/intel/v1/channels":
			_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":1,"name":"Delve Intel","post":true}]}`))
		default:
			var body struct{ Lines []string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			got = append(got, body.Lines...)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"posted":1}`))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "Delve Intel_20261021_110000_1.txt")
	if err := os.WriteFile(path, utf16le("Channel header\r\n[ 2026.10.21 11:20:05 ] Scout > 1DQ1-A +3\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	w := &Watcher{Dir: dir, Client: &Client{Server: srv.URL, Token: "esw_test"}}
	w.init()
	if err := w.Routes(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Stamp it as a busy log: last written after the watcher
	// started, before its first scan.
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	w.Scan()
	w.Send(context.Background())
	if len(got) != 0 {
		t.Fatalf("a first-scan log's existing lines are old news and must not post: %q", got)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(utf16le("[ 2026.10.21 11:22:00 ] Late > Jita +1\r\n", false))
	f.Close()

	w.Scan()
	w.Send(context.Background())
	if len(got) != 1 || got[0] != "[ 2026.10.21 11:22:00 ] Late > Jita +1" {
		t.Errorf("a line appended after the baseline posts: %q", got)
	}
}

func TestConfigCheck(t *testing.T) {
	if err := (Config{Server: "https://x.example", Token: "esw_abc"}).Check(); err != nil {
		t.Errorf("a good config: %v", err)
	}
	if err := Template.Check(); err == nil {
		t.Errorf("the empty template must not pass")
	}
}

// waitPosted polls cond until it holds or the deadline passes.
func waitPosted(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func appendLine(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(utf16le(s, false)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// Lines written while the watcher is still starting — its first
// channel fetch in flight, which retries and DNS fallbacks can
// stretch out on a bad network — are new lines and must post.
// Baselining at the sizes the first scan happens to see swallows
// them into "already there"; only a replay ever surfaced them.
func TestWatcherPostsLinesWrittenWhileStarting(t *testing.T) {
	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/intel/v1/channels" {
			time.Sleep(1500 * time.Millisecond) // the slow first fetch
			_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":1,"name":"Delve Intel","post":true}]}`))
			return
		}
		var body struct{ Lines []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posted = append(posted, body.Lines...)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"posted":1}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "Delve Intel_20261024_050000_1.txt")
	if err := os.WriteFile(path, utf16le("header\r\n[ 2026.10.24 05:00:00 ] Old > Jita old\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second log appears only while the fetch is in flight; its
	// lines count from its first byte.
	late := filepath.Join(dir, "Delve Intel_20261024_050005_2.txt")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Watcher{Dir: dir, Client: &Client{Server: srv.URL, Token: "esw_test"},
		Poll: 10 * time.Millisecond, Flush: 10 * time.Millisecond, Refresh: time.Hour}
	go func() { _ = w.Run(ctx) }()

	time.Sleep(300 * time.Millisecond) // fetch still in flight
	appendLine(t, path, "[ 2026.10.24 05:00:30 ] Scout > 1DQ1-A +3\r\n")
	if err := os.WriteFile(late, utf16le("header\r\n[ 2026.10.24 05:00:40 ] Scout > NOL-7A +1\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}

	time.Sleep(2 * time.Second) // watcher fully up
	appendLine(t, path, "[ 2026.10.24 05:02:00 ] Scout > V-LEKM clr\r\n")

	waitPosted(t, "the lines written while starting", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(posted) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	want := map[string]bool{
		"[ 2026.10.24 05:00:30 ] Scout > 1DQ1-A +3":  true,
		"[ 2026.10.24 05:00:40 ] Scout > NOL-7A +1":  true,
		"[ 2026.10.24 05:02:00 ] Scout > V-LEKM clr": true,
	}
	for _, l := range posted {
		if !want[l] {
			t.Errorf("posted %q, which was already there when the watcher started (or a duplicate)", l)
		}
		delete(want, l)
	}
	if len(want) != 0 {
		t.Errorf("never posted: %v (posted %q)", want, posted)
	}
}

// A long line appended in pieces — a d-scan pasted into the
// channel reaches the log in more than one write — posts whole,
// once its ending arrives, and not before.
func TestWatcherPostsLongLineSplitAcrossWrites(t *testing.T) {
	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/intel/v1/channels" {
			_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":1,"name":"Delve Intel","post":true}]}`))
			return
		}
		var body struct{ Lines []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posted = append(posted, body.Lines...)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"posted":1}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Watcher{Dir: dir, Client: &Client{Server: srv.URL, Token: "esw_test"},
		Poll: 10 * time.Millisecond, Flush: 10 * time.Millisecond, Refresh: time.Hour}
	go func() { _ = w.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	path := filepath.Join(dir, "Delve Intel_20261024_050000_1.txt")
	if err := os.WriteFile(path, utf16le("header\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	long := "[ 2026.10.24 05:01:00 ] Scout > " + strings.Repeat("35825 - Raitaru ", 200) + "end\r\n"
	half := len(long) / 2
	appendLine(t, path, long[:half])
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if len(posted) != 0 {
		t.Errorf("half a line must not post: %q", posted)
	}
	mu.Unlock()
	appendLine(t, path, long[half:])
	want := strings.TrimRight(long, "\r\n")
	waitPosted(t, "the completed long line", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(posted) == 1 && posted[0] == want
	})
	cancel()
}

// The folder holds logs of channels the account has no route
// for — a standard channel it never configured and a private
// conversation: scans pass over both in silence. Neither is
// followed, neither is named anywhere in the watcher's log, and
// the routed log alongside them is followed and queues its new
// lines exactly as it does on its own.
func TestScanPassesUnroutedChannelsInSilence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":1,"name":"Delve Intel","post":true}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	routed := filepath.Join(dir, "Delve Intel_20261024_050000_1.txt")
	if err := os.WriteFile(routed,
		utf16le("header\r\n[ 2026.10.24 05:00:00 ] Scout > Jita old\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	unrouted := []string{
		filepath.Join(dir, "Local_20261024_050000_1.txt"),
		filepath.Join(dir, "Jane Doe_20261024_050000_1.txt"),
	}
	for _, p := range unrouted {
		if err := os.WriteFile(p,
			utf16le("header\r\n[ 2026.10.24 05:00:00 ] Scout > Jita +2\r\n", true), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var logs []string
	w := &Watcher{Dir: dir, Client: &Client{Server: srv.URL, Token: "esw_test"},
		Logf: func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf(format, args...))
			mu.Unlock()
		}}
	w.init()
	if err := w.Routes(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.Scan()
	w.Scan()
	appendLine(t, routed, "[ 2026.10.24 05:01:00 ] Late > Jita +1\r\n")
	w.Scan()

	if _, ok := w.files[routed]; !ok {
		t.Errorf("the routed log is followed: %v", w.files)
	}
	for _, p := range unrouted {
		if _, ok := w.files[p]; ok {
			t.Errorf("the unrouted log %s has no tail", p)
		}
	}
	if got := w.pending[1]; len(got) != 1 || got[0] != "[ 2026.10.24 05:01:00 ] Late > Jita +1" {
		t.Errorf("the routed log queues its new line exactly as before: %q", got)
	}
	if n := w.queued(); n != 1 {
		t.Errorf("only the routed line is queued: %d waiting", n)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range logs {
		for _, banned := range []string{"Local", "Jane Doe", "no site channel"} {
			if strings.Contains(l, banned) {
				t.Errorf("the log says nothing about unrouted channels, found %q in %q", banned, l)
			}
		}
	}
}

// A tail that stops consuming a log whose bytes are on disk —
// here the path is swapped for a dangling symlink, so every stat
// of it fails while the watcher runs — is resynced by the stall
// watchdog once the log is back and the stall interval has
// lapsed: the waiting line is re-read and posted, the resync is
// in the log, and no restart is involved.
func TestWatcherWatchdogResyncsStalledTail(t *testing.T) {
	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/intel/v1/channels" {
			_, _ = w.Write([]byte(`{"account":"Pilot","channels":[{"id":1,"name":"Delve Intel","post":true}]}`))
			return
		}
		var body struct{ Lines []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		posted = append(posted, body.Lines...)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"posted":1}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "Delve Intel_20261025_000000_1.txt")
	if err := os.WriteFile(path, utf16le("header\r\n[ 2026.10.25 00:00:00 ] Old > Jita old\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs []string
	w := &Watcher{Dir: dir, Client: &Client{Server: srv.URL, Token: "esw_test"}, Stall: time.Hour,
		Logf: func(format string, args ...any) {
			mu.Lock()
			logs = append(logs, fmt.Sprintf(format, args...))
			mu.Unlock()
		}}
	w.init()
	if err := w.Routes(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.Scan() // baselines what is already there
	appendLine(t, path, "[ 2026.10.25 00:01:00 ] Scout > Jita +2\r\n")

	// Wedge the path: a dangling symlink stats as an error, so
	// the scan cannot observe or consume the waiting line.
	aside := path + ".aside"
	if err := os.Rename(path, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(aside+".missing", path); err != nil {
		t.Fatal(err)
	}
	w.Scan()
	w.Scan()
	w.Send(context.Background())
	mu.Lock()
	if len(posted) != 0 {
		t.Fatalf("nothing posts while the log cannot be observed: %q", posted)
	}
	mu.Unlock()

	// The log comes back and the stall interval lapses: the next
	// scan resyncs the tail instead of reading past the wait.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(aside, path); err != nil {
		t.Fatal(err)
	}
	w.Stall = time.Millisecond
	time.Sleep(10 * time.Millisecond)
	w.Scan()
	w.Send(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(posted) != 1 || posted[0] != "[ 2026.10.25 00:01:00 ] Scout > Jita +2" {
		t.Errorf("the watchdog resync posts the waiting line, once: %q", posted)
	}
	resynced := false
	for _, l := range logs {
		if strings.Contains(l, "resynced") && strings.Contains(l, "recovered 1") {
			resynced = true
		}
	}
	if !resynced {
		t.Errorf("the resync is in the log: %v", logs)
	}
}
