// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/gmlewis/go-reticulum/rns"
	rnscrypto "github.com/gmlewis/go-reticulum/rns/crypto"
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

func TestHandleOutboundAutoconfiguresZeroOutboundStampCostWithZeroValueStamp(t *testing.T) {
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
	router.updateStampCost(destination.Hash, 0)

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DeferStamp = false

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if msg.StampCost == nil || *msg.StampCost != 0 {
		t.Fatalf("stamp cost=%v want=0", msg.StampCost)
	}
	if len(msg.Stamp) != StampSize {
		t.Fatalf("stamp length=%v want=%v", len(msg.Stamp), StampSize)
	}
	workblock, err := StampWorkblock(msg.MessageID, WorkblockExpandRounds)
	if err != nil {
		t.Fatalf("StampWorkblock: %v", err)
	}
	if !StampValid(msg.Stamp, *msg.StampCost, workblock) {
		t.Fatal("generated stamp should satisfy announced outbound zero cost")
	}
	wantValue := StampValue(workblock, msg.Stamp)
	if msg.StampValue == nil || *msg.StampValue != wantValue {
		t.Fatalf("stamp value=%v want=%v", msg.StampValue, wantValue)
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

func TestDeferredStamps(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	now := time.Unix(1700000000, 0).UTC()
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }

	stampCost := 1
	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.StampCost = &stampCost

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if got := len(router.pendingOutbound); got != 0 {
		t.Fatalf("pendingOutbound length=%v want=0", got)
	}
	if got := len(router.pendingDeferredStamps); got != 1 {
		t.Fatalf("pendingDeferredStamps length=%v want=1", got)
	}
	if msg.State != StateOutbound {
		t.Fatalf("state=%v want=%v", msg.State, StateOutbound)
	}
	if len(msg.Stamp) != 0 {
		t.Fatalf("stamp=%x want empty before deferred processing", msg.Stamp)
	}

	progress := router.GetOutboundProgress(msg.Hash)
	if progress == nil || *progress != 0 {
		t.Fatalf("GetOutboundProgress() = %v, want 0", progress)
	}
	queuedStampCost := router.GetOutboundLXMStampCost(msg.Hash)
	if queuedStampCost == nil || *queuedStampCost != stampCost {
		t.Fatalf("GetOutboundLXMStampCost() = %v, want %v", queuedStampCost, stampCost)
	}

	router.ProcessDeferredStamps()

	if got := len(router.pendingDeferredStamps); got != 0 {
		t.Fatalf("pendingDeferredStamps length after processing=%v want=0", got)
	}
	if got := len(router.pendingOutbound); got != 1 {
		t.Fatalf("pendingOutbound length after processing=%v want=1", got)
	}
	if len(msg.Stamp) != StampSize {
		t.Fatalf("stamp length=%v want=%v", len(msg.Stamp), StampSize)
	}
	workblock, err := StampWorkblock(msg.MessageID, WorkblockExpandRounds)
	if err != nil {
		t.Fatalf("StampWorkblock: %v", err)
	}
	if !StampValid(msg.Stamp, stampCost, workblock) {
		t.Fatal("generated deferred stamp should satisfy outbound stamp cost")
	}
}

func TestDeferredPropagationStamps(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	now := time.Unix(1700000000, 0).UTC()
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }
	router.pathWaitSleep = func(time.Duration) {}

	router.outboundPropagationNode = rns.CalculateHash(destID, AppName, "propagation")
	appData, err := msgpack.Pack([]any{
		false,
		float64(now.Unix()),
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node A")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}
	ts.Remember(nil, router.outboundPropagationNode, destID.GetPublicKey(), appData)

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if got := len(router.pendingOutbound); got != 0 {
		t.Fatalf("pendingOutbound length=%v want=0", got)
	}
	if got := len(router.pendingDeferredStamps); got != 1 {
		t.Fatalf("pendingDeferredStamps length=%v want=1", got)
	}
	if msg.DeferStamp {
		t.Fatal("expected normal deferred stamping to be cleared when no direct stamp is required")
	}
	if !msg.DeferPropagationStamp {
		t.Fatal("expected propagation stamp generation to remain deferred")
	}
	if got := router.GetOutboundLXMPropagationStampCost(msg.Hash); got != nil {
		t.Fatalf("GetOutboundLXMPropagationStampCost() before generation=%v want=nil", *got)
	}

	router.ProcessDeferredStamps()

	if got := len(router.pendingDeferredStamps); got != 0 {
		t.Fatalf("pendingDeferredStamps length after processing=%v want=0", got)
	}
	if got := len(router.pendingOutbound); got != 1 {
		t.Fatalf("pendingOutbound length after processing=%v want=1", got)
	}
	if msg.PropagationTargetCost == nil || *msg.PropagationTargetCost != 11 {
		t.Fatalf("propagation target cost=%v want=11", msg.PropagationTargetCost)
	}
	if len(msg.PropagationStamp) != StampSize {
		t.Fatalf("propagation stamp length=%v want=%v", len(msg.PropagationStamp), StampSize)
	}
	workblock, err := StampWorkblock(msg.TransientID, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("StampWorkblock: %v", err)
	}
	if !StampValid(msg.PropagationStamp, *msg.PropagationTargetCost, workblock) {
		t.Fatal("generated propagation stamp should satisfy propagation target cost")
	}
	unpackedAny, err := msgpack.Unpack(msg.PropagationPacked)
	if err != nil {
		t.Fatalf("Unpack propagation payload: %v", err)
	}
	unpacked, ok := unpackedAny.([]any)
	if !ok || len(unpacked) != 2 {
		t.Fatalf("propagation payload=%#v want [timestamp, [lxmf_data]]", unpackedAny)
	}
	entries, ok := unpacked[1].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("propagation entries=%#v want single entry", unpacked[1])
	}
	lxmfData, ok := entries[0].([]byte)
	if !ok {
		t.Fatalf("propagation entry type=%T want []byte", entries[0])
	}
	if !bytes.HasSuffix(lxmfData, msg.PropagationStamp) {
		t.Fatalf("propagation payload=%x want suffix %x", lxmfData, msg.PropagationStamp)
	}
}

func TestDeferredOutboundProgress(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	stampCost := 5
	propagationCost := 11

	pendingOutbound := mustTestNewMessage(t, destination, sourceDest, "queued", "title", nil)
	pendingOutbound.Progress = 0.25
	pendingOutbound.StampCost = &stampCost
	if err := pendingOutbound.Pack(); err != nil {
		t.Fatalf("Pack pendingOutbound: %v", err)
	}

	deferred := mustTestNewMessage(t, destination, sourceDest, "deferred", "title", nil)
	deferred.Progress = 0.75
	deferred.StampCost = &stampCost
	deferred.PropagationTargetCost = &propagationCost
	if err := deferred.Pack(); err != nil {
		t.Fatalf("Pack deferred: %v", err)
	}

	router.pendingOutbound = append(router.pendingOutbound, pendingOutbound)
	router.pendingDeferredStamps[string(deferred.MessageID)] = deferred

	if got := router.GetOutboundProgress(pendingOutbound.Hash); got == nil || *got != pendingOutbound.Progress {
		t.Fatalf("GetOutboundProgress(pendingOutbound)=%v want=%v", got, pendingOutbound.Progress)
	}
	if got := router.GetOutboundProgress(deferred.Hash); got == nil || *got != deferred.Progress {
		t.Fatalf("GetOutboundProgress(deferred)=%v want=%v", got, deferred.Progress)
	}
	if got := router.GetOutboundLXMStampCost(pendingOutbound.Hash); got == nil || *got != stampCost {
		t.Fatalf("GetOutboundLXMStampCost(pendingOutbound)=%v want=%v", got, stampCost)
	}
	if got := router.GetOutboundLXMStampCost(deferred.Hash); got == nil || *got != stampCost {
		t.Fatalf("GetOutboundLXMStampCost(deferred)=%v want=%v", got, stampCost)
	}
	if got := router.GetOutboundLXMPropagationStampCost(deferred.Hash); got == nil || *got != propagationCost {
		t.Fatalf("GetOutboundLXMPropagationStampCost(deferred)=%v want=%v", got, propagationCost)
	}

	cancelled := false
	deferred.FailedCallback = func(*Message) { cancelled = true }
	router.CancelOutbound(deferred.MessageID, StateCancelled)
	if deferred.State != StateCancelled {
		t.Fatalf("deferred state after cancel=%v want=%v", deferred.State, StateCancelled)
	}

	router.ProcessDeferredStamps()

	if got := len(router.pendingDeferredStamps); got != 0 {
		t.Fatalf("pendingDeferredStamps length after cancel=%v want=0", got)
	}
	if !cancelled {
		t.Fatal("expected cancelled deferred message to invoke failed callback")
	}
	if !deferred.StampGenerationFailed {
		t.Fatal("expected cancelled deferred message to mark stamp generation as failed")
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

	callbackField := reflect.ValueOf(destination).Elem().FieldByName("callbacks")
	destinationCallbacks := reflect.NewAt(callbackField.Type(), unsafe.Pointer(callbackField.UnsafeAddr())).Elem()
	if destinationCallbacks.FieldByName("Packet").IsNil() {
		t.Fatal("expected propagation destination packet callback to be installed")
	}
	linkEstablished := destinationCallbacks.FieldByName("LinkEstablished")
	if linkEstablished.IsNil() {
		t.Fatal("expected propagation destination link-established callback to be installed")
	}

	link := &rns.Link{}
	linkEstablished.Interface().(func(*rns.Link))(link)

	linkCallbacksField := reflect.ValueOf(link).Elem().FieldByName("callbacks")
	linkCallbacks := reflect.NewAt(linkCallbacksField.Type(), unsafe.Pointer(linkCallbacksField.UnsafeAddr())).Elem()
	if linkCallbacks.FieldByName("Packet").IsNil() {
		t.Fatal("expected propagation link packet callback to be installed")
	}
	if linkCallbacks.FieldByName("Resource").IsNil() {
		t.Fatal("expected propagation link resource callback to be installed")
	}
	if linkCallbacks.FieldByName("ResourceStarted").IsNil() {
		t.Fatal("expected propagation link resource-started callback to be installed")
	}
	if linkCallbacks.FieldByName("ResourceConcluded").IsNil() {
		t.Fatal("expected propagation link resource-concluded callback to be installed")
	}

	resourceStrategyField := reflect.ValueOf(link).Elem().FieldByName("resourceStrategy")
	resourceStrategy := reflect.NewAt(resourceStrategyField.Type(), unsafe.Pointer(resourceStrategyField.UnsafeAddr())).Elem().Int()
	if int(resourceStrategy) != rns.AcceptApp {
		t.Fatalf("propagation link resourceStrategy=%v want=%v", resourceStrategy, rns.AcceptApp)
	}
}

func TestPropagationLinkResourceAdvertisedRejectsOversize(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationPerSyncLimit = 1

	destination, err := router.RegisterPropagationDestination()
	if err != nil {
		t.Fatalf("RegisterPropagationDestination: %v", err)
	}

	callbackField := reflect.ValueOf(destination).Elem().FieldByName("callbacks")
	linkEstablished := reflect.NewAt(callbackField.Type(), unsafe.Pointer(callbackField.UnsafeAddr())).Elem().FieldByName("LinkEstablished")
	link := &rns.Link{}
	linkEstablished.Interface().(func(*rns.Link))(link)

	linkCallbacksField := reflect.ValueOf(link).Elem().FieldByName("callbacks")
	resourceCallback := reflect.NewAt(linkCallbacksField.Type(), unsafe.Pointer(linkCallbacksField.UnsafeAddr())).Elem().FieldByName("Resource")
	if resourceCallback.IsNil() {
		t.Fatal("expected propagation link resource callback to be installed")
	}

	accepted := resourceCallback.Interface().(func(*rns.ResourceAdvertisement) bool)(&rns.ResourceAdvertisement{D: 1001})
	if accepted {
		t.Fatal("expected oversize propagation resource to be rejected")
	}
}

func TestPropagationLinkResourceAdvertisedRespectsStaticOnly(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.SetFromStaticOnly(true)
	router.propagationPerSyncLimit = 10

	destination, err := router.RegisterPropagationDestination()
	if err != nil {
		t.Fatalf("RegisterPropagationDestination: %v", err)
	}

	callbackField := reflect.ValueOf(destination).Elem().FieldByName("callbacks")
	linkEstablished := reflect.NewAt(callbackField.Type(), unsafe.Pointer(callbackField.UnsafeAddr())).Elem().FieldByName("LinkEstablished")
	link := &rns.Link{}
	linkEstablished.Interface().(func(*rns.Link))(link)

	linkCallbacksField := reflect.ValueOf(link).Elem().FieldByName("callbacks")
	resourceCallback := reflect.NewAt(linkCallbacksField.Type(), unsafe.Pointer(linkCallbacksField.UnsafeAddr())).Elem().FieldByName("Resource")
	if resourceCallback.IsNil() {
		t.Fatal("expected propagation link resource callback to be installed")
	}
	callback := resourceCallback.Interface().(func(*rns.ResourceAdvertisement) bool)

	if callback(&rns.ResourceAdvertisement{D: 500}) {
		t.Fatal("expected unidentified propagation resource to be rejected in static-only mode")
	}

	remoteIdentity := mustTestNewIdentity(t, true)
	setRouterLinkField(t, link, "remoteIdentity", remoteIdentity)
	if callback(&rns.ResourceAdvertisement{D: 500}) {
		t.Fatal("expected non-static propagation peer resource to be rejected")
	}

	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.staticPeers[string(remoteHash)] = struct{}{}
	if !callback(&rns.ResourceAdvertisement{D: 500}) {
		t.Fatal("expected static propagation peer resource to be accepted")
	}
}

type propagationPacketCaptureTransport struct {
	*rns.TransportSystem

	mu      sync.Mutex
	packets []*rns.Packet
}

func newPropagationPacketCaptureTransport() *propagationPacketCaptureTransport {
	return &propagationPacketCaptureTransport{
		TransportSystem: rns.NewTransportSystem(nil),
	}
}

func (ts *propagationPacketCaptureTransport) Outbound(packet *rns.Packet) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.packets = append(ts.packets, packet)
	return nil
}

func (ts *propagationPacketCaptureTransport) snapshots() []*rns.Packet {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	packets := make([]*rns.Packet, len(ts.packets))
	copy(packets, ts.packets)
	return packets
}

func TestPropagationPacketStoresValidClientMessageAndProves(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	message := mustTestNewMessage(t, remoteDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}

	propagationStamp, stampValue, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("GenerateStamp(): %v", err)
	}
	transientData := append(append([]byte{}, message.Packed...), propagationStamp...)
	packedData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack propagation packet: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)

	packet := &rns.Packet{
		Destination:     link,
		DestinationType: rns.DestinationLink,
		FromPacked:      true,
		PacketHash:      bytes.Repeat([]byte{0x11}, 32),
	}

	router.propagationPacket(packedData, packet)

	transientID := rns.FullHash(message.Packed)
	entry := router.propagationEntries[string(transientID)]
	if entry == nil {
		t.Fatalf("expected propagation entry for %x", transientID)
	}
	if !bytes.Equal(entry.payload, message.Packed) {
		t.Fatalf("stored payload=%x want=%x", entry.payload, message.Packed)
	}
	if got, want := entry.stampValue, stampValue; got != want {
		t.Fatalf("stampValue=%v want=%v", got, want)
	}
	if got, want := router.clientPropagationMessagesReceived, 1; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}
	if _, ok := router.locallyProcessedIDs[string(transientID)]; !ok {
		t.Fatalf("expected locally processed entry for %x", transientID)
	}

	packets := ts.snapshots()
	if len(packets) != 1 {
		t.Fatalf("sent packets=%v want=1", len(packets))
	}
	if got, want := packets[0].PacketType, rns.PacketProof; got != want {
		t.Fatalf("proof packet type=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkActive {
		t.Fatalf("link status=%v want=%v", got, rns.LinkActive)
	}
}

func TestPropagationPacketRejectsInvalidClientTransferAfterAcceptingValidEntries(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	validMessage := mustTestNewMessage(t, remoteDest, sourceDest, "valid", "title", nil)
	if err := validMessage.Pack(); err != nil {
		t.Fatalf("valid Pack(): %v", err)
	}
	validStamp, _, _, err := GenerateStamp(rns.FullHash(validMessage.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("GenerateStamp(valid): %v", err)
	}

	invalidMessage := mustTestNewMessage(t, remoteDest, sourceDest, "invalid", "title", nil)
	if err := invalidMessage.Pack(); err != nil {
		t.Fatalf("invalid Pack(): %v", err)
	}
	workblock, err := StampWorkblock(rns.FullHash(invalidMessage.Packed), WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("StampWorkblock(invalid): %v", err)
	}
	var invalidStamp []byte
	for candidate := 0; candidate < 256; candidate++ {
		stamp := bytes.Repeat([]byte{byte(candidate)}, StampSize)
		if !StampValid(stamp, router.propagationCost, workblock) {
			invalidStamp = stamp
			break
		}
	}
	if invalidStamp == nil {
		t.Fatal("expected to find an invalid propagation stamp candidate")
	}

	validTransientData := append(append([]byte{}, validMessage.Packed...), validStamp...)
	invalidTransientData := append(append([]byte{}, invalidMessage.Packed...), invalidStamp...)
	packedData, err := msgpack.Pack([]any{float64(1), []any{validTransientData, invalidTransientData}})
	if err != nil {
		t.Fatalf("Pack propagation packet: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)

	packet := &rns.Packet{
		Destination:     link,
		DestinationType: rns.DestinationLink,
		FromPacked:      true,
		PacketHash:      bytes.Repeat([]byte{0x22}, 32),
	}

	router.propagationPacket(packedData, packet)

	validTransientID := rns.FullHash(validMessage.Packed)
	if _, ok := router.propagationEntries[string(validTransientID)]; !ok {
		t.Fatalf("expected valid propagation entry for %x", validTransientID)
	}
	invalidTransientID := rns.FullHash(invalidMessage.Packed)
	if _, ok := router.propagationEntries[string(invalidTransientID)]; ok {
		t.Fatalf("did not expect invalid propagation entry for %x", invalidTransientID)
	}
	if got, want := router.clientPropagationMessagesReceived, 1; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}

	packets := ts.snapshots()
	if len(packets) < 1 {
		t.Fatalf("sent packets=%v want at least 1", len(packets))
	}
	rejectData, err := msgpack.Pack([]any{peerErrorInvalidStamp})
	if err != nil {
		t.Fatalf("Pack reject data: %v", err)
	}
	if !bytes.Equal(packets[0].Data, rejectData) {
		t.Fatalf("reject packet data=%x want=%x", packets[0].Data, rejectData)
	}
	for _, sent := range packets {
		if sent.PacketType == rns.PacketProof {
			t.Fatal("did not expect proof packet for invalid client transfer")
		}
	}
	if got := link.GetStatus(); got != rns.LinkClosed {
		t.Fatalf("link status=%v want=%v", got, rns.LinkClosed)
	}
}

func TestPropagationPacketPersistsStampedStoreFormat(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	message := mustTestNewMessage(t, remoteDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}

	propagationStamp, stampValue, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("GenerateStamp(): %v", err)
	}
	transientData := append(append([]byte{}, message.Packed...), propagationStamp...)
	packedData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack propagation packet: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)

	packet := &rns.Packet{
		Destination:     link,
		DestinationType: rns.DestinationLink,
		FromPacked:      true,
		PacketHash:      bytes.Repeat([]byte{0x33}, 32),
	}

	router.propagationPacket(packedData, packet)

	transientID := rns.FullHash(message.Packed)
	entry := router.propagationEntries[string(transientID)]
	if entry == nil {
		t.Fatalf("expected propagation entry for %x", transientID)
	}
	fileData, err := os.ReadFile(entry.path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", entry.path, err)
	}
	if !bytes.Equal(fileData, transientData) {
		t.Fatalf("persisted file=%x want=%x", fileData, transientData)
	}
	if got, want := entry.size, len(transientData); got != want {
		t.Fatalf("entry size=%v want=%v", got, want)
	}
	if got, want := entry.stampValue, stampValue; got != want {
		t.Fatalf("stampValue=%v want=%v", got, want)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	recovered.propagationEnabled = true
	recovered.mu.Lock()
	recovered.reindexPropagationStoreLocked()
	recovered.mu.Unlock()

	recoveredEntry := recovered.propagationEntries[string(transientID)]
	if recoveredEntry == nil {
		t.Fatalf("expected recovered propagation entry for %x", transientID)
	}
	if !bytes.Equal(recoveredEntry.payload, message.Packed) {
		t.Fatalf("recovered payload=%x want=%x", recoveredEntry.payload, message.Packed)
	}
	if !bytes.Equal(recoveredEntry.destinationHash, remoteDest.Hash) {
		t.Fatalf("recovered destinationHash=%x want=%x", recoveredEntry.destinationHash, remoteDest.Hash)
	}
	if got, want := recoveredEntry.size, len(transientData); got != want {
		t.Fatalf("recovered size=%v want=%v", got, want)
	}
}

func TestPropagationResourceConcludedStoresValidClientMessage(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	message := mustTestNewMessage(t, remoteDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}

	propagationStamp, stampValue, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("GenerateStamp(): %v", err)
	}
	transientData := append(append([]byte{}, message.Packed...), propagationStamp...)
	resourceData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	transientID := rns.FullHash(message.Packed)
	entry := router.propagationEntries[string(transientID)]
	if entry == nil {
		t.Fatalf("expected propagation entry for %x", transientID)
	}
	if got, want := entry.stampValue, stampValue; got != want {
		t.Fatalf("stampValue=%v want=%v", got, want)
	}
	if got, want := router.clientPropagationMessagesReceived, 1; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkActive {
		t.Fatalf("link status=%v want=%v", got, rns.LinkActive)
	}
}

func TestPropagationResourceConcludedRejectsInvalidClientStamp(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	message := mustTestNewMessage(t, remoteDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	workblock, err := StampWorkblock(rns.FullHash(message.Packed), WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("StampWorkblock(): %v", err)
	}
	var invalidStamp []byte
	for candidate := 0; candidate < 256; candidate++ {
		stamp := bytes.Repeat([]byte{byte(candidate)}, StampSize)
		if !StampValid(stamp, router.propagationCost, workblock) {
			invalidStamp = stamp
			break
		}
	}
	if invalidStamp == nil {
		t.Fatal("expected invalid propagation stamp")
	}

	transientData := append(append([]byte{}, message.Packed...), invalidStamp...)
	resourceData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	transientID := rns.FullHash(message.Packed)
	if _, ok := router.propagationEntries[string(transientID)]; ok {
		t.Fatalf("did not expect propagation entry for %x", transientID)
	}
	if got, want := router.clientPropagationMessagesReceived, 0; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkClosed {
		t.Fatalf("link status=%v want=%v", got, rns.LinkClosed)
	}
}

func TestPropagationResourceConcludedRejectsClientMultiMessageTransfer(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	messages := make([]any, 0, 2)
	transientIDs := make([][]byte, 0, 2)
	for _, content := range []string{"one", "two"} {
		message := mustTestNewMessage(t, remoteDest, sourceDest, content, "title", nil)
		if err := message.Pack(); err != nil {
			t.Fatalf("Pack(%q): %v", content, err)
		}
		transientIDs = append(transientIDs, rns.FullHash(message.Packed))
		propagationStamp, _, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
		if err != nil {
			t.Fatalf("GenerateStamp(%q): %v", content, err)
		}
		messages = append(messages, append(append([]byte{}, message.Packed...), propagationStamp...))
	}
	resourceData, err := msgpack.Pack([]any{float64(1), messages})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	for _, transientID := range transientIDs {
		if _, ok := router.propagationEntries[string(transientID)]; ok {
			t.Fatalf("did not expect propagation entry for %x", transientID)
		}
	}
	if got, want := router.clientPropagationMessagesReceived, 0; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkClosed {
		t.Fatalf("link status=%v want=%v", got, rns.LinkClosed)
	}
}

func TestPropagationResourceConcludedAccountsUnpeeredIdentifiedTransfer(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	finalID := mustTestNewIdentity(t, true)
	remoteIdentity := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	finalDest := mustTestNewDestination(t, ts, finalID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	message := mustTestNewMessage(t, finalDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	propagationStamp, stampValue, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("GenerateStamp(): %v", err)
	}
	transientData := append(append([]byte{}, message.Packed...), propagationStamp...)
	resourceData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)
	setRouterLinkField(t, link, "remoteIdentity", remoteIdentity)
	setRouterLinkField(t, link, "linkID", []byte("unpeered-identified-link"))

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	transientID := rns.FullHash(message.Packed)
	entry := router.propagationEntries[string(transientID)]
	if entry == nil {
		t.Fatalf("expected propagation entry for %x", transientID)
	}
	if got, want := entry.stampValue, stampValue; got != want {
		t.Fatalf("stampValue=%v want=%v", got, want)
	}
	if got, want := router.unpeeredPropagationIncoming, 1; got != want {
		t.Fatalf("unpeeredPropagationIncoming=%v want=%v", got, want)
	}
	if got, want := router.unpeeredPropagationRXBytes, len(message.Packed); got != want {
		t.Fatalf("unpeeredPropagationRXBytes=%v want=%v", got, want)
	}
	if got, want := router.clientPropagationMessagesReceived, 0; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkActive {
		t.Fatalf("link status=%v want=%v", got, rns.LinkActive)
	}
}

func TestPropagationResourceConcludedAutopeersIdentifiedSyncSender(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	autopeerMaxDepth := 2
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath:      tmpDir,
		Autopeer:         true,
		AutopeerMaxdepth: &autopeerMaxDepth,
	})
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0
	router.hopsTo = func([]byte) int { return 1 }

	sourceID := mustTestNewIdentity(t, true)
	finalID := mustTestNewIdentity(t, true)
	remoteIdentity := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	finalDest := mustTestNewDestination(t, ts, finalID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	appData, err := msgpack.Pack([]any{
		false,
		1700002000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node B")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}
	ts.Remember(nil, remotePropagationHash, remoteIdentity.GetPublicKey(), appData)

	message := mustTestNewMessage(t, finalDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	propagationStamp, _, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("GenerateStamp(): %v", err)
	}
	transientData := append(append([]byte{}, message.Packed...), propagationStamp...)
	resourceData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)
	setRouterLinkField(t, link, "remoteIdentity", remoteIdentity)
	setRouterLinkField(t, link, "linkID", []byte("autopeer-incoming-sync-link"))

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	peer := router.peers[string(remotePropagationHash)]
	if peer == nil {
		t.Fatalf("expected autopeered sender %x", remotePropagationHash)
	}
	if got, want := peer.peeringTimebase, 1700002000.0; got != want {
		t.Fatalf("peeringTimebase=%v want=%v", got, want)
	}
	if got, want := peer.incoming, 1; got != want {
		t.Fatalf("peer incoming=%v want=%v", got, want)
	}
	if got, want := peer.rxBytes, len(message.Packed); got != want {
		t.Fatalf("peer rxBytes=%v want=%v", got, want)
	}
	if got, want := peer.metadata[int64(PNMetaName)], []byte("Node B"); !bytes.Equal(got.([]byte), want) {
		t.Fatalf("peer metadata name=%v want=%q", got, want)
	}
	if got, want := router.unpeeredPropagationIncoming, 0; got != want {
		t.Fatalf("unpeeredPropagationIncoming=%v want=%v", got, want)
	}
}

func TestPropagationResourceConcludedAcceptsValidatedPeerMultiMessageTransfer(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0

	sourceID := mustTestNewIdentity(t, true)
	finalID := mustTestNewIdentity(t, true)
	remoteIdentity := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	finalDest := mustTestNewDestination(t, ts, finalID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	ts.Remember(nil, remotePropagationHash, remoteIdentity.GetPublicKey(), nil)
	peer := NewPeer(router, remotePropagationHash)
	router.peers[string(remotePropagationHash)] = peer

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)
	setRouterLinkField(t, link, "remoteIdentity", remoteIdentity)
	linkID := []byte("validated-peer-link")
	setRouterLinkField(t, link, "linkID", linkID)

	transientIDs := make([][]byte, 0, 2)
	transientData := make([]any, 0, 2)
	for _, content := range []string{"one", "two"} {
		message := mustTestNewMessage(t, finalDest, sourceDest, content, "title", nil)
		if err := message.Pack(); err != nil {
			t.Fatalf("Pack(%q): %v", content, err)
		}
		transientIDs = append(transientIDs, rns.FullHash(message.Packed))
		propagationStamp, _, _, err := GenerateStamp(rns.FullHash(message.Packed), router.propagationCost, WorkblockExpandRoundsPN)
		if err != nil {
			t.Fatalf("GenerateStamp(%q): %v", content, err)
		}
		transientData = append(transientData, append(append([]byte{}, message.Packed...), propagationStamp...))
	}
	resourceData, err := msgpack.Pack([]any{float64(1), transientData})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)
	validKey, _, _, err := GenerateStamp(peeringID, router.peeringCost, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp(valid key): %v", err)
	}
	requestData, err := msgpack.Pack([]any{validKey, []any{transientIDs[0], transientIDs[1]}})
	if err != nil {
		t.Fatalf("Pack request data: %v", err)
	}
	if response := router.offerRequest("", requestData, nil, linkID, remoteIdentity, time.Now()); response != true {
		t.Fatalf("offer response=%v want=true", response)
	}

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)
	router.FlushQueues()

	if got, want := peer.incoming, 2; got != want {
		t.Fatalf("peer incoming=%v want=%v", got, want)
	}
	var wantRX int
	for _, transientID := range transientIDs {
		entry := router.propagationEntries[string(transientID)]
		if entry == nil {
			t.Fatalf("expected propagation entry for %x", transientID)
		}
		wantRX += len(entry.payload)
		if !containsByteSlice(entry.handledBy, remotePropagationHash) {
			t.Fatalf("expected handledBy to include peer for %x", transientID)
		}
		if containsByteSlice(entry.unhandledBy, remotePropagationHash) {
			t.Fatalf("did not expect unhandledBy to include source peer for %x", transientID)
		}
	}
	if got, want := peer.rxBytes, wantRX; got != want {
		t.Fatalf("peer rxBytes=%v want=%v", got, want)
	}
	if got, want := router.unpeeredPropagationIncoming, 0; got != want {
		t.Fatalf("unpeeredPropagationIncoming=%v want=%v", got, want)
	}
	if got, want := router.clientPropagationMessagesReceived, 0; got != want {
		t.Fatalf("clientPropagationMessagesReceived=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkActive {
		t.Fatalf("link status=%v want=%v", got, rns.LinkActive)
	}
}

func TestPropagationResourceConcludedThrottlesInvalidIdentifiedTransfer(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.propagationCost = 1
	router.propagationCostFlexibility = 0
	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	sourceID := mustTestNewIdentity(t, true)
	finalID := mustTestNewIdentity(t, true)
	remoteIdentity := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	finalDest := mustTestNewDestination(t, ts, finalID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	remoteDest := mustTestNewDestination(t, ts, remoteIdentity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	message := mustTestNewMessage(t, finalDest, sourceDest, "payload", "title", nil)
	if err := message.Pack(); err != nil {
		t.Fatalf("Pack(): %v", err)
	}
	workblock, err := StampWorkblock(rns.FullHash(message.Packed), WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("StampWorkblock(): %v", err)
	}
	var invalidStamp []byte
	for candidate := 0; candidate < 256; candidate++ {
		stamp := bytes.Repeat([]byte{byte(candidate)}, StampSize)
		if !StampValid(stamp, router.propagationCost, workblock) {
			invalidStamp = stamp
			break
		}
	}
	if invalidStamp == nil {
		t.Fatal("expected invalid propagation stamp")
	}

	transientData := append(append([]byte{}, message.Packed...), invalidStamp...)
	resourceData, err := msgpack.Pack([]any{float64(1), []any{transientData}})
	if err != nil {
		t.Fatalf("Pack resource data: %v", err)
	}

	link, err := rns.NewLink(ts, remoteDest)
	if err != nil {
		t.Fatalf("NewLink(): %v", err)
	}
	activateRouterTestLink(t, link)
	setRouterLinkField(t, link, "remoteIdentity", remoteIdentity)
	setRouterLinkField(t, link, "linkID", []byte("invalid-identified-link"))

	resource := &rns.Resource{}
	setResourceField(t, resource, "link", link)
	setResourceField(t, resource, "data", resourceData)
	setResourceIntField(t, resource, "status", rns.ResourceStatusComplete)

	router.propagationResourceConcluded(link, resource)

	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	until, ok := router.throttledPeers[string(remotePropagationHash)]
	if !ok {
		t.Fatalf("expected throttled peer entry for %x", remotePropagationHash)
	}
	if want := now.Add(180 * time.Second); !until.Equal(want) {
		t.Fatalf("throttle until=%v want=%v", until, want)
	}
	if got, want := router.unpeeredPropagationIncoming, 0; got != want {
		t.Fatalf("unpeeredPropagationIncoming=%v want=%v", got, want)
	}
	if got := link.GetStatus(); got != rns.LinkClosed {
		t.Fatalf("link status=%v want=%v", got, rns.LinkClosed)
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

func TestOfferRequestMarksValidatedPeerLink(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)
	wantID := rns.FullHash([]byte("validated-link-wanted"))
	linkID := []byte("validated-link-id")

	peeringID := make([]byte, 0, len(router.identity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, router.identity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)
	validKey, _, _, err := GenerateStamp(peeringID, router.peeringCost, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp(valid key): %v", err)
	}

	requestData, err := msgpack.Pack([]any{validKey, []any{wantID}})
	if err != nil {
		t.Fatalf("Pack request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, linkID, remoteIdentity, time.Now())
	if response != true {
		t.Fatalf("response=%v want=true", response)
	}
	if !router.validatedPeerLinks[string(linkID)] {
		t.Fatalf("expected validated peer link for %x", linkID)
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

func TestMessageGetRequestMissingWantedMessagesReturnsEmptyList(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)
	missingID := rns.FullHash([]byte("missing-message"))
	request, err := msgpack.Pack([]any{[]any{missingID}, nil})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	response := router.messageGetRequest("", request, nil, nil, remoteIdentity, time.Now())
	payloads, ok := response.([]any)
	if !ok {
		t.Fatalf("response type=%T want=[]any", response)
	}
	if len(payloads) != 0 {
		t.Fatalf("payloads len=%v want=0", len(payloads))
	}
}

func TestMessageGetRequestUsesStampedFileSizeForTransferLimit(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	stampData := bytes.Repeat([]byte{0x7a}, StampSize)
	payloadOne := append(append([]byte{}, remoteDestinationHash...), bytes.Repeat([]byte("a"), 32)...)
	payloadTwo := append(append([]byte{}, remoteDestinationHash...), bytes.Repeat([]byte("b"), 32)...)

	idOne := router.storePropagationMessageStamped(remoteDestinationHash, payloadOne, stampData, 1, nil)
	idTwo := router.storePropagationMessageStamped(remoteDestinationHash, payloadTwo, stampData, 1, nil)

	request, err := msgpack.Pack([]any{[]any{idOne, idTwo}, nil, 0.17})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	response := router.messageGetRequest("", request, nil, nil, remoteIdentity, time.Now())
	payloads, ok := response.([]any)
	if !ok {
		t.Fatalf("response type=%T want=[]any", response)
	}
	if len(payloads) != 1 {
		t.Fatalf("payloads len=%v want=1", len(payloads))
	}
	payload, ok := payloads[0].([]byte)
	if !ok {
		t.Fatalf("payload type=%T want=[]byte", payloads[0])
	}
	if !bytes.Equal(payload, payloadOne) {
		t.Fatalf("payload=%x want=%x", payload, payloadOne)
	}
}

func TestMessageGetRequestFalseTransferLimitReturnsNoMessages(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	transientID := router.storePropagationMessage(remoteDestinationHash, []byte("payload"))

	request, err := msgpack.Pack([]any{[]any{transientID}, nil, false})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	response := router.messageGetRequest("", request, nil, nil, remoteIdentity, time.Now())
	payloads, ok := response.([]any)
	if !ok {
		t.Fatalf("response type=%T want=[]any", response)
	}
	if len(payloads) != 0 {
		t.Fatalf("payloads len=%v want=0", len(payloads))
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

func TestMessageGetRequestMalformedWantsReturnsNil(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	request, err := msgpack.Pack([]any{1, nil})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	remoteIdentity := mustTestNewIdentity(t, true)
	if response := router.messageGetRequest("", request, nil, nil, remoteIdentity, time.Now()); response != nil {
		t.Fatalf("response=%#v want nil", response)
	}
}

func TestMessageGetRequestMalformedHavesReturnsNil(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	request, err := msgpack.Pack([]any{nil, 1})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	remoteIdentity := mustTestNewIdentity(t, true)
	if response := router.messageGetRequest("", request, nil, nil, remoteIdentity, time.Now()); response != nil {
		t.Fatalf("response=%#v want nil", response)
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
	filePath, _, err := router.writePropagationMessageFile(transientID, receivedAt, 0, destinationHash, payload, nil)
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

func TestWritePropagationMessageFileOmitsZeroStampSuffixForStampedZeroValue(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destinationHash := bytes.Repeat([]byte{0x44}, DestinationLength)
	payload := append(append([]byte{}, destinationHash...), []byte("zero-stamp-payload")...)
	stampData := make([]byte, StampSize)
	transientID := rns.FullHash(payload)
	receivedAt := time.Now().Add(-time.Hour)

	filePath, _, err := router.writePropagationMessageFile(transientID, receivedAt, 0, destinationHash, payload, stampData)
	if err != nil {
		t.Fatalf("writePropagationMessageFile() error = %v", err)
	}

	wantName := fmt.Sprintf("%x_%s", transientID, strconv.FormatFloat(peerTime(receivedAt), 'f', -1, 64))
	if got := filepath.Base(filePath); got != wantName {
		t.Fatalf("file name=%q want=%q", got, wantName)
	}
}

func TestPropagationStoreIgnoresPythonStyleZeroStampFilenameOnRestart(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	destinationHash := bytes.Repeat([]byte{0x44}, DestinationLength)
	lxmfPayload := append(append([]byte{}, destinationHash...), []byte("python-style-unstamped")...)
	stampData := make([]byte, StampSize)
	transientID := rns.FullHash(lxmfPayload)
	receivedAt := time.Now().Add(-time.Hour)

	storePath := router.propagationMessageStorePath()
	if err := os.MkdirAll(storePath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", storePath, err)
	}
	fileName := fmt.Sprintf("%x_%s", transientID, strconv.FormatFloat(peerTime(receivedAt), 'f', -1, 64))
	filePath := filepath.Join(storePath, fileName)
	fileData := append(append([]byte{}, lxmfPayload...), stampData...)
	if err := os.WriteFile(filePath, fileData, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", filePath, err)
	}

	router.EnablePropagation()

	if entry := router.propagationEntries[string(transientID)]; entry != nil {
		t.Fatalf("expected Python-style zero-stamp file %x to be ignored on restart, got %#v", transientID, entry)
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

func TestPropagationStoreQueuesPeerDistributionOnFlush(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	peerAHash := bytes.Repeat([]byte{0x21}, rns.TruncatedHashLength/8)
	peerBHash := bytes.Repeat([]byte{0x22}, rns.TruncatedHashLength/8)
	peerA := NewPeer(router, peerAHash)
	peerB := NewPeer(router, peerBHash)
	router.peers[string(peerAHash)] = peerA
	router.peers[string(peerBHash)] = peerB

	destinationHash := bytes.Repeat([]byte{0x42}, DestinationLength)
	transientID := router.storePropagationMessage(destinationHash, []byte("queued-for-peers"))

	router.FlushQueues()

	entry := router.propagationEntries[string(transientID)]
	if entry == nil {
		t.Fatalf("expected propagation entry for %x", transientID)
	}
	if len(entry.handledBy) != 0 {
		t.Fatalf("handledBy = %x, want empty", entry.handledBy)
	}
	if got := peerA.UnhandledMessageCount(); got != 1 {
		t.Fatalf("peerA.UnhandledMessageCount() = %v, want 1", got)
	}
	if got := peerB.UnhandledMessageCount(); got != 1 {
		t.Fatalf("peerB.UnhandledMessageCount() = %v, want 1", got)
	}
}

func TestFlushQueuesSkipsSourcePeerInDistribution(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()

	peerAHash := bytes.Repeat([]byte{0x31}, rns.TruncatedHashLength/8)
	peerBHash := bytes.Repeat([]byte{0x32}, rns.TruncatedHashLength/8)
	peerA := NewPeer(router, peerAHash)
	peerB := NewPeer(router, peerBHash)
	router.peers[string(peerAHash)] = peerA
	router.peers[string(peerBHash)] = peerB

	destinationHash := bytes.Repeat([]byte{0x52}, DestinationLength)
	transientID := router.storePropagationMessage(destinationHash, []byte("from-peer-a"))
	router.peerDistributionQueue = nil
	router.enqueuePeerDistribution(transientID, peerA)

	router.FlushQueues()

	if got := peerA.UnhandledMessageCount(); got != 0 {
		t.Fatalf("peerA.UnhandledMessageCount() = %v, want 0", got)
	}
	if got := peerB.UnhandledMessageCount(); got != 1 {
		t.Fatalf("peerB.UnhandledMessageCount() = %v, want 1", got)
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

func TestLocalTransientIDCachesLoadEachCacheIndependently(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Second)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.now = func() time.Time { return now }

	processedID := bytes.Repeat([]byte{0x73}, 32)
	processedPayload, err := msgpack.Pack(map[string]any{
		string(processedID): peerTime(now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatalf("Pack processed cache: %v", err)
	}
	if err := os.WriteFile(router.localDeliveriesPath(), []byte{0xc1}, 0o644); err != nil {
		t.Fatalf("WriteFile(local_deliveries): %v", err)
	}
	if err := os.WriteFile(router.locallyProcessedPath(), processedPayload, 0o644); err != nil {
		t.Fatalf("WriteFile(locally_processed): %v", err)
	}

	router.locallyDeliveredIDs = map[string]time.Time{"stale": now}
	router.locallyProcessedIDs = map[string]time.Time{}
	if err := router.LoadLocalTransientIDCaches(); err != nil {
		t.Fatalf("LoadLocalTransientIDCaches(): %v", err)
	}

	if len(router.locallyDeliveredIDs) != 0 {
		t.Fatalf("locallyDeliveredIDs=%v want empty after corrupt cache", router.locallyDeliveredIDs)
	}
	if got := router.locallyProcessedIDs[string(processedID)]; !got.Equal(now.Add(-time.Minute)) {
		t.Fatalf("processed timestamp = %v, want %v", got, now.Add(-time.Minute))
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

func TestAvailableTicketsRestartCleansAndRewritesExpiredEntries(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	now := time.Unix(1700000000, 0).UTC()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.now = func() time.Time { return now }

	destinationHash := bytes.Repeat([]byte{0x45}, DestinationLength)
	outboundTicket := bytes.Repeat([]byte{0x46}, TicketLength)
	router.ticketStore.outbound[string(destinationHash)] = TicketEntry{
		Expires: float64(now.Add(-time.Second).UnixNano()) / 1e9,
		Ticket:  outboundTicket,
	}

	inboundTicket := bytes.Repeat([]byte{0x47}, TicketLength)
	router.ticketStore.inbound[string(destinationHash)] = map[string]TicketEntry{
		string(inboundTicket): {
			Expires: float64(now.Add(-time.Duration(DefaultTicketGraceSeconds)*time.Second-time.Second).UnixNano()) / 1e9,
			Ticket:  inboundTicket,
		},
	}
	router.ticketStore.lastDeliveries[string(destinationHash)] = 1234

	if err := router.SaveAvailableTickets(); err != nil {
		t.Fatalf("SaveAvailableTickets(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	if got := recovered.ticketStore.OutboundTicket(destinationHash, now); len(got) != 0 {
		t.Fatalf("recovered outbound ticket=%x want empty", got)
	}
	if got := recovered.ticketStore.InboundTickets(destinationHash, now); len(got) != 0 {
		t.Fatalf("recovered inbound tickets=%x want empty", got)
	}
	if got := recovered.ticketStore.lastDeliveries[string(destinationHash)]; got != 1234 {
		t.Fatalf("recovered last delivery=%v want=1234", got)
	}

	data, err := os.ReadFile(recovered.availableTicketsPath())
	if err != nil {
		t.Fatalf("ReadFile(available_tickets): %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("Unpack(available_tickets): %v", err)
	}
	payload, ok := anyToMap(unpacked)
	if !ok {
		t.Fatalf("available_tickets payload type=%T want map", unpacked)
	}
	outbound, ok := anyToMap(payload["outbound"])
	if !ok {
		t.Fatalf("outbound payload type=%T want map", payload["outbound"])
	}
	if len(outbound) != 0 {
		t.Fatalf("outbound payload=%v want empty map", outbound)
	}
	inbound, ok := anyToMap(payload["inbound"])
	if !ok {
		t.Fatalf("inbound payload type=%T want map", payload["inbound"])
	}
	if len(inbound) != 0 {
		t.Fatalf("inbound payload=%v want empty map", inbound)
	}
	lastDeliveries, ok := anyToMap(payload["last_deliveries"])
	if !ok {
		t.Fatalf("last_deliveries payload type=%T want map", payload["last_deliveries"])
	}
	if len(lastDeliveries) != 1 {
		t.Fatalf("last_deliveries payload=%v want one preserved entry", lastDeliveries)
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

func TestOutboundStampCostRestartRecoveryPreservesZeroCost(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	destinationHash := bytes.Repeat([]byte{0x44}, DestinationLength)
	router.updateStampCost(destinationHash, 0)

	if err := router.Close(); err != nil {
		t.Fatalf("router.Close(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	stampCost, ok := recovered.OutboundStampCost(destinationHash)
	if !ok || stampCost != 0 {
		t.Fatalf("recovered OutboundStampCost = (%v,%v), want (0,true)", stampCost, ok)
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

	data, err := os.ReadFile(recovered.outboundStampCostsPath())
	if err != nil {
		t.Fatalf("ReadFile(outbound_stamp_costs): %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("Unpack(outbound_stamp_costs): %v", err)
	}
	payload, ok := anyToMap(unpacked)
	if !ok {
		t.Fatalf("outbound_stamp_costs payload type=%T want map", unpacked)
	}
	if len(payload) != 0 {
		t.Fatalf("outbound_stamp_costs payload=%v want empty map after startup cleanup", payload)
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

func TestDeliveryAnnounceHandlerLogsMalformedStampCostAndStillRetriesPendingOutbound(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogExtreme)
	logger.SetLogDest(rns.LogCallback)
	logs := make(chan string, 8)
	logger.SetLogCallback(func(msg string) {
		logs <- msg
	})

	ts := rns.NewTransportSystem(logger)
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

	nowSeconds := float64(time.Now().UnixNano()) / 1e9
	router.handleDeliveryAnnounce(dest.Hash, nil, []byte{0x91, 0xc1})

	select {
	case msg := <-logs:
		want := "An error occurred while trying to decode announced stamp cost. The contained exception was: encountered reserved code: 0xc1"
		if !bytes.Contains([]byte(msg), []byte(want)) {
			t.Fatalf("log = %q, want substring %q", msg, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected malformed stamp-cost log")
	}

	if _, ok := router.OutboundStampCost(dest.Hash); ok {
		t.Fatal("OutboundStampCost unexpectedly updated from malformed app data")
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

func TestDeliveryAnnounceHandlerMatchesPendingOutboundByDestinationHash(t *testing.T) {
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
		Destination:         nil,
		DestinationHash:     append([]byte{}, dest.Hash...),
		Method:              MethodOpportunistic,
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

	if message.NextDeliveryAttempt > nowSeconds+0.5 {
		t.Fatalf("NextDeliveryAttempt = %v, want immediate retry", message.NextDeliveryAttempt)
	}
	select {
	case <-retried:
	case <-time.After(time.Second):
		t.Fatal("delivery announce did not trigger outbound processing")
	}
}

func TestDeliveryAnnounceHandlerWaitsForActiveOutboundProcessingBeforeRetrying(t *testing.T) {
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

	sleepCalled := make(chan struct{}, 1)
	releaseSleep := make(chan struct{})
	retried := make(chan struct{}, 1)
	router.outboundProcessingActive.Store(true)
	router.outboundTriggerSleep = func(time.Duration) {
		select {
		case sleepCalled <- struct{}{}:
		default:
		}
		<-releaseSleep
	}
	router.processOutbound = func() {
		retried <- struct{}{}
	}

	appData, err := msgpack.Pack([]any{[]byte("Carol"), 8})
	if err != nil {
		t.Fatalf("Pack announce app data: %v", err)
	}

	router.handleDeliveryAnnounce(dest.Hash, nil, appData)

	select {
	case <-retried:
		t.Fatal("delivery announce retried before active outbound processing completed")
	case <-sleepCalled:
	case <-time.After(time.Second):
		t.Fatal("delivery announce did not wait on active outbound processing")
	}

	router.outboundProcessingActive.Store(false)
	close(releaseSleep)

	select {
	case <-retried:
	case <-time.After(time.Second):
		t.Fatal("delivery announce did not retry after outbound processing completed")
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

func TestPropagationAnnounceHandlerRefreshesStaticPeerConfig(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.maxPeeringCost = 26

	now := time.Unix(1700001234, 0).UTC()
	router.now = func() time.Time { return now }

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	oldStampCost := 3
	oldSyncLimit := 32
	oldTransferLimit := 64.0
	oldFlexibility := 1
	oldPeeringCost := 2
	router.staticPeers[string(remoteHash)] = struct{}{}
	router.peers[string(remoteHash)] = &Peer{
		destinationHash:                 append([]byte{}, remoteHash...),
		alive:                           false,
		lastHeard:                       1,
		peeringTimebase:                 10,
		metadata:                        map[any]any{int64(PNMetaName): []byte("Old Node")},
		propagationStampCost:            cloneOptionalInt(&oldStampCost),
		propagationSyncLimit:            cloneOptionalInt(&oldSyncLimit),
		propagationTransferLimit:        cloneOptionalFloat64(&oldTransferLimit),
		propagationStampCostFlexibility: cloneOptionalInt(&oldFlexibility),
		peeringCost:                     cloneOptionalInt(&oldPeeringCost),
	}

	appData, err := msgpack.Pack([]any{
		false,
		1700002000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node B")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}

	router.handlePropagationAnnounce(remoteHash, remoteIdentity, appData)

	peer := router.peers[string(remoteHash)]
	if peer == nil {
		t.Fatal("expected static peer to remain present")
	}
	if !peer.alive {
		t.Fatal("expected static peer to be refreshed alive")
	}
	if peer.peeringTimebase != 1700002000 {
		t.Fatalf("peeringTimebase=%v want=1700002000", peer.peeringTimebase)
	}
	if peer.lastHeard != peerTime(now) {
		t.Fatalf("lastHeard=%v want=%v", peer.lastHeard, peerTime(now))
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
	if got := peer.metadata[int64(PNMetaName)]; !bytes.Equal(got.([]byte), []byte("Node B")) {
		t.Fatalf("metadata name=%v want %q", got, "Node B")
	}
}

func TestPropagationAnnounceHandlerIgnoresPathResponseForHeardStaticPeer(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.maxPeeringCost = 26

	now := time.Unix(1700001234, 0).UTC()
	router.now = func() time.Time { return now }

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	oldStampCost := 3
	oldSyncLimit := 32
	oldTransferLimit := 64.0
	oldFlexibility := 1
	oldPeeringCost := 2
	router.staticPeers[string(remoteHash)] = struct{}{}
	router.peers[string(remoteHash)] = &Peer{
		destinationHash:                 append([]byte{}, remoteHash...),
		alive:                           false,
		lastHeard:                       1,
		peeringTimebase:                 10,
		metadata:                        map[any]any{int64(PNMetaName): []byte("Old Node")},
		propagationStampCost:            cloneOptionalInt(&oldStampCost),
		propagationSyncLimit:            cloneOptionalInt(&oldSyncLimit),
		propagationTransferLimit:        cloneOptionalFloat64(&oldTransferLimit),
		propagationStampCostFlexibility: cloneOptionalInt(&oldFlexibility),
		peeringCost:                     cloneOptionalInt(&oldPeeringCost),
	}

	appData, err := msgpack.Pack([]any{
		false,
		1700002000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node B")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}

	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, appData, true)

	peer := router.peers[string(remoteHash)]
	if peer == nil {
		t.Fatal("expected static peer to remain present")
	}
	if peer.alive {
		t.Fatal("expected path response not to refresh heard static peer")
	}
	if peer.peeringTimebase != 10 {
		t.Fatalf("peeringTimebase=%v want=10", peer.peeringTimebase)
	}
	if got := peer.metadata[int64(PNMetaName)]; !bytes.Equal(got.([]byte), []byte("Old Node")) {
		t.Fatalf("metadata name=%v want %q", got, "Old Node")
	}
}

func TestPropagationAnnounceHandlerRefreshesUnheardStaticPeerOnPathResponse(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.maxPeeringCost = 26

	now := time.Unix(1700001234, 0).UTC()
	router.now = func() time.Time { return now }

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	oldStampCost := 3
	oldSyncLimit := 32
	oldTransferLimit := 64.0
	oldFlexibility := 1
	oldPeeringCost := 2
	router.staticPeers[string(remoteHash)] = struct{}{}
	router.peers[string(remoteHash)] = &Peer{
		destinationHash:                 append([]byte{}, remoteHash...),
		alive:                           false,
		lastHeard:                       0,
		peeringTimebase:                 10,
		metadata:                        map[any]any{int64(PNMetaName): []byte("Old Node")},
		propagationStampCost:            cloneOptionalInt(&oldStampCost),
		propagationSyncLimit:            cloneOptionalInt(&oldSyncLimit),
		propagationTransferLimit:        cloneOptionalFloat64(&oldTransferLimit),
		propagationStampCostFlexibility: cloneOptionalInt(&oldFlexibility),
		peeringCost:                     cloneOptionalInt(&oldPeeringCost),
	}

	appData, err := msgpack.Pack([]any{
		false,
		1700002000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node B")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}

	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, appData, true)

	peer := router.peers[string(remoteHash)]
	if peer == nil {
		t.Fatal("expected static peer to remain present")
	}
	if !peer.alive {
		t.Fatal("expected unheard static peer path response to refresh peer")
	}
	if peer.peeringTimebase != 1700002000 {
		t.Fatalf("peeringTimebase=%v want=1700002000", peer.peeringTimebase)
	}
	if peer.lastHeard != peerTime(now) {
		t.Fatalf("lastHeard=%v want=%v", peer.lastHeard, peerTime(now))
	}
}

func TestPropagationAnnounceHandlerIgnoresPathResponseForAutopeer(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	autopeerMaxDepth := 2
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath:      tmpDir,
		Autopeer:         true,
		AutopeerMaxdepth: &autopeerMaxDepth,
	})
	router.propagationEnabled = true
	router.maxPeeringCost = 26
	router.hopsTo = func([]byte) int { return 1 }

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	appData, err := msgpack.Pack([]any{
		false,
		1700002000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node B")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}

	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, appData, true)

	if peer := router.peers[string(remoteHash)]; peer != nil {
		t.Fatal("expected autopeer path response to be ignored")
	}
}

func TestPropagationAnnounceHandlerLogsMalformedAppData(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogExtreme)
	logger.SetLogDest(rns.LogCallback)
	logs := make(chan string, 8)
	logger.SetLogCallback(func(msg string) {
		logs <- msg
	})

	ts := rns.NewTransportSystem(logger)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.propagationEnabled = true
	router.maxPeeringCost = 26
	router.hopsTo = func([]byte) int { return 1 }

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, []byte{0x91, 0xc1}, false)

	select {
	case msg := <-logs:
		want := "Could not validate propagation node announce data: encountered reserved code: 0xc1"
		if !bytes.Contains([]byte(msg), []byte(want)) {
			t.Fatalf("log = %q, want substring %q", msg, want)
		}
	case <-time.After(time.Second):
		t.Fatal("expected malformed propagation announce log")
	}

	if peer := router.peers[string(remoteHash)]; peer != nil {
		t.Fatal("expected malformed propagation announce to be ignored")
	}
}

func TestPropagationAnnounceHandlerRecoversPanickingHopsLookup(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogExtreme)
	logger.SetLogDest(rns.LogCallback)
	logs := make(chan string, 8)
	logger.SetLogCallback(func(msg string) {
		logs <- msg
	})

	ts := rns.NewTransportSystem(logger)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	autopeerMaxDepth := 2
	router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
		StoragePath:      tmpDir,
		Autopeer:         true,
		AutopeerMaxdepth: &autopeerMaxDepth,
	})
	router.propagationEnabled = true
	router.maxPeeringCost = 26
	router.hopsTo = func([]byte) int {
		panic("boom")
	}

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	appData, err := msgpack.Pack([]any{
		false,
		1700002000,
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node B")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}

	router.handlePropagationAnnounceWithContext(remoteHash, remoteIdentity, appData, false)

	want := []string{
		"Error while evaluating propagation node announce, ignoring announce.",
		"The contained exception was: boom",
	}
	for _, wantMsg := range want {
		select {
		case msg := <-logs:
			if !bytes.Contains([]byte(msg), []byte(wantMsg)) {
				t.Fatalf("log = %q, want substring %q", msg, wantMsg)
			}
		case <-time.After(time.Second):
			t.Fatalf("expected log containing %q", wantMsg)
		}
	}

	if peer := router.peers[string(remoteHash)]; peer != nil {
		t.Fatal("expected panicking propagation announce to be ignored")
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

func TestPropagationAnnounceHandlerRejectsInvalidAnnounceShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		appData []any
		wantLog string
	}{
		{
			name: "missing sync limit",
			appData: []any{
				false,
				1700002000,
				true,
				128,
				nil,
				[]any{11, 3, 7},
				map[any]any{PNMetaName: []byte("Node B")},
			},
			wantLog: "Could not validate propagation node announce data: Invalid announce data: Could not decode propagation sync limit",
		},
		{
			name: "non-map metadata",
			appData: []any{
				false,
				1700002000,
				true,
				128,
				256,
				[]any{11, 3, 7},
				[]any{"not", "metadata"},
			},
			wantLog: "Could not validate propagation node announce data: Invalid announce data: Could not decode metadata",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := rns.NewLogger()
			logger.SetLogLevel(rns.LogExtreme)
			logger.SetLogDest(rns.LogCallback)
			logs := make(chan string, 8)
			logger.SetLogCallback(func(msg string) {
				logs <- msg
			})

			ts := rns.NewTransportSystem(logger)
			tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
			defer cleanup()

			autopeerMaxDepth := 2
			router := mustTestNewRouterFromConfig(t, ts, RouterConfig{
				StoragePath:      tmpDir,
				Autopeer:         true,
				AutopeerMaxdepth: &autopeerMaxDepth,
			})
			router.propagationEnabled = true
			router.maxPeeringCost = 26
			router.hopsTo = func([]byte) int { return 1 }

			remoteIdentity := mustTestNewIdentity(t, true)
			remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
			packed, err := msgpack.Pack(tc.appData)
			if err != nil {
				t.Fatalf("Pack propagation app data: %v", err)
			}

			router.handlePropagationAnnounce(remoteHash, remoteIdentity, packed)

			select {
			case msg := <-logs:
				if !bytes.Contains([]byte(msg), []byte(tc.wantLog)) {
					t.Fatalf("log = %q, want substring %q", msg, tc.wantLog)
				}
			case <-time.After(time.Second):
				t.Fatalf("expected invalid announce log containing %q", tc.wantLog)
			}

			if peer := router.peers[string(remoteHash)]; peer != nil {
				t.Fatal("expected invalid announce to be ignored")
			}
		})
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

	// Simulate an in-progress sync without starting the asynchronous
	// path-wait job, since Python can race that timeout back to PRNoPath
	// after cancellation.
	router.propagationTransferState = PRPathRequested
	router.wantsDownloadOnPathAvailableFrom = append([]byte{}, propNode...)
	router.wantsDownloadOnPathAvailableTo = router.identity
	router.propagationTransferLastResult = 4
	router.propagationTransferLastResultSet = true
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
	if got, ok := router.PropagationTransferLastResult(); ok {
		t.Fatalf("last result after cancel = (%v,%v), want cleared result", got, ok)
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
		if router.PropagationTransferState() != PRLinkEstablished {
			t.Fatalf("state during identify = %v, want PRLinkEstablished", router.PropagationTransferState())
		}
		identifiedWith = identity
		return nil
	}
	router.requestLink = func(_ *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), _ time.Duration) (*rns.RequestReceipt, error) {
		if router.PropagationTransferState() != PRLinkEstablished {
			t.Fatalf("state during request = %v, want PRLinkEstablished", router.PropagationTransferState())
		}
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
	if got, ok := router.PropagationTransferLastResult(); !ok || got != 0 {
		t.Fatalf("last result = (%v,%v), want (0,true)", got, ok)
	}
}

func TestPropagationSyncMessageListResponseEmptyBytesTearsDown(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	link := &rns.Link{}
	router.outboundPropagationLink = link
	var teardownCount int
	router.teardownLink = func(closed *rns.Link) {
		if closed != link {
			t.Fatal("teardown called with unexpected link")
		}
		teardownCount++
	}

	router.messageListResponse(&rns.RequestReceipt{
		Link:     link,
		Response: []byte{},
	})

	if teardownCount != 1 {
		t.Fatalf("teardown count = %d, want 1", teardownCount)
	}
}

func TestPropagationSyncMessageListResponseAllHavesRequestsPurge(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	haveID := []byte("have")
	router.locallyDeliveredIDs[string(haveID)] = time.Now()

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
		Response: []any{haveID},
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
	if len(wants) != 0 {
		t.Fatalf("wants = %x, want empty", wants)
	}
	haves, ok := fields[1].([][]byte)
	if !ok {
		t.Fatalf("haves type = %T, want [][]byte", fields[1])
	}
	if len(haves) != 1 || !bytes.Equal(haves[0], haveID) {
		t.Fatalf("haves = %x, want [%x]", haves, haveID)
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
	if got, ok := router.PropagationTransferLastResult(); !ok || got != 2 {
		t.Fatalf("last result = (%v,%v), want (2,true)", got, ok)
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
	if _, err := os.Stat(filepath.Join(router.storagePath, "locally_processed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no locally_processed file after sync completion, Stat() error = %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	if _, ok := recovered.locallyDeliveredIDs[string(freshTransientID)]; !ok {
		t.Fatal("expected recovered local_deliveries cache to retain fresh transient ID")
	}
	if len(recovered.locallyProcessedIDs) != 0 {
		t.Fatalf("recovered locallyProcessedIDs = %v, want empty", recovered.locallyProcessedIDs)
	}
}

func TestPropagationSyncMessageGetResponseEmptyBytesCompletes(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.messageGetResponse(&rns.RequestReceipt{
		Link:     &rns.Link{},
		Response: []byte{},
	})

	if router.PropagationTransferState() != PRComplete {
		t.Fatalf("state = %v, want PRComplete", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 1.0 {
		t.Fatalf("progress = %v, want 1.0", router.PropagationTransferProgress())
	}
	if got, ok := router.PropagationTransferLastResult(); !ok || got != 0 {
		t.Fatalf("last result = (%v,%v), want (0,true)", got, ok)
	}
}

func TestPropagationSyncMessageGetResponseEmptyStringCompletes(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	router.messageGetResponse(&rns.RequestReceipt{
		Link:     &rns.Link{},
		Response: "",
	})

	if router.PropagationTransferState() != PRComplete {
		t.Fatalf("state = %v, want PRComplete", router.PropagationTransferState())
	}
	if router.PropagationTransferProgress() != 1.0 {
		t.Fatalf("progress = %v, want 1.0", router.PropagationTransferProgress())
	}
	if got, ok := router.PropagationTransferLastResult(); !ok || got != 0 {
		t.Fatalf("last result = (%v,%v), want (0,true)", got, ok)
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
	router.propagationTransferLastResultSet = true
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
	if got, ok := router.PropagationTransferLastResult(); ok {
		t.Fatalf("last result = (%v,%v), want cleared result", got, ok)
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
	msg.DeferPropagationStamp = false

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
	msg.DeferPropagationStamp = false

	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	ts.Remember(nil, propNodeDest.Hash, propNodeID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	hasPath := false
	requestCount := 0
	router.hasPath = func(_ []byte) bool { return hasPath }
	router.requestPath = func(_ []byte) error { requestCount++; return nil }
	fakeLink, err := rns.NewLink(ts, propNodeDest)
	if err != nil {
		t.Fatalf("NewLink(fake): %v", err)
	}
	var established func(*rns.Link)
	newLinkCount := 0
	establishCount := 0
	linkState := rns.LinkPending
	router.newLink = func(_ rns.Transport, dest *rns.Destination) (*rns.Link, error) {
		newLinkCount++
		if !bytes.Equal(dest.Hash, propNodeDest.Hash) {
			t.Fatalf("newLink destination hash = %x, want %x", dest.Hash, propNodeDest.Hash)
		}
		return fakeLink, nil
	}
	router.setLinkEstablishedCallback = func(link *rns.Link, callback func(*rns.Link)) {
		if link != fakeLink {
			t.Fatal("setLinkEstablishedCallback received unexpected link")
		}
		established = callback
	}
	router.establishLink = func(link *rns.Link) error {
		if link != fakeLink {
			t.Fatal("establishLink received unexpected link")
		}
		establishCount++
		return nil
	}
	router.linkStatus = func(link *rns.Link) int {
		if link == fakeLink {
			return linkState
		}
		return link.GetStatus()
	}
	sendCount := 0
	router.sendPacket = func(packet *rns.Packet) error {
		sendCount++
		if packet.Destination != fakeLink {
			t.Fatal("expected propagated send to target the propagation link")
		}
		if !bytes.Equal(packet.Data, msg.PropagationPacked) {
			t.Fatal("expected propagated packet data")
		}
		return nil
	}

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
	if newLinkCount != 0 || establishCount != 0 || sendCount != 0 {
		t.Fatalf("before path, newLink=%v establish=%v send=%v want 0/0/0", newLinkCount, establishCount, sendCount)
	}

	// Advance past path request wait, path now available. Python should
	// establish a propagation link before sending.
	now = now.Add(pathRequestWait + time.Second)
	hasPath = true
	router.ProcessOutbound()

	if newLinkCount != 1 {
		t.Fatalf("new link count=%v want=1", newLinkCount)
	}
	if establishCount != 1 {
		t.Fatalf("establish count=%v want=1", establishCount)
	}
	if sendCount != 0 {
		t.Fatalf("send count=%v want=0 before link establishment", sendCount)
	}
	if established == nil {
		t.Fatal("expected link-established callback to be installed")
	}

	linkState = rns.LinkActive
	established(fakeLink)

	if sendCount != 1 {
		t.Fatalf("send count=%v want=1 after link establishment", sendCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundPropagatedActiveLinkUsesPropagationLink(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)
	router.linkStatus = func(link *rns.Link) int {
		if link == router.outboundPropagationLink {
			return rns.LinkActive
		}
		return link.GetStatus()
	}
	router.hasPath = func(_ []byte) bool { t.Fatal("did not expect path lookup with active propagation link"); return false }
	router.requestPath = func(_ []byte) error { t.Fatal("did not expect path request with active propagation link"); return nil }

	sendCount := 0
	router.sendPacket = func(packet *rns.Packet) error {
		sendCount++
		if packet.Destination != router.outboundPropagationLink {
			t.Fatal("expected packet destination to be the active propagation link")
		}
		if !bytes.Equal(packet.Data, msg.PropagationPacked) {
			t.Fatal("expected propagated packet payload")
		}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if sendCount != 1 {
		t.Fatalf("send count=%v want=1", sendCount)
	}
	if msg.State != StateSent {
		t.Fatalf("state=%v want=%v", msg.State, StateSent)
	}
}

func TestProcessOutboundPropagatedPendingLinkWaits(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)
	router.linkStatus = func(link *rns.Link) int {
		if link == router.outboundPropagationLink {
			return rns.LinkPending
		}
		return link.GetStatus()
	}

	sendCount := 0
	router.sendPacket = func(_ *rns.Packet) error { sendCount++; return nil }
	router.requestPath = func(_ []byte) error {
		t.Fatal("did not expect path request while propagation link is pending")
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if sendCount != 0 {
		t.Fatalf("send count=%v want=0", sendCount)
	}
	if msg.State != StateOutbound {
		t.Fatalf("state=%v want=%v", msg.State, StateOutbound)
	}
	if len(router.pendingOutbound) != 1 {
		t.Fatalf("pending outbound=%v want=1", len(router.pendingOutbound))
	}
}

func TestProcessOutboundPropagatedClosedLinkClearsAndRetries(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	ts.Remember(nil, propNodeDest.Hash, propNodeID.GetPublicKey(), nil)

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	closedLink, err := rns.NewLink(ts, propNodeDest)
	if err != nil {
		t.Fatalf("NewLink(closed): %v", err)
	}
	freshLink, err := rns.NewLink(ts, propNodeDest)
	if err != nil {
		t.Fatalf("NewLink(fresh): %v", err)
	}
	router.outboundPropagationLink = closedLink
	router.linkStatus = func(link *rns.Link) int {
		switch link {
		case closedLink:
			return rns.LinkClosed
		case freshLink:
			return rns.LinkPending
		default:
			return link.GetStatus()
		}
	}
	router.hasPath = func(hash []byte) bool { return bytes.Equal(hash, propNodeDest.Hash) }
	newLinkCount := 0
	establishCount := 0
	router.newLink = func(_ rns.Transport, dest *rns.Destination) (*rns.Link, error) {
		newLinkCount++
		if !bytes.Equal(dest.Hash, propNodeDest.Hash) {
			t.Fatalf("newLink destination hash = %x, want %x", dest.Hash, propNodeDest.Hash)
		}
		return freshLink, nil
	}
	router.establishLink = func(link *rns.Link) error {
		if link != freshLink {
			t.Fatal("expected fresh propagation link to be established")
		}
		establishCount++
		return nil
	}
	sendCount := 0
	router.sendPacket = func(_ *rns.Packet) error { sendCount++; return nil }

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	if sendCount != 0 {
		t.Fatalf("send count=%v want=0", sendCount)
	}
	if router.outboundPropagationLink != nil {
		t.Fatal("expected closed propagation link to be cleared")
	}
	if newLinkCount != 0 || establishCount != 0 {
		t.Fatalf("newLink=%v establish=%v want 0/0 before retry", newLinkCount, establishCount)
	}
	if msg.NextDeliveryAttempt <= 0 {
		t.Fatal("expected retry to be scheduled after closed link")
	}
	now = now.Add(deliveryRetryWait + time.Second)
	router.ProcessOutbound()

	if newLinkCount != 1 {
		t.Fatalf("new link count=%v want=1 after retry", newLinkCount)
	}
	if establishCount != 1 {
		t.Fatalf("establish count=%v want=1 after retry", establishCount)
	}
	if router.outboundPropagationLink != freshLink {
		t.Fatal("expected retry to install a fresh propagation link")
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
	msg.DeferPropagationStamp = false

	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)
	router.linkStatus = func(link *rns.Link) int {
		if link == router.outboundPropagationLink {
			return rns.LinkActive
		}
		return link.GetStatus()
	}
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

	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	ts.Remember(nil, propNodeDest.Hash, propNodeID.GetPublicKey(), nil)
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return true }
	router.requestPath = func(_ []byte) error { return nil }
	fakeLink, err := rns.NewLink(ts, propNodeDest)
	if err != nil {
		t.Fatalf("NewLink(fake): %v", err)
	}
	var established func(*rns.Link)
	linkState := rns.LinkPending
	router.newLink = func(_ rns.Transport, dest *rns.Destination) (*rns.Link, error) {
		if !bytes.Equal(dest.Hash, propNodeDest.Hash) {
			t.Fatalf("newLink destination hash = %x, want %x", dest.Hash, propNodeDest.Hash)
		}
		return fakeLink, nil
	}
	router.setLinkEstablishedCallback = func(link *rns.Link, callback func(*rns.Link)) {
		if link != fakeLink {
			t.Fatal("setLinkEstablishedCallback got unexpected link")
		}
		established = callback
	}
	router.establishLink = func(link *rns.Link) error {
		if link != fakeLink {
			t.Fatal("establishLink got unexpected link")
		}
		return nil
	}
	router.linkStatus = func(link *rns.Link) int {
		if link == fakeLink {
			return linkState
		}
		return link.GetStatus()
	}

	// Fail every direct send attempt.
	sendCount := 0
	router.sendPacket = func(packet *rns.Packet) error {
		if packet.Destination == fakeLink {
			sendCount++
			return nil
		}
		return assertErr("direct send failed")
	}

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

	if established == nil {
		now = now.Add(deliveryRetryWait + time.Second)
		router.ProcessOutbound()
	}
	if established == nil {
		t.Fatal("expected propagation fallback to establish a link")
	}
	if sendCount != 0 {
		t.Fatalf("send count=%v want=0 before link establishment", sendCount)
	}

	linkState = rns.LinkActive
	established(fakeLink)

	if msg.State != StateSent {
		t.Fatalf("state after propagation=%v want=%v", msg.State, StateSent)
	}
	if sendCount != 1 {
		t.Fatalf("send count=%v want=1 after propagation fallback", sendCount)
	}
}

func TestPropagationTransferDelivered(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)
	router.linkStatus = func(link *rns.Link) int {
		if link == router.outboundPropagationLink {
			return rns.LinkActive
		}
		return link.GetStatus()
	}

	delivered := false
	msg.DeliveryCallback = func(_ *Message) { delivered = true }
	var lastPacket *rns.Packet
	router.sendPacket = func(packet *rns.Packet) error {
		lastPacket = packet
		packet.Receipt = &rns.PacketReceipt{}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if lastPacket == nil || lastPacket.Receipt == nil {
		t.Fatal("expected propagated packet receipt")
	}
	if got, want := msg.State, StateSending; got != want {
		t.Fatalf("state after send=%v want=%v", got, want)
	}
	if got, want := msg.Progress, 0.50; got != want {
		t.Fatalf("progress after send=%v want=%v", got, want)
	}

	lastPacket.Receipt.TriggerDelivery()

	if got, want := msg.State, StateSent; got != want {
		t.Fatalf("state after delivery=%v want=%v", got, want)
	}
	if got, want := msg.Progress, 1.0; got != want {
		t.Fatalf("progress after delivery=%v want=%v", got, want)
	}
	if !delivered {
		t.Fatal("expected delivery callback after propagated proof")
	}
}

func TestPropagationTransferTimeout(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)
	router.linkStatus = func(link *rns.Link) int {
		if link == router.outboundPropagationLink {
			return rns.LinkActive
		}
		return link.GetStatus()
	}

	var lastPacket *rns.Packet
	router.sendPacket = func(packet *rns.Packet) error {
		lastPacket = packet
		packet.Receipt = &rns.PacketReceipt{}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	lastPacket.Receipt.TriggerTimeout()

	if got, want := msg.State, StateOutbound; got != want {
		t.Fatalf("state after timeout=%v want=%v", got, want)
	}
	if got, want := msg.Progress, 0.0; got != want {
		t.Fatalf("progress after timeout=%v want=%v", got, want)
	}
}

func TestPropagationTransferProgress(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, string(bytes.Repeat([]byte("P"), LinkPacketMaxContent)), "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	link, err := rns.NewLink(ts, propNodeDest)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateRouterTestLink(t, link)
	router.outboundPropagationLink = link
	router.linkStatus = func(candidate *rns.Link) int {
		if candidate == link {
			return rns.LinkActive
		}
		return candidate.GetStatus()
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if msg.ResourceRepresentation == nil {
		t.Fatal("expected propagated resource representation")
	}
	if got, want := msg.State, StateSending; got != want {
		t.Fatalf("state after resource start=%v want=%v", got, want)
	}
	if got, want := msg.Progress, 0.10; got != want {
		t.Fatalf("progress after resource start=%v want=%v", got, want)
	}

	setResourceIntField(t, msg.ResourceRepresentation, "sentParts", 5)
	setResourceIntField(t, msg.ResourceRepresentation, "totalParts", 10)
	invokeResourceProgressCallback(t, msg.ResourceRepresentation)
	if got, want := msg.Progress, 0.55; got != want {
		t.Fatalf("progress after resource update=%v want=%v", got, want)
	}

	setResourceIntField(t, msg.ResourceRepresentation, "status", rns.ResourceStatusComplete)
	invokeResourceCallback(t, msg.ResourceRepresentation)
	if got, want := msg.State, StateSent; got != want {
		t.Fatalf("state after resource completion=%v want=%v", got, want)
	}
	if got, want := msg.Progress, 1.0; got != want {
		t.Fatalf("progress after resource completion=%v want=%v", got, want)
	}
}

func TestPropagationTransferClosedLink(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.Method = MethodPropagated
	msg.State = StateSending
	msg.Progress = 0.4
	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	router.pendingOutbound = []*Message{msg}
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)

	router.handleOutboundPropagationLinkClosed(router.outboundPropagationLink)

	if got, want := msg.State, StateOutbound; got != want {
		t.Fatalf("state after closed link=%v want=%v", got, want)
	}
	if got, want := msg.Progress, 0.0; got != want {
		t.Fatalf("progress after closed link=%v want=%v", got, want)
	}
	if msg.NextDeliveryAttempt <= 0 {
		t.Fatal("expected closed link recovery to schedule a retry")
	}
}

func TestPropagationTransferInvalidStampSignalRejectsMessage(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	propNodeID := mustTestNewIdentity(t, true)
	propNodeDest := mustTestNewDestination(t, ts, propNodeID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated
	msg.DeferPropagationStamp = false
	if err := router.SetOutboundPropagationNode(propNodeDest.Hash); err != nil {
		t.Fatal(err)
	}
	router.outboundPropagationLink, _ = rns.NewLink(ts, propNodeDest)
	router.linkStatus = func(link *rns.Link) int {
		if link == router.outboundPropagationLink {
			return rns.LinkActive
		}
		return link.GetStatus()
	}

	failed := false
	msg.FailedCallback = func(_ *Message) { failed = true }
	router.sendPacket = func(packet *rns.Packet) error {
		packet.Receipt = &rns.PacketReceipt{}
		return nil
	}

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}
	if got, want := msg.State, StateSending; got != want {
		t.Fatalf("state after send=%v want=%v", got, want)
	}
	if len(router.pendingOutbound) != 1 {
		t.Fatalf("pending outbound=%v want=1", len(router.pendingOutbound))
	}

	signalData, err := msgpack.Pack([]any{peerErrorInvalidStamp})
	if err != nil {
		t.Fatalf("Pack invalid stamp signal: %v", err)
	}
	invokeRouterLinkPacketCallback(t, router.outboundPropagationLink, signalData)

	if got, want := msg.State, StateRejected; got != want {
		t.Fatalf("state after invalid stamp signal=%v want=%v", got, want)
	}
	if !failed {
		t.Fatal("expected failed callback after invalid propagation stamp signal")
	}
	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pending outbound after rejection=%v want=0", len(router.pendingOutbound))
	}
	if router.outboundPropagationLinkMessage != nil {
		t.Fatal("expected propagation link message tracking to be cleared")
	}
}

func activateRouterTestLink(t *testing.T, link *rns.Link) {
	t.Helper()
	setRouterLinkField(t, link, "status", rns.LinkActive)

	token, err := rnscrypto.NewToken(bytes.Repeat([]byte{0xA5}, 32))
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	setRouterLinkField(t, link, "token", token)
}

func invokeRouterLinkPacketCallback(t *testing.T, link *rns.Link, data []byte) {
	t.Helper()
	callbackField := reflect.ValueOf(link).Elem().FieldByName("callbacks")
	packetCallback := reflect.NewAt(callbackField.Type(), unsafe.Pointer(callbackField.UnsafeAddr())).Elem().FieldByName("Packet")
	if packetCallback.IsNil() {
		t.Fatal("expected link packet callback to be installed")
	}
	packet := &rns.Packet{Data: data, Destination: link}
	packetCallback.Interface().(func(*rns.Link, *rns.Packet))(link, packet)
}

func setRouterLinkField(t *testing.T, link *rns.Link, name string, value any) {
	t.Helper()
	field := reflect.ValueOf(link).Elem().FieldByName(name)
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func setResourceIntField(t *testing.T, resource *rns.Resource, name string, value int) {
	t.Helper()
	field := reflect.ValueOf(resource).Elem().FieldByName(name)
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetInt(int64(value))
}

func setResourceField(t *testing.T, resource *rns.Resource, name string, value any) {
	t.Helper()
	field := reflect.ValueOf(resource).Elem().FieldByName(name)
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func invokeResourceProgressCallback(t *testing.T, resource *rns.Resource) {
	t.Helper()
	field := reflect.ValueOf(resource).Elem().FieldByName("progressCallback")
	callback := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(func(*rns.Resource))
	callback(resource)
}

func invokeResourceCallback(t *testing.T, resource *rns.Resource) {
	t.Helper()
	field := reflect.ValueOf(resource).Elem().FieldByName("callback")
	callback := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Interface().(func(*rns.Resource))
	callback(resource)
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

func TestEnablePropagationActivatesStaticPeersAndRequestsPath(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteHash := bytes.Repeat([]byte{0x55}, rns.TruncatedHashLength/8)
	router.staticPeers[string(remoteHash)] = struct{}{}

	var requested [][]byte
	router.requestPath = func(destinationHash []byte) error {
		requested = append(requested, append([]byte{}, destinationHash...))
		return nil
	}

	router.EnablePropagation()

	peer := router.peers[string(remoteHash)]
	if peer == nil {
		t.Fatal("expected static peer to be activated during EnablePropagation")
	}
	if peer.lastHeard != 0 {
		t.Fatalf("lastHeard=%v want=0", peer.lastHeard)
	}
	if len(requested) != 1 || !bytes.Equal(requested[0], remoteHash) {
		t.Fatalf("requested paths=%x want [%x]", requested, remoteHash)
	}
}

func TestEnablePropagationRequestsPathForPersistedUnheardStaticPeer(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	remoteIdentity := mustTestNewIdentity(t, true)
	remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	ts.Remember(nil, remoteHash, remoteIdentity.GetPublicKey(), nil)

	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.EnablePropagation()
	router.staticPeers[string(remoteHash)] = struct{}{}
	router.peers[string(remoteHash)] = NewPeer(router, remoteHash)
	if err := router.SavePeers(); err != nil {
		t.Fatalf("SavePeers(): %v", err)
	}

	recovered := mustTestNewRouter(t, ts, nil, tmpDir)
	recovered.staticPeers[string(remoteHash)] = struct{}{}

	var requested [][]byte
	recovered.requestPath = func(destinationHash []byte) error {
		requested = append(requested, append([]byte{}, destinationHash...))
		return nil
	}

	recovered.EnablePropagation()

	peer := recovered.peers[string(remoteHash)]
	if peer == nil {
		t.Fatal("expected persisted static peer to be rebuilt")
	}
	if peer.lastHeard != 0 {
		t.Fatalf("lastHeard=%v want=0", peer.lastHeard)
	}
	if len(requested) != 1 || !bytes.Equal(requested[0], remoteHash) {
		t.Fatalf("requested paths=%x want [%x]", requested, remoteHash)
	}
}

func TestEnablePropagationLeavesRouterDisabledOnCorruptPeersFile(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if err := os.WriteFile(router.peersPath(), []byte{0xc1}, 0o644); err != nil {
		t.Fatalf("WriteFile(peers): %v", err)
	}

	router.EnablePropagation()

	if router.PropagationEnabled() {
		t.Fatal("propagation should remain disabled when peers file is corrupt")
	}
}
