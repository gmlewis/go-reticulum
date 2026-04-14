// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

import "testing"

func TestTTYRestorerUsesPTYTermiosOnDarwin(t *testing.T) {
	t.Parallel()

	pty, err := openPTY()
	if err != nil {
		t.Fatalf("openPTY() error: %v", err)
	}
	defer pty.close()

	restorer, err := newTTYRestorer(int(pty.slave.Fd()))
	if err != nil {
		t.Fatalf("newTTYRestorer() error: %v", err)
	}
	if restorer == nil {
		t.Fatal("newTTYRestorer() returned nil restorer")
	}
	if !restorer.active {
		t.Fatal("expected Darwin TTY restorer to be active for a PTY")
	}
	if err := restorer.raw(); err != nil {
		t.Fatalf("raw() error: %v", err)
	}
	if err := restorer.restore(); err != nil {
		t.Fatalf("restore() error: %v", err)
	}
}
