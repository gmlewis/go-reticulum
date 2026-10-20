// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync/atomic"
	"testing"
	"time"
)

// queueTransport returns a transport with the inbound queue created but no
// drainer running, so a test can assert what has and has not been dispatched
// and drain the queue by hand.
func queueTransport(t *testing.T, sizes [NumTrafficClasses]int) *TransportSystem {
	t.Helper()
	ts := NewTransportSystem(nil)
	ts.mu.Lock()
	ts.inboundQueueLengths = sizes
	ts.enableInboundQueueLocked()
	ts.mu.Unlock()
	return ts
}

// plainCountingDest registers a PLAIN destination whose packet callback counts
// deliveries and returns a packed packet addressed to it.
func plainCountingDest(t *testing.T, ts *TransportSystem, aspect, payload string) ([]byte, *atomic.Int32) {
	t.Helper()
	dest := mustTestNewDestination(t, ts, nil, DestinationIn, DestinationPlain, "queuetest", aspect)
	ts.RegisterDestination(dest)
	var got atomic.Int32
	dest.SetPacketCallback(func(data []byte, p *Packet) { got.Add(1) })
	p := NewPacket(dest, []byte(payload))
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return p.Raw, &got
}

// TestInboundQueueDefersUntilDrainerRuns pins the queue boundary: with the
// queue enabled, Inbound preprocesses and enqueues but dispatches nothing
// until the drainer takes the packet (Python Transport.inbound → inbound_job,
// Transport.py:1799-1815).
func TestInboundQueueDefersUntilDrainerRuns(t *testing.T) {
	t.Parallel()
	ts := queueTransport(t, [NumTrafficClasses]int{4, 4, 4, 4})
	raw, got := plainCountingDest(t, ts, "defer", "hello-deferred")
	iface := &dummyInterface{name: "dummy"}

	ts.Inbound(raw, iface)
	if got.Load() != 0 {
		t.Fatalf("packet dispatched before the drainer ran (got=%v, want 0)", got.Load())
	}
	if n := ts.inboundQueues.QSizeOf(TCData); n != 1 {
		t.Fatalf("TCData queue height = %v, want 1", n)
	}

	packet, err := ts.inboundQueues.TryGet()
	if err != nil {
		t.Fatalf("TryGet: %v", err)
	}
	ts.processInboundPacket(packet)
	if got.Load() != 1 {
		t.Fatalf("packet not dispatched when the drainer ran (got=%v, want 1)", got.Load())
	}
}

// TestInboundQueueClassifiesTraffic pins Python's classification
// (Transport.py:1706-1778): announces are TC_ANNOUNCE, path requests are
// TC_PATH_REQUEST, and everything else is TC_DATA.
func TestInboundQueueClassifiesTraffic(t *testing.T) {
	t.Parallel()
	ts := queueTransport(t, [NumTrafficClasses]int{4, 4, 4, 4})
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)
	iface := &dummyInterface{name: "dummy"}

	// A valid announce.
	id := mustTestNewIdentity(t, true)
	announceDest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "queue-classify")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	announce := mustTestAnnouncePacketWithEmission(t, ts, id, announceDest, uint64(time.Now().Unix()))
	if err := announce.Pack(); err != nil {
		t.Fatalf("Pack announce: %v", err)
	}

	// A path request to the transport's path-request destination.
	prDest, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("NewDestination(pr): %v", err)
	}
	ts.mu.Lock()
	ts.pathRequestHash = copyBytes(prDest.Hash)
	ts.mu.Unlock()
	pr := NewPacket(prDest, append(copyBytes(prDest.Hash), make([]byte, TruncatedHashLength/8)...))
	if err := pr.Pack(); err != nil {
		t.Fatalf("Pack path request: %v", err)
	}

	// An ordinary data packet to an unknown destination.
	dataRaw, _ := plainCountingDest(t, ts, "plain-data", "hello-data")

	ts.Inbound(announce.Raw, iface)
	ts.Inbound(pr.Raw, iface)
	ts.Inbound(dataRaw, iface)

	total, heights, dropped := ts.inboundQueues.Snapshot()
	if want := (struct {
		total   int
		heights [NumTrafficClasses]int
		dropped [NumTrafficClasses]int
	}{3, [NumTrafficClasses]int{1, 1, 1, 0}, [NumTrafficClasses]int{}}); total != want.total ||
		heights != want.heights || dropped != want.dropped {
		t.Errorf("queue snapshot = (%v, %v, %v), want (%v, %v, %v)",
			total, heights, dropped, want.total, want.heights, want.dropped)
	}
}

// TestInboundQueueFullDropsAndCounts pins the drop path
// (Transport.py:1802-1804): a full class drops its packet, counts the drop on
// that class, and leaves the other classes alone.
func TestInboundQueueFullDropsAndCounts(t *testing.T) {
	t.Parallel()
	ts := queueTransport(t, [NumTrafficClasses]int{1, 4, 4, 4})
	iface := &dummyInterface{name: "dummy"}

	first, _ := plainCountingDest(t, ts, "full-a", "first")
	second, _ := plainCountingDest(t, ts, "full-b", "second")
	ts.Inbound(first, iface)
	ts.Inbound(second, iface)

	_, heights, dropped := ts.inboundQueues.Snapshot()
	if heights[TCData] != 1 {
		t.Errorf("TCData queue height = %v, want 1 (only one fits)", heights[TCData])
	}
	if dropped[TCData] != 1 {
		t.Errorf("TCData dropped = %v, want 1", dropped[TCData])
	}
	for class := TCAnnounce; class < NumTrafficClasses; class++ {
		if dropped[class] != 0 {
			t.Errorf("class %v dropped = %v, want 0 (a full class must not affect the others)", class, dropped[class])
		}
	}
}

// TestHeldAnnounceReleaseCarriesIngressLimited pins the held-announce release
// path: Python re-injects a held announce with tc=TC_INGRESS_LIMITED
// (Interfaces/Interface.py:275) and stamps packet.traffic_class with it
// (Transport.py:1801).
func TestHeldAnnounceReleaseCarriesIngressLimited(t *testing.T) {
	t.Parallel()
	ts := queueTransport(t, [NumTrafficClasses]int{4, 4, 4, 4})
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)
	iface := &dummyInterface{name: "dummy"}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "queue-held")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	announce := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix()))
	if err := announce.Pack(); err != nil {
		t.Fatalf("Pack announce: %v", err)
	}

	// The held release path.
	ts.inbound(announce.Raw, iface, TCIngressLimited, true)
	if n := ts.inboundQueues.QSizeOf(TCIngressLimited); n != 1 {
		t.Fatalf("TCIngressLimited queue height = %v, want 1", n)
	}
	packet, err := ts.inboundQueues.TryGet()
	if err != nil {
		t.Fatalf("TryGet: %v", err)
	}
	if packet.TrafficClass == nil || *packet.TrafficClass != TCIngressLimited {
		t.Errorf("held announce TrafficClass = %v, want %v", packet.TrafficClass, TCIngressLimited)
	}

	// An ordinary announce carries no traffic class, because Python only
	// stamps packet.traffic_class for the ingress-limited case. A second
	// destination is used because the first announce's hash is now in the
	// deduplication list.
	id2 := mustTestNewIdentity(t, true)
	dest2, err := NewDestination(nil, id2, DestinationIn, DestinationSingle, "queue-held-2")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	announce2 := mustTestAnnouncePacketWithEmission(t, ts, id2, dest2, uint64(time.Now().Unix()))
	if err := announce2.Pack(); err != nil {
		t.Fatalf("Pack announce: %v", err)
	}
	ts.Inbound(announce2.Raw, iface)
	if n := ts.inboundQueues.QSizeOf(TCAnnounce); n != 1 {
		t.Fatalf("TCAnnounce queue height = %v, want 1", n)
	}
	ordinary, err := ts.inboundQueues.TryGet()
	if err != nil {
		t.Fatalf("TryGet (ordinary): %v", err)
	}
	if ordinary.TrafficClass != nil {
		t.Errorf("ordinary announce TrafficClass = %v, want nil", *ordinary.TrafficClass)
	}
}

// ifacRejectingInterface is a dummy interface that rejects every frame in the
// IFAC step and records that the step ran.
type ifacRejectingInterface struct {
	*dummyInterface

	applied atomic.Int32
}

func (i *ifacRejectingInterface) ApplyIFACInbound(data []byte) ([]byte, bool) {
	i.applied.Add(1)
	return nil, false
}

// TestHeldAnnounceReleaseSkipsIFAC pins ifac_handled=True on the held-announce
// release (Interfaces/Interface.py:275): the held bytes are the post-IFAC
// frame, so re-running the IFAC check would reject a perfectly valid announce.
func TestHeldAnnounceReleaseSkipsIFAC(t *testing.T) {
	t.Parallel()
	ts := queueTransport(t, [NumTrafficClasses]int{4, 4, 4, 4})
	ts.identity = mustTestNewIdentity(t, true)
	iface := &ifacRejectingInterface{dummyInterface: &dummyInterface{name: "ifac"}}

	raw, _ := plainCountingDest(t, ts, "ifac-skip", "hello")
	ts.inbound(raw, iface, TCIngressLimited, true)
	if applied := iface.applied.Load(); applied != 0 {
		t.Errorf("IFAC step ran on an already-unwrapped frame (%v times)", applied)
	}
	if n := ts.inboundQueues.QSizeOf(TCIngressLimited); n != 1 {
		t.Errorf("TCIngressLimited queue height = %v, want 1", n)
	}

	// The ordinary path still applies IFAC.
	ts.Inbound(raw, iface)
	if applied := iface.applied.Load(); applied != 1 {
		t.Errorf("IFAC step ran %v times on the ordinary path, want 1", applied)
	}
	if n := ts.inboundQueues.QSize(); n != 1 {
		t.Errorf("queue height = %v, want 1 (the rejected frame must not be enqueued)", n)
	}
}
