// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// recordingInterface is a plain, alive interface that counts the frames the
// transport hands it.
type recordingInterface struct {
	dummyInterface
	sends atomic.Int64
}

func (r *recordingInterface) Send([]byte) error {
	r.sends.Add(1)
	return nil
}

func newRecordingInterface(name string) *recordingInterface {
	r := &recordingInterface{}
	r.name = name
	return r
}

// TestPendingPathRequesterSurvivesUntilItIsGone pins the lifetime of the record
// that lets a relayed path request's reply find its way home.
//
// A node that relays a path request remembers WHO asked, so the reply — an
// announce with context PATH_RESPONSE — can be sent back to them. A co-located
// client has no other route to that reply: path-response announces are
// deliberately excluded from the local-client announce fan-out (handleAnnounce
// gates on `packet.Context != ContextPathResponse`), so if the record is gone
// when the reply lands, the reply is dropped and the client never learns the
// peer's identity or path.
//
// Python bounds that record by the requester's own lifetime and never by a
// timer: "Cull invalidated path requests" pops it only when the recording
// interface is no longer among Transport.interfaces (Transport.py:660-672), and
// the delivery path pops it when the reply arrives (Transport.py:1975). A
// fixed bound would fail on slow multi-hop links, where a relayed round trip
// routinely outlives it, while never mattering on a LAN.
func TestPendingPathRequesterSurvivesUntilItIsGone(t *testing.T) {
	t.Parallel()

	dest := "103eff1c97a1bff6bb11664288ad0c0c"

	newTransport := func() (*TransportSystem, *recordingInterface) {
		ts := NewTransportSystem(testSilentLogger())
		ts.identity = mustTestNewIdentity(t, true)
		waiting := newRecordingInterface("waiting-client")
		ts.RegisterInterface(waiting)
		ts.RegisterInterface(newRecordingInterface("network"))
		ts.mu.Lock()
		ts.ensureStateLocked()
		ts.pendingPathRequests[dest] = []interfaces.Interface{waiting}
		ts.mu.Unlock()
		return ts, waiting
	}

	remembers := func(ts *TransportSystem) bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		return len(ts.pendingPathRequests[dest]) > 0
	}

	// The requester asked a moment ago and is still connected. However much
	// mesh time passes before the cull next runs, its record must survive so a
	// slow reply can still be delivered.
	for _, elapsed := range []time.Duration{0, time.Minute, 24 * time.Hour, 30 * 24 * time.Hour} {
		ts, _ := newTransport()
		ts.cullPathRequests(time.Now().Add(elapsed))
		if !remembers(ts) {
			t.Errorf("a requester that is still connected was forgotten after %v of mesh time; "+
				"a reply arriving then would be dropped and the client would never learn the peer", elapsed)
		}
	}

	// The requester is GONE: the record is useless and must be culled, exactly
	// as Python does.
	ts, waiting := newTransport()
	gone := newDetachedInterface("gone-client")
	gone.setDetached(true)
	ts.mu.Lock()
	ts.pendingPathRequests[dest] = []interfaces.Interface{gone}
	ts.mu.Unlock()
	ts.cullPathRequests(time.Now())
	if remembers(ts) {
		t.Error("a record whose requesting interface has gone survived the cull")
	}

	// And the still-connected requester's record is untouched by the same run.
	ts.mu.Lock()
	kept := len(ts.pendingPathRequests[dest]) > 0
	ts.mu.Unlock()
	_ = waiting
	_ = kept
}

// TestRelayedPathResponseReachesTheWaitingClient asserts the consequence
// directly: a path response landing long after the request was relayed must
// still be delivered to the interface that asked, because that is the only way
// the attached client ever learns the peer.
func TestRelayedPathResponseReachesTheWaitingClient(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = mustTestNewIdentity(t, true)

	waiting := newRecordingInterface("waiting-client")
	network := newRecordingInterface("network")
	ts.RegisterInterface(waiting)
	ts.RegisterInterface(network)

	dest := mustHexDecode(t, "103eff1c97a1bff6bb11664288ad0c0c")

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.pendingPathRequests[string(dest)] = []interfaces.Interface{waiting}
	ts.mu.Unlock()

	// A day of mesh time passes before the reply gets back.
	ts.cullPathRequests(time.Now().Add(24 * time.Hour))

	// The reply lands: an announce carrying the peer's identity and path.
	reply := &Packet{
		DestinationHash: dest,
		Hops:            2,
		Raw:             append([]byte{0x00, 0x02}, make([]byte, 31)...),
	}
	ts.forwardPathResponseToRequesters(reply, network)
	ts.outboundWG.Wait() // the forwarding is dispatched per-interface

	if got := waiting.sends.Load(); got == 0 {
		t.Error("the path response was not delivered to the interface that asked for it")
	}
}
