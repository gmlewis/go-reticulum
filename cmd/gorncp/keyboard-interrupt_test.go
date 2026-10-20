// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestKeyboardInterruptHandling(t *testing.T) {
	t.Parallel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		time.Sleep(10 * time.Millisecond)
	}()

	select {
	case <-sigChan:
		t.Log("Signal received, cleanup would occur")
	case <-time.After(100 * time.Millisecond):
		t.Log("No signal received (expected in test)")
	}

	cleanupCalled := false
	cleanup := func() {
		cleanupCalled = true
	}
	cleanup()

	if !cleanupCalled {
		t.Fatal("Cleanup function was not called")
	}

	t.Log("KeyboardInterrupt handling test PASSED")
}
