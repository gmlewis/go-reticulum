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

// WeaveInterface implements a highly specialized, ultra-low latency serial abstraction tailored specifically for Weave routing endpoints.
// It inherits the foundational robustness of the standard serial stack but aggressively forces transmission parameters to Weave's accelerated defaults.
type WeaveInterface struct {
	inner Interface
}

// NewWeaveInterface strictly enforces port validity before securely binding a serial interface at the Weave-mandated 3 Mbps baud rate.
// It serves as the authoritative factory, automatically adjusting the logical bitrate capacity metrics reported to the routing engine.
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

func (w *WeaveInterface) Name() string           { return w.inner.Name() }
func (w *WeaveInterface) Type() string           { return "WeaveInterface" }
func (w *WeaveInterface) Status() bool           { return w.inner.Status() }
func (w *WeaveInterface) IsOut() bool            { return w.inner.IsOut() }
func (w *WeaveInterface) Mode() int              { return w.inner.Mode() }
func (w *WeaveInterface) Bitrate() int           { return w.inner.Bitrate() }
func (w *WeaveInterface) Send(data []byte) error { return w.inner.Send(data) }
func (w *WeaveInterface) BytesReceived() uint64  { return w.inner.BytesReceived() }
func (w *WeaveInterface) BytesSent() uint64      { return w.inner.BytesSent() }
func (w *WeaveInterface) Detach() error          { return w.inner.Detach() }
func (w *WeaveInterface) IsDetached() bool       { return w.inner.IsDetached() }
func (w *WeaveInterface) Age() time.Duration     { return w.inner.Age() }

func (w *WeaveInterface) SetBitrate(bitrate int) {
	if setter, ok := w.inner.(interface{ SetBitrate(int) }); ok {
		setter.SetBitrate(bitrate)
	}
}

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
