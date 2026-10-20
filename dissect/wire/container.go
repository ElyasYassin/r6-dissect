package wire

import (
	"encoding/binary"
	"errors"
)

// PayloadKind is the sniffed format of a channel's envelope payloads.
type PayloadKind int

const (
	PayloadUnknown  PayloadKind = iota
	PayloadTLV                  // payload[0] == 0x04 and payload ends with 12 zero bytes
	PayloadMovement             // payload = u16 count + count records whose tag ends 73 85 fe
)

func (k PayloadKind) String() string {
	switch k {
	case PayloadTLV:
		return "tlv"
	case PayloadMovement:
		return "movement"
	}
	return "unknown"
}

// Envelope is one tick-stamped payload: u32 tick, u32 len, u32 0, payload[len].
type Envelope struct {
	Start, End int // [Start, End) of the whole envelope in the buffer
	Tick       uint32
	Payload    []byte // slice of the buffer, len == the envelope's len field
}

// Channel is a run of envelopes introduced by a 45-byte header (or, for the
// leading channel, by nothing).
type Channel struct {
	Start, End int     // header start to end of the last envelope
	HasHeader  bool    // false for the leading channel that starts without a header
	ID         [8]byte // the header's id8 (zero when HasHeader is false)
	Kind       PayloadKind
	Envelopes  []Envelope
}

// Gap is a run of bytes not covered by a channel header or envelope.
type Gap struct{ Start, End int }

// Layout is the result of Walk.
type Layout struct {
	Size     int
	Prelude  int // bytes before the first channel or envelope
	Channels []Channel
	Gaps     []Gap // excludes the prelude
}

// UnplacedBytes is Prelude plus the size of every gap.
func (l Layout) UnplacedBytes() int {
	n := l.Prelude
	for _, g := range l.Gaps {
		n += g.End - g.Start
	}
	return n
}

// ChannelsOf returns the channels whose Kind == kind, in buffer order.
func (l Layout) ChannelsOf(kind PayloadKind) []Channel {
	var out []Channel
	for _, c := range l.Channels {
		if c.Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// ErrNoEnvelopes is returned when the buffer holds no recognizable channel or envelope.
var ErrNoEnvelopes = errors.New("wire: no channels or envelopes found")

const (
	channelHeaderLen = 45
	// shortHeaderLen is the 28 bytes both header forms share plus one zero. The
	// trailer channels (after the last game tick) carry this form: the 17 zero
	// bytes of the full header are replaced by the first envelope's own words.
	shortHeaderLen = 28
)

// ParseChannelHeaderAt reports whether a 45-byte channel header starts at p:
// a free u32, 8 bytes that are zero except for an optional first byte, id8,
// u32, u32 and 17 zero bytes.
//
// The leading u32 is 03 b9 00 00 in the Fortress round but 6b 98 00 00 in
// others (it reads like the stream's last tick), so it is not matched, only
// shaped: a non-zero low half and a zero high half. Byte 4 is 0a in one
// channel and zero elsewhere. Walk only trusts a header that is also followed
// by an envelope, another header or the end of the buffer (see headerAt).
func ParseChannelHeaderAt(b []byte, p int) (id [8]byte, ok bool) {
	if p < 0 || p > len(b)-channelHeaderLen {
		return id, false
	}
	id, ok = headerBaseAt(b, p)
	if !ok || !allZero(b[p+28:p+channelHeaderLen]) {
		return id, false
	}
	return id, true
}

// headerBaseAt checks the 28 bytes shared by both header forms.
func headerBaseAt(b []byte, p int) (id [8]byte, ok bool) {
	if p < 0 || p > len(b)-shortHeaderLen {
		return id, false
	}
	h := b[p : p+shortHeaderLen]
	// A small number, so the upper half is zero and the lower half is not.
	// This rejects float runs such as 00 00 80 3f.
	if (h[0] == 0 && h[1] == 0) || h[2] != 0 || h[3] != 0 || !allZero(h[5:12]) {
		return id, false
	}
	copy(id[:], h[12:20])
	if id == ([8]byte{}) {
		return id, false
	}
	return id, true
}

// ParseEnvelopeAt parses u32 tick, u32 len, u32 zero, payload[len] at p.
// ok is false when p+12 > len(b), the zero word is not zero, len == 0, or p+12+len > len(b).
func ParseEnvelopeAt(b []byte, p int) (Envelope, bool) {
	if p < 0 || p > len(b)-12 {
		return Envelope{}, false
	}
	if binary.LittleEndian.Uint32(b[p+8:]) != 0 {
		return Envelope{}, false
	}
	n := int(binary.LittleEndian.Uint32(b[p+4:]))
	if n == 0 || n > len(b)-p-12 {
		return Envelope{}, false
	}
	return Envelope{
		Start:   p,
		End:     p + 12 + n,
		Tick:    binary.LittleEndian.Uint32(b[p:]),
		Payload: b[p+12 : p+12+n : p+12+n],
	}, true
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// sniffPayload classifies one envelope payload.
func sniffPayload(p []byte) PayloadKind {
	if len(p) >= 13 && p[0] == 0x04 && allZero(p[len(p)-12:]) {
		return PayloadTLV
	}
	// u16 count, then record 0: handle8 u32 L tag4; tag = 6x 73 85 fe.
	if len(p) >= 2+12+4 && binary.LittleEndian.Uint16(p) > 0 &&
		p[2+8+4+1] == 0x73 && p[2+8+4+2] == 0x85 && p[2+8+4+3] == 0xfe {
		return PayloadMovement
	}
	return PayloadUnknown
}

// headerAt finds a channel header at p and returns its length: the 45-byte
// form, or the 29-byte trailer form (28 shared bytes, one zero, then an
// envelope). Either must be followed by an envelope, another header or the end
// of the buffer, which is how a header-shaped run inside float data is told
// from a real header: it has none of these after it. The short form is much
// less distinctive, so only callers standing exactly on an envelope boundary
// (allowShort) accept it; scans over unknown bytes do not.
func headerAt(b []byte, p int, allowShort bool) (id [8]byte, n int, ok bool) {
	if id, ok = ParseChannelHeaderAt(b, p); ok {
		q := p + channelHeaderLen
		if q == len(b) {
			return id, channelHeaderLen, true
		}
		if _, ok = ParseEnvelopeAt(b, q); ok {
			return id, channelHeaderLen, true
		}
		if _, ok = ParseChannelHeaderAt(b, q); ok {
			return id, channelHeaderLen, true
		}
	}
	if !allowShort {
		return id, 0, false
	}
	if _, isEnv := ParseEnvelopeAt(b, p); isEnv {
		// The short form's first 12 bytes read as an envelope whenever byte 4
		// is non-zero, and so does every ordinary envelope's header-shaped
		// prefix. A real envelope wins.
		return id, 0, false
	}
	id, ok = headerBaseAt(b, p)
	if !ok || p+shortHeaderLen >= len(b) || b[p+shortHeaderLen] != 0 {
		return id, 0, false
	}
	q := p + shortHeaderLen + 1
	e, ok := ParseEnvelopeAt(b, q)
	if !ok {
		return id, 0, false
	}
	if e.End == len(b) {
		return id, shortHeaderLen + 1, true
	}
	f, ok := ParseEnvelopeAt(b, e.End)
	return id, shortHeaderLen + 1, ok && f.Tick >= e.Tick
}

// startsAt reports whether a channel header, or a sniffable envelope that passes
// the same test as gap recovery, begins at p.
func startsAt(b []byte, p int) bool {
	if _, _, ok := headerAt(b, p, false); ok {
		return true
	}
	e, ok := ParseEnvelopeAt(b, p)
	return ok && sniffPayload(e.Payload) != PayloadUnknown && resyncAt(b, p, 0)
}

// resyncAt reports whether the walk may resume at p after a gap: a channel
// header, or an envelope that is itself followed by a channel header, the end
// of the buffer, or another envelope. Ticks are non-decreasing inside a
// channel, so the envelope must not be older than minTick (the last tick the
// open channel saw) and its follower must not be older than it. Requiring the
// follower keeps a random 12-byte coincidence from ending a gap, and the tick
// order keeps a coincidence that survives that from entering a channel.
func resyncAt(b []byte, p int, minTick uint32) bool {
	if _, _, ok := headerAt(b, p, false); ok {
		return true
	}
	e, ok := ParseEnvelopeAt(b, p)
	if !ok || e.Tick < minTick {
		return false
	}
	if e.End == len(b) {
		return true
	}
	if _, _, ok := headerAt(b, e.End, true); ok {
		return true
	}
	f, ok := ParseEnvelopeAt(b, e.End)
	return ok && f.Tick >= e.Tick
}

// Walk splits the decompressed buffer (dissect.Reader.Data()) into channels and envelopes.
func Walk(b []byte) (Layout, error) {
	l := Layout{Size: len(b)}
	p := 0
	for p < len(b) && !startsAt(b, p) {
		p++
	}
	if p >= len(b) {
		return Layout{Size: len(b), Prelude: len(b)}, ErrNoEnvelopes
	}
	l.Prelude = p

	cur := -1 // index of the open channel
	for p < len(b) {
		if id, n, ok := headerAt(b, p, true); ok {
			l.Channels = append(l.Channels, Channel{Start: p, End: p + n, HasHeader: true, ID: id})
			cur = len(l.Channels) - 1
			p += n
			continue
		}
		if e, ok := ParseEnvelopeAt(b, p); ok {
			if cur < 0 {
				l.Channels = append(l.Channels, Channel{Start: p})
				cur = 0
			}
			c := &l.Channels[cur]
			if len(c.Envelopes) == 0 {
				c.Kind = sniffPayload(e.Payload)
			}
			c.Envelopes = append(c.Envelopes, e)
			c.End = e.End
			p = e.End
			continue
		}
		start := p
		var minTick uint32
		if cur >= 0 {
			if n := len(l.Channels[cur].Envelopes); n > 0 {
				minTick = l.Channels[cur].Envelopes[n-1].Tick
			}
		}
		for p++; p < len(b) && !resyncAt(b, p, minTick); p++ {
		}
		l.Gaps = append(l.Gaps, Gap{Start: start, End: p})
	}
	return l, nil
}
