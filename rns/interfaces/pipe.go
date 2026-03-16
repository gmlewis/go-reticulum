// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "sync"

// PipeInterface establishes a direct in-memory conduit between two endpoints,
// optimized for localized testing and simulation. It bypasses the physical
// network stack and enables rapid point-to-point payload delivery using Go
// channels.
type PipeInterface struct {
	*BaseInterface
	Other     *PipeInterface
	OnReceive func([]byte, Interface)
	queue     chan []byte
	closeOnce sync.Once
}

// NewPipeInterface provisions an asynchronous in-memory testing channel. It
// spawns a dedicated worker goroutine to drain the internal queue and dispatch
// received frames into the linked peer's callback.
func NewPipeInterface(name string, onReceive func([]byte, Interface)) *PipeInterface {
	p := &PipeInterface{
		BaseInterface: NewBaseInterface(name, ModeFull, 1000000),
		OnReceive:     onReceive,
		queue:         make(chan []byte, 100),
	}
	go p.processQueue()
	return p
}

func (p *PipeInterface) processQueue() {
	for data := range p.queue {
		if p.Other != nil && p.Other.OnReceive != nil {
			p.Other.OnReceive(data, p.Other)
		}
	}
}

func (p *PipeInterface) Send(data []byte) error {
	p.txBytes += uint64(len(data))
	// Use a copy to avoid data races if the buffer is reused
	buf := make([]byte, len(data))
	copy(buf, data)
	defer func() { recover() }()
	p.queue <- buf
	return nil
}

func (p *PipeInterface) Type() string { return "PipeInterface" }
func (p *PipeInterface) IsOut() bool  { return true }
func (p *PipeInterface) Status() bool { return true }

// Detach closes the internal queue channel, stopping the background
// processQueue goroutine and releasing resources.
func (p *PipeInterface) Detach() error {
	p.closeOnce.Do(func() { close(p.queue) })
	return nil
}
