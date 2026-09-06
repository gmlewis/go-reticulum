// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// skipIfNoPythonCBOR skips the test when python3 without cbor2 is all that is
// available; the cross-implementation round-trips below are meaningless then.
func skipIfNoPythonCBOR(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping cbor cross-implementation test")
	}
	cmd := exec.Command("python3", "-c", "import cbor2")
	if err := cmd.Run(); err != nil {
		t.Skip("python3 cbor2 not available; skipping cbor cross-implementation test")
	}
}

// goToPythonSamples builds the Go-side values whose encodings Python must
// decode to exactly the same structure (including key order).
func goToPythonSamples(t *testing.T) []any {
	t.Helper()
	notice := env(goldenMid8, goldenTS, goldenSrc32, int64(21), int64(5), "#foo", int64(6), "hi")
	caps := NewMap()
	caps.Set(int64(1), true)
	caps.Set(int64(2), true)
	caps.Set(int64(0), true)
	limits := NewMap()
	limits.Set(int64(0), int64(32))
	limits.Set(int64(1), int64(64))
	limits.Set(int64(2), int64(350))
	limits.Set(int64(3), int64(32))
	limits.Set(int64(4), int64(240))
	body := NewMap()
	body.Set(int64(0), "rrc")
	body.Set(int64(1), "0.1")
	body.Set(int64(2), caps)
	body.Set(int64(3), limits)
	welcome := env(goldenMid8, goldenTS, goldenSrc32, int64(2), int64(6), body)
	appData := NewMap()
	appData.Set("proto", "rrc")
	appData.Set("v", int64(1))
	appData.Set("hub", "rrc")
	boolKey := NewMap()
	boolKey.Set(true, int64(7))
	mixed := NewMap()
	mixed.Set("txt", "café ☕")
	mixed.Set("bin", []byte{0x00, 0xff})
	mixed.Set("neg", int64(-25))
	mixed.Set("big", int64(4294967296))
	mixed.Set("f", 12345.678)
	return []any{notice, welcome, appData, boolKey, mixed,
		[]any{int64(1), int64(-25), "t", []byte("b"), 0.5, true, nil, []any{}, NewMap()},
		NewMap(), []any{}, "", 0.0}
}

// expectedPythonLiteral mirrors goToPythonSamples as Python literals; order
// matters, so the script compares list(dict.items()).
const expectedPythonLiteral = `
[
    {0:1, 1:21, 2:bytes.fromhex("aabbccddee112233"), 3:1700000000000,
     4:bytes(range(32)), 5:"#foo", 6:"hi"},
    {0:1, 1:2, 2:bytes.fromhex("aabbccddee112233"), 3:1700000000000,
     4:bytes(range(32)),
     6:{0:"rrc", 1:"0.1", 2:{1:True, 2:True, 0:True},
        3:{0:32, 1:64, 2:350, 3:32, 4:240}}},
    {"proto":"rrc", "v":1, "hub":"rrc"},
    {True: 7},
    {"txt":"café ☕", "bin":b"\x00\xff",
     "neg":-25, "big":4294967296, "f":12345.678},
    [1, -25, "t", b"b", 0.5, True, None, [], {}],
    {},
    [],
    "",
    0.0,
]
`

const goToPythonScript = `
import cbor2, sys
lines = [l.strip() for l in open(sys.argv[1]).read().splitlines() if l.strip()]
expected = eval(sys.argv[2])
assert len(lines) == len(expected), (len(lines), len(expected))
for i, (hx, want) in enumerate(zip(lines, expected)):
    got = cbor2.loads(bytes.fromhex(hx))
    if isinstance(want, dict):
        if not isinstance(got, dict):
            raise AssertionError("sample %d: type %r, want dict" % (i, type(got)))
        if list(got.items()) != list(want.items()):
            raise AssertionError("sample %d:\n got %r\nwant %r" % (i, list(got.items()), want))
    elif got != want:
        raise AssertionError("sample %d: got %r, want %r" % (i, got, want))
print("OK", len(lines))
`

// TestCrossImplGoToPython encodes representative values with the Go encoder
// and verifies Python's cbor2 decodes them to the identical structure and
// insertion order.
func TestCrossImplGoToPython(t *testing.T) {
	t.Parallel()
	skipIfNoPythonCBOR(t)

	samples := goToPythonSamples(t)
	dir := testutils.TempDir(t, "cbor-ximpl-")
	hexLines := make([]string, len(samples))
	for i, s := range samples {
		hexLines[i] = strings.ToLower(encodeHexForTest(Encode(s)))
	}
	path := filepath.Join(dir, "samples.hex")
	if err := os.WriteFile(path, []byte(strings.Join(hexLines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write samples: %v", err)
	}
	out := runPythonWithCBOR(t, goToPythonScript, path, expectedPythonLiteral)
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python cross-decode failed:\n%s", out)
	}
}

// pythonToGoSamples lists Python literals whose cbor2 encodings the Go
// decoder must reproduce exactly (including the bool key).
const pythonToGoScript = `
import cbor2, sys
samples = [
    {True: 7},
    {0:1, 1:True, 2:2.5, 3:b"bin", 4:"txt é", 5:[1, 2], 6:{}},
    {0: 4294967296},
    {1: -1000},
    [1.0, None, False],
    "plain",
    3.141592653589793,
    b"\x00\xff\xfe",
    {0: {1: bytes(range(4))}},
]
for s in samples:
    print(cbor2.dumps(s).hex())
`

func TestCrossImplPythonToGo(t *testing.T) {
	t.Parallel()
	skipIfNoPythonCBOR(t)

	out := runPythonWithCBOR(t, pythonToGoScript)
	assertPythonToGo(t, strings.TrimSpace(out))
}

func assertPythonToGo(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) != 9 {
		t.Fatalf("got %d sample lines, want 9: %q", len(lines), out)
	}
	// {True: 7}
	m0 := mustDecode(t, lines[0]).(*Map)
	if m0.Len() != 1 {
		t.Fatalf("sample 0 len %v", m0.Len())
	}
	if got := keys(m0); len(got) != 1 || got[0] != true {
		t.Fatalf("sample 0 keys = %v, want [true]", got)
	}
	if v, ok := m0.Get(int64(1)); !ok || v != int64(7) {
		t.Fatalf("sample 0 Get(1) = %v, %v; want 7", v, ok)
	}
	// Mixed-type map, in order.
	m1 := mustDecode(t, lines[1]).(*Map)
	if got := keys(m1); len(got) != 7 {
		t.Fatalf("sample 1 keys = %v", got)
	}
	if v, ok := m1.GetBytes(int64(3)); !ok || string(v) != "bin" {
		t.Fatalf("sample 1 GetBytes(3) = %v, %v", v, ok)
	}
	if v, ok := m1.GetString(int64(4)); !ok || v != "txt é" {
		t.Fatalf("sample 1 GetString(4) = %v, %v", v, ok)
	}
	f, ok := m1.Get(int64(2))
	if !ok || f != 2.5 {
		t.Fatalf("sample 1 Get(2) = %#v, %v; want 2.5", f, ok)
	}
	// Wide unsigned int.
	m2 := mustDecode(t, lines[2]).(*Map)
	if v, ok := m2.Get(int64(0)); !ok || v != int64(4294967296) {
		t.Fatalf("sample 2 = %#v, %v", v, ok)
	}
	// Negative int.
	m3 := mustDecode(t, lines[3]).(*Map)
	if v, ok := m3.Get(int64(1)); !ok || v != int64(-1000) {
		t.Fatalf("sample 3 = %#v, %v", v, ok)
	}
	// Array of [1.0, None, False].
	arr := mustDecode(t, lines[4]).([]any)
	if len(arr) != 3 || arr[0] != 1.0 || arr[1] != nil || arr[2] != false {
		t.Fatalf("sample 4 = %#v", arr)
	}
	// Plain text.
	if v := mustDecode(t, lines[5]); v != "plain" {
		t.Fatalf("sample 5 = %#v", v)
	}
	// Double float.
	if v := mustDecode(t, lines[6]); v != 3.141592653589793 {
		t.Fatalf("sample 6 = %#v", v)
	}
	// Byte string.
	if v := mustDecode(t, lines[7]); string(v.([]byte)) != "\x00\xff\xfe" {
		t.Fatalf("sample 7 = %#v", v)
	}
	// Nested map with byte value.
	m8 := mustDecode(t, lines[8]).(*Map)
	inner, ok := m8.GetMap(int64(0))
	if !ok {
		t.Fatalf("sample 8 GetMap(0) failed")
	}
	if v, ok := inner.GetBytes(int64(1)); !ok || string(v) != "\x00\x01\x02\x03" {
		t.Fatalf("sample 8 inner bytes = %#v, %v", v, ok)
	}
}

func runPythonWithCBOR(t *testing.T, script string, args ...string) string {
	t.Helper()
	dir := testutils.TempDir(t, "cbor-ximpl-")
	scriptPath := filepath.Join(dir, "script.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}
	cmd := exec.Command("python3", append([]string{scriptPath}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python3 script failed: %v\n--- stderr ---\n%s\n--- stdout ---\n%s",
			err, stderr.String(), stdout.String())
	}
	return stdout.String()
}

func encodeHexForTest(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}
