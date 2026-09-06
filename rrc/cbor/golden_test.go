// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import (
	"encoding/hex"
	"testing"
)

// Golden vectors captured from Python rrcd 0.3.2 + cbor2 6.1.2 (cbor2.dumps).
// The envelope shapes below mirror rrcd/envelope.py make_envelope with a fixed
// 8-byte message id and fixed millisecond timestamp.

var (
	goldenSrc32 = bytesOf(0x00, 32) // 32-byte identity hash: 00..1f
	goldenPeer  = bytesOf(0x10, 32) // 32-byte hash: 10..2f
	goldenMid8  = []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x11, 0x22, 0x33}
	goldenTS    = int64(1700000000000)
)

func bytesOf(start, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(start + i)
	}
	return b
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad golden hex %q: %v", s, err)
	}
	return b
}

// env builds an envelope map in make_envelope key order: base 0,1,2,3,4 then
// optional 8,5,6,7.
func env(mid []byte, ts int64, src []byte, kvs ...any) *Map {
	m := NewMap()
	m.Set(int64(0), int64(1))
	m.Set(int64(1), kvs[0].(int64))
	m.Set(int64(2), mid)
	m.Set(int64(3), ts)
	m.Set(int64(4), src)
	rest := kvs[1:]
	for i := 0; i+1 < len(rest); i += 2 {
		m.Set(rest[i], rest[i+1])
	}
	return m
}

func TestGoldenInts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "00"},
		{1, "01"},
		{10, "0a"},
		{23, "17"},
		{24, "1818"},
		{100, "1864"},
		{255, "18ff"},
		{256, "190100"},
		{1000, "1903e8"},
		{65535, "19ffff"},
		{65536, "1a00010000"},
		{4294967295, "1affffffff"},
		{4294967296, "1b0000000100000000"},
		{1755120000000, "1b00000198a54ddc00"},
	} {
		if got := hex.EncodeToString(Encode(tc.in)); got != tc.want {
			t.Errorf("Encode(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGoldenNegs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{-1, "20"},
		{-2, "21"},
		{-24, "37"},
		{-25, "3818"},
		{-100, "3863"},
		{-1000, "3903e7"},
	} {
		if got := hex.EncodeToString(Encode(tc.in)); got != tc.want {
			t.Errorf("Encode(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGoldenPrims(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"true", true, "f5"},
		{"false", false, "f4"},
		{"none", nil, "f6"},
		{"bytes AB", []byte("AB"), "424142"},
		{"text AB", "AB", "624142"},
		{"empty text", "", "60"},
		{"empty map", NewMap(), "a0"},
		{"empty array", []any{}, "80"},
		{"0.5", 0.5, "fb3fe0000000000000"},
		{"1.0", 1.0, "fb3ff0000000000000"},
		{"12345.678", 12345.678, "fb40c81cd6c8b43958"},
	} {
		if got := hex.EncodeToString(Encode(tc.in)); got != tc.want {
			t.Errorf("%v: Encode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGoldenNoticeEnvelope(t *testing.T) {
	t.Parallel()
	e := env(goldenMid8, goldenTS, goldenSrc32,
		int64(21), // K_T: T_NOTICE
		int64(5), "#foo",
		int64(6), "hi",
	)
	const want = "a7000101150248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056423666f6f06626869"
	if got := hex.EncodeToString(Encode(e)); got != want {
		t.Errorf("NOTICE envelope:\n got %v\nwant %v", got, want)
	}
}

func TestGoldenWelcome(t *testing.T) {
	t.Parallel()
	limits := NewMap()
	limits.Set(int64(0), int64(32))
	limits.Set(int64(1), int64(64))
	limits.Set(int64(2), int64(350))
	limits.Set(int64(3), int64(32))
	limits.Set(int64(4), int64(240))
	if got, want := hex.EncodeToString(Encode(limits)), "a50018200118400219015e0318200418f0"; got != want {
		t.Fatalf("limits map: got %v, want %v", got, want)
	}

	capsOn := NewMap()
	capsOn.Set(int64(1), true)
	capsOn.Set(int64(2), true)
	capsOn.Set(int64(0), true) // appended last when resource transfer is on
	if got, want := hex.EncodeToString(Encode(capsOn)), "a301f502f500f5"; got != want {
		t.Fatalf("caps map (on): got %v, want %v", got, want)
	}
	capsOff := NewMap()
	capsOff.Set(int64(1), true)
	capsOff.Set(int64(2), true)
	if got, want := hex.EncodeToString(Encode(capsOff)), "a201f502f5"; got != want {
		t.Fatalf("caps map (off): got %v, want %v", got, want)
	}

	build := func(caps *Map) string {
		body := NewMap()
		body.Set(int64(0), "rrc")
		body.Set(int64(1), "0.3.2")
		body.Set(int64(2), caps)
		body.Set(int64(3), limits)
		w := env(goldenMid8, goldenTS, goldenSrc32,
			int64(2), // K_T: T_WELCOME
			int64(6), body,
		)
		return hex.EncodeToString(Encode(w))
	}
	const wantOn = "a6000101020248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06a400637272630165302e332e3202a301f502f500f503a50018200118400219015e0318200418f0"
	const wantOff = "a6000101020248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06a400637272630165302e332e3202a201f502f503a50018200118400219015e0318200418f0"
	if got := build(capsOn); got != wantOn {
		t.Errorf("WELCOME (caps on):\n got %v\nwant %v", got, wantOn)
	}
	if got := build(capsOff); got != wantOff {
		t.Errorf("WELCOME (caps off):\n got %v\nwant %v", got, wantOff)
	}
}

func TestGoldenJoined(t *testing.T) {
	t.Parallel()
	// Fanout copy: body = one member hash, K_NICK present (appended last).
	fanout := env(goldenMid8, goldenTS, goldenSrc32,
		int64(11), // K_T: T_JOINED
		int64(5), "#foo",
		int64(6), []any{goldenPeer},
		int64(7), "tester",
	)
	const wantFanout = "a80001010b0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056423666f6f06815820101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f0766746573746572"
	if got := hex.EncodeToString(Encode(fanout)); got != wantFanout {
		t.Errorf("JOINED fanout:\n got %v\nwant %v", got, wantFanout)
	}

	// Actor's own copy: body = full member list (incl. self), no nick.
	self := env(goldenMid8, goldenTS, goldenSrc32,
		int64(11),
		int64(5), "#foo",
		int64(6), []any{goldenSrc32, goldenPeer},
	)
	const wantSelf = "a70001010b0248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056423666f6f06825820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f5820101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f"
	if got := hex.EncodeToString(Encode(self)); got != wantSelf {
		t.Errorf("JOINED self:\n got %v\nwant %v", got, wantSelf)
	}
}

func TestGoldenHello(t *testing.T) {
	t.Parallel()
	// The exact nomadnet client HELLO: body {0:"nomadnet",1:"0.1",2:{0:true,1:true}}
	// plus K_NICK "tester" (nomadnet RRC.py _send_hello).
	caps := NewMap()
	caps.Set(int64(0), true) // CAP_RESOURCE_ENVELOPE
	caps.Set(int64(1), true) // CAP_ACTION
	body := NewMap()
	body.Set(int64(0), "nomadnet")
	body.Set(int64(1), "0.1")
	body.Set(int64(2), caps)
	hello := env([]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}, goldenTS, goldenSrc32,
		int64(1), // K_T: T_HELLO
		int64(6), body,
		int64(7), "tester",
	)
	const want = "a70001010102480011223344556677031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06a300686e6f6d61646e65740163302e3102a200f501f50766746573746572"
	if got := hex.EncodeToString(Encode(hello)); got != want {
		t.Errorf("HELLO envelope:\n got %v\nwant %v", got, want)
	}
}

func TestGoldenHelloLegacyNick(t *testing.T) {
	t.Parallel()
	// Legacy body key 64 carrying the nick (pre-spec clients).
	body := NewMap()
	body.Set(int64(0), "nomadnet")
	body.Set(int64(64), "legacynick")
	hello := env(goldenMid8, goldenTS, goldenSrc32,
		int64(1),
		int64(6), body,
	)
	const want = "a6000101010248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06a200686e6f6d61646e657418406a6c65676163796e69636b"
	if got := hex.EncodeToString(Encode(hello)); got != want {
		t.Errorf("HELLO legacy envelope:\n got %v\nwant %v", got, want)
	}
}

func TestGoldenResourceEnvelope(t *testing.T) {
	t.Parallel()
	rid := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x23, 0x45, 0x67}
	// sha256("motd text")
	sha := mustHex(t, "ab9575a1f7b46ae0e9821e252ab22b53ccccf6fd1c66f2ad644643c99536c015")
	body := NewMap()
	body.Set(int64(0), rid)
	body.Set(int64(1), "notice")
	body.Set(int64(2), int64(9))
	body.Set(int64(3), sha)
	body.Set(int64(4), "utf-8")
	const wantBody = "a50048deadbeef0123456701666e6f746963650209035820ab9575a1f7b46ae0e9821e252ab22b53ccccf6fd1c66f2ad644643c99536c01504657574662d38"
	if got := hex.EncodeToString(Encode(body)); got != wantBody {
		t.Fatalf("resource body:\n got %v\nwant %v", got, wantBody)
	}
	resEnv := env(goldenMid8, goldenTS, goldenSrc32,
		int64(50), // K_T: T_RESOURCE_ENVELOPE
		int64(6), body,
	)
	const wantEnv = "a600010118320248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f06a50048deadbeef0123456701666e6f746963650209035820ab9575a1f7b46ae0e9821e252ab22b53ccccf6fd1c66f2ad644643c99536c01504657574662d38"
	if got := hex.EncodeToString(Encode(resEnv)); got != wantEnv {
		t.Errorf("RESOURCE_ENVELOPE:\n got %v\nwant %v", got, wantEnv)
	}
}

func TestGoldenAnnounceAppData(t *testing.T) {
	t.Parallel()
	m := NewMap()
	m.Set("proto", "rrc")
	m.Set("v", int64(1))
	m.Set("hub", "rrc")
	const want = "a36570726f746f637272636176016368756263727263"
	if got := hex.EncodeToString(Encode(m)); got != want {
		t.Errorf("announce app_data:\n got %v\nwant %v", got, want)
	}
}
