package wire_test

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/redraskal/r6-dissect/dissect"
	wire "github.com/redraskal/r6-dissect/dissect/wire"
	"github.com/rs/zerolog"
)

func TestMain(m *testing.M) {
	zerolog.SetGlobalLevel(zerolog.Disabled)
	os.Exit(m.Run())
}

const validDir = "../test/data/replays/valid"

// fixtureRecs returns the absolute, sorted paths of every *.rec under the given seasons.
func fixtureRecs(t *testing.T, seasons ...string) []string {
	t.Helper()
	var out []string
	for _, s := range seasons {
		m, err := filepath.Glob(filepath.Join(validDir, s, "*.rec"))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range m {
			abs, err := filepath.Abs(p)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatalf("no .rec fixtures for seasons %v", seasons)
	}
	return out
}

// fixture returns the absolute path of one fixture.
func fixture(t *testing.T, season, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join(validDir, season, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("missing fixture: %v", err)
	}
	return p
}

// firstRec returns the basename of the first .rec of a season.
func firstRec(t *testing.T, season string) string {
	t.Helper()
	return filepath.Base(fixtureRecs(t, season)[0])
}

// decompress opens path with dissect.NewReader and returns the decompressed buffer.
func decompress(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := dissect.NewReader(f)
	if err != nil {
		t.Fatalf("NewReader(%s): %v", path, err)
	}
	return r.Data()
}

func le16(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }
func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func mustHex(s string, max int) []byte {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) < 1 || len(b) > max {
		panic("bad hex " + s)
	}
	return b
}

// h builds a Handle from 1..8 hex bytes copied into the front of a zero Handle.
func h(hexs string) wire.Handle {
	var out wire.Handle
	copy(out[:], mustHex(hexs, 8))
	return out
}

// fid builds a FieldID from 8 hex chars in file byte order.
func fid(hex8 string) wire.FieldID {
	var out wire.FieldID
	if len(hex8) != 8 {
		panic("bad field id " + hex8)
	}
	copy(out[:], mustHex(hex8, 4))
	return out
}

// cid builds a ClassID from 8 hex chars in file byte order.
func cid(hex8 string) wire.ClassID {
	var out wire.ClassID
	if len(hex8) != 8 {
		panic("bad class id " + hex8)
	}
	copy(out[:], mustHex(hex8, 4))
	return out
}

// channelHeader builds the 45-byte header: 03 b9 00 00, 8 zero bytes, id, u32, u32, 17 zero bytes.
func channelHeader(id [8]byte) []byte {
	return concat([]byte{0x03, 0xb9, 0, 0}, make([]byte, 8), id[:], le32(1), le32(2), make([]byte, 17))
}

// envelope parses le32(tick)+le32(len)+le32(0)+payload at offset 0; it panics if that does not parse.
func envelope(tick uint32, payload []byte) wire.Envelope {
	e, ok := wire.ParseEnvelopeAt(envBytes(tick, payload), 0)
	if !ok {
		panic("envelope does not parse")
	}
	return e
}

func envBytes(tick uint32, payload []byte) []byte {
	return concat(le32(tick), le32(uint32(len(payload))), le32(0), payload)
}

// assertTicksMonotonic reports an error when an envelope's tick is lower than its predecessor's.
func assertTicksMonotonic(t *testing.T, c wire.Channel) {
	t.Helper()
	for i := 1; i < len(c.Envelopes); i++ {
		if c.Envelopes[i].Tick < c.Envelopes[i-1].Tick {
			t.Errorf("channel at %d: envelope %d tick %d < previous %d", c.Start, i, c.Envelopes[i].Tick, c.Envelopes[i-1].Tick)
			return
		}
	}
}
