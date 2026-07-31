// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"testing"
)

func TestRNodeReadLoopCommandStateAccumulation(t *testing.T) {
	t.Parallel()

	state := newRnodeReadLoopState()
	stream := []byte{
		kissFend, rnodeKISSCommandFrequency, 0x12, 0x34, 0x56, 0x78, kissFend,
		kissFend, rnodeKISSCommandBandwidth, 0x00, 0x01, 0x86, 0xa0, kissFend,
		kissFend, rnodeKISSCommandPlatform, 0x70, kissFend,
		kissFend, rnodeKISSCommandFWVersion, 0x02, 0x05, kissFend,
		kissFend, rnodeKISSCommandDevHash,
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
		0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14,
		0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c,
		0x1d, 0x1e, 0x1f, 0x20,
		kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x01,
		0xa1, 0xa2, 0xa3, 0xa4,
		0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xab, 0xac,
		0xad, 0xae, 0xaf, 0xb0,
		0xb1, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8,
		0xb9, 0xba, 0xbb, 0xbc,
		0xbd, 0xbe, 0xbf, kissFesc, kissTfend,
		kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x02,
		0xc1, 0xc2, 0xc3, 0xc4,
		0xc5, 0xc6, 0xc7, 0xc8,
		0xc9, 0xca, 0xcb, 0xcc,
		0xcd, 0xce, 0xcf, 0xd0,
		0xd1, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8,
		0xd9, 0xda, kissFesc, kissTfesc,
		0xdc, 0xdd, 0xde, 0xdf, 0xe0,
		kissFend,
	}

	for _, b := range stream {
		state.feedByte(b)
	}

	if state.rFrequency != 0x12345678 {
		t.Fatalf("frequency mismatch: got %#x want %#x", state.rFrequency, 0x12345678)
	}
	if state.rBandwidth != 0x000186a0 {
		t.Fatalf("bandwidth mismatch: got %#x want %#x", state.rBandwidth, 0x000186a0)
	}
	if state.majorVersion != 0x02 || state.minorVersion != 0x05 {
		t.Fatalf("firmware version mismatch: got %v.%v want %v.%v", state.majorVersion, state.minorVersion, 0x02, 0x05)
	}
	if state.platform != 0x70 {
		t.Fatalf("platform mismatch: got %#x want %#x", state.platform, 0x70)
	}
	wantHash := []byte{
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
		0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14,
		0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c,
		0x1d, 0x1e, 0x1f, 0x20,
	}
	if !bytes.Equal(state.deviceHash, wantHash) {
		t.Fatalf("device hash mismatch:\n got: %x\nwant: %x", state.deviceHash, wantHash)
	}
	wantTargetHash := []byte{
		0xa1, 0xa2, 0xa3, 0xa4,
		0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xab, 0xac,
		0xad, 0xae, 0xaf, 0xb0,
		0xb1, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8,
		0xb9, 0xba, 0xbb, 0xbc,
		0xbd, 0xbe, 0xbf, 0xc0,
	}
	if !bytes.Equal(state.firmwareHashTarget, wantTargetHash) {
		t.Fatalf("firmware target hash mismatch:\n got: %x\nwant: %x", state.firmwareHashTarget, wantTargetHash)
	}
	wantFirmwareHash := []byte{
		0xc1, 0xc2, 0xc3, 0xc4,
		0xc5, 0xc6, 0xc7, 0xc8,
		0xc9, 0xca, 0xcb, 0xcc,
		0xcd, 0xce, 0xcf, 0xd0,
		0xd1, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8,
		0xd9, 0xda, 0xdb, 0xdc,
		0xdd, 0xde, 0xdf, 0xe0,
	}
	if !bytes.Equal(state.firmwareHash, wantFirmwareHash) {
		t.Fatalf("firmware hash mismatch:\n got: %x\nwant: %x", state.firmwareHash, wantFirmwareHash)
	}
}

func TestRNodeReadLoopIdleTimeoutUsesStrictGreaterThan(t *testing.T) {
	t.Parallel()

	state := newRnodeReadLoopState()
	state.inFrame = true
	state.escape = true
	state.command = rnodeKISSCommandData
	state.dataBuffer = []byte{0x01, 0x02}
	state.commandBuffer = []byte{0x0a, 0x0b}

	if state.idleTimeoutExpired(150, 100, 50) {
		t.Fatalf("did not expect timeout reset when elapsed equals timeout")
	}
	if len(state.dataBuffer) != 2 || !state.inFrame || !state.escape || state.command != rnodeKISSCommandData {
		t.Fatalf("state changed unexpectedly on exact timeout boundary: %#v", state)
	}

	if !state.idleTimeoutExpired(151, 100, 50) {
		t.Fatalf("expected timeout reset when elapsed exceeds timeout")
	}
	if state.inFrame || state.escape || state.command != rnodeKISSCommandUnknown || len(state.dataBuffer) != 0 {
		t.Fatalf("timeout reset did not clear payload state: %#v", state)
	}
	if !bytes.Equal(state.commandBuffer, []byte{0x0a, 0x0b}) {
		t.Fatalf("timeout reset should not clear command buffer: %x", state.commandBuffer)
	}
}

func TestRNodeReadLoopShutdownCleanupResetsState(t *testing.T) {
	t.Parallel()

	state := newRnodeReadLoopState()
	state.inFrame = true
	state.escape = true
	state.command = rnodeKISSCommandFrequency
	state.dataBuffer = []byte{0x01, 0x02}
	state.commandBuffer = []byte{0x12, 0x34, 0x56, 0x78}

	state.shutdownCleanup()

	if state.inFrame || state.escape || state.command != rnodeKISSCommandUnknown {
		t.Fatalf("shutdown cleanup did not reset frame state: %#v", state)
	}
	if len(state.dataBuffer) != 0 || len(state.commandBuffer) != 0 {
		t.Fatalf("shutdown cleanup did not clear buffers: %#v", state)
	}
}
