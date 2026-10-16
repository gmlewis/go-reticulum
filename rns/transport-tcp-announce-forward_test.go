// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestTCPServerForwardsAnnounceToItsOtherClients is the "two clients of one hub
// never hear each other" defect, in the topology the fleet actually runs: every
// node, and both Android appliances, dial glenn-kamrui's TCPServerInterface.
//
// An announce that arrives on a TCP server's spawned client interface must be
// forwarded to that server's OTHER spawned clients. Python gets this for free:
// Transport rebroadcasts on every interface except the receiving one, and the
// receiving one is the per-client *spawned* interface (TCPInterface.py:612), not
// the listener — so the listener is still an eligible out-interface and fans the
// announce out to its remaining clients. Go's forwardAnnounceToLocalClients
// collected only LocalServerInterface spawns (the Unix-socket shared instance),
// so it never covered TCP-server clients; and after the duplicate-frame fix the
// server's own process_outgoing is a no-op, so there was no second path either.
//
// Field symptom, measured on the two appliances on 2026-10-16: the tablet, a
// client of the hub, received announces from peers the hub had heard on its OTHER
// interfaces (127 of them) but never once from the phone, the hub's other client —
// so the phone's display name and app-data never arrived, the tablet's
// conversation header sat at "<hex>…", and only a propagation-node sync could get
// a message across.
func TestTCPServerForwardsAnnounceToItsOtherClients(t *testing.T) {
	t.Parallel()

	hub := NewTransportSystem(testSilentLogger())
	hub.identity = &Identity{Hash: mustHexDecode(t, "aabbccddeeff00112233445566778899")}
	hub.SetEnabled(true)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}

	// Production registers each spawned client with the transport as its
	// connectHandler fires (rns.go wraps NewTCPServerInterface's onConnect with
	// Transport.RegisterInterface).
	spawnedCh := make(chan *interfaces.TCPClientInterface, 4)
	srv, err := interfaces.NewTCPServerInterface("hub-test", "127.0.0.1", port,
		func(data []byte, iface interfaces.Interface) { hub.Inbound(data, iface) },
		func(iface interfaces.Interface) {
			if ci, ok := iface.(*interfaces.TCPClientInterface); ok {
				hub.RegisterInterface(ci)
				select {
				case spawnedCh <- ci:
				default:
				}
			}
		})
	if err != nil {
		t.Fatalf("server create: %v", err)
	}
	defer func() { _ = srv.Detach() }()
	hub.RegisterInterface(srv)

	// Client A is a real node with its own identity, so the announce it sends is
	// a valid one the hub will accept and forward.
	tsA := NewTransportSystem(testSilentLogger())
	tsA.identity = mustTestNewIdentity(t, true)
	clientA, err := interfaces.NewTCPClientInterface("client-a", "127.0.0.1", port, false,
		func(data []byte, iface interfaces.Interface) { tsA.Inbound(data, iface) })
	if err != nil {
		t.Fatalf("client A create: %v", err)
	}
	defer func() { _ = clientA.Detach() }()
	tsA.RegisterInterface(clientA)

	awaitSpawn := func() {
		t.Helper()
		select {
		case <-spawnedCh:
		case <-time.After(3 * time.Second):
			t.Fatal("the hub never spawned a client interface")
		}
	}
	awaitSpawn() // client A

	// Client B only has to observe what the hub forwards to its other clients.
	connB, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("client B dial: %v", err)
	}
	defer func() { _ = connB.Close() }()
	awaitSpawn() // client B

	// An announcing destination must be IN + SINGLE, as an LXMF delivery
	// destination is.
	dest, err := NewDestination(tsA, tsA.identity, DestinationIn, DestinationSingle, "lxmf", "delivery")
	if err != nil {
		t.Fatalf("destination: %v", err)
	}
	if err := dest.Announce([]byte("forwarded-announce")); err != nil {
		t.Fatalf("announce: %v", err)
	}

	// The announce must first be ACCEPTED (a path installed), or there is nothing
	// to forward and this test would pass for the wrong reason.
	waitForCondition(t, 3*time.Second, func() bool { return hub.HasPath(dest.Hash) },
		"the hub never installed a path for client A's announce, so it was rejected rather than unforwarded")

	// The hub must relay that announce to its other client exactly ONCE. Reads are
	// bounded (readFramesUntilQuiet sets a read deadline), so a frame that never
	// arrives fails here instead of hanging the test — and the exact count matters:
	// a server's clients were once delivered every frame twice (see
	// TestTCPClientReceivesOneCopyOfEachFrame), and forwarding co-located clients
	// explicitly is exactly the kind of second path that reintroduces that.
	frames := readFramesUntilQuiet(t, connB, 2*time.Second)
	switch len(frames) {
	case 0:
		t.Error("the hub never forwarded the announce to its other TCP client: two clients of one TCP hub cannot hear each other, so they never learn each other's paths, identities or display names (Python forwards it, because the receiving interface is the per-client spawned one and the listener is still an eligible out-interface)")
	case 1:
		// Correct.
	default:
		t.Errorf("the hub forwarded the announce to its other client %v times, want 1 (a duplicate path reintroduces the double-delivery defect)", len(frames))
	}
}
