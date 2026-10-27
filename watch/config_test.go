package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A config that sets no shortcuts gets the defaults; one that sets
// them gets its own, an empty one unbound, and they survive a save.
func TestShortcutsDefaultAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axon.json")
	if err := os.WriteFile(path, []byte(`{"server":"https://es.example","token":"esw_x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Keys() != DefaultShortcuts {
		t.Errorf("no shortcuts set: %+v, want the defaults", c.Keys())
	}
	if err := os.WriteFile(path, []byte(`{"server":"https://es.example","token":"esw_x","shortcuts":{"submit":"Ctrl+Shift+F1","dashboard":"","map":"Alt+M","feed":""},"overlay_channel":12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err = LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	want := Shortcuts{Submit: "Ctrl+Shift+F1", Map: "Alt+M"}
	if c.Keys() != want || c.OverlayChannel != 12 {
		t.Errorf("set shortcuts: %+v channel %d", c.Keys(), c.OverlayChannel)
	}
	b, _ := json.Marshal(c)
	var back Config
	if err := json.Unmarshal(b, &back); err != nil || back.Keys() != want {
		t.Errorf("round trip: %s, %v", b, err)
	}
}
