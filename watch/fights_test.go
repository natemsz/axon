package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A game log read end to end: the grammar, the gap that ends a
// fight, the rat-only fight that is never posted, and the tallies.
func TestCombatLogToFight(t *testing.T) {
	dir := t.TempDir()
	log := strings.Join([]string{
		"------------------------------------------------------------",
		"  Gamelog",
		"  Listener: Iwaira Itovuod",
		"  Session Started: 2026.11.04 12:00:00",
		"------------------------------------------------------------",
		// Rats only: not a battle.
		"[ 2026.11.04 12:00:01 ] (combat) <color=0xff00ffff><b>300</b> <color=0x77ffffff><font size=10>to</font> <b><color=0xffffffff>Guristas Destroyer</b><font size=10><color=0x77ffffff> - Heavy Pulse Laser II - Hits",
		// Two minutes later, a player.
		"[ 2026.11.04 12:03:00 ] (notify) Warp scramble attempt from <b>Bad Guy</b>[EVIL](Vexor) to <b>you!</b>",
		"[ 2026.11.04 12:03:01 ] (combat) <color=0xffcc0000><b>120</b> <color=0x77ffffff><font size=10>from</font> <b><color=0xffffffff>Bad Guy</b>[EVIL](Vexor)<font size=10><color=0x77ffffff> - Hammerhead II - Smashes",
		"[ 2026.11.04 12:03:01 ] (combat) <color=0xff00ffff><b>523</b> <color=0x77ffffff><font size=10>to</font> <b><color=0xffffffff>Bad Guy</b>[EVIL](Vexor)<font size=10><color=0x77ffffff> - Heavy Pulse Laser II - Penetrates",
		"[ 2026.11.04 12:03:02 ] (combat) Your group of Heavy Pulse Laser II misses Bad Guy[EVIL](Vexor) completely - Heavy Pulse Laser II",
		"[ 2026.11.04 12:03:03 ] (combat) <color=0xffccff66><b>352</b><color=0x77ffffff><font size=10> remote armor repaired by </font><b><color=0xffffffff>Good Friend</b>[GOOD](Guardian)<color=0x77ffffff><font size=10> - Large Remote Armor Repairer II</font>",
		"[ 2026.11.04 12:03:04 ] (combat) <color=0xffe57f7f><b>140 GJ</b><color=0x77ffffff><font size=10> energy neutralized </font><b><color=0xffffffff>Bad Guy</b>[EVIL](Vexor)<color=0x77ffffff><font size=10> - Medium Energy Neutralizer II</font>",
		"[ 2026.11.04 12:03:05 ] (combat) Something the grammar has never seen",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "20261104_120000_90000001.txt"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newCombatFollower(dir, true, func(string, ...any) {})
	c.scan(time.Date(2026, 11, 4, 12, 3, 30, 0, time.UTC))
	if len(c.pending) != 0 {
		t.Fatalf("a fight was closed before its quiet gap: %+v", c.pending)
	}
	c.scan(time.Date(2026, 11, 4, 12, 10, 0, 0, time.UTC))
	if len(c.pending) != 1 {
		t.Fatalf("want the player fight alone, got %d fights", len(c.pending))
	}
	f := c.pending[0]
	if f.Listener != "Iwaira Itovuod" || f.CharacterID != 90000001 {
		t.Errorf("frame: %q %d", f.Listener, f.CharacterID)
	}
	if got := f.End.Sub(f.Start); got != 4*time.Second || len(f.Series.DamageOut) != 5 {
		t.Errorf("window %s, series %d seconds", got, len(f.Series.DamageOut))
	}
	if f.Series.DamageIn[1] != 120 || f.Series.DamageOut[1] != 523 || f.Series.RepsIn[3] != 352 || f.Series.CapIn[4] != 140 {
		t.Errorf("series: %+v", f.Series)
	}
	if f.Unparsed != 1 {
		t.Errorf("unparsed %d, want 1", f.Unparsed)
	}
	var bad, friend *Tally
	for i := range f.Parties {
		switch f.Parties[i].Name {
		case "Bad Guy":
			bad = &f.Parties[i]
		case "Good Friend":
			friend = &f.Parties[i]
		}
	}
	if bad == nil || friend == nil {
		t.Fatalf("parties: %+v", f.Parties)
	}
	if !bad.Pilot || bad.Hull != "Vexor" || bad.Ticker != "EVIL" || bad.DamageIn != 120 || bad.DamageOut != 523 || bad.CapIn != 140 {
		t.Errorf("Bad Guy: %+v", bad)
	}
	if bad.TackleIn == nil || bad.TackleIn.Kind != "scramble" {
		t.Errorf("tackle: %+v", bad.TackleIn)
	}
	if friend.RepsIn != 352 || friend.Hull != "Guardian" {
		t.Errorf("Good Friend: %+v", friend)
	}
	var lasers *WeaponTally
	for i := range bad.Weapons {
		if bad.Weapons[i].Dir == DirOut && bad.Weapons[i].Weapon == "Heavy Pulse Laser II" {
			lasers = &bad.Weapons[i]
		}
	}
	if lasers == nil || lasers.Shots != 2 || lasers.Quality[QPenetrate] != 1 || lasers.Quality[QMiss] != 1 {
		t.Errorf("lasers: %+v", lasers)
	}
}
