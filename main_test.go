package main

import "testing"

func TestIsLink(t *testing.T) {
	for arg, want := range map[string]bool{
		"evesynapse-axon://open":      true,
		"evesynapse-axon://open/":     true, // as some browsers pass it
		"EveSynapse-Axon://open":      true,
		"evesynapse-axon://other?x=1": true, // the window still opens
		"evesynapse-axon:":            true,
		"":                            false,
		"axon.json":                   false,
		`C:\Users\p\axon.json`:        false,
		"http://127.0.0.1:47611/open": false,
		"evesynapse-axonx://open":     false,
	} {
		if got := isLink(arg); got != want {
			t.Errorf("isLink(%q) = %v, want %v", arg, got, want)
		}
	}
}
