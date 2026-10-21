package install

import "testing"

func TestSchemeCommand(t *testing.T) {
	l := layoutFor("windows", `C:\Users\p`, func(string) string { return `C:\Users\p\AppData\Local` })
	// main reads the link from -url; quoted, a path with spaces stays whole.
	want := `"C:\Users\p\AppData\Local\Programs\EveSynapse Axon\axon.exe" -url "%1"`
	if got := schemeCommand(l); got != want {
		t.Errorf("schemeCommand = %s, want %s", got, want)
	}
}
