// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	TCPBitrateGuess = 10 * 1000 * 1000
	TCPHWMTU        = 262144

	// TCPDefaultIFACSize is the DEFAULT_IFAC_SIZE for TCP client and server
	// interfaces (RNS/Interfaces/TCPInterface.py:77,467).
	TCPDefaultIFACSize = 16

	// tcpWriteTimeout bounds a single outbound frame write. Without it, a
	// half-open TCP peer (one that died without sending a FIN) makes
	// conn.Write block until the OS TCP retransmit timeout — minutes to
	// forever on some platforms — which wedges any goroutine that calls
	// Send synchronously (the transport maintenance loop and each
	// readLoop). A RNS frame is at most a few KB; a write that cannot
	// complete in this window means the peer is gone, so we fail the
	// connection and reconnect instead of blocking forever. Individual
	// interfaces may override this via TCPClientInterface.writeTimeout
	// (e.g. for slow or high-latency links).
	tcpWriteTimeout = 10 * time.Second

	// tcpKeepAliveIdle/Interval/Count configure OS-level keep-alive probes on
	// TCP connections so a half-open peer (one that died without sending a
	// FIN) is detected within a bounded window and reported to readLoop's
	// conn.Read, instead of lingering as a live-but-dead interface that
	// reports Status()==true and black-holes directed packets until an
	// outbound write happens to time out. Idle is the quiet time before the
	// first probe; Interval is the gap between probes; Count is the number of
	// unanswered probes before the OS drops the connection. The detection
	// window is approximately Idle + Interval*Count. This is an idle backstop
	// — for connections carrying traffic, the per-write deadline
	// (tcpWriteTimeout) detects a half-open peer faster on the next outbound
	// send. Values are deliberately lenient to avoid killing healthy
	// connections through transient latency or packet loss.
	tcpKeepAliveIdle     = 60 * time.Second
	tcpKeepAliveInterval = 15 * time.Second
	tcpKeepAliveCount    = 4
)

// applyTCPConnOpts sets shared TCP socket options on a freshly established or
// accepted connection: TCP_NODELAY so small RNS frames are sent immediately,
// and keep-alive probes so a half-open peer is reaped by the OS in a bounded
// window (see tcpKeepAlive*).
func applyTCPConnOpts(conn net.Conn) {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	if err := tcpConn.SetNoDelay(true); err != nil {
		log.Printf("[TCP] Failed to set TCP_NODELAY: %v", err)
	}
	if err := tcpConn.SetKeepAliveConfig(net.KeepAliveConfig{
		Enable:   true,
		Idle:     tcpKeepAliveIdle,
		Interval: tcpKeepAliveInterval,
		Count:    tcpKeepAliveCount,
	}); err != nil {
		log.Printf("[TCP] Failed to set keep-alive: %v", err)
	}
}

// TCPClientInterface drives a persistent outbound TCP session used to tunnel
// Reticulum frames. It manages reconnection logic and supports both raw HDLC
// and KISS framing over the TCP stream.
type TCPClientInterface struct {
	*BaseInterface

	conn           net.Conn
	targetHost     string
	targetPort     int
	reconnectDelay time.Duration

	// spawned is true for interfaces created by a TCPServerInterface
	// from an accepted inbound connection. Spawned interfaces must not
	// attempt to reconnect when the connection drops, since they have
	// no target host/port to reconnect to.
	spawned bool

	// spawnedAt is the time the server spawned this client interface, set in
	// handleConnection. It backs the fast-flap connected-time check on
	// teardown (BackboneInterface.py:487,827-829, v1.3.9).
	spawnedAt time.Time
	// remoteIP is the remote address of a spawned client, extracted in
	// handleConnection so the fast-flap teardown hook can attribute the flap
	// (BackboneInterface.py:834, v1.3.9).
	remoteIP string
	// onSpawnedDown, when copied from the parent server in handleConnection,
	// is fired by failConn on the up->down transition to record a fast flap
	// for this remote IP (BackboneInterface.py:833-842, v1.3.9).
	onSpawnedDown func(remoteIP string, spawnedAt time.Time)

	kissFraming    bool
	inboundHandler InboundHandler

	// hwmtu is this interface's hardware MTU ceiling for inbound HDLC frame
	// validation (RNS/Interfaces/{TCP,Backbone}Interface.py HW_MTU). TCP
	// interfaces use TCPHWMTU (262144); Backbone interfaces use
	// BackboneHWMTU (1048576). It is set in the struct literal before
	// readLoop starts, so the read goroutine observes it without
	// synchronization.
	hwmtu int

	running int32
	mu      sync.Mutex

	// writeMu serializes outbound writes so each SetWriteDeadline applies
	// to exactly one Write. mu guards conn/lifecycle; writeMu guards the
	// deadline+Write pair. They are never held simultaneously in a way
	// that could deadlock: Send takes writeMu only after releasing mu,
	// and no other path holds writeMu while waiting on mu.
	writeMu sync.Mutex

	// writeTimeout bounds each outbound frame write for this interface; it
	// defaults to tcpWriteTimeout and may be overridden per interface (e.g.
	// for slow or high-latency links). A zero value falls back to
	// tcpWriteTimeout in Send.
	writeTimeout time.Duration

	// onDown, if set, is invoked once when this interface transitions from
	// up to down (its connection fails), so the transport can eagerly
	// invalidate paths routed through it. This replaces the old flood-prone
	// lazy invalidation where every rebroadcast/forward attempt to a down
	// interface ran a full pathTable scan. Set by the transport when the
	// interface is registered.
	onDownMu sync.Mutex
	onDown   func()

	// onConnect, if set, is invoked each time this outbound client interface
	// (re)establishes its connection — after a successful connect() in
	// reconnectLoop. The transport uses it to re-announce the local
	// destinations over the interface. This matters because a TCP client is
	// registered with the transport (and RegisterInterface announces its
	// destinations) even when the initial connect is refused; that announce
	// is sent to a dead socket and lost. Without re-announcing on reconnect,
	// the peer never learns this node's destinations until the periodic
	// announce interval (minutes) fires. Set by the transport when the
	// interface is registered; nil for spawned (server-accepted) interfaces,
	// which are announced by the server's connectHandler at accept time.
	onConnectMu sync.Mutex
	onConnect   func()
}

// NewTCPClientInterface initiates a resilient TCP connection to a remote peer.
// It establishes the link, configures framing mode, and starts read/write
// goroutines.
func NewTCPClientInterface(name, host string, port int, kiss bool, handler InboundHandler) (*TCPClientInterface, error) {
	return newTCPClientInterface(name, host, port, kiss, handler, TCPHWMTU)
}

// newTCPClientInterface is the shared constructor used by both TCP
// (TCPHWMTU) and Backbone (BackboneHWMTU) client interfaces so the inbound
// HDLC frame-length gate uses the correct hardware MTU for the interface
// type. hwmtu is recorded before readLoop starts, so the read goroutine
// observes it without synchronization.
func newTCPClientInterface(name, host string, port int, kiss bool, handler InboundHandler, hwmtu int) (*TCPClientInterface, error) {
	log.Printf("NewTCPClientInterface %v target=%v:%v", name, host, port)
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.setDefaultIFACSize(TCPDefaultIFACSize)
	tci := &TCPClientInterface{
		BaseInterface:  bi,
		targetHost:     host,
		targetPort:     port,
		kissFraming:    kiss,
		inboundHandler: handler,
		reconnectDelay: 5 * time.Second,
		writeTimeout:   tcpWriteTimeout,
		hwmtu:          hwmtu,
	}

	if err := tci.connect(); err != nil {
		// In Python it starts a reconnection thread if initial connect fails
		go tci.reconnectLoop()
	} else {
		atomic.StoreInt32(&tci.running, 1)
		go tci.readLoop()
	}

	return tci, nil
}

// TargetHost returns the remote host configured for this client interface.
func (tci *TCPClientInterface) TargetHost() string { return tci.targetHost }

// TargetPort returns the remote TCP port configured for this client interface.
func (tci *TCPClientInterface) TargetPort() int { return tci.targetPort }

// KISSFraming reports whether this client interface uses KISS framing instead of
// raw HDLC framing.
func (tci *TCPClientInterface) KISSFraming() bool { return tci.kissFraming }

func (tci *TCPClientInterface) connect() error {
	addr := fmt.Sprintf("%v:%v", tci.targetHost, tci.targetPort)
	log.Printf("Go TCPClientInterface %v connecting to %v", tci.name, addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Printf("Go TCPClientInterface %v connect failed: %v", tci.name, err)
		return err
	}
	log.Printf("Go TCPClientInterface %v connected", tci.name)
	applyTCPConnOpts(conn)
	tci.mu.Lock()
	tci.conn = conn
	tci.mu.Unlock()
	atomic.StoreInt32(&tci.running, 1)
	return nil
}

func (tci *TCPClientInterface) reconnectLoop() {
	for atomic.LoadInt32(&tci.running) == 0 && !tci.IsDetached() {
		time.Sleep(tci.reconnectDelay)
		if err := tci.connect(); err == nil {
			go tci.readLoop()
			// The interface just came back up. Re-announce the local
			// destinations so the peer learns about this node now,
			// instead of waiting for the periodic announce interval.
			// RegisterInterface's own announce ran against a dead
			// socket when this interface registered while disconnected,
			// so without this the first successful connect after a
			// refused/lost connection would leave this node unannounced
			// to the peer for minutes. The hook is nil (a no-op) for
			// spawned interfaces and before registration completes.
			tci.onConnectMu.Lock()
			hook := tci.onConnect
			tci.onConnectMu.Unlock()
			if hook != nil {
				hook()
			}
			return
		}
	}
}

func (tci *TCPClientInterface) readLoop() {
	log.Printf("Go TCPClientInterface %v readLoop starting", tci.name)

	// Capture the connection this readLoop owns. A concurrent reconnect may
	// later replace tci.conn with a fresh net.Conn and start its own
	// readLoop; this one must only read and tear down the connection it was
	// started with, never the newer one. failConn enforces that by
	// comparing the captured conn against the current tci.conn.
	tci.mu.Lock()
	conn := tci.conn
	tci.mu.Unlock()

	buf := make([]byte, 4096)
	frameBuffer := make([]byte, 0, TCPHWMTU)

	for atomic.LoadInt32(&tci.running) == 1 {
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("[TCP] %v: readLoop Read error: %v", tci.name, err)
			if atomic.LoadInt32(&tci.running) == 1 && !tci.IsDetached() {
				tci.panicOnInterfaceErrorf("tcp interface %v read failed: %v", tci.name, err)
			}
			break
		}

		if n > 0 {
			// log.Printf("[TCP] %v: read %v bytes", tci.name, n)
			if tci.kissFraming {
				frameBuffer = append(frameBuffer, buf[:n]...)
				for {
					start := bytes.IndexByte(frameBuffer, KISSFend)
					if start == -1 {
						frameBuffer = frameBuffer[:0]
						break
					}
					end := bytes.IndexByte(frameBuffer[start+1:], KISSFend)
					if end == -1 {
						frameBuffer = frameBuffer[start:]
						break
					}
					end += start + 1

					frame := frameBuffer[start+1 : end]
					if len(frame) > 0 {
						command := frame[0] & 0x0F
						if command == KISSCmdData {
							unescaped := KISSUnescape(frame[1:])
							if len(unescaped) > 0 {
								atomic.AddUint64(&tci.rxBytes, uint64(len(unescaped)))
								if tci.inboundHandler != nil {
									tci.inboundHandler(unescaped, tci)
								}
							}
						}
					}
					frameBuffer = frameBuffer[end:]
				}
			} else {
				// HDLC framing
				frameBuffer = append(frameBuffer, buf[:n]...)
				// Reassemble complete frames (hdlcReassemble) including the
				// v1.4.0 frame_buffer overflow guard, then validate and
				// dispatch each one (CheckFrameLen, RNS/Interfaces/{TCP,Backbone}Interface.py).
				var reassembled [][]byte
				frameBuffer, reassembled = hdlcReassemble(frameBuffer, tci.hwmtu)
				ifacSize := tci.IFACConfig().Size
				for _, unescaped := range reassembled {
					// Frame-length validation (check_frame_len, v1.4.0): drop
					// frames at or below HEADER_MINSIZE or above
					// HW_MTU+ifac_size before inbound dispatch. Python's
					// process_incoming (and thus rxb accounting) only runs for
					// frames that pass this gate, so rxBytes is incremented
					// only on the accept branch.
					if !CheckFrameLen(len(unescaped), tci.hwmtu, ifacSize) {
						InvalidFrame(tci.name, len(unescaped), tci.hwmtu, ifacSize)
						continue
					}
					atomic.AddUint64(&tci.rxBytes, uint64(len(unescaped)))
					if tci.inboundHandler != nil {
						tci.inboundHandler(unescaped, tci)
					}
				}
			}
		}
	}

	// Tear down the connection this readLoop owns. failConn is race-free:
	// if a Send already failed and a reconnect installed a newer
	// connection, this is a no-op for the down transition (it only closes
	// the stale captured conn). Only reconnect for outbound client
	// interfaces that have a target host/port; spawned interfaces (created
	// by TCPServerInterface from an accepted connection) have no target to
	// reconnect to.
	tci.failConn(conn)
}

// Send frames and writes data to the remote TCP peer using the interface's
// configured HDLC or KISS transport framing.
func (tci *TCPClientInterface) Send(data []byte) error {
	if atomic.LoadInt32(&tci.running) != 1 {
		return fmt.Errorf("interface %v is not running", tci.name)
	}

	var frame []byte
	if tci.kissFraming {
		frame = append([]byte{KISSFend, KISSCmdData}, KISSEscape(data)...)
		frame = append(frame, KISSFend)
	} else {
		frame = append([]byte{HDLCFlag}, HDLCEscape(data)...)
		frame = append(frame, HDLCFlag)
	}

	tci.mu.Lock()
	conn := tci.conn
	tci.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection for interface %v", tci.name)
	}

	// Serialize the deadline+Write pair so each SetWriteDeadline governs
	// exactly one Write, and a blocked writer cannot be preempted by
	// another goroutine resetting the deadline mid-write. The deadline is
	// never cleared (to zero): every write is bounded, so a half-open
	// peer fails fast instead of blocking the transport maintenance loop
	// and readLoop forwarding paths for the OS retransmit timeout.
	writeTimeout := tci.writeTimeout
	if writeTimeout <= 0 {
		writeTimeout = tcpWriteTimeout
	}
	tci.writeMu.Lock()
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		tci.writeMu.Unlock()
		tci.failConn(conn)
		return err
	}
	n, err := conn.Write(frame)
	tci.writeMu.Unlock()

	if err != nil {
		log.Printf("[TCP] %v: Send write error: %v", tci.name, err)
		tci.failConn(conn)
		return err
	}

	atomic.AddUint64(&tci.txBytes, uint64(n))
	return nil
}

// failConn tears down the given connection and, if it is still the current
// connection, marks the interface down and (for outbound client interfaces)
// schedules a reconnect. The captured conn is passed in so a stale readLoop
// or Send goroutine operating on an old connection cannot close or take down a
// newer connection installed by a concurrent reconnect: only the connection
// that is still current triggers the down transition.
func (tci *TCPClientInterface) failConn(conn net.Conn) {
	tci.mu.Lock()
	current := tci.conn
	if current == conn {
		tci.conn = nil
	}
	tci.mu.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil {
			log.Printf("[TCP] %v: failConn close failed: %v", tci.name, err)
		}
	}

	// A newer connection has already been installed by a reconnect; this
	// failure is stale and must not disturb the live connection.
	if current != conn {
		return
	}

	if atomic.CompareAndSwapInt32(&tci.running, 1, 0) {
		// Notify the transport once on the up->down transition so it can
		// eagerly invalidate paths routed through this interface. This
		// replaces the old behavior where path invalidation happened
		// lazily (and noisily) on every failed rebroadcast/forward Send.
		tci.onDownMu.Lock()
		hook := tci.onDown
		tci.onDownMu.Unlock()
		if hook != nil {
			hook()
		}
		// Fast-flap accounting (BackboneInterface only): a spawned client
		// that just tore down records a flap for its remote IP when the
		// connection was shorter than the parent's fast-flap threshold
		// (BackboneInterface.py:827-843, v1.3.9). Plain TCP servers leave
		// onSpawnedDown nil.
		if tci.spawned && tci.onSpawnedDown != nil {
			tci.onSpawnedDown(tci.remoteIP, tci.spawnedAt)
		}
		if !tci.IsDetached() && !tci.spawned {
			go tci.reconnectLoop()
		}
	}
}

// SetOnDown registers a callback invoked once when this interface's
// connection fails and it transitions from up to down. The transport uses it
// to eagerly invalidate paths routed through the interface.
func (tci *TCPClientInterface) SetOnDown(f func()) {
	tci.onDownMu.Lock()
	tci.onDown = f
	tci.onDownMu.Unlock()
}

// SetOnConnect registers a callback invoked each time this outbound client
// interface (re)establishes its connection. The transport uses it to
// re-announce the local destinations so a client that registered while
// disconnected does not stay unannounced to the peer after it reconnects.
func (tci *TCPClientInterface) SetOnConnect(f func()) {
	tci.onConnectMu.Lock()
	tci.onConnect = f
	tci.onConnectMu.Unlock()
}

// Status reports whether the TCP client is currently connected and running.
func (tci *TCPClientInterface) Status() bool {
	return atomic.LoadInt32(&tci.running) == 1
}

// Type identifies this interface as a TCP transport.
func (tci *TCPClientInterface) Type() string {
	return "TCPInterface"
}

// IsOut reports whether this interface can originate outbound traffic.
func (tci *TCPClientInterface) IsOut() bool {
	return true
}

// Detach closes the client connection and suppresses any future reconnect loop.
func (tci *TCPClientInterface) Detach() error {
	tci.SetDetached(true)
	atomic.StoreInt32(&tci.running, 0)
	tci.mu.Lock()
	defer tci.mu.Unlock()
	if tci.conn != nil {
		return tci.conn.Close()
	}
	return nil
}

// TCPServerInterface operates a concurrent TCP listener that accepts inbound
// Reticulum peer connections. It spawns client interface instances as new peers
// connect.
type TCPServerInterface struct {
	*BaseInterface

	listener net.Listener
	bindIP   string
	bindPort int

	spawnedInterfaces []*TCPClientInterface
	inboundHandler    InboundHandler
	connectHandler    ConnectHandler

	// incomingGate, when non-nil, is consulted by handleConnection before
	// spawning a client; returning false rejects the connection (close and
	// do not spawn). BackboneInterface sets it to its fast-flap block check
	// (BackboneInterface.py:397,420-435, v1.3.9).
	incomingGate func(remoteIP string) bool
	// onSpawnedDown, when non-nil, is fired by a spawned client's failConn on
	// the up->down transition, carrying the remote IP and the spawn time so the
	// parent can record a fast flap. BackboneInterface sets it to recordFlap
	// (BackboneInterface.py:827-843, v1.3.9).
	onSpawnedDown func(remoteIP string, spawnedAt time.Time)

	// hwmtu is the hardware MTU ceiling passed to each spawned client
	// interface for inbound HDLC frame-length validation. TCP servers use
	// TCPHWMTU; Backbone servers use BackboneHWMTU. It is read in
	// handleConnection when constructing spawned clients.
	hwmtu int

	running int32
	mu      sync.Mutex
}

// NewTCPServerInterface binds to the given IP and port and starts a listening
// socket for incoming TCP peers. It then enters a non-blocking accept loop and
// delegates connection handling to spawned client interfaces.
func NewTCPServerInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler) (*TCPServerInterface, error) {
	return newTCPServerInterface(name, bindIP, bindPort, handler, onConnect, TCPHWMTU)
}

// newTCPServerInterface is the shared constructor used by both TCP
// (TCPHWMTU) and Backbone (BackboneHWMTU) server interfaces so spawned
// clients inherit the correct hardware MTU for inbound HDLC frame-length
// validation.
func newTCPServerInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler, hwmtu int) (*TCPServerInterface, error) {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.setDefaultIFACSize(TCPDefaultIFACSize)

	addr := fmt.Sprintf("%v:%v", bindIP, bindPort)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	tsi := &TCPServerInterface{
		BaseInterface:  bi,
		listener:       l,
		bindIP:         bindIP,
		bindPort:       bindPort,
		inboundHandler: handler,
		connectHandler: onConnect,
		hwmtu:          hwmtu,
	}

	atomic.StoreInt32(&tsi.running, 1)
	go tsi.acceptLoop()

	return tsi, nil
}

// BindIP returns the configured listen address for this TCP server interface.
func (tsi *TCPServerInterface) BindIP() string { return tsi.bindIP }

// BindPort returns the configured listen TCP port for this server interface.
func (tsi *TCPServerInterface) BindPort() int { return tsi.bindPort }

func (tsi *TCPServerInterface) acceptLoop() {
	for atomic.LoadInt32(&tsi.running) == 1 {
		conn, err := tsi.listener.Accept()
		if err != nil {
			if atomic.LoadInt32(&tsi.running) == 1 && !tsi.IsDetached() {
				tsi.panicOnInterfaceErrorf("tcp interface %v accept failed: %v", tsi.name, err)
			}
			break
		}

		tsi.handleConnection(conn)
	}
}

func (tsi *TCPServerInterface) handleConnection(conn net.Conn) {
	name := fmt.Sprintf("Client %v on %v", conn.RemoteAddr().String(), tsi.name)
	// log.Printf("[TCP] Server %v: accepted connection from %v, creating spawned interface", tsi.name, conn.RemoteAddr())
	// Fast-flap gate (BackboneInterface only): reject the connection before
	// spawning if the remote IP is currently blocked (BackboneInterface.py:397,
	// 420-435, v1.3.9). Plain TCP servers leave incomingGate nil. The hooks are
	// guarded by tsi.mu so the accept loop reads them safely even if the parent
	// (e.g. BackboneInterface) installs them after newTCPServerInterface
	// started this loop.
	remoteIP, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
	tsi.mu.Lock()
	gate := tsi.incomingGate
	onDown := tsi.onSpawnedDown
	tsi.mu.Unlock()
	if gate != nil && !gate(remoteIP) {
		if err := conn.Close(); err != nil {
			log.Printf("[TCP] %v: rejected-conn close failed: %v", tsi.name, err)
		}
		return
	}
	applyTCPConnOpts(conn)
	// Create a TCPClientInterface from the connected socket
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.setDefaultIFACSize(TCPDefaultIFACSize)
	bi.copyPanicOnInterfaceErrorFrom(tsi.BaseInterface)
	// Inherit the parent server's gravity for weighted path selection
	// (RNS/Interfaces/TCPInterface.py:639, v1.4.1:
	// `spawned_interface.gravity = self.gravity`). The same spawn path backs
	// I2P and Backbone servers (they reuse newTCPServerInterface).
	bi.SetGravity(tsi.Gravity())
	// Inherit the parent server's ingress/egress-control configuration
	// (RNS/Interfaces/TCPInterface.py:595-608, v1.1.5). The same spawn
	// path backs I2P and Backbone servers (they reuse
	// newTCPServerInterface).
	bi.copyIngressEgressFrom(tsi.BaseInterface)
	// Record the parent server so frequency/byte-counter events on this
	// spawned peer propagate up to the aggregating parent
	// (TCPInterface.py:611: spawned_interface.parent_interface = self).
	bi.parentInterface = tsi.BaseInterface
	tci := &TCPClientInterface{
		BaseInterface:  bi,
		conn:           conn,
		inboundHandler: tsi.inboundHandler,
		writeTimeout:   tcpWriteTimeout,
		hwmtu:          tsi.hwmtu,
		spawned:        true,
		spawnedAt:      time.Now(),
		remoteIP:       remoteIP,
		onSpawnedDown:  onDown,
	}
	atomic.StoreInt32(&tci.running, 1)

	tsi.mu.Lock()
	tsi.spawnedInterfaces = append(tsi.spawnedInterfaces, tci)
	tsi.mu.Unlock()

	// Start readLoop FIRST so interface can receive data
	go tci.readLoop()
	// log.Printf("[TCP] Server %v: started readLoop for %v", tsi.name, tci.name)

	// Then register with transport (which will trigger re-announce)
	// log.Printf("[TCP] Server %v: spawned interface %v, calling connectHandler", tsi.name, tci.name)
	if tsi.connectHandler != nil {
		tsi.connectHandler(tci)
		// log.Printf("[TCP] Server %v: connectHandler completed for %v", tsi.name, tci.name)
	}
}

// Send forwards the payload to each active spawned client connection.
func (tsi *TCPServerInterface) Send(data []byte) error {
	// Snapshot the live spawned clients under the lock, then send to each
	// WITHOUT holding tsi.mu. Holding the lock across the sends serialized
	// the clients (one stalled peer blocked every other) and also blocked
	// acceptLoop's handleConnection, which needs the same lock to register
	// a new client. data is read-only here: each TCPClientInterface.Send
	// builds its own framed copy, so it is safe to share across goroutines.
	tsi.mu.Lock()
	clients := make([]*TCPClientInterface, 0, len(tsi.spawnedInterfaces))
	for _, ci := range tsi.spawnedInterfaces {
		if ci != nil && ci.Status() {
			clients = append(clients, ci)
		}
	}
	tsi.mu.Unlock()

	for _, ci := range clients {
		go func(ci *TCPClientInterface) {
			if err := ci.Send(data); err != nil {
				log.Printf("Failed to send to spawned client %v: %v", ci.name, err)
			}
		}(ci)
	}
	return nil
}

// Status reports whether the TCP server listener is still running.
func (tsi *TCPServerInterface) Status() bool {
	return atomic.LoadInt32(&tsi.running) == 1
}

// Type identifies this interface as a TCP transport.
func (tsi *TCPServerInterface) Type() string {
	return "TCPInterface"
}

// IsOut reports whether the server can originate traffic through its spawned
// client interfaces.
func (tsi *TCPServerInterface) IsOut() bool {
	return true
}

// Detach stops accepting connections, detaches spawned clients, and closes the
// listening socket.
func (tsi *TCPServerInterface) Detach() error {
	atomic.StoreInt32(&tsi.running, 0)
	tsi.mu.Lock()
	defer tsi.mu.Unlock()

	for _, ci := range tsi.spawnedInterfaces {
		if err := ci.Detach(); err != nil {
			fmt.Printf("tcp server interface %v detach failed for %v: %v\n", tsi.name, ci.name, err)
		}
	}

	if tsi.listener != nil {
		return tsi.listener.Close()
	}
	return nil
}
