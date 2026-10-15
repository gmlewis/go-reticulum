// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSharedInstanceForwardsAnnouncesToAttachedClient pins whether a client
// attached to a shared Reticulum instance learns, from the instance's own
// announce rebroadcast, the identity AND path of a remote peer.
//
// A shared-instance client owns no network interfaces: every announce it will
// ever see arrives over the local interface from the instance, and it persists
// neither known destinations nor a path table (Python
// Identity.save_known_destinations / Transport path-table load-save both return
// early for a connected-to-shared-instance process). So an attached client's
// ability to recall a peer identity and to route to a peer depends entirely on
// the instance forwarding announces to it.
func TestSharedInstanceForwardsAnnouncesToAttachedClient(t *testing.T) {
	testutils.SkipShortIntegration(t)

	logger := testSilentLogger()

	tsS := NewTransportSystem(logger) // S: the shared instance
	tsC := NewTransportSystem(logger) // C: the attached client
	tsR := NewTransportSystem(logger) // R: a remote peer on the network

	tsS.identity = mustTestNewIdentity(t, true)
	tsC.identity = mustTestNewIdentity(t, true)
	tsR.identity = mustTestNewIdentity(t, true)

	tsS.SetConnectedToSharedInstance(false)
	tsC.SetConnectedToSharedInstance(true)
	tsR.SetConnectedToSharedInstance(false)
	tsS.SetEnabled(true)

	for _, ts := range []Transport{tsS, tsC, tsR} {
		if err := ts.Start(testutils.TempDir(t, "rns-shared-learns")); err != nil {
			t.Fatalf("transport Start: %v", err)
		}
	}

	// --- Network link S<->R ---
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

	// --- IPC link S<->C (the production local interface) ---
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

	// --- R announces a destination on the network ---
	destR := mustTestNewDestination(t, tsR, tsR.identity, DestinationIn, DestinationSingle, "lxmf", "delivery")

	clientSaw := make(chan struct{}, 1)
	tsC.RegisterAnnounceHandler(&AnnounceHandler{
		ReceivedAnnounce: func(destinationHash []byte, _ *Identity, _ []byte) {
			if bytes.Equal(destinationHash, destR.Hash) {
				select {
				case clientSaw <- struct{}{}:
				default:
				}
			}
		},
	})

	if err := destR.Announce(nil); err != nil {
		t.Fatalf("destR.Announce: %v", err)
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-clientSaw:
			// The attached client received the forwarded announce.
			if id := tsC.RecallNoUse(destR.Hash); id == nil {
				t.Fatalf("client received the announce but cannot recall the peer identity")
			}
			tsC.mu.Lock()
			_, hasPath := tsC.pathTable[string(destR.Hash)]
			tsC.mu.Unlock()
			if !hasPath {
				t.Fatalf("client received the announce but has no path to the peer")
			}
			return
		case <-deadline:
			t.Fatalf("the attached client never received the peer's announce forwarded by the shared instance")
		}
	}
}
