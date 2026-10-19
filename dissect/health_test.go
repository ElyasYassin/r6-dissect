package dissect

import "testing"

func TestHealthStateDown(t *testing.T) {
	slot := []byte{0x30, 0xbb, 0x2b, 0xf0}
	health := []byte{0x38, 0xbb, 0x2b, 0xf0}
	var b []byte
	// link: 1B <slot> 00000000 41 54 DC C4 <health> 00000000
	b = append(b, 0x1B)
	b = append(b, slot...)
	b = append(b, 0, 0, 0, 0)
	b = append(b, healthLinkIndicator...)
	b = append(b, health...)
	b = append(b, 0, 0, 0, 0)
	linkOffset := len(b) - 8
	// 23 <health> 00000000 25 26 76 C9 04 <0> 22 E7 88 F6 A5 04 <3>
	b = append(b, 0x23)
	b = append(b, health...)
	b = append(b, 0, 0, 0, 0, 0x25, 0x26, 0x76, 0xC9, 0x04, 0, 0, 0, 0, 0x22)
	b = append(b, healthStateIndicator...)
	stateOffset := len(b)
	b = append(b, 3, 0, 0, 0)

	r := &Reader{
		b:            b,
		healthToSlot: make(map[[4]byte][4]byte),
		healthState:  make(map[int]uint32),
		Header: Header{
			Players: []Player{{Username: "downed", slotID: slot}},
		},
	}
	r.clock.started = true
	r.offset = linkOffset
	if err := readHealthLink(r); err != nil {
		t.Fatal(err)
	}
	r.offset = stateOffset
	if err := readHealthState(r); err != nil {
		t.Fatal(err)
	}
	if len(r.healthDowns) != 1 || r.healthDowns[0].update.Target != "downed" {
		t.Fatalf("expected one down of 'downed', got %+v", r.healthDowns)
	}
	// the same state again is not a new down
	if err := readHealthState(r); err != nil {
		t.Fatal(err)
	}
	if len(r.healthDowns) != 1 {
		t.Fatalf("expected repeated state to be ignored, got %d downs", len(r.healthDowns))
	}
}

func TestApplyHealthDBNO(t *testing.T) {
	r := &Reader{
		healthSeen: true,
		Header: Header{
			Players: []Player{{Username: "x"}, {Username: "z"}, {Username: "w"}},
		},
		MatchFeedback: []MatchUpdate{
			{Type: DBNO, Username: "a", Target: "x", ElapsedSeconds: 10}, // kill feed heuristic
			{Type: Kill, Username: "a", Target: "x", ElapsedSeconds: 12, DBNOBy: "a"},
			{Type: Kill, Username: "b", Target: "y", ElapsedSeconds: 30},
			{Type: Kill, Username: "c", Target: "w", ElapsedSeconds: 90},
		},
		healthTransitions: []healthTransition{
			{player: 0, state: healthStateDowned, elapsed: 11},
			{player: 0, state: healthStateWounded, elapsed: 11.5},
			{player: 0, state: healthStateDead, elapsed: 11.6},
			{player: 2, state: healthStateDowned, elapsed: 35},
			{player: 2, state: healthStateWounded, elapsed: 45}, // revived
			{player: 1, state: healthStateDowned, elapsed: 40},
			{player: 2, state: healthStateDead, elapsed: 90},
		},
		healthDowns: []healthDown{
			{index: 1, transition: 0, update: MatchUpdate{Type: DBNO, Target: "x", ElapsedSeconds: 11, fromHealth: true}},
			{index: 3, transition: 3, update: MatchUpdate{Type: DBNO, Target: "w", ElapsedSeconds: 35, fromHealth: true}},
			{index: 3, transition: 5, update: MatchUpdate{Type: DBNO, Target: "z", ElapsedSeconds: 40, fromHealth: true}},
		},
	}
	r.applyHealthDBNO()
	want := []struct {
		typ      MatchUpdateType
		username string
		target   string
		dbnoBy   string
	}{
		{DBNO, "a", "x", ""},
		{Kill, "a", "x", "a"},
		{Kill, "b", "y", ""},
		{DBNO, "", "w", ""},   // revived: instigator unknown, not credited to the later killer
		{DBNO, "", "z", ""},   // round end
		{Revive, "", "w", ""}, // no score revive candidate
		{Kill, "c", "w", ""},
	}
	if len(r.MatchFeedback) != len(want) {
		t.Fatalf("expected %d events, got %+v", len(want), r.MatchFeedback)
	}
	for i, w := range want {
		u := r.MatchFeedback[i]
		if u.Type != w.typ || u.Username != w.username || u.Target != w.target || u.DBNOBy != w.dbnoBy {
			t.Fatalf("event %d: got %+v, want %+v", i, u, w)
		}
	}
}
