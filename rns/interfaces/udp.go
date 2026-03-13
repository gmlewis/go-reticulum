// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

const (
	UDPBitrateGuess = 10 * 1000 * 1000
)

// InboundHandler is a function that processes incoming data from an interface.
type InboundHandler func(data []byte, iface Interface)

// UDPInterface implements a Reticulum interface over UDP.
type UDPInterface struct {
	*BaseInterface

	listenAddr  *net.UDPAddr
	forwardAddr *net.UDPAddr
	conn        *net.UDPConn

	inboundHandler InboundHandler

	running int32
	mu      sync.Mutex
}

// NewUDPInterface creates a new UDP interface.
func NewUDPInterface(name string, listenIP string, listenPort int, forwardIP string, forwardPort int, handler InboundHandler) (*UDPInterface, error) {
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

// Send transmits data over the UDP interface.
func (ui *UDPInterface) Send(data []byte) error {
	if atomic.LoadInt32(&ui.running) != 1 {
		return fmt.Errorf("interface %v is not running", ui.name)
	}

	// Create a temporary connection for sending if it's a broadcast or specific forward
	// In Python it uses a single socket and sendto.
	n, err := ui.conn.WriteToUDP(data, ui.forwardAddr)
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

func (ui *UDPInterface) Type() string {
	return "UDPInterface"
}

func (ui *UDPInterface) IsOut() bool {
	return true
}

// Detach closes the interface and stops the listener.
func (ui *UDPInterface) Detach() error {
	atomic.StoreInt32(&ui.running, 0)
	ui.BaseInterface.SetDetached(true)
	if ui.conn != nil {
		return ui.conn.Close()
	}
	return nil
}
