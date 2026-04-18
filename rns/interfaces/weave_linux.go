// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"strings"
	"time"
)

const (
	WeaveDefaultSpeed    = 3000000
	WeaveDefaultDataBits = 8
	WeaveDefaultStopBits = 1
	WeaveDefaultParity   = "N"
)

// WeaveInterface implements a specialized, ultra-low latency serial abstraction for
// Weave routing endpoints. It builds on the standard serial stack while applying
// Weave-specific transmission defaults.
type WeaveInterface struct {
	inner Interface
}

// NewWeaveInterface enforces port validity and binds a serial interface at Weave's
// mandated 3 Mbps baud rate. It adjusts logical bitrate metrics reported to the
// routing engine as necessary.
func NewWeaveInterface(name, port string, configuredBitrate int, handler InboundHandler) (Interface, error) {
	if !validWeavePort(port) {
		return nil, errNoPortForWeave()
	}

	iface, err := NewSerialInterface(name, port, WeaveDefaultSpeed, WeaveDefaultDataBits, WeaveDefaultStopBits, WeaveDefaultParity, handler)
	if err != nil {
		return nil, err
	}

	wi := &WeaveInterface{inner: iface}
	if configuredBitrate > 0 {
		wi.SetBitrate(configuredBitrate)
	}

	return wi, nil
}

// Name returns the configured interface name.
func (w *WeaveInterface) Name() string { return w.inner.Name() }

// Type identifies this interface as a Weave serial transport.
func (w *WeaveInterface) Type() string { return "WeaveInterface" }

// Status reports whether the wrapped interface is currently active.
func (w *WeaveInterface) Status() bool { return w.inner.Status() }

// IsOut reports whether the wrapped interface can originate outbound traffic.
func (w *WeaveInterface) IsOut() bool { return w.inner.IsOut() }

// Mode returns the operating mode of the wrapped interface.
func (w *WeaveInterface) Mode() int { return w.inner.Mode() }

// Bitrate returns the bitrate reported by the wrapped interface.
func (w *WeaveInterface) Bitrate() int { return w.inner.Bitrate() }

// Send forwards the payload to the wrapped interface.
func (w *WeaveInterface) Send(data []byte) error { return w.inner.Send(data) }

// BytesReceived returns the total bytes received by the wrapped interface.
func (w *WeaveInterface) BytesReceived() uint64 { return w.inner.BytesReceived() }

// BytesSent returns the total bytes sent by the wrapped interface.
func (w *WeaveInterface) BytesSent() uint64 { return w.inner.BytesSent() }

// Detach detaches the wrapped interface.
func (w *WeaveInterface) Detach() error { return w.inner.Detach() }

// IsDetached reports whether the wrapped interface has been detached.
func (w *WeaveInterface) IsDetached() bool { return w.inner.IsDetached() }

// Age returns how long the wrapped interface has existed.
func (w *WeaveInterface) Age() time.Duration { return w.inner.Age() }

// SetBitrate propagates a bitrate override to the wrapped serial interface
// when it supports that operation.
func (w *WeaveInterface) SetBitrate(bitrate int) {
	if setter, ok := w.inner.(interface{ SetBitrate(int) }); ok {
		setter.SetBitrate(bitrate)
	}
}

// SetIFACConfig propagates IFAC configuration to the wrapped serial interface
// when it supports that operation.
func (w *WeaveInterface) SetIFACConfig(cfg IFACConfig) {
	if setter, ok := w.inner.(interface{ SetIFACConfig(IFACConfig) }); ok {
		setter.SetIFACConfig(cfg)
	}
}

func validWeavePort(port string) bool {
	return strings.TrimSpace(port) != ""
}

func errNoPortForWeave() error {
	return &weaveConfigError{msg: "no port specified for Weave interface"}
}

type weaveConfigError struct {
	msg string
}

func (e *weaveConfigError) Error() string {
	return e.msg
}
