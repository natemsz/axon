// Package watch is the desktop log watcher axon's engine: it
// follows the chat logs the EVE client writes, picks out the lines of
// the intel channels it is told about, and posts them to an EveSynapse
// instance's intel channel API, where they become reports.
//
// The client writes one log per channel per session, named
//
//	Channel Name_20261021_112005_90000001.txt
//
// (the channel, when the session began, and the listening character),
// in UTF-16LE with a byte-order mark: a header block, then one line a
// message, "[ 2026.10.21 11:20:05 ] Speaker > text". The watcher keeps
// an offset per file and reads only what was appended, so a long log
// costs nothing, and it starts at the end of every log already there
// (old chatter is not news) unless told to replay.
package watch

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf16"
)

// bom is the byte-order mark, which a log may repeat mid-file when
// the client reopens it.
var bom = string(rune(0xFEFF))

var logName = regexp.MustCompile(`^(.+?)_(\d{8})_(\d{6})(?:_(\d+))?\.txt$`)

// ChannelOf reads the channel's name off a chat log's file name, ok
// false for a file that is not a chat log.
func ChannelOf(file string) (string, bool) {
	m := logName.FindStringSubmatch(filepath.Base(file))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// IsMessage reports whether a log line is a chat message rather than
// the header block's furniture.
func IsMessage(line string) bool {
	return strings.HasPrefix(line, "[") && strings.Contains(line, "] ") && strings.Contains(line, " > ")
}

// tail follows one log: what has been read, and a line still being
// written. It also keeps the last direct stat of the file, an
// observation that does not come from the folder listing the scan
// iterates, and when the file last changed and was last read, so a
// tail that has stopped consuming a file that has not stopped
// growing is visible.
type tail struct {
	channel string
	offset  int64
	partial []byte // undecoded bytes after the last complete line

	size  int64     // stat size at the last observation
	mtime time.Time // stat modification time at the last observation
	id    uint64    // the file's platform identity, where one is offered
	idOK  bool      // whether id holds an identity

	lastConsume time.Time // when the tail last read bytes
	movedAt     time.Time // when the stat last showed the file changed
}

// read decodes the bytes appended since the last read into complete
// lines; a line not yet ended waits for the next read.
func (t *tail) read(b []byte) []string {
	if t.offset == 0 && len(t.partial) == 0 {
		b = bytes.TrimPrefix(b, []byte{0xFF, 0xFE})
	}
	buf := append(t.partial, b...)
	// UTF-16 units are two bytes: an odd byte waits for its pair.
	even := len(buf) &^ 1
	units := make([]uint16, even/2)
	for i := range units {
		units[i] = uint16(buf[2*i]) | uint16(buf[2*i+1])<<8
	}
	// Up to the last line break; the rest waits.
	last := -1
	for i := len(units) - 1; i >= 0; i-- {
		if units[i] == '\n' {
			last = i
			break
		}
	}
	if last < 0 {
		t.partial = buf
		return nil
	}
	text := string(utf16.Decode(units[:last+1]))
	t.partial = append([]byte(nil), buf[2*(last+1):]...)
	var out []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimRight(l, "\r"+bom)
		l = strings.TrimSpace(strings.TrimPrefix(l, bom))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// FreshestSpeaker names the character whose chat log in dir
// changed most recently: the Listener line of that log's header.
// ok is false when dir holds no chat log, or the freshest log's
// header names no listener.
func FreshestSpeaker(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var freshest string
	var freshestAt time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if freshest == "" || info.ModTime().After(freshestAt) {
			freshest, freshestAt = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	if freshest == "" {
		return "", false
	}
	return logListener(freshest)
}

// logListener reads the Listener line out of a chat log's header
// block: the character the client was playing when the session
// began. The header sits at the top of the file, in the client's
// UTF-16LE.
func logListener(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8192))
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
			if name = strings.TrimSpace(name); name != "" {
				return name, true
			}
			return "", false
		}
		if IsMessage(line) {
			break // past the header: no listener in it
		}
	}
	return "", false
}

// DefaultLogDirs are where the client keeps its chat logs, most
// likely first: Documents (and a OneDrive-redirected Documents) on
// Windows, Documents on a Mac, and the Steam Proton prefix on Linux.
func DefaultLogDirs() []string {
	home, _ := os.UserHomeDir()
	rel := filepath.Join("EVE", "logs", "Chatlogs")
	var dirs []string
	switch runtime.GOOS {
	case "windows":
		if od := os.Getenv("OneDrive"); od != "" {
			dirs = append(dirs, filepath.Join(od, "Documents", rel))
		}
		if up := os.Getenv("USERPROFILE"); up != "" {
			home = up
		}
		dirs = append(dirs, filepath.Join(home, "Documents", rel))
	case "linux":
		dirs = append(dirs,
			filepath.Join(home, ".steam", "steam", "steamapps", "compatdata", "8500", "pfx", "drive_c", "users", "steamuser", "Documents", rel),
			filepath.Join(home, ".local", "share", "Steam", "steamapps", "compatdata", "8500", "pfx", "drive_c", "users", "steamuser", "Documents", rel),
			filepath.Join(home, "Documents", rel))
	default:
		dirs = append(dirs, filepath.Join(home, "Documents", rel))
	}
	return dirs
}

// FindLogDir is the first of dirs that exists, or "".
func FindLogDir(dirs []string) string {
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	return ""
}
