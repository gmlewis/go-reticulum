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

// TestSharedInstanceBothClientsLinkSurvival reproduces the 2026-09-04
// raspberrypi redeploy topology exactly: gorrcd (the hub) AND gonomadnet
// (the chat client) BOTH attach to the SAME shared instance (gornsd -s).
// The client dials the hub's destination through the shared instance; the
// link must establish, carry data both ways, and SURVIVE past the link
// keepalive interval. The live symptom was links cycling every ~20 seconds
// (each cycle re-delivering the hub WELCOME, hence "multiple welcome
// messages"), with the hub logging receipt-style retransmissions.
func TestSharedInstanceBothClientsLinkSurvival(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()
	if os.Getenv("RNS_TEST_VERBOSE") != "" {
		logger = mustTestLogger(t, LogDebug)
	}

	// --- Transports: S the shared instance, C the attached hub, R the attached client ---
	tsS := NewTransportSystem(logger)
	tsC := NewTransportSystem(logger)
	tsR := NewTransportSystem(logger)

	tsS.identity = mustTestNewIdentity(t, true)
	tsC.identity = mustTestNewIdentity(t, true)
	tsR.identity = mustTestNewIdentity(t, true)

	tsS.SetConnectedToSharedInstance(false)
	tsC.SetConnectedToSharedInstance(true)
	tsR.SetConnectedToSharedInstance(true)
	tsS.SetEnabled(true)

	for _, ts := range []Transport{tsS, tsC, tsR} {
		if err := ts.Start(testutils.TempDir(t, "rns-both-clients")); err != nil {
			t.Fatalf("transport Start: %v", err)
		}
	}

	// --- Two IPC links S<->C and S<->R (real TCP local interfaces) ---
	attach := func(name string, ts Transport) {
		t.Helper()
		localPort := reserveTCPPort(t)
		clientChan := make(chan interfaces.Interface, 1)
		server, err := interfaces.NewLocalServerInterface(name, "", localPort, func(data []byte, iface interfaces.Interface) {
			tsS.Inbound(data, iface)
		})
		if err != nil {
			t.Fatalf("NewLocalServerInterface: %v", err)
		}
		tsS.RegisterInterface(server)
		t.Cleanup(func() { _ = server.Detach() })
		server.SetOnClientConnected(func(c interfaces.Interface) {
			select {
			case clientChan <- c:
			default:
			}
		})
		client, err := interfaces.NewLocalClientInterface(name, "", localPort, func(data []byte, iface interfaces.Interface) {
			ts.Inbound(data, iface)
		})
		if err != nil {
			t.Fatalf("NewLocalClientInterface: %v", err)
		}
		ts.RegisterInterface(client)
		t.Cleanup(func() { _ = client.Detach() })

		select {
		case <-clientChan:
		case <-time.After(5 * time.Second):
			t.Fatalf("shared instance never accepted %v", name)
		}
	}
	attach("S<->C", tsC)
	attach("S<->R", tsR)

	// --- C (the hub role) registers its IN destination and announces ---
	destC := mustTestNewDestination(t, tsC, tsC.identity, DestinationIn, DestinationSingle, "rrc", "hub")
	hubEstablished := make(chan *Link, 4)
	destC.SetLinkEstablishedCallback(func(l *Link) {
		select {
		case hubEstablished <- l:
		default:
		}
	})

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

	select {
	case <-announced:
	case <-time.After(10 * time.Second):
		t.Fatal("the shared instance never received the attached hub's announce")
	}
	select {
	case <-pathLearned:
	case <-time.After(10 * time.Second):
		t.Fatal("the attached client never learned the path to the hub's destination")
	}

	// --- R dials C's destination through the shared instance ---
	initiatorLink := mustTestNewLink(t, tsR, destC)
	linkActive := make(chan struct{}, 1)
	initiatorLink.SetLinkEstablishedCallback(func(_ *Link) {
		select {
		case linkActive <- struct{}{}:
		default:
		}
	})

	if err := initiatorLink.Establish(); err != nil {
		t.Fatalf("initiatorLink.Establish: %v", err)
	}
	select {
	case <-linkActive:
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for client->hub link establishment through the shared instance")
	}
	var hubLink *Link
	select {
	case hubLink = <-hubEstablished:
	case <-time.After(20 * time.Second):
		t.Fatal("timeout waiting for hub-side link establishment")
	}

	// --- Link data must flow BOTH directions through the attachments ---
	rxOnR := make(chan []byte, 4)
	initiatorLink.SetPacketCallback(func(data []byte, _ *Packet) {
		select {
		case rxOnR <- append([]byte(nil), data...):
		default:
		}
	})
	pingData := []byte("ping through shared instance")
	ping := NewPacketWithTransport(tsC, hubLink, pingData)
	if err := ping.Send(); err != nil {
		t.Fatalf("hub->client send: %v", err)
	}
	select {
	case got := <-rxOnR:
		if !bytes.Equal(got, pingData) {
			t.Fatalf("hub->client data mismatch: %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the client never received the hub's link data through the shared instance")
	}

	rxOnC := make(chan []byte, 4)
	hubLink.SetPacketCallback(func(data []byte, _ *Packet) {
		select {
		case rxOnC <- append([]byte(nil), data...):
		default:
		}
	})
	pongData := []byte("pong through shared instance")
	pong := NewPacketWithTransport(tsR, initiatorLink, pongData)
	if err := pong.Send(); err != nil {
		t.Fatalf("client->hub send: %v", err)
	}
	select {
	case got := <-rxOnC:
		if !bytes.Equal(got, pongData) {
			t.Fatalf("client->hub data mismatch: %q", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the hub never received the client's link data through the shared instance")
	}

	// --- The link must SURVIVE the keepalive interval: no timeout teardown ---
	// The live failure was links tearing down ~20s after establishment with
	// reason=timeout, cycling WELCOME after WELCOME. Let the watchdog run
	// through several keepalive intervals.
	dead := make(chan struct{}, 1)
	initiatorLink.SetLinkClosedCallback(func(_ *Link) {
		select {
		case dead <- struct{}{}:
		default:
		}
	})
	select {
	case <-dead:
		t.Fatalf("the link died through the shared instance before surviving the keepalive window (reason: %v)",
			TeardownReasonName(initiatorLink.TeardownReason()))
	case <-time.After(75 * time.Second):
	}
	if hubLink.GetStatus() != LinkActive {
		t.Fatalf("hub-side link status = %v, want active", hubLink.GetStatus())
	}
}
