package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, season, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "dissect", "test", "data", "replays", "valid", season, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("missing fixture: %v", err)
	}
	return p
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

func TestRun_Segments(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"segments", fixture(t, "Y11S3", "custom_clubhouse_short.rec")}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	lines := nonEmptyLines(out.String())
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last["summary"] != true || last["unplacedFraction"].(float64) > 0.005 {
		t.Fatal(last)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"channel", "hasHeader", "id", "kind", "start", "end", "envelopes", "tickFirst", "tickLast"} {
		if _, ok := first[k]; !ok {
			t.Errorf("channel line lacks %q: %v", k, first)
		}
	}
}

func TestRun_TLVLimit(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"tlv", "--limit", "3", fixture(t, "Y11S3", "custom_clubhouse_short.rec")}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	lines := nonEmptyLines(out.String())
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	var tok map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &tok); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"tick", "offset", "op", "object", "field"} {
		if _, ok := tok[k]; !ok {
			t.Errorf("token lacks %q: %v", k, tok)
		}
	}
}

func TestRun_Movement(t *testing.T) {
	path := fixture(t, "Y11S3", "custom_clubhouse_short.rec")
	var out, errb bytes.Buffer
	if code := run([]string{"movement", "--limit", "2", "--no-payload", path}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	lines := nonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, ln := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatal(err)
		}
		if _, ok := rec["entity"]; !ok {
			t.Errorf("record lacks entity: %v", rec)
		}
		if _, ok := rec["payload"]; ok {
			t.Errorf("--no-payload kept payload: %v", rec)
		}
	}
	// Without the flag the payload is present.
	out.Reset()
	errb.Reset()
	if code := run([]string{"movement", "--limit", "1", path}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(nonEmptyLines(out.String())[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec["payload"]; !ok {
		t.Errorf("payload missing by default: %v", rec)
	}
}

func TestRun_Coverage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"coverage", fixture(t, "Y11S3", "custom_clubhouse_short.rec")}, &out, &errb); code != 0 {
		t.Fatal(code, errb.String())
	}
	lines := nonEmptyLines(out.String())
	if len(lines) != 1 {
		t.Fatalf("lines = %d", len(lines))
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatal(err)
	}
	if obj["tlv"].(map[string]any)["errors"] != 0.0 {
		t.Fatal(obj["tlv"])
	}
	if obj["movement"].(map[string]any)["records"].(float64) <= 0 {
		t.Fatal(obj["movement"])
	}
}

func TestRun_Errors(t *testing.T) { // Review Focus 5
	for _, args := range [][]string{
		{}, {"bogus", "x.rec"}, {"segments"}, {"segments", "/nonexistent.rec"}, {"segments", t.TempDir()},
	} {
		var out, errb bytes.Buffer
		code := run(args, &out, &errb)
		if code == 0 || out.Len() != 0 || len(nonEmptyLines(errb.String())) != 1 {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out.String(), errb.String())
		}
	}
}
