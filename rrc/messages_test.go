// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// G16.3 The WELCOME limits map carries the raw config values: a TOML
// float like max_nick_bytes = 32.9 rides the wire as a CBOR float the way
// Python's queue_welcome puts the config fields into the map uncoerced
// (the client int()s them at RRC.py:897). Golden captured from python
// cbor2.dumps({0: 32.9, 1: 64, 2: 350.5, 3: 32, 4: 240}).
func TestQueueWelcomeLimitsRawValues(t *testing.T) {
	t.Parallel()

	mh := NewMessageHelper(MessageHooks{
		IdentityHash:           func() []byte { return bytesOf(0x21, 32) },
		StatsInc:               func(string, int) {},
		SendPacket:             func(*rns.Link, []byte) error { return nil },
		EnableResourceTransfer: func() bool { return false },
		HubName:                func() string { return "TestHub" },
		WelcomeLimits: func() []any {
			return []any{32.9, int64(64), 350.5, int64(32), int64(240)}
		},
		FmtHash:   func(hash []byte) string { return hexOf(hash) },
		FmtLinkID: func(*rns.Link) string { return "-" },
	})

	outgoing := &OutgoingList{}
	mh.QueueWelcome(outgoing, &rns.Link{}, bytesOf(0xaa, 32))
	if len(outgoing.Queue) != 1 {
		t.Fatalf("queued %v payloads, want 1", len(outgoing.Queue))
	}
	payload := outgoing.Queue[0].Payload

	// Decode the WELCOME body and assert the limits map byte-for-byte.
	decoded, err := cbor.Decode(payload)
	if err != nil {
		t.Fatalf("WELCOME payload does not decode: %v", err)
	}
	env, ok := decoded.(*cbor.Map)
	if !ok {
		t.Fatal("WELCOME payload is not a CBOR map")
	}
	body, ok := env.Get(KBody)
	if !ok {
		t.Fatal("WELCOME body missing")
	}
	bodyMap, ok := body.(*cbor.Map)
	if !ok {
		t.Fatal("WELCOME body is not a CBOR map")
	}
	limits, ok := bodyMap.Get(BWelcomeLimits)
	if !ok {
		t.Fatal("WELCOME limits map missing")
	}
	limitsMap, ok := limits.(*cbor.Map)
	if !ok {
		t.Fatal("WELCOME limits is not a CBOR map")
	}
	got := hexOf(cbor.Encode(limitsMap))
	const want = "a500fb404073333333333301184002fb4075e800000000000318200418f0"
	if got != want {
		t.Errorf("WELCOME limits bytes:\n got %v\nwant %v", got, want)
	}
	// The raw values keep the Python key order 0,1,2,3,4.
	keys := []any{}
	for _, p := range limitsMap.Pairs() {
		keys = append(keys, p.Key)
	}
	wantKeys := []any{int64(0), int64(1), int64(2), int64(3), int64(4)}
	if len(keys) != len(wantKeys) {
		t.Fatalf("limits keys = %v", keys)
	}
	for i := range wantKeys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("limits keys = %v, want %v", keys, wantKeys)
		}
	}
}

// G16.9 NOTICE chunking must split on a bare \r the way Python's
// str.splitlines() does ("a\rb" → ['a', 'b']), while the \r\n pair stays
// one break and the remaining break characters keep working.
func TestQueueNoticeChunksCarriageReturn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{name: "bare carriage return", text: "a\rb", want: []string{"a", "b"}},
		{name: "crlf pair", text: "a\r\nb", want: []string{"a", "b"}},
		{name: "trailing bare \r", text: "a\r", want: []string{"a"}},
		{name: "trailing crlf", text: "a\r\n", want: []string{"a"}},
		{name: "lone crlf", text: "\r\n", want: []string{""}},
		{name: "mixed", text: "one\rtwo\r\nthree\nfour", want: []string{"one", "two", "three", "four"}},
	}
	for _, tt := range tests {
		if got := splitLinesPython(tt.text); !equalStrings(got, tt.want) {
			t.Errorf("%v: splitLinesPython = %q, want %q", tt.name, got, tt.want)
		}
	}

	// The queued NOTICE chunks carry one line each.
	mh := NewMessageHelper(MessageHooks{
		IdentityHash: func() []byte { return bytesOf(0x21, 32) },
		StatsInc:     func(string, int) {},
		SendPacket:   func(*rns.Link, []byte) error { return nil },
		FmtHash:      func(hash []byte) string { return hexOf(hash) },
		FmtLinkID:    func(*rns.Link) string { return "-" },
	})
	room := "general"
	outgoing := &OutgoingList{}
	mh.QueueNoticeChunks(outgoing, &rns.Link{}, &room, "one\rtwo")
	if len(outgoing.Queue) != 2 {
		t.Fatalf("queued %v chunks, want 2", len(outgoing.Queue))
	}
	bodies := []string{}
	for _, item := range outgoing.Queue {
		decoded, err := cbor.Decode(item.Payload)
		if err != nil {
			t.Fatalf("chunk does not decode: %v", err)
		}
		env, ok := decoded.(*cbor.Map)
		if !ok {
			t.Fatal("chunk is not a CBOR map")
		}
		body, _ := env.Get(KBody)
		bodies = append(bodies, body.(string))
	}
	if !equalStrings(bodies, []string{"one", "two"}) {
		t.Errorf("chunk bodies = %q, want [one two]", bodies)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// G16.4 The WELCOME carries the version of the code that welcomed the client:
// the body's BWelcomeVer slot is rns.VERSION, which both the hub's WELCOME and
// the `gorrcd --version` probe are compiled from. This in-package check pins
// that source of truth deterministically, with no built artifact involved, and
// is the counterpart of TestIntegrationHelloWelcomeOverPipe's wire assertion
// that the WELCOME version equals the built binary's own version.
func TestQueueWelcomeVersionMatchesRNSVersion(t *testing.T) {
	t.Parallel()

	mh := NewMessageHelper(MessageHooks{
		IdentityHash:           func() []byte { return bytesOf(0x21, 32) },
		StatsInc:               func(string, int) {},
		SendPacket:             func(*rns.Link, []byte) error { return nil },
		EnableResourceTransfer: func() bool { return false },
		HubName:                func() string { return "TestHub" },
		WelcomeLimits: func() []any {
			return []any{int64(32), int64(64), int64(350), int64(32), int64(240)}
		},
		FmtHash:   func(hash []byte) string { return hexOf(hash) },
		FmtLinkID: func(*rns.Link) string { return "-" },
	})

	outgoing := &OutgoingList{}
	mh.QueueWelcome(outgoing, &rns.Link{}, bytesOf(0xaa, 32))
	if len(outgoing.Queue) != 1 {
		t.Fatalf("queued %v payloads, want 1", len(outgoing.Queue))
	}

	decoded, err := cbor.Decode(outgoing.Queue[0].Payload)
	if err != nil {
		t.Fatalf("WELCOME payload does not decode: %v", err)
	}
	env, ok := decoded.(*cbor.Map)
	if !ok {
		t.Fatal("WELCOME payload is not a CBOR map")
	}
	body, ok := env.Get(KBody)
	if !ok {
		t.Fatal("WELCOME body missing")
	}
	bodyMap, ok := body.(*cbor.Map)
	if !ok {
		t.Fatal("WELCOME body is not a CBOR map")
	}
	version, ok := bodyMap.Get(BWelcomeVer)
	if !ok {
		t.Fatal("WELCOME version field missing")
	}
	if got, ok := version.(string); !ok || got != rns.VERSION {
		t.Errorf("WELCOME version = %#v, want the rns.VERSION string %q", version, rns.VERSION)
	}
}

// TestQueueWelcomeFitsFleetMTU pins the WELCOME/MTU failure mode rrcd
// documents (README.md:74-88): a hub whose WELCOME does not fit the link MTU
// refuses to welcome the peer, and the client then times out waiting for a
// WELCOME. Two properties keep the fleet's hub inside the MTU: the greeting is
// NOT part of the WELCOME (it travels after it as chunked NOTICE messages via
// the post-send callbacks), and include_joined_member_list only affects
// JOINED/PARTED fanouts, never the WELCOME body (rrcd/router.py:504-640,
// session.py:153; Go router.go:463, session.go:225).
func TestQueueWelcomeFitsFleetMTU(t *testing.T) {
	t.Parallel()

	greeting := "Welcome! JOIN #general to discuss go-nomadnet, go-reticulum, and asic-reticulum."
	newHelper := func(hubName string, logged *[]string) *MessageHelper {
		return NewMessageHelper(MessageHooks{
			IdentityHash:           func() []byte { return bytesOf(0x21, 32) },
			StatsInc:               func(string, int) {},
			SendPacket:             func(*rns.Link, []byte) error { return nil },
			EnableResourceTransfer: func() bool { return true },
			HubName:                func() string { return hubName },
			WelcomeLimits:          func() []any { return []any{int64(32), int64(64), int64(350), int64(32), int64(240)} },
			FmtHash:                func(hash []byte) string { return hexOf(hash) },
			FmtLinkID:              func(*rns.Link) string { return "-" },
			Logf: func(format string, args ...any) {
				*logged = append(*logged, fmt.Sprintf(format, args...))
			},
		})
	}

	// The fleet's public hub, with the greeting the owner actually serves.
	var fleetLog []string
	fleet := newHelper("gonomadnet Public Hub", &fleetLog)
	outgoing := &OutgoingList{}
	fleet.QueueWelcome(outgoing, &rns.Link{}, bytesOf(0xaa, 32))
	if len(outgoing.Queue) != 1 {
		t.Fatalf("fleet WELCOME queued %v payloads, want 1 (log=%v)", len(outgoing.Queue), fleetLog)
	}
	payload := outgoing.Queue[0].Payload
	if len(payload) > rnsMDU {
		t.Errorf("fleet WELCOME is %v bytes, over the %v-byte link MDU", len(payload), rnsMDU)
	}
	if strings.Contains(string(payload), greeting) {
		t.Error("fleet WELCOME carries the greeting; the greeting must travel as NOTICE chunks after it")
	}
	t.Logf("fleet WELCOME payload=%v bytes (mdu=%v)", len(payload), rnsMDU)

	// A hub name large enough to overflow the link MTU must be refused loudly
	// rather than queued into a send that cannot fit.
	var oversizeLog []string
	oversize := newHelper(strings.Repeat("h", 512), &oversizeLog)
	oversizeOutgoing := &OutgoingList{}
	oversize.QueueWelcome(oversizeOutgoing, &rns.Link{}, bytesOf(0xaa, 32))
	if len(oversizeOutgoing.Queue) != 0 {
		t.Errorf("oversized WELCOME queued %v payloads, want 0", len(oversizeOutgoing.Queue))
	}
	if len(oversizeLog) != 1 || !strings.Contains(oversizeLog[0], "WELCOME would not fit MTU; cannot welcome peer=") {
		t.Errorf("oversize refusal log = %v, want the documented WELCOME-would-not-fit warning", oversizeLog)
	}
}
