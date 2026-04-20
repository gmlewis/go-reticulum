// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestPeerRoundTrip(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	peerHash := bytes.Repeat([]byte{0x11}, rns.TruncatedHashLength/8)
	handledID := []byte("handled")
	unhandledID := []byte("unhandled")
	router.propagationEntries[string(handledID)] = &propagationEntry{}
	router.propagationEntries[string(unhandledID)] = &propagationEntry{}

	transferLimit := 123.5
	syncLimit := 456
	stampCost := 7
	stampFlex := 8
	peeringCost := 3

	peer := NewPeer(router, peerHash)
	peer.peeringTimebase = 1.25
	peer.alive = true
	peer.metadata = map[string]any{"name": "peer"}
	peer.lastHeard = 2.5
	peer.syncStrategy = PeerStrategyLazy
	peer.peeringKey = []any{[]byte("key"), 3}
	peer.linkEstablishmentRate = 4.5
	peer.syncTransferRate = 5.5
	peer.propagationTransferLimit = &transferLimit
	peer.propagationSyncLimit = &syncLimit
	peer.propagationStampCost = &stampCost
	peer.propagationStampCostFlexibility = &stampFlex
	peer.peeringCost = &peeringCost
	peer.lastSyncAttempt = 6.5
	peer.offered = 9
	peer.outgoing = 10
	peer.incoming = 11
	peer.rxBytes = 12
	peer.txBytes = 13
	peer.addHandledMessage(handledID)
	peer.addUnhandledMessage(unhandledID)

	peerBytes, err := peer.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes() error = %v", err)
	}

	loaded, err := router.PeerFromBytes(peerBytes)
	if err != nil {
		t.Fatalf("PeerFromBytes() error = %v", err)
	}

	if !bytes.Equal(loaded.destinationHash, peerHash) {
		t.Fatalf("destinationHash = %x, want %x", loaded.destinationHash, peerHash)
	}
	if loaded.peeringTimebase != 1.25 {
		t.Fatalf("peeringTimebase = %v, want 1.25", loaded.peeringTimebase)
	}
	if !loaded.alive {
		t.Fatal("expected loaded peer to be alive")
	}
	if loaded.lastHeard != 2.5 {
		t.Fatalf("lastHeard = %v, want 2.5", loaded.lastHeard)
	}
	if loaded.syncStrategy != PeerStrategyLazy {
		t.Fatalf("syncStrategy = %v, want %v", loaded.syncStrategy, PeerStrategyLazy)
	}
	if value := loaded.PeeringKeyValue(); value == nil || *value != 3 {
		t.Fatalf("PeeringKeyValue() = %v, want 3", value)
	}
	if loaded.linkEstablishmentRate != 4.5 {
		t.Fatalf("linkEstablishmentRate = %v, want 4.5", loaded.linkEstablishmentRate)
	}
	if loaded.syncTransferRate != 5.5 {
		t.Fatalf("syncTransferRate = %v, want 5.5", loaded.syncTransferRate)
	}
	if loaded.propagationTransferLimit == nil || *loaded.propagationTransferLimit != transferLimit {
		t.Fatalf("propagationTransferLimit = %v, want %v", loaded.propagationTransferLimit, transferLimit)
	}
	if loaded.propagationSyncLimit == nil || *loaded.propagationSyncLimit != syncLimit {
		t.Fatalf("propagationSyncLimit = %v, want %v", loaded.propagationSyncLimit, syncLimit)
	}
	if loaded.propagationStampCost == nil || *loaded.propagationStampCost != stampCost {
		t.Fatalf("propagationStampCost = %v, want %v", loaded.propagationStampCost, stampCost)
	}
	if loaded.propagationStampCostFlexibility == nil || *loaded.propagationStampCostFlexibility != stampFlex {
		t.Fatalf("propagationStampCostFlexibility = %v, want %v", loaded.propagationStampCostFlexibility, stampFlex)
	}
	if loaded.peeringCost == nil || *loaded.peeringCost != peeringCost {
		t.Fatalf("peeringCost = %v, want %v", loaded.peeringCost, peeringCost)
	}
	if loaded.lastSyncAttempt != 6.5 {
		t.Fatalf("lastSyncAttempt = %v, want 6.5", loaded.lastSyncAttempt)
	}
	if loaded.offered != 9 || loaded.outgoing != 10 || loaded.incoming != 11 {
		t.Fatalf("counters = (%v,%v,%v), want (9,10,11)", loaded.offered, loaded.outgoing, loaded.incoming)
	}
	if loaded.rxBytes != 12 || loaded.txBytes != 13 {
		t.Fatalf("byte counters = (%v,%v), want (12,13)", loaded.rxBytes, loaded.txBytes)
	}
	if got := loaded.HandledMessageCount(); got != 1 {
		t.Fatalf("HandledMessageCount() = %v, want 1", got)
	}
	if got := loaded.UnhandledMessageCount(); got != 1 {
		t.Fatalf("UnhandledMessageCount() = %v, want 1", got)
	}
}

func TestPeerFromBytesDefaults(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	peerHash := bytes.Repeat([]byte{0x22}, rns.TruncatedHashLength/8)
	handledID := []byte("handled-existing")
	unhandledID := []byte("unhandled-existing")
	router.propagationEntries[string(handledID)] = &propagationEntry{}
	router.propagationEntries[string(unhandledID)] = &propagationEntry{}

	peerBytes, err := msgpack.Pack(map[string]any{
		"destination_hash":                   peerHash,
		"peering_timebase":                   7.0,
		"alive":                              false,
		"last_heard":                         8.0,
		"sync_strategy":                      "bad",
		"propagation_transfer_limit":         "bad",
		"propagation_sync_limit":             "bad",
		"propagation_stamp_cost":             "bad",
		"propagation_stamp_cost_flexibility": "bad",
		"peering_cost":                       "bad",
		"handled_ids":                        []any{[]byte("missing"), handledID},
		"unhandled_ids":                      []any{[]byte("missing"), unhandledID},
	})
	if err != nil {
		t.Fatalf("msgpack.Pack() error = %v", err)
	}

	peer, err := router.PeerFromBytes(peerBytes)
	if err != nil {
		t.Fatalf("PeerFromBytes() error = %v", err)
	}

	if peer.syncStrategy != DefaultPeerSyncStrategy {
		t.Fatalf("syncStrategy = %v, want default %v", peer.syncStrategy, DefaultPeerSyncStrategy)
	}
	if peer.linkEstablishmentRate != 0 || peer.syncTransferRate != 0 {
		t.Fatalf("rates = (%v,%v), want zero defaults", peer.linkEstablishmentRate, peer.syncTransferRate)
	}
	if peer.propagationTransferLimit != nil || peer.propagationSyncLimit != nil {
		t.Fatalf("limits = (%v,%v), want nil defaults", peer.propagationTransferLimit, peer.propagationSyncLimit)
	}
	if peer.propagationStampCost != nil || peer.propagationStampCostFlexibility != nil || peer.peeringCost != nil {
		t.Fatalf("optional costs should default nil, got (%v,%v,%v)", peer.propagationStampCost, peer.propagationStampCostFlexibility, peer.peeringCost)
	}
	if peer.offered != 0 || peer.outgoing != 0 || peer.incoming != 0 || peer.rxBytes != 0 || peer.txBytes != 0 {
		t.Fatalf("expected zero default counters, got (%v,%v,%v,%v,%v)", peer.offered, peer.outgoing, peer.incoming, peer.rxBytes, peer.txBytes)
	}
	if got := peer.HandledMessageCount(); got != 1 {
		t.Fatalf("HandledMessageCount() = %v, want 1", got)
	}
	if got := peer.UnhandledMessageCount(); got != 1 {
		t.Fatalf("UnhandledMessageCount() = %v, want 1", got)
	}
}

func TestPeerQueueProcessingAndPeeringKey(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	peerHash := bytes.Repeat([]byte{0x33}, rns.TruncatedHashLength/8)
	handledID := []byte("handled-q")
	unhandledID := []byte("unhandled-q")
	router.propagationEntries[string(handledID)] = &propagationEntry{}
	router.propagationEntries[string(unhandledID)] = &propagationEntry{}

	peer := NewPeer(router, peerHash)
	peer.QueueUnhandledMessage(unhandledID)
	peer.ProcessQueues()
	if got := peer.UnhandledMessageCount(); got != 1 {
		t.Fatalf("UnhandledMessageCount() after queue = %v, want 1", got)
	}

	peer.QueueHandledMessage(unhandledID)
	peer.QueueHandledMessage(handledID)
	peer.ProcessQueues()
	if got := peer.UnhandledMessageCount(); got != 0 {
		t.Fatalf("UnhandledMessageCount() after handled move = %v, want 0", got)
	}
	if got := peer.HandledMessageCount(); got != 2 {
		t.Fatalf("HandledMessageCount() after handled move = %v, want 2", got)
	}
	if rate := peer.AcceptanceRate(); rate != 0 {
		t.Fatalf("AcceptanceRate() with zero offered = %v, want 0", rate)
	}

	offered := 4
	outgoing := 3
	peer.offered = offered
	peer.outgoing = outgoing
	if rate := peer.AcceptanceRate(); rate != 0.75 {
		t.Fatalf("AcceptanceRate() = %v, want 0.75", rate)
	}

	peeringCost := 3
	peer.peeringCost = &peeringCost
	peer.peeringKey = []any{[]byte("short"), 2}
	if peer.PeeringKeyReady() {
		t.Fatal("PeeringKeyReady() unexpectedly accepted insufficient key value")
	}
	if peer.peeringKey != nil {
		t.Fatal("PeeringKeyReady() should clear insufficient peering keys")
	}

	peer.peeringKey = []any{[]byte("good"), 3}
	if !peer.PeeringKeyReady() {
		t.Fatal("PeeringKeyReady() rejected matching peering key value")
	}
}
