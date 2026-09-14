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
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestWelcomeLimitsConcurrentRead pins the locking contract on the limits the
// WELCOME envelope carries. The clients read them LIVE while a hub session is
// running — a chat composer consults the per-message body limit on every send
// (Python reads self.hub.max_msg_body_bytes at send time, Channels.py:879), the
// nickname limit on every nick change, and the hub-info panel renders them — so
// handleWelcome's writes must hold the same lock the Max*Limit accessors take.
// Under the race detector this test fails when that write is unguarded.
func TestWelcomeLimitsConcurrentRead(t *testing.T) {
	t.Parallel()

	_, hub := fanoutFixture(t)

	limits := map[any]any{
		LMaxNickBytes:           32,
		LMaxRoomNameBytes:       64,
		LMaxMsgBodyBytes:        350,
		LMaxRoomsPerSession:     32,
		LRateLimitMsgsPerMinute: 240,
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = hub.MaxMsgBodyLimit()
				_ = hub.MaxNickLimit()
			}
		}
	})

	for range 100 {
		hub.handleWelcome(map[any]any{BWelcomeLimits: limits})
	}
	close(done)
	wg.Wait()

	if got := hub.MaxMsgBodyLimit(); got != 350 {
		t.Errorf("MaxMsgBodyLimit = %v, want the advertised 350", got)
	}
	if got := hub.MaxNickLimit(); got != 32 {
		t.Errorf("MaxNickLimit = %v, want the advertised 32", got)
	}
}

// TestWelcomeLimitsFallBackWithoutAdvertisement pins Python's `or <default>`
// fallbacks on the live accessors: a hub that never advertises limits (or
// advertises a non-positive value) leaves the 350-byte message-body default and
// the 32-byte nick default in force, which is what the composer's over-limit
// gate must use.
func TestWelcomeLimitsFallBackWithoutAdvertisement(t *testing.T) {
	t.Parallel()

	_, hub := fanoutFixture(t)

	hub.handleWelcome(map[any]any{})
	if got := hub.MaxMsgBodyLimit(); got != DefaultMaxMsgBytes {
		t.Errorf("MaxMsgBodyLimit without limits = %v, want %v", got, DefaultMaxMsgBytes)
	}

	hub.handleWelcome(map[any]any{BWelcomeLimits: map[any]any{LMaxMsgBodyBytes: 0, LMaxNickBytes: 0}})
	if got := hub.MaxMsgBodyLimit(); got != DefaultMaxMsgBytes {
		t.Errorf("MaxMsgBodyLimit on a zero limit = %v, want %v", got, DefaultMaxMsgBytes)
	}
	if got := hub.MaxNickLimit(); got != DefaultMaxNickBytes {
		t.Errorf("MaxNickLimit on a zero limit = %v, want %v", got, DefaultMaxNickBytes)
	}

	hub.handleWelcome(map[any]any{BWelcomeLimits: map[any]any{LMaxMsgBodyBytes: 500}})
	if got := hub.MaxMsgBodyLimit(); got != 500 {
		t.Errorf("MaxMsgBodyLimit after advertising 500 = %v, want 500", got)
	}
}

// TestMsgBodyEnvelopesFitOneLinkPacket pins the reason the 350-byte default
// exists — gorrcd's bootstrap config: "max_msg_body_bytes should not allow
// messages so large that they cannot fit within the link MTU after UTF-8
// encoding and envelope overhead". Every body up to that limit, and the
// 362-byte draft from the 2026-09-14 live forensics on the RNS Community hub,
// must still travel as a single RNS link packet: the client-side over-limit gate
// and the split dialog enforce the HUB's rule, never a transport limit. The
// expected sizes are the ones measured on the wire in that session (a 172-byte
// body left the client as a 239-byte send, a 189-byte body as 256 bytes), so
// this also pins the 67-byte envelope overhead for bodies under 256 bytes — one
// byte more at and above 256, where the CBOR text header grows.
func TestMsgBodyEnvelopesFitOneLinkPacket(t *testing.T) {
	t.Parallel()

	src := bytes.Repeat([]byte{0xAB}, 16)
	for _, tc := range []struct {
		body, wantEnvelope int
	}{
		{172, 239},                // live forensics: part 1 of the 2026-09-14 draft
		{189, 256},                // live forensics: part 2
		{DefaultMaxMsgBytes, 418}, // the hub-side default
		{362, 430},                // the full draft that C-d failed to send
	} {
		// MsgID is 8 bytes in production (Python os.urandom(8), RRC.py:113).
		env := MakeClientEnvelope(TypeMsg, src, []byte("general"), []byte("gonomadnet"),
			strings.Repeat("x", tc.body), MsgID(), NowMs())
		data, err := EncodeEnvelope(env)
		if err != nil {
			t.Fatalf("EncodeEnvelope for a %v-byte body: %v", tc.body, err)
		}
		if len(data) != tc.wantEnvelope {
			t.Errorf("a %v-byte body encodes to %v bytes, want %v", tc.body, len(data), tc.wantEnvelope)
		}
		if len(data) > rns.MDU {
			t.Errorf("a %v-byte body encodes to %v bytes, over the %v-byte link MDU",
				tc.body, len(data), rns.MDU)
		}
	}
}
