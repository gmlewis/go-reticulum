// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestTransport(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	ts := NewTransportSystem(nil)
	if err := ts.Start(tmpDir); err != nil {
		t.Fatalf("Transport start failed: %v", err)
	}

	if ts.identity == nil {
		t.Errorf("Transport identity not initialized")
	}

	// Test registration
	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"app")

	ts.mu.Lock()
	found := slices.Contains(ts.destinations, dest)
	ts.mu.Unlock()

	if !found {
		t.Errorf("Destination not registered in Transport")
	}
}

func TestHandleAnnounce(t *testing.T) {
	t.Parallel()
	// LogLevel = LogDebug
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"testapp")
	// Deregister so handleAnnounce treats it as a remote destination.
	// Python (Transport.py:1767-1772) skips the entire path-install/rebroadcast
	// block for local destinations; tests of announce reception must use a
	// non-local destination to exercise that code path.
	delete(ts.destinationsMap, string(dest.Hash))

	// Create announce data
	// nameHash is calculated from ExpandName(nil, appName, aspects...)
	nameHash := FullHash([]byte("testapp"))[:NameHashLength/8]
	randomHash := make([]byte, 10)
	for i := range randomHash {
		randomHash[i] = byte(i)
	}

	// signed_data = destination_hash+public_key+name_hash+random_hash+ratchet+app_data
	signedData := make([]byte, 0, 128)
	signedData = append(signedData, dest.Hash...)
	signedData = append(signedData, id.GetPublicKey()...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomHash...)

	sig, _ := id.Sign(signedData)

	announceData := make([]byte, 0, 256)
	announceData = append(announceData, id.GetPublicKey()...)
	announceData = append(announceData, nameHash...)
	announceData = append(announceData, randomHash...)
	announceData = append(announceData, sig...)

	p := NewPacket(dest, announceData)
	p.PacketType = PacketAnnounce
	p.Data = announceData // Ensure Data matches what we signed
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	// Simulate receiving on an interface
	iface := &dummyInterface{name: "dummy"}
	ts.handleAnnounce(p, iface)

	if !ts.HasPath(dest.Hash) {
		t.Errorf("Transport did not learn path from announce")
	}

	if len(ts.announceTable) == 0 {
		t.Errorf("Transport did not schedule announce re-broadcast")
	}
}

// TestHandleAnnounceGravityWeightedPathReplacement verifies the v1.4.1
// gravity-weighted path replacement (RNS/Transport.py:1821-1845): when an
// announce arrives with hops <= the existing path entry and the SAME emission
// timebase, the path is replaced only if the receiving interface's gravity is
// strictly higher than the current entry's. A same-timebase announce on an
// equal-or-lower-gravity interface must NOT replace the path.
func TestHandleAnnounceGravityWeightedPathReplacement(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	lowGrav := &capturingInterface{name: "low-gravity", gravity: 1}
	highGrav := &capturingInterface{name: "high-gravity", gravity: 10}
	ts.interfaces = append(ts.interfaces, lowGrav, highGrav)

	remoteID := mustTestNewIdentity(t, true)
	remoteDest, err := NewDestination(nil, remoteID, DestinationIn, DestinationSingle, "gravpath")
	if err != nil {
		t.Fatalf("remote dest: %v", err)
	}

	mkAnnounce := func() *Packet {
		p := mustTestAnnouncePacketWithEmission(t, nil, remoteID, remoteDest, 5)
		p.Hops = 2
		if len(p.Raw) > 1 {
			p.Raw[1] = 2
		}
		return p
	}

	// First announce on the low-gravity interface → path learned via it.
	ts.handleAnnounce(mkAnnounce(), lowGrav)
	entry, ok := ts.pathTable[string(remoteDest.Hash)]
	if !ok {
		t.Fatal("expected path after first announce")
	}
	if entry.Interface != lowGrav {
		t.Fatalf("expected initial path via low-gravity iface, got %v", entry.InterfaceName)
	}

	// Same emission timebase, same hops, on a higher-gravity interface → replace.
	ts.handleAnnounce(mkAnnounce(), highGrav)
	entry = ts.pathTable[string(remoteDest.Hash)]
	if entry.Interface != highGrav {
		t.Fatalf("expected path replaced to high-gravity iface, got %v", entry.InterfaceName)
	}

	// Same emission timebase on an equal-or-lower-gravity interface → NO replace.
	ts.handleAnnounce(mkAnnounce(), lowGrav)
	entry = ts.pathTable[string(remoteDest.Hash)]
	if entry.Interface != highGrav {
		t.Fatalf("expected path to remain on high-gravity iface after lower-gravity same-timebase announce, got %v", entry.InterfaceName)
	}
}

// TestRequestPathAlwaysSendsAndTag verifies that RequestPath always transmits a
// path request — Python's Transport.request_path has no min-interval dedup and
// sends on every call (Transport.py:2771-2815) — and stamps a fresh random tag
// on each request. A prior Go-only 20s dedup silently dropped legitimate retries
// (for example after a held or dropped first response); it was removed for
// Python parity, so a repeat request for the same destination transmits again.
func TestRequestPathAlwaysSendsAndTag(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	capIface := &capturingInterface{name: "cap"}
	ts.interfaces = append(ts.interfaces, capIface)

	destHash := bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)
	if err := ts.RequestPath(destHash); err != nil {
		t.Fatalf("first RequestPath failed: %v", err)
	}
	if capIface.sendCount != 1 {
		t.Fatalf("expected one request transmission, got %v", capIface.sendCount)
	}

	if err := ts.RequestPath(destHash); err != nil {
		t.Fatalf("second RequestPath failed: %v", err)
	}
	if capIface.sendCount != 2 {
		t.Fatalf("expected second request to transmit (no dedup, for parity), got %v sends", capIface.sendCount)
	}

	p := NewPacketFromRaw(capIface.lastSent)
	if err := p.Unpack(); err != nil {
		t.Fatalf("failed unpacking path request packet: %v", err)
	}

	if len(p.Data) != (TruncatedHashLength/8)*2 {
		t.Fatalf("expected path request data length %v, got %v", (TruncatedHashLength/8)*2, len(p.Data))
	}

	if !bytes.Equal(p.Data[:TruncatedHashLength/8], destHash) {
		t.Fatalf("path request destination hash mismatch")
	}

	tag := p.Data[TruncatedHashLength/8:]
	if bytes.Equal(tag, make([]byte, len(tag))) {
		t.Fatalf("expected random non-zero request tag")
	}
}

// TestOutboundPRRecordsSentPathRequest verifies that transmitting an outbound
// path request records an outgoing PR on each transmitting interface
// (Transport.py:1354: when packet.destination.type == PLAIN and
// packet.is_outbound_pr, interface.sent_path_request()). RequestPath stamps
// IsOutboundPR on the packet (Transport.py:2896), so each interface that
// broadcasts the PR advances its outbound-PR counter.
func TestOutboundPRRecordsSentPathRequest(t *testing.T) {
	t.Parallel()

	t.Run("RequestPath advances outbound PR counter", func(t *testing.T) {
		t.Parallel()
		ts := NewTransportSystem(nil)
		capIface := &capturingInterface{name: "cap"}
		ts.interfaces = append(ts.interfaces, capIface)

		destHash := bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)
		if err := ts.RequestPath(destHash); err != nil {
			t.Fatalf("RequestPath failed: %v", err)
		}
		if capIface.sendCount != 1 {
			t.Fatalf("expected 1 transmission, got %v", capIface.sendCount)
		}
		if capIface.sentPRCount != 1 {
			t.Fatalf("expected sentPRCount 1 after RequestPath, got %v", capIface.sentPRCount)
		}
	})

	t.Run("PLAIN packet with IsOutboundPR advances counter", func(t *testing.T) {
		t.Parallel()
		ts := NewTransportSystem(nil)
		capIface := &capturingInterface{name: "cap"}
		ts.interfaces = append(ts.interfaces, capIface)

		dest := mustTestNewDestination(t, ts, nil, DestinationOut, DestinationPlain, "pr-test")
		p := NewPacketWithTransport(ts, dest, []byte("hello"))
		p.IsOutboundPR = true
		if err := p.Pack(); err != nil {
			t.Fatalf("pack failed: %v", err)
		}
		if err := ts.Outbound(p); err != nil {
			t.Fatalf("outbound failed: %v", err)
		}
		if capIface.sentPRCount != 1 {
			t.Fatalf("expected sentPRCount 1 for PLAIN IsOutboundPR packet, got %v", capIface.sentPRCount)
		}
	})

	t.Run("PLAIN packet without IsOutboundPR does not advance counter", func(t *testing.T) {
		t.Parallel()
		ts := NewTransportSystem(nil)
		capIface := &capturingInterface{name: "cap"}
		ts.interfaces = append(ts.interfaces, capIface)

		dest := mustTestNewDestination(t, ts, nil, DestinationOut, DestinationPlain, "plain-test")
		p := NewPacketWithTransport(ts, dest, []byte("hello"))
		// IsOutboundPR left false (zero value)
		if err := p.Pack(); err != nil {
			t.Fatalf("pack failed: %v", err)
		}
		if err := ts.Outbound(p); err != nil {
			t.Fatalf("outbound failed: %v", err)
		}
		if capIface.sendCount != 1 {
			t.Fatalf("expected 1 transmission, got %v", capIface.sendCount)
		}
		if capIface.sentPRCount != 0 {
			t.Fatalf("expected sentPRCount 0 for non-PR PLAIN packet, got %v", capIface.sentPRCount)
		}
	})

	t.Run("non-PLAIN packet with IsOutboundPR does not advance counter", func(t *testing.T) {
		t.Parallel()
		ts := NewTransportSystem(nil)
		capIface := &capturingInterface{name: "cap"}
		ts.interfaces = append(ts.interfaces, capIface)

		remoteID := mustTestNewIdentity(t, true)
		// DestinationSingle (not PLAIN) so the IsOutboundPR gate must not fire.
		dest := mustTestNewDestination(t, ts, remoteID, DestinationOut, DestinationSingle, "single-test")
		ts.pathTable[string(dest.Hash)] = &PathEntry{Interface: capIface, Hops: 1, Expires: time.Now().Add(time.Hour)}
		p := NewPacketWithTransport(ts, dest, []byte("hello"))
		p.IsOutboundPR = true
		if err := p.Pack(); err != nil {
			t.Fatalf("pack failed: %v", err)
		}
		if err := ts.Outbound(p); err != nil {
			t.Fatalf("outbound failed: %v", err)
		}
		if capIface.sendCount != 1 {
			t.Fatalf("expected 1 transmission, got %v", capIface.sendCount)
		}
		if capIface.sentPRCount != 0 {
			t.Fatalf("expected sentPRCount 0 for non-PLAIN IsOutboundPR packet, got %v", capIface.sentPRCount)
		}
	})
}

func TestHandlePathRequestEmitsTargetedPathResponse(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	tsid := mustTestNewIdentity(t, true)
	ts.identity = tsid

	recvIface := &capturingInterface{name: "recv"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, recvIface, otherIface)

	localID := mustTestNewIdentity(t, true)
	localDest := mustTestNewDestination(t, ts, localID, DestinationIn, DestinationSingle, "pathreq", "target")

	tag := bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)
	requestData := make([]byte, 0, TruncatedHashLength/4)
	requestData = append(requestData, localDest.Hash...)
	requestData = append(requestData, tag...)

	requestPacket := &Packet{ReceivingInterface: recvIface}
	ts.handlePathRequest(requestData, requestPacket)

	if recvIface.sendCount != 1 {
		t.Fatalf("expected one targeted path response on receiving interface, got %v", recvIface.sendCount)
	}
	if otherIface.sendCount != 0 {
		t.Fatalf("expected no path response on non-requesting interface, got %v", otherIface.sendCount)
	}

	response := NewPacketFromRaw(recvIface.lastSent)
	if err := response.Unpack(); err != nil {
		t.Fatalf("failed unpacking path response packet: %v", err)
	}
	if response.PacketType != PacketAnnounce {
		t.Fatalf("expected announce packet type for path response, got %v", response.PacketType)
	}
	if response.Context != ContextPathResponse {
		t.Fatalf("expected ContextPathResponse, got %v", response.Context)
	}
	if response.HeaderType != Header2 {
		t.Fatalf("expected Header2 path response, got %v", response.HeaderType)
	}
	if response.TransportType != TransportForward {
		t.Fatalf("expected TransportForward path response, got %v", response.TransportType)
	}
	if !bytes.Equal(response.TransportID, ts.identity.Hash) {
		t.Fatalf("expected path response transport ID to match transport identity")
	}
	if !bytes.Equal(response.DestinationHash, localDest.Hash) {
		t.Fatalf("expected path response destination hash to match local destination")
	}
}

// TestHandlePathRequestTagDedup verifies the path-request tag dedup that
// mirrors Python's Transport.path_request_handler (Transport.py:2984-2987): a
// path request is only answered the first time its unique tag
// (destination_hash + tag_bytes) is seen; an identical follow-up request is
// silently consumed (handled, but not answered or relayed). A request with a
// different tag is answered independently, and a tagless request (no bytes
// beyond the destination hash) is dropped entirely ("Ignoring tagless path
// request"), matching Python's else branch.
func TestHandlePathRequestTagDedup(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	recvIface := &capturingInterface{name: "recv"}
	ts.interfaces = append(ts.interfaces, recvIface)

	localID := mustTestNewIdentity(t, true)
	localDest := mustTestNewDestination(t, ts, localID, DestinationIn, DestinationSingle, "pr-dedup", "target")

	hashLen := TruncatedHashLength / 8
	tagA := bytes.Repeat([]byte{0x11}, hashLen)
	tagB := bytes.Repeat([]byte{0x22}, hashLen)
	req := func(tag []byte) []byte {
		d := make([]byte, 0, len(localDest.Hash)+len(tag))
		d = append(d, localDest.Hash...)
		d = append(d, tag...)
		return d
	}
	pkt := &Packet{ReceivingInterface: recvIface}

	// First request with tag A is answered on the receiving interface.
	if !ts.handlePathRequest(req(tagA), pkt) {
		t.Fatal("first request with tag A: want handled=true, got false")
	}
	if recvIface.sendCount != 1 {
		t.Fatalf("after first tag A request: sendCount=%v, want 1", recvIface.sendCount)
	}

	// A duplicate request with the same unique tag is consumed, not answered.
	if !ts.handlePathRequest(req(tagA), pkt) {
		t.Fatal("duplicate tag A request: want handled=true (consumed), got false")
	}
	if recvIface.sendCount != 1 {
		t.Fatalf("after duplicate tag A request: sendCount=%v, want 1 (deduped)", recvIface.sendCount)
	}

	// A request with a different tag is answered independently.
	if !ts.handlePathRequest(req(tagB), pkt) {
		t.Fatal("tag B request: want handled=true, got false")
	}
	if recvIface.sendCount != 2 {
		t.Fatalf("after tag B request: sendCount=%v, want 2", recvIface.sendCount)
	}

	// A tagless request (destination hash only) is dropped, not answered and
	// not relayed (handled=true means consumed, matching Python's "Ignoring
	// tagless path request" branch).
	tagless := append([]byte(nil), localDest.Hash...)
	if !ts.handlePathRequest(tagless, pkt) {
		t.Fatal("tagless request: want handled=true (dropped), got false")
	}
	if recvIface.sendCount != 2 {
		t.Fatalf("after tagless request: sendCount=%v, want 2 (dropped)", recvIface.sendCount)
	}
}

// TestCullDiscoveryPRTags verifies the path-request tag set is bounded to
// maxPRTags entries and that eviction removes the oldest entries from both
// the insertion-ordered slice and the membership set, mirroring Python's
// Transport.py:674-676 cull. It drives the cull directly with a slice larger
// than maxPRTags rather than emitting 32000+ real requests.
func TestCullDiscoveryPRTags(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.ensureStateLocked()

	// Seed maxPRTags + 50 unique tags; the cull must drop the 50 oldest.
	total := maxPRTags + 50
	ts.discoveryPRTags = make([]string, 0, total)
	ts.discoveryPRTagsSet = make(map[string]struct{}, total)
	for i := range total {
		key := string([]byte{byte(i >> 8), byte(i)})
		ts.discoveryPRTags = append(ts.discoveryPRTags, key)
		ts.discoveryPRTagsSet[key] = struct{}{}
	}

	ts.discoveryPRTagsMu.Lock()
	ts.cullDiscoveryPRTagsLocked()
	ts.discoveryPRTagsMu.Unlock()

	if len(ts.discoveryPRTags) != maxPRTags {
		t.Fatalf("after cull: len=%v, want %v", len(ts.discoveryPRTags), maxPRTags)
	}
	if len(ts.discoveryPRTagsSet) != maxPRTags {
		t.Fatalf("after cull: set size=%v, want %v", len(ts.discoveryPRTagsSet), maxPRTags)
	}
	// The oldest 50 entries (indices 0..49) must be evicted from the set.
	for i := range 50 {
		b := []byte{byte(i >> 8), byte(i)}
		if _, ok := ts.discoveryPRTagsSet[string(b)]; ok {
			t.Fatalf("evicted tag %v still present in set after cull", i)
		}
	}
	// The most recent maxPRTags entries (indices 50..total-1) must remain.
	for i := 50; i < total; i++ {
		b := []byte{byte(i >> 8), byte(i)}
		if _, ok := ts.discoveryPRTagsSet[string(b)]; !ok {
			t.Fatalf("retained tag %v missing from set after cull", i)
		}
	}
	// A no-op cull at or below the limit must not change anything.
	ts.discoveryPRTagsMu.Lock()
	ts.cullDiscoveryPRTagsLocked()
	ts.discoveryPRTagsMu.Unlock()
	if len(ts.discoveryPRTags) != maxPRTags || len(ts.discoveryPRTagsSet) != maxPRTags {
		t.Fatalf("no-op cull changed sizes: slice=%v set=%v", len(ts.discoveryPRTags), len(ts.discoveryPRTagsSet))
	}
}

// TestHandlePathRequestAnswersFromCachedPath verifies the cached-path branch
// of handlePathRequest: when a transport node already knows a path to a
// REMOTE destination (not local to it), it answers a path request by replaying
// the cached announce as a path response, instead of relaying the request
// onward and relying on a round-trip to the remote node. This mirrors Python
// Reticulum's "elif (transport_enabled or is_from_local_client) and
// (destination_hash in path_table)" branch and is what makes
// sparsely-announcing destinations (e.g. a nomadnet node announcing every 60
// minutes) reachable by a freshly-started client within the request timeout.
func TestHandlePathRequestAnswersFromCachedPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true) // this node is transport-enabled
	ts.connectedToSharedInstance = false

	recvIface := &capturingInterface{name: "recv"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, recvIface, otherIface)

	// A "remote" destination that lives on another node. Created with a nil
	// transport so it is NOT registered as local to ts; handlePathRequest must
	// therefore not find it local and must answer from the cached path.
	remoteID := mustTestNewIdentity(t, true)
	remoteDest, err := NewDestination(nil, remoteID, DestinationIn, DestinationSingle, "cacheremote")
	if err != nil {
		t.Fatalf("remote dest: %v", err)
	}

	// ts learns a path to remoteDest by receiving its announce on otherIface.
	announce := mustTestAnnouncePacketWithEmission(t, nil, remoteID, remoteDest, 1)
	announce.Hops = 2
	if len(announce.Raw) > 1 {
		announce.Raw[1] = 2
	}
	ts.handleAnnounce(announce, otherIface)

	entry, ok := ts.pathTable[string(remoteDest.Hash)]
	if !ok {
		t.Fatalf("expected ts to have cached a path to the remote dest")
	}
	if len(entry.Packet) == 0 {
		t.Fatalf("expected cached path entry to store the raw announce for replay")
	}

	// A freshly-started client requests the path to remoteDest. The request
	// arrives on recvIface; the requestor's transport ID is its own identity
	// (not the next hop toward the destination), so loop prevention must not
	// suppress the answer.
	reqID := mustTestNewIdentity(t, true)
	tag := bytes.Repeat([]byte{0xCD}, TruncatedHashLength/8)
	requestData := make([]byte, 0, len(remoteDest.Hash)+len(reqID.Hash)+len(tag))
	requestData = append(requestData, remoteDest.Hash...)
	requestData = append(requestData, reqID.Hash...)
	requestData = append(requestData, tag...)

	handled := ts.handlePathRequest(requestData, &Packet{ReceivingInterface: recvIface})
	if !handled {
		t.Fatalf("expected handlePathRequest to answer from cached path, but it returned false")
	}
	if recvIface.sendCount != 1 {
		t.Fatalf("expected one cached path response on recv interface, got %v", recvIface.sendCount)
	}
	if otherIface.sendCount != 0 {
		t.Fatalf("expected no response on non-requesting interface, got %v", otherIface.sendCount)
	}

	response := NewPacketFromRaw(recvIface.lastSent)
	if err := response.Unpack(); err != nil {
		t.Fatalf("failed unpacking cached path response: %v", err)
	}
	if response.PacketType != PacketAnnounce {
		t.Fatalf("expected announce packet type, got %v", response.PacketType)
	}
	if response.Context != ContextPathResponse {
		t.Fatalf("expected ContextPathResponse, got %v", response.Context)
	}
	if response.HeaderType != Header2 {
		t.Fatalf("expected Header2 cached path response, got %v", response.HeaderType)
	}
	if !bytes.Equal(response.TransportID, ts.identity.Hash) {
		t.Fatalf("expected cached path response transport ID to be this node's identity")
	}
	if !bytes.Equal(response.DestinationHash, remoteDest.Hash) {
		t.Fatalf("expected cached path response destination hash to match remote dest")
	}
	// Python (Transport.py:3049): packet.hops = path_table[IDX_PT_HOPS],
	// i.e. the cached path's hop count. The requestor increments it by 1
	// in inbound, storing cached+1. The response itself carries cached_hops.
	if int(response.Hops) != entry.Hops {
		t.Fatalf("expected hops = cached = %v, got %v (Python: packet.hops = path_table[IDX_PT_HOPS])", entry.Hops, response.Hops)
	}

	// The replayed announce must still carry a valid remote-identity
	// signature: the header was rebuilt (new next hop, PathResponse context,
	// adjusted hops) but the signed payload is unchanged, so ValidateAnnounce
	// must still pass.
	if !ValidateAnnounce(ts, response) {
		t.Fatalf("replayed cached path response failed announce validation (signature broken)")
	}
}

// TestHandlePathRequestCachedPathLoopPrevention verifies that when the path
// request's requestor IS the next hop toward the cached destination, the node
// consumes the request without answering (answering would echo the announce
// back along the path it already traveled).
func TestHandlePathRequestCachedPathLoopPrevention(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.connectedToSharedInstance = false

	recvIface := &capturingInterface{name: "recv"}
	ts.interfaces = append(ts.interfaces, recvIface)

	remoteID := mustTestNewIdentity(t, true)
	remoteDest, err := NewDestination(nil, remoteID, DestinationIn, DestinationSingle, "cacheloop")
	if err != nil {
		t.Fatalf("remote dest: %v", err)
	}
	announce := mustTestAnnouncePacketWithEmission(t, nil, remoteID, remoteDest, 1)
	announce.Hops = 2
	if len(announce.Raw) > 1 {
		announce.Raw[1] = 2
	}
	ts.handleAnnounce(announce, recvIface)

	entry := ts.pathTable[string(remoteDest.Hash)]
	if entry == nil {
		t.Fatalf("expected cached path")
	}

	// Build a request whose requestor transport ID equals the cached next
	// hop. The announce was Header1 (no transport id), so nextHop fell back
	// to the destination hash; use that as the requestor ID to trigger loop
	// prevention.
	nextHop := entry.NextHop
	tag := bytes.Repeat([]byte{0xEE}, TruncatedHashLength/8)
	requestData := make([]byte, 0, len(remoteDest.Hash)+len(nextHop)+len(tag))
	requestData = append(requestData, remoteDest.Hash...)
	requestData = append(requestData, nextHop...)
	requestData = append(requestData, tag...)

	handled := ts.handlePathRequest(requestData, &Packet{ReceivingInterface: recvIface})
	if !handled {
		t.Fatalf("expected loop-prevention to consume the request (return true), got false")
	}
	if recvIface.sendCount != 0 {
		t.Fatalf("expected no path response when requestor is next hop, got %v sends", recvIface.sendCount)
	}
}

func TestAnnounceRebroadcastProcessing(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	tsID := mustTestNewIdentity(t, true)
	ts.identity = tsID
	ts.SetEnabled(true)

	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound"}
	ts.interfaces = append(ts.interfaces, source, outbound)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"testapp")
	delete(ts.destinationsMap, string(dest.Hash))

	nameHash := FullHash([]byte("testapp"))[:NameHashLength/8]
	randomHash := make([]byte, 10)
	for i := range randomHash {
		randomHash[i] = byte(i)
	}

	signedData := make([]byte, 0, 128)
	signedData = append(signedData, dest.Hash...)
	signedData = append(signedData, id.GetPublicKey()...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomHash...)

	sig, _ := id.Sign(signedData)

	announceData := make([]byte, 0, 256)
	announceData = append(announceData, id.GetPublicKey()...)
	announceData = append(announceData, nameHash...)
	announceData = append(announceData, randomHash...)
	announceData = append(announceData, sig...)

	p := NewPacket(dest, announceData)
	p.PacketType = PacketAnnounce
	p.Data = announceData
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	p.Hops = 1

	ts.handleAnnounce(p, source)

	if len(ts.announceTable) != 1 {
		t.Fatalf("expected one queued announce, got %v", len(ts.announceTable))
	}

	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	ts.WaitOutboundSends()

	if outbound.sendCount != 1 {
		t.Fatalf("expected one rebroadcast on outbound interface, got %v", outbound.sendCount)
	}
	// Python (Transport.py:1199-1340) rebroadcasts on ALL interfaces
	// including the source — the source-interface skip was a Go bug.
	// The source is a capturingInterface with ModeFull (default), so the
	// announce-cap rate limiter applies. With the corrected announceCapDefault
	// (0.02) and a fast in-memory interface, the wait may be sub-nanosecond
	// (truncated to 0), so the rebroadcast goes out immediately.
	if source.sendCount == 0 {
		t.Fatalf("expected rebroadcast on source interface too (Python rebroadcasts on all interfaces), got 0")
	}

	rebroadcast := NewPacketFromRaw(outbound.lastSent)
	if err := rebroadcast.Unpack(); err != nil {
		t.Fatalf("failed unpacking rebroadcast packet: %v", err)
	}
	if rebroadcast.HeaderType != Header2 {
		t.Fatalf("expected Header2 rebroadcast, got %v", rebroadcast.HeaderType)
	}
	if rebroadcast.TransportType != TransportForward {
		t.Fatalf("expected TransportForward rebroadcast, got %v", rebroadcast.TransportType)
	}
	if !bytes.Equal(rebroadcast.TransportID, ts.identity.Hash) {
		t.Fatalf("expected rebroadcast transport ID to match transport identity")
	}
}

// TestProcessAnnounceTableStalledPeerDoesNotBlock is the regression test for
// the 0.8.0 unbrowsable-nodes bug: the outbound fan-out (processAnnounceTable,
// forwardPathRequest, forwardPathResponseToRequesters) used to send to every
// interface sequentially on the single maintenance goroutine, so one
// half-open TCP peer whose conn.Write stalled wedged the whole transport —
// link handshakes on every other interface timed out. The fan-out now
// dispatches each send on its own goroutine, so a stalled peer blocks only
// itself.
func TestProcessAnnounceTableStalledPeerDoesNotBlock(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	source := &capturingInterface{name: "source"}
	stalled := &blockingInterface{capturingInterface: capturingInterface{name: "stalled"}, blockFor: 2 * time.Second}
	fast := &capturingInterface{name: "fast"}
	ts.interfaces = append(ts.interfaces, source, stalled, fast)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "stall-test")
	delete(ts.destinationsMap, string(dest.Hash))
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, source)

	// processAnnounceTable fans the rebroadcast out on goroutines. The
	// stalled peer's 2s Send runs in the background, so processAnnounceTable
	// itself must return far sooner — otherwise a single half-open peer
	// wedges maintenance and every link handshake behind it.
	start := time.Now()
	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	if d := time.Since(start); d > time.Second {
		t.Fatalf("processAnnounceTable blocked %v on a stalled peer; fan-out must be concurrent", d)
	}

	// The fast interface still gets its rebroadcast despite the stalled peer.
	// WaitOutboundSends drains the dispatched goroutines, including the
	// stalled peer's (which completes after its 2s block).
	ts.WaitOutboundSends()
	if fast.sendCount != 1 {
		t.Fatalf("expected fast interface to receive rebroadcast despite stalled peer, got %v", fast.sendCount)
	}
	if stalled.sendCount != 1 {
		t.Fatalf("expected stalled interface to eventually receive rebroadcast, got %v", stalled.sendCount)
	}
}

func TestAnnounceQueueQueuesAndDrainsOnCappedInterface(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound", bitrate: 1000}
	ts.interfaces = append(ts.interfaces, source, outbound)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "queued-announce")
	delete(ts.destinationsMap, string(dest.Hash))
	packet := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	packet.Hops = 1

	now := time.Now()
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.announceQueues[outbound] = &announceQueueState{allowedAt: now.Add(time.Minute)}
	ts.mu.Unlock()

	ts.handleAnnounce(packet, source)
	ts.processAnnounceTable(now.Add(10 * time.Second))
	ts.WaitOutboundSends()

	if outbound.sendCount != 0 {
		t.Fatalf("expected announce to be queued during active cap, got %v sends", outbound.sendCount)
	}

	ts.mu.Lock()
	state := ts.announceQueues[outbound]
	if state == nil || len(state.queue) != 1 {
		t.Fatalf("expected one queued announce, got %v", len(state.queue))
	}
	allowedAt := state.allowedAt
	ts.mu.Unlock()

	ts.processAnnounceQueue(outbound, allowedAt.Add(time.Millisecond))
	if outbound.sendCount != 1 {
		t.Fatalf("expected one drained announce, got %v sends", outbound.sendCount)
	}
}

func TestAnnounceQueueDeduplicatesNewerDestination(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound", bitrate: 1000}
	ts.interfaces = append(ts.interfaces, source, outbound)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "queued-dedup")
	delete(ts.destinationsMap, string(dest.Hash))
	first := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	second := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 2)
	first.Hops = 1
	second.Hops = 1

	now := time.Now()
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.announceQueues[outbound] = &announceQueueState{allowedAt: now.Add(time.Minute)}
	ts.mu.Unlock()

	ts.handleAnnounce(first, source)
	ts.processAnnounceTable(now.Add(10 * time.Second))
	ts.WaitOutboundSends()

	ts.handleAnnounce(second, source)
	ts.processAnnounceTable(now.Add(20 * time.Second))
	ts.WaitOutboundSends()

	ts.mu.Lock()
	state := ts.announceQueues[outbound]
	if state == nil || len(state.queue) != 1 {
		t.Fatalf("expected one deduplicated queued announce, got %v", len(state.queue))
	}
	allowedAt := state.allowedAt
	ts.mu.Unlock()

	ts.processAnnounceQueue(outbound, allowedAt.Add(time.Millisecond))
	if outbound.sendCount != 1 {
		t.Fatalf("expected one drained announce, got %v sends", outbound.sendCount)
	}

	rebroadcast := NewPacketFromRaw(outbound.lastSent)
	if err := rebroadcast.Unpack(); err != nil {
		t.Fatalf("failed unpacking rebroadcast packet: %v", err)
	}
	randomBlobStart := IdentityKeySize/8 + NameHashLength/8
	if len(rebroadcast.Data) < randomBlobStart+10 {
		t.Fatalf("rebroadcast data too short for random blob")
	}
	var gotEmission uint64
	for _, b := range rebroadcast.Data[randomBlobStart+5 : randomBlobStart+10] {
		gotEmission = (gotEmission << 8) | uint64(b)
	}
	if gotEmission != 2 {
		t.Fatalf("queued announce emission=%v, want 2", gotEmission)
	}
}

func TestAnnounceQueueBlocksForwardedPathRequests(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound", bitrate: 1000}
	ts.interfaces = append(ts.interfaces, source, outbound)

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.announceQueues[outbound] = &announceQueueState{
		queue: []announceQueueEntry{{destinationHash: "queued"}},
	}
	ts.mu.Unlock()

	pathRequestDst, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("NewDestination(path request): %v", err)
	}

	targetHash := bytes.Repeat([]byte{0xAA}, TruncatedHashLength/8)
	requestTag := bytes.Repeat([]byte{0xBB}, TruncatedHashLength/8)
	packet := NewPacket(pathRequestDst, append(targetHash, requestTag...))
	if err := packet.Pack(); err != nil {
		t.Fatalf("Pack(path request): %v", err)
	}

	ts.forwardPathRequest(packet, source)
	ts.WaitOutboundSends()
	if outbound.sendCount != 0 {
		t.Fatalf("expected queued announces to block forwarded path request, got %v sends", outbound.sendCount)
	}
}

func TestPathResponseAnnounceNotRebroadcast(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound"}
	ts.interfaces = append(ts.interfaces, source, outbound)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"path-response-test")
	delete(ts.destinationsMap, string(dest.Hash))

	nameHash := FullHash([]byte("path-response-test"))[:NameHashLength/8]
	randomHash := make([]byte, 10)
	for i := range randomHash {
		randomHash[i] = byte(i)
	}

	signedData := make([]byte, 0, 128)
	signedData = append(signedData, dest.Hash...)
	signedData = append(signedData, id.GetPublicKey()...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomHash...)

	sig, _ := id.Sign(signedData)

	announceData := make([]byte, 0, 256)
	announceData = append(announceData, id.GetPublicKey()...)
	announceData = append(announceData, nameHash...)
	announceData = append(announceData, randomHash...)
	announceData = append(announceData, sig...)

	p := NewPacket(dest, announceData)
	p.PacketType = PacketAnnounce
	p.Context = ContextPathResponse
	p.Data = announceData
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	p.Hops = 1

	ts.handleAnnounce(p, source)

	if !ts.HasPath(dest.Hash) {
		t.Fatalf("expected path to be learned from path-response announce")
	}
	if len(ts.announceTable) != 0 {
		t.Fatalf("expected no queued rebroadcast for path-response announce")
	}

	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	ts.WaitOutboundSends()
	if outbound.sendCount != 0 {
		t.Fatalf("expected no rebroadcast transmissions for path-response announce, got %v", outbound.sendCount)
	}
}

func TestAnnounceHandlerPathResponseDelivery(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "testapp")
	delete(ts.destinationsMap, string(dest.Hash))

	nameHash := FullHash([]byte("testapp"))[:NameHashLength/8]
	randomHash := make([]byte, 10)
	for i := range randomHash {
		randomHash[i] = byte(i)
	}

	signedData := make([]byte, 0, 128)
	signedData = append(signedData, dest.Hash...)
	signedData = append(signedData, id.GetPublicKey()...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomHash...)

	sig, _ := id.Sign(signedData)

	announceData := make([]byte, 0, 256)
	announceData = append(announceData, id.GetPublicKey()...)
	announceData = append(announceData, nameHash...)
	announceData = append(announceData, randomHash...)
	announceData = append(announceData, sig...)

	packet := NewPacket(dest, announceData)
	packet.PacketType = PacketAnnounce
	packet.Context = ContextPathResponse
	packet.Data = announceData
	if err := packet.Pack(); err != nil {
		t.Fatalf("Pack failed: %v", err)
	}

	var defaultCalled bool
	var extendedCalled bool
	var extendedPathResponse bool
	ts.RegisterAnnounceHandler(&AnnounceHandler{
		AspectFilter: "testapp",
		ReceivedAnnounce: func(destinationHash []byte, announcedIdentity *Identity, appData []byte) {
			defaultCalled = true
		},
	})
	ts.RegisterAnnounceHandler(&AnnounceHandler{
		AspectFilter:         "testapp",
		ReceivePathResponses: true,
		ReceivedAnnounceWithContext: func(destinationHash []byte, announcedIdentity *Identity, appData []byte, isPathResponse bool) {
			extendedCalled = true
			extendedPathResponse = isPathResponse
		},
	})

	ts.handleAnnounce(packet, &dummyInterface{name: "dummy"})

	if defaultCalled {
		t.Fatal("expected default announce handler not to receive path response")
	}
	if !extendedCalled {
		t.Fatal("expected extended announce handler to receive path response")
	}
	if !extendedPathResponse {
		t.Fatal("expected path response flag to be true")
	}
}

func TestInvalidatePathByHash(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	hash := []byte("destination-hash-1")
	ts.pathTable[string(hash)] = &PathEntry{Hops: 1, Expires: time.Now().Add(time.Hour)}
	ts.announceTable[string(hash)] = &AnnounceEntry{}
	ts.pathRequests[string(hash)] = time.Now()

	if removed := ts.InvalidatePath(hash); !removed {
		t.Fatalf("expected path removal to return true")
	}
	if ts.HasPath(hash) {
		t.Fatalf("expected path to be removed")
	}
	if _, ok := ts.announceTable[string(hash)]; ok {
		t.Fatalf("expected announce table entry to be removed")
	}
	if _, ok := ts.pathRequests[string(hash)]; ok {
		t.Fatalf("expected path request entry to be removed")
	}
}

func TestInvalidatePathsViaInterface(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	ifaceA := &capturingInterface{name: "A"}
	ifaceB := &capturingInterface{name: "B"}

	ts.pathTable["a"] = &PathEntry{Interface: ifaceA, Expires: time.Now().Add(time.Hour)}
	ts.pathTable["b"] = &PathEntry{Interface: ifaceA, Expires: time.Now().Add(time.Hour)}
	ts.pathTable["c"] = &PathEntry{Interface: ifaceB, Expires: time.Now().Add(time.Hour)}

	removed := ts.InvalidatePathsViaInterface(ifaceA)
	if removed != 2 {
		t.Fatalf("expected 2 removed paths, got %v", removed)
	}
	if _, ok := ts.pathTable["a"]; ok {
		t.Fatalf("expected path a removed")
	}
	if _, ok := ts.pathTable["b"]; ok {
		t.Fatalf("expected path b removed")
	}
	if _, ok := ts.pathTable["c"]; !ok {
		t.Fatalf("expected path c retained")
	}
}

func TestCullExpiredPaths(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	now := time.Now()
	ts.pathTable["expired"] = &PathEntry{Expires: now.Add(-time.Minute)}
	ts.pathTable["valid"] = &PathEntry{Expires: now.Add(time.Minute)}

	ts.cullExpiredPaths(now)

	if _, ok := ts.pathTable["expired"]; ok {
		t.Fatalf("expected expired path removed")
	}
	if _, ok := ts.pathTable["valid"]; !ok {
		t.Fatalf("expected valid path retained")
	}
}

func TestOutboundSendFailureInvalidatesPaths(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	failing := &failingInterface{name: "failing"}
	good := &capturingInterface{name: "good"}
	ts.interfaces = append(ts.interfaces, failing, good)

	ts.pathTable["via-failing"] = &PathEntry{Interface: failing, Expires: time.Now().Add(time.Hour)}
	ts.pathTable["via-good"] = &PathEntry{Interface: good, Expires: time.Now().Add(time.Hour)}

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"outbound-test")
	p := NewPacket(dest, []byte("hello"))
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	if err := ts.Outbound(p); err != nil {
		t.Fatalf("outbound failed: %v", err)
	}

	if _, ok := ts.pathTable["via-failing"]; ok {
		t.Fatalf("expected path via failing interface to be invalidated")
	}
	if _, ok := ts.pathTable["via-good"]; !ok {
		t.Fatalf("expected path via good interface retained")
	}
}

func TestInboundIFACHookDrop(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"ifac-drop")
	p := NewPacket(dest, []byte("payload"))
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	iface := &ifacDropInterface{name: "ifac-dropper"}
	ts.Inbound(p.Raw, iface)

	if !iface.inboundCalled {
		t.Fatalf("expected IFAC inbound hook to be called")
	}
	if len(ts.packetHashes) != 0 {
		t.Fatalf("expected packet not to enter duplicate cache when IFAC drops ingress")
	}
}

func TestOutboundIFACEgressTransform(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	iface := &ifacTransformInterface{name: "ifac-transform"}
	ts.interfaces = append(ts.interfaces, iface)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle,
		"ifac-out")
	p := NewPacket(dest, []byte("payload"))
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	if err := ts.Outbound(p); err != nil {
		t.Fatalf("outbound failed: %v", err)
	}

	if !iface.outboundCalled {
		t.Fatalf("expected IFAC outbound hook to be called")
	}
	if len(iface.lastSent) == 0 || iface.lastSent[0] != 0xAA {
		t.Fatalf("expected transformed packet prefix in transmitted bytes")
	}
}

func TestOutboundUsesKnownPathSingleHop(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	routeIface := &capturingInterface{name: "route"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, routeIface, otherIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, remoteID, DestinationOut, DestinationSingle, "route-test")
	ts.pathTable[string(dest.Hash)] = &PathEntry{Interface: routeIface, Hops: 1, Expires: time.Now().Add(time.Hour)}

	p := NewPacketWithTransport(ts, dest, []byte("hello"))
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	if err := ts.Outbound(p); err != nil {
		t.Fatalf("outbound failed: %v", err)
	}

	if routeIface.sendCount != 1 {
		t.Fatalf("expected route interface send count 1, got %v", routeIface.sendCount)
	}
	if otherIface.sendCount != 0 {
		t.Fatalf("expected non-route interface to get 0 sends, got %v", otherIface.sendCount)
	}
	if !bytes.Equal(routeIface.lastSent, p.Raw) {
		t.Fatalf("expected single-hop routed packet to remain Header1/original payload")
	}
}

func TestOutboundUsesKnownPathMultiHopHeader2(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	routeIface := &capturingInterface{name: "route"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, routeIface, otherIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, remoteID, DestinationOut, DestinationSingle, "route-test-multihop")
	nextHop := bytes.Repeat([]byte{0x44}, TruncatedHashLength/8)
	ts.pathTable[string(dest.Hash)] = &PathEntry{Interface: routeIface, Hops: 3, NextHop: nextHop, Expires: time.Now().Add(time.Hour)}

	p := NewPacketWithTransport(ts, dest, []byte("hello"))
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	if err := ts.Outbound(p); err != nil {
		t.Fatalf("outbound failed: %v", err)
	}

	if routeIface.sendCount != 1 {
		t.Fatalf("expected route interface send count 1, got %v", routeIface.sendCount)
	}
	if otherIface.sendCount != 0 {
		t.Fatalf("expected non-route interface to get 0 sends, got %v", otherIface.sendCount)
	}

	wire := NewPacketFromRaw(routeIface.lastSent)
	if err := wire.Unpack(); err != nil {
		t.Fatalf("failed to unpack routed wire packet: %v", err)
	}
	if wire.HeaderType != Header2 {
		t.Fatalf("expected Header2 for multi-hop route, got %v", wire.HeaderType)
	}
	if wire.TransportType != TransportForward {
		t.Fatalf("expected TransportForward for multi-hop route, got %v", wire.TransportType)
	}
	if !bytes.Equal(wire.TransportID, nextHop) {
		t.Fatalf("transport ID mismatch for routed packet")
	}
	if !bytes.Equal(wire.DestinationHash, dest.Hash) {
		t.Fatalf("destination hash mismatch for routed packet")
	}
}

func TestInboundForwardsWhenTransportIDMatches(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)

	identity := mustTestNewIdentity(t, true)
	ts.SetNetworkIdentity(identity)

	inboundIface := &capturingInterface{name: "inbound"}
	forwardIface := &capturingInterface{name: "forward"}
	ts.interfaces = append(ts.interfaces, inboundIface, forwardIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, remoteID, DestinationOut, DestinationSingle,
		"inbound-forward")
	nextHop := bytes.Repeat([]byte{0x55}, TruncatedHashLength/8)
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Interface: forwardIface,
		Hops:      3,
		NextHop:   nextHop,
		Expires:   time.Now().Add(time.Hour),
	}

	payload := make([]byte, 16)
	rand.Read(payload)
	p := NewPacketWithTransport(ts, dest, payload)
	p.HeaderType = Header2
	p.TransportType = TransportForward
	p.TransportID = ts.identity.Hash
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	p.UpdateHash()
	ts.mu.Lock()
	ts.packetHashes = make(map[string]time.Time)
	ts.packetHashesPrev = make(map[string]time.Time)
	ts.mu.Unlock()
	ts.Inbound(p.Raw, inboundIface)

	if forwardIface.sendCount != 1 {
		t.Fatalf("expected one forwarded send, got %v", forwardIface.sendCount)
	}

	forwarded := NewPacketFromRaw(forwardIface.lastSent)
	if err := forwarded.Unpack(); err != nil {
		t.Fatalf("failed to unpack forwarded packet: %v", err)
	}
	if forwarded.HeaderType != Header2 {
		t.Fatalf("expected forwarded packet to remain Header2, got %v", forwarded.HeaderType)
	}
	if !bytes.Equal(forwarded.TransportID, nextHop) {
		t.Fatalf("expected forwarded transport ID to be path next-hop")
	}
	if forwarded.Hops != 1 {
		t.Fatalf("expected forwarded hops to be incremented to 1, got %v", forwarded.Hops)
	}

	if len(ts.reverseTable) != 1 {
		t.Fatalf("expected reverse table entry to be created")
	}
}

func TestInboundForwardFinalHopStripsTransportHeader(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)

	identity := mustTestNewIdentity(t, true)
	ts.SetNetworkIdentity(identity)

	inboundIface := &capturingInterface{name: "inbound"}
	forwardIface := &capturingInterface{name: "forward"}
	ts.interfaces = append(ts.interfaces, inboundIface, forwardIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, remoteID, DestinationOut, DestinationSingle,
		"inbound-final-hop")
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Interface: forwardIface,
		Hops:      1,
		NextHop:   bytes.Repeat([]byte{0x66}, TruncatedHashLength/8),
		Expires:   time.Now().Add(time.Hour),
	}

	payload := make([]byte, 16)
	rand.Read(payload)
	p := NewPacketWithTransport(ts, dest, payload)
	p.HeaderType = Header2
	p.TransportType = TransportForward
	p.TransportID = ts.identity.Hash
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

	p.UpdateHash()
	ts.mu.Lock()
	ts.packetHashes = make(map[string]time.Time)
	ts.packetHashesPrev = make(map[string]time.Time)
	ts.mu.Unlock()
	ts.Inbound(p.Raw, inboundIface)

	if forwardIface.sendCount != 1 {
		t.Fatalf("expected one forwarded send, got %v", forwardIface.sendCount)
	}

	forwarded := NewPacketFromRaw(forwardIface.lastSent)
	if err := forwarded.Unpack(); err != nil {
		t.Fatalf("failed to unpack forwarded packet: %v", err)
	}
	if forwarded.HeaderType != Header1 {
		t.Fatalf("expected final-hop packet to be Header1, got %v", forwarded.HeaderType)
	}
	if !bytes.Equal(forwarded.DestinationHash, dest.Hash) {
		t.Fatalf("expected final-hop packet destination hash to match original destination")
	}
	if forwarded.Hops != 1 {
		t.Fatalf("expected forwarded hops to be incremented to 1, got %v", forwarded.Hops)
	}
}

func TestSeenOrRememberPacketHashRotation(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.packetHashRotateAt = 2

	now := time.Now()
	h1 := []byte("hash-1")
	h2 := []byte("hash-2")
	h3 := []byte("hash-3")

	if duplicate := ts.seenOrRememberPacketHashLocked(h1, now); duplicate {
		t.Fatalf("first hash should not be duplicate")
	}
	if duplicate := ts.seenOrRememberPacketHashLocked(h2, now); duplicate {
		t.Fatalf("second hash should not be duplicate")
	}
	if duplicate := ts.seenOrRememberPacketHashLocked(h3, now); duplicate {
		t.Fatalf("third hash should not be duplicate")
	}

	if duplicate := ts.seenOrRememberPacketHashLocked(h1, now); !duplicate {
		t.Fatalf("expected hash from previous window to be treated as duplicate")
	}
}

func TestCullStaleTransportTables(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	now := time.Now()
	ts.reverseTable["stale"] = &ReverseEntry{Timestamp: now.Add(-reverseEntryTimeout - time.Minute)}
	ts.reverseTable["fresh"] = &ReverseEntry{Timestamp: now}

	ts.linkTable["stale-ts"] = &LinkEntry{Timestamp: now.Add(-linkEntryTimeout - time.Minute)}
	ts.linkTable["stale-proof"] = &LinkEntry{Timestamp: now, ProofTimeout: now.Add(-time.Second)}
	ts.linkTable["fresh"] = &LinkEntry{Timestamp: now, ProofTimeout: now.Add(time.Minute)}

	ts.cullStaleTransportTables(now)

	if _, ok := ts.reverseTable["stale"]; ok {
		t.Fatalf("expected stale reverse table entry removed")
	}
	if _, ok := ts.reverseTable["fresh"]; !ok {
		t.Fatalf("expected fresh reverse table entry retained")
	}

	if _, ok := ts.linkTable["stale-ts"]; ok {
		t.Fatalf("expected stale link entry removed by timestamp")
	}
	if _, ok := ts.linkTable["stale-proof"]; ok {
		t.Fatalf("expected stale link entry removed by proof timeout")
	}
	if _, ok := ts.linkTable["fresh"]; !ok {
		t.Fatalf("expected fresh link entry retained")
	}
}

func TestPathTablePersistenceRoundTrip(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	iface := &capturingInterface{name: "persist-iface"}
	ts := NewTransportSystem(nil)
	ts.storagePath = tmpDir
	ts.interfaces = []interfaces.Interface{iface}

	destHash := []byte("persist-destination")
	nextHop := []byte("persist-next-hop")
	timestamp := time.Now().Truncate(time.Second)
	expires := timestamp.Add(time.Hour)
	packet := []byte("persist-packet")
	packetHash := bytes.Repeat([]byte{0x0c}, 32)

	ts.pathTable[string(destHash)] = &PathEntry{
		Timestamp:  timestamp,
		NextHop:    nextHop,
		Hops:       3,
		Expires:    expires,
		Interface:  iface,
		IfaceHash:  interfaceHash(iface),
		Packet:     packet,
		PacketHash: packetHash,
	}

	ts.persistPathTable()
	if _, err := os.Stat(filepath.Join(tmpDir, "destination_table")); err != nil {
		t.Fatalf("expected persisted destination_table file: %v", err)
	}

	tsLoaded := NewTransportSystem(nil)
	tsLoaded.storagePath = tmpDir

	tsLoaded.loadPathTableLocked()
	loaded, ok := tsLoaded.pathTable[string(destHash)]
	if !ok {
		t.Fatalf("expected persisted path table entry to load")
	}
	if !bytes.Equal(loaded.NextHop, nextHop) {
		t.Fatalf("next-hop mismatch after load")
	}
	if loaded.Hops != 3 {
		t.Fatalf("hops mismatch after load: got %v", loaded.Hops)
	}
	if loaded.Interface != nil {
		t.Fatalf("expected interface unresolved until registration")
	}
	// The new Python-compatible layout persists the interface HASH (field [6]),
	// not the interface name, so the loaded entry carries IfaceHash and an
	// empty InterfaceName until the live interface is reattached by hash.
	wantIfaceHash := interfaceHash(iface)
	if !bytes.Equal(loaded.IfaceHash, wantIfaceHash) {
		t.Fatalf("interface hash mismatch after load: got %x, want %x", loaded.IfaceHash, wantIfaceHash)
	}
	if !bytes.Equal(loaded.Packet, packet) {
		t.Fatalf("packet payload mismatch after load")
	}

	tsLoaded.RegisterInterface(iface)
	resolved := tsLoaded.pathTable[string(destHash)]
	if resolved.Interface == nil {
		t.Fatalf("expected interface to resolve after registration")
	}
}

func TestRememberSkipsPersistenceWhenTransportStopped(t *testing.T) {
	t.Parallel()

	tmpDir := testutils.TempDir(t, tempDirPrefix)

	ts := NewTransportSystem(nil)
	if err := ts.Start(tmpDir); err != nil {
		t.Fatalf("Transport start failed: %v", err)
	}
	ts.Stop()

	ts.Remember([]byte("packet-hash"), []byte("dest-hash"), []byte("public-key"), nil)

	if _, err := os.Stat(filepath.Join(tmpDir, "known_destinations")); !os.IsNotExist(err) {
		t.Fatalf("expected no known_destinations persistence after stop, got %v", err)
	}
}

func TestSaveKnownDestinationsPersistsWithoutStart(t *testing.T) {
	t.Parallel()

	tmpDir := testutils.TempDir(t, tempDirPrefix)

	ts := NewTransportSystem(nil)
	ts.Remember([]byte("packet-hash"), []byte("dest-hash"), []byte("public-key"), nil)
	ts.SaveKnownDestinations(tmpDir)

	if _, err := os.Stat(filepath.Join(tmpDir, "known_destinations")); err != nil {
		t.Fatalf("expected known_destinations persistence without start: %v", err)
	}
}

func TestDiscoverInterfacesRunsHook(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	called := make(chan struct{}, 1)
	ts.SetDiscoverInterfacesHook(func() {
		called <- struct{}{}
	})

	ts.DiscoverInterfaces()

	if got := ts.DiscoverInterfacesCallCount(); got != 1 {
		t.Fatalf("DiscoverInterfacesCallCount() = %v, want 1", got)
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discover hook call")
	}
}

func TestDiscoverInterfacesOnlyRunsHookOnce(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	called := make(chan struct{}, 2)
	ts.SetDiscoverInterfacesHook(func() {
		called <- struct{}{}
	})

	ts.DiscoverInterfaces()
	ts.DiscoverInterfaces()

	if got := ts.DiscoverInterfacesCallCount(); got != 1 {
		t.Fatalf("DiscoverInterfacesCallCount() = %v, want 1", got)
	}
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discover hook call")
	}
	select {
	case <-called:
		t.Fatal("discover hook ran more than once")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDiscoverInterfacesRunsHookAsync(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	ts.SetDiscoverInterfacesHook(func() {
		close(started)
		<-release
	})

	go func() {
		ts.DiscoverInterfaces()
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for discover hook to start")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("expected DiscoverInterfaces to return before hook completed")
	}

	close(release)
}

func TestEnableBlackholeUpdaterStartsUpdater(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	defer ts.StopBlackholeUpdater()

	ts.EnableBlackholeUpdater()

	if got := ts.EnableBlackholeUpdaterCallCount(); got != 1 {
		t.Fatalf("EnableBlackholeUpdaterCallCount() = %v, want 1", got)
	}
	if ts.BlackholeUpdater() == nil {
		t.Fatal("BlackholeUpdater() = nil, want a started updater")
	}
}

func TestEnableBlackholeUpdaterIdempotent(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	defer ts.StopBlackholeUpdater()

	ts.EnableBlackholeUpdater()
	ts.EnableBlackholeUpdater()

	if got := ts.EnableBlackholeUpdaterCallCount(); got != 1 {
		t.Fatalf("EnableBlackholeUpdaterCallCount() = %v, want 1 (idempotent)", got)
	}
	if ts.BlackholeUpdater() == nil {
		t.Fatal("BlackholeUpdater() = nil, want a started updater")
	}
}

func TestEnableBlackholeUpdaterReturnsAsync(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	defer ts.StopBlackholeUpdater()

	// EnableBlackholeUpdater must return promptly: it only constructs the
	// updater and spawns its loop (which waits INITIAL_WAIT before the first
	// pass). It must not block on the (network-bound) fetch.
	done := make(chan struct{})
	go func() {
		ts.EnableBlackholeUpdater()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("EnableBlackholeUpdater did not return asynchronously")
	}

	if ts.BlackholeUpdater() == nil {
		t.Fatal("BlackholeUpdater() = nil after EnableBlackholeUpdater returned")
	}
}

type dummyInterface struct {
	name string
}

func (d *dummyInterface) Name() string                { return d.name }
func (d *dummyInterface) Type() string                { return "dummy" }
func (d *dummyInterface) IsOut() bool                 { return true }
func (d *dummyInterface) Status() bool                { return true }
func (d *dummyInterface) Mode() int                   { return 1 }
func (d *dummyInterface) Bitrate() int                { return 1000 }
func (d *dummyInterface) Send(data []byte) error      { return nil }
func (d *dummyInterface) BytesReceived() uint64       { return 0 }
func (d *dummyInterface) BytesSent() uint64           { return 0 }
func (d *dummyInterface) Detach() error               { return nil }
func (d *dummyInterface) IsDetached() bool            { return false }
func (d *dummyInterface) Age() time.Duration          { return 0 }
func (d *dummyInterface) Gravity() int                { return 0 }
func (d *dummyInterface) RecursivePrs() bool          { return false }
func (d *dummyInterface) AnnouncesFromInternal() bool { return true }
func (d *dummyInterface) AnnouncesToInternal() *bool  { return nil }
func (d *dummyInterface) ReceivedAnnounce()           {}
func (d *dummyInterface) ShouldIngressLimit() bool    { return false }
func (d *dummyInterface) HoldAnnounce([]byte, interfaces.Interface, int, []byte) {
}
func (d *dummyInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (d *dummyInterface) HeldAnnounces() int { return 0 }
func (d *dummyInterface) ReleaseHeldAnnounce([]byte) ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (d *dummyInterface) ReceivedPathRequest()               {}
func (d *dummyInterface) SentPathRequest()                   {}
func (d *dummyInterface) IncomingPrFrequency() float64       { return 0 }
func (d *dummyInterface) OutgoingPrFrequency() float64       { return 0 }
func (d *dummyInterface) ShouldIngressLimitPr() bool         { return false }
func (d *dummyInterface) ShouldEgressLimitPr() bool          { return false }
func (d *dummyInterface) AnnounceRateTarget() *int           { return nil }
func (d *dummyInterface) AnnounceRateGrace() *int            { return nil }
func (d *dummyInterface) AnnounceRatePenalty() *int          { return nil }
func (d *dummyInterface) IncomingAnnounceFrequency() float64 { return 0 }
func (d *dummyInterface) OutgoingAnnounceFrequency() float64 { return 0 }
func (d *dummyInterface) ICBurstActive() bool                { return false }
func (d *dummyInterface) ICBurstActivated() time.Time        { return time.Time{} }
func (d *dummyInterface) ICPrBurstActive() bool              { return false }
func (d *dummyInterface) ICPrBurstActivated() time.Time      { return time.Time{} }

type capturingInterface struct {
	name        string
	mu          sync.Mutex
	sendCount   int
	lastSent    []byte
	bitrate     int
	gravity     int
	sentPRCount int
}

func (c *capturingInterface) Name() string { return c.name }
func (c *capturingInterface) Type() string { return "capture" }
func (c *capturingInterface) IsOut() bool  { return true }
func (c *capturingInterface) Status() bool { return true }
func (c *capturingInterface) Mode() int    { return 1 }
func (c *capturingInterface) Bitrate() int {
	if c.bitrate > 0 {
		return c.bitrate
	}
	return 1000
}
func (c *capturingInterface) BytesReceived() uint64       { return 0 }
func (c *capturingInterface) BytesSent() uint64           { return 0 }
func (c *capturingInterface) Detach() error               { return nil }
func (c *capturingInterface) IsDetached() bool            { return false }
func (c *capturingInterface) Age() time.Duration          { return 0 }
func (c *capturingInterface) Gravity() int                { return c.gravity }
func (c *capturingInterface) RecursivePrs() bool          { return false }
func (c *capturingInterface) AnnouncesFromInternal() bool { return true }
func (c *capturingInterface) AnnouncesToInternal() *bool  { return nil }
func (c *capturingInterface) ReceivedAnnounce()           {}
func (c *capturingInterface) ShouldIngressLimit() bool    { return false }
func (c *capturingInterface) HoldAnnounce([]byte, interfaces.Interface, int, []byte) {
}
func (c *capturingInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (c *capturingInterface) HeldAnnounces() int { return 0 }
func (c *capturingInterface) ReleaseHeldAnnounce([]byte) ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (c *capturingInterface) ReceivedPathRequest() {}
func (c *capturingInterface) SentPathRequest() {
	c.mu.Lock()
	c.sentPRCount++
	c.mu.Unlock()
}
func (c *capturingInterface) IncomingPrFrequency() float64       { return 0 }
func (c *capturingInterface) OutgoingPrFrequency() float64       { return 0 }
func (c *capturingInterface) ShouldIngressLimitPr() bool         { return false }
func (c *capturingInterface) ShouldEgressLimitPr() bool          { return false }
func (c *capturingInterface) AnnounceRateTarget() *int           { return nil }
func (c *capturingInterface) AnnounceRateGrace() *int            { return nil }
func (c *capturingInterface) AnnounceRatePenalty() *int          { return nil }
func (c *capturingInterface) IncomingAnnounceFrequency() float64 { return 0 }
func (c *capturingInterface) OutgoingAnnounceFrequency() float64 { return 0 }
func (c *capturingInterface) ICBurstActive() bool                { return false }
func (c *capturingInterface) ICBurstActivated() time.Time        { return time.Time{} }
func (c *capturingInterface) ICPrBurstActive() bool              { return false }
func (c *capturingInterface) ICPrBurstActivated() time.Time      { return time.Time{} }
func (c *capturingInterface) Send(data []byte) error {
	c.mu.Lock()
	c.sendCount++
	c.lastSent = make([]byte, len(data))
	copy(c.lastSent, data)
	c.mu.Unlock()
	return nil
}

// SendCount returns the number of Send calls observed. The counter is guarded
// by the mutex because Send may run on a background goroutine (e.g. the
// shared-instance path-response announce scheduled by RegisterDestination,
// transport.go), while a test reads the counter; the accessor establishes a
// happens-before edge that a bare field read would not.
func (c *capturingInterface) SendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sendCount
}

// SentPRCount returns the number of SentPathRequest calls observed, guarded
// the same way as SendCount.
func (c *capturingInterface) SentPRCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sentPRCount
}

// LastSent returns a copy of the most recent Send payload, guarded the same
// way as SendCount.
func (c *capturingInterface) LastSent() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]byte, len(c.lastSent))
	copy(out, c.lastSent)
	return out
}

// blockingInterface is a capturingInterface whose Send blocks for a fixed
// duration, simulating a half-open TCP peer whose conn.Write stalls until the
// per-interface write deadline fires. It verifies the outbound fan-out is
// concurrent: a stalled peer must block only its own goroutine, never the
// maintenance loop or a readLoop that dispatched the send (the 0.8.0
// regression where one stalled peer wedged link handshakes on every other
// interface).
type blockingInterface struct {
	capturingInterface
	blockFor time.Duration
}

func (b *blockingInterface) Send(data []byte) error {
	time.Sleep(b.blockFor)
	return b.capturingInterface.Send(data)
}

type failingInterface struct {
	name string
}

func (f *failingInterface) Name() string                { return f.name }
func (f *failingInterface) Type() string                { return "failing" }
func (f *failingInterface) IsOut() bool                 { return true }
func (f *failingInterface) Status() bool                { return true }
func (f *failingInterface) Mode() int                   { return 1 }
func (f *failingInterface) Bitrate() int                { return 1000 }
func (f *failingInterface) BytesReceived() uint64       { return 0 }
func (f *failingInterface) BytesSent() uint64           { return 0 }
func (f *failingInterface) Detach() error               { return nil }
func (f *failingInterface) IsDetached() bool            { return false }
func (f *failingInterface) Age() time.Duration          { return 0 }
func (f *failingInterface) Gravity() int                { return 0 }
func (f *failingInterface) RecursivePrs() bool          { return false }
func (f *failingInterface) AnnouncesFromInternal() bool { return true }
func (f *failingInterface) AnnouncesToInternal() *bool  { return nil }
func (f *failingInterface) ReceivedAnnounce()           {}
func (f *failingInterface) ShouldIngressLimit() bool    { return false }
func (f *failingInterface) HoldAnnounce([]byte, interfaces.Interface, int, []byte) {
}
func (f *failingInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (f *failingInterface) HeldAnnounces() int { return 0 }
func (f *failingInterface) ReleaseHeldAnnounce([]byte) ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (f *failingInterface) ReceivedPathRequest()               {}
func (f *failingInterface) SentPathRequest()                   {}
func (f *failingInterface) IncomingPrFrequency() float64       { return 0 }
func (f *failingInterface) OutgoingPrFrequency() float64       { return 0 }
func (f *failingInterface) ShouldIngressLimitPr() bool         { return false }
func (f *failingInterface) ShouldEgressLimitPr() bool          { return false }
func (f *failingInterface) AnnounceRateTarget() *int           { return nil }
func (f *failingInterface) AnnounceRateGrace() *int            { return nil }
func (f *failingInterface) AnnounceRatePenalty() *int          { return nil }
func (f *failingInterface) IncomingAnnounceFrequency() float64 { return 0 }
func (f *failingInterface) OutgoingAnnounceFrequency() float64 { return 0 }
func (f *failingInterface) ICBurstActive() bool                { return false }
func (f *failingInterface) ICBurstActivated() time.Time        { return time.Time{} }
func (f *failingInterface) ICPrBurstActive() bool              { return false }
func (f *failingInterface) ICPrBurstActivated() time.Time      { return time.Time{} }
func (f *failingInterface) Send(data []byte) error {
	return os.ErrClosed
}

type ifacDropInterface struct {
	name          string
	inboundCalled bool
}

func (i *ifacDropInterface) Name() string                { return i.name }
func (i *ifacDropInterface) Type() string                { return "ifac-drop" }
func (i *ifacDropInterface) IsOut() bool                 { return true }
func (i *ifacDropInterface) Status() bool                { return true }
func (i *ifacDropInterface) Mode() int                   { return 1 }
func (i *ifacDropInterface) Bitrate() int                { return 1000 }
func (i *ifacDropInterface) BytesReceived() uint64       { return 0 }
func (i *ifacDropInterface) BytesSent() uint64           { return 0 }
func (i *ifacDropInterface) Detach() error               { return nil }
func (i *ifacDropInterface) IsDetached() bool            { return false }
func (i *ifacDropInterface) Age() time.Duration          { return 0 }
func (i *ifacDropInterface) Gravity() int                { return 0 }
func (i *ifacDropInterface) RecursivePrs() bool          { return false }
func (i *ifacDropInterface) AnnouncesFromInternal() bool { return true }
func (i *ifacDropInterface) AnnouncesToInternal() *bool  { return nil }
func (i *ifacDropInterface) ReceivedAnnounce()           {}
func (i *ifacDropInterface) ShouldIngressLimit() bool    { return false }
func (i *ifacDropInterface) HoldAnnounce([]byte, interfaces.Interface, int, []byte) {
}
func (i *ifacDropInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (i *ifacDropInterface) HeldAnnounces() int { return 0 }
func (i *ifacDropInterface) ReleaseHeldAnnounce([]byte) ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (i *ifacDropInterface) ReceivedPathRequest()               {}
func (i *ifacDropInterface) SentPathRequest()                   {}
func (i *ifacDropInterface) IncomingPrFrequency() float64       { return 0 }
func (i *ifacDropInterface) OutgoingPrFrequency() float64       { return 0 }
func (i *ifacDropInterface) ShouldIngressLimitPr() bool         { return false }
func (i *ifacDropInterface) ShouldEgressLimitPr() bool          { return false }
func (i *ifacDropInterface) AnnounceRateTarget() *int           { return nil }
func (i *ifacDropInterface) AnnounceRateGrace() *int            { return nil }
func (i *ifacDropInterface) AnnounceRatePenalty() *int          { return nil }
func (i *ifacDropInterface) IncomingAnnounceFrequency() float64 { return 0 }
func (i *ifacDropInterface) OutgoingAnnounceFrequency() float64 { return 0 }
func (i *ifacDropInterface) ICBurstActive() bool                { return false }
func (i *ifacDropInterface) ICBurstActivated() time.Time        { return time.Time{} }
func (i *ifacDropInterface) ICPrBurstActive() bool              { return false }
func (i *ifacDropInterface) ICPrBurstActivated() time.Time      { return time.Time{} }
func (i *ifacDropInterface) Send(data []byte) error {
	return nil
}
func (i *ifacDropInterface) ApplyIFACInbound(data []byte) ([]byte, bool) {
	i.inboundCalled = true
	return nil, false
}

type ifacTransformInterface struct {
	name           string
	outboundCalled bool
	lastSent       []byte
}

func (i *ifacTransformInterface) Name() string                { return i.name }
func (i *ifacTransformInterface) Type() string                { return "ifac-transform" }
func (i *ifacTransformInterface) IsOut() bool                 { return true }
func (i *ifacTransformInterface) Status() bool                { return true }
func (i *ifacTransformInterface) Mode() int                   { return 1 }
func (i *ifacTransformInterface) Bitrate() int                { return 1000 }
func (i *ifacTransformInterface) BytesReceived() uint64       { return 0 }
func (i *ifacTransformInterface) BytesSent() uint64           { return 0 }
func (i *ifacTransformInterface) Detach() error               { return nil }
func (i *ifacTransformInterface) IsDetached() bool            { return false }
func (i *ifacTransformInterface) Age() time.Duration          { return 0 }
func (i *ifacTransformInterface) Gravity() int                { return 0 }
func (i *ifacTransformInterface) RecursivePrs() bool          { return false }
func (i *ifacTransformInterface) AnnouncesFromInternal() bool { return true }
func (i *ifacTransformInterface) AnnouncesToInternal() *bool  { return nil }
func (i *ifacTransformInterface) ReceivedAnnounce()           {}
func (i *ifacTransformInterface) ShouldIngressLimit() bool    { return false }
func (i *ifacTransformInterface) HoldAnnounce([]byte, interfaces.Interface, int, []byte) {
}
func (i *ifacTransformInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (i *ifacTransformInterface) HeldAnnounces() int { return 0 }
func (i *ifacTransformInterface) ReleaseHeldAnnounce([]byte) ([]byte, interfaces.Interface, bool) {
	return nil, nil, false
}
func (i *ifacTransformInterface) ReceivedPathRequest()               {}
func (i *ifacTransformInterface) SentPathRequest()                   {}
func (i *ifacTransformInterface) IncomingPrFrequency() float64       { return 0 }
func (i *ifacTransformInterface) OutgoingPrFrequency() float64       { return 0 }
func (i *ifacTransformInterface) ShouldIngressLimitPr() bool         { return false }
func (i *ifacTransformInterface) ShouldEgressLimitPr() bool          { return false }
func (i *ifacTransformInterface) AnnounceRateTarget() *int           { return nil }
func (i *ifacTransformInterface) AnnounceRateGrace() *int            { return nil }
func (i *ifacTransformInterface) AnnounceRatePenalty() *int          { return nil }
func (i *ifacTransformInterface) IncomingAnnounceFrequency() float64 { return 0 }
func (i *ifacTransformInterface) OutgoingAnnounceFrequency() float64 { return 0 }
func (i *ifacTransformInterface) ICBurstActive() bool                { return false }
func (i *ifacTransformInterface) ICBurstActivated() time.Time        { return time.Time{} }
func (i *ifacTransformInterface) ICPrBurstActive() bool              { return false }
func (i *ifacTransformInterface) ICPrBurstActivated() time.Time      { return time.Time{} }
func (i *ifacTransformInterface) Send(data []byte) error {
	i.lastSent = append([]byte(nil), data...)
	return nil
}
func (i *ifacTransformInterface) ApplyIFACOutbound(data []byte) ([]byte, error) {
	i.outboundCalled = true
	out := make([]byte, 0, len(data)+1)
	out = append(out, 0xAA)
	out = append(out, data...)
	return out, nil
}

func mustTestAnnouncePacketWithEmission(t *testing.T, _ *TransportSystem, id *Identity, dest *Destination, emission uint64) *Packet {
	t.Helper()

	nameHash := FullHash([]byte(dest.appName))[:NameHashLength/8]
	randomBlob := make([]byte, 10)
	for i := range 5 {
		randomBlob[9-i] = byte(emission & 0xff)
		emission >>= 8
	}

	signedData := make([]byte, 0, 128)
	signedData = append(signedData, dest.Hash...)
	signedData = append(signedData, id.GetPublicKey()...)
	signedData = append(signedData, nameHash...)
	signedData = append(signedData, randomBlob...)

	sig, err := id.Sign(signedData)
	if err != nil {
		t.Fatalf("Sign(announce): %v", err)
	}

	announceData := make([]byte, 0, 256)
	announceData = append(announceData, id.GetPublicKey()...)
	announceData = append(announceData, nameHash...)
	announceData = append(announceData, randomBlob...)
	announceData = append(announceData, sig...)

	packet := NewPacket(dest, announceData)
	packet.PacketType = PacketAnnounce
	packet.Data = announceData
	if err := packet.Pack(); err != nil {
		t.Fatalf("Pack(announce): %v", err)
	}
	return packet
}

func TestTransportBlackholeRegistry(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = &Identity{Hash: []byte{0x09, 0x09, 0x09}}
	hash := []byte{0x01, 0x02, 0x03}
	until := time.Now().Add(time.Hour).Unix()

	if ok := ts.BlackholeIdentity(hash, &until, "test-reason"); !ok {
		t.Fatalf("BlackholeIdentity returned false")
	}

	entries := ts.GetBlackholedIdentities()
	if len(entries) != 1 {
		t.Fatalf("expected 1 blackhole entry, got %v", len(entries))
	}
	if got := entries[0]["source"]; got == nil {
		t.Fatalf("expected blackhole entry source to be recorded")
	}

	if ok := ts.UnblackholeIdentity(hash); !ok {
		t.Fatalf("UnblackholeIdentity returned false")
	}

	entries = ts.GetBlackholedIdentities()
	if len(entries) != 0 {
		t.Fatalf("expected 0 blackhole entries after removal, got %v", len(entries))
	}
}

func TestTransportDropAnnounceQueues(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	iface := &capturingInterface{name: "queued"}
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.announceTable["dest1"] = &AnnounceEntry{}
	ts.announceTable["dest2"] = &AnnounceEntry{}
	ts.pendingPathRequests["dest1"] = nil
	ts.pendingPathRequestAt["dest1"] = time.Now()
	ts.announceQueues[iface] = &announceQueueState{
		queue: []announceQueueEntry{{destinationHash: "queued-dest"}},
	}
	ts.mu.Unlock()

	dropped := ts.DropAnnounceQueues()
	if dropped != 2 {
		t.Fatalf("DropAnnounceQueues dropped %v, want 2", dropped)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.announceTable) != 0 || len(ts.pendingPathRequests) != 0 || len(ts.pendingPathRequestAt) != 0 {
		t.Fatalf("expected announce and pending queues to be cleared")
	}
	if state := ts.announceQueues[iface]; state != nil && len(state.queue) != 0 {
		t.Fatalf("expected per-interface announce queue to be cleared")
	}
}

func TestTransportPacketMetricCaches(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.mu.Lock()
	ts.ensureStateLocked()
	key := string([]byte{0xaa, 0xbb})
	ts.packetRSSICache[key] = -73.5
	ts.packetSNRCache[key] = 9.25
	ts.packetQCache[key] = 0.87
	ts.mu.Unlock()

	if v, ok := ts.GetPacketRSSI([]byte{0xaa, 0xbb}); !ok || v != -73.5 {
		t.Fatalf("GetPacketRSSI = (%v,%v), want (-73.5,true)", v, ok)
	}
	if v, ok := ts.GetPacketSNR([]byte{0xaa, 0xbb}); !ok || v != 9.25 {
		t.Fatalf("GetPacketSNR = (%v,%v), want (9.25,true)", v, ok)
	}
	if v, ok := ts.GetPacketQ([]byte{0xaa, 0xbb}); !ok || v != 0.87 {
		t.Fatalf("GetPacketQ = (%v,%v), want (0.87,true)", v, ok)
	}
}

func TestTransportPacketCaching(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)

	// seenOrRememberPacketHashLocked returns false on first observation
	// (not seen), true on subsequent ones (deduplicated).
	hash := []byte("dedup-test-hash")
	if ts.seenOrRememberPacketHashLocked(hash, time.Now()) {
		t.Fatal("first observation should return false (not seen)")
	}
	if !ts.seenOrRememberPacketHashLocked(hash, time.Now()) {
		t.Fatal("second observation should return true (deduplicated)")
	}

	// CleanCache should not throw.
	ts.CleanCache()
}

func TestTunnelSystem(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, "rns-test-")

	// Create a tunnel.
	ts.mu.Lock()
	if ts.tunnels == nil {
		ts.tunnels = map[string]*Tunnel{}
	}
	td := &Tunnel{
		ID:        []byte{1},
		Interface: nil, // synthesized interface not created in this test
		Paths:     map[string]*PathEntry{},
	}
	ts.tunnels[string([]byte{1})] = td
	ts.mu.Unlock()

	// Verify lookup.
	ts.mu.Lock()
	got, ok := ts.tunnels[string([]byte{1})]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("tunnel not found after registration")
	}
	if !bytes.Equal(got.ID, []byte{1}) {
		t.Fatalf("tunnel ID = %x, want 01", got.ID)
	}

	// Void the tunnel: the entry stays but its interface is cleared.
	ts.VoidTunnelInterface([]byte{1})
	ts.mu.Lock()
	entry, stillThere := ts.tunnels[string([]byte{1})]
	ts.mu.Unlock()
	if !stillThere {
		t.Fatal("tunnel entry removed by VoidTunnelInterface; should only clear interface")
	}
	if entry.Interface != nil {
		t.Fatal("tunnel interface not cleared by VoidTunnelInterface")
	}

	// SaveTunnelTable to disk should not throw.
	if err := ts.SaveTunnelTable(tmpDir); err != nil {
		t.Fatalf("SaveTunnelTable: %v", err)
	}
}

func TestPathResponsiveness(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	hash := []byte("path-responsiveness-test")

	// Initially, a fresh path is not unresponsive.
	ts.mu.Lock()
	ts.pathTable = map[string]*PathEntry{}
	ts.pathTable[string(hash)] = &PathEntry{}
	ts.mu.Unlock()

	if ts.PathIsUnresponsive(hash) {
		t.Fatal("fresh path is reported as unresponsive")
	}

	ts.MarkPathUnresponsive(hash)
	if !ts.PathIsUnresponsive(hash) {
		t.Fatal("path is not reported as unresponsive after MarkPathUnresponsive")
	}

	ts.MarkPathResponsive(hash)
	if ts.PathIsUnresponsive(hash) {
		t.Fatal("path is still reported as unresponsive after MarkPathResponsive")
	}

	ts.MarkPathUnknownState(hash)
	if ts.PathIsUnresponsive(hash) {
		t.Fatal("path is still reported as unresponsive after MarkPathUnknownState")
	}

	// ExpirePath removes the path from the table.
	ts.ExpirePath(hash)
	ts.mu.Lock()
	_, stillThere := ts.pathTable[string(hash)]
	ts.mu.Unlock()
	if stillThere {
		t.Fatal("path still present after ExpirePath")
	}
}

func TestDataPersistence(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	tmpDir := testutils.TempDir(t, "rns-test-")

	// Persist a packet-hash list using real HashLength/8-byte hashes.
	hashA := make([]byte, HashLength/8)
	hashB := make([]byte, HashLength/8)
	hashA[0], hashB[0] = 0xAA, 0xBB
	ts.mu.Lock()
	ts.packetHashes = map[string]time.Time{
		string(hashA): time.Now(),
		string(hashB): time.Now(),
	}
	ts.mu.Unlock()

	if err := ts.SavePacketHashlist(tmpDir); err != nil {
		t.Fatalf("SavePacketHashlist: %v", err)
	}
	rawPath := filepath.Join(tmpDir, "packet_hashlist.raw")
	if _, err := os.Stat(rawPath); err != nil {
		t.Fatalf("expected %v to exist: %v", rawPath, err)
	}

	// Persist the path table.
	ts.mu.Lock()
	ts.pathTable = map[string]*PathEntry{
		"path-1": {Hops: 1, Timestamp: time.Now()},
	}
	ts.mu.Unlock()

	if err := ts.SavePathTable(tmpDir); err != nil {
		t.Fatalf("SavePathTable: %v", err)
	}

	// Load the packet-hash list back into a fresh transport.
	ts2 := NewTransportSystem(nil)
	ts2.SetEnabled(true)
	if err := ts2.LoadPacketHashlist(tmpDir); err != nil {
		t.Fatalf("LoadPacketHashlist: %v", err)
	}
	ts2.mu.Lock()
	_, okA := ts2.packetHashes[string(hashA)]
	_, okB := ts2.packetHashes[string(hashB)]
	ts2.mu.Unlock()
	if !okA || !okB {
		t.Fatalf("expected both packet hashes to round-trip, got okA=%v okB=%v", okA, okB)
	}
}

func TestDeregisterDestination(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(ts, id, DestinationIn, DestinationSingle, "test", "app")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	ts.RegisterDestination(dest)
	ts.mu.Lock()
	registered := len(ts.destinations)
	ts.mu.Unlock()
	if registered == 0 {
		t.Fatal("destination was not registered")
	}

	ts.DeregisterDestination(dest)
	ts.mu.Lock()
	for _, d := range ts.destinations {
		if d == dest {
			t.Fatal("destination was not deregistered")
		}
	}
	ts.mu.Unlock()
}

func TestRemoteManagement(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, "rns-test-")
	writeConfig(t, tmpDir, "[reticulum]\nshare_instance = No\n")
	r, err := NewReticulumWithLogger(ts, tmpDir, nil)
	if err != nil {
		t.Fatalf("NewReticulumWithLogger: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	// Register the remote handlers.
	ts.remoteStatusHandlerFn = func(data []any) any {
		return []any{ts.interfaceStatsForRemote()}
	}
	ts.remotePathHandlerFn = func(data []any) any {
		return []any{}
	}

	// Both handlers must be set.
	if ts.remoteStatusHandlerFn == nil {
		t.Fatal("remoteStatusHandlerFn is nil")
	}
	if ts.remotePathHandlerFn == nil {
		t.Fatal("remotePathHandlerFn is nil")
	}

	// Exercise the handler functions directly.
	resp := ts.remoteStatusHandlerFn([]any{true})
	if resp == nil {
		t.Fatal("remoteStatusHandlerFn returned nil")
	}

	resp = ts.remotePathHandlerFn([]any{"table"})
	if resp == nil {
		t.Fatal("remotePathHandlerFn returned nil")
	}
}

func TestAwaitPath(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	hash := []byte("await-path-test")

	// AwaitPath on a known destination should return immediately, without
	// waiting for the timeout to elapse.
	ts.mu.Lock()
	ts.pathTable[string(hash)] = &PathEntry{}
	ts.mu.Unlock()

	start := time.Now()
	got, err := ts.AwaitPath(hash, 5*time.Second)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("AwaitPath on known dest: %v", err)
	}
	if !bytes.Equal(got, hash) {
		t.Fatalf("AwaitPath on known dest returned %x, want %x", got, hash)
	}
	// A known path must return without sleeping through the timeout. Use a
	// generous bound so this does not flake under -race scheduler contention
	// while still catching a real failure to short-circuit on a known path.
	if elapsed > time.Second {
		t.Fatalf("AwaitPath on known dest took too long: %v", elapsed)
	}

	// AwaitPath on an unknown destination must return (nil, nil) once the
	// timeout elapses rather than blocking forever. The bound is generous:
	// under -race with many parallel transport tests, goroutine scheduling
	// can delay the poll loop well past the requested timeout. We only need
	// to confirm it terminates in finite time and reports no error.
	unknown := []byte("await-path-unknown")
	start = time.Now()
	got, err = ts.AwaitPath(unknown, 20*time.Millisecond)
	elapsed = time.Since(start)
	if err != nil {
		t.Fatalf("AwaitPath on unknown dest: %v", err)
	}
	if got != nil {
		t.Fatalf("AwaitPath on unknown dest returned %x, want nil", got)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("AwaitPath on unknown dest hung: %v", elapsed)
	}
}

// TestClaimDownNotifyOncePerDown verifies the down-notify latch lets exactly
// one concurrent caller claim a down transition for an interface, suppresses
// further claims until the interface is observed up again, and re-arms after
// that clear.
func TestClaimDownNotifyOncePerDown(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	iface := &capturingInterface{name: "x"}

	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for range 100 {
		wg.Go(func() {
			if ts.claimDownNotify(iface) {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("expected exactly 1 winning claim among 100 concurrent, got %d", wins)
	}
	if ts.claimDownNotify(iface) {
		t.Fatalf("expected second claim to be suppressed by latch")
	}
	// After the latch is cleared (interface observed up again), a claim wins.
	ts.mu.Lock()
	delete(ts.downNotified, iface)
	ts.mu.Unlock()
	if !ts.claimDownNotify(iface) {
		t.Fatalf("expected claim to win after latch clear")
	}
}

// TestDispatchForwardSendFloodInvalidatesOncePerDown reproduces the Quortal
// flood: a half-open interface whose Send fails on a burst of ~50 concurrent
// queued sends (the drain after a write-deadline timeout). Without the latch
// each failing send ran a full InvalidatePathsViaInterface scan; with it, the
// path is invalidated exactly once per down transition and a second burst
// while the interface is still down suppresses, matching Python RNS which
// tears down once instead of invalidating per failed send.
func TestDispatchForwardSendFloodInvalidatesOncePerDown(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	dead := &failingInterface{name: "dead-quortal"}
	ts.interfaces = append(ts.interfaces, dead)

	destHash := []byte("deadpath-destination")
	burst := func(n int) {
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				ts.dispatchForwardSend(dead, []byte{0xC0, 0x02, 0x01}, "forwarding path request")
			})
		}
		wg.Wait()
	}

	// Install a path that routes via the dead interface.
	ts.mu.Lock()
	ts.pathTable[string(destHash)] = &PathEntry{Interface: dead, Hops: 2, Timestamp: time.Now()}
	ts.mu.Unlock()

	// First burst: interface is up, Send fails. Exactly one goroutine claims
	// the latch and invalidates the path; the other 49 suppress.
	burst(50)
	ts.mu.Lock()
	_, pathOK := ts.pathTable[string(destHash)]
	_, latched := ts.downNotified[dead]
	ts.mu.Unlock()
	if pathOK {
		t.Fatalf("expected path via dead interface to be invalidated after first burst")
	}
	if !latched {
		t.Fatalf("expected down-notify latch to be set after first burst")
	}

	// Re-add the path. A second burst must NOT re-invalidate while the latch
	// is set (the interface has not been observed up again).
	ts.mu.Lock()
	ts.pathTable[string(destHash)] = &PathEntry{Interface: dead, Hops: 2, Timestamp: time.Now()}
	ts.mu.Unlock()
	burst(50)
	ts.mu.Lock()
	_, stillThere := ts.pathTable[string(destHash)]
	ts.mu.Unlock()
	if !stillThere {
		t.Fatalf("second burst re-invalidated the path; latch should suppress until interface is back up")
	}

	// Simulate the interface coming back up (processAnnounceTable clears the
	// latch for up interfaces). A third burst can invalidate again.
	ts.mu.Lock()
	delete(ts.downNotified, dead)
	ts.mu.Unlock()
	burst(50)
	ts.mu.Lock()
	_, goneAgain := ts.pathTable[string(destHash)]
	ts.mu.Unlock()
	if goneAgain {
		t.Fatalf("third burst after latch clear should invalidate the path again")
	}
}

// ingressLimitingInterface is a capturingInterface whose ingress-limit state
// machine is controllable from tests: ShouldIngressLimit returns a configured
// flag, and HoldAnnounce/HeldAnnounces/ProcessHeldAnnounces record and replay
// held announces so the Transport.Inbound announce gate can be exercised
// end-to-end without a real BaseInterface burst state machine.
type ingressLimitingInterface struct {
	capturingInterface
	ingressLimit bool
	held         []heldRecord
}

type heldRecord struct {
	raw      []byte
	recv     interfaces.Interface
	hops     int
	destHash []byte
}

func (i *ingressLimitingInterface) ShouldIngressLimit() bool { return i.ingressLimit }
func (i *ingressLimitingInterface) HoldAnnounce(raw []byte, recv interfaces.Interface, hops int, destHash []byte) {
	i.held = append(i.held, heldRecord{raw: raw, recv: recv, hops: hops, destHash: append([]byte(nil), destHash...)})
}
func (i *ingressLimitingInterface) HeldAnnounces() int { return len(i.held) }
func (i *ingressLimitingInterface) ProcessHeldAnnounces() ([]byte, interfaces.Interface, bool) {
	if len(i.held) == 0 {
		return nil, nil, false
	}
	h := i.held[0]
	i.held = i.held[1:]
	return h.raw, h.recv, true
}
func (i *ingressLimitingInterface) ReleaseHeldAnnounce(destHash []byte) ([]byte, interfaces.Interface, bool) {
	for idx, h := range i.held {
		if bytes.Equal(h.destHash, destHash) {
			i.held = append(i.held[:idx], i.held[idx+1:]...)
			return h.raw, h.recv, true
		}
	}
	return nil, nil, false
}

// TestShouldHoldAnnounceGate verifies the inbound-announce ingress-limit gate
// (Python Transport.py:1752-1765): announces for unknown destinations are held
// on ingress-limiting interfaces, but a destination with a pending path request
// bypasses the gate so path-finding is never starved, and already-known
// destinations are never gated.
func TestShouldHoldAnnounceGate(t *testing.T) {
	t.Parallel()

	mkTS := func(t *testing.T) (*TransportSystem, *ingressLimitingInterface) {
		ts := NewTransportSystem(nil)
		ts.identity = mustTestNewIdentity(t, true)
		iface := &ingressLimitingInterface{capturingInterface: capturingInterface{name: "ingress"}, ingressLimit: true}
		ts.interfaces = append(ts.interfaces, iface)
		return ts, iface
	}

	mkAnnounce := func(t *testing.T, id *Identity, dest *Destination, hops int, iface interfaces.Interface) *Packet {
		t.Helper()
		p := mustTestAnnouncePacketWithEmission(t, nil, id, dest, 5)
		p.Hops = hops
		if len(p.Raw) > 1 {
			p.Raw[1] = byte(hops)
		}
		p.ReceivingInterface = iface
		return p
	}

	mkRemote := func(t *testing.T, app string) (*Identity, *Destination) {
		t.Helper()
		id := mustTestNewIdentity(t, true)
		dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, app)
		if err != nil {
			t.Fatalf("remote dest: %v", err)
		}
		return id, dest
	}

	inTable := func(ts *TransportSystem, destHash []byte) bool {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		_, ok := ts.pathTable[string(destHash)]
		return ok
	}

	t.Run("unknown destination held when ingress limited", func(t *testing.T) {
		t.Parallel()
		ts, iface := mkTS(t)
		id, dest := mkRemote(t, "gate")
		ts.handleAnnounce(mkAnnounce(t, id, dest, 1, iface), iface)
		if iface.HeldAnnounces() != 1 {
			t.Fatalf("HeldAnnounces = %d, want 1 (unknown dest held)", iface.HeldAnnounces())
		}
		if inTable(ts, dest.Hash) {
			t.Fatal("held announce must not enter the path table")
		}
	})

	t.Run("pending path request bypasses gate", func(t *testing.T) {
		t.Parallel()
		ts, iface := mkTS(t)
		id, dest := mkRemote(t, "prbypass")
		ts.mu.Lock()
		ts.ensureStateLocked()
		ts.pendingPathRequests[string(dest.Hash)] = []interfaces.Interface{iface}
		ts.mu.Unlock()
		ts.handleAnnounce(mkAnnounce(t, id, dest, 1, iface), iface)
		if iface.HeldAnnounces() != 0 {
			t.Fatalf("HeldAnnounces = %d, want 0 (pending PR bypasses gate)", iface.HeldAnnounces())
		}
		if !inTable(ts, dest.Hash) {
			t.Fatal("announce with pending PR must be processed into the path table")
		}
	})

	t.Run("known destination not held", func(t *testing.T) {
		t.Parallel()
		ts, iface := mkTS(t)
		id, dest := mkRemote(t, "known")
		ts.mu.Lock()
		ts.ensureStateLocked()
		ts.pathTable[string(dest.Hash)] = &PathEntry{Interface: iface, Hops: 1, Expires: time.Now().Add(time.Hour)}
		ts.mu.Unlock()
		ts.handleAnnounce(mkAnnounce(t, id, dest, 1, iface), iface)
		if iface.HeldAnnounces() != 0 {
			t.Fatalf("HeldAnnounces = %d, want 0 (known dest not gated)", iface.HeldAnnounces())
		}
	})

	// An announce for a destination THIS node originated a path request for
	// (Python Transport.path_requests / Go pathRequests) bypasses the gate,
	// matching Python Transport.py:1701-1706. Go previously bypassed only the
	// relayed table (pendingPathRequests), so a path-response announce answering
	// the node's own request could be held on an ingress-limiting interface.
	t.Run("originated path request bypasses gate", func(t *testing.T) {
		t.Parallel()
		ts, iface := mkTS(t)
		id, dest := mkRemote(t, "origbypass")
		ts.mu.Lock()
		ts.ensureStateLocked()
		ts.pathRequests[string(dest.Hash)] = time.Now()
		ts.mu.Unlock()
		ts.handleAnnounce(mkAnnounce(t, id, dest, 1, iface), iface)
		if iface.HeldAnnounces() != 0 {
			t.Fatalf("HeldAnnounces = %d, want 0 (originated PR bypasses gate)", iface.HeldAnnounces())
		}
		if !inTable(ts, dest.Hash) {
			t.Fatal("announce for an originated-PR destination must be processed into the path table")
		}
	})

	t.Run("not ingress limited not held", func(t *testing.T) {
		t.Parallel()
		ts, iface := mkTS(t)
		iface.ingressLimit = false
		id, dest := mkRemote(t, "nolimit")
		ts.handleAnnounce(mkAnnounce(t, id, dest, 1, iface), iface)
		if iface.HeldAnnounces() != 0 {
			t.Fatalf("HeldAnnounces = %d, want 0 (ingress limit off)", iface.HeldAnnounces())
		}
		if !inTable(ts, dest.Hash) {
			t.Fatal("announce with ingress limit off must be processed into the path table")
		}
	})

	// Flooding announces for several distinct unknown destinations holds all of
	// them, but an announce for a destination with a pending path request is
	// still processed (not held) under the same flood.
	t.Run("flooding holds all but pending-PR dest", func(t *testing.T) {
		t.Parallel()
		ts, iface := mkTS(t)
		for range 5 {
			id, dest := mkRemote(t, "flood")
			ts.handleAnnounce(mkAnnounce(t, id, dest, 1, iface), iface)
		}
		prID, prDest := mkRemote(t, "prflood")
		ts.mu.Lock()
		ts.ensureStateLocked()
		ts.pendingPathRequests[string(prDest.Hash)] = []interfaces.Interface{iface}
		ts.mu.Unlock()
		ts.handleAnnounce(mkAnnounce(t, prID, prDest, 1, iface), iface)
		if iface.HeldAnnounces() != 5 {
			t.Fatalf("HeldAnnounces = %d, want 5 (flooded unknowns held; PR dest not held)", iface.HeldAnnounces())
		}
		if !inTable(ts, prDest.Hash) {
			t.Fatal("pending-PR announce must be processed into the path table even under flooding")
		}
	})
}
