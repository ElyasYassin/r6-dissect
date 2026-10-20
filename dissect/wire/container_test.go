package wire_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"path/filepath"
	"testing"

	wire "github.com/redraskal/r6-dissect/dissect/wire"
)

func tlvPayload() []byte {
	return concat([]byte{0x04}, le32(1), []byte{0x22, 0x22, 0x22}, make([]byte, 12))
}

func movePayload() []byte {
	return concat(le16(1), make([]byte, 8), le32(6), []byte{0x60, 0x73, 0x85, 0xfe}, []byte{0, 0})
}

// unknownPayload starts with 0x04 but has a non-zero tail: it must not sniff as TLV.
func unknownPayload() []byte {
	return []byte{0x04, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
}

func TestParseEnvelopeAt(t *testing.T) {
	b := concat(le32(7), le32(3), le32(0), []byte{1, 2, 3})
	e, ok := wire.ParseEnvelopeAt(b, 0)
	if !ok || e.Tick != 7 || e.Start != 0 || e.End != 15 || !bytes.Equal(e.Payload, []byte{1, 2, 3}) {
		t.Fatalf("%+v %v", e, ok)
	}
	if _, ok := wire.ParseEnvelopeAt(b[:14], 0); ok {
		t.Fatal("len past buffer must not parse")
	}
	if _, ok := wire.ParseEnvelopeAt(b, -1); ok {
		t.Fatal("negative offset must not parse")
	}
	bad := concat(le32(7), le32(3), le32(9), []byte{1, 2, 3})
	if _, ok := wire.ParseEnvelopeAt(bad, 0); ok {
		t.Fatal("non-zero third word must not parse")
	}
	zero := concat(le32(7), le32(0), le32(0))
	if _, ok := wire.ParseEnvelopeAt(zero, 0); ok {
		t.Fatal("zero len must not parse")
	}
	huge := concat(le32(7), le32(0xffffffff), le32(0), []byte{1})
	if _, ok := wire.ParseEnvelopeAt(huge, 0); ok {
		t.Fatal("huge len must not parse")
	}
}

func TestParseChannelHeaderAt(t *testing.T) {
	hd := channelHeader([8]byte{0xaa, 1, 2, 3, 4, 5, 6, 7})
	id, ok := wire.ParseChannelHeaderAt(hd, 0)
	if !ok || id[0] != 0xaa || id[7] != 7 {
		t.Fatal(id, ok)
	}
	if _, ok := wire.ParseChannelHeaderAt(hd[:44], 0); ok {
		t.Fatal("short header")
	}
	mut := func(f func(h []byte)) []byte {
		h := append([]byte(nil), hd...)
		f(h)
		return h
	}
	free := mut(func(h []byte) { h[0], h[1], h[4] = 0x6b, 0x98, 0x0a; h[20], h[22] = 7, 9; h[33], h[35] = 0x41, 5 })
	if _, ok := wire.ParseChannelHeaderAt(free, 0); !ok {
		t.Fatal("lead, byte 4, off 20 and off 33 are free")
	}
	for name, h := range map[string][]byte{
		"zero lead":      mut(func(h []byte) { h[0], h[1] = 0, 0 }),
		"high lead half": mut(func(h []byte) { h[2] = 1 }),
		"pad at 5":       mut(func(h []byte) { h[5] = 1 }),
		"pad at 11":      mut(func(h []byte) { h[11] = 1 }),
		"zero id":        mut(func(h []byte) { copy(h[12:20], make([]byte, 8)) }),
		"byte 24":        mut(func(h []byte) { h[24] = 1 }),
		"count 1":        mut(func(h []byte) { h[25] = 1 }),
		"count 0":        mut(func(h []byte) { h[25] = 0 }),
		"pad at 29":      mut(func(h []byte) { h[29] = 1 }),
		"pad at 37":      mut(func(h []byte) { h[37] = 1 }),
		"pad at 44":      mut(func(h []byte) { h[44] = 1 }),
	} {
		if _, ok := wire.ParseChannelHeaderAt(h, 0); ok {
			t.Errorf("%s must not parse", name)
		}
	}
}

const lead = 1000

func TestWalk_Synthetic(t *testing.T) {
	prelude := []byte{0xff, 0xff, 0xff, 0xff, 0xff}
	id1 := [8]byte{1}
	id2 := [8]byte{2}
	b := concat(prelude,
		envBytes(1, tlvPayload()),
		channelHeaderN(id1, lead, 2, 0), envBytes(2, movePayload()), envBytes(3, movePayload()),
		channelHeaderN(id2, lead, 1, 0), envBytes(4, unknownPayload()),
		terminator(lead),
	)
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 3 {
		t.Fatalf("channels = %d", len(l.Channels))
	}
	c0, c1, c2 := l.Channels[0], l.Channels[1], l.Channels[2]
	if c0.HasHeader || c0.Kind != wire.PayloadTLV || len(c0.Envelopes) != 1 || c0.Start != 5 {
		t.Errorf("c0 %+v", c0)
	}
	if !c1.HasHeader || c1.ID != id1 || c1.Kind != wire.PayloadMovement || len(c1.Envelopes) != 2 || c1.Start != c0.End {
		t.Errorf("c1 %+v", c1)
	}
	if !c2.HasHeader || c2.ID != id2 || c2.Kind != wire.PayloadUnknown || len(c2.Envelopes) != 1 || c2.Start != c1.End {
		t.Errorf("c2 %+v", c2)
	}
	if l.Prelude != 5 {
		t.Errorf("prelude = %d", l.Prelude)
	}
	if len(l.Gaps) != 1 || l.Gaps[0] != (wire.Gap{Start: c2.End, End: len(b)}) {
		t.Errorf("gaps = %v want the terminator [{%d %d}]", l.Gaps, c2.End, len(b))
	}
	if l.UnplacedBytes() != 5+12 {
		t.Errorf("unplaced = %d", l.UnplacedBytes())
	}
	if got := len(l.ChannelsOf(wire.PayloadMovement)); got != 1 {
		t.Errorf("movement channels = %d", got)
	}
	if wire.PayloadTLV.String() != "tlv" || wire.PayloadMovement.String() != "movement" || wire.PayloadUnknown.String() != "unknown" {
		t.Error("kind strings")
	}
}

// F1: bytes 33..36 are a free u32 (1345 in the fixtures), not part of a zero
// tail. A 45-byte header carrying one must still open a channel with all of its
// envelopes and leave no gap but the terminator.
func TestWalk_HeaderWithNonZeroU33(t *testing.T) {
	b := concat(channelHeaderN([8]byte{5}, lead, 3, 1345),
		envBytes(2, movePayload()), envBytes(3, movePayload()), envBytes(4, movePayload()), terminator(lead))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 3 {
		t.Fatalf("channels %+v", l.Channels)
	}
	if len(l.Gaps) != 1 || l.Gaps[0].End-l.Gaps[0].Start != 12 || l.Gaps[0].End != len(b) {
		t.Fatalf("gaps = %v want only the 12-byte terminator", l.Gaps)
	}
}

// F2: a header with u33 = 4 must not lend its tail to a phantom tick-0 envelope;
// the first envelope starts right after the 45 header bytes.
func TestWalk_NoPhantomEnvelopeInHeaderTail(t *testing.T) {
	b := concat(channelHeaderN([8]byte{5}, lead, 2, 4), envBytes(2, movePayload()), envBytes(3, movePayload()), terminator(lead))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 {
		t.Fatalf("channels %+v", l.Channels)
	}
	c := l.Channels[0]
	if len(c.Envelopes) != 2 || c.Envelopes[0].Start != c.Start+45 || c.Envelopes[0].Tick != 2 {
		t.Fatalf("envelopes %+v", c.Envelopes)
	}
}

// Bare envelopes inside a gap are never adopted, whatever their ticks.
func TestWalk_GapEnvelopesNeverAdopted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		t1, t2 uint32
	}{
		{"ticks above lead (F3)", lead + 10, lead + 11},
		{"ticks within lead", 500, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			garbage := concat([]byte{0xee, 0xee}, envBytes(tc.t1, []byte{9}), envBytes(tc.t2, []byte{9}))
			b := concat(channelHeaderN([8]byte{1}, lead, 0, 0),
				channelHeaderN([8]byte{2}, lead, 1, 0), envBytes(2, movePayload()), garbage)
			l, err := wire.Walk(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(l.Channels) != 2 || len(l.Channels[1].Envelopes) != 1 {
				t.Fatalf("channels %+v", l.Channels)
			}
			if len(l.Gaps) != 1 || l.Gaps[0] != (wire.Gap{Start: l.Channels[1].End, End: len(b)}) {
				t.Fatalf("gaps = %v want one running from %d to %d", l.Gaps, l.Channels[1].End, len(b))
			}
		})
	}
}

// An envelope stamped after the lead ends its channel and opens a gap.
func TestWalk_TickAboveLeadIsAGap(t *testing.T) {
	b := concat(channelHeaderN([8]byte{1}, lead, 0, 0),
		channelHeaderN([8]byte{2}, lead, 2, 0), envBytes(2, movePayload()), envBytes(lead+1, movePayload()),
		channelHeaderN([8]byte{3}, lead, 1, 0), envBytes(5, movePayload()), terminator(lead))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 3 || len(l.Channels[1].Envelopes) != 1 || len(l.Channels[2].Envelopes) != 1 {
		t.Fatalf("channels %+v", l.Channels)
	}
	bad := l.Channels[1].End
	if len(l.Gaps) != 2 || l.Gaps[0] != (wire.Gap{Start: bad, End: l.Channels[2].Start}) {
		t.Fatalf("gaps = %v want the bad envelope [%d %d] then the terminator", l.Gaps, bad, l.Channels[2].Start)
	}
}

// Ticks never decrease inside a channel.
func TestWalk_TickBelowPreviousIsAGap(t *testing.T) {
	b := concat(channelHeaderN([8]byte{1}, lead, 0, 0),
		channelHeaderN([8]byte{2}, lead, 2, 0), envBytes(50, movePayload()), envBytes(49, movePayload()), terminator(lead))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 2 || len(l.Channels[1].Envelopes) != 1 || len(l.Gaps) != 1 || l.Gaps[0].End != len(b) {
		t.Fatalf("channels %+v gaps %v", l.Channels, l.Gaps)
	}
}

// A header-shaped run is not a first header unless its envelopes chain to a
// same-lead header or the terminator. The fixtures' float runs satisfy the
// header's zero anchors but have neither.
func TestWalk_HeaderShapedRunInPreludeIsNotAHeader(t *testing.T) {
	fake := concat(channelHeaderN([8]byte{0xf7, 0xe9, 0x94, 0x89, 0x5d}, 0x3f80, 3, 0), bytes.Repeat([]byte{0xee}, 12))
	b := concat(fake, channelHeaderN([8]byte{7}, lead, 2, 0), envBytes(2, movePayload()), envBytes(3, movePayload()), terminator(lead))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if l.Prelude != len(fake) {
		t.Fatalf("prelude = %d, want %d", l.Prelude, len(fake))
	}
	if len(l.Channels) != 1 || l.Channels[0].ID != [8]byte{7} {
		t.Fatalf("channels %+v", l.Channels)
	}
}

// An empty channel (header straight into the next header) keeps both headers.
func TestWalk_HeaderFollowedByHeader(t *testing.T) {
	b := concat(channelHeader2(1), channelHeader2(2), envBytes(2, movePayload()), envBytes(3, movePayload()), terminator(lead))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 2 || len(l.Channels[0].Envelopes) != 0 || len(l.Channels[1].Envelopes) != 2 || len(l.Gaps) != 1 {
		t.Fatalf("channels %+v gaps %v", l.Channels, l.Gaps)
	}
}

// channelHeader2 is an empty-channel header for the test lead.
func channelHeader2(n byte) []byte { return channelHeaderN([8]byte{n}, lead, 0, 0) }

// Cutting inside the last envelope keeps the envelopes before it and ends in a gap to the cut.
func TestWalk_TruncatedMidEnvelope(t *testing.T) {
	full := concat(channelHeaderN([8]byte{1}, lead, 3, 0), envBytes(2, movePayload()), envBytes(3, movePayload()), envBytes(4, movePayload()))
	b := full[:len(full)-4]
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 2 {
		t.Fatalf("%+v", l.Channels)
	}
	if len(l.Gaps) != 1 || l.Gaps[0] != (wire.Gap{Start: l.Channels[0].End, End: len(b)}) {
		t.Fatalf("gaps %v, want one from %d to the buffer end %d", l.Gaps, l.Channels[0].End, len(b))
	}
}

// F5: the cut payload holds an envelope-shaped run that ends exactly at the cut.
// The overrunning envelope ends the walk with a gap to the buffer end, and
// nothing inside it is adopted.
func TestWalk_CutPayloadHoldingEnvelopeRun(t *testing.T) {
	inner := envBytes(7, []byte{1, 2, 3, 4})
	tail := bytes.Repeat([]byte{0xaa}, 40)
	payload := concat(bytes.Repeat([]byte{0xbb}, 20), inner, tail)
	e3 := envBytes(4, payload)
	full := concat(channelHeaderN([8]byte{1}, lead, 3, 0), envBytes(2, movePayload()), envBytes(3, movePayload()), e3)
	b := full[:len(full)-len(tail)]
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 2 {
		t.Fatalf("channels %+v", l.Channels)
	}
	start := l.Channels[0].End
	if len(l.Gaps) != 1 || l.Gaps[0] != (wire.Gap{Start: start, End: len(b)}) {
		t.Fatalf("gaps %v, want [{%d %d}]", l.Gaps, start, len(b))
	}
}

func TestWalk_Empty(t *testing.T) {
	for _, in := range [][]byte{nil, {1, 2, 3}, make([]byte, 100)} {
		l, err := wire.Walk(in)
		if !errors.Is(err, wire.ErrNoEnvelopes) {
			t.Fatalf("%v: %v", in, err)
		}
		if l.Size != len(in) || l.Prelude != len(in) || len(l.Channels) != 0 {
			t.Fatalf("%+v", l)
		}
	}
}

// wantChannels is the header count in each Y11 fixture.
var wantChannels = map[string]int{
	"custom_early_plant.rec":     11,
	"custom_solid_snake.rec":     11,
	"custom_clubhouse_short.rec": 10,
	"custom_fortress_plant.rec":  10,
	"custom_lair_plant_stop.rec": 10,
}

func TestWalk_Fixtures(t *testing.T) {
	for _, path := range fixtureRecs(t, "Y11S2", "Y11S3") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b := decompress(t, path)
			l, err := wire.Walk(b)
			if err != nil {
				t.Fatal(err)
			}
			want, ok := wantChannels[filepath.Base(path)]
			if !ok {
				t.Fatalf("no expected channel count for %s", filepath.Base(path))
			}
			if len(l.Channels) != want {
				t.Errorf("channels = %d, want %d", len(l.Channels), want)
			}
			if got := l.UnplacedBytes(); got != l.Prelude+12 {
				t.Errorf("unplaced = %d, want prelude %d + 12", got, l.Prelude)
			}
			var recs int
			for _, c := range l.Channels {
				assertTicksMonotonic(t, c)
				if !c.HasHeader {
					continue
				}
				if n, cnt := len(c.Envelopes), int(binary.LittleEndian.Uint32(b[c.Start+25:]))-2; n != cnt {
					t.Errorf("channel at %d: %d envelopes, header count-2 = %d", c.Start, n, cnt)
				}
				ld := binary.LittleEndian.Uint32(b[c.Start:])
				for _, e := range c.Envelopes {
					if e.Tick > ld {
						t.Errorf("channel at %d: tick %d > lead %d", c.Start, e.Tick, ld)
						break
					}
				}
			}
			if n := len(l.ChannelsOf(wire.PayloadTLV)); n < 1 {
				t.Errorf("tlv channels = %d", n)
			}
			mv := l.ChannelsOf(wire.PayloadMovement)
			if len(mv) < 1 {
				t.Fatal("no movement channel")
			}
			for _, c := range mv {
				recs += len(c.Envelopes)
			}
			if recs < 1000 {
				t.Errorf("movement envelopes = %d", recs)
			}
			t.Logf("size=%d unplaced=%d prelude=%d channels=%d tlv=%d movement=%d movement_envelopes=%d gaps=%d",
				l.Size, l.UnplacedBytes(), l.Prelude, len(l.Channels), len(l.ChannelsOf(wire.PayloadTLV)), len(mv), recs, len(l.Gaps))
		})
	}
}

// F4: the Y8 layout has no channel headers; nothing in it may be taken for one.
func TestWalk_LegacyLayout(t *testing.T) {
	for _, path := range fixtureRecs(t, "Y8S1", "Y8S2") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b := decompress(t, path)
			l, err := wire.Walk(b)
			if !errors.Is(err, wire.ErrNoEnvelopes) {
				t.Fatalf("err = %v, want ErrNoEnvelopes", err)
			}
			if len(l.Channels) != 0 || l.Prelude != l.Size || l.Size != len(b) {
				t.Fatalf("channels=%d prelude=%d size=%d", len(l.Channels), l.Prelude, l.Size)
			}
			t.Logf("err=%v channels=%d prelude=%d size=%d", err, len(l.Channels), l.Prelude, l.Size)
		})
	}
}

// F5: cutting a fixture at any point yields either no layout or a layout whose
// envelopes are a prefix of the full one and whose last gap runs to the cut.
func TestWalk_Truncated(t *testing.T) {
	for _, path := range fixtureRecs(t, "Y11S2", "Y11S3") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			full := decompress(t, path)
			fl, err := wire.Walk(full)
			if err != nil {
				t.Fatal(err)
			}
			type span struct{ s, e int }
			known := map[span]bool{}
			for _, c := range fl.Channels {
				for _, e := range c.Envelopes {
					known[span{e.Start, e.End}] = true
				}
			}
			var noLayout, withGap int
			for i := 1; i <= 50; i++ {
				cut := len(full) * i / 50
				b := full[:cut]
				l, err := wire.Walk(b)
				if errors.Is(err, wire.ErrNoEnvelopes) {
					noLayout++
					if len(l.Channels) != 0 || l.Prelude != cut {
						t.Errorf("cut %d: ErrNoEnvelopes with channels=%d prelude=%d", cut, len(l.Channels), l.Prelude)
					}
					continue
				}
				if err != nil {
					t.Fatalf("cut %d: %v", cut, err)
				}
				if n := len(l.Gaps); n == 0 || l.Gaps[n-1].End != cut {
					t.Errorf("cut %d: gaps %v, want the last to end at the cut", cut, l.Gaps)
				} else {
					withGap++
				}
				for _, c := range l.Channels {
					if c.End > cut {
						t.Errorf("cut %d: channel past buffer: %d", cut, c.End)
					}
					for _, e := range c.Envelopes {
						if !known[span{e.Start, e.End}] {
							t.Errorf("cut %d: envelope [%d %d) is not in the full layout", cut, e.Start, e.End)
						}
					}
				}
			}
			t.Logf("cuts=50 no_layout=%d last_gap_at_cut=%d", noLayout, withGap)
		})
	}
}
