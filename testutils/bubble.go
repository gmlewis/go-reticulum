// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"testing"
	"testing/synctest"
)

// RunInBubble runs fn inside a testing/synctest bubble: a fake clock starting
// at midnight UTC 2000-01-01 that advances only while every goroutine in the
// bubble is durably blocked, so fixed sleeps and timers cost nothing and
// "no event within X" assertions become exact. fn receives the bubble's own
// *testing.T (synctest.Test, not Run, so t.Fatalf/t.Errorf inside work and
// test naming/log depth stay right).
//
// Contract for fn (violations hang or panic loudly at bubble exit — they are
// deterministic failures, not flakes):
//   - No real network, os/exec, or cross-process work: those block on the
//     outside world, not on bubble timers, so virtual time never advances.
//     Use in-memory pipes (net.Pipe, interfaces.PipeInterface) instead.
//   - Every goroutine started inside fn must have returned before fn returns,
//     with NO pending bubble timers left behind: once fn exits, the runtime
//     deadlocks ("main bubble goroutine has exited but blocked goroutines
//     remain") on any goroutine still in time.Sleep/<-time.After — it does
//     NOT advance the clock to drain pending timers. Stop loop-style workers
//     explicitly, then sleep past their interval (durably blocking fn, which
//     advances the clock through the worker's in-flight sleep) and call
//     Wait(). Drain library goroutine fan-outs (e.g. rns WaitOutboundSends)
//     the same way. Note the asymmetry: while fn is STILL RUNNING, a caller
//     blocked on a channel/timer does advance the clock and wake timer-driven
//     goroutines (that is how tests wait for events) — the rule bites only
//     at fn return. synctest.Wait() alone never advances the clock.
//   - Channels used to synchronize with fn must be created INSIDE the
//     bubble: a receive on a channel created outside is not durably blocked
//     on bubble time, so the fake clock never advances.
//   - Do not call t.Parallel, t.Run, or t.Fatal inside fn; use t.Errorf from
//     helper goroutines and propagate failures over channels.
//   - Create scratch directories BEFORE entering the bubble (testutils.TempDir
//     registers cleanup on the outer t, which runs on the real clock after
//     the bubble closes). File I/O inside the bubble is fine.
//   - Anything captured from the real wall clock outside the bubble cannot be
//     compared against times produced inside (the bubble starts at 2000-01-01).
//   - Weak lower-bound elapsed assertions (elapsed >= X) are unreliable when
//     unrelated tickers run in the bubble: virtual time can overshoot. Assert
//     the effect (event fired, state advanced) instead of the elapsed time.
//
// Converted tests keep working under the repo's -race driver.
func RunInBubble(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	synctest.Test(t, fn)
}

// Wait waits until every other goroutine in the current bubble is durably
// blocked. It is a quiescence barrier: it does NOT advance the bubble clock
// past pending timers (virtual time advances only when EVERY goroutine,
// including the caller, is durably blocked — so a timer-driven goroutine's
// sleep fires once the caller is also blocked, e.g. receiving on the channel
// that goroutine signals). Call it before asserting on state written by
// spawned goroutines, and before returning from RunInBubble bodies to settle
// goroutines whose work is already queued. Must be called inside a bubble,
// and never concurrently by multiple bubble goroutines.
func Wait() {
	synctest.Wait()
}
