// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSetStateSetMethodSynchronizedWithSnapshot verifies that SetState and
// SetMethod take the per-Message persist mutex, so a concurrent
// PackedContainer/WriteToDirectory snapshot (which reads State and Method
// under the same lock) never races with state/method mutation. This guards
// the router's move from direct m.state/m.method assignment to the locked
// helpers (router.go ProcessOutbound/sendMessagePacketLocked and friends).
//
// Run under -race: an unlocked setter trips the race detector on the
// concurrent State/Method read in packedContainerLocked. The bounded deadline
// also catches any deadlock regression in the r.mu -> persistMu lock ordering
// the helpers participate in (the snapshot path takes only persistMu, so a
// cycle here would mean the helpers double-lock or the snapshot path started
// taking r.mu).
func TestSetStateSetMethodSynchronizedWithSnapshot(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)
	dir := testutils.TempDir(t, tempDirPrefix)

	deadline := time.Now().Add(1500 * time.Millisecond)
	var wg sync.WaitGroup
	var failed atomic.Bool

	// Snapshot/persist goroutine: exercises the exported PackedContainer and
	// WriteToDirectory paths that read State and Method under persistMu.
	wg.Go(func() {
		for time.Now().Before(deadline) && !failed.Load() {
			if _, err := msg.PackedContainer(); err != nil {
				t.Errorf("PackedContainer: %v", err)
				failed.Store(true)
				return
			}
			if _, err := msg.WriteToDirectory(dir); err != nil {
				t.Errorf("WriteToDirectory: %v", err)
				failed.Store(true)
				return
			}
		}
	})

	// State mutator: uses the locked helper the router now uses instead of
	// direct field assignment.
	wg.Go(func() {
		for i := 0; time.Now().Before(deadline) && !failed.Load(); i++ {
			msg.SetState(i % 256)
		}
	})

	// Method mutator: uses the locked helper the router now uses instead of
	// direct field assignment.
	wg.Go(func() {
		for i := 0; time.Now().Before(deadline) && !failed.Load(); i++ {
			msg.SetMethod(i % 8)
		}
	})

	// Deadlock guard: if the locked helpers and the snapshot path ever form a
	// lock cycle, wg.Wait never returns.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetState/SetMethod deadlocked with concurrent PackedContainer/WriteToDirectory")
	}
}

// TestSetStateSetMethodAssignsValue verifies the helpers actually write the
// supplied value and that the write is visible to a subsequent reader — the
// persist mutex provides the happens-before edge, so a reader that also takes
// the lock (as PackedContainer does) observes the latest value.
func TestSetStateSetMethodAssignsValue(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)

	for _, tc := range []struct{ state, method int }{
		{StateOutbound, MethodDirect},
		{StateSending, MethodPropagated},
		{StateSent, MethodOpportunistic},
		{StateDelivered, MethodPaper},
		{StateFailed, MethodDirect},
	} {
		msg.SetState(tc.state)
		msg.SetMethod(tc.method)

		// Read under the persist lock the way packedContainerLocked does, so
		// the assertion observes the synchronized value rather than relying on
		// plain field access. The unexported fields are read directly because
		// the lock is already held — the State()/Method() getters would re-enter
		// the non-reentrant persistMu and self-deadlock.
		msg.persistMu.Lock()
		gotState, gotMethod := msg.state, msg.method
		msg.persistMu.Unlock()

		if gotState != tc.state {
			t.Fatalf("State=%#x want=%#x", gotState, tc.state)
		}
		if gotMethod != tc.method {
			t.Fatalf("Method=%#x want=%#x", gotMethod, tc.method)
		}
	}
}

// TestSetStateSetMethodConcurrentMutatorsNoDeadlock hammers the two helpers
// from many goroutines at once. Because both contend only on the single
// per-Message persistMu (with no second lock acquired underneath), this must
// never deadlock and must complete well under the timeout. It catches a
// regression where a helper accidentally acquires a second lock (e.g. r.mu)
// in an order that could cycle with a caller already holding it.
func TestSetStateSetMethodConcurrentMutatorsNoDeadlock(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)

	deadline := time.Now().Add(1500 * time.Millisecond)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for i := 0; time.Now().Before(deadline); i++ {
				if i&1 == 0 {
					msg.SetState(i % 256)
				} else {
					msg.SetMethod(i % 8)
				}
			}
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent SetState/SetMethod deadlocked")
	}
}

// TestSetProgressSynchronizedWithSnapshot verifies that SetProgress takes the
// per-Message persist mutex, so a concurrent PackedContainer/WriteToDirectory
// snapshot never races with a progress write, and an external Progress() reader
// can never race the router's progress updates. Run under -race: an unlocked
// SetProgress trips the race detector. The bounded deadline also catches any
// deadlock regression in the r.mu -> persistMu lock ordering.
func TestSetProgressSynchronizedWithSnapshot(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)
	dir := testutils.TempDir(t, tempDirPrefix)

	deadline := time.Now().Add(1500 * time.Millisecond)
	var wg sync.WaitGroup
	var failed atomic.Bool

	// Snapshot/persist goroutine: exercises the exported PackedContainer and
	// WriteToDirectory paths that take persistMu.
	wg.Go(func() {
		for time.Now().Before(deadline) && !failed.Load() {
			if _, err := msg.PackedContainer(); err != nil {
				t.Errorf("PackedContainer: %v", err)
				failed.Store(true)
				return
			}
			if _, err := msg.WriteToDirectory(dir); err != nil {
				t.Errorf("WriteToDirectory: %v", err)
				failed.Store(true)
				return
			}
		}
	})

	// Progress mutator: uses the locked helper the router uses for every
	// progress change.
	wg.Go(func() {
		for i := 0; time.Now().Before(deadline) && !failed.Load(); i++ {
			// Sweep a 0.0-1.0 range so the value changes every iteration.
			msg.SetProgress(float64(i%100) / 100.0)
		}
	})

	// External reader: reads Progress via the locked getter the way a
	// downstream consumer would. Must never race a SetProgress write.
	wg.Go(func() {
		for time.Now().Before(deadline) && !failed.Load() {
			_ = msg.Progress()
		}
	})

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SetProgress/Progress deadlocked with concurrent PackedContainer/WriteToDirectory")
	}
}

// TestSetProgressAssignsValue verifies SetProgress actually writes the supplied
// value and that it is visible via the locked Progress getter.
func TestSetProgressAssignsValue(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)

	for _, want := range []float64{0.0, 0.01, 0.10, 0.50, 0.90, 1.0} {
		msg.SetProgress(want)
		if got := msg.Progress(); got != want {
			t.Fatalf("Progress=%v want=%v", got, want)
		}
	}
}
