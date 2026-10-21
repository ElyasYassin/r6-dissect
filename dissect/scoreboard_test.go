package dissect

import "testing"

// reconcileFixture: O1 and O2 are each credited with a kill the scoreboard says they didn't get;
// U1 and U2 each got a kill the feed doesn't credit them with. Which kill moves to whom depends
// on the order the over/under-credited players are processed in.
func reconcileFixture() *Reader {
	r := &Reader{
		scoreboardIDToPlayer:   map[[4]byte]int{{1}: 0, {2}: 1, {3}: 2, {4}: 3},
		scoreboardInitialKills: map[[4]byte]uint32{{1}: 0, {2}: 0, {3}: 0, {4}: 0},
		scoreboardFinalKills:   map[int]uint32{0: 0, 1: 0, 2: 1, 3: 1},
	}
	r.Header.Players = []Player{
		{Username: "O1", TeamIndex: 0}, {Username: "O2", TeamIndex: 0},
		{Username: "U1", TeamIndex: 0}, {Username: "U2", TeamIndex: 0},
		{Username: "E1", TeamIndex: 1}, {Username: "E2", TeamIndex: 1},
	}
	r.MatchFeedback = []MatchUpdate{
		{Type: Kill, Username: "O1", Target: "E1"},
		{Type: Kill, Username: "O2", Target: "E2"},
	}
	return r
}

func killers(r *Reader) string {
	s := ""
	for _, u := range r.MatchFeedback {
		s += u.Username + ">" + u.Target + " "
	}
	return s
}

// The over/under-credited lists are built from map iteration, so without sorting the same
// replay could attribute kills differently from run to run.
func TestReconcileKillsFromScoreboard_Deterministic(t *testing.T) {
	first := ""
	for i := 0; i < 200; i++ {
		r := reconcileFixture()
		r.reconcileKillsFromScoreboard()
		got := killers(r)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d attributed kills as %q, run 0 as %q", i, got, first)
		}
	}
	if first != "U1>E1 O2>E2 " {
		t.Fatalf("unexpected attribution %q", first)
	}
}
