// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"testing"
	"time"
)

// TestRecordFlapNoPriorRecordNoCrash covers Phase 18 task 3: invoking the
// fast-flap teardown path (recordFlap) for a remote IP with no prior flap
// record must not crash and must create a fresh entry, mirroring Python's
// none-check that creates `[now, now, 0]` for a missing record
// (BackboneInterface.py:836-837, v1.4.0).
func TestRecordFlapNoPriorRecordNoCrash(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)

	const ip = "192.0.2.55"
	spawnedAt := time.Unix(9_999, 0) // 1s connection < 600s threshold

	// No prior record: this must not panic and must seed the entry.
	b.recordFlap(ip, spawnedAt)

	b.fastFlappingMu.Lock()
	entry, ok := b.fastFlapping[ip]
	b.fastFlappingMu.Unlock()
	if !ok {
		t.Fatal("missing record after recordFlap; want a fresh entry created")
	}
	if entry.flaps != 1 {
		t.Fatalf("flaps=%d want 1 for a fresh entry", entry.flaps)
	}
	if !entry.lastFlap.Equal(time.Unix(10_000, 0)) {
		t.Fatalf("lastFlap=%v want t=10000", entry.lastFlap)
	}
	if !entry.startedFlapping.Equal(time.Unix(10_000, 0)) {
		t.Fatalf("startedFlapping=%v want t=10000", entry.startedFlapping)
	}
}

// TestRecordFlapViaOnSpawnedDownNoPriorRecord covers Phase 18 task 3: the actual
// teardown path fires the TCPServerInterface onSpawnedDown hook (which
// BackboneInterface wires to recordFlap). Driving it for a remote IP with no
// prior flap record must not crash and must record the flap
// (BackboneInterface.py:820-843, v1.4.0).
func TestRecordFlapViaOnSpawnedDownNoPriorRecord(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)
	// Wire the hooks exactly as NewBackboneInterface does, so the test
	// exercises the real teardown→recordFlap path.
	inner := b.TCPServerInterface
	inner.mu.Lock()
	inner.onSpawnedDown = func(remoteIP string, spawnedAt time.Time) { b.recordFlap(remoteIP, spawnedAt) }
	inner.mu.Unlock()

	const ip = "192.0.2.56"
	spawnedAt := time.Unix(9_999, 0)
	hook := func() string {
		inner.mu.Lock()
		defer inner.mu.Unlock()
		if inner.onSpawnedDown == nil {
			return "<nil>"
		}
		inner.onSpawnedDown(ip, spawnedAt)
		return "fired"
	}()
	if hook != "fired" {
		t.Fatal("onSpawnedDown hook not wired")
	}

	b.fastFlappingMu.Lock()
	entry, ok := b.fastFlapping[ip]
	b.fastFlappingMu.Unlock()
	if !ok {
		t.Fatal("onSpawnedDown did not create a flap record; want a fresh entry")
	}
	if entry.flaps != 1 {
		t.Fatalf("flaps=%d want 1 after onSpawnedDown", entry.flaps)
	}
}

// TestRecordFlapEmptyRemoteIPNoOp covers Phase 18 task 3: a teardown with an
// empty remote IP (defensive guard) is a no-op that does not crash
// (BackboneInterface.py:833 would skip on an empty target_ip).
func TestRecordFlapEmptyRemoteIPNoOp(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)

	spawnedAt := time.Unix(9_999, 0)
	b.recordFlap("", spawnedAt) // must not panic

	b.fastFlappingMu.Lock()
	n := len(b.fastFlapping)
	b.fastFlappingMu.Unlock()
	if n != 0 {
		t.Fatalf("empty remote IP recorded %d entries; want 0", n)
	}
}

// TestRecordFlapNilBackboneNoCrash covers Phase 18 task 3: a nil receiver is a
// defensive no-op (the guard at the top of recordFlap), so the teardown path
// never crashes on an uninitialised BackboneInterface.
func TestRecordFlapNilBackboneNoCrash(t *testing.T) {
	t.Parallel()
	var b *BackboneInterface
	// Must not panic on a nil receiver.
	b.recordFlap("192.0.2.57", time.Unix(9_999, 0))
}

// TestRecordFlapGraceWarningNoCrash covers Phase 18 task 3: crossing the grace
// threshold emits the "Ignoring further connections" warning via the standard
// logger and does not crash (BackboneInterface.py:852, v1.4.0). The behavioral
// observable is that the IP becomes blocked (isBlocked true) without any
// panic.
func TestRecordFlapGraceWarningNoCrash(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 2, 12*60*60, now)

	const ip = "192.0.2.58"
	spawnedAt := time.Unix(9_999, 0)
	// grace 2: the 3rd flap pushes flaps > grace and logs the warning.
	for range 3 {
		b.recordFlap(ip, spawnedAt)
	}
	if !b.isBlocked(ip) {
		t.Fatal("expected blocked after exceeding grace (and warning logged)")
	}
}
