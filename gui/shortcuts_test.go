package gui

import "testing"

func TestParseChord(t *testing.T) {
	ok := map[string]string{
		"Ctrl+Alt+V": "Ctrl+Alt+V", "alt+ctrl+v": "Ctrl+Alt+V", " Shift + F2 ": "Shift+F2",
		"F13": "F13", "Pause": "Pause", "Win+Space": "Win+Space", "ctrl+shift+7": "Ctrl+Shift+7",
		"Alt+PgDn": "Alt+PageDown",
	}
	for in, want := range ok {
		c, err := parseChord(in)
		if err != nil || c.String() != want {
			t.Errorf("parseChord(%q) = %q, %v; want %q", in, c.String(), err, want)
		}
	}
	for _, bad := range []string{"", "V", "F2", "Ctrl+", "Hyper+V", "Ctrl+F25", "Ctrl+Enter!"} {
		if _, err := parseChord(bad); err == nil {
			t.Errorf("parseChord(%q) accepted", bad)
		}
	}
}

func TestBindings(t *testing.T) {
	got, problems := bindings(map[shortcutAction]string{
		actSubmit: "Ctrl+Alt+V", actDashboard: "ctrl+alt+v", actMap: "M", actFeed: "Alt+F",
	})
	if len(got) != 2 || got[0].Action != actSubmit || got[1].Action != actFeed {
		t.Errorf("bound %+v", got)
	}
	if len(problems) != 2 {
		t.Errorf("problems %q, want the duplicate and the bare key", problems)
	}
}
