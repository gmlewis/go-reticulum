// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package lxmf implements the Lightweight Extensible Message Format (LXMF)
// for the Go Reticulum ecosystem.
//
// LXMF is an application-layer messaging format and routing protocol built on
// top of Reticulum. It enables compact, signed, end-to-end encrypted message
// exchange across heterogeneous and low-bandwidth transports while preserving
// interoperability with existing Python LXMF implementations.
//
// At a high level, an LXMF message contains:
//
//   - destination hash (Reticulum truncated destination hash),
//   - source hash,
//   - Ed25519 signature,
//   - msgpack payload with timestamp, title, content and fields,
//   - optional anti-spam stamp and optional ticket metadata.
//
// This package is designed to support the same practical usage model as Python
// LXMF clients and services:
//
//   - endpoint-to-endpoint messaging over Reticulum links,
//   - opportunistic single-packet messaging when payload size allows,
//   - propagated store-and-forward delivery through propagation nodes,
//   - paper-message workflows via lxm:// URI payload exchange.
//
// Relationship to the rest of this repository:
//
//   - The rns package provides transport, identity, destination, packet,
//     request and resource primitives.
//   - The lxmf package layers message semantics, stamping/ticket policies,
//     routing behavior and interoperability helpers on top of rns.
//
// In practice, higher-level applications (for example NomadNet-style clients)
// use lxmf as the messaging substrate while relying on rns for underlying
// networking and cryptography.
//
// This package uses only the Go standard library plus internal repository packages.
package lxmf
