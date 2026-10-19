// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestHopsDeltaRewritesTheForwardedHopByte exercises the forwarding half of the
// local_hops_delta disguise end to end, which needs a real local-client first
// hop: Python rewrites the hop byte on the outbound leg only when the packet
// arrived from a client of the shared instance and is not bound for another
// client (Transport.py:1692):
//
//	if Transport.local_hops_delta != 0 and from_local_client and not to_local_client:
//	    new_raw = Transport.mangle_hops(new_raw, Transport.local_hops_delta)
//
// A pipe is not a local client, so the three-transport topology tests never take
// this branch. The chain here is a TCP loopback client of a shared instance, the
// shared instance itself, and a peer behind a network pipe. The client dials a
// destination on the peer; the shared instance relays it and must emit the
// disguise byte, which is captured on the receiving end of the pipe.
func TestHopsDeltaRewritesTheForwardedHopByte(t *testing.T) {
	testutils.SkipShortIntegration(t)

	shared := topologyTransport(t, "delta-fwd-shared", false, true)
	client := topologyTransport(t, "delta-fwd-client", true, false)
	peer := topologyTransport(t, "delta-fwd-peer", false, false)

	// The client's first hop is a spawned local-client interface of the shared
	// instance. That is what makes the forwarded-hop disguise apply at all.
	localPort := reserveTCPPort(t)
	relayClient := make(chan interfaces.Interface, 1)
	server, err := interfaces.NewLocalServerInterface("delta-fwd-local", "", localPort, func(data []byte, iface interfaces.Interface) {
		shared.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalServerInterface: %v", err)
	}
	shared.RegisterInterface(server)
	t.Cleanup(func() { _ = server.Detach() })
	server.SetOnClientConnected(func(c interfaces.Interface) {
		select {
		case relayClient <- c:
		default:
		}
	})
	localClient, err := interfaces.NewLocalClientInterface("delta-fwd-local", "", localPort, func(data []byte, iface interfaces.Interface) {
		client.Inbound(data, iface)
	})
	if err != nil {
		t.Fatalf("NewLocalClientInterface: %v", err)
	}
	client.RegisterInterface(localClient)
	t.Cleanup(func() { _ = localClient.Detach() })

	var relayIface interfaces.Interface
	select {
	case relayIface = <-relayClient:
	case <-time.After(5 * time.Second):
		t.Fatal("the shared instance never accepted the local client")
	}
	if !shared.isLocalClientInterface(relayIface) {
		t.Fatalf("the shared instance does not classify %v as a local client interface", relayIface.Name())
	}

	// The shared instance's other side is a network pipe to the peer. Capture on
	// the receiving end: the peer's OnReceive sees exactly the bytes that
	// crossed the link.
	var (
		mu     sync.Mutex
		frames [][]byte
	)
	pipeShared := interfaces.NewPipeInterface("delta-fwd-shared-net", func(data []byte, iface interfaces.Interface) {
		shared.Inbound(data, iface)
	})
	pipePeer := interfaces.NewPipeInterface("delta-fwd-peer-net", func(data []byte, iface interfaces.Interface) {
		mu.Lock()
		frames = append(frames, append([]byte{}, data...))
		mu.Unlock()
		peer.Inbound(data, iface)
	})
	pipeShared.SetOther(pipePeer)
	pipePeer.SetOther(pipeShared)
	shared.RegisterInterface(pipeShared)
	peer.RegisterInterface(pipePeer)
	t.Cleanup(func() {
		_ = pipeShared.Detach()
		_ = pipePeer.Detach()
	})

	// Deterministic delta: (3 % 6) + 2 = 5 (Python Transport.py:240).
	shared.SetLocalHopsDeltaRand(func() byte { return 3 })
	shared.SetLocalHopsDeltaEnabled(true)

	destPeer := mustTestNewDestination(t, peer, peer.identity, DestinationIn, DestinationSingle, "delta", "forward")
	outDest := mustTestNewDestination(t, client, peer.identity, DestinationOut, DestinationSingle, "delta", "forward")
	if !bytes.Equal(destPeer.Hash, outDest.Hash) {
		t.Fatalf("destination hash mismatch between peer and client views")
	}

	if err := destPeer.Announce(nil); err != nil {
		t.Fatalf("announce: %v", err)
	}
	entry := waitForPath(t, client, outDest.Hash)
	// A destination one hop away, learned through the shared instance, is the
	// case Python injects a transport header for (Transport.py:1173-1191).
	if entry.Hops != 1 {
		t.Fatalf("client path hops = %v, want 1 (the peer behind the shared instance)", entry.Hops)
	}

	mu.Lock()
	frames = nil
	mu.Unlock()

	if err := NewPacket(outDest, []byte("forwarded delta payload")).Send(); err != nil {
		t.Fatalf("send: %v", err)
	}

	var frame []byte
	testutils.EventuallyFatal(t, 5*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, f := range frames {
			if len(f) >= TruncatedHashLength/8+3 && bytes.Equal(f[2:TruncatedHashLength/8+2], destPeer.Hash) {
				frame = append([]byte{}, f...)
				return true
			}
		}
		return false
	}, "the relayed frame never crossed the shared instance's network interface")
	mu.Lock()
	frame = append([]byte{}, frame...)
	mu.Unlock()

	if len(frame) < 2 {
		t.Fatalf("captured frame is too short to carry a hop byte: %x", frame)
	}
	if got := frame[1]; got != 5 {
		t.Errorf("the forwarded frame's hop byte = %v, want the shared instance's delta 5 (Transport.py:1692 mangles a frame from a local client)", got)
	}
}
