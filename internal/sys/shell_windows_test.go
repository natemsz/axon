package sys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEditPath(t *testing.T) {
	const dir = `C:\Users\p\AppData\Local\Programs\EveSynapse Axon`
	for _, c := range []struct {
		cur, want string
		add, ok   bool
	}{
		{`C:\a;C:\b`, `C:\a;C:\b;` + dir, true, true},
		{``, dir, true, true},
		{`C:\a;` + dir + `\`, `C:\a;` + dir + `\`, true, false}, // there already, trailing slash
		{`C:\a;` + `c:\users\p\appdata\local\programs\evesynapse axon` + `;C:\b`, `C:\a;C:\b`, false, true},
		{`C:\a;;C:\b`, `C:\a;;C:\b`, false, false},
		{`%USERPROFILE%\bin;C:\a`, `%USERPROFILE%\bin;C:\a;` + dir, true, true},
	} {
		got, ok := editPath(c.cur, dir, c.add)
		if got != c.want || ok != c.ok {
			t.Errorf("editPath(%q, add=%v) = %q, %v; want %q, %v", c.cur, c.add, got, ok, c.want, c.ok)
		}
	}
}

func TestShortcut(t *testing.T) {
	dir := t.TempDir()
	lnk := filepath.Join(dir, "Axon.lnk")
	target := filepath.Join(os.Getenv("SystemRoot"), "notepad.exe")
	if err := Shortcut(lnk, target, dir, target, "a test shortcut"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(lnk)
	if err != nil {
		t.Fatal(err)
	}
	// A shell link starts with its header size and the ShellLink CLSID.
	if len(b) < 20 || b[0] != 0x4C || b[4] != 0x01 || b[5] != 0x14 || b[6] != 0x02 {
		t.Errorf("not a shell link: % x", b[:20])
	}
}

func TestKnownFolders(t *testing.T) {
	for name, f := range map[string]func() (string, error){"Desktop": DesktopDir, "Programs": ProgramsDir} {
		d, err := f()
		if err != nil || d == "" {
			t.Errorf("%s: %q, %v", name, d, err)
		}
	}
}

func TestURLScheme(t *testing.T) {
	const scheme = "evesynapse-axon-test"
	t.Cleanup(func() { RemoveURLScheme(scheme) })
	const command = `"C:\Axon\axon.exe" -url "%1"`
	if err := URLScheme(scheme, "Axon test", `C:\Axon\axon.ico`, command); err != nil {
		t.Fatal(err)
	}
	if got := URLSchemeCommand(scheme); got != command {
		t.Errorf("command %q, want %q", got, command)
	}
	RemoveURLScheme(scheme)
	if got := URLSchemeCommand(scheme); got != "" {
		t.Errorf("still there after removing: %q", got)
	}
}
