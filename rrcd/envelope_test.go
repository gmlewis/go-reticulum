// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

var (
	src32 = bytesOf(0x00, 32)
	mid8  = []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x11, 0x22, 0x33}
	gTS   = int64(1700000000000)
)

func bytesOf(start, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(start + i)
	}
	return b
}

func TestMakeEnvelopeGoldenNotice(t *testing.T) {
	t.Parallel()
	// Golden captured from Python make_envelope + cbor2.dumps.
	e := MakeEnvelope(int(TNotice), src32,
		WithID(mid8), WithTS(gTS), WithRoom("#foo"), WithBody("hi"))
	const want = "a7000101150248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056423666f6f06626869"
	if got := hexOf(cbor.Encode(e)); got != want {
		t.Errorf("NOTICE envelope:\n got %v\nwant %v", got, want)
	}
}

func TestMakeEnvelopeKeyOrder(t *testing.T) {
	t.Parallel()
	// Optional keys append in dst(8), room(5), body(6), nick(7) order after
	// the base 0,1,2,3,4.
	e := MakeEnvelope(int(TNotice), src32,
		WithID(mid8), WithTS(gTS),
		WithNick("tester"), WithRoom("#foo"), WithBody("b"), WithDst(src32))
	keys := []any{}
	for _, p := range e.Pairs() {
		keys = append(keys, p.Key)
	}
	want := []any{int64(0), int64(1), int64(2), int64(3), int64(4),
		int64(8), int64(5), int64(6), int64(7)}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
}

func TestMakeEnvelopeFalsyTraps(t *testing.T) {
	t.Parallel()
	// Empty mid → fresh 8 random bytes; ts 0 → now.
	e := MakeEnvelope(int(TNotice), src32, WithID(nil), WithTS(0))
	id, _ := e.GetBytes(KID)
	if len(id) != 8 {
		t.Fatalf("fresh mid length = %v, want 8", len(id))
	}
	ts, _ := EnvGetInt(e, KTS)
	if ts <= 0 {
		t.Fatalf("fresh ts = %v, want > 0", ts)
	}
	// ts 0 supplied explicitly still falls back to now (Python falsy).
	if ts == gTS {
		t.Fatalf("explicit ts 0 must not be kept")
	}
}

func TestMakeEnvelopeNickRules(t *testing.T) {
	t.Parallel()
	// Valid nick → K_NICK normalized (trimmed).
	e := MakeEnvelope(int(THello), src32, WithNick("  alice  "), WithID(mid8), WithTS(gTS))
	nick, ok := EnvGetString(e, KNick)
	if !ok || nick != "alice" {
		t.Fatalf("nick = %q, %v; want alice", nick, ok)
	}
	// Invalid nick (over the hardcoded 32-byte default) → omitted.
	long := strings.Repeat("a", 33)
	e = MakeEnvelope(int(THello), src32, WithNick(long), WithID(mid8), WithTS(gTS))
	if e.Has(KNick) {
		t.Fatal("K_NICK present for an over-limit nick")
	}
	// Empty nick → omitted.
	e = MakeEnvelope(int(THello), src32, WithNick(""), WithID(mid8), WithTS(gTS))
	if e.Has(KNick) {
		t.Fatal("K_NICK present for an empty nick")
	}
}

func TestMakeEnvelopeBodyPresence(t *testing.T) {
	t.Parallel()
	// WithBody("") includes K_BODY; WithBody(nil) omits it.
	e := MakeEnvelope(int(TNotice), src32, WithBody(""), WithID(mid8), WithTS(gTS))
	if v, ok := e.Get(KBody); !ok || v != "" {
		t.Fatalf("empty body = %v, %v; want present", v, ok)
	}
	e = MakeEnvelope(int(TNotice), src32, WithBody(nil), WithID(mid8), WithTS(gTS))
	if e.Has(KBody) {
		t.Fatal("nil body must omit K_BODY")
	}
}

func TestMakeEnvelopeOmitsRoomAndDstByDefault(t *testing.T) {
	t.Parallel()
	e := MakeEnvelope(int(TNotice), src32, WithID(mid8), WithTS(gTS))
	if e.Has(KRoom) || e.Has(KDst) || e.Has(KNick) {
		t.Fatal("optional keys present without opts")
	}
}

func TestMakeEnvelopeRoomPointer(t *testing.T) {
	t.Parallel()
	// Python's make_envelope omits K_ROOM when room is None, which a nil
	// WithRoomPtr pointer stands in for.
	e := MakeEnvelope(int(TError), src32, WithRoomPtr(nil), WithID(mid8), WithTS(gTS))
	if e.Has(KRoom) {
		t.Fatal("K_ROOM present for a nil room pointer")
	}
	// A non-nil pointer is sent, an empty room name included.
	empty := ""
	e = MakeEnvelope(int(TError), src32, WithRoomPtr(&empty), WithID(mid8), WithTS(gTS))
	if v, ok := EnvGetString(e, KRoom); !ok || v != "" {
		t.Fatalf("empty room via pointer = %v, %v; want present", v, ok)
	}
	// WithRoom always sends, an empty room name included.
	e = MakeEnvelope(int(TError), src32, WithRoom(""), WithID(mid8), WithTS(gTS))
	if v, ok := EnvGetString(e, KRoom); !ok || v != "" {
		t.Fatalf("empty room via WithRoom = %v, %v; want present", v, ok)
	}
}

func TestValidateEnvelopeAcceptsValid(t *testing.T) {
	t.Parallel()
	e := MakeEnvelope(int(THello), src32, WithBody(map[int64]any{BHelloNickLegacy: "alice"}))
	if err := ValidateEnvelope(e); err != nil {
		t.Fatalf("ValidateEnvelope(valid) = %v", err)
	}
}

func TestValidateEnvelopeBoolVersion(t *testing.T) {
	t.Parallel()
	// K_V=true passes validation (True == 1); K_V=false fails with
	// "unsupported version False".
	e := MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	e.Set(KV, true)
	if err := ValidateEnvelope(e); err != nil {
		t.Fatalf("K_V=true rejected: %v", err)
	}
	e.Set(KV, false)
	err := ValidateEnvelope(e)
	if err == nil || err.Error() != "unsupported version False" {
		t.Fatalf("K_V=false error = %v, want unsupported version False", err)
	}
}

func TestValidateEnvelopeRejections(t *testing.T) {
	t.Parallel()
	base := func() *cbor.Map {
		return MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	}
	for _, tc := range []struct {
		name    string
		mutate  func(e *cbor.Map)
		wantErr string
	}{
		{"wrong version", func(e *cbor.Map) { e.Set(KV, int64(2)) }, "unsupported version 2"},
		{"missing ts", func(e *cbor.Map) { e.Pop(KTS) }, "missing envelope key 3"},
		{"missing v", func(e *cbor.Map) { e.Pop(KV) }, "missing envelope key 0"},
		{"missing t", func(e *cbor.Map) { e.Pop(KT) }, "missing envelope key 1"},
		{"missing id", func(e *cbor.Map) { e.Pop(KID) }, "missing envelope key 2"},
		{"missing src", func(e *cbor.Map) { e.Pop(KSrc) }, "missing envelope key 4"},
		{"string key", func(e *cbor.Map) {
			t, _ := e.Pop(KT)
			e.Set("1", t)
		}, "envelope keys must be integers"},
		{"negative key", func(e *cbor.Map) {
			t, _ := e.Pop(KT)
			e.Set(int64(-1), t)
		}, "envelope keys must be unsigned integers"},
		{"float version", func(e *cbor.Map) { e.Set(KV, 1.0) },
			"protocol version must be an integer"},
		{"id as string", func(e *cbor.Map) { e.Set(KID, "not-bytes") },
			"message id must be bytes"},
		{"src as string", func(e *cbor.Map) { e.Set(KSrc, "not-bytes") },
			"sender identity must be bytes"},
		{"ts as string", func(e *cbor.Map) { e.Set(KTS, "not-int") },
			"timestamp must be an integer"},
		{"negative ts", func(e *cbor.Map) { e.Set(KTS, int64(-1)) },
			"timestamp must be unsigned"},
		{"nick as int", func(e *cbor.Map) { e.Set(KNick, int64(123)) },
			"nickname must be a string"},
		{"dst as string", func(e *cbor.Map) { e.Set(KDst, "not-bytes") },
			"destination identity must be bytes"},
		{"room as int", func(e *cbor.Map) { e.Set(KRoom, int64(1)) },
			"room name must be a string"},
	} {
		e := base()
		tc.mutate(e)
		err := ValidateEnvelope(e)
		if err == nil {
			t.Errorf("%v: ValidateEnvelope accepted an invalid envelope", tc.name)
			continue
		}
		if err.Error() != tc.wantErr {
			t.Errorf("%v: error = %q, want %q", tc.name, err.Error(), tc.wantErr)
		}
	}
}

func TestValidateEnvelopeAllowsExtensions(t *testing.T) {
	t.Parallel()
	// Unknown int keys are legal; omitted body is legal; ridiculous nicks
	// validate (no length check); float K_T is rejected.
	e := MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	e.Set(int64(64), map[int64]any{int64(1): true})
	if err := ValidateEnvelope(e); err != nil {
		t.Fatalf("unknown extension key rejected: %v", err)
	}

	e = MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	e.Set(KNick, "")
	if err := ValidateEnvelope(e); err != nil {
		t.Fatalf("empty nick rejected: %v", err)
	}
	e = MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	e.Set(KNick, "   ")
	if err := ValidateEnvelope(e); err != nil {
		t.Fatalf("whitespace nick rejected: %v", err)
	}

	// A float message type is rejected even though ints are required.
	e = MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	e.Set(KT, 1.0)
	if err := ValidateEnvelope(e); err == nil || err.Error() != "message type must be an integer" {
		t.Fatalf("float K_T error = %v", err)
	}
}

func TestValidateEnvelopeNil(t *testing.T) {
	t.Parallel()
	err := ValidateEnvelope(nil)
	if err == nil || err.Error() != "envelope must be a CBOR map (dict)" {
		t.Fatalf("nil envelope error = %v", err)
	}
}

func TestEnvelopeForwardingHelpers(t *testing.T) {
	t.Parallel()
	// The router rewrite rules: in-place K_SRC/K_ROOM updates keep the
	// key's position; a newly attached K_NICK appends last; Pop removes.
	env, err := cbor.Decode(mustHex(t, "a7000101150248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056423666f6f06626869"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	m := env.(*cbor.Map)
	peerHash := bytesOf(0x10, 32)
	EnvSetBytes(m, KSrc, peerHash)
	EnvSetString(m, KRoom, "general")

	keys := []any{}
	for _, p := range m.Pairs() {
		keys = append(keys, p.Key)
	}
	want := []any{KV, KT, KID, KTS, KSrc, KRoom, KBody}
	if len(keys) != len(want) {
		t.Fatalf("keys after rewrites = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("key order after rewrites = %v, want %v", keys, want)
		}
	}
	if b, ok := EnvGetBytes(m, KSrc); !ok || string(b) != string(peerHash) {
		t.Fatalf("K_SRC = %v, %v", b, ok)
	}
	if s, ok := EnvGetString(m, KRoom); !ok || s != "general" {
		t.Fatalf("K_ROOM = %v, %v", s, ok)
	}
	// A newly attached K_NICK appends last.
	EnvSetString(m, KNick, "alice")
	keys = append(keys[:0], nil...)
	for _, p := range m.Pairs() {
		keys = append(keys, p.Key)
	}
	if last := keys[len(keys)-1]; last != KNick {
		t.Fatalf("last key = %v, want K_NICK", last)
	}
	// Pop removes while preserving the order of the rest.
	if v, ok := EnvPop(m, KRoom); !ok || v != "general" {
		t.Fatalf("Pop(K_ROOM) = %v, %v", v, ok)
	}
	keys = keys[:0]
	for _, p := range m.Pairs() {
		keys = append(keys, p.Key)
	}
	want = []any{KV, KT, KID, KTS, KSrc, KBody, KNick}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("key order after pop = %v, want %v", keys, want)
		}
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}
