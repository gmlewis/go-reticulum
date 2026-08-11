// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package interfaces formalizes the robust, polymorphic communication abstractions central to the Reticulum Network Stack.
//
// This package dictates the strict operational contracts required to integrate diverse physical and virtual media—ranging from UDP/TCP links to raw Serial and LoRa radios—into a unified, hardware-agnostic routing fabric. It provides the foundational boilerplate, cryptographic boundary enforcements, and lifecycle managers that all concrete interface implementations must inherit and honor.
package interfaces

import (
	"time"
)

// Interface modes dictate the architectural role and forwarding behavior an interface adopts within the broader network topology.
const (
	// ModeFull indicates the interface is a fully participating, bidirectional routing nexus.
	ModeFull = 0x01
	// ModePointToPoint signifies a direct, unshared link between exactly two peers, optimizing broadcast behaviors.
	ModePointToPoint = 0x02
	// ModeAccessPoint designates the interface as a hub serving multiple downstream, potentially transient, client nodes.
	ModeAccessPoint = 0x03
	// ModeRoaming specifies that the interface is highly mobile and expects its underlying network attachment to change frequently.
	ModeRoaming = 0x04
	// ModeBoundary establishes a strict routing partition, carefully filtering traffic bridging disparate network segments.
	ModeBoundary = 0x05
	// ModeGateway acts as an egress or ingress gateway, actively brokering traffic into external, potentially non-Reticulum networks.
	ModeGateway = 0x06
	// ModeInternal marks an interface as internal to the local node; transport
	// nodes discover paths for it (added at RNS v1.3.7) and it may host
	// discoverable services without being reconfigured into another mode.
	ModeInternal = 0x07
)

// DiscoverPathsFor lists the interface modes for which a transport node
// actively discovers paths. It mirrors RNS 1.4.2's
// Interface.DISCOVER_PATHS_FOR, which gained MODE_INTERNAL at v1.3.7.
var DiscoverPathsFor = []int{
	ModeAccessPoint,
	ModeGateway,
	ModeRoaming,
	ModeInternal,
}

// Interface strictly defines the operational contract that all Reticulum physical and virtual transport mechanisms must fulfill.
// It enforces uniform lifecycle management, capability introspection, and asynchronous payload delivery, allowing the routing core to remain entirely hardware-agnostic.
type Interface interface {
	// Name returns the configured interface name.
	Name() string
	// Type returns the implementation type name used for diagnostics and config
	// matching.
	Type() string
	// Status reports whether the interface is currently online and usable.
	Status() bool
	// IsOut reports whether the interface can originate outbound traffic.
	IsOut() bool
	// Mode returns the interface's operating mode.
	Mode() int
	// Bitrate returns the interface bitrate estimate.
	Bitrate() int

	// Send transmits a payload through the interface.
	Send(data []byte) error

	// Stats
	// BytesReceived returns the cumulative number of received bytes.
	BytesReceived() uint64
	// BytesSent returns the cumulative number of transmitted bytes.
	BytesSent() uint64

	// Lifecycle
	// Detach stops the interface and releases its resources.
	Detach() error
	// IsDetached reports whether Detach has already been called successfully.
	IsDetached() bool
	// Age returns how long the interface has existed.
	Age() time.Duration

	// Phase 1 contract accessors (RNS v1.3.7/1.4.1). These expose per-interface
	// routing policy that the transport consults when handling announces and
	// path requests.
	// Gravity reports the interface gravity used for weighted path selection.
	Gravity() int
	// RecursivePrs reports whether recursive path requests egress on this
	// interface regardless of its mode.
	RecursivePrs() bool
	// AnnouncesFromInternal reports whether announces whose next-hop interface
	// is MODE_INTERNAL are accepted. Defaults to true.
	AnnouncesFromInternal() bool
	// AnnouncesToInternal reports the boundary→internal allowance policy. A
	// nil pointer means the interface does not override the default block.
	AnnouncesToInternal() *bool
}
