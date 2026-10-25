package watch

import (
	"context"
	"errors"
	"fmt"
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
	// again (a channel made or granted while running); Beat how
	// often the watcher logs a liveness line; Stall how long a
	// followed log may hold bytes its tail has not consumed, while
	// the file itself keeps changing, before the tail is declared
	// stale and resynced.
	Poll, Flush, Refresh, Beat, Stall time.Duration

	files    map[string]*tail
	first    bool
	baseline map[string]int64 // log sizes when Run began; nil if the folder could not be read then
	anom     map[string]*anomaly
	routes   map[string]int64
	names    map[int64]string
	pending  map[int64][]string
	recent   map[string]time.Time
	backoff  time.Time
	wait     time.Duration

	bytesRead   int64
	linesQueued int64
	linesSent   int64
	lastActive  time.Time
}

// maxQueued is the most lines held for one channel while the server
// is unreachable; the oldest go first.
const maxQueued = 1000

// maxRead is the most of one log read at a time (a replayed day of
// a busy channel is a few hundred kilobytes).
const maxRead = 4 << 20

// recentTTL is how long a line is remembered to skip its duplicates.
const recentTTL = 15 * time.Minute

// anomalyInterval is how often a standing anomaly repeats its log
// line as a summary while it keeps occurring.
const anomalyInterval = 5 * time.Minute

// anomaly is one standing failure's place in the log: when it was
// last said, and how often it has occurred since.
type anomaly struct {
	count   int
	lastLog time.Time
}

func (w *Watcher) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
	}
}

// noteAnomaly logs an anomaly — a stat or a read that fails, a
// short read, a channel with nowhere to post, a duplicate line —
// the first time it occurs, and again as a summary once per
// anomalyInterval while it keeps occurring, so a standing failure
// is never silent and never fills the log either.
func (w *Watcher) noteAnomaly(key string, now time.Time, format string, args ...any) {
	if w.anom == nil {
		w.anom = map[string]*anomaly{}
	}
	a := w.anom[key]
	if a == nil {
		w.anom[key] = &anomaly{lastLog: now}
		w.logf(format, args...)
		return
	}
	if now.Sub(a.lastLog) >= anomalyInterval {
		if a.count > 0 {
			w.logf(format+" (and %d more times since the last note)", append(args, a.count)...)
		} else {
			w.logf(format, args...)
		}
		a.count, a.lastLog = 0, now
		return
	}
	a.count++
}

// displayName is a channel's site name for log lines, or its
// number when the name is not known.
func (w *Watcher) displayName(id int64) string {
	if name := w.names[id]; name != "" {
		return name
	}
	return fmt.Sprintf("channel %d", id)
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
	if w.Beat == 0 {
		w.Beat = 5 * time.Minute
	}
	if w.Stall == 0 {
		w.Stall = time.Minute
	}
	w.files = map[string]*tail{}
	w.first = true
	w.baseline = nil
	w.anom = map[string]*anomaly{}
	w.pending = map[int64][]string{}
	w.recent = map[string]time.Time{}
	w.bytesRead, w.linesQueued, w.linesSent = 0, 0, 0
	w.lastActive = time.Time{}
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

// snapshot records how large every chat log is when Run begins,
// before any network call. It is the one listing baselines are
// taken from: the first scan adopts them from it, so a line
// written while the (possibly slow) first channel fetch is still
// in flight counts as new and is posted — baselining at
// first-scan sizes instead would swallow it into "already there"
// and only a replay would ever surface it. Files the listing
// missed are not baselined at all: they are read from the start,
// and the scan says so. If the folder cannot be read, baseline
// stays nil and the first scan falls back to baselining at the
// sizes it sees, and says what it fenced there too.
func (w *Watcher) snapshot() {
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		return
	}
	base := map[string]int64{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := ChannelOf(e.Name()); !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			base[filepath.Join(w.Dir, e.Name())] = -1 // there, size unknown
			continue
		}
		base[filepath.Join(w.Dir, e.Name())] = info.Size()
	}
	w.baseline = base
}

// Run watches until ctx ends.
func (w *Watcher) Run(ctx context.Context) error {
	w.init()
	w.snapshot()
	if err := w.Routes(ctx); err != nil {
		return err
	}
	w.logf("reading chat logs in %s", w.Dir)
	poll, flush, refresh, beat := time.NewTicker(w.Poll), time.NewTicker(w.Flush), time.NewTicker(w.Refresh), time.NewTicker(w.Beat)
	defer poll.Stop()
	defer flush.Stop()
	defer refresh.Stop()
	defer beat.Stop()
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
		case <-beat.C:
			w.liveness()
		}
	}
}

// liveness is the watcher's heartbeat in its own log: what it
// follows, how much of it has moved, and when anything last did,
// so a stall shows within one interval instead of by the absence
// of reports.
func (w *Watcher) liveness() {
	last := "no lines yet"
	if !w.lastActive.IsZero() {
		last = "last activity " + w.lastActive.Format("15:04:05")
	}
	w.logf("watcher: following %d logs; %d bytes read, %d lines queued, %d waiting, %d sent; %s",
		len(w.files), w.bytesRead, w.linesQueued, w.queued(), w.linesSent, last)
}

// Scan reads what every followed log has gained and queues its
// messages. Growth is judged by a direct stat of each file, not
// by the folder listing the scan iterates: the listing's sizes
// and the files themselves can disagree (a sync-backed folder is
// the case in point), and a watcher that trusts the listing can
// go blind to a log that is in fact growing.
func (w *Watcher) Scan() {
	if w.files == nil {
		w.init()
	}
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		w.noteAnomaly("readdir", time.Now(), "reading %s: %v", w.Dir, err)
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
			w.noteAnomaly("unrouted\x00"+name, now, "%s: no site channel to post to under that name; its lines are not sent", name)
			continue
		}
		path := filepath.Join(w.Dir, e.Name())
		st, err := os.Stat(path)
		if err != nil {
			w.noteAnomaly("stat\x00"+path, now, "%s: statting it: %v", e.Name(), err)
			continue
		}
		size := st.Size()
		t := w.files[path]
		created, baselined := false, false
		reason := ""
		if t == nil {
			created = true
			t = &tail{channel: name, size: size, mtime: st.ModTime(), lastConsume: now}
			// A log there when Run began holds old news: start at
			// the size it had then (see snapshot), unless replaying.
			// A log appearing later is new, and every line in it
			// counts, whenever it was written. With no snapshot the
			// first scan baselines at the sizes it sees.
			switch {
			case w.Replay:
				reason = "replay is on"
			case w.first:
				if base, ok := w.baseline[path]; ok {
					if base >= 0 {
						t.offset = base
					} else {
						t.offset = size
					}
					baselined = true
				} else if w.baseline != nil {
					reason = "it appeared while the watcher was starting"
				} else {
					t.offset = size
					baselined = true
				}
			default:
				reason = "it appeared after the watcher started"
			}
			if size > t.offset {
				t.movedAt = now
			}
			w.files[path] = t
			if baselined && size >= t.offset {
				// A startup states what it fenced: the offset it
				// adopted and how many complete lines sit behind
				// it, so a line buried by the baseline is visible
				// in the log rather than by its absence.
				behind, cerr := countCompleteLines(path, t.offset)
				if cerr != nil {
					w.logf("%s: baselined at byte %d; the lines behind it could not be counted (%v); they are not sent", e.Name(), t.offset, cerr)
				} else {
					w.logf("%s: baselined at byte %d; %d complete lines behind it are not sent", e.Name(), t.offset, behind)
				}
			}
		}
		// The witness: a stat of this file, taken this poll. A
		// change in it is the file having moved, whatever any
		// listing says.
		if st.Size() != t.size || !st.ModTime().Equal(t.mtime) {
			t.size, t.mtime = st.Size(), st.ModTime()
			t.movedAt = now
		}
		if size < t.offset {
			// The file shrank or was replaced under its tail:
			// one event, logged, and the new file starts at its
			// end rather than being read whole by accident.
			w.logf("%s: the log shrank or was replaced (read to byte %d, now %d bytes); baselining at its end", e.Name(), t.offset, size)
			t.offset, t.partial = size, nil
			t.id, t.idOK = 0, false
			t.lastConsume = now
			continue
		}
		if size > t.offset && now.Sub(t.lastConsume) >= w.Stall && t.movedAt.After(t.lastConsume) {
			// The file holds bytes the tail has not consumed,
			// it has kept changing, and a stall's worth of time
			// has passed: the tail is stale, whatever made it
			// stop. Resync it rather than wait for a restart.
			w.resync(path, t, id, size, now)
			continue
		}
		if size == t.offset {
			continue
		}
		from := t.offset
		if size-from > maxRead {
			// Keep to whole UTF-16 units; the cut line is dropped
			// as furniture (it does not start with a timestamp).
			to := (size - maxRead) &^ 1
			w.noteAnomaly("skip\x00"+path, now, "%s: %d unread bytes is more than one read holds; skipping ahead %d bytes, which are not sent", e.Name(), size-from, to-from)
			t.offset, t.partial = to, nil
			from = to
		}
		b, fid, fidOK, err := readRange(path, from, size)
		if err != nil {
			w.noteAnomaly("read\x00"+path, now, "%s: reading it: %v", e.Name(), err)
			continue
		}
		if fidOK && t.idOK && fid != t.id {
			w.logf("%s: the log file was replaced; baselining at the end of the new one", e.Name())
			t.offset, t.partial = size, nil
			t.id, t.idOK = fid, true
			t.lastConsume = now
			continue
		}
		if fidOK {
			t.id, t.idOK = fid, true
		}
		if short := size - from - int64(len(b)); short > 0 {
			w.noteAnomaly("short\x00"+path, now, "%s: a read returned %d of %d new bytes; the remaining %d are retried next look", e.Name(), len(b), size-from, short)
		}
		lines := t.read(b)
		// The offset advances by the bytes the read actually
		// returned, never by the size it was asked for: a short
		// read leaves the gap to be read next poll rather than
		// discarded unread.
		t.offset = from + int64(len(b))
		if len(b) > 0 {
			t.lastConsume = now
			w.bytesRead += int64(len(b))
		}
		n := w.queue(t, id, lines, now)
		if created && from == 0 && size > 0 && reason != "" {
			w.logf("%s: read the whole log from the start (%s); %d lines queued", e.Name(), reason, n)
		}
	}
	for k, at := range w.recent {
		if now.Sub(at) > recentTTL {
			delete(w.recent, k)
		}
	}
	w.first = false
}

// resync recovers a stalled tail: it re-reads the bytes the file
// holds past the tail's offset, under a fresh open whose file
// identity is checked against the tail's. When recovery is
// impossible the tail baselines at the verified end and the log
// says, prominently, how much was skipped: a bounded, stated loss
// instead of an unbounded silent one.
func (w *Watcher) resync(path string, t *tail, id int64, size int64, now time.Time) {
	base := filepath.Base(path)
	old := t.offset
	since := t.lastConsume
	b, fid, fidOK, err := readRange(path, old, size)
	replaced := err == nil && fidOK && t.idOK && fid != t.id
	if err != nil || replaced {
		why := fmt.Sprintf("reading it: %v", err)
		if replaced {
			why = "the file was replaced"
		}
		skipped := -1
		if err == nil {
			skipped = len(completeLines(old, b))
		} else if cb, _, _, cerr := readRange(path, old, size); cerr == nil {
			skipped = len(completeLines(old, cb))
		}
		t.offset, t.partial = size, nil
		if fidOK {
			t.id, t.idOK = fid, true
		} else {
			t.id, t.idOK = 0, false
		}
		t.lastConsume = now
		if skipped >= 0 {
			w.logf("%s: tail stalled since %s and could not be recovered (%s); baselined at byte %d, skipping %d complete lines", base, since.Format("15:04:05"), why, size, skipped)
		} else {
			w.logf("%s: tail stalled since %s and could not be recovered (%s); baselined at byte %d, skipping %d unreadable bytes", base, since.Format("15:04:05"), why, size, size-old)
		}
		return
	}
	lines := t.read(b)
	t.offset = old + int64(len(b))
	t.lastConsume = now
	w.bytesRead += int64(len(b))
	if fidOK {
		t.id, t.idOK = fid, true
	}
	n := w.queue(t, id, lines, now)
	word := "lines"
	if n == 1 {
		word = "line"
	}
	w.logf("%s: tail stalled; resynced from byte %d to %d and recovered %d %s", base, old, t.offset, n, word)
}

// completeLines decodes raw log bytes read from offset from and
// returns the complete lines in them, for counting what a span
// holds without queueing any of it.
func completeLines(from int64, b []byte) []string {
	probe := &tail{offset: from}
	return probe.read(b)
}

// countCompleteLines counts the complete lines in the first upto
// bytes of the file at path, streaming so a long log costs a
// buffer, not its size. Lines end in a UTF-16 line feed, so the
// count is of 0x000A units at even offsets.
func countCompleteLines(path string, upto int64) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 64<<10)
	n := 0
	for pos := int64(0); pos < upto; {
		want := int64(len(buf))
		if rem := upto - pos; rem < want {
			want = rem
		}
		m, rerr := f.ReadAt(buf[:want], pos)
		for i := 0; i+1 < m; i += 2 {
			if buf[i] == '\n' && buf[i+1] == 0 {
				n++
			}
		}
		pos += int64(m)
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return n, rerr
		}
		if m == 0 {
			break
		}
	}
	return n, nil
}

// queue sends decoded lines to their channel's pending queue,
// skipping furniture and lines another log of the same channel
// already brought. It returns how many lines it queued.
func (w *Watcher) queue(t *tail, id int64, lines []string, now time.Time) int {
	n := 0
	for _, l := range lines {
		if !IsMessage(l) {
			continue
		}
		key := strings.ToLower(t.channel) + "\x00" + l
		if _, dup := w.recent[key]; dup {
			w.noteAnomaly("dup\x00"+t.channel, now, "%s: the same line arrived in more than one log; sending it once", t.channel)
			continue
		}
		w.recent[key] = now
		w.pending[id] = append(w.pending[id], l)
		n++
	}
	if n > 0 {
		w.linesQueued += int64(n)
		w.lastActive = now
	}
	if over := len(w.pending[id]) - maxQueued; over > 0 {
		w.logf("%s: more than %d lines are waiting on the server; dropped the oldest %d", w.displayName(id), maxQueued, over)
		w.pending[id] = w.pending[id][over:]
	}
	return n
}

// readRange reads the bytes in [from, to) of the file at path. It
// returns the bytes it actually got, which may be fewer than were
// asked for when the file ends early, so the caller advances by
// what came back; and the file's platform identity, where one is
// offered.
func readRange(path string, from, to int64) ([]byte, uint64, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	id, idOK := fileID(f)
	b := make([]byte, to-from)
	n, err := f.ReadAt(b, from)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, false, err
	}
	return b[:n], id, idOK, nil
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
			w.linesSent += int64(len(batch))
			w.lastActive = time.Now()
			word := "reports"
			if res.Posted == 1 {
				word = "report"
			}
			w.logf("%s: %d new %s, %d already there, %d skipped", w.names[id], res.Posted, word, res.Duplicates, res.Skipped)
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
