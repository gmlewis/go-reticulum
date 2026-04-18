// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"sync"
)

// PipeInterface establishes a direct in-memory conduit between two endpoints,
// optimized for localized testing and simulation. It bypasses the physical
// network stack and enables rapid point-to-point payload delivery using Go
// channels.
type PipeInterface struct {
	*BaseInterface
	other     *PipeInterface
	OnReceive func([]byte, Interface)
	queue     chan []byte
	mu        sync.RWMutex
	closeOnce sync.Once
}

// NewPipeInterface provisions an asynchronous in-memory testing channel. It
// spawns a dedicated worker goroutine to drain the internal queue and dispatch
// received frames into the linked peer's callback.
func NewPipeInterface(name string, onReceive func([]byte, Interface)) *PipeInterface {
	p := &PipeInterface{
		BaseInterface: NewBaseInterface(name, ModeFull, 1000000),
		OnReceive:     onReceive,
		queue:         make(chan []byte, 1000),
	}
	go p.processQueue()
	return p
}

func (p *PipeInterface) processQueue() {
	for data := range p.queue {
		p.mu.RLock()
		other := p.other
		p.mu.RUnlock()
		if other != nil && other.OnReceive != nil {
			other.OnReceive(data, other)
		}
	}
}

// SetOther establishes the peer PipeInterface for bidirectional communication.
func (p *PipeInterface) SetOther(other *PipeInterface) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.other = other
}

// Send queues a copy of the payload for delivery to the paired PipeInterface.
func (p *PipeInterface) Send(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.IsDetached() {
		return nil
	}

	p.txBytes += uint64(len(data))
	// Use a copy to avoid data races if the buffer is reused
	buf := make([]byte, len(data))
	copy(buf, data)
	p.queue <- buf
	return nil
}

// Type identifies this interface as an in-memory pipe transport.
func (p *PipeInterface) Type() string { return "PipeInterface" }

// IsOut reports whether the pipe may originate outbound traffic.
func (p *PipeInterface) IsOut() bool { return true }

// Status reports whether the in-memory pipe is available for use.
func (p *PipeInterface) Status() bool { return true }

// Detach closes the internal queue channel, stopping the background
// processQueue goroutine and releasing resources.
func (p *PipeInterface) Detach() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closeOnce.Do(func() {
		p.SetDetached(true)
		close(p.queue)
	})
	return nil
}
