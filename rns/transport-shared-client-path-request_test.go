// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSharedInstanceClientGetsPathFromRemoteCache verifies that a
// shared-instance client asking for a peer's path gets a reply relayed through
// the instance, even when the instance itself has no cached path.
//
// The peer announces on its own schedule (announce_interval is 360 minutes),
// the peer's announce can land BEFORE the client's stack is (re)started, and a
// shared-instance client persists neither identities nor paths
// (Identity.save_known_destinations / Transport's path table both return early
// for a connected-to-shared-instance process). So the client starts empty and
// its ONLY route to the peer is a path request that is answered — either by its
// own instance from cache, or by some other node on the network that has the
// path cached (Python Transport.py:3044-3119 answers a request from a cached
// announce) with the reply relayed back through the instance to the requester.
//
// This test models the case where the instance has NOTHING (it started after
// the peer's announce) and a remote node has the path cached. The client's
// request must still be answered and the client's tables updated.
func TestSharedInstanceClientGetsPathFromRemoteCache(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()

	tsD := NewTransportSystem(logger) // the peer, which announced before the instance existed
	tsR := NewTransportSystem(logger) // an intermediate transport node that cached that announce
	tsS := NewTransportSystem(logger) // the shared instance, started later, so it has nothing
	tsC := NewTransportSystem(logger) // the attached client (gonomadnet)

	tsD.identity = mustTestNewIdentity(t, true)
	tsR.identity = mustTestNewIdentity(t, true)
	tsS.identity = mustTestNewIdentity(t, true)
	tsC.identity = mustTestNewIdentity(t, true)

	tsD.SetConnectedToSharedInstance(false)
	tsR.SetConnectedToSharedInstance(false)
	tsS.SetConnectedToSharedInstance(false)
	tsC.SetConnectedToSharedInstance(true)
	tsR.SetEnabled(true)
	tsS.SetEnabled(true)

	// D and R are on the network first: the peer's announce predates the
	// appliance's stack, which is the whole point of the scenario.
	for _, ts := range []Transport{tsD, tsR} {
		if err := ts.Start(testutils.TempDir(t, "rns-shared-pathreq")); err != nil {
			t.Fatalf("transport Start: %v", err)
		}
	}

	// --- Network link D <-> R ---
	dToR := interfaces.NewPipeInterface("D-to-R", func(data []byte, iface interfaces.Interface) {
		tsD.Inbound(data, iface)
	})
	rFromD := interfaces.NewPipeInterface("R-from-D", func(data []byte, iface interfaces.Interface) {
		tsR.Inbound(data, iface)
	})
	dToR.SetOther(rFromD)
	rFromD.SetOther(dToR)
	tsD.RegisterInterface(dToR)
	tsR.RegisterInterface(rFromD)
	t.Cleanup(func() {
		_ = dToR.Detach()
		_ = rFromD.Detach()
	})

	// --- The peer announces, long before the instance exists ---
	destD := mustTestNewDestination(t, tsD, tsD.identity, DestinationIn, DestinationSingle, "lxmf", "delivery")
	if err := destD.Announce(nil); err != nil {
		t.Fatalf("destD.Announce: %v", err)
	}

	waitFor(t, 10*time.Second, "R caches the peer's path", func() bool {
		tsR.mu.Lock()
		defer tsR.mu.Unlock()
		e := tsR.pathTable[string(destD.Hash)]
		return e != nil && len(e.Packet) > 0
	})

	// --- Now the shared instance comes up with nothing, and the client attaches ---
	if err := tsS.Start(testutils.TempDir(t, "rns-shared-pathreq")); err != nil {
		t.Fatalf("tsS.Start: %v", err)
	}
	if err := tsC.Start(testutils.TempDir(t, "rns-shared-pathreq")); err != nil {
		t.Fatalf("tsC.Start: %v", err)
	}

	// --- Network link S <-> R ---
	sToR := interfaces.NewPipeInterface("S-to-R", func(data []byte, iface interfaces.Interface) {
		tsS.Inbound(data, iface)
	})
	rFromS := interfaces.NewPipeInterface("R-from-S", func(data []byte, iface interfaces.Interface) {
		tsR.Inbound(data, iface)
	})
	sToR.SetOther(rFromS)
	rFromS.SetOther(sToR)
	tsS.RegisterInterface(sToR)
	tsR.RegisterInterface(rFromS)
	t.Cleanup(func() {
		_ = sToR.Detach()
		_ = rFromS.Detach()
	})

	// --- IPC link S <-> C (the production local interface) ---
	localPort := reserveTCPPort(t)
	server, err := interfaces.NewLocalServerInterface("Local shared instance", "", localPort, func(data []byte, iface interfaces.Interface) {
		tsS.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalServerInterface: %v", err)
	}
	tsS.RegisterInterface(server)
	t.Cleanup(func() { _ = server.Detach() })

	clientConnected := make(chan interfaces.Interface, 1)
	server.SetOnClientConnected(func(c interfaces.Interface) {
		select {
		case clientConnected <- c:
		default:
		}
	})

	client, err := interfaces.NewLocalClientInterface("Local shared instance", "", localPort, func(data []byte, iface interfaces.Interface) {
		tsC.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalClientInterface: %v", err)
	}
	tsC.RegisterInterface(client)
	t.Cleanup(func() { _ = client.Detach() })

	select {
	case <-clientConnected:
	case <-time.After(5 * time.Second):
		t.Fatalf("shared instance never accepted the local client connection")
	}

	// The premise: at the moment the client asks, its instance has no path and
	// no identity for the peer. Clear anything a rebroadcast may have leaked in
	// so the reply can only come from the network.
	tsS.InvalidatePath(destD.Hash)

	tsS.mu.Lock()
	_, instanceHasPath := tsS.pathTable[string(destD.Hash)]
	tsS.mu.Unlock()
	if instanceHasPath {
		t.Fatal("premise violated: the shared instance still has a path to the peer")
	}
	if tsC.RecallNoUse(destD.Hash) != nil {
		t.Fatal("premise violated: the client already knows the peer identity")
	}

	// The client asks for the peer's path, exactly as nomadnet's RememberPeer /
	// RecallPeer does when a conversation is opened for an unknown peer.
	if err := tsC.RequestPath(destD.Hash); err != nil {
		t.Fatalf("tsC.RequestPath: %v", err)
	}

	waitFor(t, 15*time.Second, "the attached client learns the peer's identity and path", func() bool {
		if tsC.RecallNoUse(destD.Hash) == nil {
			return false
		}
		tsC.mu.Lock()
		defer tsC.mu.Unlock()
		return tsC.pathTable[string(destD.Hash)] != nil
	})
}

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %v", d, what)
}

// TestSharedInstanceClientPathRequestRoundTripThroughTCPHub models the full
// deployment topology: a client attached by a local socket to the appliance's
// own shared instance, whose only network interface is a TCPClientInterface to
// the hub — and the requested peer reachable only over that hub. Nothing here
// is cached anywhere: the peer is not in the client's instance, not in the hub,
// and the peer announced before any of this existed. The request must travel
// client → instance → hub → peer and the reply must come all the way back,
// because that round trip is the ONLY way a shared-instance client can learn a
// sparsely-announcing peer's identity (announce_interval is 360 minutes, so a
// path request bridges that gap rather than waiting for the next announce).
func TestSharedInstanceClientPathRequestRoundTripThroughTCPHub(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()

	// --- The hub: a shared instance serving the fleet's TCP gateway ---
	hub := NewTransportSystem(logger)
	hub.identity = mustTestNewIdentity(t, true)
	hub.SetConnectedToSharedInstance(false)
	hub.SetEnabled(true)
	if err := hub.Start(testutils.TempDir(t, "rns-hub-pathreq")); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}

	port := reserveTCPPort(t)
	srv, err := interfaces.NewTCPServerInterface("hub", "127.0.0.1", port,
		func(data []byte, iface interfaces.Interface) { hub.Inbound(data, iface) },
		func(iface interfaces.Interface) {
			if ci, ok := iface.(*interfaces.TCPClientInterface); ok {
				hub.RegisterInterface(ci)
			}
		})
	if err != nil {
		t.Fatalf("NewTCPServerInterface: %v", err)
	}
	hub.RegisterInterface(srv)
	t.Cleanup(func() { _ = srv.Detach() })

	// --- The peer: reachable only over the hub ---
	tsR := NewTransportSystem(logger)
	tsR.identity = mustTestNewIdentity(t, true)
	tsR.SetConnectedToSharedInstance(false)
	tsR.SetEnabled(true)
	if err := tsR.Start(testutils.TempDir(t, "rns-hub-pathreq")); err != nil {
		t.Fatalf("tsR.Start: %v", err)
	}
	rToHub, err := interfaces.NewTCPClientInterface("peer-to-hub", "127.0.0.1", port, false,
		func(data []byte, iface interfaces.Interface) { tsR.Inbound(data, iface) })
	if err != nil {
		t.Fatalf("peer TCPClientInterface: %v", err)
	}
	tsR.RegisterInterface(rToHub)
	t.Cleanup(func() { _ = rToHub.Detach() })

	// The peer hosts its own LXMF delivery destination, exactly as a nomadnet
	// node does, so it answers a path request for itself.
	destR := mustTestNewDestination(t, tsR, tsR.identity, DestinationIn, DestinationSingle, "lxmf", "delivery")

	// --- The appliance's shared instance: a hub client, nothing cached ---
	tsS := NewTransportSystem(logger)
	tsS.identity = mustTestNewIdentity(t, true)
	tsS.SetConnectedToSharedInstance(false)
	tsS.SetEnabled(true)
	if err := tsS.Start(testutils.TempDir(t, "rns-hub-pathreq")); err != nil {
		t.Fatalf("tsS.Start: %v", err)
	}
	sToHub, err := interfaces.NewTCPClientInterface("instance-to-hub", "127.0.0.1", port, false,
		func(data []byte, iface interfaces.Interface) { tsS.Inbound(data, iface) })
	if err != nil {
		t.Fatalf("instance TCPClientInterface: %v", err)
	}
	tsS.RegisterInterface(sToHub)
	t.Cleanup(func() { _ = sToHub.Detach() })

	// --- The client: attached to the appliance's instance by the local socket ---
	tsC := NewTransportSystem(logger)
	tsC.identity = mustTestNewIdentity(t, true)
	tsC.SetConnectedToSharedInstance(true)
	if err := tsC.Start(testutils.TempDir(t, "rns-hub-pathreq")); err != nil {
		t.Fatalf("tsC.Start: %v", err)
	}

	localPort := reserveTCPPort(t)
	server, err := interfaces.NewLocalServerInterface("Local shared instance", "", localPort, func(data []byte, iface interfaces.Interface) {
		tsS.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalServerInterface: %v", err)
	}
	tsS.RegisterInterface(server)
	t.Cleanup(func() { _ = server.Detach() })

	clientConnected := make(chan interfaces.Interface, 1)
	server.SetOnClientConnected(func(c interfaces.Interface) {
		select {
		case clientConnected <- c:
		default:
		}
	})

	client, err := interfaces.NewLocalClientInterface("Local shared instance", "", localPort, func(data []byte, iface interfaces.Interface) {
		tsC.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalClientInterface: %v", err)
	}
	tsC.RegisterInterface(client)
	t.Cleanup(func() { _ = client.Detach() })

	select {
	case <-clientConnected:
	case <-time.After(5 * time.Second):
		t.Fatalf("the instance never accepted the local client connection")
	}

	// Let the TCP links settle before asking.
	waitFor(t, 5*time.Second, "the hub sees the peer's TCP link", func() bool {
		return len(hub.GetInterfaces()) >= 2
	})

	// Nothing anywhere knows the peer: not the client, not its instance, not the hub.
	tsS.InvalidatePath(destR.Hash)
	hub.InvalidatePath(destR.Hash)
	if tsC.RecallNoUse(destR.Hash) != nil {
		t.Fatal("premise violated: the client already knows the peer identity")
	}
	tsS.mu.Lock()
	_, instanceHasPath := tsS.pathTable[string(destR.Hash)]
	tsS.mu.Unlock()
	if instanceHasPath {
		t.Fatal("premise violated: the instance already has a path to the peer")
	}

	// The client asks for the peer's path: this is what nomadnet does when a
	// conversation is opened for a peer whose identity is not known.
	if err := tsC.RequestPath(destR.Hash); err != nil {
		t.Fatalf("tsC.RequestPath: %v", err)
	}

	waitFor(t, 20*time.Second, "the round trip client → instance → hub → peer → back delivers the identity and path", func() bool {
		if tsC.RecallNoUse(destR.Hash) == nil {
			return false
		}
		tsC.mu.Lock()
		defer tsC.mu.Unlock()
		return tsC.pathTable[string(destR.Hash)] != nil
	})
}
