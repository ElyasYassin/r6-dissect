package dissect

import "testing"

func kill(killer, target string, elapsed float64) MatchUpdate {
	return MatchUpdate{Type: Kill, Username: killer, Target: target, Phase: ActionPhase, ElapsedSeconds: elapsed}
}

func teamKill(killer, target string, elapsed float64) MatchUpdate {
	u := kill(killer, target, elapsed)
	u.TeamKill = true
	return u
}

func TestOpeningKill_SkipsTeamKill(t *testing.T) {
	r := &Reader{MatchFeedback: []MatchUpdate{teamKill("a1", "a2", 5), kill("a1", "d1", 8)}}
	if got := r.OpeningKill().Target; got != "d1" {
		t.Fatalf("want the first non-teamkill (target d1), got target %q", got)
	}
}

// other events between two kills do not break or create a trade.
func TestTrades_KillsOnly(t *testing.T) {
	stop := MatchUpdate{Type: DefuserPlantStop, Username: "a2", Phase: ActionPhase, ElapsedSeconds: 11}
	r := &Reader{MatchFeedback: []MatchUpdate{kill("d1", "a1", 10), stop, kill("a2", "d1", 12)}}
	trades := r.Trades()
	if len(trades) != 1 || trades[0][0].Target != "a1" || trades[0][1].Target != "d1" {
		t.Fatalf("want one trade [d1→a1, a2→d1], got %+v", trades)
	}
}

func TestTrades_SkipsTeamKill(t *testing.T) {
	r := &Reader{MatchFeedback: []MatchUpdate{kill("d1", "a1", 10), teamKill("a2", "a3", 11), kill("a2", "d1", 12)}}
	trades := r.Trades()
	if len(trades) != 1 || trades[0][0].Target != "a1" || trades[0][1].Target != "d1" {
		t.Fatalf("want one trade [d1→a1, a2→d1] without the teamkill, got %+v", trades)
	}
}
