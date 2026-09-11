// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file verifies the stub filter host: without the wago build tag the
// host is permanently inactive, every filter call errors, and the delivery
// wrapper falls through to the normal (unfiltered) delivery behavior.

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestFilterHostStubInactive verifies that the stub build never activates a
// filter host and never accepts filter loads.
func TestFilterHostStubInactive(t *testing.T) {
	t.Parallel()

	h := NewFilterHost(50*time.Millisecond, nil)
	if h == nil {
		t.Fatal("NewFilterHost returned nil")
	}
	if h.Active() {
		t.Fatal("stub FilterHost reports Active, want inactive")
	}

	wasmPath := filepath.Join(testutils.TempDir(t, "golxmd-filter"), "filter.wasm")
	if err := os.WriteFile(wasmPath, MinWasmFilterAccept, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := h.LoadPlugin(wasmPath); err == nil {
		t.Fatal("stub LoadPlugin succeeded, want an error")
	}
	if h.Active() {
		t.Fatal("stub FilterHost became Active after LoadPlugin")
	}

	ok, err := h.HandleFilter([]byte(`{"content":"hi"}`))
	if err == nil {
		t.Fatalf("stub HandleFilter returned (%v, nil), want an error", ok)
	}
	if ok {
		t.Error("stub HandleFilter accepted a message, want false")
	}

	h.Close()
	if h.Active() {
		t.Fatal("stub FilterHost reports Active after Close")
	}
}

// TestFilteredDeliveryWithoutHosts verifies that the delivery wrapper calls
// the base handler unchanged when no filter host is active.
func TestFilteredDeliveryWithoutHosts(t *testing.T) {
	t.Parallel()

	var delivered []*lxmf.Message
	base := func(lxm *lxmf.Message) { delivered = append(delivered, lxm) }

	idle := NewFilterHost(time.Second, nil)
	msg := &lxmf.Message{Content: []byte("hello")}
	filteredDelivery(base, []*FilterHost{idle}, msg, nil)
	if len(delivered) != 1 || delivered[0] != msg {
		t.Fatalf("filteredDelivery delivered %v message(s), want the original one", len(delivered))
	}

	// An empty host list behaves the same.
	filteredDelivery(base, nil, msg, nil)
	if len(delivered) != 2 {
		t.Fatalf("filteredDelivery with no hosts delivered %v message(s), want 2 total", len(delivered))
	}
}

// TestMessageToFilterJSON verifies the serialized filter payload carries the
// owned leaf fields a sandboxed plugin may inspect.
func TestMessageToFilterJSON(t *testing.T) {
	t.Parallel()

	msg := &lxmf.Message{
		DestinationHash: []byte{0xaa, 0xbb},
		SourceHash:      []byte{0xcc, 0xdd},
		Title:           []byte("note"),
		Content:         []byte("hello world"),
		Timestamp:       1730000000,
	}
	var payload map[string]any
	if err := json.Unmarshal(messageToFilterJSON(msg), &payload); err != nil {
		t.Fatalf("messageToFilterJSON does not decode: %v", err)
	}
	if payload["destination_hash"] != "aabb" {
		t.Errorf("destination_hash = %v, want aabb", payload["destination_hash"])
	}
	if payload["source_hash"] != "ccdd" {
		t.Errorf("source_hash = %v, want ccdd", payload["source_hash"])
	}
	if payload["title"] != "note" {
		t.Errorf("title = %v, want note", payload["title"])
	}
	if payload["content"] != "hello world" {
		t.Errorf("content = %v, want %q", payload["content"], "hello world")
	}
	if payload["timestamp"] != float64(1730000000) {
		t.Errorf("timestamp = %v, want 1730000000", payload["timestamp"])
	}
}

// TestErrFiltersNotLinked documents the stub-build error sentinel.
func TestErrFiltersNotLinked(t *testing.T) {
	t.Parallel()

	if errFiltersNotLinked == nil {
		t.Fatal("errFiltersNotLinked is nil, want a sentinel error")
	}
	if !errors.Is(errFiltersNotLinked, errFiltersNotLinked) {
		t.Fatal("errFiltersNotLinked does not match itself")
	}
}
