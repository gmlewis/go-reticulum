// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
	"time"
)

func TestUDPInterface(t *testing.T) {
	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Create two interfaces talking to each other on localhost
	if1, err := NewUDPInterface("if1", "127.0.0.1", 37428, "127.0.0.1", 37429, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := if1.Detach(); err != nil {
			t.Fatalf("if1 detach failed: %v", err)
		}
	}()

	if2, err := NewUDPInterface("if2", "127.0.0.1", 37429, "127.0.0.1", 37428, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := if2.Detach(); err != nil {
			t.Fatalf("if2 detach failed: %v", err)
		}
	}()

	msg := []byte("hello reticulum")
	if err := if2.Send(msg); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %s, got %s", msg, data)
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("timed out waiting for data")
	}

	if if1.BytesReceived() != uint64(len(msg)) {
		t.Errorf("expected %v bytes received, got %v", len(msg), if1.BytesReceived())
	}
	if if2.BytesSent() != uint64(len(msg)) {
		t.Errorf("expected %v bytes sent, got %v", len(msg), if2.BytesSent())
	}
}
