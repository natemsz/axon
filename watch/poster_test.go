package watch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A local list pastes as one name a line; it is posted as its own
// lines, trimmed, blanks dropped, order kept, so the site reads
// the roster line by line.
func TestPrepareLinesRoster(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 41, 7, 0, time.FixedZone("PDT", -7*3600))
	got, err := PrepareLines("Burzrujat", "Aaranen Hilanen\nAhdah\n\n  Air Hays Creire-Gen  \n", now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Aaranen Hilanen", "Ahdah", "Air Hays Creire-Gen"}
	if len(got) != len(want) {
		t.Fatalf("lines %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d %q, want %q", i, got[i], want[i])
		}
	}
}

// A typed line goes as itself, trimmed, in one chat line spoken by
// the speaker; CRLF pastes are one paste. A paste whose first line
// is already a chat line goes as its lines, unchanged.
func TestPrepareLinesTyped(t *testing.T) {
	now := time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC)
	got, err := PrepareLines("Scout", "  Teonusude to Bosena gate is clear \r\n", now)
	if err != nil {
		t.Fatal(err)
	}
	want := "[ 2026.10.25 06:00:00 ] Scout > Teonusude to Bosena gate is clear"
	if len(got) != 1 || got[0] != want {
		t.Errorf("lines %q, want [%q]", got, want)
	}
	got, err = PrepareLines("Scout", "[ 2026.10.25 05:59:00 ] Burzrujat > Bosena clr\n[ 2026.10.25 05:59:30 ] Burzrujat > nv Hrober", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "[ 2026.10.25 05:59:00 ] Burzrujat > ") || !strings.HasSuffix(got[1], "nv Hrober") {
		t.Errorf("chat lines %q, want them as they are", got)
	}
}

func TestPrepareLinesEmpty(t *testing.T) {
	for _, text := range []string{"", "  \n \r\n\t\n"} {
		if _, err := PrepareLines("Scout", text, time.Now()); err == nil {
			t.Errorf("text %q: no error", text)
		}
	}
}

// fakeSite serves the channels list and records what is posted,
// and as whom.
type fakeSite struct {
	posted        []string
	postedSpeaker string
	postedTo      string
}

func (f *fakeSite) transport() http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		reply := func(body string) *http.Response {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
		}
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/intel/v1/channels":
			return reply(`{"account":"Burzrujat","channels":[{"id":9,"name":"Other","post":false},{"id":12,"name":"Delve Intel","post":true},{"id":4,"name":"a first","post":true}]}`), nil
		case req.Method == http.MethodPost && strings.HasPrefix(req.URL.Path, "/api/intel/v1/channels/"):
			f.postedTo = req.URL.Path
			var body struct {
				Lines   []string `json:"lines"`
				Speaker string   `json:"speaker"`
			}
			b, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(b, &body); err != nil {
				t := &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"bad"}`))}
				return t, nil
			}
			f.posted = body.Lines
			f.postedSpeaker = body.Speaker
			return reply(`{"posted":1,"duplicates":0,"skipped":0}`), nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"no"}`))}, nil
	})
}

func TestPosterPostable(t *testing.T) {
	p := &Poster{Client: &Client{Server: "https://es.example", Token: "esw_x", HTTP: &http.Client{Transport: (&fakeSite{}).transport()}}}
	account, list, err := p.Postable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account != "Burzrujat" || len(list) != 2 || list[0].ID != 4 || list[1].ID != 12 {
		t.Errorf("account %q, channels %+v: want the two postable, by name", account, list)
	}
}

// A paste is posted to the chosen channel as its own lines, with
// the batch's speaker named: the account's character, when no log
// folder says otherwise.
func TestPosterPostsThePasteLines(t *testing.T) {
	site := &fakeSite{}
	p := &Poster{Client: &Client{Server: "https://es.example", Token: "esw_x", HTTP: &http.Client{Transport: site.transport()}}}
	now := time.Date(2026, 10, 25, 6, 41, 7, 0, time.UTC)
	res, target, as, err := p.Post(context.Background(), 12, "Aaranen Hilanen\nAhdah", now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Posted != 1 || site.postedTo != "/api/intel/v1/channels/12/lines" || target.Name != "Delve Intel" {
		t.Errorf("result %+v, target %+v, posted to %q", res, target, site.postedTo)
	}
	if len(site.posted) != 2 || site.posted[0] != "Aaranen Hilanen" || site.posted[1] != "Ahdah" {
		t.Errorf("posted %q, want the paste's own lines", site.posted)
	}
	if site.postedSpeaker != "Burzrujat" || as != "Burzrujat (account character)" {
		t.Errorf("speaker %q, posted as %q: want the account's character, named as such", site.postedSpeaker, as)
	}
}

// A channel the account cannot post to is refused here, by name of
// the problem, and nothing is sent.
func TestPosterRefusesAnUnpostableChannel(t *testing.T) {
	site := &fakeSite{}
	p := &Poster{Client: &Client{Server: "https://es.example", Token: "esw_x", HTTP: &http.Client{Transport: site.transport()}}}
	if _, _, _, err := p.Post(context.Background(), 9, "hello", time.Now()); err == nil {
		t.Fatal("posted to a channel the account cannot post to")
	}
	if site.posted != nil {
		t.Errorf("posted %q, want nothing sent", site.posted)
	}
}

// FreshestSpeaker names the listener of the log that changed most
// recently. A folder whose freshest log names no listener, and a
// folder with no logs at all, report not-found.
func TestFreshestSpeaker(t *testing.T) {
	header := func(name string) string {
		return "---------------------------------------------------------------\r\n" +
			"  ChannelID:  (('channelID', 123),)\r\n" +
			"  Channel Name:  Intel\r\n" +
			"  Listener:  " + name + "\r\n" +
			"  Session started:  2026.10.25 06:29:11\r\n" +
			"---------------------------------------------------------------\r\n"
	}
	dir := t.TempDir()
	old := filepath.Join(dir, "Intel_20261025_060000_90000001.txt")
	fresh := filepath.Join(dir, "Intel_20261025_063000_90000002.txt")
	if err := os.WriteFile(old, utf16le(header("Old Pilot"), true), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, utf16le(header("Fresh Pilot"), true), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, past.Add(time.Hour), past.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, ok := FreshestSpeaker(dir); !ok || got != "Fresh Pilot" {
		t.Errorf("FreshestSpeaker = %q, %v: want the fresher log's listener", got, ok)
	}

	bare := t.TempDir()
	noname := filepath.Join(bare, "Intel_20261025_063000_90000003.txt")
	if err := os.WriteFile(noname, utf16le("header\r\n[ 2026.10.25 06:30:00 ] Scout > Jita +2\r\n", true), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, ok := FreshestSpeaker(bare); ok || got != "" {
		t.Errorf("FreshestSpeaker = %q, %v: want not-found when no header names a listener", got, ok)
	}
	if got, ok := FreshestSpeaker(filepath.Join(bare, "nope")); ok || got != "" {
		t.Errorf("FreshestSpeaker = %q, %v: want not-found for a missing folder", got, ok)
	}
}
