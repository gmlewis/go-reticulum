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

func TestTCPInterface(t *testing.T) {
	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Create server
	server, err := NewTCPServerInterface("server", "127.0.0.1", 37430, handler)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := server.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	// Create client
	client, err := NewTCPClientInterface("client", "127.0.0.1", 37430, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Detach(); err != nil {
			t.Fatalf("client detach failed: %v", err)
		}
	}()

	// Wait for connection to be accepted
	time.Sleep(100 * time.Millisecond)

	msg := []byte("hello tcp")
	if err := client.Send(msg); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %v, got %v", msg, data)
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timed out waiting for data")
	}
}

func TestHDLCFraming(t *testing.T) {
	data := []byte{0x01, 0x7E, 0x02, 0x7D, 0x03}
	escaped := HDLCEscape(data)
	unescaped := HDLCUnescape(escaped)

	if !bytes.Equal(data, unescaped) {
		t.Errorf("HDLC framing mismatch: expected %v, got %v", data, unescaped)
	}
}
