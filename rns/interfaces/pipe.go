// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

// PipeInterface establishes a direct, in-memory conduit connecting two endpoints, explicitly optimized for localized testing and simulation.
// It bypasses the physical network stack, allowing rapid point-to-point payload delivery via concurrent Go channels.
type PipeInterface struct {
	*BaseInterface
	Other     *PipeInterface
	OnReceive func([]byte, Interface)
	queue     chan []byte
}

// NewPipeInterface provisions an asynchronous, unidirectional in-memory testing channel.
// It spawns a dedicated worker routine to drain the internal queue and dispatch received frames directly into the linked peer's callback.
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
	p.queue <- buf
	return nil
}

func (p *PipeInterface) Type() string  { return "PipeInterface" }
func (p *PipeInterface) IsOut() bool   { return true }
func (p *PipeInterface) Status() bool  { return true }
func (p *PipeInterface) Detach() error { return nil }
