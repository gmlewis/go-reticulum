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
	TCPBitrateGuess = 1000000
	TCPHWMTU        = 1064
)

type TCPClientInterface struct {
	*BaseInterface
	conn           net.Conn
	inboundHandler InboundHandler
	targetHost     string
	targetPort     int
	kissFraming    bool
	running        int32
	mu             sync.Mutex
}

func NewTCPClientInterface(name, host string, port int, kiss bool, handler InboundHandler) (*TCPClientInterface, error) {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	tci := &TCPClientInterface{
		BaseInterface:  bi,
		inboundHandler: handler,
		targetHost:     host,
		targetPort:     port,
		kissFraming:    kiss,
	}

	if err := tci.connect(); err != nil {
		// In Python it starts a reconnection thread if initial connect fails
		go tci.reconnectLoop()
	} else {
		go tci.readLoop()
	}

	return tci, nil
}

func (tci *TCPClientInterface) Type() string {
	return "TCPClientInterface"
}

func (tci *TCPClientInterface) connect() error {
	addr := fmt.Sprintf("%v:%v", tci.targetHost, tci.targetPort)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	tci.mu.Lock()
	tci.conn = conn
	tci.mu.Unlock()
	atomic.StoreInt32(&tci.running, 1)
	log.Printf("TCP client interface %v connected to %v", tci.name, addr)
	return nil
}

func (tci *TCPClientInterface) reconnectLoop() {
	if tci.targetHost == "" {
		return
	}
	for atomic.LoadInt32(&tci.running) == 0 && !tci.IsDetached() {
		time.Sleep(5 * time.Second)
		if err := tci.connect(); err == nil {
			go tci.readLoop()
			return
		} else {
			log.Printf("TCP client interface %v reconnection attempt to %v:%v failed: %v", tci.name, tci.targetHost, tci.targetPort, err)
		}
	}
}

func (tci *TCPClientInterface) readLoop() {
	buf := make([]byte, 4096)
	frameBuffer := make([]byte, 0, TCPHWMTU)

	for atomic.LoadInt32(&tci.running) == 1 {
		n, err := tci.conn.Read(buf)
		if err != nil {
			log.Printf("TCP interface %v read error: %v", tci.name, err)
			break
		}

		if n > 0 {
			log.Printf("TCP interface %v read %d bytes", tci.name, n)
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
					unescaped := HDLCUnescape(frame)
					if len(unescaped) > 0 {
						atomic.AddUint64(&tci.rxBytes, uint64(len(unescaped)))
						if tci.inboundHandler != nil {
							tci.inboundHandler(unescaped, tci)
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
		return fmt.Errorf("interface %v has no connection", tci.name)
	}

	log.Printf("TCP interface %v writing %d bytes (framed %d bytes)", tci.name, len(data), len(frame))
	n, err := conn.Write(frame)
	if err != nil {
		log.Printf("TCP interface %v write error: %v", tci.name, err)
		return err
	}
	atomic.AddUint64(&tci.txBytes, uint64(len(data)))
	if n != len(frame) {
		log.Printf("TCP interface %v short write: %d of %d", tci.name, n, len(frame))
		return fmt.Errorf("short write on %v", tci.name)
	}

	return nil
}

func (tci *TCPClientInterface) Detach() error {
	tci.SetDetached(true)
	atomic.StoreInt32(&tci.running, 0)
	tci.mu.Lock()
	if tci.conn != nil {
		_ = tci.conn.Close()
	}
	tci.mu.Unlock()
	return nil
}

type TCPServerInterface struct {
	*BaseInterface
	listenIP       string
	listenPort     int
	listener       net.Listener
	inboundHandler InboundHandler
	connectHandler ConnectHandler
	mu             sync.Mutex
}

func NewTCPServerInterface(name, ip string, port int, handler InboundHandler, onConnect ConnectHandler) (*TCPServerInterface, error) {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	tsi := &TCPServerInterface{
		BaseInterface:  bi,
		listenIP:       ip,
		listenPort:     port,
		inboundHandler: handler,
		connectHandler: onConnect,
	}

	addr := fmt.Sprintf("%v:%v", ip, port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	tsi.listener = l

	go tsi.acceptLoop()

	return tsi, nil
}

func (tsi *TCPServerInterface) Type() string {
	return "TCPServerInterface"
}

func (tsi *TCPServerInterface) acceptLoop() {
	for !tsi.IsDetached() {
		conn, err := tsi.listener.Accept()
		if err != nil {
			if !tsi.IsDetached() {
				fmt.Printf("tcp server interface %v accept failed: %v\n", tsi.name, err)
			}
			break
		}
		go tsi.handleConnection(conn)
	}
}

func (tsi *TCPServerInterface) handleConnection(conn net.Conn) {
	name := fmt.Sprintf("Client %v on %v", conn.RemoteAddr().String(), tsi.name)
	// Create a TCPClientInterface from the connected socket
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	tci := &TCPClientInterface{
		BaseInterface:  bi,
		conn:           conn,
		inboundHandler: tsi.inboundHandler,
	}
	atomic.StoreInt32(&tci.running, 1)

	log.Printf("TCP server interface %v accepted connection from %v", tsi.name, conn.RemoteAddr().String())

	if tsi.connectHandler != nil {
		tsi.connectHandler(tci)
	}

	go tci.readLoop()
}

func (tsi *TCPServerInterface) Send(data []byte) error {
	// Server interface broadcasts to all connected clients?
	// Actually, Reticulum transport handles this by calling Send on specific client interfaces if they are registered.
	// But TCPServerInterface is registered as one interface.
	// In Python, TCPServerInterface maintains a list of handlers.
	// For now, let's just error or implement a simple broadcast if needed.
	return fmt.Errorf("Send not implemented directly on TCPServerInterface")
}

func (tsi *TCPServerInterface) Detach() error {
	tsi.SetDetached(true)
	if tsi.listener != nil {
		return tsi.listener.Close()
	}
	return nil
}
