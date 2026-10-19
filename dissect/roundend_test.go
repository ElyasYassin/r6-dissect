package dissect

import "testing"

func roundEndPlayers() []Player {
	return []Player{
		{Username: "a1", TeamIndex: 0}, {Username: "a2", TeamIndex: 0},
		{Username: "d1", TeamIndex: 1}, {Username: "d2", TeamIndex: 1},
	}
}

// plant with unresolved planter (username "") must still count as a plant.
func TestRoundEnd_UnattributedPlantDefenseWin(t *testing.T) {
	r := &Reader{Header: Header{CodeVersion: Y10S4, GameMode: Bomb,
		Teams:   [2]Team{{Role: Attack, StartingScore: 0, Score: 0}, {Role: Defense, StartingScore: 0, Score: 1}},
		Players: roundEndPlayers()},
		MatchFeedback: []MatchUpdate{{Type: DefuserPlantComplete, Username: ""}},
	}
	r.roundEnd()
	if got := r.Header.Teams[1].WinCondition; got != DisabledDefuser {
		t.Fatalf("defense won after a plant: want DisabledDefuser, got %q", got)
	}
}

// duplicate Death events for the same player must not count twice.
func TestRoundEnd_DuplicateDeathCountedOnce(t *testing.T) {
	r := &Reader{Header: Header{CodeVersion: Y10S4, GameMode: Bomb,
		Teams:   [2]Team{{Role: Attack, StartingScore: 0, Score: 0}, {Role: Defense, StartingScore: 0, Score: 1}},
		Players: roundEndPlayers()},
		MatchFeedback: []MatchUpdate{
			{Type: Death, Username: "a1", ElapsedSeconds: 25},
			{Type: Death, Username: "a1", ElapsedSeconds: 26},
		},
	}
	r.roundEnd()
	if got := r.Header.Teams[1].WinCondition; got == KilledOpponents {
		t.Fatalf("only 1 of 2 attackers died: got KilledOpponents")
	}
}
