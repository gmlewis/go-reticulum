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

func TestI2PInterfaceRoundTrip(t *testing.T) {
	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	serverIface, err := NewI2PInterface("i2p-server", "127.0.0.1", 37434, handler)
	mustTest(t, err)
	defer func() {
		if err := serverIface.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	if serverIface.Type() != "I2PInterface" {
		t.Fatalf("server type = %q, want I2PInterface", serverIface.Type())
	}

	peerIface, err := NewI2PInterfacePeer("i2p-peer", "127.0.0.1", 37434, nil)
	mustTest(t, err)
	defer func() {
		if err := peerIface.Detach(); err != nil {
			t.Fatalf("peer detach failed: %v", err)
		}
	}()

	if peerIface.Type() != "I2PInterfacePeer" {
		t.Fatalf("peer type = %q, want I2PInterfacePeer", peerIface.Type())
	}

	time.Sleep(100 * time.Millisecond)

	msg := []byte("hello i2p")
	if err := peerIface.Send(msg); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Fatalf("received data mismatch: expected %q, got %q", string(msg), string(data))
		}
	case <-time.After(800 * time.Millisecond):
		t.Fatal("timed out waiting for i2p data")
	}
}
