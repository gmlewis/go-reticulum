// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

const tempDirPrefix = "lxmf-test-"

func TestNewRouterRequiresStoragePath(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	if _, err := NewRouter(ts, nil, ""); err == nil {
		t.Fatal("expected error when storage path is empty")
	}
}

func TestRegisterDeliveryIdentitySingleDestinationOnly(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	if _, err := router.RegisterDeliveryIdentity(id, "", &zero); err != nil {
		t.Fatalf("RegisterDeliveryIdentity #1: %v", err)
	}

	id2, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity #2: %v", err)
	}
	if _, err := router.RegisterDeliveryIdentity(id2, "", &zero); err == nil {
		t.Fatal("expected second RegisterDeliveryIdentity call to fail")
	}
}

func TestHandleOutboundValidatesMessage(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if err := router.HandleOutbound(nil); err == nil {
		t.Fatal("expected nil message error")
	}
}

func TestHandleOutboundIncludesReplyTicketBeforePacking(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.IncludeTicket = true

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	entry := outboundTicketFieldEntry(msg.Fields, now)
	if entry == nil {
		t.Fatal("expected outbound message to include a reply ticket")
	}
	if entry.Expires <= float64(now.UnixNano())/1e9 {
		t.Fatalf("ticket expiry=%v want future timestamp", entry.Expires)
	}

	inboundTickets := router.ticketStore.InboundTickets(destination.Hash, now)
	if len(inboundTickets) != 1 {
		t.Fatalf("inbound tickets=%d want=1", len(inboundTickets))
	}
	if !bytes.Equal(inboundTickets[0], entry.Ticket) {
		t.Fatalf("generated ticket=%x want=%x", inboundTickets[0], entry.Ticket)
	}
}

func TestHandleOutboundDeliveryMarksTicketDelivery(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }

	var sentPacket *rns.Packet
	router.sendPacket = func(packet *rns.Packet) error {
		sentPacket = packet
		packet.Receipt = &rns.PacketReceipt{}
		return nil
	}

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.IncludeTicket = true

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if sentPacket == nil || sentPacket.Receipt == nil {
		t.Fatal("expected HandleOutbound to send a packet with a receipt")
	}

	sentPacket.Receipt.TriggerDelivery()

	if got, want := msg.State, StateDelivered; got != want {
		t.Fatalf("message state=%v want=%v", got, want)
	}
	if got := router.ticketStore.lastDeliveries[string(destination.Hash)]; got != float64(now.UnixNano())/1e9 {
		t.Fatalf("last delivery=%v want=%v", got, float64(now.UnixNano())/1e9)
	}
}

func TestHandleOutboundUsesCachedOutboundTicketForStamp(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }

	ticket := bytes.Repeat([]byte{0x4a}, TicketLength)
	expiry := float64(now.Add(48*time.Hour).UnixNano()) / 1e9
	router.ticketStore.RememberOutboundTicket(destination.Hash, TicketEntry{Expires: expiry, Ticket: ticket})

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.DeferStamp {
		t.Fatal("expected outbound ticket to disable deferred stamping")
	}
	if !bytes.Equal(msg.OutboundTicket, ticket) {
		t.Fatalf("outbound ticket=%x want=%x", msg.OutboundTicket, ticket)
	}
	expectedStamp := rns.TruncatedHash(append(append([]byte{}, ticket...), msg.MessageID...))
	if !bytes.Equal(msg.Stamp, expectedStamp) {
		t.Fatalf("stamp=%x want=%x", msg.Stamp, expectedStamp)
	}

	unpacked, err := UnpackMessageFromBytes(ts, msg.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("expected packed ticket-backed stamp message to validate, reason=%v", unpacked.UnverifiedReason)
	}
	if !bytes.Equal(unpacked.Stamp, expectedStamp) {
		t.Fatalf("unpacked stamp=%x want=%x", unpacked.Stamp, expectedStamp)
	}
}

func TestHandleOutboundAutoconfiguresOutboundStampCostBeforePacking(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }
	router.updateStampCost(destination.Hash, 4)

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DeferStamp = false

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.StampCost == nil || *msg.StampCost != 4 {
		t.Fatalf("stamp cost=%v want=4", msg.StampCost)
	}
	if len(msg.Stamp) != StampSize {
		t.Fatalf("stamp length=%v want=%v", len(msg.Stamp), StampSize)
	}
	workblock, err := StampWorkblock(msg.MessageID, WorkblockExpandRounds)
	if err != nil {
		t.Fatalf("StampWorkblock: %v", err)
	}
	if !StampValid(msg.Stamp, *msg.StampCost, workblock) {
		t.Fatal("generated stamp should satisfy announced outbound cost")
	}

	unpacked, err := UnpackMessageFromBytes(ts, msg.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("expected packed announced-cost message to validate, reason=%v", unpacked.UnverifiedReason)
	}
}

func TestHandleOutboundDisablesDeferredStampWhenNoStampIsRequired(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.DeferStamp {
		t.Fatal("expected HandleOutbound to clear deferred stamping when no stamp is required")
	}
	if len(msg.Stamp) != 0 {
		t.Fatalf("stamp=%x want empty", msg.Stamp)
	}
}

func TestProcessOutboundDirectRequestsPathWhenUnavailable(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	requestCount := 0
	router.requestPath = func(_ []byte) error { requestCount++; return nil }
	router.sendPacket = func(_ *rns.Packet) error {
		t.Fatal("sendPacket should not be called when direct path is unavailable")
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if requestCount != 1 {
		t.Fatalf("request path count=%v want=1", requestCount)
	}
	if msg.State != StateOutbound {
		t.Fatalf("state=%v want=%v", msg.State, StateOutbound)
	}
	if msg.DeliveryAttempts != 1 {
		t.Fatalf("attempts=%v want=1", msg.DeliveryAttempts)
	}
	if msg.NextDeliveryAttempt <= float64(now.UnixNano())/1e9 {
		t.Fatal("expected next delivery attempt to be scheduled in the future")
	}
}

func TestProcessOutboundOpportunisticPathRecoversWithoutPathRequest(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodOpportunistic

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	hasPath := false
	router.hasPath = func(_ []byte) bool { return hasPath }
	requestCount := 0
	router.requestPath = func(_ []byte) error { requestCount++; return nil }

	sendCount := 0
	router.sendPacket = func(packet *rns.Packet) error {
		sendCount++
		if packet == nil {
			t.Fatal("packet should not be nil")
		}
		if got := len(packet.Data); got != len(msg.Packed)-DestinationLength {
			t.Fatalf("opportunistic packet data length=%v want=%v", got, len(msg.Packed)-DestinationLength)
		}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if sendCount != 0 {
		t.Fatalf("send count=%v want=0", sendCount)
	}
	if requestCount != 0 {
		t.Fatalf("request path count=%v want=0", requestCount)
	}
	if msg.State != StateOutbound {
		t.Fatalf("state=%v want=%v", msg.State, StateOutbound)
	}
	if msg.DeliveryAttempts != 1 {
		t.Fatalf("attempts=%v want=1", msg.DeliveryAttempts)
	}

	now = now.Add(deliveryRetryWait + time.Second)
	hasPath = true
	router.ProcessOutbound()

	if sendCount != 1 {
		t.Fatalf("send count after path recovery=%v want=1", sendCount)
	}
	if requestCount != 0 {
		t.Fatalf("request path count after path recovery=%v want=0", requestCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state after path recovery=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundOpportunisticEscalatesToPathRequestThenSends(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodOpportunistic

	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }
	hasPath := false
	router.hasPath = func(_ []byte) bool { return hasPath }
	requestCount := 0
	router.requestPath = func(_ []byte) error { requestCount++; return nil }

	sendCount := 0
	router.sendPacket = func(_ *rns.Packet) error {
		sendCount++
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if msg.DeliveryAttempts != 1 {
		t.Fatalf("attempts after first pathless run=%v want=1", msg.DeliveryAttempts)
	}
	if requestCount != 0 {
		t.Fatalf("request path count after first pathless run=%v want=0", requestCount)
	}

	now = now.Add(deliveryRetryWait + time.Second)
	router.ProcessOutbound()
	if msg.DeliveryAttempts != 2 {
		t.Fatalf("attempts after second pathless run=%v want=2", msg.DeliveryAttempts)
	}
	if requestCount != 1 {
		t.Fatalf("request path count after second pathless run=%v want=1", requestCount)
	}
	if sendCount != 0 {
		t.Fatalf("send count after second pathless run=%v want=0", sendCount)
	}

	now = now.Add(pathRequestWait + time.Second)
	hasPath = true
	router.ProcessOutbound()

	if sendCount != 1 {
		t.Fatalf("send count after path request recovery=%v want=1", sendCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state after path request recovery=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundSendFailureEventuallyFails(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error { return assertErr("send failed") }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	for i := 0; i < maxDeliveryAttempts+1; i++ {
		now = now.Add(deliveryRetryWait + time.Second)
		router.ProcessOutbound()
	}

	if msg.State != StateFailed {
		t.Fatalf("state=%v want=%v", msg.State, StateFailed)
	}
}

func TestProcessOutboundSendSuccessSetsSent(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error { return nil }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundSentMessageNotResentUntilTimeout(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	sendCount := 0
	router.sendPacket = func(_ *rns.Packet) error {
		sendCount++
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if sendCount != 1 {
		t.Fatalf("send count=%v want=1", sendCount)
	}

	router.ProcessOutbound()
	if sendCount != 1 {
		t.Fatalf("send count after ProcessOutbound=%v want=1", sendCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundTimeoutRequeuesForRetry(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	sendCount := 0
	var lastPacket *rns.Packet
	router.sendPacket = func(packet *rns.Packet) error {
		sendCount++
		lastPacket = packet
		if packet.Receipt == nil {
			packet.Receipt = &rns.PacketReceipt{}
		}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if sendCount != 1 {
		t.Fatalf("send count=%v want=1", sendCount)
	}
	if lastPacket == nil || lastPacket.Receipt == nil {
		t.Fatal("expected packet receipt to be created")
	}

	lastPacket.Receipt.TriggerTimeout()
	if msg.State != StateOutbound {
		t.Fatalf("state after timeout=%v want=%v", msg.State, StateOutbound)
	}

	now = now.Add(deliveryRetryWait + time.Second)
	router.ProcessOutbound()
	if sendCount != 2 {
		t.Fatalf("send count after retry=%v want=2", sendCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state after retry=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundDeliveryCallbackSetsDeliveredAndPreventsTimeoutRequeue(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	var lastPacket *rns.Packet
	router.sendPacket = func(packet *rns.Packet) error {
		lastPacket = packet
		if packet.Receipt == nil {
			packet.Receipt = &rns.PacketReceipt{}
		}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if lastPacket == nil || lastPacket.Receipt == nil {
		t.Fatal("expected packet receipt")
	}

	lastPacket.Receipt.TriggerDelivery()
	if msg.State != StateDelivered {
		t.Fatalf("state after delivery=%v want=%v", msg.State, StateDelivered)
	}

	lastPacket.Receipt.TriggerTimeout()
	if msg.State != StateDelivered {
		t.Fatalf("state after timeout post-delivery=%v want=%v", msg.State, StateDelivered)
	}

	router.ProcessOutbound()
	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pending outbound count=%v want=0", len(router.pendingOutbound))
	}
}

func TestProcessOutboundSelectsPacketRepresentation(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "short content", "title", nil)

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	packetCount := 0
	router.sendPacket = func(_ *rns.Packet) error {
		packetCount++
		return nil
	}
	router.sendResource = func(_ *Message) error {
		t.Fatal("sendResource should not be called for packet-sized message")
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if packetCount != 1 {
		t.Fatalf("packet send count=%v want=1", packetCount)
	}
	if msg.Representation != RepresentationPacket {
		t.Fatalf("representation=%v want=%v", msg.Representation, RepresentationPacket)
	}
}

func TestProcessOutboundSelectsResourceRepresentation(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'A'
	}

	msg := mustTestNewMessage(t, destination, sourceDest, string(content), "title", nil)

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error {
		t.Fatal("sendPacket should not be called for resource-sized message")
		return nil
	}
	resourceCount := 0
	router.sendResource = func(_ *Message) error {
		resourceCount++
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if resourceCount != 1 {
		t.Fatalf("resource send count=%v want=1", resourceCount)
	}
	if msg.Representation != RepresentationResource {
		t.Fatalf("representation=%v want=%v", msg.Representation, RepresentationResource)
	}
	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundResourceUnsupportedFails(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'B'
	}

	msg := mustTestNewMessage(t, destination, sourceDest, string(content), "title", nil)

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error {
		t.Fatal("sendPacket should not be called for resource-sized message")
		return nil
	}
	router.sendResource = func(_ *Message) error {
		return errResourceRepresentationNotSupported
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.Representation != RepresentationResource {
		t.Fatalf("representation=%v want=%v", msg.Representation, RepresentationResource)
	}
	if msg.State != StateFailed {
		t.Fatalf("state=%v want=%v", msg.State, StateFailed)
	}
	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pending outbound count=%v want=0", len(router.pendingOutbound))
	}
}

func TestProcessOutboundResourceLinkPendingRetryNoAttemptIncrement(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'C'
	}
	msg := mustTestNewMessage(t, destination, sourceDest, string(content), "title", nil)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendResource = func(_ *Message) error { return errResourceLinkPending }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.State != StateOutbound {
		t.Fatalf("state=%v want=%v", msg.State, StateOutbound)
	}
	if msg.Representation != RepresentationResource {
		t.Fatalf("representation=%v want=%v", msg.Representation, RepresentationResource)
	}
	if msg.DeliveryAttempts != 0 {
		t.Fatalf("attempts=%v want=0", msg.DeliveryAttempts)
	}
	if msg.NextDeliveryAttempt <= float64(now.UnixNano())/1e9 {
		t.Fatal("expected next delivery attempt to be scheduled")
	}
	if len(router.pendingOutbound) != 1 {
		t.Fatalf("pending outbound count=%v want=1", len(router.pendingOutbound))
	}
}

func TestSendMessageResourceLockedEstablishError(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'D'
	}
	msg := mustTestNewMessage(t, destination, sourceDest, string(content), "title", nil)
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	wantErr := errors.New("establish failed")
	router.newLink = func(rns.Transport, *rns.Destination) (*rns.Link, error) {
		return nil, wantErr
	}

	if err := router.sendMessageResourceLocked(msg); !errors.Is(err, wantErr) {
		t.Fatalf("error=%v want=%v", err, wantErr)
	}
}

func TestProcessOutboundResourceSendFailureEventuallyFails(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'E'
	}
	msg := mustTestNewMessage(t, destination, sourceDest, string(content), "title", nil)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendResource = func(_ *Message) error { return assertErr("resource send failed") }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	for i := 0; i < maxDeliveryAttempts+1; i++ {
		now = now.Add(deliveryRetryWait + time.Second)
		router.ProcessOutbound()
	}

	if msg.State != StateFailed {
		t.Fatalf("state=%v want=%v", msg.State, StateFailed)
	}
}

func TestProcessOutboundResourceSendRetriesThenSucceeds(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'F'
	}
	msg := mustTestNewMessage(t, destination, sourceDest, string(content), "title", nil)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	attempt := 0
	router.sendResource = func(_ *Message) error {
		attempt++
		if attempt < 3 {
			return assertErr("transient resource error")
		}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	now = now.Add(deliveryRetryWait + time.Second)
	router.ProcessOutbound()
	now = now.Add(deliveryRetryWait + time.Second)
	router.ProcessOutbound()

	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
	if msg.DeliveryAttempts != 2 {
		t.Fatalf("delivery attempts=%v want=%v", msg.DeliveryAttempts, 2)
	}
}

func TestProcessOutboundDropsTerminalStatesFromQueue(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	msgDelivered := &Message{State: StateDelivered}
	msgFailed := &Message{State: StateFailed}
	msgCancelled := &Message{State: StateCancelled}

	router.pendingOutbound = []*Message{msgDelivered, msgFailed, msgCancelled}
	router.ProcessOutbound()

	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pending outbound count=%v want=0", len(router.pendingOutbound))
	}
}

func TestHandleInboundResourceDataDeliversMessage(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "resource-content", "resource-title", nil)
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	var received *Message
	router.RegisterDeliveryCallback(func(m *Message) {
		received = m
	})

	router.handleInboundResourceData(msg.Packed)

	if received == nil {
		t.Fatal("expected delivery callback to receive message")
	}
	if received.ContentString() != "resource-content" {
		t.Fatalf("content=%q want=%q", received.ContentString(), "resource-content")
	}
	if received.Method != MethodDirect {
		t.Fatalf("method=%v want=%v", received.Method, MethodDirect)
	}
}

func TestHandleInboundResourceDataIgnoresInvalidPayload(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	called := false
	router.RegisterDeliveryCallback(func(_ *Message) {
		called = true
	})

	router.handleInboundResourceData([]byte("not-an-lxmf-payload"))

	if called {
		t.Fatal("delivery callback should not be invoked for invalid payload")
	}
}

func TestRegisterPropagationControlDestination(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destination, err := router.RegisterPropagationControlDestination(nil)
	if err != nil {
		t.Fatalf("RegisterPropagationControlDestination: %v", err)
	}
	if destination == nil {
		t.Fatal("expected non-nil control destination")
	}

	again, err := router.RegisterPropagationControlDestination(nil)
	if err != nil {
		t.Fatalf("RegisterPropagationControlDestination second call: %v", err)
	}
	if again != destination {
		t.Fatal("expected idempotent control destination registration")
	}
}

func TestControlStatsGetRequest(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)

	router.clientPropagationMessagesReceived = 11
	router.clientPropagationMessagesServed = 7
	router.unpeeredPropagationIncoming = 3
	router.unpeeredPropagationRXBytes = 512
	router.peers["peer-1"] = &Peer{destinationHash: []byte("peer-1"), lastHeard: peerTime(time.Now())}
	router.peers["peer-2"] = &Peer{destinationHash: []byte("peer-2"), lastHeard: peerTime(time.Now())}

	responseAny := router.statsGetRequest("", nil, nil, nil, remoteIdentity, time.Now())
	response, ok := responseAny.(map[string]any)
	if !ok {
		t.Fatalf("unexpected response type %T", responseAny)
	}

	if got := response["client_propagation_messages_received"]; got != 11 {
		t.Fatalf("client_propagation_messages_received=%v want=11", got)
	}
	if got := response["client_propagation_messages_served"]; got != 7 {
		t.Fatalf("client_propagation_messages_served=%v want=7", got)
	}
	if got := response["unpeered_propagation_incoming"]; got != 3 {
		t.Fatalf("unpeered_propagation_incoming=%v want=3", got)
	}
	if got := response["unpeered_propagation_rx_bytes"]; got != 512 {
		t.Fatalf("unpeered_propagation_rx_bytes=%v want=512", got)
	}
	if got := response["peer_count"]; got != 2 {
		t.Fatalf("peer_count=%v want=2", got)
	}
}

func TestControlStatsGetRequestAccessErrors(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if got := router.statsGetRequest("", nil, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("stats no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	allowedIdentity := mustTestNewIdentity(t, true)
	router.controlAllowed[string(allowedIdentity.Hash)] = struct{}{}

	notAllowedIdentity := mustTestNewIdentity(t, true)

	if got := router.statsGetRequest("", nil, nil, nil, notAllowedIdentity, time.Now()); got != peerErrorNoAccess {
		t.Fatalf("stats no access=%v want=%v", got, peerErrorNoAccess)
	}
}

func TestControlPeerSyncAndUnpeerRequests(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)

	peerIdentity := mustTestNewIdentity(t, true)
	peerHash := append([]byte{}, peerIdentity.Hash...)
	router.peers[string(peerHash)] = &Peer{destinationHash: append([]byte{}, peerHash...), lastHeard: peerTime(time.Now().Add(-time.Hour))}

	syncResponse := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, time.Now())
	if syncResponse != true {
		t.Fatalf("sync response=%v want=true", syncResponse)
	}

	unpeerResponse := router.peerUnpeerRequest("", peerHash, nil, nil, remoteIdentity, time.Now())
	if unpeerResponse != true {
		t.Fatalf("unpeer response=%v want=true", unpeerResponse)
	}
	if _, exists := router.peers[string(peerHash)]; exists {
		t.Fatal("expected peer to be removed after unpeer")
	}
}

func TestControlPeerSyncBackoff(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	if err := router.SetPeerSyncBackoff(10 * time.Second); err != nil {
		t.Fatalf("SetPeerSyncBackoff: %v", err)
	}

	remoteIdentity := mustTestNewIdentity(t, true)

	peerIdentity := mustTestNewIdentity(t, true)
	peerHash := append([]byte{}, peerIdentity.Hash...)
	router.peers[string(peerHash)] = &Peer{destinationHash: append([]byte{}, peerHash...), lastHeard: peerTime(now)}

	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, now); got != peerErrorThrottled {
		t.Fatalf("sync throttled=%v want=%v", got, peerErrorThrottled)
	}

	now = now.Add(11 * time.Second)
	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, now); got != true {
		t.Fatalf("sync after backoff=%v want=true", got)
	}
}

func TestPruneStalePeers(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	if err := router.SetPeerMaxAge(30 * time.Second); err != nil {
		t.Fatalf("SetPeerMaxAge: %v", err)
	}

	peerOld := []byte("peer-old-01234567")
	peerNew := []byte("peer-new-01234567")
	router.peers[string(peerOld)] = &Peer{destinationHash: append([]byte{}, peerOld...), lastHeard: peerTime(now.Add(-2 * time.Minute))}
	router.peers[string(peerNew)] = &Peer{destinationHash: append([]byte{}, peerNew...), lastHeard: peerTime(now.Add(-10 * time.Second))}

	removed := router.PruneStalePeers()
	if removed != 1 {
		t.Fatalf("removed=%v want=1", removed)
	}
	if _, ok := router.peers[string(peerOld)]; ok {
		t.Fatal("expected old peer removed")
	}
	if _, ok := router.peers[string(peerNew)]; !ok {
		t.Fatal("expected recent peer retained")
	}
}

func TestControlPeerSyncAndUnpeerErrors(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if got := router.peerSyncRequest("", nil, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("sync no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	remoteIdentity := mustTestNewIdentity(t, true)

	allowedIdentity := mustTestNewIdentity(t, true)
	router.controlAllowed[string(allowedIdentity.Hash)] = struct{}{}

	if got := router.peerSyncRequest("", make([]byte, rns.TruncatedHashLength/8), nil, nil, remoteIdentity, time.Now()); got != peerErrorNoAccess {
		t.Fatalf("sync no access=%v want=%v", got, peerErrorNoAccess)
	}
	if got := router.peerUnpeerRequest("", make([]byte, rns.TruncatedHashLength/8), nil, nil, remoteIdentity, time.Now()); got != peerErrorNoAccess {
		t.Fatalf("unpeer no access=%v want=%v", got, peerErrorNoAccess)
	}

	router.controlAllowed = map[string]struct{}{}
	router.controlAllowed[string(remoteIdentity.Hash)] = struct{}{}

	if got := router.peerSyncRequest("", []byte("short"), nil, nil, remoteIdentity, time.Now()); got != peerErrorInvalidData {
		t.Fatalf("sync invalid data=%v want=%v", got, peerErrorInvalidData)
	}

	peerIdentity := mustTestNewIdentity(t, true)
	peerHash := append([]byte{}, peerIdentity.Hash...)

	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != peerErrorNotFound {
		t.Fatalf("sync not found=%v want=%v", got, peerErrorNotFound)
	}

	if got := router.peerUnpeerRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != peerErrorNotFound {
		t.Fatalf("unpeer not found=%v want=%v", got, peerErrorNotFound)
	}
}

func TestRegisterPropagationDestination(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destination, err := router.RegisterPropagationDestination()
	if err != nil {
		t.Fatalf("RegisterPropagationDestination: %v", err)
	}
	if destination == nil {
		t.Fatal("expected non-nil propagation destination")
	}

	again, err := router.RegisterPropagationDestination()
	if err != nil {
		t.Fatalf("RegisterPropagationDestination second call: %v", err)
	}
	if again != destination {
		t.Fatal("expected idempotent propagation destination registration")
	}
}

func TestOfferRequestReturnsWantedIDs(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)

	destinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	haveID := router.storePropagationMessage(destinationHash, []byte("msg-have"))
	wantID := rns.FullHash([]byte("missing"))

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)
	validKey, _, _, err := GenerateStamp(peeringID, router.peeringCost, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp(valid key): %v", err)
	}

	requestData, err := msgpack.Pack([]any{validKey, []any{haveID, wantID}})
	if err != nil {
		t.Fatalf("Pack request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	wanted, ok := response.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", response)
	}
	if len(wanted) != 1 {
		t.Fatalf("wanted len=%v want=1", len(wanted))
	}
	wantedID, ok := wanted[0].([]byte)
	if !ok {
		t.Fatalf("unexpected wanted id type %T", wanted[0])
	}
	if string(wantedID) != string(wantID) {
		t.Fatalf("wanted id mismatch got=%x want=%x", wantedID, wantID)
	}
}

func TestOfferRequestInvalidKey(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)

	wantID := rns.FullHash([]byte("missing"))
	requestData, err := msgpack.Pack([]any{nil, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != peerErrorInvalidKey {
		t.Fatalf("response=%v want=%v", response, peerErrorInvalidKey)
	}
}

func TestOfferRequestThrottled(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	remoteIdentity := mustTestNewIdentity(t, true)

	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.throttledPeers[string(remotePropagationHash)] = now.Add(time.Minute)

	wantID := rns.FullHash([]byte("throttled-wanted"))
	requestData, err := msgpack.Pack([]any{[]byte("key"), []any{wantID}})
	if err != nil {
		t.Fatalf("Pack request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, now)
	if response != peerErrorThrottled {
		t.Fatalf("response=%v want=%v", response, peerErrorThrottled)
	}
}

func TestOfferRequestStaticOnlyNoAccess(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)

	router.fromStaticOnly = true

	wantID := rns.FullHash([]byte("static-only-wanted"))
	requestData, err := msgpack.Pack([]any{[]byte("key"), []any{wantID}})
	if err != nil {
		t.Fatalf("Pack request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != peerErrorNoAccess {
		t.Fatalf("response=%v want=%v", response, peerErrorNoAccess)
	}
}

func TestOfferRequestPeeringCostValidation(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.peeringCost = 2

	remoteIdentity := mustTestNewIdentity(t, true)

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)
	validKey, _, _, err := GenerateStamp(peeringID, router.peeringCost, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp(valid key): %v", err)
	}

	wantID := rns.FullHash([]byte("peering-cost-validation"))

	validRequest, err := msgpack.Pack([]any{validKey, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack valid request: %v", err)
	}
	validResponse := router.offerRequest("", validRequest, nil, []byte("link"), remoteIdentity, time.Now())
	if validResponse != true {
		t.Fatalf("valid response=%v want=true", validResponse)
	}

	invalidRequest, err := msgpack.Pack([]any{[]byte{}, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack invalid request: %v", err)
	}
	invalidResponse := router.offerRequest("", invalidRequest, nil, []byte("link"), remoteIdentity, time.Now())
	if invalidResponse != peerErrorInvalidKey {
		t.Fatalf("invalid response=%v want=%v", invalidResponse, peerErrorInvalidKey)
	}
}

func TestRouterPolicyConfigurationAPIs(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	if err := router.SetPeeringCost(-1); err == nil {
		t.Fatal("expected SetPeeringCost to reject negative cost")
	}
	if err := router.SetPeeringCost(2); err != nil {
		t.Fatalf("SetPeeringCost: %v", err)
	}

	if err := router.SetStaticPeers([][]byte{[]byte("short")}); err == nil {
		t.Fatal("expected SetStaticPeers to reject invalid hash length")
	}

	remoteIdentity := mustTestNewIdentity(t, true)
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")

	if err := router.SetStaticPeers([][]byte{remotePropagationHash}); err != nil {
		t.Fatalf("SetStaticPeers: %v", err)
	}
	router.SetFromStaticOnly(true)

	if err := router.ThrottlePeer([]byte("short"), time.Second); err == nil {
		t.Fatal("expected ThrottlePeer to reject invalid hash length")
	}
	if err := router.ThrottlePeer(remotePropagationHash, time.Minute); err != nil {
		t.Fatalf("ThrottlePeer(add): %v", err)
	}

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)
	validKey, _, _, err := GenerateStamp(peeringID, 2, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}
	wantID := rns.FullHash([]byte("policy-config-want"))
	requestData, err := msgpack.Pack([]any{validKey, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	throttled := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, now)
	if throttled != peerErrorThrottled {
		t.Fatalf("throttled response=%v want=%v", throttled, peerErrorThrottled)
	}

	if err := router.ThrottlePeer(remotePropagationHash, 0); err != nil {
		t.Fatalf("ThrottlePeer(clear): %v", err)
	}

	allowed := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, now)
	if allowed != true {
		t.Fatalf("allowed response=%v want=true", allowed)
	}

	nonStaticIdentity := mustTestNewIdentity(t, true)
	nonStaticPeeringID := make([]byte, 0, len(router.identity.Hash)+len(nonStaticIdentity.Hash))
	nonStaticPeeringID = append(nonStaticPeeringID, router.identity.Hash...)
	nonStaticPeeringID = append(nonStaticPeeringID, nonStaticIdentity.Hash...)
	nonStaticKey, _, _, err := GenerateStamp(nonStaticPeeringID, 2, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp(non-static): %v", err)
	}
	nonStaticRequest, err := msgpack.Pack([]any{nonStaticKey, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack non-static request: %v", err)
	}
	nonStaticResp := router.offerRequest("", nonStaticRequest, nil, []byte("link"), nonStaticIdentity, now)
	if nonStaticResp != peerErrorNoAccess {
		t.Fatalf("non-static response=%v want=%v", nonStaticResp, peerErrorNoAccess)
	}
}

func TestApplyPolicyConfig(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")

	cfg := map[string]any{
		"peering_cost":      float64(2),
		"from_static_only":  true,
		"static_peers":      []any{hex.EncodeToString(remotePropagationHash)},
		"auth_required":     true,
		"allowed_list":      []any{hex.EncodeToString(remoteIdentity.Hash)},
		"peer_sync_backoff": float64(1.5),
		"peer_max_age":      float64(120),
	}

	if err := router.ApplyPolicyConfig(cfg); err != nil {
		t.Fatalf("ApplyPolicyConfig: %v", err)
	}

	if router.peeringCost != 2 {
		t.Fatalf("peeringCost=%v want=2", router.peeringCost)
	}
	if !router.fromStaticOnly {
		t.Fatal("expected fromStaticOnly=true")
	}
	if _, ok := router.staticPeers[string(remotePropagationHash)]; !ok {
		t.Fatal("expected remote propagation hash in static peers")
	}
	if !router.authRequired {
		t.Fatal("expected authRequired=true")
	}
	if _, ok := router.allowedList[string(remoteIdentity.Hash)]; !ok {
		t.Fatal("expected remote identity hash in allowed list")
	}
	if router.peerSyncBackoff != 1500*time.Millisecond {
		t.Fatalf("peerSyncBackoff=%v want=%v", router.peerSyncBackoff, 1500*time.Millisecond)
	}
	if router.peerMaxAge != 120*time.Second {
		t.Fatalf("peerMaxAge=%v want=%v", router.peerMaxAge, 120*time.Second)
	}

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)
	validKey, _, _, err := GenerateStamp(peeringID, 2, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}
	wantID := rns.FullHash([]byte("apply-policy-config"))
	requestData, err := msgpack.Pack([]any{validKey, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}
	if got := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now()); got != true {
		t.Fatalf("offer response=%v want=true", got)
	}
}

func TestApplyPolicyConfigErrors(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if err := router.ApplyPolicyConfig(map[string]any{"peering_cost": "bad"}); err == nil {
		t.Fatal("expected peering_cost parse error")
	}
	if err := router.ApplyPolicyConfig(map[string]any{"from_static_only": "bad"}); err == nil {
		t.Fatal("expected from_static_only parse error")
	}
	if err := router.ApplyPolicyConfig(map[string]any{"static_peers": []any{"not-hex"}}); err == nil {
		t.Fatal("expected static_peers parse error")
	}
	if err := router.ApplyPolicyConfig(map[string]any{"auth_required": "bad"}); err == nil {
		t.Fatal("expected auth_required parse error")
	}
	if err := router.ApplyPolicyConfig(map[string]any{"allowed_list": []any{"not-hex"}}); err == nil {
		t.Fatal("expected allowed_list parse error")
	}
	if err := router.ApplyPolicyConfig(map[string]any{"peer_sync_backoff": "bad"}); err == nil {
		t.Fatal("expected peer_sync_backoff parse error")
	}
	if err := router.ApplyPolicyConfig(map[string]any{"peer_max_age": -1}); err == nil {
		t.Fatal("expected peer_max_age parse error")
	}
}

func TestNewRouterWithConfigAppliesPolicy(t *testing.T) {
	t.Parallel()
	remoteIdentity := mustTestNewIdentity(t, true)
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")

	ts := rns.NewTransportSystem(nil)
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouterWithConfig(t, ts, nil, td, map[string]any{
		"peering_cost":     2,
		"from_static_only": true,
		"static_peers":     []any{hex.EncodeToString(remotePropagationHash)},
		"auth_required":    true,
		"allowed_list":     []any{hex.EncodeToString(remoteIdentity.Hash)},
	})

	if router.peeringCost != 2 {
		t.Fatalf("peeringCost=%v want=2", router.peeringCost)
	}
	if !router.fromStaticOnly {
		t.Fatal("expected fromStaticOnly=true")
	}
	if _, ok := router.staticPeers[string(remotePropagationHash)]; !ok {
		t.Fatal("expected remote propagation hash in static peers")
	}
	if !router.authRequired {
		t.Fatal("expected authRequired=true")
	}
	if _, ok := router.allowedList[string(remoteIdentity.Hash)]; !ok {
		t.Fatal("expected remote identity hash in allowed list")
	}
}

func TestNewRouterWithConfigReturnsPolicyError(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	if _, err := NewRouterWithConfig(ts, nil, td, map[string]any{"peering_cost": "bad"}); err == nil {
		t.Fatal("expected policy config error")
	}
}

func TestMessageGetRequestListAndFetch(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)

	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	otherIdentity := mustTestNewIdentity(t, true)
	otherDestinationHash := rns.CalculateHash(otherIdentity, AppName, "delivery")

	idOne := router.storePropagationMessage(remoteDestinationHash, []byte("payload-1"))
	idTwo := router.storePropagationMessage(remoteDestinationHash, []byte("payload-2"))
	_ = router.storePropagationMessage(otherDestinationHash, []byte("payload-other"))

	listRequest, err := msgpack.Pack([]any{nil, nil})
	if err != nil {
		t.Fatalf("Pack list request: %v", err)
	}

	listResponse := router.messageGetRequest("", listRequest, nil, nil, remoteIdentity, time.Now())
	available, ok := listResponse.([]any)
	if !ok {
		t.Fatalf("unexpected list response type %T", listResponse)
	}
	if len(available) != 2 {
		t.Fatalf("available len=%v want=2", len(available))
	}

	fetchRequest, err := msgpack.Pack([]any{[]any{idOne, idTwo}, []any{idOne}, float64(100)})
	if err != nil {
		t.Fatalf("Pack fetch request: %v", err)
	}

	fetchResponse := router.messageGetRequest("", fetchRequest, nil, nil, remoteIdentity, time.Now())
	payloads, ok := fetchResponse.([]any)
	if !ok {
		t.Fatalf("unexpected fetch response type %T", fetchResponse)
	}
	if len(payloads) != 1 {
		t.Fatalf("payload len=%v want=1", len(payloads))
	}
	payload, ok := payloads[0].([]byte)
	if !ok {
		t.Fatalf("unexpected payload type %T", payloads[0])
	}
	if string(payload) != "payload-2" {
		t.Fatalf("payload=%v want=payload-2", string(payload))
	}

	if _, exists := router.propagationEntries[string(idOne)]; exists {
		t.Fatal("expected haves message to be removed from propagation store")
	}
	if got := router.clientPropagationMessagesServed; got != 1 {
		t.Fatalf("clientPropagationMessagesServed=%v want=1", got)
	}
}

func TestMessageGetRequestPurgeRemovesPersistedFile(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")

	transientID := router.storePropagationMessage(remoteDestinationHash, []byte("persisted-payload"))
	entry := router.propagationEntries[string(transientID)]
	if entry == nil || entry.path == "" {
		t.Fatalf("expected persisted propagation entry for %x", transientID)
	}
	if _, err := os.Stat(entry.path); err != nil {
		t.Fatalf("expected persisted message file, Stat() error = %v", err)
	}

	request, err := msgpack.Pack([]any{nil, []any{transientID}})
	if err != nil {
		t.Fatalf("Pack purge request: %v", err)
	}
	_ = router.messageGetRequest("", request, nil, nil, remoteIdentity, time.Now())

	if _, exists := router.propagationEntries[string(transientID)]; exists {
		t.Fatal("expected purged message to be removed from propagationEntries")
	}
	if _, err := os.Stat(entry.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected persisted message file to be removed, Stat() error = %v", err)
	}
}

func TestMessageGetRequestRequiresIdentity(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	request, err := msgpack.Pack([]any{nil, nil})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	response := router.messageGetRequest("", request, nil, nil, nil, time.Now())
	if response != peerErrorNoIdentity {
		t.Fatalf("response=%v want=%v", response, peerErrorNoIdentity)
	}
}

func TestMessageGetRequestNoAccessWhenAuthRequired(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.authRequired = true

	request, err := msgpack.Pack([]any{nil, nil})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	notAllowedIdentity := mustTestNewIdentity(t, true)
	if response := router.messageGetRequest("", request, nil, nil, notAllowedIdentity, time.Now()); response != peerErrorNoAccess {
		t.Fatalf("message_get no access=%v want=%v", response, peerErrorNoAccess)
	}

	allowedIdentity := mustTestNewIdentity(t, true)
	router.allowedList[string(allowedIdentity.Hash)] = struct{}{}

	response := router.messageGetRequest("", request, nil, nil, allowedIdentity, time.Now())
	if _, ok := response.([]any); !ok {
		t.Fatalf("message_get allowed response type=%T want=[]any", response)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}

func TestDeliveryPacketOpportunisticAndDirect(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)

	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	message := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	var mu sync.Mutex
	received := make([]*Message, 0, 1)
	router.RegisterDeliveryCallback(func(m *Message) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, m)
	})

	opportunisticPacket := &rns.Packet{
		DestinationType: rns.DestinationSingle,
		DestinationHash: destination.Hash,
	}
	router.deliveryPacket(message.Packed[DestinationLength:], opportunisticPacket)

	directPacket := &rns.Packet{
		DestinationType: rns.DestinationLink,
	}
	router.deliveryPacket(message.Packed, directPacket)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("received count=%v want=1", len(received))
	}
	if received[0].ContentString() != "content" {
		t.Fatalf("unexpected content value: %q", received[0].ContentString())
	}
	if received[0].Method != MethodOpportunistic {
		t.Fatalf("first method=%v want=%v", received[0].Method, MethodOpportunistic)
	}
}

func TestNewRouterFromConfig(t *testing.T) {
	t.Parallel()
	id := mustTestNewIdentity(t, true)

	maxPeers := 5
	staticPeer := rns.CalculateHash(id, AppName, "propagation")

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		Identity:         id,
		StoragePath:      tmpDir,
		Autopeer:         true,
		PropagationLimit: 128,
		SyncLimit:        512,
		DeliveryLimit:    200,
		MaxPeers:         &maxPeers,
		StaticPeers:      [][]byte{staticPeer},
		PropagationCost:  20,
	})

	if got := router.PropagationPerTransferLimit(); got != 128 {
		t.Fatalf("PropagationPerTransferLimit=%v want=128", got)
	}
	if got := router.PropagationPerSyncLimit(); got != 512 {
		t.Fatalf("PropagationPerSyncLimit=%v want=512", got)
	}
	if got := router.DeliveryPerTransferLimit(); got != 200 {
		t.Fatalf("DeliveryPerTransferLimit=%v want=200", got)
	}
	if got := router.MaxPeers(); got != 5 {
		t.Fatalf("MaxPeers=%v want=5", got)
	}
	if router.peeringCost != 20 {
		t.Fatalf("peeringCost=%v want=20", router.peeringCost)
	}
}

func TestNewRouterFromConfigDefaults(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath: td,
	})

	if got := router.PropagationPerTransferLimit(); got != DefaultPropagationLimit {
		t.Fatalf("PropagationPerTransferLimit=%v want=%v", got, DefaultPropagationLimit)
	}
	if got := router.PropagationPerSyncLimit(); got != DefaultSyncLimit {
		t.Fatalf("PropagationPerSyncLimit=%v want=%v", got, DefaultSyncLimit)
	}
	if got := router.DeliveryPerTransferLimit(); got != DefaultDeliveryLimit {
		t.Fatalf("DeliveryPerTransferLimit=%v want=%v", got, DefaultDeliveryLimit)
	}
	if got := router.MaxPeers(); got != DefaultMaxPeers {
		t.Fatalf("MaxPeers=%v want=%v", got, DefaultMaxPeers)
	}
	// PropagationCost 0 should be clamped to PropagationCostMin
	if router.peeringCost != PropagationCostMin {
		t.Fatalf("peeringCost=%v want=%v", router.peeringCost, PropagationCostMin)
	}
}

func TestNewRouterFromConfigSyncLimitClamped(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath:      td,
		PropagationLimit: 500,
		SyncLimit:        100, // less than PropagationLimit
	})

	// sync limit should be clamped to propagation limit
	if got := router.PropagationPerSyncLimit(); got != 500 {
		t.Fatalf("PropagationPerSyncLimit=%v want=500 (clamped to propagation limit)", got)
	}
}

func TestRouterIgnoreDestination(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hash := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	if router.IsIgnored(hash) {
		t.Fatal("hash should not be ignored initially")
	}

	router.IgnoreDestination(hash)

	if !router.IsIgnored(hash) {
		t.Fatal("hash should be ignored after IgnoreDestination")
	}
}

func TestRouterEnforceStamps(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if router.StampsEnforced() {
		t.Fatal("stamps should not be enforced initially")
	}

	router.EnforceStamps()

	if !router.StampsEnforced() {
		t.Fatal("stamps should be enforced after EnforceStamps()")
	}
}

func TestRouterMessageStorageLimit(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if got := router.MessageStorageLimit(); got != 0 {
		t.Fatalf("initial MessageStorageLimit=%v want=0", got)
	}

	router.SetMessageStorageLimit(2000)

	if got := router.MessageStorageLimit(); got != 2000 {
		t.Fatalf("MessageStorageLimit=%v want=2000", got)
	}
}

func TestPropagationStoreRemovesExpiredEntryOnEnable(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destinationHash := bytes.Repeat([]byte{0x44}, DestinationLength)
	payload := []byte("expired-payload")
	transientID := rns.FullHash(payload)
	receivedAt := time.Now().Add(-(messageExpiry + 24*time.Hour))
	filePath, _, err := router.writePropagationMessageFile(transientID, receivedAt, 0, destinationHash, payload)
	if err != nil {
		t.Fatalf("writePropagationMessageFile() error = %v", err)
	}

	router.EnablePropagation()

	if _, exists := router.propagationEntries[string(transientID)]; exists {
		t.Fatalf("expected expired propagation entry %x to be removed", transientID)
	}
	if _, err := os.Stat(filePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected expired file to be removed, Stat() error = %v", err)
	}
}

func TestPropagationStoreRespectsStorageLimit(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	prioritisedHash := bytes.Repeat([]byte{0x45}, DestinationLength)
	ordinaryHash := bytes.Repeat([]byte{0x46}, DestinationLength)
	router.Prioritise(prioritisedHash)

	now := time.Unix(1_700_000_000, 0)
	router.now = func() time.Time { return now }

	keepID := router.storePropagationMessage(prioritisedHash, bytes.Repeat([]byte("k"), 40))
	now = now.Add(-10 * 24 * time.Hour)
	dropID := router.storePropagationMessage(ordinaryHash, bytes.Repeat([]byte("d"), 60))

	router.SetMessageStorageLimit(80)
	router.cleanMessageStore()

	if _, exists := router.propagationEntries[string(dropID)]; exists {
		t.Fatalf("expected over-limit ordinary entry %x to be removed", dropID)
	}
	if _, exists := router.propagationEntries[string(keepID)]; !exists {
		t.Fatalf("expected prioritised entry %x to remain", keepID)
	}
	if got := router.messageStorageSize(); got > router.MessageStorageLimit() {
		t.Fatalf("messageStorageSize() = %v, want <= %v", got, router.MessageStorageLimit())
	}
}

func TestPropagationStoreRestartRecovery(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	destinationHash := bytes.Repeat([]byte{0x42}, DestinationLength)
	payload := []byte("stored-after-restart")
	transientID := router.storePropagationMessage(destinationHash, payload)

	messageStorePath := filepath.Join(tmpDir, "lxmf", "messagestore")
	entries, err := os.ReadDir(messageStorePath)
	if err != nil {
		t.Fatalf("ReadDir(messagestore) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("messagestore entries = %v, want 1", len(entries))
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	recovered.EnablePropagation()

	entry := recovered.propagationEntries[string(transientID)]
	if entry == nil {
		t.Fatalf("recovered propagation entry for %x not found", transientID)
	}
	if !bytes.Equal(entry.destinationHash, destinationHash) {
		t.Fatalf("recovered destination hash = %x, want %x", entry.destinationHash, destinationHash)
	}
	if !bytes.Equal(entry.payload, payload) {
		t.Fatalf("recovered payload = %q, want %q", entry.payload, payload)
	}
}

func TestPropagationPeerAndNodeStatsRestartRecovery(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	ts.Remember([]byte("peer-packet"), remoteHash, remoteIdentity.GetPublicKey(), nil)

	peer := NewPeer(router, remoteHash)
	peer.lastHeard = peerTime(time.Now())
	peer.peeringTimebase = 12345
	router.peers[string(remoteHash)] = peer
	router.clientPropagationMessagesReceived = 3
	router.clientPropagationMessagesServed = 4
	router.unpeeredPropagationIncoming = 5
	router.unpeeredPropagationRXBytes = 6

	if err := router.Close(); err != nil {
		t.Fatalf("router.Close(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	recovered.EnablePropagation()

	recoveredPeer := recovered.peers[string(remoteHash)]
	if recoveredPeer == nil {
		t.Fatalf("expected recovered peer for %x", remoteHash)
	}
	if got, want := recoveredPeer.peeringTimebase, peer.peeringTimebase; got != want {
		t.Fatalf("recovered peer peeringTimebase = %v, want %v", got, want)
	}
	if got, want := recoveredPeer.lastHeard, peer.lastHeard; got != want {
		t.Fatalf("recovered peer lastHeard = %v, want %v", got, want)
	}
	if recovered.clientPropagationMessagesReceived != 3 {
		t.Fatalf("clientPropagationMessagesReceived = %v, want 3", recovered.clientPropagationMessagesReceived)
	}
	if recovered.clientPropagationMessagesServed != 4 {
		t.Fatalf("clientPropagationMessagesServed = %v, want 4", recovered.clientPropagationMessagesServed)
	}
	if recovered.unpeeredPropagationIncoming != 5 {
		t.Fatalf("unpeeredPropagationIncoming = %v, want 5", recovered.unpeeredPropagationIncoming)
	}
	if recovered.unpeeredPropagationRXBytes != 6 {
		t.Fatalf("unpeeredPropagationRXBytes = %v, want 6", recovered.unpeeredPropagationRXBytes)
	}
}

func TestLocalTransientIDCachesRestartRecovery(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.now = func() time.Time { return now }

	deliveredID := bytes.Repeat([]byte{0x61}, 32)
	processedID := bytes.Repeat([]byte{0x62}, 32)
	router.locallyDeliveredIDs[string(deliveredID)] = now
	router.locallyProcessedIDs[string(processedID)] = now.Add(-time.Minute)

	if err := router.SaveLocalTransientIDCaches(); err != nil {
		t.Fatalf("SaveLocalTransientIDCaches(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	recovered.now = func() time.Time { return now }

	if got := recovered.locallyDeliveredIDs[string(deliveredID)]; !got.Equal(now) {
		t.Fatalf("recovered delivered timestamp = %v, want %v", got, now)
	}
	if got := recovered.locallyProcessedIDs[string(processedID)]; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("recovered processed timestamp = %v, want %v", got, now.Add(-time.Minute))
	}
}

func TestLocalTransientIDCachesDropExpiredEntriesOnLoad(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	now := time.Unix(1700000000, 0)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.now = func() time.Time { return now }

	expiredID := bytes.Repeat([]byte{0x71}, 32)
	freshID := bytes.Repeat([]byte{0x72}, 32)
	expiredAt := peerTime(now.Add(-transientIDCacheExpiry - time.Second))
	freshAt := peerTime(now.Add(-time.Minute))

	deliveredPayload, err := msgpack.Pack(map[string]any{
		string(expiredID): expiredAt,
		string(freshID):   freshAt,
	})
	if err != nil {
		t.Fatalf("Pack delivered cache: %v", err)
	}
	processedPayload, err := msgpack.Pack(map[string]any{
		string(expiredID): expiredAt,
		string(freshID):   freshAt,
	})
	if err != nil {
		t.Fatalf("Pack processed cache: %v", err)
	}
	if err := os.WriteFile(router.localDeliveriesPath(), deliveredPayload, 0o644); err != nil {
		t.Fatalf("WriteFile(local_deliveries): %v", err)
	}
	if err := os.WriteFile(router.locallyProcessedPath(), processedPayload, 0o644); err != nil {
		t.Fatalf("WriteFile(locally_processed): %v", err)
	}

	router.locallyDeliveredIDs = map[string]time.Time{}
	router.locallyProcessedIDs = map[string]time.Time{}
	if err := router.LoadLocalTransientIDCaches(); err != nil {
		t.Fatalf("LoadLocalTransientIDCaches(): %v", err)
	}

	if _, ok := router.locallyDeliveredIDs[string(expiredID)]; ok {
		t.Fatalf("expired delivered transient ID %x should be dropped", expiredID)
	}
	if _, ok := router.locallyProcessedIDs[string(expiredID)]; ok {
		t.Fatalf("expired processed transient ID %x should be dropped", expiredID)
	}
	if got := router.locallyDeliveredIDs[string(freshID)]; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("fresh delivered timestamp = %v, want %v", got, now.Add(-time.Minute))
	}
	if got := router.locallyProcessedIDs[string(freshID)]; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("fresh processed timestamp = %v, want %v", got, now.Add(-time.Minute))
	}
}

func TestAvailableTicketsSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }

	sourceID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	replyTicketDestinationID := mustTestNewIdentity(t, true)
	replyTicketDestination := mustTestNewDestination(t, ts, replyTicketDestinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	inboundEntry := router.ticketStore.GenerateInboundTicket(replyTicketDestination.Hash, now, DefaultTicketExpirySeconds)
	if inboundEntry == nil {
		t.Fatal("expected inbound ticket")
	}
	router.ticketStore.MarkDelivery(replyTicketDestination.Hash, now)

	outboundTicket := bytes.Repeat([]byte{0x44}, TicketLength)
	expiry := float64(now.Add(48*time.Hour).UnixNano()) / 1e9
	router.ticketStore.RememberOutboundTicket(sourceDest.Hash, TicketEntry{Expires: expiry, Ticket: outboundTicket})

	if err := router.SaveAvailableTickets(); err != nil {
		t.Fatalf("SaveAvailableTickets(): %v", err)
	}

	router.ticketStore = NewTicketStore()
	if err := router.LoadAvailableTickets(); err != nil {
		t.Fatalf("LoadAvailableTickets(): %v", err)
	}

	gotInbound := router.ticketStore.InboundTickets(replyTicketDestination.Hash, now)
	if len(gotInbound) != 1 || !bytes.Equal(gotInbound[0], inboundEntry.Ticket) {
		t.Fatalf("recovered inbound tickets=%x want=%x", gotInbound, inboundEntry.Ticket)
	}
	if got := router.ticketStore.OutboundTicket(sourceDest.Hash, now); !bytes.Equal(got, outboundTicket) {
		t.Fatalf("recovered outbound ticket=%x want=%x", got, outboundTicket)
	}
	if got := router.ticketStore.lastDeliveries[string(replyTicketDestination.Hash)]; got != float64(now.UnixNano())/1e9 {
		t.Fatalf("recovered last delivery=%v want=%v", got, float64(now.UnixNano())/1e9)
	}
}

func TestDeliveryPacketSuppressesDuplicateLocalDelivery(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)

	localID := mustTestNewIdentity(t, true)
	localDestination := mustTestNewDestination(t, ts, localID, rns.DestinationIn, rns.DestinationSingle, AppName, "delivery")
	sourceID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

	var deliveries int
	router.RegisterDeliveryCallback(func(*Message) {
		deliveries++
	})

	message := mustTestNewMessage(t, localDestination, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}

	packet := &rns.Packet{DestinationType: rns.DestinationLink}
	router.deliveryPacket(message.Packed, packet)
	router.deliveryPacket(message.Packed, packet)

	if deliveries != 1 {
		t.Fatalf("deliveries = %v, want 1", deliveries)
	}
	if _, ok := router.locallyDeliveredIDs[string(message.Hash)]; !ok {
		t.Fatalf("expected local delivery cache entry for %x", message.Hash)
	}
}

func TestDeliveryPacketRemembersOutboundTicket(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }

	sourceID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	localID := mustTestNewIdentity(t, true)
	localDestination := mustTestNewDestination(t, ts, localID, rns.DestinationIn, rns.DestinationSingle, AppName, "delivery")

	outboundTicket := bytes.Repeat([]byte{0x44}, TicketLength)
	expiry := float64(now.Add(48*time.Hour).UnixNano()) / 1e9
	inboundMessage := mustTestNewMessage(t, localDestination, sourceDest, "payload", "title", map[any]any{
		FieldTicket: []any{expiry, outboundTicket},
	})
	if err := inboundMessage.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	packet := &rns.Packet{DestinationType: rns.DestinationLink}
	router.deliveryPacket(inboundMessage.Packed, packet)

	if got := router.ticketStore.OutboundTicket(sourceDest.Hash, now); !bytes.Equal(got, outboundTicket) {
		t.Fatalf("outbound ticket=%x want=%x", got, outboundTicket)
	}
}

func TestHandleInboundResourceDataRemembersOutboundTicket(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	now := time.Now().UTC().Truncate(time.Second)
	router.now = func() time.Time { return now }

	sourceID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	localID := mustTestNewIdentity(t, true)
	localDestination := mustTestNewDestination(t, ts, localID, rns.DestinationIn, rns.DestinationSingle, AppName, "delivery")

	outboundTicket := bytes.Repeat([]byte{0x45}, TicketLength)
	expiry := float64(now.Add(48*time.Hour).UnixNano()) / 1e9
	inboundMessage := mustTestNewMessage(t, localDestination, sourceDest, "payload", "title", map[any]any{
		FieldTicket: []any{expiry, outboundTicket},
	})
	if err := inboundMessage.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}

	router.handleInboundResourceData(inboundMessage.Packed)

	if got := router.ticketStore.OutboundTicket(sourceDest.Hash, now); !bytes.Equal(got, outboundTicket) {
		t.Fatalf("outbound ticket=%x want=%x", got, outboundTicket)
	}
}

func TestOutboundStampCostRestartRecovery(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	destinationHash := bytes.Repeat([]byte{0x51}, DestinationLength)
	router.updateStampCost(destinationHash, 8)

	if err := router.Close(); err != nil {
		t.Fatalf("router.Close(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	stampCost, ok := recovered.OutboundStampCost(destinationHash)
	if !ok || stampCost != 8 {
		t.Fatalf("recovered OutboundStampCost = (%v,%v), want (8,true)", stampCost, ok)
	}
}

func TestOutboundStampCostRestartDropsExpiredEntries(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	destinationHash := bytes.Repeat([]byte{0x52}, DestinationLength)
	router.outboundStampCosts[string(destinationHash)] = outboundStampCostEntry{
		updatedAt: time.Unix(0, 0),
		stampCost: 9,
	}
	if err := router.SaveOutboundStampCosts(); err != nil {
		t.Fatalf("router.SaveOutboundStampCosts(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	if stampCost, ok := recovered.OutboundStampCost(destinationHash); ok {
		t.Fatalf("expired OutboundStampCost = (%v,%v), want missing entry", stampCost, ok)
	}
}

func TestMessageGetRequestListSortedByMessageSize(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")

	smallID := router.storePropagationMessage(remoteDestinationHash, []byte("small"))
	largeID := router.storePropagationMessage(remoteDestinationHash, bytes.Repeat([]byte("L"), 50))

	listRequest, err := msgpack.Pack([]any{nil, nil})
	if err != nil {
		t.Fatalf("Pack list request: %v", err)
	}
	listResponse := router.messageGetRequest("", listRequest, nil, nil, remoteIdentity, time.Now())
	available, ok := listResponse.([]any)
	if !ok {
		t.Fatalf("unexpected list response type %T", listResponse)
	}
	if len(available) != 2 {
		t.Fatalf("available len=%v want=2", len(available))
	}

	if got, ok := available[0].([]byte); !ok || !bytes.Equal(got, smallID) {
		t.Fatalf("available[0]=%x want small message %x", got, smallID)
	}
	if got, ok := available[1].([]byte); !ok || !bytes.Equal(got, largeID) {
		t.Fatalf("available[1]=%x want large message %x", got, largeID)
	}
}

func TestRouterPrioritise(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hash := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}

	if router.IsPrioritised(hash) {
		t.Fatal("hash should not be prioritised initially")
	}

	router.Prioritise(hash)

	if !router.IsPrioritised(hash) {
		t.Fatal("hash should be prioritised after Prioritise()")
	}
}

func TestRouterSetInboundStampCost(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	dest, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	cost8 := 8
	if !router.SetInboundStampCost(dest.Hash, &cost8) {
		t.Fatal("SetInboundStampCost should return true")
	}
	got, ok := router.InboundStampCost(dest.Hash)
	if !ok {
		t.Fatal("InboundStampCost should find the destination")
	}
	if got != 8 {
		t.Fatalf("InboundStampCost=%v want=8", got)
	}

	// nil stamp cost should reset to 0
	if !router.SetInboundStampCost(dest.Hash, nil) {
		t.Fatal("SetInboundStampCost(nil) should return true")
	}
	got, _ = router.InboundStampCost(dest.Hash)
	if got != 0 {
		t.Fatalf("InboundStampCost=%v want=0 after nil", got)
	}

	// >= 255 should fail
	cost255 := 255
	if router.SetInboundStampCost(dest.Hash, &cost255) {
		t.Fatal("SetInboundStampCost(255) should return false")
	}

	// Unknown destination should fail
	if router.SetInboundStampCost([]byte("unknown"), &cost8) {
		t.Fatal("SetInboundStampCost for unknown dest should return false")
	}
}

func TestRouterRegistersDeliveryAnnounceHandler(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	_, err := NewRouter(ts, nil, tmpDir)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	found := false
	for _, handler := range ts.AnnounceHandlers() {
		if handler != nil && handler.AspectFilter == AppName+".delivery" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("announce handlers = %+v, want %q registered", ts.AnnounceHandlers(), AppName+".delivery")
	}
}

func TestRouterRegistersPropagationAnnounceHandler(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	_, err := NewRouter(ts, nil, tmpDir)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	found := false
	for _, handler := range ts.AnnounceHandlers() {
		if handler != nil && handler.AspectFilter == AppName+".propagation" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("announce handlers = %+v, want %q registered", ts.AnnounceHandlers(), AppName+".propagation")
	}
}

func TestDeliveryAnnounceHandlerUpdatesStampCostAndRetriesPendingOutbound(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	dest, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	message := &Message{
		Destination:         dest,
		DestinationHash:     append([]byte{}, dest.Hash...),
		Method:              MethodDirect,
		NextDeliveryAttempt: float64(time.Now().Add(time.Hour).UnixNano()) / 1e9,
	}
	router.pendingOutbound = append(router.pendingOutbound, message)
	retried := make(chan struct{}, 1)
	router.processOutbound = func() {
		retried <- struct{}{}
	}

	appData, err := msgpack.Pack([]any{[]byte("Carol"), 8})
	if err != nil {
		t.Fatalf("Pack announce app data: %v", err)
	}

	nowSeconds := float64(time.Now().UnixNano()) / 1e9
	router.handleDeliveryAnnounce(dest.Hash, nil, appData)

	stampCost, ok := router.OutboundStampCost(dest.Hash)
	if !ok || stampCost != 8 {
		t.Fatalf("OutboundStampCost = (%v,%v), want (8,true)", stampCost, ok)
	}
	if message.NextDeliveryAttempt > nowSeconds+0.5 {
		t.Fatalf("NextDeliveryAttempt = %v, want immediate retry", message.NextDeliveryAttempt)
	}
	select {
	case <-retried:
	case <-time.After(time.Second):
		t.Fatal("delivery announce did not trigger outbound processing")
	}
}

func TestPropagationAnnounceHandlerAutopeersWithinDepth(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.maxPeeringCost = 26
	router.hopsTo = func([]byte) int { return 2 }

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	appData, err := msgpack.Pack([]any{
		false,
		1700000000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node A")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}

	router.handlePropagationAnnounce(remoteHash, remoteIdentity, appData)

	peer := router.peers[string(remoteHash)]
	if peer == nil {
		t.Fatal("expected peer to be auto-created")
	}
	if !peer.alive {
		t.Fatal("expected peer to be alive")
	}
	if peer.peeringTimebase != 1700000000 {
		t.Fatalf("peeringTimebase=%v want=1700000000", peer.peeringTimebase)
	}
	if peer.propagationTransferLimit == nil || *peer.propagationTransferLimit != 128 {
		t.Fatalf("propagationTransferLimit=%v want=128", peer.propagationTransferLimit)
	}
	if peer.propagationSyncLimit == nil || *peer.propagationSyncLimit != 256 {
		t.Fatalf("propagationSyncLimit=%v want=256", peer.propagationSyncLimit)
	}
	if peer.propagationStampCost == nil || *peer.propagationStampCost != 11 {
		t.Fatalf("propagationStampCost=%v want=11", peer.propagationStampCost)
	}
	if peer.propagationStampCostFlexibility == nil || *peer.propagationStampCostFlexibility != 3 {
		t.Fatalf("propagationStampCostFlexibility=%v want=3", peer.propagationStampCostFlexibility)
	}
	if peer.peeringCost == nil || *peer.peeringCost != 7 {
		t.Fatalf("peeringCost=%v want=7", peer.peeringCost)
	}
	if got := peer.metadata[int64(PNMetaName)]; !bytes.Equal(got.([]byte), []byte("Node A")) {
		t.Fatalf("metadata name=%v want %q", got, "Node A")
	}
}

func TestPropagationAnnounceHandlerUnpeersDisabledOrOutOfRangeNodes(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.maxPeeringCost = 26

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.peers[string(remoteHash)] = &Peer{
		destinationHash: append([]byte{}, remoteHash...),
		peeringTimebase: 10,
	}

	outOfRangeAppData, err := msgpack.Pack([]any{
		false,
		11,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{},
	})
	if err != nil {
		t.Fatalf("Pack out-of-range app data: %v", err)
	}
	router.hopsTo = func([]byte) int { return 99 }
	router.handlePropagationAnnounce(remoteHash, remoteIdentity, outOfRangeAppData)
	if _, exists := router.peers[string(remoteHash)]; exists {
		t.Fatal("expected out-of-range peer to be removed")
	}

	router.peers[string(remoteHash)] = &Peer{
		destinationHash: append([]byte{}, remoteHash...),
		peeringTimebase: 10,
	}
	disabledAppData, err := msgpack.Pack([]any{
		false,
		12,
		false,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{},
	})
	if err != nil {
		t.Fatalf("Pack disabled app data: %v", err)
	}
	router.hopsTo = func([]byte) int { return 1 }
	router.handlePropagationAnnounce(remoteHash, remoteIdentity, disabledAppData)
	if _, exists := router.peers[string(remoteHash)]; exists {
		t.Fatal("expected disabled propagation node to be unpeered")
	}
}

func TestRouterAnnounce(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	dest, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	// Announce should succeed for registered destination
	if err := router.Announce(dest.Hash); err != nil {
		t.Fatalf("Announce: %v", err)
	}

	// Announce for unknown destination should fail
	if err := router.Announce([]byte("unknown")); err == nil {
		t.Fatal("expected error for unknown destination")
	}
}

func TestRequestMessagesNoPropNode(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	// No propagation node set — should log and return without panicking.
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRIdle {
		t.Fatalf("state = %v, want PRIdle", router.PropagationTransferState())
	}
}

func TestRequestMessagesPathRequested(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = byte(i)
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	// No path available — should transition to PRPathRequested.
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRPathRequested {
		t.Fatalf("state = %v, want PRPathRequested", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.0 {
		t.Fatalf("progress = %v, want 0.0", router.PropagationTransferProgress())
	}
}

func TestRequestMessagesPathTimeoutSetsNoPath(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = byte(i)
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }
	router.pathWaitSleep = func(time.Duration) {}
	router.startRequestMessagesPathJob = func() {
		go func() {
			router.mu.Lock()
			router.wantsDownloadOnPathAvailableAt = router.now()
			router.mu.Unlock()
			router.requestMessagesPathJob()
			close(done)
		}()
	}

	router.RequestMessagesFromPropagationNode(nil)
	<-done

	if router.PropagationTransferState() != PRNoPath {
		t.Fatalf("state = %v, want PRNoPath", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.0 {
		t.Fatalf("progress = %v, want 0.0", router.PropagationTransferProgress())
	}
	if router.wantsDownloadOnPathAvailableFrom != nil || router.wantsDownloadOnPathAvailableTo != nil {
		t.Fatal("expected pending path download state to be cleared after timeout")
	}
}

func TestRequestMessagesPathJobResumesSyncWhenPathAppears(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = byte(i + 1)
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	requested := make(chan struct{}, 1)
	hasPath := false
	router.hasPath = func(_ []byte) bool { return hasPath }
	router.requestPath = func(_ []byte) error { return nil }
	router.linkStatus = func(*rns.Link) int { return rns.LinkActive }
	router.pathWaitSleep = func(time.Duration) {
		hasPath = true
		router.outboundPropagationLink = &rns.Link{}
	}
	router.identifyLink = func(_ *rns.Link, identity *rns.Identity) error {
		if identity != router.identity {
			t.Fatal("path job resumed sync with wrong identity")
		}
		return nil
	}
	router.requestLink = func(_ *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), _ time.Duration) (*rns.RequestReceipt, error) {
		if path != messageGetPath {
			t.Fatalf("request path = %q, want %q", path, messageGetPath)
		}
		requested <- struct{}{}
		return nil, nil
	}
	router.startRequestMessagesPathJob = func() {
		go func() {
			router.requestMessagesPathJob()
			close(done)
		}()
	}

	router.RequestMessagesFromPropagationNode(nil)
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for resumed sync request")
	}
	<-done

	if router.PropagationTransferState() != PRRequestSent {
		t.Fatalf("state = %v, want PRRequestSent", router.PropagationTransferState())
	}
	if router.wantsDownloadOnPathAvailableFrom != nil || router.wantsDownloadOnPathAvailableTo != nil {
		t.Fatal("expected pending path download state to be cleared after path became available")
	}
}

func TestRequestMessagesLinkEstablished(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	// Create and remember a peer identity.
	peerID := mustTestNewIdentity(t, true)
	peerDest := mustTestNewDestination(t, ts, peerID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	ts.Remember(nil, peerDest.Hash, peerID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(peerDest.Hash); err != nil {
		t.Fatal(err)
	}

	// Path available and link succeeds — should wait for establishment before
	// issuing the list request.
	router.hasPath = func(_ []byte) bool { return true }
	link := &rns.Link{}
	var status int
	var establishedCallback func(*rns.Link)
	var establishCount int
	var requestCount int
	router.linkStatus = func(*rns.Link) int { return status }
	router.newLink = func(rns.Transport, *rns.Destination) (*rns.Link, error) {
		return link, nil
	}
	router.setLinkEstablishedCallback = func(got *rns.Link, callback func(*rns.Link)) {
		if got != link {
			t.Fatal("setLinkEstablishedCallback got unexpected link")
		}
		establishedCallback = callback
	}
	router.establishLink = func(got *rns.Link) error {
		if got != link {
			t.Fatal("establishLink got unexpected link")
		}
		establishCount++
		return nil
	}
	router.identifyLink = func(*rns.Link, *rns.Identity) error { return nil }
	router.requestLink = func(*rns.Link, string, any, func(*rns.RequestReceipt), func(*rns.RequestReceipt), func(*rns.RequestReceipt), time.Duration) (*rns.RequestReceipt, error) {
		requestCount++
		return nil, nil
	}
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRLinkEstablishing {
		t.Fatalf("state = %v, want PRLinkEstablishing", router.PropagationTransferState())
	}
	if establishCount != 1 {
		t.Fatalf("establish count = %d, want 1", establishCount)
	}
	if requestCount != 0 {
		t.Fatalf("request count before callback = %d, want 0", requestCount)
	}
	if establishedCallback == nil {
		t.Fatal("expected established callback to be installed")
	}

	status = rns.LinkActive
	establishedCallback(link)

	if router.PropagationTransferState() != PRRequestSent {
		t.Fatalf("state after callback = %v, want PRRequestSent", router.PropagationTransferState())
	}
	if requestCount != 1 {
		t.Fatalf("request count after callback = %d, want 1", requestCount)
	}
}

func TestRequestMessagesLinkFailed(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	peerID := mustTestNewIdentity(t, true)
	peerDest := mustTestNewDestination(t, ts, peerID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	ts.Remember(nil, peerDest.Hash, peerID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(peerDest.Hash); err != nil {
		t.Fatal(err)
	}

	// Path available but link fails — should transition to PRLinkFailed.
	router.hasPath = func(_ []byte) bool { return true }
	router.newLink = func(rns.Transport, *rns.Destination) (*rns.Link, error) {
		return nil, errors.New("link failed")
	}
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRLinkFailed {
		t.Fatalf("state = %v, want PRLinkFailed", router.PropagationTransferState())
	}
}

func TestRequestMessagesEstablishFailed(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	peerID := mustTestNewIdentity(t, true)
	peerDest := mustTestNewDestination(t, ts, peerID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	ts.Remember(nil, peerDest.Hash, peerID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(peerDest.Hash); err != nil {
		t.Fatal(err)
	}

	router.hasPath = func(_ []byte) bool { return true }
	link := &rns.Link{}
	router.newLink = func(rns.Transport, *rns.Destination) (*rns.Link, error) { return link, nil }
	router.setLinkEstablishedCallback = func(*rns.Link, func(*rns.Link)) {}
	router.establishLink = func(*rns.Link) error { return errors.New("establish failed") }

	router.RequestMessagesFromPropagationNode(nil)

	if router.PropagationTransferState() != PRLinkFailed {
		t.Fatalf("state = %v, want PRLinkFailed", router.PropagationTransferState())
	}
	if router.outboundPropagationLink != nil {
		t.Fatal("expected outbound propagation link to be cleared after establish failure")
	}
}

func TestCancelPropagationResetsState(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = byte(i)
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	// Simulate an in-progress sync.
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRPathRequested {
		t.Fatalf("state = %v, want PRPathRequested", router.PropagationTransferState())
	}

	// Cancel should reset to idle.
	router.CancelPropagationNodeRequests()
	if router.PropagationTransferState() != PRIdle {
		t.Fatalf("state after cancel = %v, want PRIdle", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.0 {
		t.Fatalf("progress after cancel = %v, want 0.0", router.PropagationTransferProgress())
	}
}

func TestRequestMessagesUsesExistingPropagationLink(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	propNode := make([]byte, rns.TruncatedHashLength/8)
	for i := range propNode {
		propNode[i] = byte(i + 1)
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	router.outboundPropagationLink = &rns.Link{}
	router.linkStatus = func(*rns.Link) int { return rns.LinkActive }

	var identifiedWith *rns.Identity
	var requestedPath string
	var requestedData any
	router.identifyLink = func(_ *rns.Link, identity *rns.Identity) error {
		identifiedWith = identity
		return nil
	}
	router.requestLink = func(_ *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), _ time.Duration) (*rns.RequestReceipt, error) {
		if responseCallback == nil {
			t.Fatal("expected response callback")
		}
		if failedCallback == nil {
			t.Fatal("expected failed callback")
		}
		if progressCallback != nil {
			t.Fatal("did not expect progress callback for initial list request")
		}
		requestedPath = path
		requestedData = data
		return nil, nil
	}

	router.RequestMessagesFromPropagationNode(nil)

	if identifiedWith != router.identity {
		t.Fatal("existing propagation link was not identified with router identity")
	}
	if requestedPath != messageGetPath {
		t.Fatalf("request path = %q, want %q", requestedPath, messageGetPath)
	}
	fields, ok := requestedData.([]any)
	if !ok {
		t.Fatalf("request data type = %T, want []any", requestedData)
	}
	if len(fields) != 2 {
		t.Fatalf("request field count = %d, want 2", len(fields))
	}
	if fields[0] != nil || fields[1] != nil {
		t.Fatalf("request data = %#v, want [nil nil]", fields)
	}
	if router.PropagationTransferState() != PRRequestSent {
		t.Fatalf("state = %v, want PRRequestSent", router.PropagationTransferState())
	}
}

func TestPropagationSyncMessageListResponseRequestsWantedMessages(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	haveID := rns.FullHash([]byte("have"))
	want1 := rns.FullHash([]byte("want-1"))
	want2 := rns.FullHash([]byte("want-2"))
	router.locallyDeliveredIDs[string(haveID)] = time.Now()
	router.propagationTransferMaxMessages = 1

	var requestedPath string
	var requestedData any
	router.requestLink = func(_ *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), _ time.Duration) (*rns.RequestReceipt, error) {
		if responseCallback == nil {
			t.Fatal("expected response callback")
		}
		if failedCallback == nil {
			t.Fatal("expected failed callback")
		}
		if progressCallback == nil {
			t.Fatal("expected progress callback")
		}
		requestedPath = path
		requestedData = data
		return nil, nil
	}

	router.messageListResponse(&rns.RequestReceipt{
		Link:     &rns.Link{},
		Response: []any{haveID, want1, want2},
	})

	if requestedPath != messageGetPath {
		t.Fatalf("request path = %q, want %q", requestedPath, messageGetPath)
	}
	fields, ok := requestedData.([]any)
	if !ok {
		t.Fatalf("request data type = %T, want []any", requestedData)
	}
	if len(fields) != 3 {
		t.Fatalf("request field count = %d, want 3", len(fields))
	}
	wants, ok := fields[0].([][]byte)
	if !ok {
		t.Fatalf("wants type = %T, want [][]byte", fields[0])
	}
	haves, ok := fields[1].([][]byte)
	if !ok {
		t.Fatalf("haves type = %T, want [][]byte", fields[1])
	}
	if len(wants) != 1 || !bytes.Equal(wants[0], want1) {
		t.Fatalf("wants = %x, want [%x]", wants, want1)
	}
	if len(haves) != 1 || !bytes.Equal(haves[0], haveID) {
		t.Fatalf("haves = %x, want [%x]", haves, haveID)
	}
	limit, ok := fields[2].(float64)
	if !ok {
		t.Fatalf("limit type = %T, want float64", fields[2])
	}
	if got, want := limit, router.deliveryPerTransferLimit; got != want {
		t.Fatalf("limit = %v, want %v", got, want)
	}
}

func TestPropagationSyncMessageListResponseEmptyCompletes(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.messageListResponse(&rns.RequestReceipt{
		Link:     &rns.Link{},
		Response: []any{},
	})

	if router.PropagationTransferState() != PRComplete {
		t.Fatalf("state = %v, want PRComplete", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 1.0 {
		t.Fatalf("progress = %v, want 1.0", router.PropagationTransferProgress())
	}
	if router.PropagationTransferLastResult() != 0 {
		t.Fatalf("last result = %v, want 0", router.PropagationTransferLastResult())
	}
}

func TestPropagationSyncMessageGetProgressUpdatesState(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.requestProgress = func(_ *rns.RequestReceipt) float64 { return 0.625 }

	router.messageGetProgress(&rns.RequestReceipt{})

	if router.PropagationTransferState() != PRReceiving {
		t.Fatalf("state = %v, want PRReceiving", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.625 {
		t.Fatalf("progress = %v, want 0.625", router.PropagationTransferProgress())
	}
}

func TestPropagationSyncMessageGetResponseTracksDuplicatesAndPurges(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)
	var zero int
	localDest, err := router.RegisterDeliveryIdentity(destID, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	duplicateMsg := mustTestNewMessage(t, localDest, sourceDest, "duplicate", "title", nil)
	freshMsg := mustTestNewMessage(t, localDest, sourceDest, "fresh", "title", nil)
	if err := duplicateMsg.Pack(); err != nil {
		t.Fatalf("duplicateMsg.Pack: %v", err)
	}
	if err := freshMsg.Pack(); err != nil {
		t.Fatalf("freshMsg.Pack: %v", err)
	}
	duplicateTransientID := rns.FullHash(duplicateMsg.Packed)
	freshTransientID := rns.FullHash(freshMsg.Packed)
	router.locallyProcessedIDs[string(duplicateTransientID)] = time.Now()

	var delivered []*Message
	router.deliveryCallback = func(message *Message) {
		delivered = append(delivered, message)
	}

	var requestedPath string
	var requestedData any
	router.requestLink = func(_ *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), _ time.Duration) (*rns.RequestReceipt, error) {
		if failedCallback == nil {
			t.Fatal("expected failed callback")
		}
		if responseCallback != nil {
			t.Fatal("did not expect response callback for purge request")
		}
		if progressCallback != nil {
			t.Fatal("did not expect progress callback for purge request")
		}
		requestedPath = path
		requestedData = data
		return nil, nil
	}

	router.messageGetResponse(&rns.RequestReceipt{
		Link:     &rns.Link{},
		Response: []any{duplicateMsg.Packed, freshMsg.Packed},
	})

	if requestedPath != messageGetPath {
		t.Fatalf("request path = %q, want %q", requestedPath, messageGetPath)
	}
	fields, ok := requestedData.([]any)
	if !ok {
		t.Fatalf("request data type = %T, want []any", requestedData)
	}
	if len(fields) != 2 {
		t.Fatalf("request field count = %d, want 2", len(fields))
	}
	if fields[0] != nil {
		t.Fatalf("purge wants field = %#v, want nil", fields[0])
	}
	haves, ok := fields[1].([][]byte)
	if !ok {
		t.Fatalf("purge haves type = %T, want [][]byte", fields[1])
	}
	if len(haves) != 2 || !bytes.Equal(haves[0], duplicateTransientID) || !bytes.Equal(haves[1], freshTransientID) {
		t.Fatalf("purge haves = %x, want [%x %x]", haves, duplicateTransientID, freshTransientID)
	}
	if len(delivered) != 1 {
		t.Fatalf("delivered count = %d, want 1", len(delivered))
	}
	if !bytes.Equal(delivered[0].Packed, freshMsg.Packed) {
		t.Fatalf("delivered packed = %x, want %x", delivered[0].Packed, freshMsg.Packed)
	}
	if router.propagationTransferLastDuplicates != 1 {
		t.Fatalf("last duplicates = %v, want 1", router.propagationTransferLastDuplicates)
	}
	if router.PropagationTransferLastResult() != 2 {
		t.Fatalf("last result = %v, want 2", router.PropagationTransferLastResult())
	}
	if router.PropagationTransferState() != PRComplete {
		t.Fatalf("state = %v, want PRComplete", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 1.0 {
		t.Fatalf("progress = %v, want 1.0", router.PropagationTransferProgress())
	}
	if _, ok := router.locallyProcessedIDs[string(freshTransientID)]; !ok {
		t.Fatal("expected fresh transient ID in locally processed cache")
	}
	if _, ok := router.locallyDeliveredIDs[string(freshTransientID)]; !ok {
		t.Fatal("expected fresh transient ID in locally delivered cache")
	}
	if _, err := os.Stat(filepath.Join(router.storagePath, "local_deliveries")); err != nil {
		t.Fatalf("expected local_deliveries file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(router.storagePath, "locally_processed")); err != nil {
		t.Fatalf("expected locally_processed file: %v", err)
	}
}

func TestPropagationSyncClosedLinkAfterCompleteResetsToIdle(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.outboundPropagationLink = &rns.Link{}
	router.propagationTransferState = PRComplete
	router.propagationTransferProgress = 1.0
	router.propagationTransferLastResult = 3
	router.wantsDownloadOnPathAvailableFrom = []byte("pending")
	router.wantsDownloadOnPathAvailableTo = router.identity

	router.handleOutboundPropagationLinkClosed(router.outboundPropagationLink)

	if router.outboundPropagationLink != nil {
		t.Fatal("expected outbound propagation link to be cleared")
	}
	if router.PropagationTransferState() != PRIdle {
		t.Fatalf("state = %v, want PRIdle", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.0 {
		t.Fatalf("progress = %v, want 0.0", router.PropagationTransferProgress())
	}
	if router.wantsDownloadOnPathAvailableFrom != nil || router.wantsDownloadOnPathAvailableTo != nil {
		t.Fatal("expected pending path state to be cleared")
	}
}

func TestPropagationSyncClosedLinkBeforeEstablishBecomesLinkFailed(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.outboundPropagationLink = &rns.Link{}
	router.propagationTransferState = PRPathRequested
	router.propagationTransferProgress = 0.4

	router.handleOutboundPropagationLinkClosed(router.outboundPropagationLink)

	if router.PropagationTransferState() != PRLinkFailed {
		t.Fatalf("state = %v, want PRLinkFailed", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.0 {
		t.Fatalf("progress = %v, want 0.0", router.PropagationTransferProgress())
	}
}

func TestPropagationSyncClosedLinkMidTransferBecomesTransferFailed(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.outboundPropagationLink = &rns.Link{}
	router.propagationTransferState = PRReceiving
	router.propagationTransferProgress = 0.7

	router.handleOutboundPropagationLinkClosed(router.outboundPropagationLink)

	if router.PropagationTransferState() != PRTransferFailed {
		t.Fatalf("state = %v, want PRTransferFailed", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 0.0 {
		t.Fatalf("progress = %v, want 0.0", router.PropagationTransferProgress())
	}
}

func TestPropagationSyncMessageGetFailedTearsDownIntoTransferFailed(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	link := &rns.Link{}
	router.outboundPropagationLink = link
	router.propagationTransferState = PRRequestSent
	var teardownCount int
	router.teardownLink = func(closed *rns.Link) {
		if closed != link {
			t.Fatal("teardown called with unexpected link")
		}
		teardownCount++
		router.handleOutboundPropagationLinkClosed(closed)
	}

	router.messageGetFailed(&rns.RequestReceipt{})

	if teardownCount != 1 {
		t.Fatalf("teardown count = %d, want 1", teardownCount)
	}
	if router.PropagationTransferState() != PRTransferFailed {
		t.Fatalf("state = %v, want PRTransferFailed", router.PropagationTransferState())
	}
}

func TestProcessOutboundPropagatedNoNodeFails(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	// No outbound propagation node set — should fail immediately.
	failedCalled := false
	msg.FailedCallback = func(_ *Message) { failedCalled = true }

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error { return nil }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.State != StateFailed {
		t.Fatalf("state=%v want=%v", msg.State, StateFailed)
	}
	if !failedCalled {
		t.Fatal("expected FailedCallback to be invoked")
	}
}

func TestProcessOutboundPropagatedRequestsPathThenSends(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = 0xAA
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	hasPath := false
	requestCount := 0
	router.hasPath = func(_ []byte) bool { return hasPath }
	router.requestPath = func(_ []byte) error { requestCount++; return nil }
	sendCount := 0
	router.sendPacket = func(_ *rns.Packet) error { sendCount++; return nil }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	// First attempt: no path to propagation node — should request path.
	if requestCount != 1 {
		t.Fatalf("request path count=%v want=1", requestCount)
	}
	if msg.State != StateOutbound {
		t.Fatalf("state=%v want=%v", msg.State, StateOutbound)
	}

	// Advance past path request wait, path now available.
	now = now.Add(pathRequestWait + time.Second)
	hasPath = true
	router.ProcessOutbound()

	if sendCount != 1 {
		t.Fatalf("send count=%v want=1", sendCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundPropagatedSentRemovesFromQueue(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = 0xBB
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error { return nil }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}

	// Python removes propagated messages from queue when SENT.
	// Process again — message should be removed.
	router.ProcessOutbound()
	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pending outbound=%v want=0", len(router.pendingOutbound))
	}
}

func TestProcessOutboundTryPropagationOnFailFallback(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodDirect
	msg.TryPropagationOnFail = true

	propNode := make([]byte, 16)
	for i := range propNode {
		propNode[i] = 0xCC
	}
	if err := router.SetOutboundPropagationNode(propNode); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }

	// Fail every direct send attempt.
	router.sendPacket = func(_ *rns.Packet) error { return assertErr("direct send failed") }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	// Exhaust all direct delivery attempts.
	for i := 0; i < maxDeliveryAttempts+1; i++ {
		now = now.Add(deliveryRetryWait + time.Second)
		router.ProcessOutbound()
	}

	// Instead of failing, it should have switched to propagated.
	if msg.Method != MethodPropagated {
		t.Fatalf("method=%v want=%v (propagated)", msg.Method, MethodPropagated)
	}
	if msg.State == StateFailed {
		t.Fatal("expected message NOT to be in failed state after fallback to propagation")
	}

	// Now make propagation succeed.
	router.sendPacket = func(_ *rns.Packet) error { return nil }
	now = now.Add(deliveryRetryWait + time.Second)
	router.ProcessOutbound()

	if msg.State != StateSent {
		t.Fatalf("state after propagation=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundFailedCallbackInvoked(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)

	failedCalled := false
	msg.FailedCallback = func(_ *Message) { failedCalled = true }

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	router.sendPacket = func(_ *rns.Packet) error { return assertErr("send failed") }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	for i := 0; i < maxDeliveryAttempts+1; i++ {
		now = now.Add(deliveryRetryWait + time.Second)
		router.ProcessOutbound()
	}

	if msg.State != StateFailed {
		t.Fatalf("state=%v want=%v", msg.State, StateFailed)
	}
	if !failedCalled {
		t.Fatal("expected FailedCallback to be invoked when message fails")
	}
}

func TestSetDisplayNameAndAnnounceAppData(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	cost8 := 8
	dest, err := router.RegisterDeliveryIdentity(id, "", &cost8)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	router.SetDisplayName(dest.Hash, "Alice")

	appData := router.GetAnnounceAppData(dest.Hash)
	if appData == nil {
		t.Fatal("expected non-nil app data")
	}

	// Unpack and verify: Python packs [display_name_bytes, stamp_cost].
	unpacked, err := msgpack.Unpack(appData)
	if err != nil {
		t.Fatalf("unpack app data: %v", err)
	}
	peerData, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", unpacked)
	}
	if len(peerData) != 2 {
		t.Fatalf("peer data length=%v want=2", len(peerData))
	}

	nameBytes, ok := peerData[0].([]byte)
	if !ok {
		t.Fatalf("display name type=%T want=[]byte", peerData[0])
	}
	if string(nameBytes) != "Alice" {
		t.Fatalf("display name=%q want=%q", string(nameBytes), "Alice")
	}

	stampCostVal, ok := peerData[1].(uint64)
	if !ok {
		// msgpack might unpack small ints as different types
		if sc, ok2 := peerData[1].(int64); ok2 {
			stampCostVal = uint64(sc)
		} else {
			t.Fatalf("stamp cost type=%T want=uint64 or int64", peerData[1])
		}
	}
	if stampCostVal != 8 {
		t.Fatalf("stamp cost=%v want=8", stampCostVal)
	}
}

func TestSetDisplayNameNilReturnsNilAppData(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	dest, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	// No display name set — app data should be nil.
	appData := router.GetAnnounceAppData(dest.Hash)
	if appData != nil {
		t.Fatalf("expected nil app data when no display name set, got %v bytes", len(appData))
	}
}

func TestSetDisplayNameNoStampCost(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	dest, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	router.SetDisplayName(dest.Hash, "Bob")

	appData := router.GetAnnounceAppData(dest.Hash)
	if appData == nil {
		t.Fatal("expected non-nil app data")
	}

	unpacked, err := msgpack.Unpack(appData)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	peerData := unpacked.([]any)
	if len(peerData) != 2 {
		t.Fatalf("len=%v want=2", len(peerData))
	}

	if string(peerData[0].([]byte)) != "Bob" {
		t.Fatalf("name=%q want=%q", string(peerData[0].([]byte)), "Bob")
	}
	// stamp_cost should be nil when zero
	if peerData[1] != nil {
		t.Fatalf("stamp cost=%v want=nil", peerData[1])
	}
}

func TestAnnounceIncludesAppData(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	cost12 := 12
	dest, err := router.RegisterDeliveryIdentity(id, "", &cost12)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	router.SetDisplayName(dest.Hash, "TestNode")

	// Build an announce packet directly to capture the packet data
	// without requiring a full transport system.
	appData := router.GetAnnounceAppData(dest.Hash)
	if appData == nil {
		t.Fatal("expected non-nil app data")
	}

	// Build the announce packet via the destination's internal method.
	// We'll build it manually to mimic what Announce() does internally.
	packet, err := dest.BuildAnnouncePacket(appData)
	if err != nil {
		t.Fatalf("BuildAnnouncePacket: %v", err)
	}

	if packet.PacketType != rns.PacketAnnounce {
		t.Fatalf("packet type=%v want=%v", packet.PacketType, rns.PacketAnnounce)
	}

	// Verify the announce data contains the app_data at the tail.
	// Structure: public_key(64) + name_hash(10) + random_hash(10) + [ratchet(32)] + signature(64) + app_data
	data := packet.Data
	keySize := rns.IdentityKeySize / 8    // 64
	nameHashLen := rns.NameHashLength / 8 // 10
	sigLen := 64
	minLen := keySize + nameHashLen + 10 + sigLen
	if len(data) < minLen+len(appData) {
		t.Fatalf("packet data too short: %v < %v", len(data), minLen+len(appData))
	}

	// The app_data should be the last bytes of the packet data.
	tail := data[len(data)-len(appData):]
	if string(tail) != string(appData) {
		t.Fatalf("app_data tail mismatch: got %x want %x", tail, appData)
	}

	// Validate the announce signature using the standard validator.
	if err := packet.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !rns.ValidateAnnounce(ts, packet) {
		t.Fatal("ValidateAnnounce returned false for announce with app_data")
	}

	// Unpack the app_data and verify contents.
	unpacked, err := msgpack.Unpack(tail)
	if err != nil {
		t.Fatalf("unpack app data: %v", err)
	}
	peerData, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", unpacked)
	}
	if len(peerData) != 2 {
		t.Fatalf("peer data length=%v want=2", len(peerData))
	}
	nameBytes, ok := peerData[0].([]byte)
	if !ok {
		t.Fatalf("display name type=%T want=[]byte", peerData[0])
	}
	if string(nameBytes) != "TestNode" {
		t.Fatalf("display name=%q want=%q", string(nameBytes), "TestNode")
	}
}

func TestPropagationNodeAppDataMatchesPythonShape(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.now = func() time.Time { return time.Unix(1700000000, 0) }
	router.propagationEnabled = true
	router.fromStaticOnly = false
	router.propagationPerTransferLimit = 256
	router.propagationPerSyncLimit = 512
	router.propagationCost = 11
	router.propagationCostFlexibility = 3
	router.peeringCost = 7
	router.name = "Node A"

	appData := router.getPropagationNodeAppDataLocked()
	unpacked, err := msgpack.Unpack(appData)
	if err != nil {
		t.Fatalf("Unpack propagation app data: %v", err)
	}
	announceData, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("unpacked type=%T want []any", unpacked)
	}
	if len(announceData) != 7 {
		t.Fatalf("len(announceData)=%v want=7", len(announceData))
	}
	if legacy, ok := announceData[0].(bool); !ok || legacy {
		t.Fatalf("legacy support flag=%v want=false", announceData[0])
	}
	if timebase, ok := announceData[1].(int64); !ok || timebase != 1700000000 {
		t.Fatalf("timebase=%v want=1700000000", announceData[1])
	}
	if nodeState, ok := announceData[2].(bool); !ok || !nodeState {
		t.Fatalf("node state=%v want=true", announceData[2])
	}
	if transferLimit, ok := announceData[3].(float64); !ok || transferLimit != 256 {
		t.Fatalf("transfer limit=%v want=256", announceData[3])
	}
	if syncLimit, ok := announceData[4].(float64); !ok || syncLimit != 512 {
		t.Fatalf("sync limit=%v want=512", announceData[4])
	}

	stampCost, ok := announceData[5].([]any)
	if !ok || len(stampCost) != 3 {
		t.Fatalf("stamp cost=%T/%v want []any len 3", announceData[5], announceData[5])
	}
	if stampCost[0] != int64(11) || stampCost[1] != int64(3) || stampCost[2] != int64(7) {
		t.Fatalf("stamp cost=%v want [11 3 7]", stampCost)
	}

	metadata, ok := announceData[6].(map[any]any)
	if !ok {
		t.Fatalf("metadata type=%T want map[any]any", announceData[6])
	}
	nameValue, ok := metadata[int64(PNMetaName)].([]byte)
	if !ok {
		t.Fatalf("metadata[%v]=%T/%v want []byte", PNMetaName, metadata[int64(PNMetaName)], metadata[int64(PNMetaName)])
	}
	if string(nameValue) != "Node A" {
		t.Fatalf("metadata name=%q want=%q", string(nameValue), "Node A")
	}
}

func TestAnnounceWithoutDisplayNamePassesNilAppData(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	dest, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	appData := router.GetAnnounceAppData(dest.Hash)
	if appData != nil {
		t.Fatalf("expected nil app data, got %v bytes", len(appData))
	}

	// Build packet without app_data.
	packet, err := dest.BuildAnnouncePacket(nil)
	if err != nil {
		t.Fatalf("BuildAnnouncePacket: %v", err)
	}

	if err := packet.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if !rns.ValidateAnnounce(ts, packet) {
		t.Fatal("ValidateAnnounce returned false for announce without app_data")
	}
}

func TestRouterPropagationToggle(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if router.PropagationEnabled() {
		t.Fatal("propagation should not be enabled initially")
	}

	router.EnablePropagation()

	if !router.PropagationEnabled() {
		t.Fatal("propagation should be enabled after EnablePropagation()")
	}

	router.DisablePropagation()

	if router.PropagationEnabled() {
		t.Fatal("propagation should be disabled after DisablePropagation()")
	}
}
