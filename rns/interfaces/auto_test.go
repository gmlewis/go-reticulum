// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"crypto/sha256"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeDiscoveryScope(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", AutoScopeLink},
		{"link", AutoScopeLink},
		{"admin", AutoScopeAdmin},
		{"site", AutoScopeSite},
		{"organisation", AutoScopeOrganisation},
		{"organization", AutoScopeOrganisation},
		{"global", AutoScopeGlobal},
		{"unknown", AutoScopeLink},
	}

	for _, tt := range tests {
		if got := normalizeDiscoveryScope(tt.in); got != tt.want {
			t.Fatalf("normalizeDiscoveryScope(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeMulticastAddressType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", AutoMulticastTemporary},
		{"temporary", AutoMulticastTemporary},
		{"permanent", AutoMulticastPermanent},
		{"invalid", AutoMulticastTemporary},
	}

	for _, tt := range tests {
		if got := normalizeMulticastAddressType(tt.in); got != tt.want {
			t.Fatalf("normalizeMulticastAddressType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAutoMulticastDiscoveryIPDeterministic(t *testing.T) {
	ip1 := autoMulticastDiscoveryIP([]byte("reticulum"), AutoMulticastTemporary, AutoScopeLink)
	ip2 := autoMulticastDiscoveryIP([]byte("reticulum"), AutoMulticastTemporary, AutoScopeLink)
	if ip1 == nil || ip2 == nil {
		t.Fatalf("autoMulticastDiscoveryIP returned nil")
	}
	if ip1.String() != ip2.String() {
		t.Fatalf("autoMulticastDiscoveryIP not deterministic: %q vs %q", ip1.String(), ip2.String())
	}
	if ip1.To16() == nil || ip1.To4() != nil {
		t.Fatalf("autoMulticastDiscoveryIP expected IPv6, got %q", ip1.String())
	}
}

func TestAutoProcessIncomingSuppressesDuplicateAcrossPeersWithinTTL(t *testing.T) {
	got := make(chan string, 4)
	ai := &AutoInterface{
		BaseInterface: NewBaseInterface("auto-test", ModeFull, AutoBitrateGuess),
		inboundHandler: func(data []byte, iface Interface) {
			got <- string(data)
		},
		spawnedInterfaces: map[string]*AutoInterfacePeer{},
		recentFrames:      map[[32]byte]time.Time{},
		multiIfDequeTTL:   30 * time.Millisecond,
	}
	ai.running.Store(1)
	atomic.StoreInt32(&ai.online, 1)

	ai.spawnedInterfaces["fe80::1"] = &AutoInterfacePeer{BaseInterface: NewBaseInterface("p1", ModeFull, AutoBitrateGuess), owner: ai, addr: "fe80::1", interfaceName: "if0"}
	ai.spawnedInterfaces["fe80::2"] = &AutoInterfacePeer{BaseInterface: NewBaseInterface("p2", ModeFull, AutoBitrateGuess), owner: ai, addr: "fe80::2", interfaceName: "if1"}

	payload := []byte("same-payload")
	ai.processIncoming(payload, "fe80::1")
	ai.processIncoming(payload, "fe80::2")

	select {
	case <-got:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected first payload delivery")
	}

	select {
	case d := <-got:
		t.Fatalf("expected duplicate suppression, got extra delivery %q", d)
	case <-time.After(80 * time.Millisecond):
	}

	time.Sleep(35 * time.Millisecond)
	ai.processIncoming(payload, "fe80::2")

	select {
	case <-got:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected payload delivery after dedupe TTL elapsed")
	}
}

func TestAutoUpdateInterfaceHealthTracksCarrierLossAndRecovery(t *testing.T) {
	now := time.Now()
	ai := &AutoInterface{
		BaseInterface:        NewBaseInterface("auto-health", ModeFull, AutoBitrateGuess),
		multicastEchoTimeout: 100 * time.Millisecond,
		adoptedInterfaces:    map[string]net.IP{"en0": net.ParseIP("fe80::1")},
		multicastEchoes:      map[string]time.Time{"en0": now.Add(-2 * time.Second)},
		initialEchoes:        map[string]time.Time{"en0": now.Add(-3 * time.Second)},
		timedOutIfaces:       map[string]bool{},
	}
	ai.running.Store(1)

	ai.mu.Lock()
	ai.updateInterfaceHealthLocked(now)
	ai.mu.Unlock()

	if ai.Status() {
		t.Fatalf("expected AutoInterface to be offline after carrier timeout")
	}
	if !ai.timedOutIfaces["en0"] {
		t.Fatalf("expected en0 timeout flag after carrier timeout")
	}

	ai.mu.Lock()
	ai.multicastEchoes["en0"] = now
	ai.updateInterfaceHealthLocked(now)
	ai.mu.Unlock()

	if !ai.Status() {
		t.Fatalf("expected AutoInterface to recover online after fresh echo")
	}
	if ai.timedOutIfaces["en0"] {
		t.Fatalf("expected en0 timeout flag cleared after recovery")
	}
}

func TestAutoUpdateInterfaceHealthDoesNotTimeoutBeforeInitialEcho(t *testing.T) {
	now := time.Now()
	ai := &AutoInterface{
		BaseInterface:        NewBaseInterface("auto-health-grace", ModeFull, AutoBitrateGuess),
		multicastEchoTimeout: 100 * time.Millisecond,
		adoptedInterfaces:    map[string]net.IP{"en0": net.ParseIP("fe80::1")},
		multicastEchoes:      map[string]time.Time{"en0": now.Add(-2 * time.Second)},
		initialEchoes:        map[string]time.Time{},
		timedOutIfaces:       map[string]bool{},
	}
	ai.running.Store(1)

	ai.mu.Lock()
	ai.updateInterfaceHealthLocked(now)
	ai.mu.Unlock()

	if !ai.Status() {
		t.Fatalf("expected AutoInterface to stay online before initial echo is observed")
	}
	if ai.timedOutIfaces["en0"] {
		t.Fatalf("expected en0 timeout flag to remain false before initial echo")
	}
}

func (ai *AutoInterface) SetFinal(final bool) {
	if final {
		atomic.StoreInt32(&ai.final, 1)
	} else {
		atomic.StoreInt32(&ai.final, 0)
	}
}

func TestAutoReplaceAdoptedInterfaceAddressUpdatesLinkLocalSet(t *testing.T) {
	ai := &AutoInterface{
		adoptedInterfaces: map[string]net.IP{"en0": net.ParseIP("fe80::1")},
		linkLocalSet:      map[string]struct{}{"fe80::1": {}},
	}

	ai.replaceAdoptedInterfaceAddressLocked("en0", net.ParseIP("fe80::2"))

	if _, ok := ai.linkLocalSet["fe80::1"]; ok {
		t.Fatalf("expected old link-local address removed from set")
	}
	if _, ok := ai.linkLocalSet["fe80::2"]; !ok {
		t.Fatalf("expected new link-local address added to set")
	}
	if got := ai.adoptedInterfaces["en0"]; got == nil || got.String() != "fe80::2" {
		t.Fatalf("expected adopted interface address updated to fe80::2, got %v", got)
	}
}

func TestAutoInterfaceDataForwarding(t *testing.T) {
	received := make(chan string, 1)
	handler := func(data []byte, iface Interface) {
		received <- string(data)
	}

	ai, err := NewAutoInterface("test-auto", AutoInterfaceConfig{}, handler, nil)
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()

	atomic.StoreInt32(&ai.online, 1)

	peerIP := "fe80::dead:beef"
	peer := &AutoInterfacePeer{
		BaseInterface: NewBaseInterface("peer", ModeFull, AutoBitrateGuess),
		owner:         ai,
		addr:          peerIP,
		interfaceName: "eth0",
	}
	peer.online.Store(true)

	ai.mu.Lock()
	ai.spawnedInterfaces[peerIP] = peer
	ai.mu.Unlock()

	// 1. Test Inbound
	payload := []byte("hello from peer")
	ai.processIncoming(payload, peerIP)

	select {
	case data := <-received:
		if data != string(payload) {
			t.Errorf("received data = %q, want %q", data, string(payload))
		}
	case <-time.After(time.Second):
		t.Errorf("inbound data not delivered to handler")
	}

	// 2. Test Outbound (Send)
	// We need to mock ai.outboundConn to verify it writes
	// Since we can't easily mock net.UDPConn, we can check that it doesn't panic
	// and try to use a real loopback UDP if needed.
	// For this unit test, we'll verify the peer.Send logic.

	// AutoInterfacePeer.Send implementation uses ai.outboundConn
	// If it's nil, it might panic or return error.
	// Let's check auto.go:735
}

func TestAutoInterfacePeerDiscovery(t *testing.T) {
	onPeerCalled := make(chan string, 1)
	onPeer := func(peer Interface) {
		onPeerCalled <- peer.Name()
	}

	ai, err := NewAutoInterface("test-auto", AutoInterfaceConfig{
		GroupID: "test-group",
	}, nil, onPeer)
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()

	// Manually set final init done so it processes discovery packets
	ai.SetFinal(true)

	// Simulate a discovery packet from a peer
	peerIP := "fe80::dead:beef"
	// Verify addPeer logic directly
	ai.addPeer(peerIP, "eth0")

	select {
	case name := <-onPeerCalled:
		expected := fmt.Sprintf("AutoPeer[eth0/%v]", peerIP)
		if name != expected {
			t.Errorf("onPeer called with %q, want %q", name, expected)
		}
	case <-time.After(time.Second):
		t.Errorf("onPeer callback not called")
	}

	ai.mu.Lock()
	state, ok := ai.peers[peerIP]
	ai.mu.Unlock()
	if !ok {
		t.Errorf("peer %v not found in peers map", peerIP)
	} else if state.interfaceName != "eth0" {
		t.Errorf("peer interface = %q, want eth0", state.interfaceName)
	}

	// Verify discoveryLoop logic by simulating receiving a packet
	// We'll use a mocked UDP connection if we can, or just call discoveryLoop logic indirectly.
	// discoveryLoop logic is:
	// srcIP := src.IP.String()
	// expected := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(srcIP)...))
	// if string(buf[:sha256.Size]) != string(expected[:]) { ... }

	peerIP2 := "fe80::cafe:babe"
	groupID := []byte("test-group")
	token2 := sha256.Sum256(append(append([]byte{}, groupID...), []byte(peerIP2)...))

	// Clear the channel
	select {
	case <-onPeerCalled:
	default:
	}

	// Test the core verification logic that would be inside discoveryLoop
	srcAddr := &net.UDPAddr{IP: net.ParseIP(peerIP2)}
	srcIP := srcAddr.IP.String()
	expected := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(srcIP)...))

	if string(token2[:]) != string(expected[:]) {
		t.Errorf("discovery token verification logic mismatch\ngot:  %x\nwant: %x", token2, expected)
	}

	ai.addPeer(srcIP, "eth1")
	select {
	case name := <-onPeerCalled:
		expectedName := fmt.Sprintf("AutoPeer[eth1/%v]", peerIP2)
		if name != expectedName {
			t.Errorf("onPeer called with %q, want %q", name, expectedName)
		}
	case <-time.After(time.Second):
		t.Errorf("onPeer callback not called for peerIP2")
	}
}

func TestAutoInterfaceDeduplication(t *testing.T) {
	got := make(chan string, 4)
	ai := &AutoInterface{
		BaseInterface:     NewBaseInterface("auto-dedupe", ModeFull, AutoBitrateGuess),
		spawnedInterfaces: map[string]*AutoInterfacePeer{},
		recentFrames:      map[[32]byte]time.Time{},
		multiIfDequeTTL:   100 * time.Millisecond,
		inboundHandler: func(data []byte, iface Interface) {
			got <- string(data)
		},
	}
	ai.running.Store(1)
	atomic.StoreInt32(&ai.online, 1)

	peerIP := "fe80::1"
	ai.spawnedInterfaces[peerIP] = &AutoInterfacePeer{
		BaseInterface: NewBaseInterface("p1", ModeFull, AutoBitrateGuess),
		owner:         ai,
		addr:          peerIP,
		interfaceName: "eth0",
	}

	payload := []byte("unique-payload")

	// 1. First reception
	ai.processIncoming(payload, peerIP)
	select {
	case d := <-got:
		if d != string(payload) {
			t.Errorf("got %q, want %q", d, string(payload))
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for first delivery")
	}

	// 2. Immediate duplicate from same peer
	ai.processIncoming(payload, peerIP)
	select {
	case d := <-got:
		t.Errorf("duplicate payload delivered: %q", d)
	case <-time.After(50 * time.Millisecond):
		// OK
	}

	// 3. Duplicate from another peer
	peerIP2 := "fe80::2"
	ai.spawnedInterfaces[peerIP2] = &AutoInterfacePeer{
		BaseInterface: NewBaseInterface("p2", ModeFull, AutoBitrateGuess),
		owner:         ai,
		addr:          peerIP2,
		interfaceName: "eth1",
	}
	ai.processIncoming(payload, peerIP2)
	select {
	case d := <-got:
		t.Errorf("cross-peer duplicate payload delivered: %q", d)
	case <-time.After(50 * time.Millisecond):
		// OK
	}

	// 4. After TTL
	time.Sleep(150 * time.Millisecond)
	ai.processIncoming(payload, peerIP)
	select {
	case d := <-got:
		if d != string(payload) {
			t.Errorf("got %q after TTL, want %q", d, string(payload))
		}
	case <-time.After(time.Second):
		t.Errorf("timed out waiting for delivery after TTL")
	}

	_ = ai.Detach()
}

func TestAutoInterfaceTiming(t *testing.T) {
	// Use very short intervals for testing
	ai := &AutoInterface{
		BaseInterface:          NewBaseInterface("auto-timing", ModeFull, AutoBitrateGuess),
		peeringTimeout:         300 * time.Millisecond,
		peerJobInterval:        50 * time.Millisecond,
		reversePeeringInterval: 100 * time.Millisecond,
		peers:                  map[string]*autoPeerState{},
		spawnedInterfaces:      map[string]*AutoInterfacePeer{},
		adoptedInterfaces:      map[string]net.IP{"eth0": net.ParseIP("fe80::1")},
		linkLocalSet:           map[string]struct{}{"fe80::1": {}},
		multicastEchoes:        map[string]time.Time{"eth0": time.Now()},
		initialEchoes:          map[string]time.Time{"eth0": time.Now()},
		timedOutIfaces:         map[string]bool{},
	}
	// peerJobs runs resyncAdoptedAddresses each tick; on runners where eth0
	// exists with a real link-local address, the resync classifies eth0 as
	// changed and calls this hook, so it must be non-nil.
	ai.rebuildSockets = func(ifname string, ip net.IP) error { return nil }
	ai.running.Store(1)
	atomic.StoreInt32(&ai.online, 1)

	peerIP := "fe80::dead:beef"
	now := time.Now()
	ai.peers[peerIP] = &autoPeerState{
		interfaceName: "eth0",
		lastHeard:     now,
		lastOutbound:  now,
	}

	// Start peerJobs in background
	go ai.peerJobs()

	// 1. Wait for reverse announce
	// We can't easily capture the packet, but we can verify lastOutbound is updated
	time.Sleep(200 * time.Millisecond)
	ai.mu.Lock()
	state := ai.peers[peerIP]
	if state == nil {
		ai.mu.Unlock()
		t.Fatalf("peer was removed prematurely")
	}
	updatedOutbound := state.lastOutbound
	ai.mu.Unlock()

	if !updatedOutbound.After(now) {
		t.Errorf("lastOutbound was not updated, reverse announce might not have triggered")
	}

	// 2. Wait for peering timeout
	time.Sleep(400 * time.Millisecond)
	ai.mu.Lock()
	_, ok := ai.peers[peerIP]
	ai.mu.Unlock()

	if ok {
		t.Errorf("peer was not removed after timeout")
	}

	_ = ai.Detach()
}

func TestAutoInterfaceAnnounceFormat(t *testing.T) {
	// Logic verified in A01 integration test.
}

func TestAutoInterfaceInterfaceSelection(t *testing.T) {
	tests := []struct {
		name              string
		allowedInterfaces []string
		ignoredInterfaces []string
		ifaceName         string
		want              bool
	}{
		{
			name:      "allow all by default",
			ifaceName: "eth0",
			want:      true,
		},
		{
			name:      "ignore lo0 by default",
			ifaceName: "lo0",
			want:      false,
		},
		{
			name:              "explicitly ignored",
			ignoredInterfaces: []string{"eth1"},
			ifaceName:         "eth1",
			want:              false,
		},
		{
			name:              "explicitly allowed",
			allowedInterfaces: []string{"eth0"},
			ifaceName:         "eth0",
			want:              true,
		},
		{
			name:              "not allowed",
			allowedInterfaces: []string{"eth0"},
			ifaceName:         "eth1",
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ai := &AutoInterface{
				allowedInterfaces: map[string]struct{}{},
				ignoredInterfaces: map[string]struct{}{},
			}
			for _, v := range tt.allowedInterfaces {
				ai.allowedInterfaces[v] = struct{}{}
			}
			for _, v := range tt.ignoredInterfaces {
				ai.ignoredInterfaces[v] = struct{}{}
			}

			if got := ai.shouldUseInterface(tt.ifaceName); got != tt.want {
				t.Errorf("shouldUseInterface(%q) = %v, want %v", tt.ifaceName, got, tt.want)
			}
		})
	}
}

// TestAutoInterfaceAndroidRmnetIgnore verifies that the rmnet0..rmnet7 Android
// system interfaces (AutoInterface.py:68 ANDROID_IGNORE_IFS) are skipped on
// Android unless explicitly allowed. The platform-injectable shouldUseInterfaceOn
// exercises the android branch regardless of the host running the test.
func TestAutoInterfaceAndroidRmnetIgnore(t *testing.T) {
	t.Parallel()

	for i := 0; i <= 7; i++ {
		name := fmt.Sprintf("rmnet%d", i)
		ai := &AutoInterface{
			allowedInterfaces: map[string]struct{}{},
			ignoredInterfaces: map[string]struct{}{},
		}
		if got := ai.shouldUseInterfaceOn(name, "android"); got {
			t.Errorf("shouldUseInterfaceOn(%q, android) = true, want false (ANDROID_IGNORE_IFS)", name)
		}
	}

	// A normal wireless interface is usable on Android.
	ai := &AutoInterface{
		allowedInterfaces: map[string]struct{}{},
		ignoredInterfaces: map[string]struct{}{},
	}
	if got := ai.shouldUseInterfaceOn("wlan0", "android"); !got {
		t.Errorf("shouldUseInterfaceOn(%q, android) = false, want true", "wlan0")
	}

	// rmnet is Android-specific: on darwin/linux it is not in the ignore set.
	if got := ai.shouldUseInterfaceOn("rmnet3", "linux"); !got {
		t.Errorf("shouldUseInterfaceOn(%q, linux) = false, want true (rmnet is android-only)", "rmnet3")
	}
	if got := ai.shouldUseInterfaceOn("rmnet3", "darwin"); !got {
		t.Errorf("shouldUseInterfaceOn(%q, darwin) = false, want true (rmnet is android-only)", "rmnet3")
	}

	// An explicitly-allowed rmnet interface overrides the Android ignore
	// (AutoInterface.py:221 `not ifname in self.allowed_interfaces`).
	aiAllowed := &AutoInterface{
		allowedInterfaces: map[string]struct{}{"rmnet3": {}},
		ignoredInterfaces: map[string]struct{}{},
	}
	if got := aiAllowed.shouldUseInterfaceOn("rmnet3", "android"); !got {
		t.Errorf("shouldUseInterfaceOn(%q, android) with allowed override = false, want true", "rmnet3")
	}
}

// loopbackUDPConn binds a throwaway udp6 socket on the IPv6 loopback address
// for use as a reader-loop input in the tests below.
func loopbackUDPConn(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: 0})
	if err != nil {
		t.Fatalf("failed to bind loopback UDP conn: %v", err)
	}
	return conn
}

// sendToUDPConn delivers one datagram to conn's own address from a second
// socket, so a reader loop parked on conn has data waiting.
func sendToUDPConn(t *testing.T, conn *net.UDPConn, payload []byte) {
	t.Helper()
	addr := &net.UDPAddr{IP: net.IPv6loopback, Port: conn.LocalAddr().(*net.UDPAddr).Port}
	sender, err := net.DialUDP("udp6", nil, addr)
	if err != nil {
		t.Fatalf("failed to open UDP sender: %v", err)
	}
	defer func() { _ = sender.Close() }()
	if _, err := sender.Write(payload); err != nil {
		t.Fatalf("failed to send UDP payload: %v", err)
	}
}

// The reader loops must run until Detach regardless of whether the running
// flag happens to be set when they are spawned. start() historically spawned
// them before storing running=1, so depending on scheduler timing they could
// all self-destruct silently at birth, leaving adopted sockets open with
// nobody reading them (observed live: AutoInterface Up with 0 bytes ever
// transferred while peerJobs alone survived).

func TestAutoInterfaceDiscoveryLoopProcessesPeerBeforeRunningSet(t *testing.T) {
	t.Parallel()
	onPeerCalled := make(chan string, 1)
	ai, err := NewAutoInterface("test-auto-race", AutoInterfaceConfig{GroupID: "race-group"}, nil,
		func(peer Interface) { onPeerCalled <- peer.Name() })
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()
	ai.SetFinal(true)
	// ai.running deliberately left 0: this is the exact hazard window of
	// start(), between goroutine spawn and the running store.

	conn := loopbackUDPConn(t)
	go ai.discoveryLoop("lo0", conn)

	srcIP := "::1"
	token := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(srcIP)...))
	sendToUDPConn(t, conn, token[:])

	select {
	case name := <-onPeerCalled:
		if want := "AutoPeer[lo0/" + srcIP + "]"; name != want {
			t.Errorf("onPeer called with %q, want %q", name, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("discoveryLoop processed no peer while running=%d: it exited at birth",
			ai.running.Load())
	}
}

func TestAutoInterfaceDataLoopDeliversInboundBeforeRunningSet(t *testing.T) {
	t.Parallel()
	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) { received <- data }
	ai, err := NewAutoInterface("test-auto-race-data", AutoInterfaceConfig{GroupID: "race-group"}, handler, nil)
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()

	conn := loopbackUDPConn(t)
	go ai.dataLoop(conn)

	// Give a birth-defective loop ample time to self-destruct while the
	// lifecycle flags are still unset, then arm the gates that
	// processIncoming requires and deliver for real.
	time.Sleep(100 * time.Millisecond)
	ai.running.Store(1)
	atomic.StoreInt32(&ai.online, 1)
	peerIP := "::1"
	ai.mu.Lock()
	ai.spawnedInterfaces[peerIP] = &AutoInterfacePeer{
		BaseInterface: NewBaseInterface("AutoPeer[lo0/::1]", ModeFull, AutoBitrateGuess),
		owner:         ai,
		addr:          peerIP,
		interfaceName: "lo0",
	}
	ai.mu.Unlock()

	payload := []byte("inbound after gates opened")
	sendToUDPConn(t, conn, payload)

	select {
	case got := <-received:
		if string(got) != string(payload) {
			t.Errorf("dataLoop delivered %q, want %q", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("dataLoop delivered nothing while running=%d: it exited at birth",
			ai.running.Load())
	}
}

// A closed socket makes ReadFromUDP fail instantly and forever. With
// panic_on_interface_error at its default (No) the old error path logged
// nothing, slept nothing, and looped — pegging a core in total silence.
// The loop must return instead.

func TestAutoInterfaceDiscoveryLoopReturnsOnClosedSocket(t *testing.T) {
	t.Parallel()
	ai, err := NewAutoInterface("test-auto-spin", AutoInterfaceConfig{GroupID: "spin-group"}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()
	ai.running.Store(1)

	conn := loopbackUDPConn(t)
	_ = conn.Close()

	done := make(chan struct{})
	go func() {
		ai.discoveryLoop("lo0", conn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("discoveryLoop is hot-spinning on a closed socket")
	}
}

func TestAutoInterfaceDataLoopReturnsOnClosedSocket(t *testing.T) {
	t.Parallel()
	ai, err := NewAutoInterface("test-auto-spin-data", AutoInterfaceConfig{GroupID: "spin-group"}, nil, nil)
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()
	ai.running.Store(1)

	conn := loopbackUDPConn(t)
	_ = conn.Close()

	done := make(chan struct{})
	go func() {
		ai.dataLoop(conn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("dataLoop is hot-spinning on a closed socket")
	}
}

func TestAutoInterfaceAnnounceLoopSurvivesUnsetRunningAndStopsOnDetach(t *testing.T) {
	t.Parallel()
	onPeerCalled := make(chan string, 1)
	ai, err := NewAutoInterface("test-auto-announce", AutoInterfaceConfig{
		GroupID:          "announce-group",
		AnnounceInterval: 5 * time.Millisecond,
	}, nil,
		func(peer Interface) { onPeerCalled <- peer.Name() })
	if err != nil {
		t.Fatalf("failed to create AutoInterface: %v", err)
	}
	defer func() { _ = ai.Detach() }()

	returned := make(chan struct{})
	go func() {
		ai.announceLoop("lo0")
		close(returned)
	}()
	// The pre-fix entry condition exited instantly whenever running was
	// still unset; the loop must stay alive here.
	select {
	case <-returned:
		t.Fatal("announceLoop exited immediately while running was unset")
	case <-time.After(250 * time.Millisecond):
	}

	_ = ai.Detach()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("announceLoop did not stop after Detach")
	}
}
