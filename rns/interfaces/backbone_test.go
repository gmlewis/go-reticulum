// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
	"time"
)

// newTestBackboneWithSpawned builds a BackboneInterface wrapping a
// TCPServerInterface whose spawnedInterfaces list is the given peers, without
// binding a real socket. The aggregate burst getters reduce over this list.
func newTestBackboneWithSpawned(spawned ...*TCPClientInterface) *BackboneInterface {
	bi := NewBaseInterface("test-backbone", ModeFull, TCPBitrateGuess)
	tsi := &TCPServerInterface{BaseInterface: bi}
	tsi.spawnedInterfaces = append(tsi.spawnedInterfaces, spawned...)
	return &BackboneInterface{TCPServerInterface: tsi}
}

// newSpawnedPeer builds a TCPClientInterface carrying the given per-peer burst
// state, so the BackboneInterface aggregate getters can reduce over it.
func newSpawnedPeer(name string, burstActive bool, burstActivated time.Time, prActive bool, prActivated time.Time) *TCPClientInterface {
	bi := NewBaseInterface(name, ModeFull, TCPBitrateGuess)
	bi.icBurstActive = burstActive
	bi.icBurstActivated = burstActivated
	bi.icPrBurstActive = prActive
	bi.icPrBurstActivated = prActivated
	return &TCPClientInterface{BaseInterface: bi}
}

// TestBackboneAggregateBurstState verifies the cached (2s TTL) aggregate
// ic_burst_active / ic_burst_activated / ic_pr_burst_active / ic_pr_burst_activated
// getters on BackboneInterface (BackboneInterface.py:173-225): ic_burst_active
// is any(spawned.ic_burst_active); ic_burst_activated is the min activation
// time over the burst-active spawned peers (zero if none); and the PR pair is
// identical. Results are cached for 2s.
func TestBackboneAggregateBurstState(t *testing.T) {
	t.Parallel()
	now := time.Unix(5_000_000, 0)
	early := now.Add(-30 * time.Second)
	late := now.Add(-10 * time.Second)

	t.Run("any active and min activated over active peers", func(t *testing.T) {
		t.Parallel()
		b := newTestBackboneWithSpawned(
			newSpawnedPeer("a", true, early, false, time.Time{}),
			newSpawnedPeer("b", true, late, true, early),
			newSpawnedPeer("c", false, time.Time{}, false, time.Time{}),
		)
		if !b.icBurstActiveAt(now) {
			t.Fatal("icBurstActiveAt = false, want true (any peer active)")
		}
		if got := b.icBurstActivatedAt(now); !got.Equal(early) {
			t.Fatalf("icBurstActivatedAt = %v, want %v (min over active)", got, early)
		}
		if !b.icPrBurstActiveAt(now) {
			t.Fatal("icPrBurstActiveAt = false, want true (peer b pr-active)")
		}
		if got := b.icPrBurstActivatedAt(now); !got.Equal(early) {
			t.Fatalf("icPrBurstActivatedAt = %v, want %v", got, early)
		}
	})

	t.Run("all inactive yields false and zero activated", func(t *testing.T) {
		t.Parallel()
		b := newTestBackboneWithSpawned(
			newSpawnedPeer("a", false, early, false, late),
			newSpawnedPeer("b", false, late, false, early),
		)
		if b.icBurstActiveAt(now) {
			t.Fatal("icBurstActiveAt = true, want false (no active peers)")
		}
		if got := b.icBurstActivatedAt(now); !got.IsZero() {
			t.Fatalf("icBurstActivatedAt = %v, want zero (no active peers)", got)
		}
		if b.icPrBurstActiveAt(now) {
			t.Fatal("icPrBurstActiveAt = true, want false")
		}
		if got := b.icPrBurstActivatedAt(now); !got.IsZero() {
			t.Fatalf("icPrBurstActivatedAt = %v, want zero", got)
		}
	})

	t.Run("results cached for 2 seconds", func(t *testing.T) {
		t.Parallel()
		b := newTestBackboneWithSpawned(
			newSpawnedPeer("a", true, early, true, early),
		)
		// First call at t0 recomputes (cache was empty) and caches until t0+2s.
		if !b.icBurstActiveAt(now) {
			t.Fatal("first call: expected true")
		}
		// Mutate underlying state, then call within the cache window: the
		// cached value (true) must be returned, NOT the new state (false).
		b.spawnedInterfaces[0].icBurstActive = false
		if !b.icBurstActiveAt(now.Add(1 * time.Second)) {
			t.Fatal("within 2s cache window: expected cached true, got false (recomputed)")
		}
		// After the 2s TTL elapses the aggregate recomputes from current state.
		if b.icBurstActiveAt(now.Add(2500 * time.Millisecond)) {
			t.Fatal("after 2s TTL: expected false (recomputed from mutated state), got true")
		}
	})
}

func TestBackboneInterfaceRoundTrip(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)
	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	serverIface := mustTestNewBackboneInterface(t, "backbone-server", "127.0.0.1", port, handler)
	defer func() {
		if err := serverIface.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	if serverIface.Type() != "BackboneInterface" {
		t.Fatalf("server type = %q, want BackboneInterface", serverIface.Type())
	}

	clientIface := mustTestNewBackboneClientInterface(t, "backbone-client", "127.0.0.1", port, nil)
	defer func() {
		if err := clientIface.Detach(); err != nil {
			t.Fatalf("client detach failed: %v", err)
		}
	}()

	if clientIface.Type() != "BackboneClientInterface" {
		t.Fatalf("client type = %q, want BackboneClientInterface", clientIface.Type())
	}

	time.Sleep(100 * time.Millisecond)

	// Payload must exceed HDLCHeaderMinSize (19): the readLoop's check_frame_len
	// gate (v1.4.0 parity) drops sub-header frames, so a real-length payload is
	// required for a round-trip delivery test.
	msg := []byte("hello backbone roundtrip!!")
	if err := clientIface.Send(msg); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Fatalf("received data mismatch: expected %q, got %q", string(msg), string(data))
		}
	case <-time.After(800 * time.Millisecond):
		t.Fatal("timed out waiting for backbone data")
	}
}
