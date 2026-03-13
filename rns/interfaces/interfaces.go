// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package interfaces defines the communication interface abstractions
// for the Reticulum Network Stack.
//
// An Interface represents a physical or virtual communication medium,
// such as UDP, TCP, Serial, or LoRa. This package provides the base
// types and definitions required to implement new interface types
// that can be used by the RNS Transport system.
package interfaces

import (
	"time"
)

// Interface modes
const (
	ModeFull         = 0x01
	ModePointToPoint = 0x02
	ModeAccessPoint  = 0x03
	ModeRoaming      = 0x04
	ModeBoundary     = 0x05
	ModeGateway      = 0x06
)

// Interface represents a physical or virtual communication medium.
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
