// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDeliveryPacketDispatchesDeliveryInGoroutine verifies that deliveryPacket
// dispatches the inbound delivery job (unpack +
// handleInboundMessage) in a goroutine so the packet callback is not
// blocked, mirroring Python LXMRouter.delivery_packet (LXMRouter.py:1949-
// 1950, v1.1.0) which starts a daemon thread running lxmf_delivery. The
// delivery callback runs concurrently with continued packet processing.
func TestDeliveryPacketDispatchesDeliveryInGoroutine(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	_, err := router.RegisterDeliveryIdentity(destID, "dest", nil)
	mustTest(t, err)

	message := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	mustTest(t, message.Pack())

	started := make(chan struct{})
	release := make(chan struct{})
	router.RegisterDeliveryCallback(func(_ *Message) {
		close(started)
		<-release
	})

	packet := &rns.Packet{DestinationType: rns.DestinationLink}

	// Run deliveryPacket in a goroutine so the test can observe whether it
	// returns before the (blocking) delivery callback completes.
	packetDone := make(chan struct{})
	go func() {
		router.deliveryPacket(message.Packed, packet)
		close(packetDone)
	}()

	// deliveryPacket must return promptly even though the callback blocks.
	select {
	case <-packetDone:
	case <-time.After(2 * time.Second):
		t.Fatal("deliveryPacket blocked waiting for delivery callback; expected async dispatch in a goroutine")
	}

	// The callback must be running concurrently in its own goroutine.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery callback did not start; deliveryPacket did not dispatch the delivery job")
	}

	// Release the callback so the in-flight goroutine finishes and the router
	// can close cleanly (Close waits on inbound delivery goroutines).
	close(release)
	router.WaitForInboundDeliveries()
}

// TestWaitForInboundDeliveriesDrainsPendingCallbacks verifies that
// WaitForInboundDeliveries blocks until every delivery goroutine dispatched by
// deliveryPacket has finished, giving callers a deterministic drain point.
func TestWaitForInboundDeliveriesDrainsPendingCallbacks(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	_, err := router.RegisterDeliveryIdentity(destID, "dest", nil)
	mustTest(t, err)

	message := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	mustTest(t, message.Pack())

	var delivered int
	router.RegisterDeliveryCallback(func(_ *Message) { delivered++ })

	packet := &rns.Packet{DestinationType: rns.DestinationLink}
	router.deliveryPacket(message.Packed, packet)
	router.deliveryPacket(message.Packed, packet)

	router.WaitForInboundDeliveries()
	if delivered != 1 {
		t.Fatalf("delivered count=%v want=1 (second call is a duplicate)", delivered)
	}
}
