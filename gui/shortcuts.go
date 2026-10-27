package gui

import (
	"errors"
	"strconv"
	"strings"
)

// The overlay's shortcuts (watch.Shortcuts): chords such as
// "Ctrl+Alt+V", remapped from Axon's settings page to any
// combination the player wants. A chord is parsed here, on every
// platform; the window code registers it the system's own way
// (overlay_windows.go: RegisterHotKey). No shortcut reads the
// clipboard: the overlay reads it once, when the player sends with
// the field empty.

// Modifier bits, in Windows' MOD_* values so the Windows code passes
// them through as they are.
const (
	modAltBit   = 0x0001
	modCtrlBit  = 0x0002
	modShiftBit = 0x0004
	modWinBit   = 0x0008
)

// chord is one parsed shortcut: its modifiers and its key, the key
// in canonical form ("V", "7", "F2", "Space").
type chord struct {
	Mods uint32
	Key  string
}

// namedKeys are the keys a chord may end on besides letters, digits
// and function keys, by their canonical names.
var namedKeys = map[string]string{
	"space": "Space", "insert": "Insert", "ins": "Insert", "delete": "Delete", "del": "Delete",
	"home": "Home", "end": "End", "pageup": "PageUp", "pgup": "PageUp", "pagedown": "PageDown", "pgdn": "PageDown",
	"pause": "Pause", "scrolllock": "ScrollLock", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"tab": "Tab", "backquote": "`", "`": "`",
}

// alone are keys that make a chord without a modifier: no game
// binding and no typing uses them.
var alone = map[string]bool{"Pause": true, "ScrollLock": true}

// parseChord reads a shortcut as the settings page writes it.
// Modifiers come first, joined by "+", in any order and any case;
// the key comes last. A key with no modifier is refused unless it is
// one nothing else uses (F13 to F24, Pause, Scroll Lock): a bare
// letter would fire while the player types in chat.
func parseChord(s string) (chord, error) {
	parts := strings.Split(strings.TrimSpace(s), "+")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return chord{}, errors.New("no shortcut given")
	}
	var c chord
	for _, p := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(p)) {
		case "ctrl", "control":
			c.Mods |= modCtrlBit
		case "alt", "option":
			c.Mods |= modAltBit
		case "shift":
			c.Mods |= modShiftBit
		case "win", "super", "cmd", "meta":
			c.Mods |= modWinBit
		default:
			return chord{}, errors.New("\"" + strings.TrimSpace(p) + "\" is not a modifier (Ctrl, Alt, Shift or Win)")
		}
	}
	key := strings.TrimSpace(parts[len(parts)-1])
	low := strings.ToLower(key)
	fn := 0
	switch {
	case len(key) == 1 && (key[0] >= 'a' && key[0] <= 'z' || key[0] >= 'A' && key[0] <= 'Z' || key[0] >= '0' && key[0] <= '9'):
		c.Key = strings.ToUpper(key)
	case len(low) >= 2 && low[0] == 'f':
		n, err := strconv.Atoi(low[1:])
		if err != nil || n < 1 || n > 24 {
			return chord{}, errors.New("\"" + key + "\" is not a key Axon knows")
		}
		fn, c.Key = n, "F"+strconv.Itoa(n)
	default:
		name, ok := namedKeys[low]
		if !ok {
			return chord{}, errors.New("\"" + key + "\" is not a key Axon knows")
		}
		c.Key = name
	}
	if c.Mods == 0 && fn < 13 && !alone[c.Key] {
		return chord{}, errors.New("add Ctrl, Alt, Shift or Win to " + c.Key + ": on its own it would fire while you type")
	}
	return c, nil
}

// String is the chord in the settings page's canonical form.
func (c chord) String() string {
	var b strings.Builder
	for _, m := range []struct {
		bit  uint32
		name string
	}{{modCtrlBit, "Ctrl"}, {modAltBit, "Alt"}, {modShiftBit, "Shift"}, {modWinBit, "Win"}} {
		if c.Mods&m.bit != 0 {
			b.WriteString(m.name + "+")
		}
	}
	return b.String() + c.Key
}

// shortcutAction is what a shortcut does.
type shortcutAction int

const (
	actSubmit shortcutAction = iota + 1
	actDashboard
	actMap
	actFeed
	actSite
)

// intelWindowOf is the intel window an opening shortcut toggles.
var intelWindowOf = map[shortcutAction]string{
	actDashboard: "threads",
	actMap:       "map",
	actFeed:      "feed",
}

// sitePaths are the site pages a shortcut opens where there are no
// intel windows (and the site panel's own, the whole site's
// workbench).
var sitePaths = map[shortcutAction]string{
	actDashboard: "/intel/workbench/",
	actMap:       "/map/",
	actFeed:      "/intel/feed/",
	actSite:      "/intel/workbench/",
}

// boundShortcut is one chord to register, with what it does.
type boundShortcut struct {
	Action shortcutAction
	Chord  chord
}

// bindings parses a config's shortcuts into what to register. A
// chord that does not parse, or that repeats one bound before it, is
// left out and named in the problems, in words for the log and the
// settings page; the rest still bind.
func bindings(keys map[shortcutAction]string) ([]boundShortcut, []string) {
	var out []boundShortcut
	var problems []string
	seen := map[chord]bool{}
	for _, a := range []shortcutAction{actSubmit, actDashboard, actMap, actFeed, actSite} {
		s := strings.TrimSpace(keys[a])
		if s == "" {
			continue
		}
		c, err := parseChord(s)
		if err != nil {
			problems = append(problems, actionName(a)+": "+err.Error())
			continue
		}
		if seen[c] {
			problems = append(problems, actionName(a)+": "+c.String()+" is already bound to another shortcut")
			continue
		}
		seen[c] = true
		out = append(out, boundShortcut{a, c})
	}
	return out, problems
}

func actionName(a shortcutAction) string {
	switch a {
	case actSubmit:
		return "Send intel"
	case actDashboard:
		return "Intel workbench"
	case actMap:
		return "Map"
	case actFeed:
		return "Feed"
	case actSite:
		return "Site panel"
	}
	return "Shortcut"
}
