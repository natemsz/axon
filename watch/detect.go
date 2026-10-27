package watch

import (
	"regexp"
	"strconv"
	"strings"
)

// The kinds of paste the overlay tells apart before sending: the
// site's scan kinds, and chat for anything else. They are the
// site's own names (its "kind" override takes them as they are).
const (
	KindDScan = "dscan"
	KindLocal = "local"
	KindFleet = "fleet"
	KindProbe = "probe"
	KindChat  = "chat"
)

var (
	sigIDRe    = regexp.MustCompile(`^[A-Z]{3}-\d{3}$`)
	charNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} '.\-_]{1,36}$`)
)

// DetectKind is what kind of paste text is, read the way the site
// reads it (internal/scan Detect in evesynapse, mirrored line for
// line): by what most of its lines look like, a stray line or two
// forgiven. Anything that is no scan is chat. The site still reads
// the paste itself when it arrives: this is only what the overlay
// shows before Send.
func DetectKind(text string) string {
	var ls []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ls = append(ls, l)
		}
	}
	if len(ls) == 0 {
		return ""
	}
	var dscan, probe, fleet, local int
	for _, l := range ls {
		cols := strings.Split(l, "\t")
		switch {
		case sigIDRe.MatchString(strings.TrimSpace(cols[0])):
			probe++
		case len(cols) >= 4 && isInt(cols[0]):
			dscan++
		case len(cols) >= 5:
			fleet++
		case len(cols) == 1 && charNameRe.MatchString(l):
			local++
		}
	}
	best, kind := 0, KindChat
	for _, c := range []struct {
		n int
		k string
	}{{dscan, KindDScan}, {probe, KindProbe}, {fleet, KindFleet}, {local, KindLocal}} {
		if c.n > best {
			best, kind = c.n, c.k
		}
	}
	if best*2 < len(ls) {
		return KindChat
	}
	// A single name-shaped line is a chat line far more often than a
	// local list of one: the site reads one line as chat too.
	if kind == KindLocal && len(ls) == 1 {
		return KindChat
	}
	return kind
}

func isInt(s string) bool {
	_, err := strconv.Atoi(strings.TrimSpace(s))
	return err == nil
}
