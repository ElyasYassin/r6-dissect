package dissect

import (
	"bytes"
	"encoding/binary"

	"github.com/rs/zerolog/log"
)

// Y10S4+ player health component.
//
// Every player slot owns a health component object, linked during the prep phase by
//
//	1B <slot id: 4> 00 00 00 00 41 54 DC C4 <health id: 4> 00 00 00 00 0C 98 C6 3F ...
//
// Its properties are written either as the first property of a reference record
// (23 <health id> 00 00 00 00 <hash> <len> <value>) or as a continuation (22 <hash> <len> <value>)
// directly after one. Observed properties (Y11S2 and Y11S3):
//
//	25 26 76 C9  uint32  current health (100/110/125 at spawn, 20 after a revive, 0 when downed or dead)
//	E7 88 F6 A5  uint32  health state (see below)
//	72 5E 99 F9  float32 bleed-out progress while downed, 0 -> 1 (~0.028/s)
//	91 5F DF 38  uint8   set while downed (toggles while being revived/interrogated, meaning unconfirmed)
//
// Health state values: 0 healthy, 2 wounded (health <= 20, e.g. after a revive), 3 downed (DBNO),
// 4 dead. A down is a transition into 3; it ends with 4 (death, sometimes via a transient 2 right
// before the kill feed record), a revive (2 with health reset to 20), or the end of the round.
var (
	healthLinkIndicator  = []byte{0x41, 0x54, 0xDC, 0xC4}
	healthStateIndicator = []byte{0xE7, 0x88, 0xF6, 0xA5, 0x04}
)

const (
	healthStateHealthy = 0
	healthStateWounded = 2
	healthStateDowned  = 3
	healthStateDead    = 4
	// continuation records follow their reference record closely (max 59 bytes observed)
	healthOwnerSearchWindow = 128
)

// readHealthLink links a health component to a player slot.
func readHealthLink(r *Reader) error {
	start := r.offset - len(healthLinkIndicator)
	if start < 9 || r.offset+8 > len(r.b) {
		return nil
	}
	if r.b[start-9] != 0x1B || !isZero(r.b[start-4:start]) || !isZero(r.b[r.offset+4:r.offset+8]) {
		return nil
	}
	var health, slot [4]byte
	copy(health[:], r.b[r.offset:r.offset+4])
	copy(slot[:], r.b[start-8:start-4])
	if health == [4]byte{} {
		return nil
	}
	r.healthToSlot[health] = slot
	return nil
}

// healthRecordOwner returns the health component that a property record starting at start belongs to.
func (r *Reader) healthRecordOwner(start int) (owner [4]byte, ok bool) {
	if start >= 9 && r.b[start-9] == 0x23 && isZero(r.b[start-4:start]) {
		copy(owner[:], r.b[start-8:start-4])
		_, ok = r.healthToSlot[owner]
		return owner, ok
	}
	if start < 1 || r.b[start-1] != 0x22 {
		return owner, false
	}
	limit := start - healthOwnerSearchWindow
	if limit < 0 {
		limit = 0
	}
	for p := start - 10; p >= limit; p-- {
		if r.b[p] != 0x23 || !isZero(r.b[p+5:p+9]) {
			continue
		}
		copy(owner[:], r.b[p+1:p+5])
		if _, known := r.healthToSlot[owner]; known {
			return owner, true
		}
	}
	return owner, false
}

func (r *Reader) playerIndexByHealth(health [4]byte) int {
	slot, ok := r.healthToSlot[health]
	if !ok {
		return -1
	}
	for i, p := range r.Header.Players {
		if bytes.Equal(p.slotID, slot[:]) {
			return i
		}
	}
	return -1
}

func readHealthState(r *Reader) error {
	start := r.offset - len(healthStateIndicator)
	if r.offset+4 > len(r.b) {
		return nil
	}
	state := binary.LittleEndian.Uint32(r.b[r.offset : r.offset+4])
	owner, ok := r.healthRecordOwner(start)
	if !ok {
		return nil
	}
	idx := r.playerIndexByHealth(owner)
	if idx < 0 || !r.clock.started {
		return nil
	}
	r.healthSeen = true
	prev, seen := r.healthState[idx]
	r.healthState[idx] = state
	if seen && prev == state {
		return nil
	}
	username := r.Header.Players[idx].Username
	r.healthTransitions = append(r.healthTransitions, healthTransition{
		player:  idx,
		state:   state,
		elapsed: r.elapsed(),
		timeRaw: r.timeRaw,
		time:    r.time,
		phase:   r.clock.phase,
	})
	log.Debug().
		Str("username", username).
		Uint32("state", state).
		Uint32("prev", prev).
		Msg("health_state")
	if state != healthStateDowned {
		return nil
	}
	u := MatchUpdate{
		Type:          DBNO,
		Target:        username,
		Time:          r.timeRaw,
		TimeInSeconds: r.time,
		fromHealth:    true,
	}
	r.stamp(&u)
	// buffered, so that the kill feed parser (which looks back for earlier events of the same
	// target) is not affected; merged into the feedback in applyHealthDBNO
	r.healthDowns = append(r.healthDowns, healthDown{
		index:      len(r.MatchFeedback),
		transition: len(r.healthTransitions) - 1,
		update:     u,
	})
	log.Debug().Interface("match_update", u).Msg("dbno from health state")
	return nil
}

type healthDown struct {
	index      int // len(MatchFeedback) when the down was read
	transition int // index of the down in healthTransitions
	update     MatchUpdate
}

type healthTransition struct {
	player  int
	state   uint32
	elapsed float64
	timeRaw string
	time    float64
	phase   RoundPhase
}

// healthDeathWindow is how soon after leaving the downed state a death must follow for the down
// to count as finished rather than revived (the state briefly passes through 2 before 4).
const healthDeathWindow = 1.5

// downRevived reports whether the down was followed by a revive, i.e. the player left the downed
// state and did not die right after, and returns the transition out of the downed state.
func (r *Reader) downRevived(d healthDown) (healthTransition, bool) {
	var none healthTransition
	if d.transition < 0 || d.transition >= len(r.healthTransitions) {
		return none, false
	}
	player := r.healthTransitions[d.transition].player
	if player < 0 || player >= len(r.Header.Players) {
		return none, false
	}
	var next []healthTransition
	for _, t := range r.healthTransitions[d.transition+1:] {
		if t.player == player {
			next = append(next, t)
			if len(next) == 2 {
				break
			}
		}
	}
	if len(next) == 0 || next[0].state == healthStateDead {
		return none, false
	}
	if len(next) == 2 && next[1].state == healthStateDead && next[1].elapsed-next[0].elapsed <= healthDeathWindow {
		return none, false
	}
	target := r.Header.Players[player].Username
	for _, u := range r.MatchFeedback {
		if u.Type == Kill && u.Target == target && u.ElapsedSeconds >= next[0].elapsed && u.ElapsedSeconds-next[0].elapsed <= healthDeathWindow {
			return none, false
		}
	}
	return next[0], true
}

// healthReviveWindow bounds the time between a revive and the reviver's +50 score update.
const healthReviveWindow = 3.0

// takeScoreReviver finds the teammate whose score revive bonus matches a revive and consumes it,
// so that detectRevivesFromScore does not record the revive again.
func (r *Reader) takeScoreReviver(target int, at float64) string {
	best, bestDiff := -1, healthReviveWindow
	for i, c := range r.pendingRevives {
		idx := r.PlayerIndexByUsername(c.Username)
		if idx < 0 || idx == target || r.Header.Players[idx].TeamIndex != r.Header.Players[target].TeamIndex {
			continue
		}
		diff := c.ElapsedSeconds - at
		if diff < 0 {
			diff = -diff
		}
		if diff <= bestDiff {
			best, bestDiff = i, diff
		}
	}
	if best < 0 {
		return ""
	}
	username := r.pendingRevives[best].Username
	r.pendingRevives = append(r.pendingRevives[:best], r.pendingRevives[best+1:]...)
	return username
}

// applyHealthDBNO replaces kill-feed DBNO heuristics with the downs read from the health state
// (when available) and credits each down to the player that killed the downed player, if they died.
// Downs that were revived have no known instigator (the replay does not say who dealt the damage);
// a Revive event is added for them, credited to the teammate with a matching +50 score update.
func (r *Reader) applyHealthDBNO() {
	if !r.healthSeen {
		return
	}
	var revives []MatchUpdate
	for i, d := range r.healthDowns {
		t, revived := r.downRevived(d)
		r.healthDowns[i].update.revived = revived
		if !revived {
			continue
		}
		revives = append(revives, MatchUpdate{
			Type:           Revive,
			Username:       r.takeScoreReviver(t.player, t.elapsed),
			Target:         r.Header.Players[t.player].Username,
			Time:           t.timeRaw,
			TimeInSeconds:  t.time,
			Phase:          t.phase,
			ElapsedSeconds: t.elapsed,
		})
	}
	for i := len(r.healthDowns) - 1; i >= 0; i-- {
		d := r.healthDowns[i]
		at := d.index
		if at > len(r.MatchFeedback) {
			at = len(r.MatchFeedback)
		}
		r.MatchFeedback = append(r.MatchFeedback, MatchUpdate{})
		copy(r.MatchFeedback[at+1:], r.MatchFeedback[at:])
		r.MatchFeedback[at] = d.update
	}
	r.healthDowns = nil
	for _, u := range revives {
		r.insertMatchUpdateByTime(u)
	}
	kept := r.MatchFeedback[:0]
	for _, u := range r.MatchFeedback {
		if u.Type == DBNO && !u.fromHealth {
			continue
		}
		kept = append(kept, u)
	}
	r.MatchFeedback = kept
	for i := range r.MatchFeedback {
		if r.MatchFeedback[i].Type == Kill {
			r.MatchFeedback[i].DBNOBy = ""
			r.MatchFeedback[i].FinishedBy = ""
		}
	}
	for i := range r.MatchFeedback {
		d := &r.MatchFeedback[i]
		if d.Type != DBNO || d.revived {
			continue
		}
		for j := i + 1; j < len(r.MatchFeedback); j++ {
			u := &r.MatchFeedback[j]
			if u.Type == Revive && u.Target == d.Target {
				break
			}
			if u.Type == DBNO && u.Target == d.Target {
				break
			}
			if (u.Type == Kill && u.Target == d.Target) || (u.Type == Death && u.Username == d.Target) {
				if u.Type == Kill {
					d.Username = u.Username
					u.DBNOBy = u.Username
				}
				break
			}
		}
	}
}
