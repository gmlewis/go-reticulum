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
)

// TCPClientInterface drives a persistent outbound TCP session used to tunnel
// Reticulum frames. It manages reconnection logic and supports both raw HDLC
// and KISS framing over the TCP stream.
type TCPClientInterface struct {
	*BaseInterface

	conn       net.Conn
	targetHost string
	targetPort int

	kissFraming    bool
	inboundHandler InboundHandler

	running int32
	mu      sync.Mutex
}

// NewTCPClientInterface initiates a resilient TCP connection to a remote peer.
// It establishes the link, configures framing mode, and starts read/write
// goroutines.
func NewTCPClientInterface(name, host string, port int, kiss bool, handler InboundHandler) (*TCPClientInterface, error) {
	log.Printf("NewTCPClientInterface %v target=%v:%v", name, host, port)
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	tci := &TCPClientInterface{
		BaseInterface:  bi,
		targetHost:     host,
		targetPort:     port,
		kissFraming:    kiss,
		inboundHandler: handler,
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
	// Disable Nagle's algorithm to ensure small packets are sent immediately
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.SetNoDelay(true); err != nil {
			log.Printf("[TCP] Failed to set TCP_NODELAY: %v", err)
		}
	}
	tci.mu.Lock()
	tci.conn = conn
	tci.mu.Unlock()
	atomic.StoreInt32(&tci.running, 1)
	return nil
}

func (tci *TCPClientInterface) reconnectLoop() {
	for atomic.LoadInt32(&tci.running) == 0 && !tci.IsDetached() {
		time.Sleep(5 * time.Second)
		if err := tci.connect(); err == nil {
			go tci.readLoop()
			return
		}
	}
}

func (tci *TCPClientInterface) readLoop() {
	log.Printf("Go TCPClientInterface %v readLoop starting", tci.name)
	buf := make([]byte, 4096)
	frameBuffer := make([]byte, 0, TCPHWMTU)

	for atomic.LoadInt32(&tci.running) == 1 {
		n, err := tci.conn.Read(buf)
		if err != nil {
			log.Printf("[TCP] %s: readLoop Read error: %v", tci.name, err)
			if atomic.LoadInt32(&tci.running) == 1 && !tci.IsDetached() {
				panicOnInterfaceErrorf("tcp interface %v read failed: %v", tci.name, err)
			}
			break
		}

		if n > 0 {
			log.Printf("[TCP] %s: read %d bytes", tci.name, n)
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
					log.Printf("[TCP] %s: HDLC frame len=%d", tci.name, len(frame))
					unescaped := HDLCUnescape(frame)
					log.Printf("[TCP] %s: HDLC unescaped len=%d", tci.name, len(unescaped))
					if len(unescaped) > 0 {
						atomic.AddUint64(&tci.rxBytes, uint64(len(unescaped)))
						if tci.inboundHandler != nil {
							log.Printf("[TCP] %s: calling inboundHandler with len=%d", tci.name, len(unescaped))
							tci.inboundHandler(unescaped, tci)
						} else {
							log.Printf("[TCP] %s: inboundHandler is nil!", tci.name)
						}
					}
					frameBuffer = frameBuffer[end:]
				}
			}
		}
	}

	tci.mu.Lock()
	if tci.conn != nil {
		if err := tci.conn.Close(); err != nil {
			fmt.Printf("tcp client interface %v close failed: %v\n", tci.name, err)
		}
	}
	tci.mu.Unlock()
	atomic.StoreInt32(&tci.running, 0)

	if !tci.IsDetached() {
		go tci.reconnectLoop()
	}
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

	log.Printf("[TCP] %s: Send writing %d bytes (frame len=%d)", tci.name, len(data), len(frame))
	n, err := conn.Write(frame)
	if err != nil {
		log.Printf("[TCP] %s: Send write error: %v", tci.name, err)
		return err
	}
	log.Printf("[TCP] %s: Send wrote %d bytes", tci.name, n)

	atomic.AddUint64(&tci.txBytes, uint64(n))
	return nil
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

	running int32
	mu      sync.Mutex
}

// NewTCPServerInterface binds to the given IP and port and starts a listening
// socket for incoming TCP peers. It then enters a non-blocking accept loop and
// delegates connection handling to spawned client interfaces.
func NewTCPServerInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler) (*TCPServerInterface, error) {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)

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
				panicOnInterfaceErrorf("tcp interface %v accept failed: %v", tsi.name, err)
			}
			break
		}

		tsi.handleConnection(conn)
	}
}

func (tsi *TCPServerInterface) handleConnection(conn net.Conn) {
	name := fmt.Sprintf("Client %v on %v", conn.RemoteAddr().String(), tsi.name)
	log.Printf("[TCP] Server %s: accepted connection from %s, creating spawned interface", tsi.name, conn.RemoteAddr())
	// Disable Nagle's algorithm to ensure small packets are sent immediately
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.SetNoDelay(true); err != nil {
			log.Printf("[TCP] Failed to set TCP_NODELAY: %v", err)
		}
	}
	// Create a TCPClientInterface from the connected socket
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	tci := &TCPClientInterface{
		BaseInterface:  bi,
		conn:           conn,
		inboundHandler: tsi.inboundHandler,
	}
	atomic.StoreInt32(&tci.running, 1)

	tsi.mu.Lock()
	tsi.spawnedInterfaces = append(tsi.spawnedInterfaces, tci)
	tsi.mu.Unlock()

	// Start readLoop FIRST so interface can receive data
	go tci.readLoop()
	log.Printf("[TCP] Server %s: started readLoop for %s", tsi.name, tci.name)

	// Then register with transport (which will trigger re-announce)
	log.Printf("[TCP] Server %s: spawned interface %s, calling connectHandler", tsi.name, tci.name)
	if tsi.connectHandler != nil {
		tsi.connectHandler(tci)
		log.Printf("[TCP] Server %s: connectHandler completed for %s", tsi.name, tci.name)
	}
}

// Send forwards the payload to each active spawned client connection.
func (tsi *TCPServerInterface) Send(data []byte) error {
	tsi.mu.Lock()
	defer tsi.mu.Unlock()
	for _, ci := range tsi.spawnedInterfaces {
		if ci != nil && ci.Status() {
			if err := ci.Send(data); err != nil {
				fmt.Printf("Failed to send to spawned client %v: %v\n", ci.name, err)
			}
		}
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
