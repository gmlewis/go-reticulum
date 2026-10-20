// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// Multi-hop topology tests.
//
// The chain is an appliance that is a client of a shared instance, reaching a
// peer three hops out through two relaying transports:
//
//	C  (client of S1)  --pipe-->  S1 (shared instance, transport enabled)
//	S1 --pipe--> S2 (relay, transport enabled) --pipe--> R (peer)
//
// Every re-framing branch a relay applies then runs for real, in order:
//
//	hop 1  C inserts a transport header naming S1      (Transport.py:1172-1183)
//	hop 2  S1 replaces that transport id with S2       (Transport.py:1149-1162)
//	hop 3  S2 strips the transport header              (Transport.py:1163-1171)
//
// Both a real link round-trip and a real path request/response are asserted, in
// both directions, rather than a single packet's arrival.

// topologyTransport builds one member of the chain.
func topologyTransport(t *testing.T, name string, sharedInstance, enabled bool) *TransportSystem {
	t.Helper()
	logger := testSilentLogger()
	if os.Getenv("RNS_TEST_VERBOSE") != "" {
		logger = mustTestLogger(t, LogDebug)
	}
	ts := NewTransportSystem(logger)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetConnectedToSharedInstance(sharedInstance)
	ts.SetEnabled(enabled)
	if err := ts.Start(testutils.TempDir(t, "rns-topology-"+name)); err != nil {
		t.Fatalf("transport %v Start: %v", name, err)
	}
	return ts
}

// joinTopologyTransports wires two transports together with an in-memory pipe
// pair, registering each end on its own transport.
func joinTopologyTransports(t *testing.T, name string, a, b *TransportSystem) {
	t.Helper()
	pipeA := interfaces.NewPipeInterface(name+"-a", func(data []byte, iface interfaces.Interface) {
		a.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface(name+"-b", func(data []byte, iface interfaces.Interface) {
		b.Inbound(data, iface)
	})
	pipeA.SetOther(pipeB)
	pipeB.SetOther(pipeA)
	a.RegisterInterface(pipeA)
	b.RegisterInterface(pipeB)
	t.Cleanup(func() {
		_ = pipeA.Detach()
		_ = pipeB.Detach()
	})
}

// topologyChain is the three-hop chain: a client of a shared instance, the
// shared instance, a relay, and a peer.
type topologyChain struct {
	client  *TransportSystem
	shared  *TransportSystem
	relay   *TransportSystem
	peer    *TransportSystem
	peerOut *Destination
}

// newTopologyChain stands the chain up and returns it.
func newTopologyChain(t *testing.T) *topologyChain {
	t.Helper()

	client := topologyTransport(t, "client", true, false)
	shared := topologyTransport(t, "shared", false, true)
	relay := topologyTransport(t, "relay", false, true)
	peer := topologyTransport(t, "peer", false, false)

	joinTopologyTransports(t, "client-shared", client, shared)
	joinTopologyTransports(t, "shared-relay", shared, relay)
	joinTopologyTransports(t, "relay-peer", relay, peer)

	peerOut := mustTestNewDestination(t, peer, peer.identity, DestinationIn, DestinationSingle, "topology", "peer")

	return &topologyChain{client: client, shared: shared, relay: relay, peer: peer, peerOut: peerOut}
}

// waitForPath waits for the client to learn a path to hash. The announce chain
// is asynchronous by design (relays hold and rebroadcast), so this waits on the
// observable end state with a bounded deadline via the shared helper.
func waitForPath(t *testing.T, ts *TransportSystem, hash []byte) *PathEntry {
	t.Helper()
	var entry *PathEntry
	testutils.EventuallyFatal(t, 10*time.Second, func() bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if e := ts.pathTable[string(hash)]; e != nil && len(e.NextHop) > 0 {
			snapshot := *e
			entry = &snapshot
		}
		return entry != nil
	}, "client never learned a path to %x", hash)
	if entry == nil {
		t.FailNow()
	}
	return entry
}

// TestTopologyThreeHopLinkRoundTrip proves a link can be established and used
// across the full chain, in both directions. The link is what a conversation
// uses to message a peer, so a failure here is exactly "the appliance cannot
// reach the peer".
func TestTopologyThreeHopLinkRoundTrip(t *testing.T) {
	testutils.SkipShortIntegration(t)

	chain := newTopologyChain(t)

	// The peer announces; the client must learn a path three hops out whose
	// next hop is the shared instance it is attached to.
	if err := chain.peerOut.Announce([]byte("peer-app-data")); err != nil {
		t.Fatalf("peer announce: %v", err)
	}
	entry := waitForPath(t, chain.client, chain.peerOut.Hash)
	if !bytes.Equal(entry.NextHop, chain.shared.identity.Hash) {
		t.Errorf("client path next hop = %x, want the shared instance %x", entry.NextHop, chain.shared.identity.Hash)
	}
	if entry.Hops != 3 {
		t.Errorf("client path hops = %v, want 3 (client -> shared -> relay -> peer)", entry.Hops)
	}

	// Establish a link from the client to the peer, across all three hops.
	established := make(chan *Link, 1)
	chain.peerOut.SetLinkEstablishedCallback(func(l *Link) { established <- l })

	// The peer answers link pings: any non-nil response suffices.
	chain.peerOut.RegisterRequestHandler(
		LinkPingPath,
		func(path string, _ any, _ []byte, _ []byte, _ *Identity, _ time.Time) any {
			_ = path
			return []byte("pong")
		},
		AllowAll, nil, false,
	)

	link, err := NewLink(chain.client, chain.peerOut)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	t.Cleanup(link.Teardown)
	if err := link.Establish(); err != nil {
		t.Fatalf("link Establish: %v", err)
	}

	select {
	case <-established:
	case <-time.After(15 * time.Second):
		t.Fatalf("peer never established the link: a link request for a three-hop destination did not survive the relay chain")
	}

	// A link ping is an RTT request/proof round trip over the established link:
	// both directions of the three-hop path must still work.
	rtt, err := link.Ping()
	if err != nil {
		t.Fatalf("link Ping across three hops: %v", err)
	}
	if rtt < 0 {
		t.Errorf("link Ping returned a negative RTT %v", rtt)
	}
}

// TestTopologyThreeHopPathRequest proves a path request is relayed the whole way
// and its response comes back, for a destination that has not announced — the
// "get a peer's keys on demand" path, which is how a typed address or a scanned
// QR code reaches a peer.
func TestTopologyThreeHopPathRequest(t *testing.T) {
	testutils.SkipShortIntegration(t)

	chain := newTopologyChain(t)

	// A destination on the peer that never announces: only a path request can
	// reveal it.
	silent := mustTestNewDestination(t, chain.peer, chain.peer.identity, DestinationIn, DestinationSingle, "topology", "silent")

	learned := make(chan []byte, 4)
	chain.client.RegisterAnnounceHandler(&AnnounceHandler{
		// Path responses are announces with PATH_RESPONSE context; the extended
		// callback is the one that also receives them (mirroring Python's
		// receive_path_responses handler attribute).
		ReceivePathResponses: true,
		ReceivedAnnounceWithContext: func(destinationHash []byte, _ *Identity, _ []byte, _ bool) {
			select {
			case learned <- append([]byte(nil), destinationHash...):
			default:
			}
		},
	})

	if err := chain.client.RequestPath(silent.Hash); err != nil {
		t.Fatalf("RequestPath: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case hash := <-learned:
			if bytes.Equal(hash, silent.Hash) {
				return
			}
		case <-deadline:
			t.Fatalf("client never received a path response for the unannounced destination %x: "+
				"the path request or its response did not survive the relay chain", silent.Hash)
		}
	}
}
