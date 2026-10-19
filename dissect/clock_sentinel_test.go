package dissect

import "testing"

// the timer jumps to 0 when a round ends early; events after it happened at the last real tick.
func TestClock_RoundEndZeroIsNotElapsedTime(t *testing.T) {
	r := &Reader{}
	for v := 180.0; v >= 22; v-- {
		r.updateClock(v)
	}
	r.updateClock(0)
	u := MatchUpdate{}
	r.stamp(&u)
	if u.ElapsedSeconds != 158 {
		t.Fatalf("want elapsedSeconds 158 (last tick 0:22), got %v", u.ElapsedSeconds)
	}
}

// natural expiry (1 -> 0) is a real tick.
func TestClock_NaturalExpiryCounts(t *testing.T) {
	r := &Reader{}
	for v := 180.0; v >= 1; v-- {
		r.updateClock(v)
	}
	r.updateClock(0)
	u := MatchUpdate{}
	r.stamp(&u)
	if u.ElapsedSeconds != 180 {
		t.Fatalf("want elapsedSeconds 180 (timer 0:00), got %v", u.ElapsedSeconds)
	}
}
