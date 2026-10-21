package watch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestConfigCheck(t *testing.T) {
	if err := (Config{Server: "https://x.example", Token: "esw_abc"}).Check(); err != nil {
		t.Errorf("a good config: %v", err)
	}
	if err := Template.Check(); err == nil {
		t.Errorf("the empty template must not pass")
	}
}
