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
	ModeFull         = 0x01
	// ModePointToPoint signifies a direct, unshared link between exactly two peers, optimizing broadcast behaviors.
	ModePointToPoint = 0x02
	// ModeAccessPoint designates the interface as a hub serving multiple downstream, potentially transient, client nodes.
	ModeAccessPoint  = 0x03
	// ModeRoaming specifies that the interface is highly mobile and expects its underlying network attachment to change frequently.
	ModeRoaming      = 0x04
	// ModeBoundary establishes a strict routing partition, carefully filtering traffic bridging disparate network segments.
	ModeBoundary     = 0x05
	// ModeGateway acts as an egress or ingress gateway, actively brokering traffic into external, potentially non-Reticulum networks.
	ModeGateway      = 0x06
)

// Interface strictly defines the operational contract that all Reticulum physical and virtual transport mechanisms must fulfill.
// It enforces uniform lifecycle management, capability introspection, and asynchronous payload delivery, allowing the routing core to remain entirely hardware-agnostic.
type Interface interface {
	Name() string
	Type() string
	Status() bool
	IsOut() bool
	Mode() int
	Bitrate() int

	Send(data []byte) error

	// Stats
	BytesReceived() uint64
	BytesSent() uint64

	// Lifecycle
	Detach() error
	IsDetached() bool
	Age() time.Duration
}
