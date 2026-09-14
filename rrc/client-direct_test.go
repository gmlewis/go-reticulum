// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rrc

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// directCapHub returns a hub that advertises CAP_DIRECT_NOTICE and knows one
// peer in a room, so a direct notice has somewhere to go.
func directCapHub(t *testing.T) (*RRCHub, []byte) {
	t.Helper()
	_, hub := newHookTestHub(t)
	hub.onSend = func(map[any]any) {}
	hub.lock.Lock()
	hub.HubCaps = map[any]any{int64(CapAction): true, int64(CapDirectNotice): true}
	hub.lock.Unlock()

	peer := peerHash(0x60)
	feedRoomMessage(t, hub, "general", peer, "Alice", "hello")
	if !hub.knowsPeer(hexString(peer)) {
		t.Fatal("test setup: the fed peer is not known to the hub")
	}
	return hub, peer
}

// TestMakeDirectNoticeEnvelopeShape asserts every key of the direct-notice
// envelope, and that a room is never attached to one.
func TestMakeDirectNoticeEnvelopeShape(t *testing.T) {
	t.Parallel()

	src := peerHash(0x01)
	dst := peerHash(0x02)
	mid := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	env := MakeDirectNoticeEnvelope(src, dst, []byte("Sender"), "hi there", mid, 1700000000000)

	if got := intVal(env, KeyType); got != TypeNotice {
		t.Errorf("K_T = %v, want TNotice (%v)", got, TypeNotice)
	}
	if got := intVal(env, KeyVersion); got != RRCVersion {
		t.Errorf("K_V = %v, want %v", got, RRCVersion)
	}
	if got := int64Val(env, KeyTimestamp); got != 1700000000000 {
		t.Errorf("K_TS = %v, want 1700000000000", got)
	}
	if got := byteVal(env, KeyDst); hex.EncodeToString(got) != hex.EncodeToString(dst) {
		t.Errorf("K_DST = %v, want %v", hex.EncodeToString(got), hex.EncodeToString(dst))
	}
	if got := byteVal(env, KeySource); hex.EncodeToString(got) != hex.EncodeToString(src) {
		t.Errorf("K_SRC = %v, want %v", hex.EncodeToString(got), hex.EncodeToString(src))
	}
	if got := byteVal(env, KeyMessageID); hex.EncodeToString(got) != hex.EncodeToString(mid) {
		t.Errorf("K_ID = %v, want %v", hex.EncodeToString(got), hex.EncodeToString(mid))
	}
	if got := envVal(env, KeyBody); got != "hi there" {
		t.Errorf("K_BODY = %v, want %q", got, "hi there")
	}
	if got := envVal(env, KeyNick); got != "Sender" {
		t.Errorf("K_NICK = %v, want %q", got, "Sender")
	}
	if _, ok := env[KeyRoom]; ok {
		t.Error("a direct notice carries K_ROOM; the hub rejects room+dst combinations")
	}
}

// TestMakeDirectNoticeEnvelopeEncodesWithoutRoomKey asserts the K_ROOM key is
// absent from the encoded bytes, not merely nil: a nil-valued K_ROOM would
// still trip the hub's "direct notice must not include room" check.
func TestMakeDirectNoticeEnvelopeEncodesWithoutRoomKey(t *testing.T) {
	t.Parallel()

	env := MakeDirectNoticeEnvelope(peerHash(0x01), peerHash(0x02), nil, "body", make([]byte, 8), NowMs())
	decoded, err := DecodeEnvelope(cbor.Encode(env))
	if err != nil {
		t.Fatalf("DecodeEnvelope: %v", err)
	}
	if _, ok := decoded[KeyRoom]; ok {
		t.Error("decoded direct notice carries K_ROOM")
	}
	if _, ok := decoded[int64(KeyRoom)]; ok {
		t.Error("decoded direct notice carries K_ROOM as int64")
	}
	if _, ok := decoded[uint64(KeyRoom)]; ok {
		t.Error("decoded direct notice carries K_ROOM as uint64")
	}
	if got := byteVal(decoded, KeyDst); len(got) != 16 {
		t.Errorf("decoded K_DST length = %v, want 16", len(got))
	}
}

// TestHasCapabilityHandlesIntAndUintKeys asserts the capability query tolerates
// every integer key and value representation a CBOR decoder may produce.
func TestHasCapabilityHandlesIntAndUintKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		caps map[any]any
		cap  int
		want bool
	}{
		{
			name: "int64 keys with bool values",
			caps: map[any]any{int64(CapDirectNotice): true, int64(CapAction): true},
			cap:  CapDirectNotice,
			want: true,
		},
		{
			name: "uint64 keys with bool values",
			caps: map[any]any{uint64(CapAction): true},
			cap:  CapAction,
			want: true,
		},
		{
			name: "uint64 keys with uint64 values",
			caps: map[any]any{uint64(CapDirectNotice): uint64(1)},
			cap:  CapDirectNotice,
			want: true,
		},
		{
			name: "int keys with int values",
			caps: map[any]any{CapAction: 1},
			cap:  CapAction,
			want: true,
		},
		{
			name: "capability advertised as false",
			caps: map[any]any{int64(CapDirectNotice): false},
			cap:  CapDirectNotice,
			want: false,
		},
		{
			name: "capability advertised as zero",
			caps: map[any]any{uint64(CapDirectNotice): uint64(0)},
			cap:  CapDirectNotice,
			want: false,
		},
		{
			name: "capability absent",
			caps: map[any]any{int64(CapAction): true},
			cap:  CapDirectNotice,
			want: false,
		},
		{
			name: "empty caps map",
			caps: map[any]any{},
			cap:  CapDirectNotice,
			want: false,
		},
		{
			name: "nil caps map",
			caps: nil,
			cap:  CapDirectNotice,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, hub := newHookTestHub(t)
			hub.lock.Lock()
			hub.HubCaps = tt.caps
			hub.lock.Unlock()
			if got := hub.HasCapability(tt.cap); got != tt.want {
				t.Errorf("HasCapability(%v) = %v, want %v", tt.cap, got, tt.want)
			}
		})
	}
}

// TestHasCapabilityAfterWelcomeRoundTrip asserts capabilities decoded from a
// real WELCOME envelope are visible to the query. A raw map-key lookup misses
// every CBOR-decoded integer key.
func TestHasCapabilityAfterWelcomeRoundTrip(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	caps := map[any]any{
		BWelcomeCaps: map[any]any{
			CAPAction:       true,
			CAPDirectNotice: true,
		},
	}
	env := MakeClientEnvelope(TypeWelcome, nil, nil, nil,
		map[any]any{BWelcomeCaps: caps[BWelcomeCaps], BWelcomeHub: "TestHub"}, make([]byte, 8), NowMs())
	hub.HandleData(cbor.Encode(env))

	if !hub.HasCapability(CapDirectNotice) {
		t.Error("HasCapability(CapDirectNotice) = false after a WELCOME advertising it")
	}
	if !hub.HasCapability(CapAction) {
		t.Error("HasCapability(CapAction) = false after a WELCOME advertising it")
	}
	if hub.HasCapability(CapResourceEnvelope) {
		t.Error("HasCapability(CapResourceEnvelope) = true, want false (not advertised)")
	}
}

// TestDirectNoticesListsOnlyPrivateNotices asserts the accessor a client
// renders private messages from: the notice the hub addressed to this client
// alone is listed, and ordinary room notices are not.
func TestDirectNoticesListsOnlyPrivateNotices(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	peer := peerHash(0x60)
	own := peerHash(0x01)

	if got := hub.DirectNotices(); len(got) != 0 {
		t.Fatalf("fresh hub DirectNotices() = %v entries, want 0", len(got))
	}

	// An ordinary room notice first: it must never be listed as private.
	feedRoomNotice(t, hub, "general", peer, "Alice", "room news")
	if got := hub.DirectNotices(); len(got) != 0 {
		t.Fatalf("DirectNotices() = %v entries after a room notice, want 0", len(got))
	}

	feedDirectNotice(t, hub, peer, "Alice", own, "psst")
	got := hub.DirectNotices()
	if len(got) != 1 {
		t.Fatalf("DirectNotices() = %v entries, want 1", len(got))
	}
	if got[0].Text != "psst" || got[0].Nick != "Alice" || !got[0].Direct {
		t.Errorf("DirectNotices()[0] = %+v, want the private notice from Alice", got[0])
	}
}

// feedRoomNotice drives one inbound room NOTICE through the hub's decode path.
func feedRoomNotice(t *testing.T, hub *RRCHub, room string, src []byte, nick, text string) {
	t.Helper()
	env := MakeClientEnvelope(TypeNotice, src, []byte(room), []byte(nick), text, make([]byte, 8), NowMs())
	hub.HandleData(cbor.Encode(env))
}

// feedDirectNotice drives one inbound private NOTICE (K_DST) through the hub.
func feedDirectNotice(t *testing.T, hub *RRCHub, src []byte, nick string, dst []byte, text string) {
	t.Helper()
	env := MakeClientEnvelope(TypeNotice, src, nil, []byte(nick), text, make([]byte, 8), NowMs())
	env[KeyDst] = dst
	hub.HandleData(cbor.Encode(env))
}

// TestSendDirectNoticeRejectsBadTargets asserts the guard rails: an empty or
// wrong-length hash, a hub without the capability, and an unknown peer all fail
// without putting anything on the wire.
func TestSendDirectNoticeRejectsBadTargets(t *testing.T) {
	t.Parallel()

	t.Run("empty target", func(t *testing.T) {
		t.Parallel()
		hub, _ := directCapHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		err := hub.SendDirectNotice(nil, "hi")
		if err == nil {
			t.Fatal("SendDirectNotice(nil) = nil error, want an error")
		}
		if !strings.Contains(err.Error(), "identity hash") {
			t.Errorf("error = %q, want it to name the identity hash", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("short target hash", func(t *testing.T) {
		t.Parallel()
		hub, _ := directCapHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		err := hub.SendDirectNotice(make([]byte, 10), "hi")
		if err == nil {
			t.Fatal("SendDirectNotice(10 bytes) = nil error, want an error")
		}
		if !strings.Contains(err.Error(), "16") {
			t.Errorf("error = %q, want it to state the required hash length", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("hub without the capability", func(t *testing.T) {
		t.Parallel()
		hub, peer := directCapHub(t)
		hub.lock.Lock()
		hub.HubCaps = map[any]any{int64(CapAction): true}
		hub.lock.Unlock()
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		err := hub.SendDirectNotice(peer, "hi")
		if !errors.Is(err, ErrDirectNoticesUnsupported) {
			t.Fatalf("error = %v, want ErrDirectNoticesUnsupported", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("unknown peer", func(t *testing.T) {
		t.Parallel()
		hub, _ := directCapHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		err := hub.SendDirectNotice(peerHash(0x7f), "hi")
		if !errors.Is(err, ErrDestinationNotConnected) {
			t.Fatalf("error = %v, want ErrDestinationNotConnected", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})

	t.Run("body larger than one envelope", func(t *testing.T) {
		t.Parallel()
		hub, peer := directCapHub(t)
		var sent int
		hub.onSend = func(map[any]any) { sent++ }
		err := hub.SendDirectNotice(peer, strings.Repeat("x", rns.MDU*2))
		if err == nil {
			t.Fatal("oversized SendDirectNotice = nil error, want an error")
		}
		if !strings.Contains(err.Error(), "MDU") {
			t.Errorf("error = %q, want it to mention the MDU", err)
		}
		if sent != 0 {
			t.Errorf("sent = %v envelopes, want 0", sent)
		}
	})
}

// TestSendDirectNoticeBuildsOneAddressedEnvelope asserts a successful send
// emits exactly one NOTICE addressed with K_DST and carrying no room.
func TestSendDirectNoticeBuildsOneAddressedEnvelope(t *testing.T) {
	t.Parallel()

	hub, peer := directCapHub(t)
	var sent []map[any]any
	hub.onSend = func(env map[any]any) { sent = append(sent, env) }

	if err := hub.SendDirectNotice(peer, "pong"); err != nil {
		t.Fatalf("SendDirectNotice: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %v envelopes, want exactly 1", len(sent))
	}
	env := sent[0]
	if got := intVal(env, KeyType); got != TypeNotice {
		t.Errorf("K_T = %v, want TNotice (%v)", got, TypeNotice)
	}
	if got := byteVal(env, KeyDst); hex.EncodeToString(got) != hex.EncodeToString(peer) {
		t.Errorf("K_DST = %v, want %v", hex.EncodeToString(got), hex.EncodeToString(peer))
	}
	if got := byteVal(env, KeySource); hex.EncodeToString(got) != hex.EncodeToString(hub.Manager.identityHash()) {
		t.Errorf("K_SRC = %v, want our own identity hash", hex.EncodeToString(got))
	}
	if got := envVal(env, KeyBody); got != "pong" {
		t.Errorf("K_BODY = %v, want %q", got, "pong")
	}
	if got := byteVal(env, KeyMessageID); len(got) == 0 {
		t.Error("K_ID is empty, want a fresh message id")
	}
	if got := int64Val(env, KeyTimestamp); got == 0 {
		t.Error("K_TS = 0, want a timestamp")
	}
	if _, ok := env[KeyRoom]; ok {
		t.Error("a direct notice carries K_ROOM")
	}
}
