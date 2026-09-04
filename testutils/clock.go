// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"sync"
	"time"
)

// StepClock is a deterministic, manually advanced clock for tests that inject
// a `now func() time.Time` seam (e.g. lxmf Router.now). Advancing the clock
// only changes what Now returns — it does not fire timers — so it is for
// timestamp sequencing, not for driving delay loops (use RunInBubble for
// that). All methods are safe for concurrent use.
type StepClock struct {
	mu  sync.Mutex
	cur time.Time
}

// NewStepClock returns a StepClock standing at start.
func NewStepClock(start time.Time) *StepClock {
	return &StepClock{cur: start}
}

// Now returns the clock's current time. It matches the `now func() time.Time`
// seam shape used across the repo.
func (c *StepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cur
}

// Advance moves the clock forward by d and returns the new time. Negative
// durations are ignored: a test clock only ever moves forward.
func (c *StepClock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.cur = c.cur.Add(d)
	}
	return c.cur
}
