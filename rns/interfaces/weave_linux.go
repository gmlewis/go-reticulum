// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"strings"
	"sync"
	"time"
)

const (
	WeaveDefaultSpeed    = 3000000
	WeaveDefaultDataBits = 8
	WeaveDefaultStopBits = 1
	WeaveDefaultParity   = "N"

	// WeaveDefaultIFACSize is Weave's own DEFAULT_IFAC_SIZE (16), overriding
	// the wrapped serial interface's 8
	// (RNS/Interfaces/WeaveInterface.py:820,881-884).
	WeaveDefaultIFACSize = 16
)

// WeaveInterface implements a specialized, ultra-low latency serial abstraction for
// Weave routing endpoints. It builds on the standard serial stack while applying
// Weave-specific transmission defaults.
type WeaveInterface struct {
	inner Interface

	hash     []byte
	hashOnce sync.Once
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

// Gravity reports the wrapped interface's gravity (RNS v1.4.1).
func (w *WeaveInterface) Gravity() int { return w.inner.Gravity() }

// RecursivePrs reports the wrapped interface's recursive-PR policy.
func (w *WeaveInterface) RecursivePrs() bool { return w.inner.RecursivePrs() }

// AnnouncesFromInternal reports the wrapped interface's internal-announce policy.
func (w *WeaveInterface) AnnouncesFromInternal() bool { return w.inner.AnnouncesFromInternal() }

// AnnouncesToInternal reports the wrapped interface's boundary→internal policy.
func (w *WeaveInterface) AnnouncesToInternal() *bool { return w.inner.AnnouncesToInternal() }

// Ingress/egress-control delegation to the wrapped interface (v1.1.5).
func (w *WeaveInterface) ReceivedAnnounce()        { w.inner.ReceivedAnnounce() }
func (w *WeaveInterface) ShouldIngressLimit() bool { return w.inner.ShouldIngressLimit() }
func (w *WeaveInterface) HoldAnnounce(raw []byte, recv Interface, hops int, destHash []byte) {
	w.inner.HoldAnnounce(raw, recv, hops, destHash)
}
func (w *WeaveInterface) ProcessHeldAnnounces() ([]byte, Interface, bool) {
	return w.inner.ProcessHeldAnnounces()
}
func (w *WeaveInterface) HeldAnnounces() int           { return w.inner.HeldAnnounces() }
func (w *WeaveInterface) ReceivedPathRequest()         { w.inner.ReceivedPathRequest() }
func (w *WeaveInterface) SentPathRequest()             { w.inner.SentPathRequest() }
func (w *WeaveInterface) IncomingPrFrequency() float64 { return w.inner.IncomingPrFrequency() }
func (w *WeaveInterface) OutgoingPrFrequency() float64 { return w.inner.OutgoingPrFrequency() }
func (w *WeaveInterface) ShouldIngressLimitPr() bool   { return w.inner.ShouldIngressLimitPr() }
func (w *WeaveInterface) ShouldEgressLimitPr() bool    { return w.inner.ShouldEgressLimitPr() }

// Announce-frequency + ingress-control burst-state delegation
// (Interface.py:121-124,277-297). ifstats reads these via the concrete
// interface (Reticulum.py:1453-1464).
func (w *WeaveInterface) IncomingAnnounceFrequency() float64 {
	return w.inner.IncomingAnnounceFrequency()
}
func (w *WeaveInterface) OutgoingAnnounceFrequency() float64 {
	return w.inner.OutgoingAnnounceFrequency()
}
func (w *WeaveInterface) ICBurstActive() bool           { return w.inner.ICBurstActive() }
func (w *WeaveInterface) ICBurstActivated() time.Time   { return w.inner.ICBurstActivated() }
func (w *WeaveInterface) ICPrBurstActive() bool         { return w.inner.ICPrBurstActive() }
func (w *WeaveInterface) ICPrBurstActivated() time.Time { return w.inner.ICPrBurstActivated() }

// Announce-rate-control delegation (Interface.py:90-92,118-120).
func (w *WeaveInterface) AnnounceRateTarget() *int  { return w.inner.AnnounceRateTarget() }
func (w *WeaveInterface) AnnounceRateGrace() *int   { return w.inner.AnnounceRateGrace() }
func (w *WeaveInterface) AnnounceRatePenalty() *int { return w.inner.AnnounceRatePenalty() }

// DefaultIFACSize returns Weave's own DEFAULT_IFAC_SIZE (16), which overrides
// the wrapped serial interface's 8 (RNS/Interfaces/WeaveInterface.py:820).
func (w *WeaveInterface) DefaultIFACSize() int { return WeaveDefaultIFACSize }

// MemoizedHash returns the memoized identity hash, computing it via compute on
// the first call and caching the result. Mirrors Python Interface.get_hash
// (RNS/Interfaces/Interface.py:144-146) so the wrapper's own string identity
// ("WeaveInterface[<name>]") is hashed at most once.
func (w *WeaveInterface) MemoizedHash(compute func() []byte) []byte {
	w.hashOnce.Do(func() {
		if w.hash == nil && compute != nil {
			w.hash = compute()
		}
	})
	return w.hash
}

// SetBitrate propagates a bitrate override to the wrapped serial interface
// when it supports that operation.
func (w *WeaveInterface) SetBitrate(bitrate int) {
	if setter, ok := w.inner.(interface{ SetBitrate(int) }); ok {
		setter.SetBitrate(bitrate)
	}
}

// SetMode propagates an interface mode override to the wrapped serial
// interface when it supports that operation.
func (w *WeaveInterface) SetMode(mode int) {
	if setter, ok := w.inner.(interface{ SetMode(int) }); ok {
		setter.SetMode(mode)
	}
}

// SetIFACConfig propagates IFAC configuration to the wrapped serial interface
// when it supports that operation. Weave's DEFAULT_IFAC_SIZE (16) overrides the
// wrapped serial default (8), so an unset Size is filled with Weave's value
// before delegation (RNS/Interfaces/WeaveInterface.py:881-884).
func (w *WeaveInterface) SetIFACConfig(cfg IFACConfig) {
	if cfg.Size < 1 {
		cfg.Size = WeaveDefaultIFACSize
	}
	if setter, ok := w.inner.(interface{ SetIFACConfig(IFACConfig) }); ok {
		setter.SetIFACConfig(cfg)
	}
}

// SetDiscoveryConfig propagates discovery metadata to the wrapped serial
// interface when supported.
func (w *WeaveInterface) SetDiscoveryConfig(cfg DiscoveryConfig) {
	if setter, ok := w.inner.(interface{ SetDiscoveryConfig(DiscoveryConfig) }); ok {
		setter.SetDiscoveryConfig(cfg)
	}
}

// DiscoveryConfig returns the wrapped discovery metadata when available.
func (w *WeaveInterface) DiscoveryConfig() DiscoveryConfig {
	if getter, ok := w.inner.(interface{ DiscoveryConfig() DiscoveryConfig }); ok {
		return getter.DiscoveryConfig()
	}
	return DiscoveryConfig{}
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
