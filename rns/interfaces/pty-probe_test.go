// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"errors"
	"io"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestRNodePortReportsAnIdleLineAsNothingAndACloseAsEIO pins, on a real
// pseudo-terminal, the two facts the RNode read loop's error handling is built
// out of. It is the test that would have caught the mistake of treating every
// io.EOF as the end of the link.
//
// The port is opened and configured exactly as openPort does it — os.OpenFile,
// then configureTermios over File.Fd() — because how a descriptor is opened and
// configured decides how a read on it behaves: File.Fd() puts the descriptor
// back into blocking mode, which is what lets the line's own VMIN/VTIME timer
// govern the read at all.
func TestRNodePortReportsAnIdleLineAsNothingAndACloseAsEIO(t *testing.T) {
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

	// A reader parked on the port, exactly as readLoopOnce leaves one, reading
	// straight through the close: a read started after the close cannot tell the
	// close apart from the line's idle timer, so the reader has to be running
	// when it happens.
	results := make(chan error, 256)
	go func() {
		buf := make([]byte, 512)
		for {
			_, err := slave.Read(buf)
			select {
			case results <- err:
			case <-time.After(5 * time.Second):
				return
			}
		}
	}()

	// An idle line reports nothing to read, over and over, and one of those
	// reads is what the loop must not mistake for an ending.
	for range 2 {
		err, ok := nextReadResult(t, results)
		if !ok {
			t.Fatal("the reader stopped reporting while the line was idle")
		}
		if err == nil {
			t.Fatal("an idle line reported a read with no error, which is not what this port does")
		}
		if !errors.Is(err, io.EOF) {
			t.Fatalf("an idle line read gave %v, want io.EOF: the rule for an idle port is written against that", err)
		}
	}

	// The other end going away is reported as EIO, once. That is the kernel's
	// behaviour on Linux, which is what the appliance runs and what CI runs; on
	// Darwin the same close arrives as another zero-byte read, indistinguishable
	// from the line being idle, so there is nothing here to distinguish and
	// nothing to assert.
	if runtime.GOOS == "darwin" {
		t.Skip("this kernel reports a close as a zero-byte read, not EIO: nothing to distinguish")
	}
	if err := master.Close(); err != nil {
		t.Fatalf("closing the port's other end: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err, ok := nextReadResult(t, results)
		if !ok {
			t.Fatal("the reader stopped reporting after the port's other end closed")
		}
		if errors.Is(err, syscall.EIO) {
			t.Logf("the close arrived as %v, which is what takes the interface offline", err)
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("the port's other end closed but no read reported EIO, so nothing can tell the interface its radio is gone")
		}
	}
}

// nextReadResult takes one read result, reporting false when the reader has
// stopped reporting altogether — which is itself a finding, not a hang.
func nextReadResult(t *testing.T, results <-chan error) (error, bool) {
	t.Helper()
	select {
	case err := <-results:
		return err, true
	case <-time.After(2 * time.Second):
		return nil, false
	}
}
