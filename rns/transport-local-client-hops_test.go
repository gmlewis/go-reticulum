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

// TestSharedInstanceLocalClientHopsParity pins the Python hop-count spoofing
// for shared-instance planes (RNS/Transport.py:1520-1525): a packet arriving
// from a spawned local client on a shared instance, or from the shared-instance
// interface on an attached client, keeps its WIRE hop count — the inbound
// `packet.hops += 1` (Transport.py:1496) is undone by the local-client
// decrement (Transport.py:1521-1525). Every hop-count comparison on these
// planes is taken at Python's scale:
//
//   - the shared instance's path-table entry for a co-located client's
//     destination stores 0 hops (Transport.py:2058: announce_hops =
//     packet.hops; the 0 is what for_local_client detection checks,
//     Transport.py:1564-1565);
//   - the attached client stores 0 hops for the destination it learned from
//     the shared instance, so its outbound takes the "directly reachable"
//     branch (Transport.py:1185-1191) and emits HEADER_1;
//   - the shared instance forwards the client's link request keeping the
//     received framing (the remaining_hops == 0 branch rewrites only the hop
//     byte, Transport.py:1620-1639) — the hub receives HEADER_1 with the
//     wire hop byte;
//   - the link-table entry records the taken hops at Python's scale
//     (Transport.py:1691: packet.hops), and the link-request proof relayed
//     back to the client carries that same wire hop byte
//     (Transport.py:2258/2343).
func TestSharedInstanceLocalClientHopsParity(t *testing.T) {
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
		if err := ts.Start(testutils.TempDir(t, "rns-local-client-hops")); err != nil {
			t.Fatalf("transport Start: %v", err)
		}
	}

	// attachWithCapture wires a TCP local-server/client pair between tsS and
	// the attached transport, teeing every frame the attachment receives into
	// a channel before handing it to the transport's Inbound.
	attachWithCapture := func(name string, attached Transport) chan []byte {
		t.Helper()
		localPort := reserveTCPPort(t)
		frames := make(chan []byte, 64)
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
			select {
			case frames <- append([]byte(nil), data...):
			default:
			}
			attached.Inbound(data, iface)
		})
		if err != nil {
			t.Fatalf("NewLocalClientInterface: %v", err)
		}
		attached.RegisterInterface(client)
		t.Cleanup(func() { _ = client.Detach() })
		select {
		case <-clientChan:
		case <-time.After(5 * time.Second):
			t.Fatalf("shared instance never accepted %v", name)
		}
		return frames
	}

	var _ = attachWithCapture // placeholder until the wiring below uses it

	// The hub attachment tees frames so the test can pin the forwarded link
	// request's wire framing and hop byte.
	framesAtC := attachWithCapture("S<->C", tsC)
	framesAtR := attachWithCapture("S<->R", tsR)

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

	// Python Transport.py:2058 stores packet.hops in the path table, which is
	// the wire hop count for local-client planes after the inbound decrement.
	tsS.mu.Lock()
	sharedEntry, sharedOK := tsS.pathTable[string(destC.Hash)]
	sharedHops := -1
	if sharedOK {
		sharedHops = sharedEntry.Hops
	}
	tsS.mu.Unlock()
	if !sharedOK {
		t.Fatal("the shared instance never installed the hub destination's path")
	}
	if sharedHops != 0 {
		t.Fatalf("shared-instance path-table hops for a local-client destination = %d, want 0 (Python Transport.py:2058 with the local-client decrement)", sharedHops)
	}
	tsR.mu.Lock()
	clientEntry, clientOK := tsR.pathTable[string(destC.Hash)]
	clientHops := -1
	if clientOK {
		clientHops = clientEntry.Hops
	}
	tsR.mu.Unlock()
	if !clientOK {
		t.Fatal("the attached client never installed the hub destination's path")
	}
	if clientHops != 0 {
		t.Fatalf("attached-client path-table hops learned via the shared instance = %d, want 0 (Python Transport.py:1496+1525 decrement)", clientHops)
	}

	// R dials C's destination through the shared instance. With a 0-hop path
	// entry the client's outbound takes Python's "directly reachable" branch
	// and emits HEADER_1; the shared instance's remaining_hops == 0 branch
	// forwards the received framing with only the hop byte rewritten.
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

	// The link request the hub received must be HEADER_1 with the wire hop
	// byte 0 (Python remaining_hops == 0 forwarding, Transport.py:1621-1634).
	lrFrame := findFrame(framesAtC, 0b10)
	if lrFrame == nil {
		t.Fatal("no link request frame was captured at the hub attachment")
	}
	if got := int(lrFrame[0]&0b11000000) >> 6; got != Header1 {
		t.Fatalf("link request frame at hub has header type %d, want Header1 (Python keeps the received framing for 0-hop local-client destinations)", got)
	}
	if got := int(lrFrame[1]); got != 0 {
		t.Fatalf("link request frame at hub has hop byte %d, want 0 (Python packet.hops for a local-client-sourced packet)", got)
	}

	// The link-request proof relayed back to the client carries the wire hop
	// byte (Python Transport.py:2258: packet.hops if from_local_client ...).
	proofFrame := findFrame(framesAtR, 0b11)
	if proofFrame == nil {
		t.Fatal("no link proof frame was captured at the client attachment")
	}
	if got := int(proofFrame[1]); got != 0 {
		t.Fatalf("relayed link proof has hop byte %d, want 0 (Python Transport.py:2258)", got)
	}

	// The shared instance's link-table entry records Python-scale hops.
	tsS.mu.Lock()
	linkEntry, linkOK := tsS.linkTable[string(hubLink.linkID)]
	linkHops, linkRemaining := -1, -1
	if linkOK {
		linkHops, linkRemaining = linkEntry.Hops, linkEntry.RemainingHops
	}
	tsS.mu.Unlock()
	if !linkOK {
		t.Fatal("the shared instance has no link-table entry for the established link")
	}
	if linkHops != 0 || linkRemaining != 0 {
		t.Fatalf("shared-instance link-table entry = (hops=%d, remaining=%d), want (0, 0) (Python Transport.py:1691/1694)", linkHops, linkRemaining)
	}
}

// findFrame drains the capture channel and returns the first frame whose low
// two flag bits equal the wanted packet type (0b00 DATA, 0b10 LINKREQUEST,
// 0b11 PROOF), or nil when none arrives.
func findFrame(frames chan []byte, packetType byte) []byte {
	for range 64 {
		select {
		case f := <-frames:
			if len(f) >= 2 && f[0]&0b00000011 == packetType&0b11 {
				return f
			}
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	}
	return nil
}
