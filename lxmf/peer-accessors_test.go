// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// addTestPeer inserts a Peer into the router's peers map (mirroring how
// Python LXMRouter populates self.peers[destination_hash] = LXMPeer(...))
// and returns the inserted peer for further field setup.
func addTestPeer(t *testing.T, router *Router, destHash []byte) *Peer {
	t.Helper()
	peer := NewPeer(router, destHash)
	router.mu.Lock()
	router.peers[string(destHash)] = peer
	router.mu.Unlock()
	return peer
}

// TestPeersAndPeerByHash verifies that Router.Peers() returns a
// snapshot of every registered peer, and Router.PeerByHash() performs
// the Python self.peers[destination_hash] lookup (Network.py:1815).
func TestPeersAndPeerByHash(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hashA := bytes.Repeat([]byte{0xa1}, rns.TruncatedHashLength/8)
	hashB := bytes.Repeat([]byte{0xb2}, rns.TruncatedHashLength/8)
	addTestPeer(t, router, hashA)
	addTestPeer(t, router, hashB)

	peers := router.Peers()
	if len(peers) != 2 {
		t.Fatalf("Peers() returned %d peers, want 2", len(peers))
	}

	seen := map[string]bool{}
	for _, p := range peers {
		seen[string(p.DestinationHash())] = true
	}
	if !seen[string(hashA)] || !seen[string(hashB)] {
		t.Fatalf("Peers() = %v, want hashes %x and %x", seen, hashA, hashB)
	}

	// Snapshot must be a copy: mutating the returned slice must not
	// affect the router's internal map.
	peers[0] = nil
	if router.Peers()[0] == nil {
		t.Fatal("Peers() did not return a defensive copy")
	}

	// PeerByHash mirrors Python self.peers[destination_hash] lookup.
	if got := router.PeerByHash(hashA); got == nil || !bytes.Equal(got.DestinationHash(), hashA) {
		t.Fatalf("PeerByHash(%x) = %v, want peer with hash %x", hashA, got, hashA)
	}
	if got := router.PeerByHash(bytes.Repeat([]byte{0xff}, rns.TruncatedHashLength/8)); got != nil {
		t.Fatalf("PeerByHash(unknown) = %v, want nil", got)
	}

	// Peers() on an empty router returns an empty (non-nil) slice.
	emptyRouter := mustTestNewRouter(t, rns.NewTransportSystem(nil), nil, testutils.TempDir(t, tempDirPrefix))
	if got := emptyRouter.Peers(); got == nil {
		t.Fatal("Peers() returned nil for empty router, want empty slice")
	} else if len(got) != 0 {
		t.Fatalf("Peers() on empty router = %v, want empty", got)
	}
}

// TestUnpeer verifies that Router.Unpeer removes a peer, mirroring
// Python LXMRouter.unpeer(destination_hash, timestamp=None)
// (LXMRouter.py:1942). A peer whose peering_timebase is in the future
// must survive (timestamp < peering_timebase), matching Python.
func TestUnpeer(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hashA := bytes.Repeat([]byte{0xa1}, rns.TruncatedHashLength/8)
	hashB := bytes.Repeat([]byte{0xb2}, rns.TruncatedHashLength/8)
	addTestPeer(t, router, hashA)
	addTestPeer(t, router, hashB)

	if len(router.Peers()) != 2 {
		t.Fatalf("setup: Peers() = %d, want 2", len(router.Peers()))
	}

	router.Unpeer(hashA)
	peers := router.Peers()
	if len(peers) != 1 {
		t.Fatalf("after Unpeer: Peers() = %d, want 1", len(peers))
	}
	if router.PeerByHash(hashA) != nil {
		t.Fatal("Unpeer did not remove hashA")
	}
	if router.PeerByHash(hashB) == nil {
		t.Fatal("Unpeer incorrectly removed hashB")
	}

	// Unpeer of an unknown hash is a no-op (Python guards with the
	// `if destination_hash in self.peers` membership check).
	router.Unpeer(bytes.Repeat([]byte{0xee}, rns.TruncatedHashLength/8))
	if len(router.Peers()) != 1 {
		t.Fatalf("Unpeer(unknown) changed peer count to %d, want 1", len(router.Peers()))
	}
}

// TestUnpeerTimestampGuard pins Python's timestamp guard: unpeer only
// removes the peer when timestamp >= peer.peering_timebase. A future
// peering_timebase protects the peer from an out-of-order unpeer.
func TestUnpeerTimestampGuard(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hash := bytes.Repeat([]byte{0xc3}, rns.TruncatedHashLength/8)
	peer := addTestPeer(t, router, hash)
	// Set a peering_timebase in the future; the default Unpeer
	// timestamp (now) must be less than this, so the peer survives.
	peer.peeringTimebase = float64(time.Now().Unix()) + 1e6

	router.Unpeer(hash)
	if router.PeerByHash(hash) == nil {
		t.Fatal("Unpeer removed peer whose peering_timebase is in the future; should survive")
	}
}

// TestPeerAccessors verifies that every Peer read-only accessor
// returns the value Python LXMPeer exposes for the same field state.
// Expected values are captured from lxmf/LXMPeer.py:141-231.
func TestPeerAccessors(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hash := bytes.Repeat([]byte{0xd4}, rns.TruncatedHashLength/8)
	peer := addTestPeer(t, router, hash)

	cost := 12
	link := &rns.Link{} // non-nil sentinel; the accessor just returns the stored link
	peer.mu.Lock()
	peer.alive = true
	peer.lastHeard = 1234.5
	peer.syncTransferRate = 567.8
	peer.linkEstablishmentRate = 901.2
	peer.peeringCost = &cost
	peer.state = PeerStateLinkReady
	peer.link = link
	peer.lastSyncAttempt = 111.0
	peer.nextSyncAttempt = 222.0
	peer.mu.Unlock()

	if got := peer.DestinationHash(); !bytes.Equal(got, hash) {
		t.Fatalf("DestinationHash() = %x, want %x", got, hash)
	}
	if got := peer.Alive(); got != true {
		t.Fatalf("Alive() = %v, want true", got)
	}
	if got := peer.LastHeard(); got != 1234.5 {
		t.Fatalf("LastHeard() = %v, want 1234.5", got)
	}
	if got := peer.SyncTransferRate(); got != 567.8 {
		t.Fatalf("SyncTransferRate() = %v, want 567.8", got)
	}
	if got := peer.LinkEstablishmentRate(); got != 901.2 {
		t.Fatalf("LinkEstablishmentRate() = %v, want 901.2", got)
	}
	if got := peer.PeeringCost(); got == nil || *got != 12 {
		t.Fatalf("PeeringCost() = %v, want 12", got)
	}
	if got := peer.State(); got != PeerStateLinkReady {
		t.Fatalf("State() = %v, want %v", got, PeerStateLinkReady)
	}
	if got := peer.Link(); got != link {
		t.Fatalf("Link() = %p, want %p", got, link)
	}
	if got := peer.LastSyncAttempt(); got != 111.0 {
		t.Fatalf("LastSyncAttempt() = %v, want 111.0", got)
	}
	if got := peer.NextSyncAttempt(); got != 222.0 {
		t.Fatalf("NextSyncAttempt() = %v, want 222.0", got)
	}

	// Identity() returns the recalled identity for the peer's
	// destination hash. With no registered identity it is nil, matching
	// Python's RNS.Identity.recall returning None.
	if got := peer.Identity(); got != nil {
		t.Fatalf("Identity() = %v, want nil for unregistered identity", got)
	}

	// PeeringCost is nil when unset (Python peering_cost = None).
	peer2 := addTestPeer(t, router, bytes.Repeat([]byte{0xe5}, rns.TruncatedHashLength/8))
	if got := peer2.PeeringCost(); got != nil {
		t.Fatalf("PeeringCost() = %v, want nil when unset", got)
	}
}

// TestPeerSyncExported verifies that Peer.Sync() is exported and
// callable by a consumer. A peer with a syncHook and met preconditions
// fires the hook (mirrors nomadnet calling peer.sync() in a thread,
// Network.py:1828).
func TestPeerSyncExported(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	hash := bytes.Repeat([]byte{0xf6}, rns.TruncatedHashLength/8)
	peer := addTestPeer(t, router, hash)

	// Satisfy every Sync() precondition so the call reaches the
	// syncHook: sync time reached, stamp costs known, peering key ready,
	// a path available, identity+destination resolved, and at least one
	// unhandled message advertised.
	peer.now = func() time.Time { return time.Unix(1000, 0) }
	peer.nextSyncAttempt = 0
	peer.propagationStampCost = new(1)
	peer.propagationStampCostFlexibility = new(2)
	peer.peeringCost = new(3)
	peer.peeringKey = []any{[]byte("k"), 3} // value 3 >= cost 3
	peer.hasPathFn = func([]byte) bool { return true }
	peer.requestPathFn = func([]byte) error { return nil }
	testID, _ := rns.NewIdentity(false, nil)
	peer.identity = testID
	peer.destination, _ = rns.NewDestination(ts, testID, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	peer.unhandledMessagesFn = func() [][]byte { return [][]byte{[]byte("msg1")} }

	var hookFired bool
	peer.syncHook = func() { hookFired = true }

	peer.Sync()
	if !hookFired {
		t.Fatal("Sync() did not fire syncHook; expected it to reach the sync path")
	}
}
