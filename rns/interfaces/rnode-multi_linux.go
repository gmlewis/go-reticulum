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

// RNodeMultiInterface multiplexes multiple physical RNode radios into a unified,
// load-balanced logical interface. It distributes outbound transmissions across
// child hardware in a round-robin fashion to expand RF throughput.
type RNodeMultiInterface struct {
	children []Interface
	created  time.Time
	nextSend uint32
}

// NewRNodeMultiInterface instantiates and validates multiple RNode hardware
// instances from the provided configuration. Partially initialized radios are
// torn down if any single device fails to boot.
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

// Name returns the name of the first child interface, or an empty string when
// no child interfaces are configured.
func (r *RNodeMultiInterface) Name() string {
	if len(r.children) == 0 {
		return ""
	}
	return r.children[0].Name()
}

// Type identifies this interface as an aggregated multi-RNode transport.
func (r *RNodeMultiInterface) Type() string {
	return "RNodeMultiInterface"
}

// Status reports whether any child interface is currently active.
func (r *RNodeMultiInterface) Status() bool {
	for _, child := range r.children {
		if child.Status() {
			return true
		}
	}
	return false
}

// IsOut reports whether any child interface can originate outbound traffic.
func (r *RNodeMultiInterface) IsOut() bool {
	for _, child := range r.children {
		if child.IsOut() {
			return true
		}
	}
	return false
}

// Mode returns the mode of the first child interface, or ModeFull when no
// children are present.
func (r *RNodeMultiInterface) Mode() int {
	if len(r.children) == 0 {
		return ModeFull
	}
	return r.children[0].Mode()
}

// Bitrate returns the aggregate bitrate reported by all child interfaces.
func (r *RNodeMultiInterface) Bitrate() int {
	total := 0
	for _, child := range r.children {
		total += child.Bitrate()
	}
	return total
}

// Send dispatches the payload to one child interface using round-robin
// selection.
func (r *RNodeMultiInterface) Send(data []byte) error {
	if len(r.children) == 0 {
		return fmt.Errorf("RNodeMultiInterface has no child interfaces")
	}
	index := int(atomic.AddUint32(&r.nextSend, 1)-1) % len(r.children)
	return r.children[index].Send(data)
}

// BytesReceived returns the total bytes received across all child interfaces.
func (r *RNodeMultiInterface) BytesReceived() uint64 {
	var total uint64
	for _, child := range r.children {
		total += child.BytesReceived()
	}
	return total
}

// BytesSent returns the total bytes sent across all child interfaces.
func (r *RNodeMultiInterface) BytesSent() uint64 {
	var total uint64
	for _, child := range r.children {
		total += child.BytesSent()
	}
	return total
}

// Detach detaches every child interface and returns the first error observed.
func (r *RNodeMultiInterface) Detach() error {
	var firstErr error
	for _, child := range r.children {
		if err := child.Detach(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// IsDetached reports whether every child interface has been detached.
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

// Age returns how long the aggregate interface has existed.
func (r *RNodeMultiInterface) Age() time.Duration {
	if !r.created.IsZero() {
		return time.Since(r.created)
	}
	if len(r.children) == 0 {
		return 0
	}
	return r.children[0].Age()
}

// SetBitrate propagates a bitrate override to all child interfaces that
// support it.
func (r *RNodeMultiInterface) SetBitrate(bitrate int) {
	for _, child := range r.children {
		if setter, ok := child.(interface{ SetBitrate(int) }); ok {
			setter.SetBitrate(bitrate)
		}
	}
}

// SetMode propagates a routing-mode override to all child interfaces that
// support it.
func (r *RNodeMultiInterface) SetMode(mode int) {
	for _, child := range r.children {
		if setter, ok := child.(interface{ SetMode(int) }); ok {
			setter.SetMode(mode)
		}
	}
}

// SetIFACConfig applies Interface Authentication Codes (IFAC) configuration to
// all child interfaces that support it.
func (r *RNodeMultiInterface) SetIFACConfig(cfg IFACConfig) {
	for _, child := range r.children {
		if setter, ok := child.(interface{ SetIFACConfig(IFACConfig) }); ok {
			setter.SetIFACConfig(cfg)
		}
	}
}

// SetDiscoveryConfig applies discovery metadata to all child interfaces that
// support it.
func (r *RNodeMultiInterface) SetDiscoveryConfig(cfg DiscoveryConfig) {
	for _, child := range r.children {
		if setter, ok := child.(interface{ SetDiscoveryConfig(DiscoveryConfig) }); ok {
			setter.SetDiscoveryConfig(cfg)
		}
	}
}

// DiscoveryConfig returns the discovery metadata of the first child interface,
// or a zero-value config when no children exist.
func (r *RNodeMultiInterface) DiscoveryConfig() DiscoveryConfig {
	if len(r.children) == 0 {
		return DiscoveryConfig{}
	}
	if getter, ok := r.children[0].(interface{ DiscoveryConfig() DiscoveryConfig }); ok {
		return getter.DiscoveryConfig()
	}
	return DiscoveryConfig{}
}
