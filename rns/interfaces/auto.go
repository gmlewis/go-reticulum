// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Constants defining the structural limits and default behaviors for AutoInterfaces.
const (
	// AutoHWMTU defines the absolute maximum transmission unit byte size supported at the hardware layer for this interface type.
	AutoHWMTU = 1196

	// AutoDefaultDiscoveryPort specifies the standard UDP port utilized for broadcasting presence announcements and discovery packets.
	AutoDefaultDiscoveryPort = 29716
	// AutoDefaultDataPort specifies the standard UDP port allocated for actual payload data transmission between peers.
	AutoDefaultDataPort = 42671
	// AutoDefaultGroupID dictates the default network partitioning ID to ensure discovery frames are constrained to intended participants.
	AutoDefaultGroupID = "reticulum"
	// AutoDefaultIFACSize specifies the standard byte length for the cryptographic IFAC authentication signature.
	AutoDefaultIFACSize = 16

	// AutoScopeLink restricts the IPv6 multicast scope strictly to the local physical link.
	AutoScopeLink = "2"
	// AutoScopeAdmin elevates the multicast scope to the administrative boundary.
	AutoScopeAdmin = "4"
	// AutoScopeSite expands the multicast propagation domain to the site level.
	AutoScopeSite = "5"
	// AutoScopeOrganisation broadens the multicast envelope to encompass the entire organization's routing boundary.
	AutoScopeOrganisation = "8"
	// AutoScopeGlobal establishes a globally routable multicast domain.
	AutoScopeGlobal = "e"

	// AutoMulticastPermanent designates the multicast address as permanently assigned and universally recognized by routing infrastructure.
	AutoMulticastPermanent = "0"
	// AutoMulticastTemporary indicates the multicast address is transient and locally administered.
	AutoMulticastTemporary = "1"

	// AutoPeeringTimeout establishes the maximum duration a peer can remain silent before its state is purged and resources reclaimed.
	AutoPeeringTimeout = 22 * time.Second
	// AutoAnnounceInterval sets the pacing frequency at which this interface broadcasts its existence to the local broadcast domain.
	AutoAnnounceInterval = 1600 * time.Millisecond
	// AutoPeerJobInterval determines the execution frequency for the background worker responsible for culling dead peers and managing state.
	AutoPeerJobInterval = 4 * time.Second
	// AutoMcastEchoTimeout defines the threshold after which missing local multicast loopbacks signal a potential network partition or socket failure.
	AutoMcastEchoTimeout = 6500 * time.Millisecond
	// AutoMultiIFDequeTTL dictates how long an incoming frame's hash is cached to rigorously suppress duplicate packet loops across bridged interfaces.
	AutoMultiIFDequeTTL = 750 * time.Millisecond
	// AutoBitrateGuess furnishes a conservative fallback estimation of the underlying interface's operational capacity, expressed in bits per second.
	AutoBitrateGuess = 10 * 1000 * 1000
)

var (
	autoAllIgnoreIfs     = map[string]struct{}{"lo0": {}}
	autoDarwinIgnoreIfs  = map[string]struct{}{"awdl0": {}, "llw0": {}, "lo0": {}, "en5": {}}
	autoAndroidIgnoreIfs = map[string]struct{}{"dummy0": {}, "lo": {}, "tun0": {}}
)

// AutoInterfaceConfig defines the comprehensive suite of initialization parameters required to bootstrap an AutoInterface.
// It exposes low-level tuning for network namespaces, port allocations, and device binding constraints.
type AutoInterfaceConfig struct {
	// GroupID scopes discovery traffic so only peers with the same group
	// participate in the same AutoInterface overlay.
	GroupID string
	// DiscoveryScope selects the IPv6 multicast scope used for discovery
	// announcements, for example link, site, or global.
	DiscoveryScope string
	// DiscoveryPort is the UDP port used for multicast discovery announcements.
	DiscoveryPort int
	// MulticastAddressType selects the multicast address class used when
	// deriving the discovery address.
	MulticastAddressType string
	// DataPort is the UDP port used for direct peer-to-peer payload traffic.
	DataPort int
	// Devices restricts discovery to the named network interfaces when non-empty.
	Devices []string
	// IgnoredDevices excludes specific network interfaces from discovery.
	IgnoredDevices []string
	// ConfiguredBitrate overrides the default bitrate estimate reported by the
	// interface when greater than zero.
	ConfiguredBitrate int
}

type autoPeerState struct {
	interfaceName string
	lastHeard     time.Time
	lastOutbound  time.Time
}

type autoSocketSet struct {
	multicast *net.UDPConn
	unicast   *net.UDPConn
	data      *net.UDPConn
}

// AutoInterface orchestrates fully autonomous, zero-configuration peer discovery and data plane management over IPv6 link-local networks.
// It actively manages a dynamic pool of peer sub-interfaces, transparently handling multicast announcements, socket lifecycle, and duplicate frame suppression.
type AutoInterface struct {
	*BaseInterface

	inboundHandler InboundHandler
	onPeer         func(Interface)

	groupID []byte

	discoveryPort        int
	unicastDiscoveryPort int
	dataPort             int
	discoveryScope       string
	multicastAddressType string
	mcastDiscoveryAddr   net.IP

	announceInterval       time.Duration
	peerJobInterval        time.Duration
	peeringTimeout         time.Duration
	multicastEchoTimeout   time.Duration
	reversePeeringInterval time.Duration
	multiIfDequeTTL        time.Duration

	allowedInterfaces map[string]struct{}
	ignoredInterfaces map[string]struct{}

	adoptedInterfaces map[string]net.IP
	linkLocalSet      map[string]struct{}
	sockets           map[string]*autoSocketSet

	peers             map[string]*autoPeerState
	spawnedInterfaces map[string]*AutoInterfacePeer
	multicastEchoes   map[string]time.Time
	initialEchoes     map[string]time.Time
	timedOutIfaces    map[string]bool
	recentFrames      map[[32]byte]time.Time

	outboundConn *net.UDPConn

	writeMu sync.Mutex
	mu      sync.Mutex

	running int32
	online  int32
	final   int32
}

// NewAutoInterface strategically provisions and activates a new autonomous discovery interface.
// It parses the provided configuration, allocates necessary UDP sockets across allowable hardware interfaces, and spawns the core asynchronous multiplexing loops.
func NewAutoInterface(name string, cfg AutoInterfaceConfig, handler InboundHandler, onPeer func(Interface)) (*AutoInterface, error) {
	bi := NewBaseInterface(name, ModeFull, AutoBitrateGuess)
	ai := &AutoInterface{
		BaseInterface: bi,

		inboundHandler: handler,
		onPeer:         onPeer,

		discoveryPort: AutoDefaultDiscoveryPort,
		dataPort:      AutoDefaultDataPort,

		announceInterval:       AutoAnnounceInterval,
		peerJobInterval:        AutoPeerJobInterval,
		peeringTimeout:         AutoPeeringTimeout,
		multicastEchoTimeout:   AutoMcastEchoTimeout,
		reversePeeringInterval: time.Duration(float64(AutoAnnounceInterval) * 3.25),
		multiIfDequeTTL:        AutoMultiIFDequeTTL,

		allowedInterfaces: map[string]struct{}{},
		ignoredInterfaces: map[string]struct{}{},

		adoptedInterfaces: map[string]net.IP{},
		linkLocalSet:      map[string]struct{}{},
		sockets:           map[string]*autoSocketSet{},

		peers:             map[string]*autoPeerState{},
		spawnedInterfaces: map[string]*AutoInterfacePeer{},
		multicastEchoes:   map[string]time.Time{},
		initialEchoes:     map[string]time.Time{},
		timedOutIfaces:    map[string]bool{},
		recentFrames:      map[[32]byte]time.Time{},
	}

	ai.groupID = []byte(strings.TrimSpace(cfg.GroupID))
	if len(ai.groupID) == 0 {
		ai.groupID = []byte(AutoDefaultGroupID)
	}

	if cfg.DiscoveryPort > 0 {
		ai.discoveryPort = cfg.DiscoveryPort
	}
	ai.unicastDiscoveryPort = ai.discoveryPort + 1
	if cfg.DataPort > 0 {
		ai.dataPort = cfg.DataPort
	}

	ai.multicastAddressType = normalizeMulticastAddressType(cfg.MulticastAddressType)
	ai.discoveryScope = normalizeDiscoveryScope(cfg.DiscoveryScope)
	ai.mcastDiscoveryAddr = autoMulticastDiscoveryIP(ai.groupID, ai.multicastAddressType, ai.discoveryScope)

	for _, dev := range cfg.Devices {
		dev = strings.TrimSpace(dev)
		if dev != "" {
			ai.allowedInterfaces[dev] = struct{}{}
		}
	}
	for _, dev := range cfg.IgnoredDevices {
		dev = strings.TrimSpace(dev)
		if dev != "" {
			ai.ignoredInterfaces[dev] = struct{}{}
		}
	}

	if cfg.ConfiguredBitrate > 0 {
		ai.bitrate = cfg.ConfiguredBitrate
	}

	if err := ai.start(); err != nil {
		return ai, err
	}

	return ai, nil
}

func normalizeMulticastAddressType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "permanent":
		return AutoMulticastPermanent
	case "temporary", "":
		return AutoMulticastTemporary
	default:
		return AutoMulticastTemporary
	}
}

func normalizeDiscoveryScope(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "link", "":
		return AutoScopeLink
	case "admin":
		return AutoScopeAdmin
	case "site":
		return AutoScopeSite
	case "organisation", "organization":
		return AutoScopeOrganisation
	case "global":
		return AutoScopeGlobal
	default:
		return AutoScopeLink
	}
}

func autoMulticastDiscoveryIP(groupID []byte, multicastType, scope string) net.IP {
	h := sha256.Sum256(groupID)
	segments := []string{
		"0",
		fmt.Sprintf("%02x", int(h[3])+(int(h[2])<<8)),
		fmt.Sprintf("%02x", int(h[5])+(int(h[4])<<8)),
		fmt.Sprintf("%02x", int(h[7])+(int(h[6])<<8)),
		fmt.Sprintf("%02x", int(h[9])+(int(h[8])<<8)),
		fmt.Sprintf("%02x", int(h[11])+(int(h[10])<<8)),
		fmt.Sprintf("%02x", int(h[13])+(int(h[12])<<8)),
	}
	addr := "ff" + multicastType + scope + ":" + strings.Join(segments, ":")
	return net.ParseIP(addr)
}

func (ai *AutoInterface) start() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}

	started := 0
	for _, iface := range ifaces {
		if !ai.shouldUseInterface(iface.Name) {
			continue
		}
		linkLocal, ok := firstLinkLocalIPv6(iface)
		if !ok {
			continue
		}
		if err := ai.startInterfaceSockets(iface, linkLocal); err != nil {
			continue
		}
		started++
	}

	atomic.StoreInt32(&ai.running, 1)
	if started > 0 {
		atomic.StoreInt32(&ai.online, 1)
	}

	go ai.peerJobs()
	go ai.finalInitBarrier()
	return nil
}

func (ai *AutoInterface) finalInitBarrier() {
	time.Sleep(time.Duration(float64(ai.announceInterval) * 1.2))
	if atomic.LoadInt32(&ai.running) == 1 {
		atomic.StoreInt32(&ai.final, 1)
	}
}

func (ai *AutoInterface) shouldUseInterface(name string) bool {
	if _, ok := ai.ignoredInterfaces[name]; ok {
		return false
	}
	if _, ok := autoAllIgnoreIfs[name]; ok {
		return false
	}

	switch runtime.GOOS {
	case "darwin":
		if _, ok := autoDarwinIgnoreIfs[name]; ok {
			if _, allowed := ai.allowedInterfaces[name]; !allowed {
				return false
			}
		}
	case "android":
		if _, ok := autoAndroidIgnoreIfs[name]; ok {
			if _, allowed := ai.allowedInterfaces[name]; !allowed {
				return false
			}
		}
	}

	if len(ai.allowedInterfaces) > 0 {
		_, ok := ai.allowedInterfaces[name]
		return ok
	}
	return true
}

func firstLinkLocalIPv6(iface net.Interface) (net.IP, bool) {
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, false
	}
	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip == nil {
			continue
		}
		if ip.To16() == nil || ip.To4() != nil {
			continue
		}
		if ip.IsLinkLocalUnicast() {
			return append(net.IP(nil), ip...), true
		}
	}
	return nil, false
}

func (ai *AutoInterface) startInterfaceSockets(iface net.Interface, linkLocal net.IP) error {
	mcastConn, err := net.ListenMulticastUDP("udp6", &iface, &net.UDPAddr{IP: ai.mcastDiscoveryAddr, Port: ai.discoveryPort})
	if err != nil {
		return err
	}

	unicastConn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: linkLocal, Port: ai.unicastDiscoveryPort, Zone: iface.Name})
	if err != nil {
		if closeErr := mcastConn.Close(); closeErr != nil {
			log.Printf("auto interface %v failed closing multicast socket: %v", iface.Name, closeErr)
		}
		return err
	}

	dataConn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: linkLocal, Port: ai.dataPort, Zone: iface.Name})
	if err != nil {
		if closeErr := mcastConn.Close(); closeErr != nil {
			log.Printf("auto interface %v failed closing multicast socket: %v", iface.Name, closeErr)
		}
		if closeErr := unicastConn.Close(); closeErr != nil {
			log.Printf("auto interface %v failed closing unicast socket: %v", iface.Name, closeErr)
		}
		return err
	}

	ai.mu.Lock()
	ai.replaceAdoptedInterfaceAddressLocked(iface.Name, linkLocal)
	ai.multicastEchoes[iface.Name] = time.Now()
	ai.sockets[iface.Name] = &autoSocketSet{multicast: mcastConn, unicast: unicastConn, data: dataConn}
	ai.mu.Unlock()

	go ai.discoveryLoop(iface.Name, mcastConn)
	go ai.discoveryLoop(iface.Name, unicastConn)
	go ai.dataLoop(dataConn)
	go ai.announceLoop(iface.Name)
	return nil
}

func (ai *AutoInterface) discoveryLoop(ifname string, conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for atomic.LoadInt32(&ai.running) == 1 {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if atomic.LoadInt32(&ai.running) == 1 {
				ai.panicOnInterfaceErrorf("auto interface %v discovery read failed: %v", ifname, err)
				continue
			}
			return
		}
		if atomic.LoadInt32(&ai.final) != 1 {
			continue
		}
		if n < sha256.Size {
			continue
		}
		if src == nil || src.IP == nil {
			continue
		}
		srcIP := src.IP.String()
		expected := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(srcIP)...))
		if string(buf[:sha256.Size]) != string(expected[:]) {
			continue
		}
		ai.addPeer(srcIP, ifname)
	}
}

func (ai *AutoInterface) announceLoop(ifname string) {
	ticker := time.NewTicker(ai.announceInterval)
	defer ticker.Stop()
	ai.peerAnnounce(ifname)
	for atomic.LoadInt32(&ai.running) == 1 {
		<-ticker.C
		ai.peerAnnounce(ifname)
	}
}

func (ai *AutoInterface) peerAnnounce(ifname string) {
	ai.mu.Lock()
	localIP, ok := ai.adoptedInterfaces[ifname]
	ai.mu.Unlock()
	if !ok {
		return
	}

	token := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(localIP.String())...))
	addr := &net.UDPAddr{IP: ai.mcastDiscoveryAddr, Port: ai.discoveryPort, Zone: ifname}

	conn, err := net.ListenUDP("udp6", nil)
	if err != nil {
		return
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Printf("auto interface %v discovery socket close failed: %v", ifname, closeErr)
		}
	}()
	if _, err := conn.WriteToUDP(token[:], addr); err != nil {
		return
	}
}

func (ai *AutoInterface) reverseAnnounce(ifname, peerAddr string) {
	ai.mu.Lock()
	localIP, ok := ai.adoptedInterfaces[ifname]
	ai.mu.Unlock()
	if !ok {
		return
	}

	token := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(localIP.String())...))
	addr := &net.UDPAddr{IP: net.ParseIP(peerAddr), Port: ai.unicastDiscoveryPort, Zone: ifname}
	if addr.IP == nil {
		return
	}

	conn, err := net.ListenUDP("udp6", nil)
	if err != nil {
		return
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Printf("auto interface %v reverse announce socket close failed: %v", ifname, closeErr)
		}
	}()
	if _, err := conn.WriteToUDP(token[:], addr); err != nil {
		return
	}
}

func (ai *AutoInterface) dataLoop(conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for atomic.LoadInt32(&ai.running) == 1 {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if atomic.LoadInt32(&ai.running) == 1 {
				ai.panicOnInterfaceErrorf("auto interface data read failed: %v", err)
				continue
			}
			return
		}
		if src == nil || src.IP == nil {
			continue
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		ai.processIncoming(payload, src.IP.String())
	}
}

func (ai *AutoInterface) addPeer(addr, ifname string) {
	ai.mu.Lock()
	defer ai.mu.Unlock()

	if _, own := ai.linkLocalSet[addr]; own {
		if _, ok := ai.multicastEchoes[ifname]; ok {
			ai.multicastEchoes[ifname] = time.Now()
			if _, ok := ai.initialEchoes[ifname]; !ok {
				ai.initialEchoes[ifname] = time.Now()
			}
		}
		return
	}

	now := time.Now()
	if state, ok := ai.peers[addr]; ok {
		state.lastHeard = now
		return
	}

	ai.peers[addr] = &autoPeerState{interfaceName: ifname, lastHeard: now, lastOutbound: now}
	peer := &AutoInterfacePeer{
		BaseInterface: NewBaseInterface(fmt.Sprintf("AutoPeer[%v/%v]", ifname, addr), ai.Mode(), ai.Bitrate()),
		owner:         ai,
		addr:          addr,
		interfaceName: ifname,
	}
	peer.copyPanicOnInterfaceErrorFrom(ai.BaseInterface)
	peer.online.Store(true)
	peer.SetIFACConfig(ai.IFACConfig())

	if old, exists := ai.spawnedInterfaces[addr]; exists {
		if err := old.Detach(); err != nil {
			log.Printf("auto interface peer detach failed for %v on %v: %v", addr, ifname, err)
		}
	}
	ai.spawnedInterfaces[addr] = peer

	if ai.onPeer != nil {
		go ai.onPeer(peer)
	}
}

func (ai *AutoInterface) refreshPeer(addr string) {
	ai.mu.Lock()
	if state, ok := ai.peers[addr]; ok {
		state.lastHeard = time.Now()
	}
	ai.mu.Unlock()
}

func (ai *AutoInterface) processIncoming(data []byte, addr string) {
	if atomic.LoadInt32(&ai.running) != 1 || atomic.LoadInt32(&ai.online) != 1 {
		return
	}

	h := sha256.Sum256(data)
	now := time.Now()

	ai.mu.Lock()
	if ts, ok := ai.recentFrames[h]; ok && now.Sub(ts) < ai.multiIfDequeTTL {
		ai.mu.Unlock()
		return
	}
	ai.recentFrames[h] = now
	for k, ts := range ai.recentFrames {
		if now.Sub(ts) >= ai.multiIfDequeTTL {
			delete(ai.recentFrames, k)
		}
	}
	peer := ai.spawnedInterfaces[addr]
	ai.mu.Unlock()

	if peer == nil {
		return
	}

	ai.refreshPeer(addr)
	atomic.AddUint64(&peer.rxBytes, uint64(len(data)))
	atomic.AddUint64(&ai.rxBytes, uint64(len(data)))

	if ai.inboundHandler != nil {
		ai.inboundHandler(data, peer)
	}
}

func (ai *AutoInterface) peerJobs() {
	ticker := time.NewTicker(ai.peerJobInterval)
	defer ticker.Stop()

	for atomic.LoadInt32(&ai.running) == 1 {
		<-ticker.C
		now := time.Now()

		var timedOut []string
		var reverse []struct {
			addr   string
			ifname string
		}

		ai.mu.Lock()
		for addr, state := range ai.peers {
			if now.Sub(state.lastHeard) > ai.peeringTimeout {
				timedOut = append(timedOut, addr)
				continue
			}
			if now.Sub(state.lastOutbound) > ai.reversePeeringInterval {
				reverse = append(reverse, struct {
					addr   string
					ifname string
				}{addr: addr, ifname: state.interfaceName})
				state.lastOutbound = now
			}
		}

		ai.updateInterfaceHealthLocked(now)
		ai.mu.Unlock()

		for _, addr := range timedOut {
			ai.removePeer(addr)
		}
		for _, r := range reverse {
			ai.reverseAnnounce(r.ifname, r.addr)
		}
	}
}

func (ai *AutoInterface) updateInterfaceHealthLocked(now time.Time) {
	anyOnline := false
	for ifname, last := range ai.multicastEchoes {
		_, seenInitialEcho := ai.initialEchoes[ifname]
		timedOut := seenInitialEcho && now.Sub(last) > ai.multicastEchoTimeout
		ai.timedOutIfaces[ifname] = timedOut
		if !timedOut {
			anyOnline = true
		}
	}

	if len(ai.adoptedInterfaces) == 0 {
		atomic.StoreInt32(&ai.online, 0)
		return
	}

	if anyOnline {
		atomic.StoreInt32(&ai.online, 1)
		return
	}

	atomic.StoreInt32(&ai.online, 0)
}

func (ai *AutoInterface) replaceAdoptedInterfaceAddressLocked(ifname string, linkLocal net.IP) {
	if existing, ok := ai.adoptedInterfaces[ifname]; ok && existing != nil {
		delete(ai.linkLocalSet, existing.String())
	}
	cloned := append(net.IP(nil), linkLocal...)
	ai.adoptedInterfaces[ifname] = cloned
	ai.linkLocalSet[cloned.String()] = struct{}{}
}

func (ai *AutoInterface) removePeer(addr string) {
	ai.mu.Lock()
	peer := ai.spawnedInterfaces[addr]
	delete(ai.spawnedInterfaces, addr)
	delete(ai.peers, addr)
	ai.mu.Unlock()

	if peer != nil {
		if err := peer.Detach(); err != nil {
			log.Printf("Failed to detach auto peer %v: %v", addr, err)
		}
	}
}

func (ai *AutoInterface) sendToPeer(peer *AutoInterfacePeer, data []byte) error {
	if atomic.LoadInt32(&ai.running) != 1 {
		return fmt.Errorf("interface %v is not running", ai.name)
	}

	ai.writeMu.Lock()
	defer ai.writeMu.Unlock()

	if ai.outboundConn == nil {
		conn, err := net.ListenUDP("udp6", nil)
		if err != nil {
			return err
		}
		ai.outboundConn = conn
	}

	addr := &net.UDPAddr{IP: net.ParseIP(peer.addr), Port: ai.dataPort, Zone: peer.interfaceName}
	if addr.IP == nil {
		return fmt.Errorf("invalid peer address %v", peer.addr)
	}

	n, err := ai.outboundConn.WriteToUDP(data, addr)
	if err != nil {
		return err
	}
	atomic.AddUint64(&peer.txBytes, uint64(n))
	atomic.AddUint64(&ai.txBytes, uint64(n))
	return nil
}

// Name returns the configured interface name.
func (ai *AutoInterface) Name() string { return ai.BaseInterface.Name() }

// Type identifies this interface as an AutoInterface.
func (ai *AutoInterface) Type() string { return "AutoInterface" }

// Status reports whether discovery is running and at least one viable adopted
// interface is currently considered online.
func (ai *AutoInterface) Status() bool {
	return atomic.LoadInt32(&ai.running) == 1 && atomic.LoadInt32(&ai.online) == 1
}

// IsOut reports whether the parent AutoInterface sends frames directly.
// Outbound traffic is carried by spawned peer interfaces instead.
func (ai *AutoInterface) IsOut() bool { return false }

// Send is intentionally a no-op for the parent AutoInterface; peer interfaces carry outbound frames.
func (ai *AutoInterface) Send(_ []byte) error { return nil }

// Detach stops discovery, closes all sockets, detaches spawned peers, and
// releases any outbound UDP socket owned by the interface.
func (ai *AutoInterface) Detach() error {
	var detachErr error

	atomic.StoreInt32(&ai.running, 0)
	atomic.StoreInt32(&ai.online, 0)
	atomic.StoreInt32(&ai.final, 0)
	ai.SetDetached(true)

	ai.mu.Lock()
	for _, set := range ai.sockets {
		if set.multicast != nil {
			if err := set.multicast.Close(); err != nil {
				detachErr = errors.Join(detachErr, err)
			}
		}
		if set.unicast != nil {
			if err := set.unicast.Close(); err != nil {
				detachErr = errors.Join(detachErr, err)
			}
		}
		if set.data != nil {
			if err := set.data.Close(); err != nil {
				detachErr = errors.Join(detachErr, err)
			}
		}
	}
	for _, peer := range ai.spawnedInterfaces {
		if err := peer.Detach(); err != nil {
			detachErr = errors.Join(detachErr, err)
		}
	}
	ai.spawnedInterfaces = map[string]*AutoInterfacePeer{}
	if ai.outboundConn != nil {
		if err := ai.outboundConn.Close(); err != nil {
			detachErr = errors.Join(detachErr, err)
		}
		ai.outboundConn = nil
	}
	ai.mu.Unlock()

	return detachErr
}

// PeerCount returns the current number of discovered peer subinterfaces.
func (ai *AutoInterface) PeerCount() int {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	return len(ai.spawnedInterfaces)
}

// AdoptedInterfaces returns the sorted set of local interface names currently
// participating in AutoInterface discovery.
func (ai *AutoInterface) AdoptedInterfaces() []string {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	out := make([]string, 0, len(ai.adoptedInterfaces))
	for name := range ai.adoptedInterfaces {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// AutoInterfacePeer signifies a distinct, dynamically discovered point-to-point connection originating from a parent AutoInterface.
// It provides dedicated egress targeting and metrics tracking for a specific neighbor while deferring complex state logic back to the orchestrating parent.
type AutoInterfacePeer struct {
	*BaseInterface
	owner         *AutoInterface
	addr          string
	interfaceName string
	online        atomic.Bool
}

// Type identifies this subinterface as an AutoInterfacePeer.
func (p *AutoInterfacePeer) Type() string { return "AutoInterfacePeer" }

// Status reports whether the peer is still online and its parent
// AutoInterface is still running.
func (p *AutoInterfacePeer) Status() bool {
	return p.online.Load() && atomic.LoadInt32(&p.owner.running) == 1 && atomic.LoadInt32(&p.owner.online) == 1
}

// IsOut reports whether the peer is directly marked as outbound-capable.
// Peer transmission is delegated through the parent AutoInterface.
func (p *AutoInterfacePeer) IsOut() bool { return false }

// Send transmits data to this specific discovered peer through the parent
// AutoInterface's UDP data channel.
func (p *AutoInterfacePeer) Send(data []byte) error {
	if !p.Status() {
		return fmt.Errorf("peer interface is offline")
	}
	return p.owner.sendToPeer(p, data)
}

// Detach marks the peer offline and prevents further traffic from being sent
// through it.
func (p *AutoInterfacePeer) Detach() error {
	p.online.Store(false)
	p.SetDetached(true)
	return nil
}
