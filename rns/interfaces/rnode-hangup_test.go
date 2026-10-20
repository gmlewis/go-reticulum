// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"os"
	"syscall"
	"testing"
)

// TestRNodePortAHungUpLineEndsTheLinkEvenWhenTheReadStartsAfterwards pins the half
// of the hangup rule that a read's *result* cannot carry.
//
// The kernel reports the far end going away as EIO exactly once, and only to a
// reader that is already parked in the read when it happens. A read started after
// the close is indistinguishable from the line's own idle timer — the same
// zero-byte read, reported the same way — so an interface that only ever looks at
// what the read returned would keep a dead descriptor and go on reporting itself
// up.
//
// So the port itself has to be asked, and this test asks it in the one state that
// leaves the read no clue at all: after the close.
func TestRNodePortAHungUpLineEndsTheLinkEvenWhenTheReadStartsAfterwards(t *testing.T) {
	t.Parallel()

	master, slavePath := openUnopenedPTY(t)
	slave, err := os.OpenFile(slavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("opening the slave %v the way the transport does: %v", slavePath, err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := configureTermios(slave.Fd(), 115200, 8, "N", 1); err != nil {
		t.Fatalf("configuring %v: %v", slavePath, err)
	}

	r := newBareRNodeForTest(t, slave, 915000000, 125000, 17, 8, 5, nil)
	t.Cleanup(func() { _ = r.Detach() })

	// A line whose far end is still there is idle, not ended: this must not be
	// mistaken for a hangup, or every quiet radio would be taken offline.
	if got := r.readLoopOnce(); got != nil {
		t.Fatalf("readLoopOnce over an idle port = %v, want nil: the line has not ended", got)
	}

	// The radio is unplugged and the bridge that owned the terminal exits. The
	// close happens now, while nobody is reading.
	if err := master.Close(); err != nil {
		t.Fatalf("closing the port's other end: %v", err)
	}

	if got := r.readLoopOnce(); got == nil {
		t.Fatal("readLoopOnce read a hung-up port as an idle line, so nothing would " +
			"ever take the interface offline and the transport would keep its dead " +
			"descriptor while reporting itself up")
	}
}
