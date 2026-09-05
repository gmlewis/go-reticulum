// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSharedInstanceHubToClientPayloadSizes reproduces the 2026-09-05 live
// raspberrypi Experiment-2 finding: through the Go shared instance, small
// hub->client link packets (20-byte keepalives, 54-byte pongs, 85-99-byte
// slash commands) flow, while ~163-byte hub->client packets (the /who
// response class) vanish at the shared instance — the shared instance's
// Debug log shows "received 163 bytes from Local Client @" with no
// "Inbound packet" line, i.e. the frame never survives Unpack.
//
// The rig drives every payload size through the identical send path
// (NewPacketWithTransport + Send) the rrcd hub uses for room notices.
func TestSharedInstanceHubToClientPayloadSizes(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()
	if os.Getenv("RNS_TEST_VERBOSE") != "" {
		logger = mustTestLogger(t, LogDebug)
	}

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
		if err := ts.Start(testutils.TempDir(t, "rns-payload-sizes")); err != nil {
			t.Fatalf("transport Start: %v", err)
		}
	}

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

	destC := mustTestNewDestination(t, tsC, tsC.identity, DestinationIn, DestinationSingle, "rrc", "hub")
	hubEstablished := make(chan *Link, 4)
	destC.SetLinkEstablishedCallback(func(l *Link) {
		select {
		case hubEstablished <- l:
		default:
		}
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
	case <-pathLearned:
	case <-time.After(10 * time.Second):
		t.Fatal("the attached client never learned the path to the hub's destination")
	}

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

	// The client records every packet payload it receives on the link.
	rxOnR := make(chan []byte, 64)
	initiatorLink.SetPacketCallback(func(data []byte, _ *Packet) {
		select {
		case rxOnR <- append([]byte(nil), data...):
		default:
		}
	})

	// Sizes mirror the live wire classes: 20-byte keepalive, 54-byte pong,
	// 85-99-byte slash commands, and the failing ~163-byte wire class
	// (144-byte payload + 19-byte HEADER_1 header). The payload bytes are
	// pseudo-random like token-encrypted CBOR envelopes.
	rnd := rand.New(rand.NewSource(1))
	sizes := []int{1, 35, 66, 80, 94, 144, 200, 247, 297}
	for _, size := range sizes {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(rnd.Intn(256))
		}
		hubLinkCopy := hubLink
		ping := NewPacketWithTransport(tsC, hubLinkCopy, payload)
		if err := ping.Send(); err != nil {
			t.Fatalf("hub->client send size=%v: %v", size, err)
		}
		select {
		case got := <-rxOnR:
			if !bytes.Equal(got, payload) {
				t.Fatalf("hub->client data mismatch size=%v: got %d bytes, want %d", size, len(got), size)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the client never received the hub's %v-byte payload through the shared instance", size)
		}
	}

	// And the reverse direction: client->hub with the same sizes.
	rxOnC := make(chan []byte, 64)
	hubLink.SetPacketCallback(func(data []byte, _ *Packet) {
		select {
		case rxOnC <- append([]byte(nil), data...):
		default:
		}
	})
	for _, size := range sizes {
		payload := make([]byte, size)
		for i := range payload {
			payload[i] = byte(rnd.Intn(256))
		}
		initiatorLinkCopy := initiatorLink
		pong := NewPacketWithTransport(tsR, initiatorLinkCopy, payload)
		if err := pong.Send(); err != nil {
			t.Fatalf("client->hub send size=%v: %v", size, err)
		}
		select {
		case got := <-rxOnC:
			if !bytes.Equal(got, payload) {
				t.Fatalf("client->hub data mismatch size=%v: got %d bytes, want %d", size, len(got), size)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the hub never received the client's %v-byte payload through the shared instance", size)
		}
	}
}
