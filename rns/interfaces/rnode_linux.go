// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	RNodeDefaultSpeed    = 115200
	RNodeDefaultDataBits = 8
	RNodeDefaultStopBits = 1
	RNodeDefaultParity   = "N"

	rNodeFreqMin        = 137000000
	rNodeFreqMax        = 3000000000
	rNodeBandwidthMin   = 7800
	rNodeBandwidthMax   = 1625000
	rNodeTXPowerMin     = 0
	rNodeTXPowerMax     = 37
	rNodeSFMin          = 5
	rNodeSFMax          = 12
	rNodeCRMin          = 5
	rNodeCRMax          = 8
	rNodeCallsignMaxLen = 32
)

// RNodeInterface wraps a serial connection to an RNode LoRa (Long Range) radio
// modem.
// It uses KISS (Keep It Simple, Stupid) framing and validates radio settings
// such as frequency, bandwidth, spreading factor, and coding rate.
type RNodeInterface struct {
	inner Interface

	hash     []byte
	hashOnce sync.Once
}

// NewRNodeInterface validates hardware bounds and initializes a physical RNode radio
// via a serial interface. It verifies physical-layer constraints—frequency,
// bandwidth, spreading factor, and coding rate—before delegating control to the
// serial controller.
func NewRNodeInterface(name, port string, speed, databits, stopbits int, parity string, frequency, bandwidth, txpower, spreadingFactor, codingRate int, flowControl bool, idInterval int, idCallsign string, handler InboundHandler) (Interface, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("no port specified for RNode interface")
	}
	if frequency < rNodeFreqMin || frequency > rNodeFreqMax {
		return nil, fmt.Errorf("invalid frequency configured for RNode interface")
	}
	if bandwidth < rNodeBandwidthMin || bandwidth > rNodeBandwidthMax {
		return nil, fmt.Errorf("invalid bandwidth configured for RNode interface")
	}
	if txpower < rNodeTXPowerMin || txpower > rNodeTXPowerMax {
		return nil, fmt.Errorf("invalid txpower configured for RNode interface")
	}
	if spreadingFactor < rNodeSFMin || spreadingFactor > rNodeSFMax {
		return nil, fmt.Errorf("invalid spreading factor configured for RNode interface")
	}
	if codingRate < rNodeCRMin || codingRate > rNodeCRMax {
		return nil, fmt.Errorf("invalid coding rate configured for RNode interface")
	}

	if idInterval > 0 || strings.TrimSpace(idCallsign) != "" {
		if idInterval <= 0 || strings.TrimSpace(idCallsign) == "" {
			return nil, fmt.Errorf("id_interval and id_callsign must both be set for RNode interface")
		}
		if len([]byte(idCallsign)) > rNodeCallsignMaxLen {
			return nil, fmt.Errorf("id_callsign exceeds max length for RNode interface")
		}
	}

	if speed <= 0 {
		speed = RNodeDefaultSpeed
	}
	if databits <= 0 {
		databits = RNodeDefaultDataBits
	}
	if stopbits <= 0 {
		stopbits = RNodeDefaultStopBits
	}
	if strings.TrimSpace(parity) == "" {
		parity = RNodeDefaultParity
	}

	iface, err := NewKISSInterface(name, port, speed, databits, stopbits, parity, handler)
	if err != nil {
		return nil, err
	}

	// if flowControl {
	// Flow control behavior is handled by underlying device firmware;
	// this Go parity slice validates and preserves configuration surface.
	// }

	return &RNodeInterface{inner: iface}, nil
}

// Name returns the configured interface name.
func (r *RNodeInterface) Name() string { return r.inner.Name() }

// Type identifies this interface as an RNode radio interface.
func (r *RNodeInterface) Type() string { return "RNodeInterface" }

// Mode returns the current operating mode reported by the wrapped interface.
func (r *RNodeInterface) Mode() int { return r.inner.Mode() }

// Bitrate returns the bitrate reported by the wrapped interface.
func (r *RNodeInterface) Bitrate() int { return r.inner.Bitrate() }

// IsOut reports whether the wrapped interface can originate outbound traffic.
func (r *RNodeInterface) IsOut() bool { return r.inner.IsOut() }

// Status reports whether the wrapped interface is currently active.
func (r *RNodeInterface) Status() bool { return r.inner.Status() }

// Send forwards the payload to the wrapped interface.
func (r *RNodeInterface) Send(data []byte) error { return r.inner.Send(data) }

// BytesReceived returns the total bytes received by the wrapped interface.
func (r *RNodeInterface) BytesReceived() uint64 { return r.inner.BytesReceived() }

// BytesSent returns the total bytes sent by the wrapped interface.
func (r *RNodeInterface) BytesSent() uint64 { return r.inner.BytesSent() }

// Detach detaches the wrapped interface.
func (r *RNodeInterface) Detach() error { return r.inner.Detach() }

// IsDetached reports whether the wrapped interface has been detached.
func (r *RNodeInterface) IsDetached() bool { return r.inner.IsDetached() }

// Age returns how long the wrapped interface has existed.
func (r *RNodeInterface) Age() time.Duration { return r.inner.Age() }

// Gravity reports the wrapped interface's gravity (RNS v1.4.1).
func (r *RNodeInterface) Gravity() int { return r.inner.Gravity() }

// RecursivePrs reports the wrapped interface's recursive-PR policy.
func (r *RNodeInterface) RecursivePrs() bool { return r.inner.RecursivePrs() }

// AnnouncesFromInternal reports the wrapped interface's internal-announce policy.
func (r *RNodeInterface) AnnouncesFromInternal() bool { return r.inner.AnnouncesFromInternal() }

// AnnouncesToInternal reports the wrapped interface's boundary→internal policy.
func (r *RNodeInterface) AnnouncesToInternal() *bool { return r.inner.AnnouncesToInternal() }

// Ingress/egress-control delegation to the wrapped interface (v1.1.5).
func (r *RNodeInterface) ReceivedAnnounce()        { r.inner.ReceivedAnnounce() }
func (r *RNodeInterface) ShouldIngressLimit() bool { return r.inner.ShouldIngressLimit() }
func (r *RNodeInterface) HoldAnnounce(raw []byte, recv Interface, hops int, destHash []byte) {
	r.inner.HoldAnnounce(raw, recv, hops, destHash)
}
func (r *RNodeInterface) ProcessHeldAnnounces() ([]byte, Interface, bool) {
	return r.inner.ProcessHeldAnnounces()
}
func (r *RNodeInterface) HeldAnnounces() int           { return r.inner.HeldAnnounces() }
func (r *RNodeInterface) ReceivedPathRequest()         { r.inner.ReceivedPathRequest() }
func (r *RNodeInterface) SentPathRequest()             { r.inner.SentPathRequest() }
func (r *RNodeInterface) IncomingPrFrequency() float64 { return r.inner.IncomingPrFrequency() }
func (r *RNodeInterface) OutgoingPrFrequency() float64 { return r.inner.OutgoingPrFrequency() }
func (r *RNodeInterface) ShouldIngressLimitPr() bool   { return r.inner.ShouldIngressLimitPr() }
func (r *RNodeInterface) ShouldEgressLimitPr() bool    { return r.inner.ShouldEgressLimitPr() }

// Announce-frequency + ingress-control burst-state delegation
// (Interface.py:121-124,277-297). ifstats reads these via the concrete
// interface (Reticulum.py:1453-1464).
func (r *RNodeInterface) IncomingAnnounceFrequency() float64 {
	return r.inner.IncomingAnnounceFrequency()
}
func (r *RNodeInterface) OutgoingAnnounceFrequency() float64 {
	return r.inner.OutgoingAnnounceFrequency()
}
func (r *RNodeInterface) ICBurstActive() bool           { return r.inner.ICBurstActive() }
func (r *RNodeInterface) ICBurstActivated() time.Time   { return r.inner.ICBurstActivated() }
func (r *RNodeInterface) ICPrBurstActive() bool         { return r.inner.ICPrBurstActive() }
func (r *RNodeInterface) ICPrBurstActivated() time.Time { return r.inner.ICPrBurstActivated() }

// Announce-rate-control delegation (Interface.py:90-92,118-120).
func (r *RNodeInterface) AnnounceRateTarget() *int  { return r.inner.AnnounceRateTarget() }
func (r *RNodeInterface) AnnounceRateGrace() *int   { return r.inner.AnnounceRateGrace() }
func (r *RNodeInterface) AnnounceRatePenalty() *int { return r.inner.AnnounceRatePenalty() }

// DefaultIFACSize returns the wrapped interface's DEFAULT_IFAC_SIZE. RNode's own
// DEFAULT_IFAC_SIZE (8) matches the wrapped KISS interface's, so delegation is
// exact (RNS/Interfaces/RNodeInterface.py:80).
func (r *RNodeInterface) DefaultIFACSize() int {
	if d, ok := r.inner.(interface{ DefaultIFACSize() int }); ok {
		return d.DefaultIFACSize()
	}
	return 8
}

// MemoizedHash returns the memoized identity hash, computing it via compute on
// the first call and caching the result. Mirrors Python Interface.get_hash
// (RNS/Interfaces/Interface.py:144-146) so the wrapper's own string identity
// ("RNodeInterface[<name>]") is hashed at most once.
func (r *RNodeInterface) MemoizedHash(compute func() []byte) []byte {
	r.hashOnce.Do(func() {
		if r.hash == nil && compute != nil {
			r.hash = compute()
		}
	})
	return r.hash
}

// SetBitrate propagates a bitrate override to the wrapped interface when it
// supports that operation.
func (r *RNodeInterface) SetBitrate(bitrate int) {
	if setter, ok := r.inner.(interface{ SetBitrate(int) }); ok {
		setter.SetBitrate(bitrate)
	}
}

// SetMode propagates an interface mode override to the wrapped interface when
// supported.
func (r *RNodeInterface) SetMode(mode int) {
	if setter, ok := r.inner.(interface{ SetMode(int) }); ok {
		setter.SetMode(mode)
	}
}

// SetIFACConfig applies Interface Authentication Codes (IFAC) configuration to
// the wrapped interface when supported.
func (r *RNodeInterface) SetIFACConfig(cfg IFACConfig) {
	if setter, ok := r.inner.(interface{ SetIFACConfig(IFACConfig) }); ok {
		setter.SetIFACConfig(cfg)
	}
}

// SetDiscoveryConfig applies interface discovery metadata to the wrapped
// interface when supported.
func (r *RNodeInterface) SetDiscoveryConfig(cfg DiscoveryConfig) {
	if setter, ok := r.inner.(interface{ SetDiscoveryConfig(DiscoveryConfig) }); ok {
		setter.SetDiscoveryConfig(cfg)
	}
}

// DiscoveryConfig returns the wrapped interface discovery metadata when
// available.
func (r *RNodeInterface) DiscoveryConfig() DiscoveryConfig {
	if getter, ok := r.inner.(interface{ DiscoveryConfig() DiscoveryConfig }); ok {
		return getter.DiscoveryConfig()
	}
	return DiscoveryConfig{}
}
