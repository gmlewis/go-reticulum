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

// TestSharedInstanceClientLinkOverSharedInstance reproduces the production
// outage where a process connected to a shared Reticulum instance (a local
// client such as ping-nomadnet-node, lxmd, or rncp) could not establish a link
// to a remote node reachable through the shared instance.
//
// Root cause: the client loaded the shared instance's destination_table from
// disk, whose entries referenced network interfaces the client does not own
// (Interface=nil), so Outbound's transport-forward branch declined to inject
// the link request into transport and instead broadcast a Header1 LR that the
// shared instance dropped. Compounding this, the shared instance never
// forwarded the announces it received to its co-located clients, so the client
// had no source of usable path entries (Interface=its local link,
// NextHop=the shared instance's transport identity).
//
// This test stands up the full topology with real interfaces: a remote node R
// linked to the shared instance S over a PipeInterface (the network), and a
// client C connected to S over a real TCP LocalServerInterface/
// LocalClientInterface pair (the IPC path used in production). R announces, S
// forwards the announce to C, C learns the path, and C establishes a link to
// R's destination through S. Without the fix the link establishment times out:
// the client either never gets a usable path entry, or its LR is dropped.
func TestSharedInstanceClientLinkOverSharedInstance(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()
	if os.Getenv("RNS_TEST_VERBOSE") != "" {
		logger = mustTestLogger(t, LogDebug)
	}

	// --- Transports ---
	tsS := NewTransportSystem(logger) // shared instance
	tsC := NewTransportSystem(logger) // client of the shared instance
	tsR := NewTransportSystem(logger) // remote node

	tsS.identity = mustTestNewIdentity(t, true)
	tsC.identity = mustTestNewIdentity(t, true)
	tsR.identity = mustTestNewIdentity(t, true)

	// Roles: S is the shared instance, C is connected to it, R is standalone.
	// This mirrors NewReticulum's startLocalInterface outcome + the
	// SetConnectedToSharedInstance call that gates path-table load/persist and
	// announce rebroadcasts.
	tsS.SetConnectedToSharedInstance(false)
	tsC.SetConnectedToSharedInstance(true)
	tsR.SetConnectedToSharedInstance(false)

	// --- Network link S<->R (in-memory pipe stands in for a real interface) ---
	pipeS := interfaces.NewPipeInterface("S-to-R", func(data []byte, iface interfaces.Interface) {
		tsS.Inbound(data, iface)
	})
	pipeR := interfaces.NewPipeInterface("R-to-S", func(data []byte, iface interfaces.Interface) {
		tsR.Inbound(data, iface)
	})
	pipeS.SetOther(pipeR)
	pipeR.SetOther(pipeS)
	tsS.RegisterInterface(pipeS)
	tsR.RegisterInterface(pipeR)
	t.Cleanup(func() {
		_ = pipeS.Detach()
		_ = pipeR.Detach()
	})

	// --- IPC link S<->C (real TCP local interface, the production path) ---
	localPort := reserveTCPPort(t)
	server, err := interfaces.NewLocalServerInterface("Local shared instance", "", localPort, func(data []byte, iface interfaces.Interface) {
		tsS.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalServerInterface: %v", err)
	}
	tsS.RegisterInterface(server)
	t.Cleanup(func() { _ = server.Detach() })

	// The connect-readiness signal must come from the SERVER side, not the
	// client: NewLocalClientInterface's connect() is synchronous, so the
	// client's own Status() is already true before the shared instance's
	// acceptLoop has accepted the connection, spawned the per-connection
	// interface, and launched its read loop. Announcing before that point
	// races forwardAnnounceToLocalClients (it would see zero spawned clients
	// and the client would never learn the path) — the original flake.
	// OnClientConnected fires only after the spawned interface is registered
	// and its read loop has started, so waiting on it makes the subsequent
	// announce deterministic. The channel is buffered so a connect that
	// completes before the select runs is not lost.
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

	// Wait deterministically for the shared instance to have accepted this
	// client (event-driven; no polling, no sleep). The deadline is a pure
	// safety backstop for an unreachable server — it is never hit on the
	// happy path, where the event fires within milliseconds of the dial.
	select {
	case <-clientConnected:
	case <-time.After(5 * time.Second):
		t.Fatalf("shared instance never accepted the local client connection")
	}

	// --- Remote destination on R ---
	destR := mustTestNewDestination(t, tsR, tsR.identity, DestinationIn, DestinationSingle, "testapp", "node")
	receiverEstablished := make(chan *Link, 1)
	destR.SetLinkEstablishedCallback(func(l *Link) { receiverEstablished <- l })

	// Drive the test off the announce itself rather than polling the path
	// table: register an announce handler on the client that signals the
	// instant it receives the forwarded announce for destR. The handler runs
	// after the path entry is installed, so the entry is visible when the
	// channel receives. The channel is buffered so an announce that arrives
	// before the select runs is not lost.
	pathLearned := make(chan struct{}, 1)
	tsC.RegisterAnnounceHandler(&AnnounceHandler{
		ReceivedAnnounce: func(destinationHash []byte, _ *Identity, _ []byte) {
			if bytes.Equal(destinationHash, destR.Hash) {
				select {
				case pathLearned <- struct{}{}:
				default:
				}
			}
		},
	})

	// --- R announces; S learns the path and forwards the announce to C ---
	if err := destR.Announce(nil); err != nil {
		t.Fatalf("destR.Announce: %v", err)
	}

	// Wait for C to receive the forwarded announce (event-driven; no
	// polling, no sleep). The deadline is a safety backstop only.
	select {
	case <-pathLearned:
	case <-time.After(15 * time.Second):
		t.Fatalf("client never learned path to remote destination via forwarded announce")
	}

	// The client's path entry must be usable: Interface set to its local link
	// and NextHop = the shared instance's transport identity. An Interface of
	// nil is exactly the regression: Outbound's transport-forward branch
	// requires a non-nil Interface, else it falls through to a Header1
	// broadcast the shared instance drops.
	tsC.mu.Lock()
	entry, ok := tsC.pathTable[string(destR.Hash)]
	tsC.mu.Unlock()
	if !ok {
		t.Fatalf("client path table missing entry for remote destination")
	}
	if entry.Interface == nil {
		t.Fatalf("client path entry has nil Interface (Outbound transport-forward branch would decline it)")
	}
	if entry.Interface != client {
		t.Fatalf("client path entry Interface is not its local client interface")
	}
	if !bytes.Equal(entry.NextHop, tsS.identity.Hash) {
		t.Fatalf("client path entry NextHop = %x, want shared instance transport identity %x", entry.NextHop, tsS.identity.Hash)
	}

	// --- C establishes a link to R through S ---
	initiatorLink := mustTestNewLink(t, tsC, destR)
	initiatorEstablished := make(chan struct{}, 1)
	initiatorLink.SetLinkEstablishedCallback(func(*Link) { initiatorEstablished <- struct{}{} })

	if err := initiatorLink.Establish(); err != nil {
		t.Fatalf("initiatorLink.Establish: %v", err)
	}

	select {
	case <-initiatorEstablished:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for client->remote link establishment through shared instance")
	}
	select {
	case <-receiverEstablished:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for remote-side link establishment")
	}
}
