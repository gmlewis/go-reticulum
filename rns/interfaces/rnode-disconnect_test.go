// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestRNodePortClosingTakesTheInterfaceOffline is the invariant behind an
// appliance that must never claim a radio it does not have.
//
// Unplugging the radio ends its serial link: the bridge holding the
// pseudo-terminal lets go, the pty master closes, and a read on the slave
// reports EIO — once, at the moment the other end goes. Python's readLoop lets
// pyserial's error reach its `except`, which sets `online = False` and starts
// reconnecting, so the interface stops reporting a radio that is not there. A
// reader that counted the same failure as "nothing to read yet" would report the
// radio as connected for as long as the process lived, because the condition it
// waits on never changes.
func TestRNodePortClosingTakesTheInterfaceOffline(t *testing.T) {
	t.Parallel()

	mock := newMockRNode()
	r := newRNodeForTest(t, mock, 915000000, 125000, 17, 8, 5, nil)
	if !r.Status() {
		t.Fatal("the interface should be online after configure")
	}

	mock.fail(&os.PathError{Op: "read", Path: "/dev/pts/0", Err: syscall.EIO})

	if !waitForStatus(r, false, offlineBound) {
		t.Fatal("the interface still reports its radio as connected after its port ended")
	}
	_ = r.Detach()
}

// TestRNodeIdlePortIsNotTheEndOfTheLink is the other half of the rule, and it is
// not hypothetical: configureTermios sets VMIN=0 and VTIME=1, so an idle line
// hands back a zero-byte read every 100ms and Go reports that on an *os.File as
// io.EOF. A reader that took io.EOF for the end of the link would take a healthy
// radio offline ten times a second — the opposite fault, and one this port's
// reader has already been through once.
func TestRNodeIdlePortIsNotTheEndOfTheLink(t *testing.T) {
	t.Parallel()

	mock := newMockRNode()
	r := newRNodeForTest(t, mock, 915000000, 125000, 17, 8, 5, nil)

	// Every read from here on is what an idle port gives.
	mock.fail(io.EOF)

	if waitForStatus(r, false, idleBound) {
		t.Fatal("an idle port took the interface offline, so a radio with nothing to say would never stay connected")
	}
	if !r.Status() {
		t.Fatal("the interface should still be online after reading an idle port")
	}
	_ = r.Detach()
}

// TestRNodeReadTransientFailuresAreNotTheEndOfTheLink covers the reads that are
// not about the link at all: an interrupted read, and one that had nothing to
// give on a port that does not block.
func TestRNodeReadTransientFailuresAreNotTheEndOfTheLink(t *testing.T) {
	t.Parallel()

	for _, err := range []error{syscall.EINTR, syscall.EAGAIN} {
		r := newRNodeForTest(t, newMockRNode(), 915000000, 125000, 17, 8, 5, nil)
		r.connMu.Lock()
		r.conn = &erroringRNodeConn{err: err}
		r.connMu.Unlock()

		if got := r.readLoopOnce(); got != nil {
			t.Fatalf("readLoopOnce over a %v read = %v, want nil: the link has not ended", err, got)
		}
		_ = r.Detach()
	}
}

// offlineBound is how long the interface has to notice that its radio is gone.
// The read loop is a goroutine and the failure is not instantaneous, so the
// transition is waited for; the bound is what keeps a platform difference
// failing in seconds with the transition named, rather than hanging.
const offlineBound = 2 * time.Second

// idleBound is how long an idle port is given to be mistaken for a dead one. It
// only has to be longer than a handful of reads, and it costs this much only
// when the rule is broken.
const idleBound = time.Second

// waitForStatus reports whether the interface reaches want within the bound.
func waitForStatus(r *RNodeInterface, want bool, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if r.Status() == want {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// erroringRNodeConn reports one fixed error from every read, and carries what it
// is written, for the reads that are about classification rather than bytes.
type erroringRNodeConn struct {
	err error
}

func (c *erroringRNodeConn) Read([]byte) (int, error)    { return 0, c.err }
func (c *erroringRNodeConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *erroringRNodeConn) Close() error                { return nil }
