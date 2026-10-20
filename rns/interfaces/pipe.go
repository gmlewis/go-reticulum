// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"sync"
	"sync/atomic"
)

// PipeDefaultIFACSize is the DEFAULT_IFAC_SIZE for pipe interfaces
// (RNS/Interfaces/PipeInterface.py:39).
const PipeDefaultIFACSize = 8

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
	// hwmtu is this interface's hardware MTU ceiling for inbound frames, matching the
	// TCP and Backbone interfaces (their `hwmtu` fields). An in-memory pipe has no wire
	// MTU of its own, so it defaults to PipeDefaultMTU — effectively unlimited, which is
	// the behaviour every existing test relies on. Tests that need an MTU-limited link
	// set it with SetHardwareMTU.
	hwmtu int
}

// PipeDefaultMTU is the MTU an untouched in-memory pipe reports: large enough that no
// frame is ever dropped by default.
const PipeDefaultMTU = 1000000

// NewPipeInterface provisions an asynchronous in-memory testing channel. It
// spawns a dedicated worker goroutine to drain the internal queue and dispatch
// received frames into the linked peer's callback.
func NewPipeInterface(name string, onReceive func([]byte, Interface)) *PipeInterface {
	p := &PipeInterface{
		BaseInterface: NewBaseInterface(name, ModeFull, 1000000),
		OnReceive:     onReceive,
		queue:         make(chan []byte, 1000),
		hwmtu:         PipeDefaultMTU,
	}
	p.setDefaultIFACSize(PipeDefaultIFACSize)
	go p.processQueue()
	return p
}

func (p *PipeInterface) processQueue() {
	for data := range p.queue {
		p.mu.RLock()
		other := p.other
		p.mu.RUnlock()
		if other == nil || other.OnReceive == nil {
			continue
		}
		// The RECEIVING interface enforces its own MTU, exactly as the TCP and Backbone
		// receive loops do before dispatching an inbound frame (CheckFrameLen, which
		// mirrors Python's check_frame_len). A frame over the peer's ceiling is dropped
		// rather than delivered, so a test can exercise MTU-limited behaviour.
		other.mu.RLock()
		hwmtu := other.hwmtu
		other.mu.RUnlock()
		// ifacSize is 0 when no IFAC is configured, matching Python's
		// `(self.ifac_size or 0)`; an IFAC adds its signature to every frame, which is
		// what the ceiling has to allow for.
		ifacSize := 0
		if cfg := other.IFACConfig(); cfg.Enabled {
			ifacSize = cfg.Size
			if ifacSize <= 0 {
				ifacSize = other.DefaultIFACSize()
			}
		}
		if !CheckFrameLen(len(data), hwmtu, ifacSize) {
			InvalidFrame(other.Name(), len(data), hwmtu, ifacSize)
			continue
		}
		other.OnReceive(data, other)
	}
}

// SetHardwareMTU sets this interface's inbound frame ceiling, mirroring the hwmtu a
// configured TCP or Backbone interface carries. It is the seam a test uses to build an
// MTU-limited link (a resource advertisement or frame larger than the ceiling is then
// dropped on arrival instead of delivered).
func (p *PipeInterface) SetHardwareMTU(mtu int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if mtu > 0 {
		p.hwmtu = mtu
	}
}

// HardwareMTU returns this interface's inbound frame ceiling.
func (p *PipeInterface) HardwareMTU() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hwmtu
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

	atomic.AddUint64(&p.txBytes, uint64(len(data)))
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
