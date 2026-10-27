package watch

import "testing"

func TestDetectKind(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"1DQ1-A Hostile One +2", KindChat},
		{"Hostile One", KindChat},
		{"Hostile One\nHostile Two\nHostile Three", KindLocal},
		{"22456\tAlpha Sabre\tSabre\t1,200 km\n587\tRifter\tRifter\t5 AU\n", KindDScan},
		{"ABC-123\tCosmic Signature\tCombat Site\t\t0.0%\t12 AU", KindProbe},
		{"Pilot\t1DQ1-A\tSabre\tInterdictor\tSquad Member\t\tWing 1", KindFleet},
		{"22456\tAlpha Sabre\tSabre\t1,200 km\nhostiles at the gate, 3 sabres!\nstill there?\nyes, camping", KindChat},
	}
	for _, c := range cases {
		if got := DetectKind(c.in); got != c.want {
			t.Errorf("DetectKind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
