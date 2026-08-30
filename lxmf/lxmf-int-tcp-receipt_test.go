// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestTCPDirectDeliverySenderReceiptReachesDelivered pins the proof-of-delivery
// path: a receiver's LXMRouter.delivery_packet must prove the inbound delivery
// packet (mirroring Python LXMRouter.delivery_packet's unconditional
// packet.prove(), LXMRouter.py:1927) so the proof travels back over the
// receiver's reverse path, validates the sender's packet receipt, and the
// receipt callback marks the sender's message StateDelivered. Before the fix,
// the message DATA arrived at the receiver but no proof ever returned: the
// sender's message stayed stuck at StateSent and was re-transmitted forever —
// exactly the "remote box never received my message" symptom reported between
// two gonomadnet nodes sharing the same shared instance.
func TestTCPDirectDeliverySenderReceiptReachesDelivered(t *testing.T) {
	testutils.SkipShortIntegration(t)

	routerA, routerB, destA, destB, tsA, _, cleanup := setupTwoRouterTCPNetwork(t)
	defer cleanup()

	receivedCh := make(chan *Message, 1)
	routerB.RegisterDeliveryCallback(func(msg *Message) {
		select {
		case receivedCh <- msg:
		default:
		}
	})

	if err := routerA.Announce(destA.Hash); err != nil {
		t.Fatalf("Announce A: %v", err)
	}
	if err := routerB.Announce(destB.Hash); err != nil {
		t.Fatalf("Announce B: %v", err)
	}

	// Wait for A to learn a path to B (announce propagation).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if tsA.HasPath(destB.Hash) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !tsA.HasPath(destB.Hash) {
		t.Fatal("timed out waiting for path A->B after announce")
	}

	msg, err := NewMessage(destB, destA, "receipt parity check", "receipt title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.DesiredMethod = MethodDirect
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if err := routerA.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	select {
	case got := <-receivedCh:
		if got.ContentString() != "receipt parity check" {
			t.Errorf("content = %q, want %q", got.ContentString(), "receipt parity check")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for direct message delivery via TCP")
	}

	// The sender's message must be confirmed delivered by the returning proof,
	// not merely sent. Poll while the router's processes-outbound loop holds
	// the photo of the in-flight message.
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if state := msg.State(); state == StateDelivered {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("sender's message state = %#02x, want StateDelivered (%#02x): the receiver's proof never validated the sender's packet receipt", msg.State(), StateDelivered)
}