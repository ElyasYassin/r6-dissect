package dissect

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/redraskal/r6-dissect/dissect/wire"
)

var clockGS = wire.Handle{0xd0, 0x01, 0, 0, 0, 0, 0, 0}

func clockTokOn(obj wire.Handle, f wire.FieldID, v uint32) wire.Token {
	return wire.Token{Op: wire.OpValueNamed, Object: obj, Field: f, Value: binary.LittleEndian.AppendUint32(nil, v)}
}

func clockTok(f wire.FieldID, v uint32) wire.Token { return clockTokOn(clockGS, f, v) }

func TestMsClock_Phases(t *testing.T) {
	c := newMsClock()
	c.observe(10, clockTok(fieldRoundTimer, 44))
	for i, ms := range []uint32{44832, 44757, 1200, 80} {
		c.observe(uint32(20+i*9), clockTok(fieldRoundClockMs, ms))
	}
	c.observe(100, clockTok(fieldRoundClockMs, 179917))
	c.observe(109, clockTok(fieldRoundClockMs, 179880))
	c.observe(480, clockTok(fieldRoundClockMs, 12000)) // plant with 12 s left: the defuser countdown rises to 45 s
	c.observe(500, clockTok(fieldRoundClockMs, 45000))
	c.observe(509, clockTok(fieldRoundClockMs, 44960))
	want := []msPhase{{PrepPhase, 20, 44832, 0}, {ActionPhase, 100, 179917, 0}, {PostPlantPhase, 500, 45000, 179917 - 12000}}
	got := c.phases()
	if len(got) != len(want) {
		t.Fatalf("phases = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("phase %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if p, ms, el, ok := c.at(30); !ok || p != PrepPhase || ms != 44757 || el != -44757 {
		t.Fatal(p, ms, el, ok)
	}
	if p, ms, el, _ := c.at(109); p != ActionPhase || ms != 179880 || el != 37 {
		t.Fatal(p, ms, el)
	}
	if p, ms, el, _ := c.at(600); p != PostPlantPhase || ms != 44960 || el != 167917+40 {
		t.Fatal(p, ms, el)
	}
	if _, _, _, ok := c.at(5); ok {
		t.Fatal("before first sample")
	}
	if ms, ok := c.sampleBefore(109); !ok || ms != 179880 {
		t.Fatal(ms, ok)
	}
}

// A plant with more than 45 s left makes the clock drop to the defuser countdown instead of
// rising, so the rising-jump rule does not see a phase change. The legacy clock handles this
// through startPostPlant; the millisecond clock does not.
func TestMsClock_PlantAboveCountdownIsNotDetected(t *testing.T) {
	c := newMsClock()
	c.observe(1, clockTok(fieldRoundTimer, 179))
	c.observe(2, clockTok(fieldRoundClockMs, 179000))
	c.observe(10, clockTok(fieldRoundClockMs, 100000))
	c.observe(20, clockTok(fieldRoundClockMs, 45000))
	if n := len(c.phases()); n != 1 {
		t.Fatalf("phases = %d, a drop to the countdown is a plain sample", n)
	}
}

func TestMsClock_PrepOnly(t *testing.T) {
	c := newMsClock()
	c.observe(1, clockTok(fieldRoundTimer, 45))
	c.observe(2, clockTok(fieldRoundClockMs, 45000))
	c.observe(9, clockTok(fieldRoundClockMs, 44000))
	if len(c.phases()) != 1 {
		t.Fatal(c.phases())
	}
	if _, _, el, _ := c.at(9); el != -44000 {
		t.Fatal(el)
	}
}

func TestMsClock_IgnoresOtherObjects(t *testing.T) {
	other := wire.Handle{0xd1, 0x02}
	c := newMsClock()
	// a clock value before the timer object is known is not a sample
	c.observe(1, clockTok(fieldRoundClockMs, 50000))
	c.observe(2, clockTokOn(clockGS, fieldRoundTimer, 44))
	c.observe(3, clockTokOn(other, fieldRoundClockMs, 30000))
	c.observe(4, clockTok(fieldRoundClockMs, 44000))
	c.observe(5, clockTokOn(other, fieldRoundClockMs, 60000))
	if _, ms, _, ok := c.at(100); !ok || ms != 44000 {
		t.Fatal(ms, ok)
	}
	if n := len(c.phases()); n != 1 {
		t.Fatalf("phases = %d", n)
	}
	if _, ok := c.sampleBefore(1); ok {
		t.Fatal("sample before the timer object was known")
	}
}

func openRec(t *testing.T, path string) *Reader {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func openFixtureReader(t *testing.T, name string) *Reader {
	t.Helper()
	return openRec(t, filepath.Join("test", "data", "replays", "valid", "Y11S3", name))
}

func TestMsClock_Fixture(t *testing.T) {
	r := openFixtureReader(t, "custom_fortress_plant.rec")
	b := r.Data()
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	c := newMsClock()
	if _, err := wire.DecodeTLV(b, l, func(tick uint32, tk wire.Token) error { c.observe(tick, tk); return nil }); err != nil {
		t.Fatal(err)
	}
	ph := c.phases()
	if len(ph) != 3 {
		t.Fatalf("phases = %+v", ph)
	}
	if ph[0].Phase != PrepPhase || ph[0].StartMs > 60000 || ph[0].StartMs < 10000 {
		t.Errorf("prep = %+v", ph[0])
	}
	if ph[1].Phase != ActionPhase || ph[1].StartMs < 170000 || ph[1].StartMs > 185000 {
		t.Errorf("action = %+v", ph[1])
	}
	if ph[2].Phase != PostPlantPhase || ph[2].StartMs < 40000 || ph[2].StartMs > 46000 {
		t.Errorf("postplant = %+v", ph[2])
	}
	t.Logf("gameState=%s samples=%d phases=%+v", c.gameState, len(c.samples), ph)
}

// Every MatchFeedback entry of every valid fixture must carry a buffer offset, so a listener
// that appends directly instead of calling addUpdate is caught.
func TestAddUpdate_OffsetsStamped(t *testing.T) {
	recs, err := filepath.Glob(filepath.Join("test", "data", "replays", "valid", "*", "*.rec"))
	if err != nil || len(recs) == 0 {
		t.Fatalf("fixtures: %v %d", err, len(recs))
	}
	for _, p := range recs {
		r := openRec(t, p)
		if err := r.Read(); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for i, u := range r.MatchFeedback {
			if u.offset == 0 {
				t.Errorf("%s: MatchFeedback[%d] (%v) has no offset", p, i, u.Type)
			}
			if u.offset == -1 && !u.derived {
				t.Errorf("%s: MatchFeedback[%d] has offset -1 but is not derived", p, i)
			}
		}
	}
}
