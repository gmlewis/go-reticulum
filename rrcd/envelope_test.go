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
	// WithDst(nil) omits K_DST the way Python's dst=None does.
	e = MakeEnvelope(int(TNotice), src32, WithID(mid8), WithTS(gTS), WithDst(nil))
	if e.Has(KDst) {
		t.Fatal("WithDst(nil) must omit K_DST")
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

// G15.6 The PING wire golden: a PING envelope carries the monotonic float
// body, in make_envelope's exact key order. Golden captured from Python
// make_envelope(T_PING, ...) + rrcd.codec.encode.
func TestGoldenPing(t *testing.T) {
	t.Parallel()

	e := MakeEnvelope(int(TPing), src32,
		WithID(mid8), WithTS(gTS), WithBody(12345.678))
	const want = "a6000101181e0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06fb40c81cd6c8b43958"
	if got := hexOf(cbor.Encode(e)); got != want {
		t.Errorf("PING envelope:\n got %v\nwant %v", got, want)
	}
}

// G15.6 The PONG echo goldens: every body kind re-encodes verbatim —
// 8-byte bytes, the 0xfb float, the empty byte string (0x40), the empty
// list (0x80) — while a None body omits K_BODY entirely (keys 0..4 only).
func TestGoldenPongEcho(t *testing.T) {
	t.Parallel()

	base := "a6000101181f0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06"
	tests := []struct {
		name string
		body any
		want string
	}{
		{name: "bytes body", body: []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x23, 0x45, 0x67},
			want: base + "48deadbeef01234567"},
		{name: "float body", body: 1.5,
			want: base + "fb3ff8000000000000"},
		{name: "empty bytes body", body: []byte{},
			want: base + "40"},
		{name: "empty list body", body: []any{},
			want: base + "80"},
	}
	for _, tt := range tests {
		e := MakeEnvelope(int(TPong), src32, WithID(mid8), WithTS(gTS), WithBody(tt.body))
		if got := hexOf(cbor.Encode(e)); got != tt.want {
			t.Errorf("%v PONG:\n got %v\nwant %v", tt.name, got, tt.want)
		}
	}

	// A None body omits K_BODY: only keys 0,1,2,3,4 remain.
	e := MakeEnvelope(int(TPong), src32, WithID(mid8), WithTS(gTS))
	keys := []any{}
	for _, p := range e.Pairs() {
		keys = append(keys, p.Key)
	}
	wantKeys := []any{int64(0), int64(1), int64(2), int64(3), int64(4)}
	if len(keys) != len(wantKeys) {
		t.Fatalf("bodyless PONG keys = %v, want %v", keys, wantKeys)
	}
	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("bodyless PONG keys = %v, want %v", keys, wantKeys)
		}
	}
	const want = "a5000101181f0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	if got := hexOf(cbor.Encode(e)); got != want {
		t.Errorf("bodyless PONG:\n got %v\nwant %v", got, want)
	}
}

// G15.6 The PARTED fanout goldens: the fanout copy carries the room, the
// one-element peer-hash list body, and the nick; the actor's own copy
// carries the body but no nick. Goldens captured from Python
// make_envelope(T_PARTED, ...) + rrcd.codec.encode.
func TestGoldenPartedFanout(t *testing.T) {
	t.Parallel()

	e := MakeEnvelope(int(TParted), src32,
		WithID(mid8), WithTS(gTS), WithRoom("general"),
		WithBody([]any{src32}), WithNick("leaver"))
	const wantFanout = "a80001010d0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056767656e6572616c06815820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f07666c6561766572"
	if got := hexOf(cbor.Encode(e)); got != wantFanout {
		t.Errorf("PARTED fanout:\n got %v\nwant %v", got, wantFanout)
	}

	e = MakeEnvelope(int(TParted), src32,
		WithID(mid8), WithTS(gTS), WithRoom("general"), WithBody([]any{src32}))
	const wantSelf = "a70001010d0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056767656e6572616c06815820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	if got := hexOf(cbor.Encode(e)); got != wantSelf {
		t.Errorf("PARTED self:\n got %v\nwant %v", got, wantSelf)
	}
}

// G16.12 A K_V beyond the int64 range must not collapse to 0 in the
// error text: Python embeds the raw big int, so a CBOR bignum version
// fails with "unsupported version 18446744073709551616". Golden captured
// from cbor2.dumps({0: 2**64, 1: 1, 2: b'\x00', 3: 0, 4: b'\x00'*32}).
func TestValidateEnvelopeWideUintVersion(t *testing.T) {
	t.Parallel()

	// The golden CBOR bytes: tag-2 bignum version of 2**64.
	const golden = "a500c249010000000000000000010102410003000458200000000000000000000000000000000000000000000000000000000000000000"
	raw, err := hex.DecodeString(golden)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cbor.Decode(raw)
	if err != nil {
		t.Fatalf("the golden envelope does not decode: %v", err)
	}
	env, ok := decoded.(*cbor.Map)
	if !ok {
		t.Fatal("the golden envelope is not a CBOR map")
	}
	err = ValidateEnvelope(env)
	if err == nil || err.Error() != "unsupported version 18446744073709551616" {
		t.Fatalf("wide K_V error = %v, want unsupported version 18446744073709551616", err)
	}

	// The decoded value renders and re-encodes like Python's int.
	v, _ := env.Get(KV)
	big, isBig := v.(cbor.BigUint)
	if !isBig {
		t.Fatalf("wide K_V decoded as %T, want cbor.BigUint", v)
	}
	if big.String() != "18446744073709551616" {
		t.Errorf("wide K_V renders as %v", big.String())
	}
	if got := hexOf(cbor.Encode(big)); got != "c249010000000000000000" {
		t.Errorf("re-encoded bignum = %v, want c249010000000000000000", got)
	}

	// A bignum K_V that fits the int64 range passes validation like a
	// plain int.
	small := MakeEnvelope(int(THello), src32, WithID(mid8), WithTS(gTS))
	small.Set(KV, cbor.BigUint{Magnitude: []byte{1}})
	if err := ValidateEnvelope(small); err != nil {
		t.Errorf("bignum version 1 rejected: %v", err)
	}
}
