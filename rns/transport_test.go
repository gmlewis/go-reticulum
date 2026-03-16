// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

func TestTransport(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "rns-transport-test")
	mustTest(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	ts := GetTransport()
	if err := ts.Start(tmpDir); err != nil {
		t.Fatalf("Transport start failed: %v", err)
	}

	if ts.identity == nil {
		t.Errorf("Transport identity not initialized")
	}

	// Test registration
	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "app")

	ts.mu.Lock()
	found := false
	for _, d := range ts.destinations {
		if d == dest {
			found = true
			break
		}
	}
	ts.mu.Unlock()

	if !found {
		t.Errorf("Destination not registered in Transport")
	}
}

func TestHandleAnnounce(t *testing.T) {
	// LogLevel = LogDebug
	ts := &TransportSystem{
		interfaces:   make([]interfaces.Interface, 0),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
		pathTable:    make(map[string]*PathEntry),
		reverseTable: make(map[string]*ReverseEntry),
		linkTable:    make(map[string]*LinkEntry),
		packetHashes: make(map[string]time.Time),
	}
	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "testapp")

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

func TestRequestPathThrottleAndTag(t *testing.T) {
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	capIface := &capturingInterface{name: "cap"}
	ts.interfaces = append(ts.interfaces, capIface)

	destHash := bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)
	if err := ts.RequestPath(destHash); err != nil {
		t.Fatalf("first RequestPath failed: %v", err)
	}
	if capIface.sendCount != 1 {
		t.Fatalf("expected exactly one request transmission, got %v", capIface.sendCount)
	}

	if err := ts.RequestPath(destHash); err != nil {
		t.Fatalf("second RequestPath failed: %v", err)
	}
	if capIface.sendCount != 1 {
		t.Fatalf("expected throttled second request not to transmit, got %v sends", capIface.sendCount)
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

func TestHandlePathRequestEmitsTargetedPathResponse(t *testing.T) {
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	tsid := mustTestNewIdentity(t, true)
	ts.identity = tsid

	recvIface := &capturingInterface{name: "recv"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, recvIface, otherIface)

	localID := mustTestNewIdentity(t, true)
	localDest := mustTestNewDestinationWithTransport(t, ts, localID, DestinationIn, DestinationSingle, "pathreq", "target")

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

func TestAnnounceRebroadcastProcessing(t *testing.T) {
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}
	tsID := mustTestNewIdentity(t, true)
	ts.identity = tsID

	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound"}
	ts.interfaces = append(ts.interfaces, source, outbound)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "testapp")

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

	if outbound.sendCount != 1 {
		t.Fatalf("expected one rebroadcast on outbound interface, got %v", outbound.sendCount)
	}
	if source.sendCount != 0 {
		t.Fatalf("expected no rebroadcast on source interface, got %v", source.sendCount)
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

func TestPathResponseAnnounceNotRebroadcast(t *testing.T) {
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	source := &capturingInterface{name: "source"}
	outbound := &capturingInterface{name: "outbound"}
	ts.interfaces = append(ts.interfaces, source, outbound)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "path-response-test")

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
	if outbound.sendCount != 0 {
		t.Fatalf("expected no rebroadcast transmissions for path-response announce, got %v", outbound.sendCount)
	}
}

func TestInvalidatePathByHash(t *testing.T) {
	ts := &TransportSystem{
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

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
	ts := &TransportSystem{
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

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
	ts := &TransportSystem{
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

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
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	failing := &failingInterface{name: "failing"}
	good := &capturingInterface{name: "good"}
	ts.interfaces = append(ts.interfaces, failing, good)

	ts.pathTable["via-failing"] = &PathEntry{Interface: failing, Expires: time.Now().Add(time.Hour)}
	ts.pathTable["via-good"] = &PathEntry{Interface: good, Expires: time.Now().Add(time.Hour)}

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "outbound-test")
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
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "ifac-drop")
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
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	iface := &ifacTransformInterface{name: "ifac-transform"}
	ts.interfaces = append(ts.interfaces, iface)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, id, DestinationIn, DestinationSingle, "ifac-out")
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
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	routeIface := &capturingInterface{name: "route"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, routeIface, otherIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestinationWithTransport(t, ts, remoteID, DestinationOut, DestinationSingle, "route-test")
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
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	routeIface := &capturingInterface{name: "route"}
	otherIface := &capturingInterface{name: "other"}
	ts.interfaces = append(ts.interfaces, routeIface, otherIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestinationWithTransport(t, ts, remoteID, DestinationOut, DestinationSingle, "route-test-multihop")
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
	setTransportEnabled(true)
	defer setTransportEnabled(false)
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	identity := mustTestNewIdentity(t, true)
	ts.identity = identity

	inboundIface := &capturingInterface{name: "inbound"}
	forwardIface := &capturingInterface{name: "forward"}
	ts.interfaces = append(ts.interfaces, inboundIface, forwardIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestinationWithTransport(t, nil, remoteID, DestinationOut, DestinationSingle, "inbound-forward")
	nextHop := bytes.Repeat([]byte{0x55}, TruncatedHashLength/8)
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Interface: forwardIface,
		Hops:      3,
		NextHop:   nextHop,
		Expires:   time.Now().Add(time.Hour),
	}

	p := NewPacketWithTransport(ts, dest, []byte("forward-me"))
	p.HeaderType = Header2
	p.TransportID = identity.Hash
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

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
	setTransportEnabled(true)
	defer setTransportEnabled(false)
	ts := &TransportSystem{
		interfaces:    make([]interfaces.Interface, 0),
		destinations:  make([]*Destination, 0),
		pendingLinks:  make([]*Link, 0),
		activeLinks:   make([]*Link, 0),
		pathTable:     make(map[string]*PathEntry),
		reverseTable:  make(map[string]*ReverseEntry),
		linkTable:     make(map[string]*LinkEntry),
		packetHashes:  make(map[string]time.Time),
		announceTable: make(map[string]*AnnounceEntry),
		pathRequests:  make(map[string]time.Time),
	}

	identity := mustTestNewIdentity(t, true)
	ts.identity = identity

	inboundIface := &capturingInterface{name: "inbound"}
	forwardIface := &capturingInterface{name: "forward"}
	ts.interfaces = append(ts.interfaces, inboundIface, forwardIface)

	remoteID := mustTestNewIdentity(t, true)
	dest := mustTestNewDestinationWithTransport(t, nil, remoteID, DestinationOut, DestinationSingle, "inbound-final-hop")
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Interface: forwardIface,
		Hops:      1,
		NextHop:   bytes.Repeat([]byte{0x66}, TruncatedHashLength/8),
		Expires:   time.Now().Add(time.Hour),
	}

	p := NewPacketWithTransport(ts, dest, []byte("final-hop"))
	p.HeaderType = Header2
	p.TransportID = identity.Hash
	if err := p.Pack(); err != nil {
		t.Fatalf("pack failed: %v", err)
	}

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
	ts := &TransportSystem{
		packetHashes:       make(map[string]time.Time),
		packetHashesPrev:   make(map[string]time.Time),
		packetHashRotateAt: 2,
		pathTable:          make(map[string]*PathEntry),
		reverseTable:       make(map[string]*ReverseEntry),
		linkTable:          make(map[string]*LinkEntry),
		announceTable:      make(map[string]*AnnounceEntry),
		pathRequests:       make(map[string]time.Time),
	}

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
	ts := &TransportSystem{
		pathTable:          make(map[string]*PathEntry),
		reverseTable:       make(map[string]*ReverseEntry),
		linkTable:          make(map[string]*LinkEntry),
		packetHashes:       make(map[string]time.Time),
		packetHashesPrev:   make(map[string]time.Time),
		packetHashRotateAt: packetHashRotateDefault,
		announceTable:      make(map[string]*AnnounceEntry),
		pathRequests:       make(map[string]time.Time),
	}

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
	tmpDir, err := os.MkdirTemp("", "rns-path-persist-*")
	mustTest(t, err)
	defer func() { _ = os.RemoveAll(tmpDir) }()

	iface := &capturingInterface{name: "persist-iface"}
	ts := &TransportSystem{
		storagePath:        tmpDir,
		interfaces:         []interfaces.Interface{iface},
		pathTable:          make(map[string]*PathEntry),
		reverseTable:       make(map[string]*ReverseEntry),
		linkTable:          make(map[string]*LinkEntry),
		packetHashes:       make(map[string]time.Time),
		packetHashesPrev:   make(map[string]time.Time),
		packetHashRotateAt: packetHashRotateDefault,
		announceTable:      make(map[string]*AnnounceEntry),
		pathRequests:       make(map[string]time.Time),
	}

	destHash := []byte("persist-destination")
	nextHop := []byte("persist-next-hop")
	timestamp := time.Now().Truncate(time.Second)
	expires := timestamp.Add(time.Hour)
	packet := []byte("persist-packet")

	ts.pathTable[string(destHash)] = &PathEntry{
		Timestamp:     timestamp,
		NextHop:       nextHop,
		Hops:          3,
		Expires:       expires,
		Interface:     iface,
		InterfaceName: iface.Name(),
		Packet:        packet,
	}

	ts.persistPathTable()
	if _, err := os.Stat(filepath.Join(tmpDir, "destination_table")); err != nil {
		t.Fatalf("expected persisted destination_table file: %v", err)
	}

	tsLoaded := &TransportSystem{
		storagePath:      tmpDir,
		interfaces:       make([]interfaces.Interface, 0),
		pathTable:        make(map[string]*PathEntry),
		reverseTable:     make(map[string]*ReverseEntry),
		linkTable:        make(map[string]*LinkEntry),
		packetHashes:     make(map[string]time.Time),
		packetHashesPrev: make(map[string]time.Time),
		announceTable:    make(map[string]*AnnounceEntry),
		pathRequests:     make(map[string]time.Time),
	}

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
	if loaded.InterfaceName != "persist-iface" {
		t.Fatalf("interface name mismatch after load: got %q", loaded.InterfaceName)
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

type dummyInterface struct {
	name string
}

func (d *dummyInterface) Name() string           { return d.name }
func (d *dummyInterface) Type() string           { return "dummy" }
func (d *dummyInterface) IsOut() bool            { return true }
func (d *dummyInterface) Status() bool           { return true }
func (d *dummyInterface) Mode() int              { return 1 }
func (d *dummyInterface) Bitrate() int           { return 1000 }
func (d *dummyInterface) Send(data []byte) error { return nil }
func (d *dummyInterface) BytesReceived() uint64  { return 0 }
func (d *dummyInterface) BytesSent() uint64      { return 0 }
func (d *dummyInterface) Detach() error          { return nil }
func (d *dummyInterface) IsDetached() bool       { return false }
func (d *dummyInterface) Age() time.Duration     { return 0 }

type capturingInterface struct {
	name      string
	sendCount int
	lastSent  []byte
}

func (c *capturingInterface) Name() string          { return c.name }
func (c *capturingInterface) Type() string          { return "capture" }
func (c *capturingInterface) IsOut() bool           { return true }
func (c *capturingInterface) Status() bool          { return true }
func (c *capturingInterface) Mode() int             { return 1 }
func (c *capturingInterface) Bitrate() int          { return 1000 }
func (c *capturingInterface) BytesReceived() uint64 { return 0 }
func (c *capturingInterface) BytesSent() uint64     { return 0 }
func (c *capturingInterface) Detach() error         { return nil }
func (c *capturingInterface) IsDetached() bool      { return false }
func (c *capturingInterface) Age() time.Duration    { return 0 }
func (c *capturingInterface) Send(data []byte) error {
	c.sendCount++
	c.lastSent = make([]byte, len(data))
	copy(c.lastSent, data)
	return nil
}

type failingInterface struct {
	name string
}

func (f *failingInterface) Name() string          { return f.name }
func (f *failingInterface) Type() string          { return "failing" }
func (f *failingInterface) IsOut() bool           { return true }
func (f *failingInterface) Status() bool          { return true }
func (f *failingInterface) Mode() int             { return 1 }
func (f *failingInterface) Bitrate() int          { return 1000 }
func (f *failingInterface) BytesReceived() uint64 { return 0 }
func (f *failingInterface) BytesSent() uint64     { return 0 }
func (f *failingInterface) Detach() error         { return nil }
func (f *failingInterface) IsDetached() bool      { return false }
func (f *failingInterface) Age() time.Duration    { return 0 }
func (f *failingInterface) Send(data []byte) error {
	return os.ErrClosed
}

type ifacDropInterface struct {
	name          string
	inboundCalled bool
}

func (i *ifacDropInterface) Name() string          { return i.name }
func (i *ifacDropInterface) Type() string          { return "ifac-drop" }
func (i *ifacDropInterface) IsOut() bool           { return true }
func (i *ifacDropInterface) Status() bool          { return true }
func (i *ifacDropInterface) Mode() int             { return 1 }
func (i *ifacDropInterface) Bitrate() int          { return 1000 }
func (i *ifacDropInterface) BytesReceived() uint64 { return 0 }
func (i *ifacDropInterface) BytesSent() uint64     { return 0 }
func (i *ifacDropInterface) Detach() error         { return nil }
func (i *ifacDropInterface) IsDetached() bool      { return false }
func (i *ifacDropInterface) Age() time.Duration    { return 0 }
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

func (i *ifacTransformInterface) Name() string          { return i.name }
func (i *ifacTransformInterface) Type() string          { return "ifac-transform" }
func (i *ifacTransformInterface) IsOut() bool           { return true }
func (i *ifacTransformInterface) Status() bool          { return true }
func (i *ifacTransformInterface) Mode() int             { return 1 }
func (i *ifacTransformInterface) Bitrate() int          { return 1000 }
func (i *ifacTransformInterface) BytesReceived() uint64 { return 0 }
func (i *ifacTransformInterface) BytesSent() uint64     { return 0 }
func (i *ifacTransformInterface) Detach() error         { return nil }
func (i *ifacTransformInterface) IsDetached() bool      { return false }
func (i *ifacTransformInterface) Age() time.Duration    { return 0 }
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

func TestTransportBlackholeRegistry(t *testing.T) {
	ResetTransport()
	defer ResetTransport()

	ts := GetTransport()
	hash := []byte{0x01, 0x02, 0x03}
	until := time.Now().Add(time.Hour).Unix()

	if ok := ts.BlackholeIdentity(hash, &until, "test-reason"); !ok {
		t.Fatalf("BlackholeIdentity returned false")
	}

	entries := ts.GetBlackholedIdentities()
	if len(entries) != 1 {
		t.Fatalf("expected 1 blackhole entry, got %v", len(entries))
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
	ResetTransport()
	defer ResetTransport()

	ts := GetTransport()
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.announceTable["dest1"] = &AnnounceEntry{}
	ts.announceTable["dest2"] = &AnnounceEntry{}
	ts.pendingPathRequests["dest1"] = nil
	ts.pendingPathRequestAt["dest1"] = time.Now()
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
}

func TestTransportPacketMetricCaches(t *testing.T) {
	ResetTransport()
	defer ResetTransport()

	ts := GetTransport()
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
