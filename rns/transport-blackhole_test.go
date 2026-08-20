// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// The following fixture bytes were captured from a live Python 1.3.5
// `umsgpack.packb` run (RNS.vendor.umsgpack) so the Go reload path is
// proven to read the exact on-disk format Python's persist_blackhole /
// remote-source files write. The dict keys are 16-byte identity hashes
// (msgpack bin), sub-entries are {until, reason} (or {source, until,
// reason} for the local file).
const (
	blackholeRemoteFixtureHex   = "83c410deadbeefdeadbeefdeadbeefdeadbeef82a5756e74696cc0a6726561736f6eaa72656d6f74652d696831c4101234567812345678123456781234567882a5756e74696ccb41d954fc40000000a6726561736f6eae72656d6f74652d65787069726564c410cafebabecafebabecafebabecafebabe82a5756e74696ccb420270b018000000a6726561736f6eaa72656d6f74652d696832"
	blackholeLocalFixtureHex    = "82c410feedfacefeedfacefeedfacefeedface83a6736f75726365c410aabbccddeeff00112233445566778899a5756e74696cc0a6726561736f6ea96c6f63616c2d696833c410deadbeefdeadbeefdeadbeefdeadbeef83a6736f75726365c410aabbccddeeff00112233445566778899a5756e74696cc0a6726561736f6ea96c6f63616c2d696831"
	blackholeDisabledFixtureHex = "81c410feedfacefeedfacefeedfacefeedface82a5756e74696cc0a6726561736f6eb464697361626c65642d73686f756c642d736b6970"
)

func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex decode %q: %v", s, err)
	}
	return b
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestReloadBlackhole golden-tests TransportSystem.reloadBlackholeAt
// (Python Transport.reload_blackhole, Transport.py:3183-3220) against a
// result captured from a live Python 1.3.5 run. The fixture files are the
// exact umsgpack bytes Python writes, so this also proves the Go msgpack
// reader accepts Python's on-disk format.
//
// Golden set (captured from Python): the pre-seeded locally-sourced ih1
// is preserved (the remote and local files' ih1 entries are ignored
// because the existing entry's source is own); ih2 is merged from the
// enabled remote source; ih3 is merged from the local file; the expired
// remote entry ihx is dropped; the disabled source file is skipped
// entirely.
func TestReloadBlackhole(t *testing.T) {
	t.Parallel()

	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")
	src := mustHexDecode(t, "112233445566778899aabbccddeeff00") // enabled source
	dis := mustHexDecode(t, "ff00ff00ff00ff00ff00ff00ff00ff00") // disabled source
	ih1 := mustHexDecode(t, "deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")
	ih3 := mustHexDecode(t, "feedfacefeedfacefeedfacefeedface")
	ihx := mustHexDecode(t, "12345678123456781234567812345678")

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: own}
	ts.blackholeSources = [][]byte{src}
	ts.blackholePath = t.TempDir()

	mustWriteFile(t, filepath.Join(ts.blackholePath, hex.EncodeToString(src)), mustHexDecode(t, blackholeRemoteFixtureHex))
	mustWriteFile(t, filepath.Join(ts.blackholePath, hex.EncodeToString(dis)), mustHexDecode(t, blackholeDisabledFixtureHex))
	mustWriteFile(t, filepath.Join(ts.blackholePath, "local"), mustHexDecode(t, blackholeLocalFixtureHex))

	// Pre-seed ih1 as locally-sourced. Python preserves an own-sourced
	// entry against every source (the per-identity `continue` when the
	// existing source == own), so neither the remote nor the local file's
	// ih1 entry may overwrite it.
	ts.blackholedIdentities[string(ih1)] = BlackholeIdentityEntry{
		IdentityHash: append([]byte(nil), ih1...),
		Source:       append([]byte(nil), own...),
		Reason:       "preseed-local",
	}

	// now is after ihx's expiry (1.7e9) and before ih2's expiry (9.9e9).
	now := time.Unix(1_800_000_000, 0)
	ts.reloadBlackholeAt(now)

	want := []struct {
		hash   []byte
		source []byte
		reason string
		unix   int64 // 0 => nil Until
	}{
		{ih1, own, "preseed-local", -1}, // Until == nil
		{ih2, src, "remote-ih2", 9_900_000_000},
		{ih3, own, "local-ih3", -1}, // Until == nil
	}

	if got, wantN := len(ts.blackholedIdentities), len(want); got != wantN {
		t.Fatalf("merged set size = %d, want %d", got, wantN)
	}
	for _, w := range want {
		got, ok := ts.blackholedIdentities[string(w.hash)]
		if !ok {
			t.Fatalf("missing expected blackholed identity %x", w.hash)
		}
		if !bytes.Equal(got.Source, w.source) {
			t.Fatalf("identity %x: source = %x, want %x", w.hash, got.Source, w.source)
		}
		if got.Reason != w.reason {
			t.Fatalf("identity %x: reason = %q, want %q", w.hash, got.Reason, w.reason)
		}
		if w.unix < 0 {
			if got.Until != nil {
				t.Fatalf("identity %x: until = %v, want nil", w.hash, got.Until)
			}
		} else {
			if got.Until == nil {
				t.Fatalf("identity %x: until = nil, want unix %d", w.hash, w.unix)
			}
			if gu := got.Until.Unix(); gu != w.unix {
				t.Fatalf("identity %x: until unix = %d, want %d", w.hash, gu, w.unix)
			}
		}
	}

	// Expired remote entry must be absent.
	if _, ok := ts.blackholedIdentities[string(ihx)]; ok {
		t.Fatalf("expired identity %x should not be in the blackhole set", ihx)
	}
}

// TestRemoveBlackholedPaths golden-tests
// TransportSystem.removeBlackholedPathsLocked (Python
// Transport.remove_blackholed_paths, Transport.py:3222-3241): only
// destinations whose recalled identity is blackholed are dropped; all
// others survive.
func TestRemoveBlackholedPaths(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(testSilentLogger())
	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")
	ts.identity = &Identity{Hash: own}

	// Two real identities whose hashes are derived from their public keys
	// (Recall materialises an identity via LoadPublicKey, so the recalled
	// hash equals TruncatedHash(pubkey)).
	blackholedID, err := NewIdentity(true, testSilentLogger())
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	survivingID, err := NewIdentity(true, testSilentLogger())
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	destA := bytes.Repeat([]byte{0xA1}, 16) // associated with blackholedID
	destB := bytes.Repeat([]byte{0xB2}, 16) // associated with survivingID

	now := float64(time.Now().UnixNano()) / 1e9
	ts.knownDestinations[string(destA)] = []any{now, []byte("pkt-a"), blackholedID.GetPublicKey(), nil}
	ts.knownDestinations[string(destB)] = []any{now, []byte("pkt-b"), survivingID.GetPublicKey(), nil}
	ts.pathTable[string(destA)] = &PathEntry{NextHop: []byte("nh-a")}
	ts.pathTable[string(destB)] = &PathEntry{NextHop: []byte("nh-b")}

	ts.blackholedIdentities[string(blackholedID.Hash)] = BlackholeIdentityEntry{
		IdentityHash: append([]byte(nil), blackholedID.Hash...),
		Source:       append([]byte(nil), own...),
	}

	ts.RemoveBlackholedPaths()

	if _, ok := ts.pathTable[string(destA)]; ok {
		t.Fatalf("destA (blackholed identity %x) should be removed from path table", blackholedID.Hash)
	}
	if _, ok := ts.pathTable[string(destB)]; !ok {
		t.Fatalf("destB (non-blackholed identity %x) should survive in path table", survivingID.Hash)
	}
}

// TestPersistBlackhole golden-tests TransportSystem.PersistBlackhole
// (Python Transport.persist_blackhole, Transport.py:3252-3275): only
// locally-sourced entries are written to blackholepath/local, each as
// {source: own, until, reason}. The result is compared (content, not
// byte order — Go map iteration is unordered whereas Python preserves
// insertion order) against the structure captured from a live Python
// persist_blackhole run.
func TestPersistBlackhole(t *testing.T) {
	t.Parallel()

	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")
	src := mustHexDecode(t, "112233445566778899aabbccddeeff00")
	ih1 := mustHexDecode(t, "deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")
	ih3 := mustHexDecode(t, "feedfacefeedfacefeedfacefeedface")

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: own}
	ts.blackholePath = t.TempDir()
	until2 := time.Unix(9_900_000_000, 0)

	ts.blackholedIdentities = map[string]BlackholeIdentityEntry{
		string(ih1): {IdentityHash: ih1, Source: own, Reason: "local-ih1", Until: nil},
		string(ih2): {IdentityHash: ih2, Source: own, Reason: "local-ih2", Until: &until2},
		string(ih3): {IdentityHash: ih3, Source: src, Reason: "remote-should-be-filtered", Until: nil},
	}

	ts.PersistBlackhole()

	data, err := os.ReadFile(filepath.Join(ts.blackholePath, "local"))
	if err != nil {
		t.Fatalf("reading persisted blackhole file: %v", err)
	}
	obj, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("unpacking persisted blackhole file: %v", err)
	}
	m, ok := obj.(map[any]any)
	if !ok {
		t.Fatalf("persisted blackhole payload type %T, want map", obj)
	}

	if got, want := len(m), 2; got != want {
		t.Fatalf("persisted %d entries, want %d (only own-sourced)", got, want)
	}
	assertBlackholeSubEntry(t, m, ih1, own, nil, "local-ih1")
	assertBlackholeSubEntry(t, m, ih2, own, float64(9_900_000_000), "local-ih2")
	if _, ok := blackholeSubEntry(m, ih3); ok {
		t.Fatalf("remote-sourced identity %x must be filtered out of the local file", ih3)
	}
}

// blackholeSubEntry returns the sub-entry map for identityHash from a
// msgpack-unpacked blackhole dict, or (nil, false) if absent.
func blackholeSubEntry(m map[any]any, identityHash []byte) (map[any]any, bool) {
	for k, v := range m {
		key := blackholeMapKey(k)
		if key != nil && bytes.Equal(key, identityHash) {
			se, ok := v.(map[any]any)
			if !ok {
				return nil, false
			}
			return se, true
		}
	}
	return nil, false
}

func assertBlackholeSubEntry(t *testing.T, m map[any]any, identityHash, source []byte, until any, reason string) {
	t.Helper()
	se, ok := blackholeSubEntry(m, identityHash)
	if !ok {
		t.Fatalf("persisted file missing identity %x", identityHash)
	}
	gotSource, _ := se["source"].([]byte)
	if !bytes.Equal(gotSource, source) {
		t.Fatalf("identity %x: source = %x, want %x", identityHash, gotSource, source)
	}
	gotReason, _ := se["reason"].(string)
	if gotReason != reason {
		t.Fatalf("identity %x: reason = %q, want %q", identityHash, gotReason, reason)
	}
	gotUntil := se["until"]
	if until == nil {
		if gotUntil != nil {
			t.Fatalf("identity %x: until = %v, want nil", identityHash, gotUntil)
		}
		return
	}
	gotUntilFloat, ok := gotUntil.(float64)
	if !ok {
		t.Fatalf("identity %x: until type %T, want float64", identityHash, gotUntil)
	}
	if gotUntilFloat != until.(float64) {
		t.Fatalf("identity %x: until = %v, want %v", identityHash, gotUntilFloat, until)
	}
}

// blackholeListHandlerGoldenHex is umsgpack.packb of the
// Transport.blackholed_identities dict captured from a live Python 1.3.5 run
// (RNS.vendor.umsgpack) for the three-entry set used by
// TestBlackholeListHandler. The dict keys are 16-byte identity hashes
// (msgpack bin), values are {source: bin, until: float|nil, reason: str}.
// This is the exact wire payload Python's blackhole_list_handler serves and
// Discovery.BlackholeUpdater consumes.
const blackholeListHandlerGoldenHex = "83c410deadbeefdeadbeefdeadbeefdeadbeef83a6736f75726365c410aabbccddeeff00112233445566778899a5756e74696cc0a6726561736f6ea96c6f63616c2d696831c410cafebabecafebabecafebabecafebabe83a6736f75726365c410112233445566778899aabbccddeeff00a5756e74696ccb420270b018000000a6726561736f6eaa72656d6f74652d696832c410feedfacefeedfacefeedfacefeedface83a6736f75726365c410aabbccddeeff00112233445566778899a5756e74696cc0a6726561736f6ea96c6f63616c2d696833"

// pythonBlackholeListPacked builds the same three-entry
// Transport.blackholed_identities dict TestBlackholeListHandler uses and packs
// it with Python's RNS.vendor.umsgpack.packb — the exact packer Python's
// blackhole_list_handler response path serves (Transport.py:3637-3644). The
// returned bytes are the live Python wire payload for the fixture set, so the
// hardcoded blackholeListHandlerGoldenHex constant can be validated against it
// and the Go handler's output compared structurally. Gated on SkipIfNoPythonRNS.
func pythonBlackholeListPacked(t *testing.T) []byte {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import sys
import RNS.vendor.umsgpack as umsgpack
own = bytes.fromhex('aabbccddeeff00112233445566778899')
src = bytes.fromhex('112233445566778899aabbccddeeff00')
ih1 = bytes.fromhex('deadbeefdeadbeefdeadbeefdeadbeef')
ih2 = bytes.fromhex('cafebabecafebabecafebabecafebabe')
ih3 = bytes.fromhex('feedfacefeedfacefeedfacefeedface')
d = {
  ih1: {"source": own, "until": None, "reason": "local-ih1"},
  ih2: {"source": src, "until": 9900000000.0, "reason": "remote-ih2"},
  ih3: {"source": own, "until": None, "reason": "local-ih3"},
}
sys.stdout.write(umsgpack.packb(d).hex())
`
	out := testutils.RunPython(t, script)
	b, err := hex.DecodeString(out)
	if err != nil {
		t.Fatalf("decode python blackhole-list hex %q: %v", out, err)
	}
	return b
}

// assertBlackholeMapsEqual asserts two msgpack-unpacked blackhole dicts
// (map[any]any keyed by binary identity hashes) carry the same entries
// with equal source/until/reason, independent of map iteration order.
func assertBlackholeMapsEqual(t *testing.T, got, want map[any]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entry count = %d, want %d", len(got), len(want))
	}
	for wk, wv := range want {
		wkey := blackholeMapKey(wk)
		se, ok := blackholeSubEntry(got, wkey)
		if !ok {
			t.Fatalf("missing expected identity %x", wkey)
		}
		wse, ok := wv.(map[any]any)
		if !ok {
			t.Fatalf("want sub-entry type %T for %x, want map", wv, wkey)
		}
		wSource, _ := wse["source"].([]byte)
		assertBlackholeSubEntry(t, map[any]any{wk: se}, wkey, wSource, wse["until"], blackholeReason(wse))
	}
}

// TestBlackholeListHandler golden-tests TransportSystem.blackholeListHandler
// (Python Transport.blackhole_list_handler, Transport.py:3243-3250). The
// handler returns the blackholed_identities dict serialised as a msgpack
// map with binary identity-hash keys — the form Python's umsgpack produces.
// The response is packed the way the link response path packs it
// (msgpack.Pack([]any{requestID, response})) and then both the Go-packed
// payload and the captured Python golden hex are decoded and compared
// structurally, so the test is independent of msgpack map key order while
// still proving the Go output decodes to exactly what Python serves.
func TestBlackholeListHandler(t *testing.T) {
	t.Parallel()

	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")
	src := mustHexDecode(t, "112233445566778899aabbccddeeff00")
	ih1 := mustHexDecode(t, "deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")
	ih3 := mustHexDecode(t, "feedfacefeedfacefeedfacefeedface")

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: own}
	ts.blackholedIdentities = map[string]BlackholeIdentityEntry{
		string(ih1): {IdentityHash: ih1, Source: own, Reason: "local-ih1", Until: nil},
		string(ih2): {IdentityHash: ih2, Source: src, Reason: "remote-ih2", Until: new(time.Unix(9_900_000_000, 0))},
		string(ih3): {IdentityHash: ih3, Source: own, Reason: "local-ih3", Until: nil},
	}

	response := ts.blackholeListHandler("/list", nil, []byte("request-id"), []byte("link-id"), nil, time.Unix(1_800_000_000, 0))
	if response == nil {
		t.Fatal("handler returned nil")
	}

	// Pack exactly as the link response path does (link.go:1546).
	packed, err := msgpack.Pack([]any{[]byte("request-id"), response})
	if err != nil {
		t.Fatalf("packing handler response: %v", err)
	}
	arr, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("unpacking packed response: %v", err)
	}
	respArr, ok := arr.([]any)
	if !ok || len(respArr) != 2 {
		t.Fatalf("packed response type %T, want 2-element array", arr)
	}
	gotMap, ok := respArr[1].(map[any]any)
	if !ok {
		t.Fatalf("response payload type %T, want map", respArr[1])
	}

	// Decode the live Python payload and assert structural equality. (Map
	// key order is not asserted: Go sorts entries by identity hash while
	// Python preserves insertion order, so the bytes differ but the decoded
	// maps must match — hence the structural comparison.)
	livePacked := pythonBlackholeListPacked(t)
	if hex.EncodeToString(livePacked) != blackholeListHandlerGoldenHex {
		t.Fatalf("live Python blackhole-list bytes != golden hex constant\n got: %x\nwant: %s", livePacked, blackholeListHandlerGoldenHex)
	}
	wantMap, err := msgpack.Unpack(livePacked)
	if err != nil {
		t.Fatalf("unpacking live python payload: %v", err)
	}
	wantM, ok := wantMap.(map[any]any)
	if !ok {
		t.Fatalf("golden type %T, want map", wantMap)
	}
	assertBlackholeMapsEqual(t, gotMap, wantM)

	// The handler must serve ALL entries (not just own-sourced, unlike
	// persist_blackhole), so the remote-sourced ih2 must be present.
	if _, ok := blackholeSubEntry(gotMap, ih2); !ok {
		t.Fatal("remote-sourced ih2 must be present in the /list response")
	}
	if len(gotMap) != 3 {
		t.Fatalf("response has %d entries, want 3", len(gotMap))
	}
}
