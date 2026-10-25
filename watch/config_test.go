package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The grab flag is off unless a config says so: a config without it,
// and the template Axon writes, both leave the clipboard alone.
func TestGrabDefaultsOff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axon.json")
	if err := os.WriteFile(path, []byte(`{"server":"https://es.example","token":"esw_x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Grab {
		t.Error("grab is on in a config that never mentions it")
	}
	if Template.Grab {
		t.Error("the template turns grab on")
	}
}

// A config that asks for the grab gets it, and the flag survives
// being saved again (the settings page saves the whole config).
func TestGrabParsesAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axon.json")
	if err := os.WriteFile(path, []byte(`{"server":"https://es.example","token":"esw_x","grab":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Grab {
		t.Fatal("grab did not parse")
	}
	b, _ := json.Marshal(c)
	var back Config
	if err := json.Unmarshal(b, &back); err != nil || !back.Grab {
		t.Errorf("round trip: %s, %v", b, err)
	}
	// Off stays out of the file.
	b, _ = json.Marshal(Config{Server: c.Server, Token: c.Token})
	if string(b) != `{"server":"https://es.example","token":"esw_x"}` {
		t.Errorf("off marshals as %s", b)
	}
}
