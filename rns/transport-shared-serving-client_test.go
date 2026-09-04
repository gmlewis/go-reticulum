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

// TestSharedInstanceServingClientPingPong reproduces the live gorrcd failure
// mode (2026-09-04 fleet): the hub process attaches to a co-located shared
// instance (the deployment's gonomadnet owns the fleet TCPServerInterface),
// registers its rrc.hub IN destination, and announces. Chat clients then dial
// the hub's destination through the shared instance. The observed live
// symptom was join/leave spam: the hub pinged each client exactly once and
// never again, the PONGs never reached the attached hub, and every link
// eventually died to a watchdog and re-joined.
//
// This test stands up the same topology — S the shared instance, C the
// attached serving client (hub role), R the remote chat client — and asserts:
//
//  1. The attached client's announce reaches the shared instance and is
//     forwarded onward, so R learns a usable path to C's destination.
//  2. R establishes a link to C through S.
//  3. Link data flows BOTH directions through the attachment: C's ping
//     reaches R and R's PONG reaches C — the cycle the hub's ping loop
//     depends on to keep client links alive.
//
// Without a working attached-client announce/data path the link never forms
// or the PONG never arrives, matching the live fleet behavior.
func TestSharedInstanceServingClientPingPong(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()
	if os.Getenv("RNS_TEST_VERBOSE") != "" {
		logger = mustTestLogger(t, LogDebug)
	}

	// --- Transports: S the shared instance, C the attached hub, R the chat client ---
	tsS := NewTransportSystem(logger)
	tsC := NewTransportSystem(logger)
	tsR := NewTransportSystem(logger)

	tsS.identity = mustTestNewIdentity(t, true)
	tsC.identity = mustTestNewIdentity(t, true)
	tsR.identity = mustTestNewIdentity(t, true)

	tsS.SetConnectedToSharedInstance(false)
	tsC.SetConnectedToSharedInstance(true)
	tsR.SetConnectedToSharedInstance(false)
	// The production shared instance runs with enable_transport=Yes (the
	// fleet's gonomadnet on raspberrypi), so S relays announces and packets
	// between its clients and the network.
	tsS.SetEnabled(true)

	// Start each transport so the maintenance worker (the announce-table
	// rebroadcast engine, mirroring NewReticulum's Start call in production)
	// runs — without it the shared instance never rebroadcasts the attached
	// client's announce to the network.
	for _, ts := range []Transport{tsS, tsC, tsR} {
		if err := ts.Start(testutils.TempDir(t, "rns-shared-serving")); err != nil {
			t.Fatalf("transport Start: %v", err)
		}
	}

	// --- Network link S<->R (in-memory pipe) ---
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

	// --- C (the hub role) registers its IN destination and announces ---
	destC := mustTestNewDestination(t, tsC, tsC.identity, DestinationIn, DestinationSingle, "rrc", "hub")
	hubEstablished := make(chan *Link, 1)
	destC.SetLinkEstablishedCallback(func(l *Link) { hubEstablished <- l })

	// The shared instance must receive the attached client's announce...
	announced := make(chan struct{}, 1)
	tsS.RegisterAnnounceHandler(&AnnounceHandler{
		ReceivedAnnounce: func(destinationHash []byte, _ *Identity, _ []byte) {
			if bytes.Equal(destinationHash, destC.Hash) {
				select {
				case announced <- struct{}{}:
				default:
				}
			}
		},
	})
	// ...and the remote client must learn the path to the hub.
	pathLearned := make(chan struct{}, 1)
	tsR.RegisterAnnounceHandler(&AnnounceHandler{
		ReceivedAnnounce: func(destinationHash []byte, _ *Identity, _ []byte) {
			if bytes.Equal(destinationHash, destC.Hash) {
				select {
				case pathLearned <- struct{}{}:
				default:
				}
			}
		},
	})

	if err := destC.Announce(nil); err != nil {
		t.Fatalf("destC.Announce: %v", err)
	}

	// DEBUG: watch the shared instance's path table for the hub destination.
	stopWatch := make(chan struct{})
	go func() {
		last := false
		for {
			select {
			case <-stopWatch:
				return
			case <-time.After(100 * time.Millisecond):
			}
			tsS.mu.Lock()
			_, present := tsS.pathTable[string(destC.Hash)]
			ats := tsS.announceTable[string(destC.Hash)]
			hops := -1
			if ats != nil {
				hops = ats.Hops
			}
			tsS.mu.Unlock()
			if present != last {
				t.Logf("DEBUG hub path present=%v announceTable=%v at %s",
					present, hops, time.Now().Format("15:04:05.000"))
				last = present
			}
		}
	}()
	t.Cleanup(func() { close(stopWatch) })

	select {
	case <-announced:
	case <-time.After(15 * time.Second):
		t.Fatal("the shared instance never received the attached client's announce")
	}
	select {
	case <-pathLearned:
	case <-time.After(15 * time.Second):
		t.Fatal("the remote client never learned the path to the attached hub's destination")
	}

	// The remote client's path entry must be usable (Interface set, NextHop
	// the shared instance's transport identity).
	tsR.mu.Lock()
	entry, ok := tsR.pathTable[string(destC.Hash)]
	tsR.mu.Unlock()
	if !ok {
		t.Fatal("remote path table missing the hub destination entry")
	}
	if entry.Interface == nil {
		t.Fatal("remote path entry has nil Interface")
	}
	if !bytes.Equal(entry.NextHop, tsS.identity.Hash) {
		t.Fatalf("remote path entry NextHop = %x, want shared instance identity %x",
			entry.NextHop, tsS.identity.Hash)
	}

	// --- R establishes a link to C (the hub) through S ---
	initiatorLink := mustTestNewLink(t, tsR, destC)
	initiatorEstablished := make(chan struct{}, 1)
	initiatorLink.SetLinkEstablishedCallback(func(*Link) { initiatorEstablished <- struct{}{} })

	if err := initiatorLink.Establish(); err != nil {
		t.Fatalf("initiatorLink.Establish: %v", err)
	}

	select {
	case <-initiatorEstablished:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for client->hub link establishment through shared instance")
	}
	var hubLink *Link
	select {
	case hubLink = <-hubEstablished:
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for hub-side link establishment")
	}

	// --- Link data must flow BOTH directions through the attachment ---

	rxOnRemote := make(chan []byte, 4)
	initiatorLink.SetPacketCallback(func(data []byte, _ *Packet) {
		select {
		case rxOnRemote <- append([]byte(nil), data...):
		default:
		}
	})
	rxOnHub := make(chan []byte, 4)
	hubLink.SetPacketCallback(func(data []byte, _ *Packet) {
		select {
		case rxOnHub <- append([]byte(nil), data...):
		default:
		}
	})

	// Hub -> client (the hub's PING): the attached serving client sends link
	// data through the shared instance to the remote client.
	ping := NewPacketWithTransport(tsC, hubLink, []byte("ping-from-hub"))
	if err := ping.Send(); err != nil {
		t.Fatalf("hub ping send: %v", err)
	}
	select {
	case got := <-rxOnRemote:
		if !bytes.Equal(got, []byte("ping-from-hub")) {
			t.Fatalf("remote received %q, want ping-from-hub", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the hub's ping never reached the client through the shared instance")
	}

	// Client -> hub (the PONG): the reply must reach the attached hub.
	pong := NewPacketWithTransport(tsR, initiatorLink, []byte("pong-from-client"))
	if err := pong.Send(); err != nil {
		t.Fatalf("client pong send: %v", err)
	}
	select {
	case got := <-rxOnHub:
		if !bytes.Equal(got, []byte("pong-from-client")) {
			t.Fatalf("hub received %q, want pong-from-client", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the client's pong never reached the attached hub through the shared instance")
	}
}
