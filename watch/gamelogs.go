package watch

// Game logs for battle reports. The client writes one game log per
// session into Gamelogs, beside Chatlogs, named
//
//	20261104_120000_90000001.txt
//
// (when the session began, and the listening character's ID), with a
// header naming the listener. Axon tails each one and sends the site
// the lines it gained, gzipped, once the log has been quiet for
// SendQuiet: longer than the site's gap between fights, so a fight
// never spans two posts. The site reads the fights out of the text
// and keeps only those. A stretch with no combat line is never sent.
//
// The site queues what it is sent and reads it in turn. It limits
// how much it takes (an account's posts, bytes and text an hour, and
// what may wait at once); a refusal that says when to come back is
// waited out before the next post, so a busy site is not sent the
// same text again and again.

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// SendQuiet is how long a game log stays quiet before what it gained
// is sent.
const SendQuiet = 2 * time.Minute

// maxGameText is the most text one log holds before it is sent
// anyway (the site reads at most 32 MB a post).
const maxGameText = 24 << 20

// maxGameQueued is the most gzipped text waiting on the server; the
// oldest goes first.
const maxGameQueued = 16 << 20

// maxGameScan is the most of the game logs one scan reads.
const maxGameScan = 64 << 20

var gameLogName = regexp.MustCompile(`^(\d{8})_(\d{6})(?:_(\d+))?\.txt$`)

// GameLogDir is the Gamelogs folder beside a Chatlogs folder.
func GameLogDir(chatDir string) string {
	if chatDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(filepath.Clean(chatDir)), "Gamelogs")
}

// gameTail follows one game log.
type gameTail struct {
	session  string
	listener string
	offset   int64
	partial  []byte
	utf16    bool
	known    bool // the encoding has been read off the first bytes
	header   bool // still inside the header block
	text     bytes.Buffer
	combat   bool // the text holds a combat line
	last     time.Time
}

// lines decodes appended bytes into complete lines. Game logs are
// UTF-8; one that opens with a UTF-16 byte-order mark is read as
// the chat logs are.
func (g *gameTail) lines(b []byte) []string {
	if !g.known && len(g.partial)+len(b) >= 2 {
		head := append(append([]byte(nil), g.partial...), b...)
		g.utf16 = head[0] == 0xFF && head[1] == 0xFE
		g.known = true
	}
	if g.utf16 {
		t := &tail{offset: g.offset, partial: g.partial}
		out := t.read(b)
		g.partial = t.partial
		return out
	}
	buf := append(g.partial, b...)
	// Lines end in CRLF, LF, or a bare CR.
	i := bytes.LastIndexAny(buf, "\r\n")
	if i < 0 {
		g.partial = buf
		return nil
	}
	text := string(buf[:i+1])
	g.partial = append([]byte(nil), buf[i+1:]...)
	var out []string
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(l, "\ufeff"))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// gameChunk is one post's worth of a game log, gzipped.
type gameChunk struct {
	session  string
	listener string
	gz       []byte
	lines    int
}

// gameFollower reads every game log in a folder and keeps the text
// waiting to be sent.
type gameFollower struct {
	dir     string
	files   map[string]*gameTail
	first   bool
	replay  bool
	pending []gameChunk
	queued  int
	logf    func(string, ...any)
	off     bool // the server takes no game logs: stop sending
	until   time.Time
}

func newGameFollower(dir string, replay bool, logf func(string, ...any)) *gameFollower {
	return &gameFollower{dir: dir, files: map[string]*gameTail{}, first: true, replay: replay, logf: logf}
}

// scan reads what each game log gained and queues the text of the
// logs that have gone quiet.
func (c *gameFollower) scan(now time.Time) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !gameLogName.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(c.dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		size := info.Size()
		g := c.files[path]
		if g == nil {
			g = &gameTail{session: e.Name(), header: true}
			if c.first && !c.replay {
				// Old sessions are history, not news: start at the
				// end, after reading the header for the listener.
				g.offset = size
				g.header = false
				if name, ok := gameLogListener(path); ok {
					g.listener = name
				}
			}
			c.files[path] = g
		}
		if size < g.offset {
			g.offset, g.partial = size, nil
			continue
		}
		// A game log is read a chunk at a time; one scan reads at
		// most maxGameScan of them all.
		for budget := int64(maxGameScan); g.offset < size && budget > 0; {
			to := min(size, g.offset+maxRead)
			b, _, _, err := readRange(path, g.offset, to)
			if err != nil || len(b) == 0 {
				break
			}
			g.offset += int64(len(b))
			budget -= int64(len(b))
			for _, l := range g.lines(b) {
				c.line(g, l, now)
			}
		}
	}
	c.first = false
	for _, g := range c.files {
		if g.text.Len() > 0 && now.Sub(g.last) >= SendQuiet {
			c.queue(g)
		}
	}
}

func (c *gameFollower) line(g *gameTail, l string, now time.Time) {
	if g.header {
		if name, ok := strings.CutPrefix(l, "Listener:"); ok {
			g.listener = strings.TrimSpace(name)
		}
		if !strings.HasPrefix(l, "[") {
			return
		}
		g.header = false
	}
	g.text.WriteString(l)
	g.text.WriteByte('\n')
	g.last = now
	if strings.Contains(l, "(combat)") {
		g.combat = true
	}
	if g.text.Len() >= maxGameText {
		c.queue(g)
	}
}

// queue gzips what a log gained and sets it waiting; a stretch with
// no combat line is dropped.
func (c *gameFollower) queue(g *gameTail) {
	defer func() {
		g.text.Reset()
		g.combat = false
	}()
	if !g.combat {
		return
	}
	if g.listener == "" {
		c.logf("combat: %s has no listener in its header; not sent", g.session)
		return
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(g.text.Bytes())
	_ = zw.Close()
	ch := gameChunk{session: g.session, listener: g.listener, gz: gz.Bytes(), lines: bytes.Count(g.text.Bytes(), []byte{'\n'})}
	c.pending = append(c.pending, ch)
	c.queued += len(ch.gz)
	for c.queued > maxGameQueued && len(c.pending) > 1 {
		c.logf("combat: more than %d MB of game logs are waiting on the server; dropped the oldest", maxGameQueued>>20)
		c.queued -= len(c.pending[0].gz)
		c.pending = c.pending[1:]
	}
}

// flush queues what every log gained (the watcher is stopping).
func (c *gameFollower) flush() {
	for _, g := range c.files {
		if g.text.Len() > 0 {
			c.queue(g)
		}
	}
}

// send posts the waiting text. It reports whether the server could
// not be reached, so the watcher backs off; a refusal that says when
// to come back holds the rest until then.
func (c *gameFollower) send(ctx context.Context, client *Client, now time.Time) (unreachable bool, err error) {
	if now.Before(c.until) {
		return false, nil
	}
	for len(c.pending) > 0 && !c.off {
		ch := c.pending[0]
		res, err := client.PostGameLog(ctx, ch.session, ch.listener, ch.gz)
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && api.RetryAfter > 0 {
				c.until = now.Add(api.RetryAfter)
				c.logf("combat: %v; sending again in %s", err, api.RetryAfter.Round(time.Second))
				return false, nil
			}
			if errors.As(err, &api) && !api.Retry() {
				if api.Status == http.StatusNotFound {
					c.off = true
					c.logf("combat: this site does not take battle reports; game logs are no longer sent")
					c.pending, c.queued = nil, 0
					return false, nil
				}
				c.logf("combat: %v; dropping %d lines of %s", err, ch.lines, ch.session)
				c.drop()
				continue
			}
			return true, err
		}
		c.drop()
		c.logf("combat: sent %d lines of %s as %s; the site reads them next, %d ahead", ch.lines, ch.session, ch.listener, res.Ahead)
	}
	return false, nil
}

func (c *gameFollower) drop() {
	c.queued -= len(c.pending[0].gz)
	c.pending = c.pending[1:]
}

// gameLogListener reads the Listener line out of a game log header,
// in either encoding.
func gameLogListener(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", false
	}
	text := string(b)
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		units := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			units = append(units, uint16(b[i])|uint16(b[i+1])<<8)
		}
		text = string(utf16.Decode(units))
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.Trim(line, " \t\r\ufeff")
		if name, ok := strings.CutPrefix(line, "Listener:"); ok {
			name = strings.TrimSpace(name)
			return name, name != ""
		}
		if strings.HasPrefix(line, "[") {
			break
		}
	}
	return "", false
}

// GameLogResult is the site's answer to a posted game log: it waits
// in the site's queue, behind Ahead others, and its fights show on
// Battle Reports once read.
type GameLogResult struct {
	Queued bool `json:"queued"`
	Ahead  int  `json:"ahead"`
}

// PostGameLog sends gzipped game log text to the site's battle
// reports.
func (c *Client) PostGameLog(ctx context.Context, session, listener string, gz []byte) (GameLogResult, error) {
	var out GameLogResult
	q := url.Values{"session": {session}, "listener": {listener}}
	err := c.send(ctx, http.MethodPost, "/api/intel/v1/gamelog?"+q.Encode(), "application/gzip", gz, &out)
	if err != nil {
		return out, fmt.Errorf("posting a game log: %w", err)
	}
	return out, nil
}

// retryAfter reads a Retry-After header given in seconds.
func retryAfter(h http.Header) time.Duration {
	s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After")))
	if err != nil || s <= 0 {
		return 0
	}
	return min(time.Duration(s)*time.Second, time.Hour)
}
