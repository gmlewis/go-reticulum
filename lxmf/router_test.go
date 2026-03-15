// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func TestNewRouterRequiresStoragePath(t *testing.T) {
	_, err := NewRouter(nil, "")
	if err == nil {
		t.Fatal("expected error when storage path is empty")
	}
}

func TestRegisterDeliveryIdentitySingleDestinationOnly(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity #1: %v", err)
	}
	zero := 0
	if _, err := router.RegisterDeliveryIdentity(id, "", &zero); err != nil {
		t.Fatalf("RegisterDeliveryIdentity #1: %v", err)
	}

	id2, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity #2: %v", err)
	}
	if _, err := router.RegisterDeliveryIdentity(id2, "", &zero); err == nil {
		t.Fatal("expected second RegisterDeliveryIdentity call to fail")
	}
}

func TestHandleOutboundValidatesMessage(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if err := router.HandleOutbound(nil); err == nil {
		t.Fatal("expected nil message error")
	}
}

func TestProcessOutboundDirectRequestsPathWhenUnavailable(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

	now := time.Unix(1700000000, 0)
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.DesiredMethod = MethodOpportunistic

	now := time.Unix(1700000000, 0)
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	msg.DesiredMethod = MethodOpportunistic

	now := time.Unix(1700000000, 0)
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
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
}

func TestProcessOutboundSentMessageNotResentUntilTimeout(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "short content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'A'
	}

	msg, err := NewMessage(destination, sourceDest, string(content), "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'B'
	}

	msg, err := NewMessage(destination, sourceDest, string(content), "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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

type fakeLinkBuilder struct {
	establishedCB func(*rns.Link)
	closedCB      func(*rns.Link)
	establishErr  error
	establishes   int
}

func (f *fakeLinkBuilder) Establish() error {
	f.establishes++
	if f.establishErr != nil {
		return f.establishErr
	}
	if f.establishedCB != nil {
		f.establishedCB(&rns.Link{})
	}
	return nil
}

func (f *fakeLinkBuilder) SetLinkEstablishedCallback(cb func(*rns.Link)) {
	f.establishedCB = cb
}

func (f *fakeLinkBuilder) SetLinkClosedCallback(cb func(*rns.Link)) {
	f.closedCB = cb
}

func TestProcessOutboundResourceLinkPendingRetryNoAttemptIncrement(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'C'
	}
	msg, err := NewMessage(destination, sourceDest, string(content), "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'D'
	}
	msg, err := NewMessage(destination, sourceDest, string(content), "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	wantErr := errors.New("establish failed")
	router.newLink = func(*rns.Destination) (*rns.Link, error) {
		return nil, wantErr
	}

	err = router.sendMessageResourceLocked(msg)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error=%v want=%v", err, wantErr)
	}
}

func TestProcessOutboundResourceSendFailureEventuallyFails(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'E'
	}
	msg, err := NewMessage(destination, sourceDest, string(content), "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	content := make([]byte, rns.MDU*2)
	for i := range content {
		content[i] = 'F'
	}
	msg, err := NewMessage(destination, sourceDest, string(content), "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "resource-content", "resource-title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	router.clientPropagationMessagesReceived = 11
	router.clientPropagationMessagesServed = 7
	router.unpeeredPropagationIncoming = 3
	router.unpeeredPropagationRXBytes = 512
	router.peers["peer-1"] = time.Now()
	router.peers["peer-2"] = time.Now()

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if got := router.statsGetRequest("", nil, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("stats no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	allowedIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(allowed): %v", err)
	}
	router.controlAllowed[string(allowedIdentity.Hash)] = struct{}{}

	notAllowedIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(not allowed): %v", err)
	}

	if got := router.statsGetRequest("", nil, nil, nil, notAllowedIdentity, time.Now()); got != peerErrorNoAccess {
		t.Fatalf("stats no access=%v want=%v", got, peerErrorNoAccess)
	}
}

func TestControlPeerSyncAndUnpeerRequests(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	peerIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(peer): %v", err)
	}
	peerHash := append([]byte{}, peerIdentity.Hash...)
	router.peers[string(peerHash)] = time.Now().Add(-time.Hour)

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	if err := router.SetPeerSyncBackoff(10 * time.Second); err != nil {
		t.Fatalf("SetPeerSyncBackoff: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	peerIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(peer): %v", err)
	}
	peerHash := append([]byte{}, peerIdentity.Hash...)
	router.peers[string(peerHash)] = now

	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, now); got != peerErrorThrottled {
		t.Fatalf("sync throttled=%v want=%v", got, peerErrorThrottled)
	}

	now = now.Add(11 * time.Second)
	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, now); got != true {
		t.Fatalf("sync after backoff=%v want=true", got)
	}
}

func TestPruneStalePeers(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	if err := router.SetPeerMaxAge(30 * time.Second); err != nil {
		t.Fatalf("SetPeerMaxAge: %v", err)
	}

	peerOld := []byte("peer-old-01234567")
	peerNew := []byte("peer-new-01234567")
	router.peers[string(peerOld)] = now.Add(-2 * time.Minute)
	router.peers[string(peerNew)] = now.Add(-10 * time.Second)

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if got := router.peerSyncRequest("", nil, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("sync no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	allowedIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(allowed): %v", err)
	}
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

	peerIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(peer): %v", err)
	}
	peerHash := append([]byte{}, peerIdentity.Hash...)

	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != peerErrorNotFound {
		t.Fatalf("sync not found=%v want=%v", got, peerErrorNotFound)
	}

	if got := router.peerUnpeerRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != peerErrorNotFound {
		t.Fatalf("unpeer not found=%v want=%v", got, peerErrorNotFound)
	}
}

func TestRegisterPropagationDestination(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	destinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	haveID := router.storePropagationMessage(destinationHash, []byte("msg-have"))
	wantID := rns.FullHash([]byte("missing"))

	requestData, err := msgpack.Pack([]any{[]byte("key"), []any{haveID, wantID}})
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	router.peeringCost = 2

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
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

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
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

	nonStaticIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(non-static): %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")

	router, err := NewRouterWithConfig(nil, t.TempDir(), map[string]any{
		"peering_cost":     2,
		"from_static_only": true,
		"static_peers":     []any{hex.EncodeToString(remotePropagationHash)},
		"auth_required":    true,
		"allowed_list":     []any{hex.EncodeToString(remoteIdentity.Hash)},
	})
	if err != nil {
		t.Fatalf("NewRouterWithConfig: %v", err)
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
}

func TestNewRouterWithConfigReturnsPolicyError(t *testing.T) {
	_, err := NewRouterWithConfig(nil, t.TempDir(), map[string]any{"peering_cost": "bad"})
	if err == nil {
		t.Fatal("expected policy config error")
	}
}

func TestMessageGetRequestListAndFetch(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	otherIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(other): %v", err)
	}
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

func TestMessageGetRequestRequiresIdentity(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	router.authRequired = true

	request, err := msgpack.Pack([]any{nil, nil})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	notAllowedIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(not allowed): %v", err)
	}
	if response := router.messageGetRequest("", request, nil, nil, notAllowedIdentity, time.Now()); response != peerErrorNoAccess {
		t.Fatalf("message_get no access=%v want=%v", response, peerErrorNoAccess)
	}

	allowedIdentity, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(allowed): %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}

	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	message, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	var mu sync.Mutex
	received := make([]*Message, 0, 2)
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
	if len(received) != 2 {
		t.Fatalf("received count=%v want=2", len(received))
	}
	if received[0].ContentString() != "content" || received[1].ContentString() != "content" {
		t.Fatalf("unexpected content values: %q and %q", received[0].ContentString(), received[1].ContentString())
	}
	if received[0].Method != MethodOpportunistic {
		t.Fatalf("first method=%v want=%v", received[0].Method, MethodOpportunistic)
	}
	if received[1].Method != MethodDirect {
		t.Fatalf("second method=%v want=%v", received[1].Method, MethodDirect)
	}
}

func TestNewRouterFromConfig(t *testing.T) {
	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}

	maxPeers := 5
	staticPeer := rns.CalculateHash(id, AppName, "propagation")

	router, err := NewRouterFromConfig(RouterConfig{
		Identity:         id,
		StoragePath:      t.TempDir(),
		Autopeer:         true,
		PropagationLimit: 128,
		SyncLimit:        512,
		DeliveryLimit:    200,
		MaxPeers:         &maxPeers,
		StaticPeers:      [][]byte{staticPeer},
		PropagationCost:  20,
	})
	if err != nil {
		t.Fatalf("NewRouterFromConfig: %v", err)
	}

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
	router, err := NewRouterFromConfig(RouterConfig{
		StoragePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewRouterFromConfig: %v", err)
	}

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
	router, err := NewRouterFromConfig(RouterConfig{
		StoragePath:      t.TempDir(),
		PropagationLimit: 500,
		SyncLimit:        100, // less than PropagationLimit
	})
	if err != nil {
		t.Fatalf("NewRouterFromConfig: %v", err)
	}

	// sync limit should be clamped to propagation limit
	if got := router.PropagationPerSyncLimit(); got != 500 {
		t.Fatalf("PropagationPerSyncLimit=%v want=500 (clamped to propagation limit)", got)
	}
}

func TestRouterIgnoreDestination(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if router.StampsEnforced() {
		t.Fatal("stamps should not be enforced initially")
	}

	router.EnforceStamps()

	if !router.StampsEnforced() {
		t.Fatal("stamps should be enforced after EnforceStamps()")
	}
}

func TestRouterMessageStorageLimit(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	if got := router.MessageStorageLimit(); got != 0 {
		t.Fatalf("initial MessageStorageLimit=%v want=0", got)
	}

	router.SetMessageStorageLimit(2000)

	if got := router.MessageStorageLimit(); got != 2000 {
		t.Fatalf("MessageStorageLimit=%v want=2000", got)
	}
}

func TestRouterPrioritise(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	zero := 0
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

func TestRouterAnnounce(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	zero := 0
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	// No propagation node set — should log and return without panicking.
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRIdle {
		t.Fatalf("state = %v, want PRIdle", router.PropagationTransferState())
	}
}

func TestRequestMessagesPathRequested(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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

func TestRequestMessagesLinkEstablished(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	// Create and remember a peer identity.
	peerID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}
	peerDest, err := rns.NewDestination(peerID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	if err != nil {
		t.Fatal(err)
	}
	rns.Remember(nil, peerDest.Hash, peerID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(peerDest.Hash); err != nil {
		t.Fatal(err)
	}

	// Path available and link succeeds — should transition to PRRequestSent.
	router.hasPath = func(_ []byte) bool { return true }
	router.newLink = func(_ *rns.Destination) (*rns.Link, error) {
		return &rns.Link{}, nil
	}
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRRequestSent {
		t.Fatalf("state = %v, want PRRequestSent", router.PropagationTransferState())
	}
}

func TestRequestMessagesLinkFailed(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	peerID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}
	peerDest, err := rns.NewDestination(peerID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	if err != nil {
		t.Fatal(err)
	}
	rns.Remember(nil, peerDest.Hash, peerID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(peerDest.Hash); err != nil {
		t.Fatal(err)
	}

	// Path available but link fails — should transition to PRLinkFailed.
	router.hasPath = func(_ []byte) bool { return true }
	router.newLink = func(_ *rns.Destination) (*rns.Link, error) {
		return nil, errors.New("link failed")
	}
	router.RequestMessagesFromPropagationNode(nil)
	if router.PropagationTransferState() != PRLinkFailed {
		t.Fatalf("state = %v, want PRLinkFailed", router.PropagationTransferState())
	}
}

func TestCancelPropagationResetsState(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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

func TestProcessOutboundPropagatedNoNodeFails(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	sourceID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}
	destID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity(dest): %v", err)
	}
	sourceDest, err := rns.NewDestination(sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	destination, err := rns.NewDestination(destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(dest): %v", err)
	}

	msg, err := NewMessage(destination, sourceDest, "content", "title", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}

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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	zero := 0
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	zero := 0
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
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
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
	if !rns.ValidateAnnounce(packet) {
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

func TestAnnounceWithoutDisplayNamePassesNilAppData(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	id, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	zero := 0
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
	if !rns.ValidateAnnounce(packet) {
		t.Fatal("ValidateAnnounce returned false for announce without app_data")
	}
}

func TestRouterPropagationToggle(t *testing.T) {
	router, err := NewRouter(nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

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
