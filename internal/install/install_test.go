package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLayouts(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	w := layoutFor("windows", `C:\Users\p`, env(map[string]string{"LOCALAPPDATA": `C:\Users\p\AppData\Local`}))
	// Where the install script put it, so an update replaces it.
	if w.Exe != filepath.Join(`C:\Users\p\AppData\Local`, "Programs", "EveSynapse Axon", "axon.exe") || filepath.Dir(w.Config) != w.Dir {
		t.Errorf("windows: %+v", w)
	}
	m := layoutFor("darwin", "/Users/p", env(nil))
	if !strings.HasSuffix(filepath.ToSlash(m.Exe), "Applications/EveSynapse Axon.app/Contents/MacOS/axon") ||
		!strings.Contains(filepath.ToSlash(m.Config), "Library/Application Support/EveSynapse Axon") || len(m.Legacy) != 1 {
		t.Errorf("darwin: %+v", m)
	}
	l := layoutFor("linux", "/home/p", env(map[string]string{"XDG_BIN_HOME": "/home/p/bin"}))
	if l.Exe != filepath.Join("/home/p/bin", "axon") || l.Config != filepath.Join("/home/p/bin", "axon.json") {
		t.Errorf("linux: %+v", l)
	}
}

func testLayout(t *testing.T) Layout {
	root := t.TempDir()
	dir := filepath.Join(root, "installed")
	return Layout{Home: root, Dir: dir, Exe: filepath.Join(dir, "axon.exe"), Config: filepath.Join(dir, "axon.json")}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCopyProgramReplaces(t *testing.T) {
	l := testLayout(t)
	src := filepath.Join(l.Home, "Downloads", "axon-windows-amd64.exe")
	write(t, src, "new")
	write(t, l.Exe, "old")
	if err := copyProgram(src, l.Exe); err != nil {
		t.Fatal(err)
	}
	if read(t, l.Exe) != "new" {
		t.Error("not replaced")
	}
	for _, leftover := range []string{l.Exe + ".new", l.Exe + ".old"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("%s left behind", leftover)
		}
	}
	// Installing the installed copy over itself is a no-op.
	if err := copyProgram(l.Exe, l.Exe); err != nil || read(t, l.Exe) != "new" {
		t.Errorf("self copy: %v", err)
	}
	if !l.Holds(l.Exe) || l.Holds(src) {
		t.Error("Holds")
	}
}

func TestCarryConfig(t *testing.T) {
	l := testLayout(t)
	downloads := filepath.Join(l.Home, "Downloads")
	write(t, filepath.Join(downloads, "axon.json"), `{"server":"https://a"}`)
	write(t, filepath.Join(downloads, "axon.key"), "key")
	from, err := carryConfig(l, downloads)
	if err != nil || from != filepath.Join(downloads, "axon.json") {
		t.Fatalf("carried %q, %v", from, err)
	}
	if read(t, l.Config) != `{"server":"https://a"}` || read(t, filepath.Join(l.Dir, "axon.key")) != "key" {
		t.Error("config or key not carried")
	}
	// A config already installed is never overwritten.
	write(t, filepath.Join(downloads, "axon.json"), `{"server":"https://b"}`)
	if from, _ := carryConfig(l, downloads); from != "" || !strings.Contains(read(t, l.Config), "https://a") {
		t.Error("installed config overwritten")
	}
}

func TestCarryLegacyConfig(t *testing.T) {
	l := testLayout(t)
	old := filepath.Join(l.Home, ".local", "bin", "axon.json")
	write(t, old, `{"server":"https://old"}`)
	l.Legacy = []string{old}
	from, err := carryConfig(l, filepath.Join(l.Home, "Downloads"))
	if err != nil || from != old || !strings.Contains(read(t, l.Config), "https://old") {
		t.Errorf("legacy: %q %v", from, err)
	}
}

func TestRecordLogs(t *testing.T) {
	l := testLayout(t)
	if set, err := recordLogs(l.Config, "/logs"); set || err != nil {
		t.Errorf("no config: %v %v", set, err)
	}
	write(t, l.Config, `{"server":"https://a","token":"t","channels":{"Delve":12},"future":true}`)
	if set, err := recordLogs(l.Config, "/logs"); !set || err != nil {
		t.Fatalf("recorded %v %v", set, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(read(t, l.Config)), &got); err != nil {
		t.Fatal(err)
	}
	if got["logs"] != "/logs" || got["future"] != true || got["token"] != "t" {
		t.Errorf("config now %v", got)
	}
	// A folder the player chose stays.
	if set, _ := recordLogs(l.Config, "/elsewhere"); set {
		t.Error("overwrote logs")
	}
}

func TestInstallRefusesWhileRunning(t *testing.T) {
	l := testLayout(t)
	_, err := Install(l, "src", "v1", func() bool { return false }, t.Logf)
	if err == nil || !strings.Contains(err.Error(), "would not stop") {
		t.Errorf("err %v", err)
	}
	if _, err := os.Stat(l.Exe); err == nil {
		t.Error("installed anyway")
	}
}
