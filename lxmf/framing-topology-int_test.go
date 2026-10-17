// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"

	"path/filepath"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// Multi-hop LXMF topology tests: a real message from a client behind a shared
// instance to a peer beyond one or two relaying transports.
//
// This is the fleet's failing scenario made executable. When unicast died two
// hops out, the client's Conversations pane showed "no path" for the
// propagation node and every message fell back to the propagation node, while
// announces — which never take the transport-insert branch — kept flowing. The
// two-router pipe tests next door are one hop and never saw it.
//
//	client (attached to the shared instance)  --pipe-->  shared instance
//	shared instance  --pipe-->  peer
//
// and, with the relay in place,
//
//	client  --pipe-->  shared instance  --pipe-->  relay  --pipe-->  peer
//
// Every relaying transport is a full transport (SetEnabled(true)), so the
// packets traverse the insert / replace / strip re-framing chain rather than a
// direct one-hop pipe.

// lxmfTopology builds a chain of transports joined by pipe interfaces. The
// first transport is the client (attached to the shared instance), the second
// is the shared instance itself, the last is the peer. Any transports between
// them are enabled relays.
func lxmfTopology(t *testing.T, relays int) []*rns.TransportSystem {
	t.Helper()

	names := []string{"client"}
	enabled := []bool{false}
	shared := []bool{true}
	names = append(names, "shared")
	enabled = append(enabled, true)
	shared = append(shared, false)
	for i := range relays {
		names = append(names, "relay"+string(rune('1'+i)))
		enabled = append(enabled, true)
		shared = append(shared, false)
	}
	names = append(names, "peer")
	enabled = append(enabled, false)
	shared = append(shared, false)

	transports := make([]*rns.TransportSystem, 0, len(names))
	for i, name := range names {
		ts := rns.NewTransportSystem(nil)
		if err := ts.Start(filepath.Join(testutils.TempDir(t, "lxmf-topo-"+name), "rns")); err != nil {
			t.Fatalf("transport %v Start: %v", name, err)
		}
		ts.SetConnectedToSharedInstance(shared[i])
		ts.SetEnabled(enabled[i])
		transports = append(transports, ts)
	}

	for i := 0; i+1 < len(transports); i++ {
		a, b := transports[i], transports[i+1]
		pipeA := interfaces.NewPipeInterface(names[i]+"-link", func(data []byte, iface interfaces.Interface) {
			a.Inbound(data, iface)
		})
		pipeB := interfaces.NewPipeInterface(names[i+1]+"-link", func(data []byte, iface interfaces.Interface) {
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
	t.Cleanup(func() {
		for _, ts := range transports {
			ts.Stop()
		}
	})
	return transports
}

// TestTopologyMultiHopLXMFDelivery proves a message from a client behind a
// shared instance reaches a peer across one and two relaying transports.
func TestTopologyMultiHopLXMFDelivery(t *testing.T) {
	testutils.SkipShortIntegration(t)

	cases := []struct {
		name   string
		relays int
	}{
		{name: "two_hops_client_shared_peer", relays: 0},
		{name: "three_hops_client_shared_relay_peer", relays: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transports := lxmfTopology(t, tc.relays)
			clientTS := transports[0]
			peerTS := transports[len(transports)-1]

			routerClient, err := NewRouter(clientTS, clientTS.Identity(), testutils.TempDir(t, "lxmf-topo-client"))
			if err != nil {
				t.Fatalf("NewRouter client: %v", err)
			}
			t.Cleanup(func() { _ = routerClient.Close() })
			routerPeer, err := NewRouter(peerTS, peerTS.Identity(), testutils.TempDir(t, "lxmf-topo-peer"))
			if err != nil {
				t.Fatalf("NewRouter peer: %v", err)
			}
			t.Cleanup(func() { _ = routerPeer.Close() })

			clientDest, err := routerClient.RegisterDeliveryIdentity(clientTS.Identity(), "Topo Client", nil)
			if err != nil {
				t.Fatalf("RegisterDeliveryIdentity client: %v", err)
			}
			peerDest, err := routerPeer.RegisterDeliveryIdentity(peerTS.Identity(), "Topo Peer", nil)
			if err != nil {
				t.Fatalf("RegisterDeliveryIdentity peer: %v", err)
			}

			received := make(chan *Message, 1)
			routerPeer.RegisterDeliveryCallback(func(msg *Message) {
				select {
				case received <- msg:
				default:
				}
			})

			if err := routerClient.Announce(clientDest.Hash); err != nil {
				t.Fatalf("announce client: %v", err)
			}
			if err := routerPeer.Announce(peerDest.Hash); err != nil {
				t.Fatalf("announce peer: %v", err)
			}

			// Wait for the client to learn a path the whole way out.
			testutils.EventuallyFatal(t, 20*time.Second, func() bool {
				return clientTS.HasPath(peerDest.Hash)
			}, "client never learned a path to the peer across %v hop(s)", tc.relays+2)

			outPeer, err := rns.NewDestination(clientTS, peerTS.Identity(), rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
			if err != nil {
				t.Fatalf("NewDestination OUT peer: %v", err)
			}
			msg, err := NewMessage(outPeer, clientDest, "hello across the relays", "topology", nil)
			if err != nil {
				t.Fatalf("NewMessage: %v", err)
			}
			msg.DesiredMethod = MethodOpportunistic
			if err := msg.Pack(); err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if err := routerClient.HandleOutbound(msg); err != nil {
				t.Fatalf("HandleOutbound: %v", err)
			}

			select {
			case got := <-received:
				if got.ContentString() != "hello across the relays" {
					t.Errorf("content = %q, want %q", got.ContentString(), "hello across the relays")
				}
				if got.TitleString() != "topology" {
					t.Errorf("title = %q, want %q", got.TitleString(), "topology")
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("timed out waiting for delivery across %v hop(s): a message from a client behind a shared "+
					"instance did not survive the relay chain", tc.relays+2)
			}
		})
	}
}
