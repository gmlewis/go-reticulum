// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	LocalBitrate = 1000 * 1000 * 1000
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
		BaseInterface:  bi,
		path:           path,
		port:           port,
		inboundHandler: handler,
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
	return nil
}

func (lci *LocalClientInterface) reconnectLoop() {
	for atomic.LoadInt32(&lci.running) == 0 && !lci.IsDetached() {
		time.Sleep(5 * time.Second)
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
		go lci.reconnectLoop()
	}
}

// Send HDLC-frames the payload and writes it to the connected local shared
// instance transport.
func (lci *LocalClientInterface) Send(data []byte) error {
	if atomic.LoadInt32(&lci.running) != 1 {
		return fmt.Errorf("interface %v is not running", lci.name)
	}

	frame := append([]byte{HDLCFlag}, HDLCEscape(data)...)
	frame = append(frame, HDLCFlag)

	lci.mu.Lock()
	conn := lci.conn
	lci.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection for interface %v", lci.name)
	}

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

// Type identifies this interface as a local shared-instance transport.
func (lci *LocalClientInterface) Type() string {
	return "LocalInterface"
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
			if atomic.LoadInt32(&lsi.running) == 1 && !lsi.IsDetached() {
				lsi.panicOnInterfaceErrorf("local interface %v accept failed: %v", lsi.name, err)
			}
			break
		}

		lsi.handleConnection(conn)
	}
}

func newLocalClientInterfaceFromConn(name string, conn net.Conn, handler InboundHandler) *LocalClientInterface {
	lci := &LocalClientInterface{
		BaseInterface:  NewBaseInterface(name, ModeFull, LocalBitrate),
		conn:           conn,
		inboundHandler: handler,
	}
	atomic.StoreInt32(&lci.running, 1)
	return lci
}

func (lsi *LocalServerInterface) handleConnection(conn net.Conn) {
	name := fmt.Sprintf("Local Client %v", conn.RemoteAddr().String())
	lci := newLocalClientInterfaceFromConn(name, conn, lsi.inboundHandler)
	lci.copyPanicOnInterfaceErrorFrom(lsi.BaseInterface)

	lsi.mu.Lock()
	lsi.spawnedInterfaces = append(lsi.spawnedInterfaces, lci)
	lsi.mu.Unlock()

	go lci.readLoop()
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
