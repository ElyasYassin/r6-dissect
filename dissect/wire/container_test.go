package wire_test

import (
	"bytes"
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
	other := append([]byte(nil), hd...)
	other[0], other[1], other[4] = 0x6b, 0x98, 0x0a
	if _, ok := wire.ParseChannelHeaderAt(other, 0); !ok {
		t.Fatal("leading u32 and byte 4 are free")
	}
	dirty := append([]byte(nil), hd...)
	dirty[44] = 1
	if _, ok := wire.ParseChannelHeaderAt(dirty, 0); ok {
		t.Fatal("non-zero trailer must not parse")
	}
	dirty = append([]byte(nil), hd...)
	dirty[11] = 1
	if _, ok := wire.ParseChannelHeaderAt(dirty, 0); ok {
		t.Fatal("non-zero pad must not parse")
	}
}

func TestWalk_Synthetic(t *testing.T) {
	prelude := []byte{0xff, 0xff, 0xff, 0xff, 0xff}
	junk := []byte{0xee, 0xee, 0xee}
	id1 := [8]byte{1}
	id2 := [8]byte{2}
	b := concat(prelude,
		envBytes(1, tlvPayload()),
		channelHeader(id1), envBytes(2, movePayload()), envBytes(3, movePayload()),
		junk,
		channelHeader(id2), envBytes(4, unknownPayload()),
	)
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 3 {
		t.Fatalf("channels = %d", len(l.Channels))
	}
	c0, c1, c2 := l.Channels[0], l.Channels[1], l.Channels[2]
	if c0.HasHeader || c0.Kind != wire.PayloadTLV || len(c0.Envelopes) != 1 {
		t.Errorf("c0 %+v", c0)
	}
	if !c1.HasHeader || c1.ID != id1 || c1.Kind != wire.PayloadMovement || len(c1.Envelopes) != 2 {
		t.Errorf("c1 %+v", c1)
	}
	if !c2.HasHeader || c2.ID != id2 || c2.Kind != wire.PayloadUnknown || len(c2.Envelopes) != 1 {
		t.Errorf("c2 %+v", c2)
	}
	if l.Prelude != 5 {
		t.Errorf("prelude = %d", l.Prelude)
	}
	gs := c1.End
	if len(l.Gaps) != 1 || l.Gaps[0] != (wire.Gap{Start: gs, End: gs + 3}) {
		t.Errorf("gaps = %v want [{%d %d}]", l.Gaps, gs, gs+3)
	}
	if l.UnplacedBytes() != 8 {
		t.Errorf("unplaced = %d", l.UnplacedBytes())
	}
	if got := len(l.ChannelsOf(wire.PayloadMovement)); got != 1 {
		t.Errorf("movement channels = %d", got)
	}
	if wire.PayloadTLV.String() != "tlv" || wire.PayloadMovement.String() != "movement" || wire.PayloadUnknown.String() != "unknown" {
		t.Error("kind strings")
	}
}

// A lone envelope-shaped run inside a gap must not end the gap.
func TestWalk_GapLoneEnvelopeIgnored(t *testing.T) {
	fake := envBytes(99, []byte{9, 9}) // parses alone, followed by garbage
	garbage := concat([]byte{0xee, 0xee}, fake, []byte{0xee, 0xee, 0xee})
	b := concat(channelHeader([8]byte{1}), envBytes(2, movePayload()), garbage, envBytes(3, movePayload()), envBytes(4, movePayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 3 {
		t.Fatalf("channels %+v", l.Channels)
	}
	if len(l.Gaps) != 1 || l.Gaps[0].End-l.Gaps[0].Start != len(garbage) {
		t.Fatalf("gaps = %v want one of %d bytes", l.Gaps, len(garbage))
	}
}

// Accepted limit: a coincidental pair of valid envelopes inside a gap is taken as real.
func TestWalk_GapCoincidentalPairIsAccepted(t *testing.T) {
	garbage := concat([]byte{0xee, 0xee}, envBytes(98, []byte{9}), envBytes(99, []byte{9}))
	b := concat(channelHeader([8]byte{1}), envBytes(2, movePayload()), garbage, envBytes(3, movePayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Gaps) != 1 || l.Gaps[0].End-l.Gaps[0].Start != 2 {
		t.Fatalf("gaps = %v: the leading 2 junk bytes only", l.Gaps)
	}
	if n := len(l.Channels[0].Envelopes); n != 4 {
		t.Fatalf("envelopes = %d, want the coincidental pair adopted (4)", n)
	}
}

// shortHeader is the trailer form: the 28 shared bytes and one zero, with the
// first envelope following directly instead of 16 more zero bytes.
func shortHeader(lead uint16, id [8]byte) []byte {
	return concat(le16(lead), make([]byte, 10), id[:], le32(1), le32(2), []byte{0})
}

// Regression: a run of float zeros (00 00 80 3f, then zeros) satisfies the header's
// zero anchors. The fixtures' first stage took such a run, a few bytes into the
// prelude, for the first channel header. A real header is followed by an
// envelope or another header; the run has neither.
func TestWalk_HeaderShapedRunInPreludeIsNotAHeader(t *testing.T) {
	fake := concat([]byte{0x80, 0x3f, 0, 0}, make([]byte, 8), []byte{0xf7, 0xe9, 0x94, 0x89, 0x5d, 0, 0, 0}, le32(1), le32(2), make([]byte, 17))
	garbage := []byte{0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee, 0xee}
	prelude := concat(fake, garbage)
	b := concat(prelude, channelHeader([8]byte{7}), envBytes(2, movePayload()), envBytes(3, movePayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if l.Prelude != len(prelude) {
		t.Fatalf("prelude = %d, want %d", l.Prelude, len(prelude))
	}
	if len(l.Channels) != 1 || l.Channels[0].ID != [8]byte{7} {
		t.Fatalf("channels %+v", l.Channels)
	}
}

// An empty channel (header straight into the next header) keeps both headers.
func TestWalk_HeaderFollowedByHeader(t *testing.T) {
	b := concat(channelHeader([8]byte{1}), channelHeader([8]byte{2}), envBytes(2, movePayload()), envBytes(3, movePayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 2 || len(l.Channels[0].Envelopes) != 0 || len(l.Channels[1].Envelopes) != 2 || len(l.Gaps) != 0 {
		t.Fatalf("channels %+v gaps %v", l.Channels, l.Gaps)
	}
}

// Regression: the trailer channels after the last game tick carry a 29-byte header.
func TestWalk_TrailerShortHeader(t *testing.T) {
	b := concat(channelHeader([8]byte{1}), envBytes(2, movePayload()), envBytes(3, movePayload()),
		shortHeader(0x4c61, [8]byte{9, 9}), envBytes(0, []byte{0, 0, 0, 0}), envBytes(6, unknownPayload()), envBytes(7, unknownPayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 2 || len(l.Gaps) != 0 {
		t.Fatalf("channels %+v gaps %v", l.Channels, l.Gaps)
	}
	c := l.Channels[1]
	if !c.HasHeader || c.ID != [8]byte{9, 9} || len(c.Envelopes) != 3 || c.Start != l.Channels[0].End {
		t.Fatalf("trailer channel %+v", c)
	}
}

// Regression: an ordinary envelope can have the short header's shape (small tick,
// a non-zero length byte at offset 4, zeros after it, a non-zero 8 bytes at offset
// 12, a zero at offset 28). On the Lair round that cut a channel in half and
// swallowed 7 MB into a gap. An envelope that parses must stay an envelope.
func TestWalk_EnvelopeShapedLikeShortHeaderStaysAnEnvelope(t *testing.T) {
	payload := make([]byte, 0xde)
	copy(payload[0:8], []byte{1, 2, 3, 4, 5, 6, 7, 8}) // bytes 12..20 of the envelope: a non-zero "id"
	// Byte 28 is zero and two envelopes start at byte 29, so every check the short
	// form makes passes; only the rule "a parsing envelope wins" keeps it an envelope.
	copy(payload[17:], concat(envBytes(0, []byte{1, 2, 3, 4}), envBytes(1, []byte{1, 2, 3, 4})))
	look := envBytes(0x4775, payload)
	if look[4] == 0 || look[28] != 0 {
		t.Fatal("test setup: not header-shaped")
	}
	b := concat(channelHeader([8]byte{1}), envBytes(2, movePayload()), look, envBytes(0x4776, movePayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 3 || len(l.Gaps) != 0 {
		t.Fatalf("channels %+v gaps %v", l.Channels, l.Gaps)
	}
}

// Ticks never decrease inside a channel, so a coincidental envelope pair older than
// the channel's last tick does not end the gap; the newer real pair does.
func TestWalk_GapPairOlderThanChannelIsIgnored(t *testing.T) {
	older := concat(envBytes(5, []byte{9}), envBytes(6, []byte{9}))
	garbage := concat([]byte{0xee, 0xee}, older)
	b := concat(channelHeader([8]byte{1}), envBytes(50, movePayload()), garbage, envBytes(60, movePayload()), envBytes(61, movePayload()))
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 3 {
		t.Fatalf("channels %+v", l.Channels)
	}
	if len(l.Gaps) != 1 || l.Gaps[0].End-l.Gaps[0].Start != len(garbage) {
		t.Fatalf("gaps = %v want one of %d bytes", l.Gaps, len(garbage))
	}
	assertTicksMonotonic(t, l.Channels[0])
}

func TestWalk_TruncatedMidEnvelope(t *testing.T) {
	full := concat(channelHeader([8]byte{1}), envBytes(2, movePayload()), envBytes(3, movePayload()))
	b := full[:len(full)-4]
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Channels) != 1 || len(l.Channels[0].Envelopes) != 1 {
		t.Fatalf("%+v", l.Channels)
	}
	if len(l.Gaps) != 1 || l.Gaps[0].End != len(b) {
		t.Fatalf("gaps %v, want one running to the buffer end %d", l.Gaps, len(b))
	}
}

func TestWalk_Empty(t *testing.T) {
	for _, in := range [][]byte{nil, {1, 2, 3}, make([]byte, 100)} {
		l, err := wire.Walk(in)
		if !errors.Is(err, wire.ErrNoEnvelopes) {
			t.Fatalf("%v: %v", in, err)
		}
		if l.Size != len(in) {
			t.Fatal("size")
		}
	}
}

func TestWalk_Fixtures(t *testing.T) {
	for _, path := range fixtureRecs(t, "Y11S2", "Y11S3") {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b := decompress(t, path)
			l, err := wire.Walk(b)
			if err != nil {
				t.Fatal(err)
			}
			frac := float64(l.UnplacedBytes()) / float64(l.Size)
			if frac > 0.005 {
				t.Errorf("unplaced %.3f%%", frac*100)
			}
			if n := len(l.ChannelsOf(wire.PayloadTLV)); n < 1 {
				t.Errorf("tlv channels = %d", n)
			}
			mv := l.ChannelsOf(wire.PayloadMovement)
			if len(mv) < 1 {
				t.Fatal("no movement channel")
			}
			var recs int
			for _, c := range mv {
				recs += len(c.Envelopes)
			}
			if recs < 1000 {
				t.Errorf("movement envelopes = %d", recs)
			}
			for _, c := range l.Channels {
				assertTicksMonotonic(t, c)
			}
			t.Logf("size=%d unplaced=%d (%.4f%%) channels=%d tlv=%d movement=%d movement_envelopes=%d gaps=%d prelude=%d",
				l.Size, l.UnplacedBytes(), frac*100, len(l.Channels), len(l.ChannelsOf(wire.PayloadTLV)), len(mv), recs, len(l.Gaps), l.Prelude)
		})
	}
}

func TestWalk_LegacyLayoutDoesNotPanic(t *testing.T) {
	b := decompress(t, fixture(t, "Y8S1", firstRec(t, "Y8S1")))
	l, err := wire.Walk(b)
	if err != nil && !errors.Is(err, wire.ErrNoEnvelopes) {
		t.Fatal(err)
	}
	if l.Size != len(b) {
		t.Fatal("size")
	}
	t.Logf("err=%v channels=%d unplaced=%d of %d", err, len(l.Channels), l.UnplacedBytes(), l.Size)
}

func TestWalk_Truncated(t *testing.T) {
	b := decompress(t, fixtureRecs(t, "Y11S3")[0])
	b = b[:len(b)/2]
	l, err := wire.Walk(b)
	if err != nil {
		t.Fatal(err)
	}
	if l.Size != len(b) {
		t.Fatal("size")
	}
	for _, c := range l.Channels {
		if c.End > len(b) {
			t.Fatalf("channel past buffer: %d > %d", c.End, len(b))
		}
	}
}
