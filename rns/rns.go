// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package rns implements the Reticulum Network Stack (RNS).
//
// Reticulum is the cryptography-based networking stack for building
// wide-area networks with readily available hardware. Reticulum can
// operate even with very low bandwidth and high latency.
//
// The core of RNS is based on several fundamental components:
//   - Identity: Handles public/private key pairs for encryption and signing.
//   - Destination: Represents addressable endpoints in the network.
//   - Packet: The basic unit of communication, handling routing and encryption.
//   - Link: Manages end-to-end encrypted sessions between peers.
//   - Transport: Handles routing, packet forwarding, and interface management.
//
// Reticulum uses modern cryptographic primitives (X25519, Ed25519, AES-CBC,
// and HKDF) to ensure all communication is secure and private.
package rns
