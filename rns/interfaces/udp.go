// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
)

const (
	UDPBitrateGuess = 10 * 1000 * 1000
)

// UDPInterface implements a high-throughput, connectionless transport interface leveraging standard User Datagram Protocol semantics.
// It is explicitly designed to handle best-effort broadcast, multicast, or direct point-to-point datagrams across IP networks.
type UDPInterface struct {
	*BaseInterface

	listenAddr  *net.UDPAddr
	forwardAddr *net.UDPAddr
	conn        *net.UDPConn

	inboundHandler InboundHandler

	running int32
	mu      sync.Mutex
}

// NewUDPInterface provisions a robust UDP socket bound to the specified listen coordinates and configured with a default forwarding target.
// It rapidly boots the asynchronous listening loop, readying the interface to ingest and broadcast connectionless frames.
func NewUDPInterface(name, listenIP string, listenPort int, forwardIP string, forwardPort int, handler InboundHandler) (*UDPInterface, error) {
	bi := NewBaseInterface(name, ModeFull, UDPBitrateGuess)

	lAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%v:%v", listenIP, listenPort))
	if err != nil {
		return nil, err
	}

	fAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%v:%v", forwardIP, forwardPort))
	if err != nil {
		return nil, err
	}

	ui := &UDPInterface{
		BaseInterface:  bi,
		listenAddr:     lAddr,
		forwardAddr:    fAddr,
		inboundHandler: handler,
	}

	if err := ui.start(); err != nil {
		return nil, err
	}

	return ui, nil
}

func (ui *UDPInterface) start() error {
	conn, err := net.ListenUDP("udp", ui.listenAddr)
	if err != nil {
		return err
	}
	ui.conn = conn
	atomic.StoreInt32(&ui.running, 1)

	go ui.listenLoop()
	return nil
}

func (ui *UDPInterface) listenLoop() {
	buf := make([]byte, 2048)
	for atomic.LoadInt32(&ui.running) == 1 {
		n, _, err := ui.conn.ReadFromUDP(buf)
		if err != nil {
			if atomic.LoadInt32(&ui.running) == 1 && !ui.IsDetached() {
				ui.panicOnInterfaceErrorf("udp interface %v read failed: %v", ui.name, err)
			}
			break
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		atomic.AddUint64(&ui.rxBytes, uint64(n))

		if ui.inboundHandler != nil {
			ui.inboundHandler(data, ui)
		}
	}
}

// Send writes the datagram to the configured forward UDP address.
func (ui *UDPInterface) Send(data []byte) error {
	log.Printf("Go UDPInterface %v sending %v bytes to %v", ui.name, len(data), ui.forwardAddr)
	ui.mu.Lock()
	conn := ui.conn
	ui.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("no connection for interface %v", ui.name)
	}

	if atomic.LoadInt32(&ui.running) == 0 {
		return fmt.Errorf("interface %v is not running", ui.name)
	}

	// Create a temporary connection for sending if it's a broadcast or specific forward
	// In Python it uses a single socket and sendto.
	n, err := conn.WriteToUDP(data, ui.forwardAddr)
	if err != nil {
		return err
	}

	atomic.AddUint64(&ui.txBytes, uint64(n))
	return nil
}

// Status returns true if the interface is online.
func (ui *UDPInterface) Status() bool {
	return atomic.LoadInt32(&ui.running) == 1
}

// Type identifies this interface as a UDP transport.
func (ui *UDPInterface) Type() string {
	return "UDPInterface"
}

// IsOut reports whether this interface can originate outbound datagrams.
func (ui *UDPInterface) IsOut() bool {
	return true
}

// Detach closes the interface and stops the listener.
func (ui *UDPInterface) Detach() error {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	atomic.StoreInt32(&ui.running, 0)
	ui.SetDetached(true)
	if ui.conn != nil {
		return ui.conn.Close()
	}
	return nil
}
