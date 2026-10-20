package dissect

import "testing"

// Review Focus 1: the plant event shares its tick frame with the clock jump. The legacy
// phase is still Action, so elapsed time must come from the Action samples, not from the
// post-plant base that already shows in this frame. Here the last Action sample shares the
// jump's tick, so the base absorbs 30 ms the event must not take.
func TestBuildEvent_PlantInJumpFrame(t *testing.T) {
	c := newMsClock()
	c.observe(1, clockTok(fieldRoundTimer, 179))
	c.observe(2, clockTok(fieldRoundClockMs, 179917))
	c.observe(100, clockTok(fieldRoundClockMs, 179880)) // 37 ms into Action
	c.observe(110, clockTok(fieldRoundClockMs, 179850)) // 67 ms, same tick as the jump
	c.observe(110, clockTok(fieldRoundClockMs, 45000))  // the jump: PostPlant, base 67
	u := MatchUpdate{Type: DefuserPlantComplete, Phase: ActionPhase, ElapsedSeconds: 0}
	e := buildEvent(0, u, 110, c)
	if e.Phase != ActionPhase || e.ElapsedMs != 37 {
		t.Fatalf("plant in the jump frame: phase %s elapsed %d", e.Phase, e.ElapsedMs)
	}
	// an event of the new phase takes the new base
	k := buildEvent(1, MatchUpdate{Type: Kill, Phase: PostPlantPhase}, 120, c)
	if k.Phase != PostPlantPhase || k.ElapsedMs != 67 {
		t.Fatalf("post-plant event: phase %s elapsed %d", k.Phase, k.ElapsedMs)
	}
}

func TestBuildEvent_BeforeFirstSample(t *testing.T) {
	c := newMsClock()
	c.observe(1, clockTok(fieldRoundTimer, 179))
	c.observe(50, clockTok(fieldRoundClockMs, 44000))
	e := buildEvent(0, MatchUpdate{Type: PlayerLeave, Phase: PrepPhase, ElapsedSeconds: -3}, 10, c)
	if e.RoundClockMs != 0 || e.ElapsedMs != -3000 {
		t.Fatalf("%+v", e)
	}
}

// C-RT-13: a plant with 44 to 46 s left makes no clock jump, so the clock has no PostPlant phase.
func TestTimelinePhases_PlantWithoutJump(t *testing.T) {
	c := newMsClock()
	c.observe(1, clockTok(fieldRoundTimer, 179))
	c.observe(2, clockTok(fieldRoundClockMs, 179917))
	for i, ms := range []uint32{179000, 178100, 177200} { // a continuous countdown, no jump
		c.observe(uint32(10+i), clockTok(fieldRoundClockMs, ms))
	}
	ms := uint32(177200)
	for tick := uint32(20); ms > 45400; tick++ {
		ms -= 900
		c.observe(tick, clockTok(fieldRoundClockMs, ms))
	}
	c.observe(1000, clockTok(fieldRoundClockMs, 45500)) // the plant, 45.5 s left: no jump
	plant := buildEvent(0, MatchUpdate{Type: DefuserPlantComplete, Phase: ActionPhase, ElapsedSeconds: 134}, 1000, c)
	got := timelinePhases(c, []TimelineEvent{plant})
	if len(got) != 2 || got[0].Phase != ActionPhase || got[1] != (TimelinePhase{PostPlantPhase, 1000, 45500}) {
		t.Fatalf("%+v", got)
	}
	if plant.ElapsedMs != 179917-45500 {
		t.Fatalf("elapsed %d", plant.ElapsedMs)
	}
	// no plant, no entry
	if got := timelinePhases(c, nil); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

// C-RT-12: the stop of a plant/disable still running at round end is an inference.
func TestStopDefuserRunAtRoundEnd_IsDerived(t *testing.T) {
	d := newDefuserTestReplay()
	defPawn := []byte{0x07, 0xB4, 0x0C, 0xF0}
	d.spawn(t, d.r.Header.Players[1].slotID, defPawn)
	d.timer(t, defPawn, defuserStateDisabling, 1, "6.983")
	d.timer(t, defPawn, -1, 0.9, "6.300")
	d.r.stopDefuserRunAtRoundEnd()
	got := d.r.MatchFeedback
	last := got[len(got)-1]
	if last.Type != DefuserDisableStop || last.offset != -1 || !last.derived {
		t.Fatalf("%+v", last)
	}
	for _, u := range got[:len(got)-1] {
		if u.derived || u.offset == -1 {
			t.Errorf("listener event marked as inferred: %+v", u)
		}
	}
}
