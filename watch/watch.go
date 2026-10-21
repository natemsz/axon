package watch

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Watcher follows a chat log folder and posts the intel channels' new
// lines.
//
// Which logs it follows: those of the in-game channels that match a
// site channel the account may post to, by name (case aside), or by
// the config's explicit Map ("In-game name" -> site channel ID), which
// wins. Two characters listening to one channel write two logs of the
// same lines; each line is sent once (and the server would store it
// once anyway). Lines wait in a queue while the server cannot be
// reached, and go when it can.
type Watcher struct {
	Dir    string
	Client *Client
	Map    map[string]int64
	Replay bool
	Logf   func(format string, args ...any)

	// Poll is how often the folder is read; Flush how often queued
	// lines are sent; Refresh how often the channel list is fetched
	// again (a channel made or granted while running).
	Poll, Flush, Refresh time.Duration

	files   map[string]*tail
	first   bool
	routes  map[string]int64
	names   map[int64]string
	pending map[int64][]string
	recent  map[string]time.Time
	backoff time.Time
	wait    time.Duration
}

// maxQueued is the most lines held for one channel while the server
// is unreachable; the oldest go first.
const maxQueued = 1000

// maxRead is the most of one log read at a time (a replayed day of
// a busy channel is a few hundred kilobytes).
const maxRead = 4 << 20

// recentTTL is how long a line is remembered to skip its duplicates.
const recentTTL = 15 * time.Minute

func (w *Watcher) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	}
}

func (w *Watcher) init() {
	if w.Poll == 0 {
		w.Poll = time.Second
	}
	if w.Flush == 0 {
		w.Flush = 2 * time.Second
	}
	if w.Refresh == 0 {
		w.Refresh = 5 * time.Minute
	}
	w.files = map[string]*tail{}
	w.first = true
	w.pending = map[int64][]string{}
	w.recent = map[string]time.Time{}
}

// Routes fetches the account's channels and settles which in-game
// channel posts where. It fails on a rejected token or no channel to
// post to, which no amount of waiting fixes.
func (w *Watcher) Routes(ctx context.Context) error {
	account, list, err := w.Client.Channels(ctx)
	if err != nil {
		return err
	}
	postable := map[int64]string{}
	routes := map[string]int64{}
	for _, c := range list {
		if c.Post {
			postable[c.ID] = c.Name
			routes[strings.ToLower(strings.TrimSpace(c.Name))] = c.ID
		}
	}
	for name, id := range w.Map {
		if _, ok := postable[id]; !ok {
			w.logf("config: channel %d (for %q) is not one this account can post to; skipped", id, name)
			continue
		}
		routes[strings.ToLower(strings.TrimSpace(name))] = id
	}
	if len(routes) == 0 {
		return errors.New("this account cannot post to any intel channel: make one on the site, or ask a channel's owner for posting access")
	}
	changed := len(routes) != len(w.routes)
	for k, v := range routes {
		if w.routes[k] != v {
			changed = true
		}
	}
	w.routes, w.names = routes, postable
	if changed {
		w.logf("signed in as %s; watching:", account)
		keys := make([]string, 0, len(routes))
		for k := range routes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			w.logf("  in-game %q -> %s (#%d)", k, postable[routes[k]], routes[k])
		}
	}
	return nil
}

// Run watches until ctx ends.
func (w *Watcher) Run(ctx context.Context) error {
	w.init()
	if err := w.Routes(ctx); err != nil {
		return err
	}
	w.logf("reading chat logs in %s", w.Dir)
	poll, flush, refresh := time.NewTicker(w.Poll), time.NewTicker(w.Flush), time.NewTicker(w.Refresh)
	defer poll.Stop()
	defer flush.Stop()
	defer refresh.Stop()
	w.Scan()
	for {
		select {
		case <-ctx.Done():
			w.Send(context.Background())
			return nil
		case <-poll.C:
			w.Scan()
		case <-flush.C:
			w.Send(ctx)
		case <-refresh.C:
			if err := w.Routes(ctx); err != nil {
				w.logf("channel list: %v (keeping the last one)", err)
			}
		}
	}
}

// Scan reads what every followed log has gained and queues its
// messages.
func (w *Watcher) Scan() {
	if w.files == nil {
		w.init()
	}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		w.logf("reading %s: %v", w.Dir, err)
		return
	}
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name, ok := ChannelOf(e.Name())
		if !ok {
			continue
		}
		id := w.routes[strings.ToLower(strings.TrimSpace(name))]
		if id == 0 {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(w.Dir, e.Name())
		t := w.files[path]
		if t == nil {
			t = &tail{channel: name}
			// A log found by the first scan was already there when
			// we started and holds old news: start at its end,
			// unless replaying. A log appearing later is new, and
			// every line in it counts, whenever it was written.
			if w.first && !w.Replay {
				t.offset = info.Size()
			}
			w.files[path] = t
		}
		size := info.Size()
		if size < t.offset {
			t.offset, t.partial = 0, nil // rewritten
		}
		if size == t.offset {
			continue
		}
		if size-t.offset > maxRead {
			// Keep to whole UTF-16 units; the cut line is dropped
			// as furniture (it does not start with a timestamp).
			t.offset, t.partial = (size-maxRead)&^1, nil
		}
		b, err := readRange(path, t.offset, size)
		if err != nil {
			continue
		}
		lines := t.read(b)
		t.offset = size
		for _, l := range lines {
			if !IsMessage(l) {
				continue
			}
			key := strings.ToLower(name) + "\x00" + l
			if _, dup := w.recent[key]; dup {
				continue
			}
			w.recent[key] = now
			w.pending[id] = append(w.pending[id], l)
		}
		if over := len(w.pending[id]) - maxQueued; over > 0 {
			w.pending[id] = w.pending[id][over:]
		}
	}
	for k, at := range w.recent {
		if now.Sub(at) > recentTTL {
			delete(w.recent, k)
		}
	}
	w.first = false
}

func readRange(path string, from, to int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, to-from)
	n, err := f.ReadAt(b, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b[:n], nil
}

// Send posts the queued lines. A refused batch (no longer allowed,
// malformed) is dropped and said; one the server could not take now
// stays queued, and sending backs off.
func (w *Watcher) Send(ctx context.Context) {
	if time.Now().Before(w.backoff) {
		return
	}
	ids := make([]int64, 0, len(w.pending))
	for id, lines := range w.pending {
		if len(lines) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		for len(w.pending[id]) > 0 {
			batch := w.pending[id]
			if len(batch) > 500 {
				batch = batch[:500]
			}
			res, err := w.Client.PostLines(ctx, id, batch)
			if err != nil {
				var api *APIError
				if errors.As(err, &api) && !api.Retry() {
					w.logf("%s: %v; dropping %d lines", w.names[id], err, len(batch))
					w.pending[id] = w.pending[id][len(batch):]
					continue
				}
				if w.wait == 0 {
					w.wait = 5 * time.Second
				} else if w.wait < 2*time.Minute {
					w.wait *= 2
				}
				w.backoff = time.Now().Add(w.wait)
				w.logf("could not reach the server (%v); %d lines queued, trying again in %s", err, w.queued(), w.wait)
				return
			}
			w.wait = 0
			w.pending[id] = w.pending[id][len(batch):]
			if res.Posted > 0 {
				w.logf("%s: %d new %s", w.names[id], res.Posted, map[bool]string{true: "report", false: "reports"}[res.Posted == 1])
			}
		}
	}
}

func (w *Watcher) queued() int {
	n := 0
	for _, l := range w.pending {
		n += len(l)
	}
	return n
}
