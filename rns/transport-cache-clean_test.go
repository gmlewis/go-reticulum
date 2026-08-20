// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestCleanCacheNonBlockingLockPostponesOverlap verifies that
// cleanCache acquires its guard with a non-blocking lock, so a second
// invocation while a sweep is already in flight postpones (returns
// immediately) instead of running concurrently (Python Transport.py:2600-2615
// cache_clean_lock acquired with blocking=False).
func TestCleanCacheNonBlockingLockPostponesOverlap(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	tmpDir := testutils.TempDir(t, "rns-cache-clean-") // reserved; CleanCache touches no files here
	_ = tmpDir

	// Seed an expired entry so cleanAnnounceCache enters its per-entry loop.
	ts.mu.Lock()
	ts.packetHashes[string([]byte("expired"))] = time.Now().Add(-time.Hour)
	ts.mu.Unlock()

	// Block the first sweep inside its per-entry yield so it holds the
	// cache-clean lock while the second call is made.
	gate := make(chan struct{})
	enteredSleep := make(chan struct{}, 1)
	ts.cacheCleanSleep = func(time.Duration) {
		select {
		case enteredSleep <- struct{}{}:
		default:
		}
		<-gate
	}

	done1 := make(chan struct{})
	go func() {
		ts.CleanCache()
		close(done1)
	}()

	// Wait until the first sweep has acquired the lock and is mid-yield.
	<-enteredSleep

	// The second call must postpone (TryLock fails) and return immediately,
	// NOT block behind the first sweep.
	if !completesWithin(func() { ts.CleanCache() }, 100*time.Millisecond) {
		t.Fatal("second CleanCache blocked instead of postponing; non-blocking lock failed")
	}

	// Release the first sweep and confirm it completes.
	close(gate)
	if !completesWithin(func() { <-done1 }, 1*time.Second) {
		t.Fatal("first CleanCache did not complete after releasing the yield gate")
	}
}

// TestCleanCacheActuallyRemovesExpiredEntries verifies that the
// background sweep deletes entries older than the 30-minute cache timeout
// while preserving fresh ones, and the per-entry yield does not skip any.
func TestCleanCacheActuallyRemovesExpiredEntries(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)

	// Use a no-op yield so the sweep is fast.
	ts.cacheCleanSleep = func(time.Duration) {}

	freshKey := []byte("fresh")
	expiredKey := []byte("expired")
	ts.mu.Lock()
	ts.packetHashes[string(freshKey)] = time.Now()
	ts.packetHashes[string(expiredKey)] = time.Now().Add(-time.Hour)
	ts.mu.Unlock()

	ts.CleanCache()

	ts.mu.Lock()
	_, hasFresh := ts.packetHashes[string(freshKey)]
	_, hasExpired := ts.packetHashes[string(expiredKey)]
	ts.mu.Unlock()
	if !hasFresh {
		t.Fatal("fresh packet-hash entry was removed by CleanCache")
	}
	if hasExpired {
		t.Fatal("expired packet-hash entry survived CleanCache")
	}
}

// TestCleanCacheSkipsConnectedToSharedInstance verifies that a
// client of a shared instance never cleans the announce cache (Python
// Transport.py:2599).
func TestCleanCacheSkipsConnectedToSharedInstance(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	ts.SetConnectedToSharedInstance(true)

	var ran atomic.Int32
	ts.cacheCleanSleep = func(time.Duration) { ran.Add(1) }

	ts.mu.Lock()
	ts.packetHashes[string([]byte("expired"))] = time.Now().Add(-time.Hour)
	ts.mu.Unlock()

	ts.CleanCache()

	ts.mu.Lock()
	_, stillThere := ts.packetHashes[string([]byte("expired"))]
	ts.mu.Unlock()
	if !stillThere {
		t.Fatal("CleanCache removed an entry despite being connected to a shared instance")
	}
	if ran.Load() != 0 {
		t.Fatal("CleanCache ran the sweep despite being connected to a shared instance")
	}
}
