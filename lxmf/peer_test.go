// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestPeerRoundTrip(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
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
	peer.metadata = map[any]any{"name": "peer"}
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
	tmpDir := testutils.TempDir(t, tempDirPrefix)
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
	tmpDir := testutils.TempDir(t, tempDirPrefix)
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

func TestPeerSyncPreconditions(t *testing.T) {
	t.Parallel()

	destHash := make([]byte, 16)
	ts := rns.NewTransportSystem(nil)
	id, _ := rns.NewIdentity(false, nil)
	dir := testutils.TempDir(t, "lxmf-peer-sync-")
	router, err := NewRouter(ts, id, dir)
	if err != nil {
		t.Fatal(err)
	}
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	fixedNow := time.Unix(1000, 0)

	tests := []struct {
		name            string
		nextSyncAttempt float64
		stampCost       *int
		stampCostFlex   *int
		peeringCost     *int
		peeringKey      []any
		wantPostpone    string
		wantSyncHook    bool
	}{
		{
			name:            "sync_time_not_reached",
			nextSyncAttempt: peerTime(time.Unix(2000, 0)),
			stampCost:       new(1),
			stampCostFlex:   new(2),
			peeringCost:     new(3),
			peeringKey:      []any{[]byte("key"), 3},
			wantPostpone:    "due to previous failures",
		},
		{
			name:            "stamp_costs_not_known",
			nextSyncAttempt: 0,
			stampCost:       nil,
			wantPostpone:    "stamp costs are not yet known",
		},
		{
			name:            "peering_key_not_ready",
			nextSyncAttempt: 0,
			stampCost:       new(1),
			stampCostFlex:   new(2),
			peeringCost:     new(3),
			peeringKey:      nil,
			wantPostpone:    "peering key has not been generated",
		},
		{
			name:            "all_preconditions_met",
			nextSyncAttempt: 0,
			stampCost:       new(1),
			stampCostFlex:   new(2),
			peeringCost:     new(3),
			peeringKey:      []any{[]byte("key"), 3},
			wantSyncHook:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			peer := NewPeer(router, destHash)
			peer.now = func() time.Time { return fixedNow }
			peer.nextSyncAttempt = tc.nextSyncAttempt
			peer.propagationStampCost = tc.stampCost
			peer.propagationStampCostFlexibility = tc.stampCostFlex
			peer.peeringCost = tc.peeringCost
			peer.peeringKey = tc.peeringKey
			peer.generatePeeringKeyFn = func() {}
			peer.hasPathFn = func([]byte) bool { return true }
			peer.pathRequestSleep = func() {}
			testID, _ := rns.NewIdentity(false, nil)
			peer.identity = testID
			testDest, _ := rns.NewDestination(ts, testID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
			peer.destination = testDest
			peer.unhandledMessagesFn = func() [][]byte { return [][]byte{[]byte("msg1")} }

			var postponeReason string
			var syncHookCalled bool
			peer.syncPostponeHook = func(reason string) { postponeReason = reason }
			peer.syncHook = func() { syncHookCalled = true }

			peer.Sync()

			if tc.wantPostpone != "" {
				if postponeReason == "" {
					t.Fatalf("expected postpone reason containing %q, got none", tc.wantPostpone)
				}
				if !bytes.Contains([]byte(postponeReason), []byte(tc.wantPostpone)) {
					t.Fatalf("postpone reason %q does not contain %q", postponeReason, tc.wantPostpone)
				}
			} else {
				if postponeReason != "" {
					t.Fatalf("unexpected postpone reason: %q", postponeReason)
				}
			}
			if tc.wantSyncHook && !syncHookCalled {
				t.Fatal("expected sync to proceed past preconditions but it was postponed")
			}
		})
	}
}

func TestPeerSyncIdentityRecall(t *testing.T) {
	t.Parallel()

	destHash := make([]byte, 16)
	destHash[0] = 0xBB
	ts := rns.NewTransportSystem(nil)
	id, _ := rns.NewIdentity(false, nil)
	dir := testutils.TempDir(t, "lxmf-peer-idrecall-")
	router, err := NewRouter(ts, id, dir)
	if err != nil {
		t.Fatal(err)
	}
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	fixedNow := time.Unix(1000, 0)

	newPeerWithPreconditions := func() *Peer {
		peer := NewPeer(router, destHash)
		peer.now = func() time.Time { return fixedNow }
		peer.nextSyncAttempt = 0
		peer.propagationStampCost = new(1)
		peer.propagationStampCostFlexibility = new(2)
		peer.peeringCost = new(3)
		peer.peeringKey = []any{[]byte("key"), 3}
		peer.generatePeeringKeyFn = func() {}
		peer.hasPathFn = func([]byte) bool { return true }
		peer.pathRequestSleep = func() {}
		testID, _ := rns.NewIdentity(false, nil)
		peer.identity = testID
		testDest, _ := rns.NewDestination(ts, testID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		peer.destination = testDest
		peer.unhandledMessagesFn = func() [][]byte { return [][]byte{[]byte("msg1")} }
		return peer
	}

	t.Run("identity_already_known", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		existingID, _ := rns.NewIdentity(false, nil)
		peer.identity = existingID
		existingDest, _ := rns.NewDestination(ts, existingID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		peer.destination = existingDest

		var recallCalled bool
		peer.recallIdentityFn = func([]byte) *rns.Identity { recallCalled = true; return nil }

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if recallCalled {
			t.Fatal("RecallIdentity should not be called when identity already known")
		}
		if !syncHookCalled {
			t.Fatal("sync should have proceeded past identity recall")
		}
	})

	t.Run("identity_recalled_successfully", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		peer.identity = nil
		peer.destination = nil

		recalledID, _ := rns.NewIdentity(false, nil)
		var recallCalled bool
		peer.recallIdentityFn = func([]byte) *rns.Identity {
			recallCalled = true
			return recalledID
		}
		var newDestCalled bool
		peer.newDestinationFn = func(identity *rns.Identity) (*rns.Destination, error) {
			newDestCalled = true
			if identity != recalledID {
				t.Fatal("NewDestination called with wrong identity")
			}
			dst, _ := rns.NewDestination(ts, identity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
			return dst, nil
		}

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if !recallCalled {
			t.Fatal("RecallIdentity should have been called")
		}
		if !newDestCalled {
			t.Fatal("NewDestination should have been called")
		}
		if !syncHookCalled {
			t.Fatal("sync should have proceeded past identity recall")
		}
		if peer.identity != recalledID {
			t.Fatal("peer identity should be the recalled identity")
		}
	})

	t.Run("identity_recall_fails", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		peer.identity = nil
		peer.destination = nil

		peer.recallIdentityFn = func([]byte) *rns.Identity { return nil }

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if syncHookCalled {
			t.Fatal("sync should NOT proceed when identity recall fails and destination is nil")
		}
	})

	t.Run("identity_recalled_but_destination_creation_fails", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		peer.identity = nil
		peer.destination = nil

		recalledID, _ := rns.NewIdentity(false, nil)
		peer.recallIdentityFn = func([]byte) *rns.Identity { return recalledID }
		peer.newDestinationFn = func(_ *rns.Identity) (*rns.Destination, error) {
			return nil, fmt.Errorf("destination creation failed")
		}

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if syncHookCalled {
			t.Fatal("sync should NOT proceed when destination creation fails")
		}
		if peer.destination != nil {
			t.Fatal("destination should remain nil when creation fails")
		}
	})
}

func TestPeerSyncPathRequest(t *testing.T) {
	t.Parallel()

	destHash := make([]byte, 16)
	destHash[0] = 0xAA
	ts := rns.NewTransportSystem(nil)
	id, _ := rns.NewIdentity(false, nil)
	dir := testutils.TempDir(t, "lxmf-peer-pathreq-")
	router, err := NewRouter(ts, id, dir)
	if err != nil {
		t.Fatal(err)
	}
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	fixedNow := time.Unix(1000, 0)

	newPeerWithPreconditions := func() *Peer {
		peer := NewPeer(router, destHash)
		peer.now = func() time.Time { return fixedNow }
		peer.nextSyncAttempt = 0
		peer.propagationStampCost = new(1)
		peer.propagationStampCostFlexibility = new(2)
		peer.peeringCost = new(3)
		peer.peeringKey = []any{[]byte("key"), 3}
		peer.generatePeeringKeyFn = func() {}
		peer.pathRequestSleep = func() {}
		testID, _ := rns.NewIdentity(false, nil)
		peer.identity = testID
		testDest, _ := rns.NewDestination(ts, testID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		peer.destination = testDest
		peer.unhandledMessagesFn = func() [][]byte { return [][]byte{[]byte("msg1")} }
		return peer
	}

	t.Run("path_already_exists", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		var requestPathCalled bool
		peer.hasPathFn = func([]byte) bool { return true }
		peer.requestPathFn = func([]byte) error { requestPathCalled = true; return nil }

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if requestPathCalled {
			t.Fatal("RequestPath should not have been called when path already exists")
		}
		if !syncHookCalled {
			t.Fatal("sync should have proceeded past path request")
		}
	})

	t.Run("path_requested_and_becomes_available", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		var requestPathCalled bool
		hasPath := false
		peer.hasPathFn = func([]byte) bool { return hasPath }
		peer.requestPathFn = func([]byte) error {
			requestPathCalled = true
			hasPath = true
			return nil
		}

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if !requestPathCalled {
			t.Fatal("RequestPath should have been called when no path exists")
		}
		if !syncHookCalled {
			t.Fatal("sync should have proceeded after path became available")
		}
	})

	t.Run("path_requested_but_still_unavailable", func(t *testing.T) {
		t.Parallel()
		peer := newPeerWithPreconditions()
		var requestPathCalled bool
		peer.hasPathFn = func([]byte) bool { return false }
		peer.requestPathFn = func([]byte) error { requestPathCalled = true; return nil }

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if !requestPathCalled {
			t.Fatal("RequestPath should have been called")
		}
		if syncHookCalled {
			t.Fatal("sync should NOT have proceeded when path is still unavailable")
		}
	})
}

func TestPeerSyncOfferRequest(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	id, _ := rns.NewIdentity(false, nil)
	dir := testutils.TempDir(t, "lxmf-peer-offer-")
	router, err := NewRouter(ts, id, dir)
	if err != nil {
		t.Fatal(err)
	}
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	fixedNow := time.Unix(1000, 0)

	newPeerWithAllPreconditions := func() *Peer {
		destHash := make([]byte, 16)
		rand.Read(destHash)
		peer := NewPeer(router, destHash)
		peer.now = func() time.Time { return fixedNow }
		peer.nextSyncAttempt = 0
		peer.propagationStampCost = new(1)
		peer.propagationStampCostFlexibility = new(2)
		peer.peeringCost = new(3)
		peer.peeringKey = []any{[]byte("key"), 3}
		peer.generatePeeringKeyFn = func() {}
		peer.hasPathFn = func([]byte) bool { return true }
		peer.pathRequestSleep = func() {}
		testID, _ := rns.NewIdentity(false, nil)
		peer.identity = testID
		testDest, _ := rns.NewDestination(ts, testID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		peer.destination = testDest
		peer.unhandledMessagesFn = func() [][]byte { return [][]byte{[]byte("msg1")} }
		return peer
	}

	t.Run("currently_transferring_returns_early", func(t *testing.T) {
		peer := newPeerWithAllPreconditions()
		peer.currentlyTransferringMessages = [][]byte{[]byte("transferring")}
		peer.state = PeerStateIdle

		var establishLinkCalled bool
		peer.establishLinkFn = func() { establishLinkCalled = true }

		peer.Sync()

		if establishLinkCalled {
			t.Fatal("should not establish link when currently transferring")
		}
		if peer.state != PeerStateIdle {
			t.Fatalf("state should remain IDLE, got %v", peer.state)
		}
	})

	t.Run("idle_state_establishes_link", func(t *testing.T) {
		peer := newPeerWithAllPreconditions()
		peer.state = PeerStateIdle
		peer.syncBackoff = 0

		var establishLinkCalled bool
		peer.establishLinkFn = func() { establishLinkCalled = true }

		peer.Sync()

		if !establishLinkCalled {
			t.Fatal("should have called establishLinkFn when state is IDLE")
		}
		if peer.state != PeerStateLinkEstablishing {
			t.Fatalf("state should be LINK_ESTABLISHING, got %v", peer.state)
		}
		if peer.syncBackoff != PeerSyncBackoffStep {
			t.Fatalf("syncBackoff should be %v, got %v", PeerSyncBackoffStep, peer.syncBackoff)
		}
		wantNextSync := peerTime(fixedNow) + PeerSyncBackoffStep
		if peer.nextSyncAttempt != wantNextSync {
			t.Fatalf("nextSyncAttempt should be %v, got %v", wantNextSync, peer.nextSyncAttempt)
		}
	})

	t.Run("non_idle_non_link_ready_state_does_nothing", func(t *testing.T) {
		peer := newPeerWithAllPreconditions()
		peer.state = PeerStateRequestSent

		var establishLinkCalled bool
		peer.establishLinkFn = func() { establishLinkCalled = true }

		peer.Sync()

		if establishLinkCalled {
			t.Fatal("should not establish link when state is REQUEST_SENT")
		}
		if peer.state != PeerStateRequestSent {
			t.Fatalf("state should remain REQUEST_SENT, got %v", peer.state)
		}
	})
}

func TestPeerSyncNoUnhandled(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	id, _ := rns.NewIdentity(false, nil)
	dir := testutils.TempDir(t, "lxmf-peer-nounh-")
	router, err := NewRouter(ts, id, dir)
	if err != nil {
		t.Fatal(err)
	}
	router.stopJobLoop()
	t.Cleanup(func() { _ = router.Close() })
	fixedNow := time.Unix(1000, 0)

	newPeerWithAllPreconditions := func() *Peer {
		destHash := make([]byte, 16)
		rand.Read(destHash)
		peer := NewPeer(router, destHash)
		peer.now = func() time.Time { return fixedNow }
		peer.nextSyncAttempt = 0
		peer.propagationStampCost = new(1)
		peer.propagationStampCostFlexibility = new(2)
		peer.peeringCost = new(3)
		peer.peeringKey = []any{[]byte("key"), 3}
		peer.generatePeeringKeyFn = func() {}
		peer.hasPathFn = func([]byte) bool { return true }
		peer.pathRequestSleep = func() {}
		testID, _ := rns.NewIdentity(false, nil)
		peer.identity = testID
		testDest, _ := rns.NewDestination(ts, testID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		peer.destination = testDest
		return peer
	}

	t.Run("no_unhandled_returns_early", func(t *testing.T) {
		peer := newPeerWithAllPreconditions()
		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if syncHookCalled {
			t.Fatal("sync should return early when no unhandled messages, not reach syncHook")
		}
	})

	t.Run("unhandled_messages_proceeds", func(t *testing.T) {
		peer := newPeerWithAllPreconditions()

		transientID := []byte("test-transient-id-1234")
		router.propagationEntries[string(transientID)] = &propagationEntry{
			unhandledBy: [][]byte{peer.destinationHash},
		}

		var syncHookCalled bool
		peer.syncHook = func() { syncHookCalled = true }

		peer.Sync()

		if !syncHookCalled {
			t.Fatal("sync should proceed past no-unhandled check when unhandled messages exist")
		}
	})
}

// noopRequestLink is a test seam for Peer.requestLinkFn that swallows the
// outbound offer request without touching a link. Tests that exercise the
// LINK_READY offer-preparation but do not care about the send use this to
// avoid needing a live rns.Link.
func noopRequestLink(*rns.Link, string, any, func(*rns.RequestReceipt), func(*rns.RequestReceipt), func(*rns.RequestReceipt), time.Duration) (*rns.RequestReceipt, error) {
	return nil, nil
}

// TestPeerSyncLinkReadyDropsPurgedAndCollectsEntries covers 24.B.1: the
// Peer.Sync LINK_READY branch collects an [transient_id, weight, size] entry
// for every unhandled transient ID that still exists in the propagation store,
// and drops (removeUnhandledMessage) any that have been purged. A purged ID is
// not collected; the surviving IDs are collected with their stored size and a
// weight derived from get_weight (LXMPeer.py:344-366).
func TestPeerSyncLinkReadyDropsPurgedAndCollectsEntries(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-linkready-"))

	// Fixed clock so getWeightLocked is deterministic: with receivedAt == now
	// the age weight is max(1, 0) = 1, and no prioritised entry means the
	// priority weight is 1, so weight == size.
	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0xCC
	peer := NewPeer(router, destHash)
	peer.state = PeerStateLinkReady
	peer.now = func() time.Time { return fixedNow }
	peer.nextSyncAttempt = 0
	peer.propagationStampCost = new(int)
	peer.propagationStampCostFlexibility = new(int)
	peer.peeringCost = new(int)
	peer.peeringKey = []any{[]byte("peering-key"), 3}
	peer.generatePeeringKeyFn = func() {}
	peer.hasPathFn = func([]byte) bool { return true }
	peer.pathRequestSleep = func() {}
	id := mustTestNewIdentity(t, false)
	peer.identity = id
	peer.destination = mustTestNewDestination(t, ts, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	tidA := []byte("tid-a")
	tidB := []byte("tid-b")
	tidPurged := []byte("tid-purged")
	router.propagationEntries[string(tidA)] = &propagationEntry{size: 100, receivedAt: fixedNow}
	router.propagationEntries[string(tidB)] = &propagationEntry{size: 200, receivedAt: fixedNow}

	peer.unhandledMessagesFn = func() [][]byte { return [][]byte{tidA, tidPurged, tidB} }
	peer.requestLinkFn = noopRequestLink

	peer.Sync()

	got := peer.pendingOfferEntries
	if len(got) != 2 {
		t.Fatalf("pendingOfferEntries len = %d, want 2 (purged ID dropped)", len(got))
	}
	byID := map[string]pendingOfferEntry{}
	for _, e := range got {
		byID[string(e.transientID)] = e
	}
	if _, ok := byID[string(tidPurged)]; ok {
		t.Fatal("purged transient ID was collected into pendingOfferEntries")
	}
	eA, okA := byID[string(tidA)]
	if !okA {
		t.Fatal("tidA missing from pendingOfferEntries")
	}
	if eA.size != 100 {
		t.Errorf("tidA size = %d, want 100", eA.size)
	}
	if eA.weight != 100 {
		t.Errorf("tidA weight = %v, want 100 (ageWeight=1, priorityWeight=1, size=100)", eA.weight)
	}
	eB, okB := byID[string(tidB)]
	if !okB {
		t.Fatal("tidB missing from pendingOfferEntries")
	}
	if eB.size != 200 {
		t.Errorf("tidB size = %d, want 200", eB.size)
	}
	if eB.weight != 200 {
		t.Errorf("tidB weight = %v, want 200 (ageWeight=1, priorityWeight=1, size=200)", eB.weight)
	}
}

// TestPeerSyncLinkReadyDropsLowStampValueMessages covers 24.B.2: the
// Peer.Sync LINK_READY branch drops unhandled messages whose stamp value is
// below the minimum accepted cost (max(0, propagation_stamp_cost -
// propagation_stamp_cost_flexibility)) via removeUnhandledMessage, and retains
// the rest in pendingOfferEntries (LXMPeer.py:347-348, 4a93697). A dropped
// entry is removed from the peer's unhandledBy set; a retained entry is not.
func TestPeerSyncLinkReadyDropsLowStampValueMessages(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-lowstamp-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0xDD
	peer := NewPeer(router, destHash)
	peer.state = PeerStateLinkReady
	peer.now = func() time.Time { return fixedNow }
	peer.nextSyncAttempt = 0
	// propagationStampCost=10, flexibility=3 -> minAcceptedCost = max(0, 7) = 7.
	stampCost, stampFlex := 10, 3
	peer.propagationStampCost = &stampCost
	peer.propagationStampCostFlexibility = &stampFlex
	peer.peeringCost = new(int)
	peer.peeringKey = []any{[]byte("peering-key"), 3}
	peer.generatePeeringKeyFn = func() {}
	peer.hasPathFn = func([]byte) bool { return true }
	peer.pathRequestSleep = func() {}
	id := mustTestNewIdentity(t, false)
	peer.identity = id
	peer.destination = mustTestNewDestination(t, ts, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	tidLow := []byte("tid-low")   // stampValue 5 < 7 -> dropped
	tidKeep := []byte("tid-keep") // stampValue 8 >= 7 -> retained
	tidEdge := []byte("tid-edge") // stampValue 7 == 7 (not <) -> retained (boundary)
	router.propagationEntries[string(tidLow)] = &propagationEntry{size: 100, receivedAt: fixedNow, stampValue: 5, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidKeep)] = &propagationEntry{size: 200, receivedAt: fixedNow, stampValue: 8, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidEdge)] = &propagationEntry{size: 300, receivedAt: fixedNow, stampValue: 7, unhandledBy: [][]byte{destHash}}

	peer.unhandledMessagesFn = func() [][]byte { return [][]byte{tidLow, tidKeep, tidEdge} }
	peer.requestLinkFn = noopRequestLink

	peer.Sync()

	got := peer.pendingOfferEntries
	if len(got) != 2 {
		t.Fatalf("pendingOfferEntries len = %d, want 2 (low-stamp ID dropped)", len(got))
	}
	byID := map[string]pendingOfferEntry{}
	for _, e := range got {
		byID[string(e.transientID)] = e
	}
	if _, ok := byID[string(tidLow)]; ok {
		t.Fatal("low-stamp-value transient ID was collected into pendingOfferEntries")
	}
	if _, ok := byID[string(tidKeep)]; !ok {
		t.Fatal("tidKeep missing from pendingOfferEntries")
	}
	if _, ok := byID[string(tidEdge)]; !ok {
		t.Fatal("tidEdge (boundary, stampValue == minAcceptedCost) missing from pendingOfferEntries")
	}

	// removeUnhandledMessage was called for tidLow, so the peer must no longer
	// be in its unhandledBy set; retained entries keep the peer.
	lowEntry := router.propagationEntries[string(tidLow)]
	if containsByteSlice(lowEntry.unhandledBy, destHash) {
		t.Error("tidLow still lists the peer in unhandledBy after Sync, want it removed (removeUnhandledMessage called)")
	}
	keepEntry := router.propagationEntries[string(tidKeep)]
	if !containsByteSlice(keepEntry.unhandledBy, destHash) {
		t.Error("tidKeep lost the peer from unhandledBy after Sync, want it retained")
	}
	edgeEntry := router.propagationEntries[string(tidEdge)]
	if !containsByteSlice(edgeEntry.unhandledBy, destHash) {
		t.Error("tidEdge lost the peer from unhandledBy after Sync, want it retained")
	}
}

// TestPeerSyncLinkReadyAppliesTransferAndSyncSizeLimits covers 24.B.3: the
// Peer.Sync LINK_READY branch sorts unhandled entries by weight ascending, then
// drops + marks handled any single message whose transfer size (size+16)
// exceeds propagation_transfer_limit*1000, and stops adding IDs once the
// cumulative size reaches propagation_sync_limit*1000 (LXMPeer.py:367-381).
func TestPeerSyncLinkReadyAppliesTransferAndSyncSizeLimits(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-sizelimit-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0xEE
	peer := NewPeer(router, destHash)
	peer.state = PeerStateLinkReady
	peer.now = func() time.Time { return fixedNow }
	peer.nextSyncAttempt = 0
	peer.propagationStampCost = new(int)
	peer.propagationStampCostFlexibility = new(int)
	peer.peeringCost = new(int)
	// transfer_limit=0.5 (KB) -> 500 bytes: any single transfer > 500 is dropped.
	transferLimit := 0.5
	peer.propagationTransferLimit = &transferLimit
	// sync_limit=1 (KB) -> 1000 bytes: stop adding once next_size >= 1000.
	syncLimit := 1
	peer.propagationSyncLimit = &syncLimit
	peer.peeringKey = []any{[]byte("peering-key"), 3}
	peer.generatePeeringKeyFn = func() {}
	peer.hasPathFn = func([]byte) bool { return true }
	peer.pathRequestSleep = func() {}
	id := mustTestNewIdentity(t, false)
	peer.identity = id
	peer.destination = mustTestNewDestination(t, ts, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	// Entries are [transientID, weight, size]. With fixedNow==receivedAt the
	// weight equals size (ageWeight=1, priorityWeight=1).
	//   E: weight=50,  size=600 -> sorted first; transfer_size=616 > 500 -> dropped + handled.
	//   A: weight=100, size=100 -> transfer_size=116; next=24+116=140   < 1000 -> offered, cum=140.
	//   B: weight=200, size=200 -> transfer_size=216; next=140+216=356  < 1000 -> offered, cum=356.
	//   C: weight=300, size=300 -> transfer_size=316; next=356+316=672  < 1000 -> offered, cum=672.
	//   D: weight=400, size=400 -> transfer_size=416; next=672+416=1088 >= 1000 -> skipped (sync), not handled.
	tidE := []byte("tid-E")
	tidA := []byte("tid-A")
	tidB := []byte("tid-B")
	tidC := []byte("tid-C")
	tidD := []byte("tid-D")
	router.propagationEntries[string(tidE)] = &propagationEntry{size: 600, receivedAt: fixedNow, stampValue: 100, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidA)] = &propagationEntry{size: 100, receivedAt: fixedNow, stampValue: 100, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidB)] = &propagationEntry{size: 200, receivedAt: fixedNow, stampValue: 100, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidC)] = &propagationEntry{size: 300, receivedAt: fixedNow, stampValue: 100, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidD)] = &propagationEntry{size: 400, receivedAt: fixedNow, stampValue: 100, unhandledBy: [][]byte{destHash}}

	peer.unhandledMessagesFn = func() [][]byte { return [][]byte{tidA, tidB, tidC, tidD, tidE} }
	peer.requestLinkFn = noopRequestLink

	peer.Sync()

	got := peer.pendingOfferIDs
	// Offered list must be ordered by weight ascending: A, B, C.
	wantOffer := [][]byte{tidA, tidB, tidC}
	if len(got) != len(wantOffer) {
		t.Fatalf("pendingOfferIDs len = %d, want %d\n got=%v\nwant=%v", len(got), len(wantOffer), got, wantOffer)
	}
	for i, want := range wantOffer {
		if !bytes.Equal(got[i], want) {
			t.Errorf("pendingOfferIDs[%d] = %v, want %v (weight-ascending order)", i, got[i], want)
		}
	}

	// E was dropped by the transfer limit and marked handled: peer in
	// handledBy, removed from unhandledBy, and absent from the offer.
	entryE := router.propagationEntries[string(tidE)]
	if !containsByteSlice(entryE.handledBy, destHash) {
		t.Error("tidE (oversized) not in handledBy after Sync, want it marked handled")
	}
	if containsByteSlice(entryE.unhandledBy, destHash) {
		t.Error("tidE still in unhandledBy after Sync, want it removed (dropped by transfer limit)")
	}

	// D was skipped by the sync limit (cumulative reached the cap): NOT
	// handled, NOT removed — still pending for a future sync.
	entryD := router.propagationEntries[string(tidD)]
	if containsByteSlice(entryD.handledBy, destHash) {
		t.Error("tidD was marked handled after Sync, want it left unhandled (sync-limit skip is not a drop)")
	}
	if !containsByteSlice(entryD.unhandledBy, destHash) {
		t.Error("tidD lost unhandledBy after Sync, want it retained (sync-limit skip is not a drop)")
	}

	// A/B/C were offered: still unhandled (not yet handled), retained in the
	// store with the peer in unhandledBy.
	for _, tid := range [][]byte{tidA, tidB, tidC} {
		e := router.propagationEntries[string(tid)]
		if containsByteSlice(e.handledBy, destHash) {
			t.Errorf("%v was marked handled after Sync, want it unhandled (offered, awaiting response)", tid)
		}
		if !containsByteSlice(e.unhandledBy, destHash) {
			t.Errorf("%v lost unhandledBy after Sync, want it retained (offered, awaiting response)", tid)
		}
	}
}

// TestPeerSyncLinkReadyEarlyReturnsWhenNoUnhandledRemain covers 24.B.4: after
// the transfer/sync size-limit filtering, if no unhandled IDs survive the
// Peer.Sync LINK_READY branch logs "no unhandled messages exist after offer
// preparation" and returns without sending an offer — state stays
// PeerStateLinkReady and lastOffer is not set (1.1.0 delta, 982c9fc,
// LXMPeer.py:383-385).
func TestPeerSyncLinkReadyEarlyReturnsWhenNoUnhandledRemain(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogExtreme)
	logger.SetLogDest(rns.LogCallback)
	var logMu sync.Mutex
	var got []string
	logger.SetLogCallback(func(msg string) {
		logMu.Lock()
		got = append(got, msg)
		logMu.Unlock()
	})

	ts := rns.NewTransportSystem(logger)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-earlyreturn-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0x77
	peer := NewPeer(router, destHash)
	peer.state = PeerStateLinkReady
	peer.now = func() time.Time { return fixedNow }
	peer.nextSyncAttempt = 0
	// propagationStampCost=10, flexibility=3 -> minAcceptedCost=7; the single
	// entry below has stampValue 1 < 7, so it is dropped as low-value and the
	// filtered unhandled ID list is empty.
	stampCost, stampFlex := 10, 3
	peer.propagationStampCost = &stampCost
	peer.propagationStampCostFlexibility = &stampFlex
	peer.peeringCost = new(int)
	peer.peeringKey = []any{[]byte("peering-key"), 3}
	peer.generatePeeringKeyFn = func() {}
	peer.hasPathFn = func([]byte) bool { return true }
	peer.pathRequestSleep = func() {}
	id := mustTestNewIdentity(t, false)
	peer.identity = id
	peer.destination = mustTestNewDestination(t, ts, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	tidOnly := []byte("tid-only")
	router.propagationEntries[string(tidOnly)] = &propagationEntry{size: 100, receivedAt: fixedNow, stampValue: 1, unhandledBy: [][]byte{destHash}}
	peer.unhandledMessagesFn = func() [][]byte { return [][]byte{tidOnly} }

	peer.Sync()

	if peer.state != PeerStateLinkReady {
		t.Errorf("peer state = %d, want %d (PeerStateLinkReady, no offer sent)", peer.state, PeerStateLinkReady)
	}
	if len(peer.lastOffer) != 0 {
		t.Errorf("lastOffer len = %d, want 0 (no offer prepared, early return)", len(peer.lastOffer))
	}
	if len(peer.pendingOfferIDs) != 0 {
		t.Errorf("pendingOfferIDs len = %d, want 0 (all messages filtered out)", len(peer.pendingOfferIDs))
	}

	logMu.Lock()
	found := false
	for _, msg := range got {
		if strings.Contains(msg, "no unhandled messages exist after offer preparation") {
			found = true
			break
		}
	}
	logMu.Unlock()
	if !found {
		t.Errorf("expected a debug log mentioning %q, got %d messages: %v", "no unhandled messages exist after offer preparation", len(got), got)
	}
}

// TestPeerSyncLinkReadySendsOfferRequest covers 24.B.5 (the keystone): the
// Peer.Sync LINK_READY branch builds the offer msgpack [peering_key[0],
// unhandled_ids], records it in lastOffer, sends it over the link via
// OFFER_REQUEST_PATH with the OfferResponse/RequestFailed callbacks, and
// advances state to PeerStateRequestSent (LXMPeer.py:386-389). The offer bytes
// are golden-captured from Python umsgpack for a fixed key and three transient
// IDs, proving Go/Python wire parity of the offer structure.
func TestPeerSyncLinkReadySendsOfferRequest(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-offersend-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0x55
	peer := NewPeer(router, destHash)
	peer.state = PeerStateLinkReady
	peer.now = func() time.Time { return fixedNow }
	peer.nextSyncAttempt = 0
	peer.propagationStampCost = new(int)
	peer.propagationStampCostFlexibility = new(int)
	peer.peeringCost = new(int)
	// peering_key[0] is a 16-byte key (bytes 0..15), matching the golden
	// capture from Python umsgpack.
	peer.peeringKey = []any{[]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}, 3}
	peer.generatePeeringKeyFn = func() {}
	peer.hasPathFn = func([]byte) bool { return true }
	peer.pathRequestSleep = func() {}
	id := mustTestNewIdentity(t, false)
	peer.identity = id
	peer.destination = mustTestNewDestination(t, ts, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	tidA := []byte("tid-A")
	tidB := []byte("tid-B")
	tidC := []byte("tid-C")
	router.propagationEntries[string(tidA)] = &propagationEntry{size: 100, receivedAt: fixedNow, stampValue: 0, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidB)] = &propagationEntry{size: 100, receivedAt: fixedNow, stampValue: 0, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tidC)] = &propagationEntry{size: 100, receivedAt: fixedNow, stampValue: 0, unhandledBy: [][]byte{destHash}}
	peer.unhandledMessagesFn = func() [][]byte { return [][]byte{tidA, tidB, tidC} }

	var (
		gotPath     string
		gotData     any
		gotResponse func(*rns.RequestReceipt)
		gotFailed   func(*rns.RequestReceipt)
	)
	peer.requestLinkFn = func(_ *rns.Link, path string, data any, responseCb, failedCb, _ func(*rns.RequestReceipt), _ time.Duration) (*rns.RequestReceipt, error) {
		gotPath = path
		gotData = data
		gotResponse = responseCb
		gotFailed = failedCb
		return nil, nil
	}

	peer.Sync()

	if gotPath != PeerOfferRequestPath {
		t.Errorf("offer request path = %q, want %q", gotPath, PeerOfferRequestPath)
	}
	if gotResponse == nil {
		t.Error("offer request response callback was nil, want p.OfferResponse")
	}
	if gotFailed == nil {
		t.Error("offer request failed callback was nil, want p.RequestFailed")
	}

	// The captured offer data must pack to the golden bytes from Python
	// umsgpack: [peering_key[0], [tid-A, tid-B, tid-C]].
	wantHex := "92c410000102030405060708090a0b0c0d0e0f93c4057469642d41c4057469642d42c4057469642d43"
	packed, err := msgpack.Pack(gotData)
	if err != nil {
		t.Fatalf("msgpack.Pack(offer) failed: %v", err)
	}
	if gotHex := hex.EncodeToString(packed); gotHex != wantHex {
		t.Errorf("offer msgpack = %s, want %s", gotHex, wantHex)
	}

	// lastOffer records the offered IDs in order.
	if len(peer.lastOffer) != 3 {
		t.Fatalf("lastOffer len = %d, want 3", len(peer.lastOffer))
	}
	for i, want := range [][]byte{tidA, tidB, tidC} {
		if !bytes.Equal(peer.lastOffer[i], want) {
			t.Errorf("lastOffer[%d] = %v, want %v", i, peer.lastOffer[i], want)
		}
	}

	if peer.state != PeerStateRequestSent {
		t.Errorf("peer state = %d, want %d (PeerStateRequestSent)", peer.state, PeerStateRequestSent)
	}
}

// TestPeerOfferResponseErrorNoIdentity covers 24.B.6: when an offer response
// carries ERROR_NO_IDENTITY (0xf0), OfferResponse re-identifies the link with
// the router's identity, resets state to PeerStateLinkReady, and re-enters
// Sync to rebuild and resend the offer (1.1.0 delta, 548be10,
// LXMPeer.py:405-411).
func TestPeerOfferResponseErrorNoIdentity(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-noidentity-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0x66
	peer := NewPeer(router, destHash)
	// OfferResponse sets state to ResponseReceived up top; the NO_IDENTITY
	// path must then reset it to LinkReady and re-enter Sync.
	peer.state = PeerStateResponseReceived
	peer.now = func() time.Time { return fixedNow }
	peer.nextSyncAttempt = 0
	peer.propagationStampCost = new(int)
	peer.propagationStampCostFlexibility = new(int)
	peer.peeringCost = new(int)
	peer.peeringKey = []any{[]byte("peering-key"), 3}
	peer.generatePeeringKeyFn = func() {}
	peer.hasPathFn = func([]byte) bool { return true }
	peer.pathRequestSleep = func() {}
	id := mustTestNewIdentity(t, false)
	peer.identity = id
	peer.destination = mustTestNewDestination(t, ts, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	peer.link = &rns.Link{}
	peer.lastOffer = [][]byte{[]byte("tid-1")}
	// No unhandled messages: the re-entered Sync returns at the no-unhandled
	// check, leaving state at LinkReady (so we can observe the reset).
	peer.unhandledMessagesFn = func() [][]byte { return nil }
	peer.requestLinkFn = noopRequestLink
	// Sentinel so re-entry into Sync is observable via lastSyncAttempt.
	peer.lastSyncAttempt = 0

	var (
		gotLink     *rns.Link
		gotIdentity *rns.Identity
		identifyCnt int
	)
	peer.identifyLinkHook = func(link *rns.Link, identity *rns.Identity) error {
		gotLink = link
		gotIdentity = identity
		identifyCnt++
		return nil
	}

	receipt := &rns.RequestReceipt{}
	receipt.Response = peerErrorNoIdentity

	peer.OfferResponse(receipt)

	if identifyCnt != 1 {
		t.Errorf("identify called %d times, want 1", identifyCnt)
	}
	if gotLink != peer.link {
		t.Error("identify was not called with the peer's link")
	}
	if gotIdentity != router.identity {
		t.Error("identify was not called with router.identity")
	}
	if peer.state != PeerStateLinkReady {
		t.Errorf("peer state = %d, want %d (PeerStateLinkReady after NO_IDENTITY reset)", peer.state, PeerStateLinkReady)
	}
	if peer.lastSyncAttempt != peerTime(fixedNow) {
		t.Errorf("lastSyncAttempt = %v, want %v (Sync re-entered)", peer.lastSyncAttempt, peerTime(fixedNow))
	}
}

// TestPeerOfferResponseErrorNoAccessAndThrottled covers 24.B.7: an offer
// response carrying ERROR_NO_ACCESS (0xf1) breaks the peering via
// router.Unpeer, and ERROR_THROTTLED (0xf6) postpones the next sync attempt by
// PN_STAMP_THROTTLE. Neither changes peer state or tears down the link
// (LXMPeer.py:413-421).
func TestPeerOfferResponseErrorNoAccessAndThrottled(t *testing.T) {
	t.Parallel()

	fixedNow := time.Unix(2_000_000, 0)

	t.Run("no_access_breaks_peering", func(t *testing.T) {
		ts := rns.NewTransportSystem(nil)
		router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-noaccess-"))
		router.now = func() time.Time { return fixedNow }

		destHash := make([]byte, 16)
		destHash[0] = 0xA1
		peer := NewPeer(router, destHash)
		peer.state = PeerStateResponseReceived
		peer.now = func() time.Time { return fixedNow }
		peer.lastOffer = [][]byte{[]byte("tid-1")}

		// Register the peer so Unpeer has someone to remove.
		router.mu.Lock()
		router.peers[string(destHash)] = peer
		router.mu.Unlock()

		receipt := &rns.RequestReceipt{}
		receipt.Response = peerErrorNoAccess

		peer.OfferResponse(receipt)

		if peer.state != PeerStateResponseReceived {
			t.Errorf("peer state = %d, want %d (NO_ACCESS must not change state)", peer.state, PeerStateResponseReceived)
		}
		router.mu.Lock()
		_, stillPeered := router.peers[string(destHash)]
		router.mu.Unlock()
		if stillPeered {
			t.Error("peer was not removed from router.peers by NO_ACCESS, want it unpeered")
		}
	})

	t.Run("throttled_postpones_sync", func(t *testing.T) {
		ts := rns.NewTransportSystem(nil)
		router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-throttled-"))
		router.now = func() time.Time { return fixedNow }

		destHash := make([]byte, 16)
		destHash[0] = 0xA2
		peer := NewPeer(router, destHash)
		peer.state = PeerStateResponseReceived
		peer.now = func() time.Time { return fixedNow }
		peer.lastOffer = [][]byte{[]byte("tid-1")}
		peer.nextSyncAttempt = 0

		receipt := &rns.RequestReceipt{}
		receipt.Response = peerErrorThrottled

		peer.OfferResponse(receipt)

		if peer.state != PeerStateResponseReceived {
			t.Errorf("peer state = %d, want %d (THROTTLED must not change state)", peer.state, PeerStateResponseReceived)
		}
		wantNext := peerTime(fixedNow) + float64(pnStampThrottle)/float64(time.Second)
		if peer.nextSyncAttempt != wantNext {
			t.Errorf("nextSyncAttempt = %v, want %v (now + PN_STAMP_THROTTLE)", peer.nextSyncAttempt, wantNext)
		}
	})
}

// TestPeerOfferResponseWantsNothing covers 24.B.8: a `false` offer response
// (the remote peer already has every advertised message) marks every lastOffer
// ID handled + removed, tears down the link, increments offered by the offer
// size, and returns the peer to Idle (LXMPeer.py:423-428, 467-474).
func TestPeerOfferResponseWantsNothing(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-wantsnothing-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0xB1
	peer := NewPeer(router, destHash)
	peer.state = PeerStateResponseReceived
	peer.now = func() time.Time { return fixedNow }

	tid1 := []byte("tid-1")
	tid2 := []byte("tid-2")
	router.propagationEntries[string(tid1)] = &propagationEntry{size: 100, receivedAt: fixedNow, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tid2)] = &propagationEntry{size: 100, receivedAt: fixedNow, unhandledBy: [][]byte{destHash}}
	peer.lastOffer = [][]byte{tid1, tid2}

	link := &rns.Link{}
	peer.link = link
	peer.offered = 0

	receipt := &rns.RequestReceipt{}
	receipt.Response = false

	peer.OfferResponse(receipt)

	for _, tid := range [][]byte{tid1, tid2} {
		e := router.propagationEntries[string(tid)]
		if !containsByteSlice(e.handledBy, destHash) {
			t.Errorf("%v not marked handled after wants-nothing response", tid)
		}
		if containsByteSlice(e.unhandledBy, destHash) {
			t.Errorf("%v still in unhandledBy after wants-nothing response", tid)
		}
	}
	if peer.state != PeerStateIdle {
		t.Errorf("peer state = %d, want %d (PeerStateIdle)", peer.state, PeerStateIdle)
	}
	if peer.link != nil {
		t.Errorf("peer.link = %v, want nil (torn down and cleared)", peer.link)
	}
	if peer.offered != 2 {
		t.Errorf("peer.offered = %d, want 2 (incremented by len(lastOffer))", peer.offered)
	}
	if link.GetStatus() != rns.LinkClosed {
		t.Errorf("link status = %d, want %d (LinkClosed, Teardown called)", link.GetStatus(), rns.LinkClosed)
	}
}

// TestPeerOfferResponseWantsEverything covers 24.B.9: a `true` offer response
// (the remote peer wants every advertised message) packs all offered messages'
// stored LXM data into a single Resource as msgpack [time, [lxm_data, ...]],
// starts the transfer, records the transferring IDs, and enters
// PeerStateResourceTransferring (LXMPeer.py:430-435, 452-465). The resource
// data is golden-captured from Python umsgpack for a fixed timestamp and two
// payloads, proving Go/Python wire parity of the sync-transfer container.
func TestPeerOfferResponseWantsEverything(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-wantseverything-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0xC1
	peer := NewPeer(router, destHash)
	peer.state = PeerStateResponseReceived
	peer.now = func() time.Time { return fixedNow }

	tid1 := []byte("tid-1")
	tid2 := []byte("tid-2")
	router.propagationEntries[string(tid1)] = &propagationEntry{payload: []byte{0xaa, 0xbb, 0xcc, 0xdd}, receivedAt: fixedNow, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tid2)] = &propagationEntry{payload: []byte{0xee, 0xff, 0x00, 0x11}, receivedAt: fixedNow, unhandledBy: [][]byte{destHash}}
	peer.lastOffer = [][]byte{tid1, tid2}
	peer.link = &rns.Link{}

	var capturedData []byte
	router.newResource = func(data []byte, link *rns.Link) (*rns.Resource, error) {
		capturedData = append([]byte{}, data...)
		return &rns.Resource{}, nil
	}

	receipt := &rns.RequestReceipt{}
	receipt.Response = true

	peer.OfferResponse(receipt)

	// Golden from Python umsgpack: [2000000.0, [b"\xaa\xbb\xcc\xdd", b"\xee\xff\x00\x11"]].
	wantHex := "92cb413e84800000000092c404aabbccddc404eeff0011"
	if gotHex := hex.EncodeToString(capturedData); gotHex != wantHex {
		t.Errorf("resource data = %s, want %s", gotHex, wantHex)
	}
	if peer.state != PeerStateResourceTransferring {
		t.Errorf("peer state = %d, want %d (PeerStateResourceTransferring)", peer.state, PeerStateResourceTransferring)
	}
	if len(peer.currentlyTransferringMessages) != 2 {
		t.Fatalf("currentlyTransferringMessages len = %d, want 2", len(peer.currentlyTransferringMessages))
	}
	for i, want := range [][]byte{tid1, tid2} {
		if !bytes.Equal(peer.currentlyTransferringMessages[i], want) {
			t.Errorf("currentlyTransferringMessages[%d] = %v, want %v", i, peer.currentlyTransferringMessages[i], want)
		}
	}
	if peer.currentSyncTransferStarted != peerTime(fixedNow) {
		t.Errorf("currentSyncTransferStarted = %v, want %v", peer.currentSyncTransferStarted, peerTime(fixedNow))
	}
}

// TestPeerOfferResponseWantedList covers 24.B.10: a list offer response (the
// remote peer wants a subset of the advertised messages) marks every
// offered-but-not-wanted ID handled + removed, and starts a sync transfer
// carrying only the wanted messages' LXM data (LXMPeer.py:476-484, 452-465).
// The resource data is golden-captured from Python umsgpack for the wanted
// subset.
func TestPeerOfferResponseWantedList(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-peer-wantedlist-"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	destHash := make([]byte, 16)
	destHash[0] = 0xD1
	peer := NewPeer(router, destHash)
	peer.state = PeerStateResponseReceived
	peer.now = func() time.Time { return fixedNow }

	tid1 := []byte("tid-1")
	tid2 := []byte("tid-2")
	router.propagationEntries[string(tid1)] = &propagationEntry{payload: []byte{0xaa, 0xbb, 0xcc, 0xdd}, receivedAt: fixedNow, unhandledBy: [][]byte{destHash}}
	router.propagationEntries[string(tid2)] = &propagationEntry{payload: []byte{0xee, 0xff, 0x00, 0x11}, receivedAt: fixedNow, unhandledBy: [][]byte{destHash}}
	peer.lastOffer = [][]byte{tid1, tid2}
	peer.link = &rns.Link{}

	var capturedData []byte
	router.newResource = func(data []byte, link *rns.Link) (*rns.Resource, error) {
		capturedData = append([]byte{}, data...)
		return &rns.Resource{}, nil
	}

	// The remote peer wants only tid2.
	receipt := &rns.RequestReceipt{}
	receipt.Response = [][]byte{tid2}

	peer.OfferResponse(receipt)

	// tid1 was offered but not wanted: marked handled + removed.
	entry1 := router.propagationEntries[string(tid1)]
	if !containsByteSlice(entry1.handledBy, destHash) {
		t.Error("tid1 (offered, not wanted) not marked handled")
	}
	if containsByteSlice(entry1.unhandledBy, destHash) {
		t.Error("tid1 (offered, not wanted) still in unhandledBy, want removed")
	}

	// tid2 was wanted: left unhandled (pending transfer), not marked handled.
	entry2 := router.propagationEntries[string(tid2)]
	if containsByteSlice(entry2.handledBy, destHash) {
		t.Error("tid2 (wanted) was marked handled, want it left unhandled for the transfer")
	}
	if !containsByteSlice(entry2.unhandledBy, destHash) {
		t.Error("tid2 (wanted) lost unhandledBy, want it retained for the transfer")
	}

	// Golden from Python umsgpack: [2000000.0, [b"\xee\xff\x00\x11"]].
	wantHex := "92cb413e84800000000091c404eeff0011"
	if gotHex := hex.EncodeToString(capturedData); gotHex != wantHex {
		t.Errorf("resource data = %s, want %s", gotHex, wantHex)
	}
	if peer.state != PeerStateResourceTransferring {
		t.Errorf("peer state = %d, want %d (PeerStateResourceTransferring)", peer.state, PeerStateResourceTransferring)
	}
	if len(peer.currentlyTransferringMessages) != 1 || !bytes.Equal(peer.currentlyTransferringMessages[0], tid2) {
		t.Errorf("currentlyTransferringMessages = %v, want [tid2]", peer.currentlyTransferringMessages)
	}
	if peer.currentSyncTransferStarted != peerTime(fixedNow) {
		t.Errorf("currentSyncTransferStarted = %v, want %v", peer.currentSyncTransferStarted, peerTime(fixedNow))
	}
}
