// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"sync"
	"testing"
	"time"
)

func TestRunInBubbleVirtualizesSleeps(t *testing.T) {
	t.Parallel()

	RunInBubble(t, func(t *testing.T) {
		// The sync channel MUST be created inside the bubble: a receive on
		// a channel created outside is not durably blocked on bubble time,
		// so the fake clock would never advance. (Real-clock wall time is
		// not observable from here either — the elapsed assert is virtual.)
		done := make(chan struct{})
		start := time.Now()
		go func() {
			time.Sleep(2 * time.Second)
			close(done)
		}()
		// Block on the signal: virtual time advances while EVERY bubble
		// goroutine (caller included) is durably blocked, which fires the
		// 2s sleep instantly. Wait() alone is a quiescence barrier and
		// does NOT advance the clock past pending timers.
		<-done
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Fatalf("virtual elapsed after 2s sleep = %v, want exactly 2s", elapsed)
		}
	})
}

func TestRunInBubbleStartsAtEpoch2000(t *testing.T) {
	t.Parallel()

	RunInBubble(t, func(t *testing.T) {
		if got := time.Now().UTC(); got.Year() != 2000 {
			t.Fatalf("bubble clock starts at %v, want year 2000", got)
		}
	})
}

func TestRunInBubbleWaitGroupQuiescencePanicIsLoud(t *testing.T) {
	t.Parallel()

	// A goroutine still running when fn returns panics at bubble exit —
	// the documented contract violation is a loud deterministic failure,
	// not a flake. Assert the panic surfaces.
	defer func() {
		if recover() == nil {
			t.Fatal("bubble exit did not panic for a still-running goroutine")
		}
	}()
	RunInBubble(t, func(t *testing.T) {
		go func() {
			for {
				time.Sleep(10 * time.Millisecond)
			}
		}()
	})
}

func TestWaitSettlesSpawnedGoroutines(t *testing.T) {
	t.Parallel()

	RunInBubble(t, func(t *testing.T) {
		var mu sync.Mutex
		counter := 0
		queued := make(chan struct{})
		go func() {
			mu.Lock()
			counter = 1
			mu.Unlock()
			close(queued)
		}()
		// Wait() settles the goroutine once its work is queued.
		Wait()
		select {
		case <-queued:
		default:
			t.Fatal("Wait() returned before the goroutine's work was queued")
		}
		mu.Lock()
		defer mu.Unlock()
		if counter != 1 {
			t.Fatalf("goroutine had not run after Wait; counter = %d", counter)
		}
	})
}
