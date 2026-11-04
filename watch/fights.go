package watch

// Fights: the game logs' combat events, cut into fights and
// aggregated here at the edge. What leaves the machine per fight is
// its frame (listener, window, session file), a per-second series
// and one tally per other party; the raw log never does.
//
// A fight is a run of combat events with no quiet gap longer than
// FightGap. Only a fight with a player on the other side of the
// pilot's damage, capacitor warfare or tackle is posted: shooting
// rats is not a battle, and a session of it posts nothing.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// FightGap is the quiet gap that ends a fight.
const FightGap = 60 * time.Second

// MaxFightLength is the longest window one fight covers; a fight
// still going at that length is posted and the next one starts.
const MaxFightLength = 2 * time.Hour

// maxQueuedFights is how many closed fights wait on the server.
const maxQueuedFights = 50

// Fight is one fight as posted.
type Fight struct {
	Listener    string    `json:"listener"`
	CharacterID int64     `json:"character_id,omitempty"`
	Session     string    `json:"session"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Series      Series    `json:"series"`
	Parties     []Tally   `json:"parties"`
	Lines       int       `json:"lines"`
	Unparsed    int       `json:"unparsed"`
}

// Series holds one value per second from Start: index i is the
// second Start+i.
type Series struct {
	DamageOut []int64 `json:"damage_out"`
	DamageIn  []int64 `json:"damage_in"`
	RepsOut   []int64 `json:"reps_out"`
	RepsIn    []int64 `json:"reps_in"`
	CapIn     []int64 `json:"cap_in"`
}

// Tally is everything between the pilot and one other party.
type Tally struct {
	Name      string        `json:"name"`
	Ticker    string        `json:"ticker,omitempty"`
	Hull      string        `json:"hull,omitempty"`
	Pilot     bool          `json:"pilot"`
	DamageOut int64         `json:"damage_out"`
	DamageIn  int64         `json:"damage_in"`
	RepsOut   int64         `json:"reps_out"`
	RepsIn    int64         `json:"reps_in"`
	CapOut    int64         `json:"cap_out"`
	CapIn     int64         `json:"cap_in"`
	First     time.Time     `json:"first"`
	Last      time.Time     `json:"last"`
	TackleIn  *Tackle       `json:"tackle_in,omitempty"`
	TackleOut *Tackle       `json:"tackle_out,omitempty"`
	Weapons   []WeaponTally `json:"weapons,omitempty"`
}

// Tackle is the first warp scramble or disruption attempt one way.
type Tackle struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"`
}

// WeaponTally is one weapon's shots one way between the pilot and a
// party, with the quality words they drew.
type WeaponTally struct {
	Dir     string           `json:"dir"`
	Weapon  string           `json:"weapon"`
	Shots   int64            `json:"shots"`
	Damage  int64            `json:"damage"`
	Quality map[string]int64 `json:"quality,omitempty"`
}

// fightBuilder gathers one fight's events.
type fightBuilder struct {
	start, last time.Time
	hostile     bool // a player was on the other side of something hostile
	secs        map[int64]*[5]int64
	parties     map[string]*Tally
	weapons     map[string]*WeaponTally
	order       []string
	lines       int
	unparsed    int
}

func newFightBuilder(at time.Time) *fightBuilder {
	return &fightBuilder{start: at, last: at, secs: map[int64]*[5]int64{}, parties: map[string]*Tally{}, weapons: map[string]*WeaponTally{}}
}

func (b *fightBuilder) add(ev CombatEvent) {
	b.lines++
	if ev.At.After(b.last) {
		b.last = ev.At
	}
	key := strings.ToLower(ev.Party.Name)
	t := b.parties[key]
	if t == nil {
		t = &Tally{Name: ev.Party.Name, First: ev.At}
		b.parties[key] = t
		b.order = append(b.order, key)
	}
	if ev.Party.Pilot {
		t.Pilot, t.Ticker, t.Hull = true, ev.Party.Ticker, ev.Party.Hull
	}
	if ev.At.After(t.Last) {
		t.Last = ev.At
	}
	sec := int64(ev.At.Sub(b.start) / time.Second)
	slot := b.secs[sec]
	if slot == nil {
		slot = &[5]int64{}
		b.secs[sec] = slot
	}
	hostile := false
	switch ev.Kind {
	case EvDamage, EvMiss:
		hostile = true
		if ev.Dir == DirOut {
			t.DamageOut += ev.Amount
			slot[0] += ev.Amount
		} else {
			t.DamageIn += ev.Amount
			slot[1] += ev.Amount
		}
		if ev.Weapon != "" || ev.Quality != "" {
			wk := key + "\x00" + ev.Dir + "\x00" + strings.ToLower(ev.Weapon)
			w := b.weapons[wk]
			if w == nil {
				w = &WeaponTally{Dir: ev.Dir, Weapon: ev.Weapon}
				b.weapons[wk] = w
			}
			w.Shots++
			w.Damage += ev.Amount
			if ev.Quality != "" {
				if w.Quality == nil {
					w.Quality = map[string]int64{}
				}
				w.Quality[ev.Quality]++
			}
		}
	case EvRep:
		if ev.Dir == DirOut {
			t.RepsOut += ev.Amount
			slot[2] += ev.Amount
		} else {
			t.RepsIn += ev.Amount
			slot[3] += ev.Amount
		}
	case EvCap:
		hostile = true
		if ev.Dir == DirOut {
			t.CapOut += ev.Amount
		} else {
			t.CapIn += ev.Amount
			slot[4] += ev.Amount
		}
	case EvTackle:
		hostile = true
		mark := &Tackle{At: ev.At, Kind: ev.Weapon}
		if ev.Dir == DirIn && t.TackleIn == nil {
			t.TackleIn = mark
		} else if ev.Dir == DirOut && t.TackleOut == nil {
			t.TackleOut = mark
		}
	}
	if hostile && ev.Party.Pilot {
		b.hostile = true
	}
}

// fight finishes the builder into what is posted.
func (b *fightBuilder) fight(listener string, charID int64, session string) Fight {
	n := int(b.last.Sub(b.start)/time.Second) + 1
	s := Series{DamageOut: make([]int64, n), DamageIn: make([]int64, n), RepsOut: make([]int64, n), RepsIn: make([]int64, n), CapIn: make([]int64, n)}
	for sec, v := range b.secs {
		if sec < 0 || int(sec) >= n {
			continue
		}
		s.DamageOut[sec], s.DamageIn[sec], s.RepsOut[sec], s.RepsIn[sec], s.CapIn[sec] = v[0], v[1], v[2], v[3], v[4]
	}
	f := Fight{Listener: listener, CharacterID: charID, Session: session, Start: b.start, End: b.last, Series: s, Lines: b.lines, Unparsed: b.unparsed}
	for _, key := range b.order {
		t := *b.parties[key]
		for wk, w := range b.weapons {
			if strings.HasPrefix(wk, key+"\x00") {
				t.Weapons = append(t.Weapons, *w)
			}
		}
		sort.Slice(t.Weapons, func(i, j int) bool {
			if t.Weapons[i].Dir != t.Weapons[j].Dir {
				return t.Weapons[i].Dir > t.Weapons[j].Dir // out first
			}
			return t.Weapons[i].Damage > t.Weapons[j].Damage
		})
		f.Parties = append(f.Parties, t)
	}
	return f
}

// Segmenter cuts one listener's events into fights.
type Segmenter struct {
	Listener    string
	CharacterID int64
	Session     string
	cur         *fightBuilder
	unparsed    int
}

// Add takes one event in log order and returns a fight it closed,
// if any, and whether that fight is worth posting.
func (s *Segmenter) Add(ev CombatEvent) (Fight, bool) {
	var out Fight
	closed := false
	if s.cur != nil && (ev.At.Sub(s.cur.last) > FightGap || ev.At.Sub(s.cur.start) >= MaxFightLength) {
		out, closed = s.close()
	}
	if s.cur == nil {
		s.cur = newFightBuilder(ev.At)
		s.cur.unparsed, s.unparsed = s.unparsed, 0
	}
	s.cur.add(ev)
	return out, closed
}

// Unparsed counts a combat line no rule read, against the fight in
// progress (or the next one).
func (s *Segmenter) Unparsed() {
	if s.cur != nil {
		s.cur.unparsed++
		s.cur.lines++
		return
	}
	s.unparsed++
}

// Expire closes the fight in progress once now is a quiet gap past
// its last event.
func (s *Segmenter) Expire(now time.Time) (Fight, bool) {
	if s.cur == nil || now.Sub(s.cur.last) <= FightGap {
		return Fight{}, false
	}
	return s.close()
}

// Flush closes the fight in progress whatever the time.
func (s *Segmenter) Flush() (Fight, bool) {
	if s.cur == nil {
		return Fight{}, false
	}
	return s.close()
}

func (s *Segmenter) close() (Fight, bool) {
	b := s.cur
	s.cur = nil
	if !b.hostile {
		return Fight{}, false
	}
	return b.fight(s.Listener, s.CharacterID, s.Session), true
}

// ---------------------------------------------------------------------------
// Following the game logs.

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
	offset  int64
	partial []byte
	utf16   bool
	known   bool // the encoding has been read off the first bytes
	header  bool // still inside the header block
	seg     *Segmenter
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
	i := bytes.LastIndexByte(buf, '\n')
	if i < 0 {
		g.partial = buf
		return nil
	}
	text := string(buf[:i+1])
	g.partial = append([]byte(nil), buf[i+1:]...)
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimRight(l, "\r"), "\ufeff"))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// combatFollower reads every game log in a folder and keeps the
// closed fights waiting to be sent.
type combatFollower struct {
	dir     string
	files   map[string]*gameTail
	first   bool
	replay  bool
	pending []Fight
	logf    func(string, ...any)
	off     bool // the server has no fights API: stop sending
}

func newCombatFollower(dir string, replay bool, logf func(string, ...any)) *combatFollower {
	return &combatFollower{dir: dir, files: map[string]*gameTail{}, first: true, replay: replay, logf: logf}
}

// scan reads what each game log gained, feeds its segmenter, and
// closes the fights a quiet gap has ended.
func (c *combatFollower) scan(now time.Time) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := gameLogName.FindStringSubmatch(e.Name())
		if m == nil {
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
			charID, _ := strconv.ParseInt(m[3], 10, 64)
			g = &gameTail{header: true, seg: &Segmenter{CharacterID: charID, Session: e.Name()}}
			if c.first && !c.replay {
				// Old sessions are history, not news: start at the
				// end, after reading the header for the listener.
				g.offset = size
				g.header = false
				if name, ok := gameLogListener(path); ok {
					g.seg.Listener = name
				}
			}
			c.files[path] = g
		}
		if size < g.offset {
			g.offset, g.partial = size, nil
			continue
		}
		if size == g.offset {
			continue
		}
		from := g.offset
		if size-from > maxRead {
			from = size - maxRead
			g.partial = nil
		}
		b, _, _, err := readRange(path, from, size)
		if err != nil {
			continue
		}
		g.offset = from + int64(len(b))
		for _, l := range g.lines(b) {
			c.line(g, l)
		}
	}
	c.first = false
	for _, g := range c.files {
		if f, ok := g.seg.Expire(now.UTC()); ok {
			c.queue(f)
		}
	}
}

func (c *combatFollower) line(g *gameTail, l string) {
	if g.header {
		if name, ok := strings.CutPrefix(strings.TrimSpace(l), "Listener:"); ok {
			g.seg.Listener = strings.TrimSpace(name)
		}
		if strings.HasPrefix(l, "[") {
			g.header = false
		} else {
			return
		}
	}
	ev, combat, ok := ParseCombat(l)
	if !combat {
		return
	}
	if !ok {
		if strings.Contains(l, "(combat)") {
			g.seg.Unparsed()
		}
		return
	}
	if f, closed := g.seg.Add(ev); closed {
		c.queue(f)
	}
}

func (c *combatFollower) queue(f Fight) {
	if f.Listener == "" {
		c.logf("combat: a fight in %s has no listener in its header; not sent", f.Session)
		return
	}
	c.pending = append(c.pending, f)
	if over := len(c.pending) - maxQueuedFights; over > 0 {
		c.logf("combat: more than %d fights are waiting on the server; dropped the oldest %d", maxQueuedFights, over)
		c.pending = c.pending[over:]
	}
	c.logf("combat: fight as %s, %s to %s UTC, %d lines", f.Listener, f.Start.Format("15:04:05"), f.End.Format("15:04:05"), f.Lines)
}

// flush closes every fight in progress (the watcher is stopping).
func (c *combatFollower) flush() {
	for _, g := range c.files {
		if f, ok := g.seg.Flush(); ok {
			c.queue(f)
		}
	}
}

// send posts the waiting fights; it reports whether the server
// could not be reached, so the watcher backs off.
func (c *combatFollower) send(ctx context.Context, client *Client) (unreachable bool, err error) {
	for len(c.pending) > 0 && !c.off {
		f := c.pending[0]
		res, err := client.PostFight(ctx, f)
		if err != nil {
			var api *APIError
			if errors.As(err, &api) && !api.Retry() {
				if api.Status == http.StatusNotFound {
					c.off = true
					c.logf("combat: this site does not take battle reports; fights are no longer sent")
					c.pending = nil
					return false, nil
				}
				c.logf("combat: %v; dropping the fight at %s", err, f.Start.Format("15:04:05"))
				c.pending = c.pending[1:]
				continue
			}
			return true, err
		}
		c.pending = c.pending[1:]
		c.logf("combat: battle report %d stored (%s)", res.ID, res.Status)
	}
	return false, nil
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

// FightResult is what the site made of a posted fight.
type FightResult struct {
	ID     int64  `json:"id"`
	Status string `json:"status"` // "stored" or "updated"
}

// PostFight sends one fight to the site's battle reports.
func (c *Client) PostFight(ctx context.Context, f Fight) (FightResult, error) {
	var out FightResult
	err := c.do(ctx, http.MethodPost, "/api/intel/v1/fights", f, &out)
	if err != nil {
		return out, fmt.Errorf("posting a fight: %w", err)
	}
	return out, nil
}
