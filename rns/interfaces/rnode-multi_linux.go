// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"fmt"
	"sync/atomic"
	"time"
)

type RNodeMultiInterface struct {
	children []Interface
	created  time.Time
	nextSend uint32
}

func NewRNodeMultiInterface(name, port string, speed, databits, stopbits int, parity string, idInterval int, idCallsign string, subinterfaces []RNodeMultiSubinterfaceConfig, handler InboundHandler) (Interface, error) {
	enabled := make([]RNodeMultiSubinterfaceConfig, 0, len(subinterfaces))
	for _, sub := range subinterfaces {
		if sub.Enabled {
			enabled = append(enabled, sub)
		}
	}

	if len(enabled) == 0 {
		return nil, fmt.Errorf("no subinterfaces enabled for %v", name)
	}

	children := make([]Interface, 0, len(enabled))
	for index, active := range enabled {
		childName := fmt.Sprintf("%v/%v", name, active.Name)
		if active.Name == "" {
			childName = fmt.Sprintf("%v/sub%v", name, index)
		}

		iface, err := NewRNodeInterface(childName, port, speed, databits, stopbits, parity, active.Frequency, active.Bandwidth, active.TXPower, active.SpreadingFactor, active.CodingRate, active.FlowControl, idInterval, idCallsign, handler)
		if err != nil {
			for _, child := range children {
				_ = child.Detach()
			}
			return nil, err
		}
		children = append(children, iface)
	}

	return &RNodeMultiInterface{children: children, created: time.Now()}, nil
}

func (r *RNodeMultiInterface) Name() string {
	if len(r.children) == 0 {
		return ""
	}
	return r.children[0].Name()
}

func (r *RNodeMultiInterface) Type() string {
	return "RNodeMultiInterface"
}

func (r *RNodeMultiInterface) Status() bool {
	for _, child := range r.children {
		if child.Status() {
			return true
		}
	}
	return false
}

func (r *RNodeMultiInterface) IsOut() bool {
	for _, child := range r.children {
		if child.IsOut() {
			return true
		}
	}
	return false
}

func (r *RNodeMultiInterface) Mode() int {
	if len(r.children) == 0 {
		return ModeFull
	}
	return r.children[0].Mode()
}

func (r *RNodeMultiInterface) Bitrate() int {
	total := 0
	for _, child := range r.children {
		total += child.Bitrate()
	}
	return total
}

func (r *RNodeMultiInterface) Send(data []byte) error {
	if len(r.children) == 0 {
		return fmt.Errorf("RNodeMultiInterface has no child interfaces")
	}
	index := int(atomic.AddUint32(&r.nextSend, 1)-1) % len(r.children)
	return r.children[index].Send(data)
}

func (r *RNodeMultiInterface) BytesReceived() uint64 {
	var total uint64
	for _, child := range r.children {
		total += child.BytesReceived()
	}
	return total
}

func (r *RNodeMultiInterface) BytesSent() uint64 {
	var total uint64
	for _, child := range r.children {
		total += child.BytesSent()
	}
	return total
}

func (r *RNodeMultiInterface) Detach() error {
	var firstErr error
	for _, child := range r.children {
		if err := child.Detach(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *RNodeMultiInterface) IsDetached() bool {
	if len(r.children) == 0 {
		return true
	}
	for _, child := range r.children {
		if !child.IsDetached() {
			return false
		}
	}
	return true
}

func (r *RNodeMultiInterface) Age() time.Duration {
	if !r.created.IsZero() {
		return time.Since(r.created)
	}
	if len(r.children) == 0 {
		return 0
	}
	return r.children[0].Age()
}

func (r *RNodeMultiInterface) SetBitrate(bitrate int) {
	for _, child := range r.children {
		if setter, ok := child.(interface{ SetBitrate(int) }); ok {
			setter.SetBitrate(bitrate)
		}
	}
}

func (r *RNodeMultiInterface) SetIFACConfig(cfg IFACConfig) {
	for _, child := range r.children {
		if setter, ok := child.(interface{ SetIFACConfig(IFACConfig) }); ok {
			setter.SetIFACConfig(cfg)
		}
	}
}
