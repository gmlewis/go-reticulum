// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

type probePathRequestFake struct {
	requested int
	reqErr    error
	revealAt  int
	checks    int
	hasPath   bool
}

func (f *probePathRequestFake) HasPath([]byte) bool {
	f.checks++
	if !f.hasPath && f.revealAt > 0 && f.checks >= f.revealAt {
		f.hasPath = true
	}
	return f.hasPath
}

func (f *probePathRequestFake) RequestPath([]byte) error {
	f.requested++
	return f.reqErr
}

type probePathClock struct {
	now time.Time
}

func (c *probePathClock) Now() time.Time        { return c.now }
func (c *probePathClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

func TestWaitForProbePathPrintsSpinnerAndReturnsNil(t *testing.T) {
	t.Parallel()

	clock := &probePathClock{now: time.Unix(0, 0)}
	fake := &probePathRequestFake{revealAt: 3}
	var out bytes.Buffer
	if err := waitForProbePathAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, clock.Now, clock.Sleep); err != nil {
		t.Fatalf("waitForProbePathAt returned error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Path to <aabb> requested") {
		t.Fatalf("missing request message: %q", got)
	}
	if !strings.Contains(got, "\b\b") {
		t.Fatalf("missing spinner output: %q", got)
	}
	if fake.requested != 1 {
		t.Fatalf("expected one path request, got %v", fake.requested)
	}
}

func TestWaitForProbePathTimesOut(t *testing.T) {
	t.Parallel()

	clock := &probePathClock{now: time.Unix(0, 0)}
	fake := &probePathRequestFake{}
	var out bytes.Buffer
	err := waitForProbePathAt(&out, fake, []byte{0xaa, 0xbb}, 0.1, clock.Now, clock.Sleep)
	if !errors.Is(err, errPathRequestTimedOut) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "Path request timed out") {
		t.Fatalf("missing timeout message: %q", out.String())
	}
}

func TestWaitForProbePathReturnsRequestError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	fake := &probePathRequestFake{reqErr: wantErr}
	var out bytes.Buffer
	err := waitForProbePathAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, func() time.Time { return time.Unix(0, 0) }, func(time.Duration) {})
	if err == nil || !strings.Contains(err.Error(), "Could not request path to <aabb>") {
		t.Fatalf("unexpected error: %v", err)
	}
	if fake.requested != 1 {
		t.Fatalf("expected one request attempt, got %v", fake.requested)
	}
}
