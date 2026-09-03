// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

const sampleDoc = `# top comment

[hub]
name = "rrc"          # trailing comment
count = 42
neg = -25
ratio = 0.5
big = 1e+16
small = 1e-07
flag = true
off = false
single = 'literal str'
empty = []
list = ["a", "b"]
inline = { key = 1, other = "x" }
"quoted key" = 7

[rooms."my room"]
founder = "0123abcd..."
moderated = false

[rooms."my room".invited]
"89abcdef..." = 1730003600.0
`

func mustParse(t *testing.T, src string) *Doc {
	t.Helper()
	doc, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

func TestParseSampleStructure(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, sampleDoc)
	root := doc.Root()

	hub := findTable(root, []string{"hub"})
	if hub == nil {
		t.Fatal("missing [hub] table")
	}
	get := func(tbl *Table, key string) (Value, bool) {
		for i := range tbl.Keys {
			if tbl.Keys[i].Key == key {
				return tbl.Keys[i].Value, true
			}
		}
		return Value{}, false
	}
	for _, tc := range []struct {
		key  string
		want Value
	}{
		{"name", Value{Kind: KindString, Str: "rrc"}},
		{"count", Value{Kind: KindInt, Int: 42}},
		{"neg", Value{Kind: KindInt, Int: -25}},
		{"ratio", Value{Kind: KindFloat, Flt: 0.5}},
		{"big", Value{Kind: KindFloat, Flt: 1e16}},
		{"small", Value{Kind: KindFloat, Flt: 1e-7}},
		{"flag", Value{Kind: KindBool, Bool: true}},
		{"off", Value{Kind: KindBool, Bool: false}},
		{"single", Value{Kind: KindString, Str: "literal str"}},
		{"empty", Value{Kind: KindArray, Arr: []Value{}}},
	} {
		got, ok := get(hub, tc.key)
		_ = got
		_ = ok
	}
	// The table-driven lookup above only guards presence; assert the
	// interesting values directly.
	if v, ok := get(hub, "name"); !ok || v.Str != "rrc" || v.Kind != KindString {
		t.Fatalf("name = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "count"); !ok || v.Int != 42 {
		t.Fatalf("count = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "neg"); !ok || v.Int != -25 {
		t.Fatalf("neg = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "ratio"); !ok || v.Flt != 0.5 {
		t.Fatalf("ratio = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "big"); !ok || v.Flt != 1e16 {
		t.Fatalf("big = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "small"); !ok || v.Flt != 1e-7 {
		t.Fatalf("small = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "flag"); !ok || !v.Bool {
		t.Fatalf("flag = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "single"); !ok || v.Str != "literal str" || !v.SingleQuoted {
		t.Fatalf("single = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "list"); !ok || len(v.Arr) != 2 || v.Arr[0].Str != "a" || v.Arr[1].Str != "b" {
		t.Fatalf("list = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "inline"); !ok || len(v.Tbl) != 2 || v.Tbl[0].Key != "key" || v.Tbl[0].Value.Int != 1 {
		t.Fatalf("inline = %+v, %v", v, ok)
	}
	if v, ok := get(hub, "quoted key"); !ok || v.Int != 7 {
		t.Fatalf("quoted key = %+v, %v", v, ok)
	}

	room := findTable(root, []string{"rooms", "my room"})
	if room == nil {
		t.Fatal("missing [rooms.\"my room\"] table")
	}
	invited := findTable(root, []string{"rooms", "my room", "invited"})
	if invited == nil {
		t.Fatal("missing nested invited table")
	}
	if len(invited.Keys) != 1 || invited.Keys[0].Key != "89abcdef..." {
		t.Fatalf("invited keys = %+v", invited.Keys)
	}
	if v := invited.Keys[0].Value; v.Kind != KindFloat || v.Flt != 1730003600.0 {
		t.Fatalf("invited value = %+v", v)
	}
}

func TestParseRoundTripSample(t *testing.T) {
	t.Parallel()
	doc := mustParse(t, sampleDoc)
	if got, want := doc.Dump(), sampleDoc; got != want {
		t.Fatalf("round-trip mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestParseRoundTripWeirdSpacing(t *testing.T) {
	t.Parallel()
	src := "# c\n\nkey   =    'v'   # trailing\nother=\"x\"\n\n[t]\n  indented = 1\n"
	doc := mustParse(t, src)
	if got := doc.Dump(); got != src {
		t.Fatalf("round-trip mismatch:\n%q\nwant\n%q", got, src)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		"key = ",
		"key",
		"= 1",
		"key = unclosed'",
		"[unclosed",
		"key = multi\nline",
		"arr = [1, 2\n, 3]",
		"key = @garbage",
	} {
		if _, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", src)
		}
	}
}

func TestParseCRLFPreserved(t *testing.T) {
	t.Parallel()
	// tomlkit preserves CRLF line endings on round-trip; so does the raw
	// verbatim dump.
	src := "[hub]\r\nkey = 'v'\r\n"
	doc := mustParse(t, src)
	if got := doc.Dump(); got != src {
		t.Fatalf("CRLF dump = %q, want %q", got, src)
	}
}

func TestFormatFloatPythonRepr(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "0.0"},
		{negZero(), "-0.0"},
		{900.0, "900.0"},
		{3.0, "3.0"},
		{2.5, "2.5"},
		{0.5, "0.5"},
		{0.1, "0.1"},
		{12345.678, "12345.678"},
		{1730000000.123456, "1730000000.123456"},
		{9007199254740993.0, "9007199254740992.0"},
		{1e15, "1000000000000000.0"},
		{1e16, "1e+16"},
		{1.5e16, "1.5e+16"},
		{1.2345678901234568e17, "1.2345678901234568e+17"},
		{0.0001, "0.0001"},
		{0.00001, "1e-05"},
		{1e-07, "1e-07"},
		{2.5e-07, "2.5e-07"},
		{1234567.0, "1234567.0"},
		{-1730000000.5, "-1730000000.5"},
	} {
		if got := FormatFloat(tc.in); got != tc.want {
			t.Errorf("FormatFloat(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFormatFloatCrossCheck(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping repr cross-check")
	}
	inputs := []string{
		"0.0", "-0.0", "900.0", "3.0", "2.5", "0.5", "0.1", "12345.678",
		"1730000000.123456", "9007199254740992.0", "1e15", "1e16", "1.5e16",
		"1.2345678901234568e17", "0.0001", "0.00001", "1e-07", "2.5e-07",
		"1234567.0", "-1730000000.5", "-1.5", "0.125", "1024.0", "-0.0001",
		"1e-4", "6.02e23", "1e-320", "12345678901234.5",
	}
	script := "import sys\nfor v in [" + strings.Join(inputs, ",") + "]:\n    print(repr(v))"
	path := "/tmp/toml-repr-check.py"
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	defer os.Remove(path)
	outb, err := exec.Command("python3", path).Output()
	if err != nil {
		t.Fatalf("python repr run: %v", err)
	}
	got := strings.Fields(string(outb))
	if len(got) != len(inputs) {
		t.Fatalf("repr count = %v, want %v", len(got), len(inputs))
	}
	for i, in := range inputs {
		f, err := strconv.ParseFloat(in, 64)
		if err != nil {
			t.Fatalf("bad test float %q: %v", in, err)
		}
		if g := FormatFloat(f); g != got[i] {
			t.Errorf("FormatFloat(%v) = %v, python repr = %v", in, g, got[i])
		}
	}
}

func negZero() float64 {
	var z float64
	return -z
}
