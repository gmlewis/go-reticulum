// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

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

func TestPeerSyncPreconditions(t *testing.T) {
	t.Parallel()

	destHash := make([]byte, 16)
	ts := rns.NewTransportSystem(nil)
	id, _ := rns.NewIdentity(false, nil)
	dir, cleanup := testutils.TempDir(t, "lxmf-peer-sync-")
	defer cleanup()
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
			stampCost:       intPtr(1),
			stampCostFlex:   intPtr(2),
			peeringCost:     intPtr(3),
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
			stampCost:       intPtr(1),
			stampCostFlex:   intPtr(2),
			peeringCost:     intPtr(3),
			peeringKey:      nil,
			wantPostpone:    "peering key has not been generated",
		},
		{
			name:            "all_preconditions_met",
			nextSyncAttempt: 0,
			stampCost:       intPtr(1),
			stampCostFlex:   intPtr(2),
			peeringCost:     intPtr(3),
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
	dir, cleanup := testutils.TempDir(t, "lxmf-peer-idrecall-")
	defer cleanup()
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
		peer.propagationStampCost = intPtr(1)
		peer.propagationStampCostFlexibility = intPtr(2)
		peer.peeringCost = intPtr(3)
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
	dir, cleanup := testutils.TempDir(t, "lxmf-peer-pathreq-")
	defer cleanup()
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
		peer.propagationStampCost = intPtr(1)
		peer.propagationStampCostFlexibility = intPtr(2)
		peer.peeringCost = intPtr(3)
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
	dir, cleanup := testutils.TempDir(t, "lxmf-peer-offer-")
	defer cleanup()
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
		peer.propagationStampCost = intPtr(1)
		peer.propagationStampCostFlexibility = intPtr(2)
		peer.peeringCost = intPtr(3)
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
	dir, cleanup := testutils.TempDir(t, "lxmf-peer-nounh-")
	defer cleanup()
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
		peer.propagationStampCost = intPtr(1)
		peer.propagationStampCostFlexibility = intPtr(2)
		peer.peeringCost = intPtr(3)
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

func intPtr(v int) *int { return &v }
