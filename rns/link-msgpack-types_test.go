// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// handshakePair returns an initiator/receiver Link pair that have already
// completed their handshakes against each other. The receiver stands in for
// the destination side of a link, which is the side that processes an inbound
// RTT packet and an inbound request packet.
func handshakePair(t *testing.T) (*Link, *Link) {
	t.Helper()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")

	initiator := mustTestNewLink(t, ts, receiverDest)
	receiver := mustTestNewLink(t, ts, receiverDest)
	receiver.initiator = false

	initiator.linkID = []byte("simulated_link_id")
	initiator.hash = initiator.linkID
	receiver.linkID = initiator.linkID
	receiver.hash = initiator.linkID

	mustTest(t, initiator.LoadPeer(receiver.pubBytes, receiver.sigPubBytes))
	mustTest(t, receiver.LoadPeer(initiator.pubBytes, initiator.sigPubBytes))
	mustTest(t, initiator.Handshake())
	mustTest(t, receiver.Handshake())

	return initiator, receiver
}

// TestLinkHandleRTTAcceptsAnyNumericEncoding pins the numeric kinds accepted
// for the link RTT payload. Python decodes the payload with
// umsgpack.unpackb and feeds the result straight to
// max(measured_rtt, rtt) (Link.py:521-522), so every numeric MessagePack kind
// establishes the link. A peer that packs its RTT as a float32 (MessagePack
// 0xca), an int, or a uint must therefore not be torn down: rejecting the
// value kills a link that the reference implementation accepts.
func TestLinkHandleRTTAcceptsAnyNumericEncoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rtt  any
	}{
		{name: "float64", rtt: 2.0},
		{name: "float32", rtt: float32(2.0)},
		{name: "int", rtt: 2},
		{name: "uint64", rtt: uint64(2)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			initiator, receiver := handshakePair(t)
			receiver.status.Store(LinkHandshake)
			receiver.requestTime = time.Now().Add(-150 * time.Millisecond)

			rttData, err := msgpack.Pack(tt.rtt)
			if err != nil {
				t.Fatalf("Pack RTT as %v: %v", tt.name, err)
			}
			encrypted, err := initiator.Encrypt(rttData)
			if err != nil {
				t.Fatalf("Encrypt RTT: %v", err)
			}

			receiver.HandleRTT(&Packet{Data: encrypted})

			if got := receiver.status.Load(); got != LinkActive {
				t.Fatalf("RTT packed as %v left status=%v, want %v: the link was torn down for a value Python accepts",
					tt.name, got, LinkActive)
			}
			if receiver.rtt < 2.0 {
				t.Fatalf("RTT packed as %v produced rtt=%v, want >= 2.0", tt.name, receiver.rtt)
			}
		})
	}
}

// TestLinkHandleRequestAcceptsAnyNumericTimestamp pins the numeric kinds
// accepted for the request timestamp. Python reads the timestamp without
// constraining its type (Link.py:806 requested_at = unpacked_request[0]) and
// passes it to the response generator, so the same kinds must reach the Go
// response generator instead of the request being dropped as malformed.
func TestLinkHandleRequestAcceptsAnyNumericTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ts     any
		wantAt time.Time
	}{
		{name: "float64", ts: 1024.5, wantAt: time.Unix(1024, 500000000)},
		{name: "float32", ts: float32(1024.5), wantAt: time.Unix(1024, 500000000)},
		{name: "int", ts: 1024, wantAt: time.Unix(1024, 0)},
		{name: "uint64", ts: uint64(1024), wantAt: time.Unix(1024, 0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, receiver := handshakePair(t)

			var (
				called bool
				gotAt  time.Time
			)
			receiver.destination.RegisterRequestHandler(
				"test",
				func(path string, data any, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
					called = true
					gotAt = requestedAt
					return nil
				},
				AllowAll, nil, false,
			)

			receiver.status.Store(LinkActive)
			// The handler table is keyed by the truncated hash of the request
			// path, and the request packet carries that same hash as bytes.
			pathHash := TruncatedHash([]byte("test"))
			receiver.handleRequest([]byte("request_id"), []any{tt.ts, pathHash, nil})

			if !called {
				t.Fatalf("request with a %v timestamp was dropped instead of handled", tt.name)
			}
			if !gotAt.Equal(tt.wantAt) {
				t.Fatalf("requestedAt=%v want=%v", gotAt, tt.wantAt)
			}
		})
	}
}
