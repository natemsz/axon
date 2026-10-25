package watch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A local list pastes as one name a line; it is posted as one chat
// line with the names two spaces apart, as the client's own chat
// box would have sent what it could of it — all of it, here.
func TestPrepareLinesRoster(t *testing.T) {
	now := time.Date(2026, 10, 24, 23, 41, 7, 0, time.FixedZone("PDT", -7*3600))
	got, err := PrepareLines("Burzrujat", "Aaranen Hilanen\nAhdah\n\n  Air Hays Creire-Gen  \n", now)
	if err != nil {
		t.Fatal(err)
	}
	want := "[ 2026.10.25 06:41:07 ] Burzrujat > Aaranen Hilanen  Ahdah  Air Hays Creire-Gen"
	if len(got) != 1 || got[0] != want {
		t.Errorf("lines %q, want [%q]", got, want)
	}
}

// A typed line goes as itself, trimmed; CRLF pastes are one paste.
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
}

func TestPrepareLinesEmpty(t *testing.T) {
	for _, text := range []string{"", "  \n \r\n\t\n"} {
		if _, err := PrepareLines("Scout", text, time.Now()); err == nil {
			t.Errorf("text %q: no error", text)
		}
	}
}

// fakeSite serves the channels list and records what is posted.
type fakeSite struct {
	posted   []string
	postedTo string
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
				Lines []string `json:"lines"`
			}
			b, _ := io.ReadAll(req.Body)
			if err := json.Unmarshal(b, &body); err != nil {
				t := &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"bad"}`))}
				return t, nil
			}
			f.posted = body.Lines
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

// A paste is posted to the chosen channel as one line spoken by the
// account's character — the shape a watched line arrives in.
func TestPosterPostsThePreparedLine(t *testing.T) {
	site := &fakeSite{}
	p := &Poster{Client: &Client{Server: "https://es.example", Token: "esw_x", HTTP: &http.Client{Transport: site.transport()}}}
	now := time.Date(2026, 10, 25, 6, 41, 7, 0, time.UTC)
	res, target, err := p.Post(context.Background(), 12, "Aaranen Hilanen\nAhdah", now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Posted != 1 || site.postedTo != "/api/intel/v1/channels/12/lines" || target.Name != "Delve Intel" {
		t.Errorf("result %+v, target %+v, posted to %q", res, target, site.postedTo)
	}
	want := "[ 2026.10.25 06:41:07 ] Burzrujat > Aaranen Hilanen  Ahdah"
	if len(site.posted) != 1 || site.posted[0] != want {
		t.Errorf("posted %q, want [%q]", site.posted, want)
	}
}

// A channel the account cannot post to is refused here, by name of
// the problem, and nothing is sent.
func TestPosterRefusesAnUnpostableChannel(t *testing.T) {
	site := &fakeSite{}
	p := &Poster{Client: &Client{Server: "https://es.example", Token: "esw_x", HTTP: &http.Client{Transport: site.transport()}}}
	if _, _, err := p.Post(context.Background(), 9, "hello", time.Now()); err == nil {
		t.Fatal("posted to a channel the account cannot post to")
	}
	if site.posted != nil {
		t.Errorf("posted %q, want nothing sent", site.posted)
	}
}
