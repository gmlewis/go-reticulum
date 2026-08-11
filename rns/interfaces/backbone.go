// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"sync"
	"time"
)

// BackboneDefaultIFACSize is the DEFAULT_IFAC_SIZE for Backbone server and
// client interfaces (RNS/Interfaces/BackboneInterface.py:54,569).
const BackboneDefaultIFACSize = 16

// BackboneHWMTU is the hardware MTU ceiling for Backbone interfaces
// (RNS/Interfaces/BackboneInterface.py HW_MTU = 1048576). Backbone links
// carry aggregated traffic and permit larger frames than a plain TCP
// interface (TCPHWMTU = 262144); the inbound HDLC frame-length gate uses this
// value so Backbone does not reject frames Python would accept.
const BackboneHWMTU = 1048576

// BackboneInterface provides a robust, highly available TCP listener used as a
// core routing nexus. It encapsulates TCP server logic and accepts point-to-
// point links from downstream clients.
type BackboneInterface struct {
	*TCPServerInterface

	// Aggregate burst-state cache (BackboneInterface.py:173-225). Each
	// aggregate getter recomputes over the spawned peers at most once per
	// 2s window; aggMu guards the cache fields.
	aggMu                       sync.Mutex
	lastICBurstCheck            time.Time
	lastICBurstState            bool
	lastICBurstActivatedCheck   time.Time
	lastICBurstActivated        time.Time
	lastICPrBurstCheck          time.Time
	lastICPrBurstState          bool
	lastICPrBurstActivatedCheck time.Time
	lastICPrBurstActivated      time.Time
}

// NewBackboneInterface binds and initializes a TCP-based BackboneInterface on the
// given address and port. It creates a persistent listener and dispatches
// incoming frames to router logic. Spawned clients inherit BackboneHWMTU for
// inbound HDLC frame-length validation.
func NewBackboneInterface(name, bindIP string, bindPort int, handler InboundHandler, onConnect ConnectHandler) (Interface, error) {
	inner, err := newTCPServerInterface(name, bindIP, bindPort, handler, onConnect, BackboneHWMTU)
	if err != nil {
		return nil, err
	}
	return &BackboneInterface{TCPServerInterface: inner}, nil
}

// Type returns the string "BackboneInterface" as the runtime type name.
func (b *BackboneInterface) Type() string { return "BackboneInterface" }

// BackboneInterface reduces the per-spawned-peer ingress-control burst state
// into aggregate cached properties (BackboneInterface.py:173-225), so the
// Backbone server reports a burst as active when ANY spawned client is in a
// burst, and the activation time as the EARLIEST (min) activation among the
// burst-active spawned clients. Each aggregate is cached for 2 seconds to
// avoid scanning the spawned list on every read (the same TTL Python uses).

const backboneAggregateCacheTTL = 2 * time.Second

// icBurstActiveAt is the time-injectable core of ICBurstActive. It recomputes
// the any-reduction over the spawned peers when the cache is older than 2s,
// otherwise returns the cached state (BackboneInterface.py:174-180).
func (b *BackboneInterface) icBurstActiveAt(now time.Time) bool {
	if b == nil || b.TCPServerInterface == nil {
		return false
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICBurstCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICBurstCheck = now
		b.lastICBurstState = false
		for _, peer := range b.snapshotSpawned() {
			if peer.ICBurstActive() {
				b.lastICBurstState = true
				break
			}
		}
	}
	return b.lastICBurstState
}

// ICBurstActive reports whether any spawned client is currently in an
// announce-burst (BackboneInterface.py:174-180).
func (b *BackboneInterface) ICBurstActive() bool {
	return b.icBurstActiveAt(time.Now())
}

// icBurstActivatedAt is the time-injectable core of ICBurstActivated. It
// recomputes the min activation time over the burst-active spawned peers when
// the cache is older than 2s, otherwise returns the cached value
// (BackboneInterface.py:186-194). With no burst-active peers the cached value
// stays at the zero time (Python's 0).
func (b *BackboneInterface) icBurstActivatedAt(now time.Time) time.Time {
	if b == nil || b.TCPServerInterface == nil {
		return time.Time{}
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICBurstActivatedCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICBurstActivatedCheck = now
		b.lastICBurstActivated = time.Time{}
		for _, peer := range b.snapshotSpawned() {
			if peer.ICBurstActive() {
				if b.lastICBurstActivated.IsZero() || peer.ICBurstActivated().Before(b.lastICBurstActivated) {
					b.lastICBurstActivated = peer.ICBurstActivated()
				}
			}
		}
	}
	return b.lastICBurstActivated
}

// ICBurstActivated reports the earliest activation time among the burst-active
// spawned clients (BackboneInterface.py:186-194).
func (b *BackboneInterface) ICBurstActivated() time.Time {
	return b.icBurstActivatedAt(time.Now())
}

// icPrBurstActiveAt is the time-injectable core of ICPrBurstActive, the
// path-request-burst any-reduction (BackboneInterface.py:202-208).
func (b *BackboneInterface) icPrBurstActiveAt(now time.Time) bool {
	if b == nil || b.TCPServerInterface == nil {
		return false
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICPrBurstCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICPrBurstCheck = now
		b.lastICPrBurstState = false
		for _, peer := range b.snapshotSpawned() {
			if peer.ICPrBurstActive() {
				b.lastICPrBurstState = true
				break
			}
		}
	}
	return b.lastICPrBurstState
}

// ICPrBurstActive reports whether any spawned client is currently in a
// path-request burst (BackboneInterface.py:202-208).
func (b *BackboneInterface) ICPrBurstActive() bool {
	return b.icPrBurstActiveAt(time.Now())
}

// icPrBurstActivatedAt is the time-injectable core of ICPrBurstActivated, the
// path-request-burst min activation time (BackboneInterface.py:214-222).
func (b *BackboneInterface) icPrBurstActivatedAt(now time.Time) time.Time {
	if b == nil || b.TCPServerInterface == nil {
		return time.Time{}
	}
	b.aggMu.Lock()
	defer b.aggMu.Unlock()
	if now.After(b.lastICPrBurstActivatedCheck.Add(backboneAggregateCacheTTL)) {
		b.lastICPrBurstActivatedCheck = now
		b.lastICPrBurstActivated = time.Time{}
		for _, peer := range b.snapshotSpawned() {
			if peer.ICPrBurstActive() {
				if b.lastICPrBurstActivated.IsZero() || peer.ICPrBurstActivated().Before(b.lastICPrBurstActivated) {
					b.lastICPrBurstActivated = peer.ICPrBurstActivated()
				}
			}
		}
	}
	return b.lastICPrBurstActivated
}

// ICPrBurstActivated reports the earliest activation time among the
// path-request-burst-active spawned clients (BackboneInterface.py:214-222).
func (b *BackboneInterface) ICPrBurstActivated() time.Time {
	return b.icPrBurstActivatedAt(time.Now())
}

// snapshotSpawned returns a copy of the spawned-interfaces list under the
// TCPServerInterface lock, so the aggregate reductions see a consistent view
// even as peers connect/disconnect.
func (b *BackboneInterface) snapshotSpawned() []*TCPClientInterface {
	if b == nil || b.TCPServerInterface == nil {
		return nil
	}
	b.TCPServerInterface.mu.Lock()
	defer b.TCPServerInterface.mu.Unlock()
	out := make([]*TCPClientInterface, len(b.spawnedInterfaces))
	copy(out, b.spawnedInterfaces)
	return out
}

// BackboneClientInterface represents an outbound TCP session that connects to
// a remote BackboneInterface listener, providing reliable point-to-point
// delivery to core network nodes.
type BackboneClientInterface struct {
	*TCPClientInterface
}

// NewBackboneClientInterface initiates a TCP connection to the target host and
// registers the inbound payload handler to process server-side data. The
// client uses BackboneHWMTU for inbound HDLC frame-length validation.
func NewBackboneClientInterface(name, targetHost string, targetPort int, handler InboundHandler) (Interface, error) {
	inner, err := newTCPClientInterface(name, targetHost, targetPort, false, handler, BackboneHWMTU)
	if err != nil {
		return nil, err
	}
	return &BackboneClientInterface{TCPClientInterface: inner}, nil
}

// NewDormantBackboneClientInterface returns an unconnected Backbone client used
// for discovery records that Python registers without an initial target.
func NewDormantBackboneClientInterface(name string, handler InboundHandler) Interface {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.setDefaultIFACSize(BackboneDefaultIFACSize)
	return &BackboneClientInterface{
		TCPClientInterface: &TCPClientInterface{
			BaseInterface:  bi,
			inboundHandler: handler,
			hwmtu:          BackboneHWMTU,
		},
	}
}

// Type returns the string "BackboneClientInterface" as the runtime type name.
func (b *BackboneClientInterface) Type() string { return "BackboneClientInterface" }
