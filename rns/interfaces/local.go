// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// localAcceptRetryDelay bounds retry pressure for transient accept errors.
const localAcceptRetryDelay = 250 * time.Millisecond

const (
	LocalBitrate = 1000 * 1000 * 1000

	// LocalClientReconnectWait is the backoff between reconnection attempts for
	// an initiator local client interface that lost its connection to the
	// shared instance (LocalInterface.py:63 RECONNECT_WAIT = 8).
	LocalClientReconnectWait = 8 * time.Second

	// ClientSleepPauseTimeout is the Android client-sleep pause window after
	// which outbound traffic to a (presumed asleep) local shared-instance
	// client is dropped (LocalInterface.py:65
	// CLIENT_SLEEP_PAUSE_TIMEOUT = 12). Each inbound frame refreshes the
	// window (LocalInterface.py:295).
	ClientSleepPauseTimeout = 12 * time.Second
)

func isAbstractUnixAddr(path string) bool {
	return strings.HasPrefix(path, "@")
}

// LocalClientInterface establishes a high-bandwidth, low-latency IPC link to a
// local Reticulum instance. It uses Unix domain sockets or loopback TCP to
// proxy routing requests to the broader network.
type LocalClientInterface struct {
	*BaseInterface

	conn net.Conn
	path string
	port int

	// isConnectedToSharedInstance distinguishes the initiator local client (a
	// client process that dials the shared instance) from a spawned local
	// client (a connection the shared instance accepted from a client). Only
	// the initiator reconnects on disconnect; spawned clients tear down and are
	// forgotten by the server (LocalInterface.py:102,151,306-314).
	isConnectedToSharedInstance bool

	// parent is the LocalServerInterface that spawned this client interface.
	// It is set only for spawned clients so they can remove themselves from the
	// server's spawned list and decrement its client count on teardown
	// (LocalInterface.py:473 parent_interface = self).
	parent *LocalServerInterface

	// Android client-sleep state (LocalInterface.py:91-106,154). On Android a
	// spawned local client pauses outbound traffic when no inbound frame has
	// refreshed the pause window within ClientSleepPauseTimeout. phyKeepalive
	// marks initiator clients that should emit keepalive frames.
	pauseOnClientSleep bool
	pauseTimeout       time.Time
	phyKeepalive       bool

	identityHash   []byte
	inboundHandler InboundHandler
	running        int32
	mu             sync.RWMutex
}

// NewLocalClientInterface dials and negotiates a persistent connection to the
// local Reticulum hub. It falls back between Unix sockets and TCP loopback as
// needed and starts asynchronous read loops on success.
func NewLocalClientInterface(name string, path string, port int, handler InboundHandler) (*LocalClientInterface, error) {
	bi := NewBaseInterface(name, ModeFull, LocalBitrate)
	lci := &LocalClientInterface{
		BaseInterface:               bi,
		path:                        path,
		port:                        port,
		inboundHandler:              handler,
		isConnectedToSharedInstance: true,
	}

	if err := lci.connect(); err != nil {
		go lci.reconnectLoop()
	} else {
		go lci.readLoop()
	}

	return lci, nil
}

func (lci *LocalClientInterface) connect() error {
	var conn net.Conn
	var err error

	if lci.path != "" && runtime.GOOS != "windows" {
		conn, err = net.Dial("unix", lci.path)
	} else {
		conn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%v", lci.port))
	}

	if err != nil {
		return err
	}

	lci.mu.Lock()
	lci.conn = conn
	lci.mu.Unlock()
	atomic.StoreInt32(&lci.running, 1)

	// On Android the initiator local client enables PHY keepalive
	// (LocalInterface.py:154).
	if runtime.GOOS == "android" {
		lci.mu.Lock()
		lci.phyKeepalive = true
		lci.mu.Unlock()
	}
	return nil
}

// reconnectLoop is the initiator-only reconnection backoff loop. A spawned
// (server-accepted) local client must never reconnect — the server does not
// dial back to clients — so the read loop routes spawned disconnects to
// teardown instead. This guard mirrors LocalInterface.py:160-192 reconnect(),
// which raises IOError on a non-initiator interface; here we log and return.
func (lci *LocalClientInterface) reconnectLoop() {
	if !lci.isConnectedToSharedInstance {
		fmt.Printf("local interface %v: attempt to reconnect on a non-initiator local interface; this should not happen\n", lci.name)
		return
	}
	for atomic.LoadInt32(&lci.running) == 0 && !lci.IsDetached() {
		time.Sleep(LocalClientReconnectWait)
		if err := lci.connect(); err == nil {
			go lci.readLoop()
			return
		}
	}
}

func (lci *LocalClientInterface) readLoop() {
	buf := make([]byte, 4096)
	frameBuffer := make([]byte, 0, TCPHWMTU)

	for atomic.LoadInt32(&lci.running) == 1 {
		lci.mu.RLock()
		conn := lci.conn
		lci.mu.RUnlock()
		if conn == nil {
			break
		}
		n, err := conn.Read(buf)
		if err != nil {
			if atomic.LoadInt32(&lci.running) == 1 && !lci.IsDetached() {
				lci.panicOnInterfaceErrorf("local interface %v read failed: %v", lci.name, err)
			}
			break
		}

		if n > 0 {
			// An inbound frame refreshes the Android client-sleep pause window
			// (LocalInterface.py:295).
			lci.refreshPauseTimeoutAt(time.Now())
			frameBuffer = append(frameBuffer, buf[:n]...)
			for {
				start := bytes.IndexByte(frameBuffer, HDLCFlag)
				if start == -1 {
					frameBuffer = frameBuffer[:0]
					break
				}
				end := bytes.IndexByte(frameBuffer[start+1:], HDLCFlag)
				if end == -1 {
					frameBuffer = frameBuffer[start:]
					break
				}
				end += start + 1

				frame := frameBuffer[start+1 : end]
				unescaped := HDLCUnescape(frame)
				if len(unescaped) > 0 {
					atomic.AddUint64(&lci.rxBytes, uint64(len(unescaped)))
					if lci.inboundHandler != nil {
						lci.inboundHandler(unescaped, lci)
					}
				}
				frameBuffer = frameBuffer[end:]
			}
		}
	}

	lci.mu.Lock()
	if lci.conn != nil {
		if err := lci.conn.Close(); err != nil {
			fmt.Printf("local interface %v close failed: %v\n", lci.name, err)
		}
	}
	lci.mu.Unlock()
	atomic.StoreInt32(&lci.running, 0)

	if !lci.IsDetached() {
		if lci.isConnectedToSharedInstance {
			// Initiator local client: the connection to the shared instance
			// was lost, so reconnect with backoff (LocalInterface.py:306-312).
			go lci.reconnectLoop()
		} else {
			// Spawned (server-accepted) local client: the remote client
			// disconnected. Tear it down and remove it from the server's
			// spawned list; never reconnect (LocalInterface.py:313-314).
			lci.teardown()
		}
	}
}

// teardown removes a spawned (server-accepted) local client from its parent
// server's spawned-client list and decrements the server's client count. It
// mirrors LocalInterface.py:345-361 teardown(nowarning=True) for spawned
// clients: no reconnect, no panic, no process exit — the read loop simply exits
// and the server forgets this client. The underlying connection has already
// been closed by the read loop before this is called.
func (lci *LocalClientInterface) teardown() {
	parent := lci.parent
	if parent == nil {
		return
	}
	parent.mu.Lock()
	for i, sc := range parent.spawnedInterfaces {
		if sc == lci {
			parent.spawnedInterfaces = append(parent.spawnedInterfaces[:i], parent.spawnedInterfaces[i+1:]...)
			break
		}
	}
	if parent.clients > 0 {
		parent.clients--
	}
	parent.mu.Unlock()
}

// Send HDLC-frames the payload and writes it to the connected local shared
// instance transport.
func (lci *LocalClientInterface) Send(data []byte) error {
	return lci.sendAt(data, time.Now())
}

// sendAt is the time-injectable core of Send. It applies the Android
// client-sleep pause gate (LocalInterface.py:221-222 process_outgoing): when
// pause_on_client_sleep is set and now is past the pause window, the outbound
// packet is dropped silently (nil error, no transmission).
func (lci *LocalClientInterface) sendAt(data []byte, now time.Time) error {
	if atomic.LoadInt32(&lci.running) != 1 {
		return fmt.Errorf("interface %v is not running", lci.name)
	}

	lci.mu.Lock()
	if lci.pauseOnClientSleep && now.After(lci.pauseTimeout) {
		lci.mu.Unlock()
		// TX paused for LocalInterface client, dropping outbound packet.
		return nil
	}
	conn := lci.conn
	lci.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection for interface %v", lci.name)
	}

	frame := append([]byte{HDLCFlag}, HDLCEscape(data)...)
	frame = append(frame, HDLCFlag)

	n, err := conn.Write(frame)
	if err != nil {
		return err
	}

	atomic.AddUint64(&lci.txBytes, uint64(n))
	return nil
}

// refreshPauseTimeoutAt extends the Android client-sleep pause window to
// now+ClientSleepPauseTimeout, mirroring the refresh performed at the end of
// LocalInterface.py:295 receive() on every inbound frame.
func (lci *LocalClientInterface) refreshPauseTimeoutAt(now time.Time) {
	lci.mu.Lock()
	defer lci.mu.Unlock()
	if lci.pauseOnClientSleep {
		lci.pauseTimeout = now.Add(ClientSleepPauseTimeout)
	}
}

// sendKeepalive emits a two-HDLC-flag keepalive frame on the local shared
// instance transport (LocalInterface.py:195-203 send_keepalive). The non-epoll
// path emits HDLC.FLAG + HDLC.FLAG.
func (lci *LocalClientInterface) sendKeepalive() error {
	if atomic.LoadInt32(&lci.running) != 1 {
		return fmt.Errorf("interface %v is not running", lci.name)
	}

	lci.mu.Lock()
	conn := lci.conn
	lci.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection for interface %v", lci.name)
	}

	frame := []byte{HDLCFlag, HDLCFlag}
	n, err := conn.Write(frame)
	if err != nil {
		return err
	}

	atomic.AddUint64(&lci.txBytes, uint64(n))
	return nil
}

// Status reports whether the local client connection is currently active.
func (lci *LocalClientInterface) Status() bool {
	return atomic.LoadInt32(&lci.running) == 1
}

// ShouldIngressLimit mirrors Python's LocalClientInterface.should_ingress_limit
// (LocalInterface.py:137-138), which unconditionally returns False. A local
// shared-instance socket is the high-bandwidth, low-latency link that carries
// every announce and path response between a co-located client and the shared
// instance. Subjecting it to the announce ingress-limit burst state machine
// holds path-response announces in a queue released one per ~5s after a 15s
// burst penalty (and drops them outright when the queue is full), which surfaced
// as ~20s path-resolution delays and intermittent "no transport path" drops for
// shared-instance clients. Local interfaces never ingress-limit announces.
// This override applies to both initiator clients and the spawned per-connection
// interfaces the server creates from accepted clients (both are
// LocalClientInterface), so it covers the shared-instance side as well.
func (lci *LocalClientInterface) ShouldIngressLimit() bool {
	return false
}

// Type identifies this interface as a local shared-instance transport.
func (lci *LocalClientInterface) Type() string {
	return "LocalInterface"
}

// HashString reproduces Python LocalClientInterface.__str__
// (RNS/Interfaces/LocalInterface.py:372-374), which Interface.get_hash hashes:
//
//	"LocalInterface["+socket_path.replace("\0", "")+"]"   (AF_UNIX)
//	"LocalInterface["+str(target_port)+"]"                (TCP fallback)
//
// AF_UNIX socket paths are NUL-stripped exactly as Python does so hashes
// agree between a Python-written destination_table and Go's
// findInterfaceByHash. Server-spawned clients inherit the server's socket
// path or the peer's port, matching LocalInterface.py:440-473.
func (lci *LocalClientInterface) HashString() string {
	if lci.path != "" {
		return "LocalInterface[" + strings.ReplaceAll(lci.path, "\x00", "") + "]"
	}
	return "LocalInterface[" + fmt.Sprintf("%d", lci.port) + "]"
}

// IsOut reports whether the local client can originate outbound traffic.
func (lci *LocalClientInterface) IsOut() bool {
	return true
}

// GetHash returns the identity hash currently associated with this local
// connection.
func (lci *LocalClientInterface) GetHash() []byte {
	lci.mu.RLock()
	defer lci.mu.RUnlock()
	return lci.identityHash
}

// SetHash associates an identity hash with this local connection.
func (lci *LocalClientInterface) SetHash(hash []byte) {
	lci.mu.Lock()
	defer lci.mu.Unlock()
	lci.identityHash = hash
}

// Detach closes the underlying local transport connection and marks the
// interface detached.
func (lci *LocalClientInterface) Detach() error {
	if lci.IsDetached() {
		return nil
	}
	lci.SetDetached(true)
	atomic.StoreInt32(&lci.running, 0)
	lci.mu.Lock()
	defer lci.mu.Unlock()
	if lci.conn != nil {
		err := lci.conn.Close()
		lci.conn = nil
		if err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}

// LocalServerInterface starts a high-performance IPC listener that services
// local Reticulum client processes. It manages concurrent client sessions over
// Unix sockets or loopback TCP.
type LocalServerInterface struct {
	*BaseInterface

	listener net.Listener
	path     string
	port     int

	spawnedInterfaces []*LocalClientInterface
	inboundHandler    InboundHandler

	// onClientConnected, when set, is invoked once a co-located client has been
	// fully accepted: the per-connection LocalClientInterface is registered in
	// spawnedInterfaces and its read loop has been launched. It is an optional
	// observability hook (no Python equivalent is exercised in production) that
	// lets callers — and tests — wait deterministically for the server side to
	// be ready to receive and forward traffic for a just-connected client,
	// rather than polling the client's own (already-true) connection status,
	// which becomes true before this server side has accepted the connection.
	onClientConnected func(client Interface)

	// clients is the count of currently-spawned local client interfaces
	// (LocalInterface.py:384 self.clients), incremented in incoming_connection
	// and decremented when a spawned client tears down.
	clients int

	running int32
	mu      sync.Mutex
}

// NewLocalServerInterface binds an IPC listener to accept incoming connections
// from co-located Reticulum instances. It manages socket files and port bindings
// and launches an asynchronous accept loop.
func NewLocalServerInterface(name, path string, port int, handler InboundHandler) (*LocalServerInterface, error) {
	bi := NewBaseInterface(name, ModeFull, LocalBitrate)

	var l net.Listener
	var err error
	if path != "" && runtime.GOOS != "windows" {
		if !isAbstractUnixAddr(path) {
			if _, err := os.Stat(path); err == nil {
				conn, dialErr := net.DialTimeout("unix", path, 150*time.Millisecond)
				if dialErr == nil {
					_ = conn.Close()
					return nil, fmt.Errorf("shared instance already running on %v", path)
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return nil, err
				}
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		l, err = net.Listen("unix", path)
	} else if held := PopPendingTCPListener(port); held != nil {
		// The test suite reserved this port with a listener already bound;
		// adopt it instead of rebinding (see pendingTCPListeners).
		l = held
	} else {
		l, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%v", port))
	}

	if err != nil {
		return nil, err
	}

	lsi := &LocalServerInterface{
		BaseInterface:  bi,
		listener:       l,
		path:           path,
		port:           port,
		inboundHandler: handler,
	}

	atomic.StoreInt32(&lsi.running, 1)
	go lsi.acceptLoop()

	return lsi, nil
}

func (lsi *LocalServerInterface) acceptLoop() {
	for atomic.LoadInt32(&lsi.running) == 1 {
		lsi.mu.Lock()
		listener := lsi.listener
		lsi.mu.Unlock()
		if listener == nil {
			break
		}
		conn, err := listener.Accept()
		if err != nil {
			// Intentional teardown (Detach sets running=0/detached before
			// closing the listener) exits silently.
			if lsi.IsDetached() || atomic.LoadInt32(&lsi.running) != 1 {
				break
			}
			// The server believes it is still up, yet the accept failed:
			// escalate through the error policy. A closed listener is
			// unrecoverable (nothing to accept from again); anything else
			// is transient and retried below.
			lsi.panicOnInterfaceErrorf("local interface %v accept failed: %v", lsi.name, err)
			if errors.Is(err, net.ErrClosed) {
				log.Printf("Go LocalServerInterface %v: listener closed while interface up", lsi.name)
				break
			}
			time.Sleep(localAcceptRetryDelay)
			continue
		}

		lsi.handleConnection(conn)
	}
}

func newLocalClientInterfaceFromConn(name string, conn net.Conn, handler InboundHandler) *LocalClientInterface {
	lci := &LocalClientInterface{
		BaseInterface:               NewBaseInterface(name, ModeFull, LocalBitrate),
		conn:                        conn,
		inboundHandler:              handler,
		isConnectedToSharedInstance: false, // LocalInterface.py:102
	}
	atomic.StoreInt32(&lci.running, 1)

	// On Android a spawned (server-accepted) local client pauses outbound
	// traffic when the inbound pause window expires
	// (LocalInterface.py:104-106).
	if runtime.GOOS == "android" {
		lci.mu.Lock()
		lci.pauseOnClientSleep = true
		lci.pauseTimeout = time.Now().Add(ClientSleepPauseTimeout)
		lci.mu.Unlock()
	}
	return lci
}

// SpawnedClientInterfaces returns a snapshot of the per-connection client
// interfaces currently spawned by this server. It is the Go equivalent of
// iterating Python's Transport.local_client_interfaces for a shared
// Reticulum instance: the transport uses it to forward announces to
// co-located clients (Python Transport.py:1790-1833) without injecting them
// into the network rebroadcast fan-out. The snapshot is taken under the
// server mutex so the caller may iterate it without holding the lock.
func (lsi *LocalServerInterface) SpawnedClientInterfaces() []Interface {
	lsi.mu.Lock()
	defer lsi.mu.Unlock()
	out := make([]Interface, len(lsi.spawnedInterfaces))
	for i, sc := range lsi.spawnedInterfaces {
		out[i] = sc
	}
	return out
}

func (lsi *LocalServerInterface) handleConnection(conn net.Conn) {
	name := fmt.Sprintf("Local Client %v", conn.RemoteAddr().String())
	lci := newLocalClientInterfaceFromConn(name, conn, lsi.inboundHandler)
	lci.parent = lsi
	lci.copyPanicOnInterfaceErrorFrom(lsi.BaseInterface)

	lsi.mu.Lock()
	lsi.spawnedInterfaces = append(lsi.spawnedInterfaces, lci)
	lsi.clients++
	lsi.mu.Unlock()

	go lci.readLoop()

	// Notify after the spawned interface is registered AND its read loop is
	// launched, so a waiter can rely on the server side being ready to read
	// from this client and to forward announces to it.
	lsi.mu.Lock()
	cb := lsi.onClientConnected
	lsi.mu.Unlock()
	if cb != nil {
		cb(lci)
	}
}

// SetOnClientConnected installs a callback invoked once for each co-located
// client this server accepts, after the per-connection interface is
// registered and its read loop has started. It is intended as a deterministic
// readiness signal for callers and tests; production leaves it unset.
func (lsi *LocalServerInterface) SetOnClientConnected(fn func(client Interface)) {
	lsi.mu.Lock()
	lsi.onClientConnected = fn
	lsi.mu.Unlock()
}

// Send is a no-op for the server wrapper because spawned client connections
// perform the actual transmission.
func (lsi *LocalServerInterface) Send(data []byte) error {
	return nil
}

// Status reports whether the local listener is currently accepting
// connections.
func (lsi *LocalServerInterface) Status() bool {
	return atomic.LoadInt32(&lsi.running) == 1
}

// Type identifies this interface as a local shared-instance transport.
func (lsi *LocalServerInterface) Type() string {
	return "LocalInterface"
}

// HashString reproduces Python LocalInterfaceServer.__str__
// (RNS/Interfaces/LocalInterface.py:495-497), which Interface.get_hash
// hashes:
//
//	"Shared Instance["+socket_path.replace("\0", "")+"]"   (AF_UNIX)
//	"Shared Instance["+str(bind_port)+"]"                  (TCP fallback)
func (lsi *LocalServerInterface) HashString() string {
	if lsi.path != "" {
		return "Shared Instance[" + strings.ReplaceAll(lsi.path, "\x00", "") + "]"
	}
	return "Shared Instance[" + fmt.Sprintf("%d", lsi.port) + "]"
}

// IsOut reports whether the server can originate traffic through spawned
// client connections.
func (lsi *LocalServerInterface) IsOut() bool {
	return true
}

// Detach stops the listener, detaches spawned clients, and removes any
// filesystem socket path owned by the server.
func (lsi *LocalServerInterface) Detach() error {
	if lsi.IsDetached() {
		return nil
	}
	lsi.SetDetached(true)
	var detachErr error

	atomic.StoreInt32(&lsi.running, 0)
	lsi.mu.Lock()
	defer lsi.mu.Unlock()

	for _, ci := range lsi.spawnedInterfaces {
		if err := ci.Detach(); err != nil {
			detachErr = errors.Join(detachErr, err)
		}
	}
	lsi.spawnedInterfaces = nil

	if lsi.listener != nil {
		if err := lsi.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			detachErr = errors.Join(detachErr, err)
		}
		lsi.listener = nil
	}

	if lsi.path != "" && runtime.GOOS != "windows" && !isAbstractUnixAddr(lsi.path) {
		if err := os.Remove(lsi.path); err != nil && !os.IsNotExist(err) {
			detachErr = errors.Join(detachErr, err)
		}
	}
	return detachErr
}
