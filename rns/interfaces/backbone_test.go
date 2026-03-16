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

func TestBackboneInterfaceRoundTrip(t *testing.T) {
	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	serverIface, err := NewBackboneInterface("backbone-server", "127.0.0.1", 37432, handler)
	mustTest(t, err)
	defer func() {
		if err := serverIface.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	if serverIface.Type() != "BackboneInterface" {
		t.Fatalf("server type = %q, want BackboneInterface", serverIface.Type())
	}

	clientIface, err := NewBackboneClientInterface("backbone-client", "127.0.0.1", 37432, nil)
	mustTest(t, err)
	defer func() {
		if err := clientIface.Detach(); err != nil {
			t.Fatalf("client detach failed: %v", err)
		}
	}()

	if clientIface.Type() != "BackboneClientInterface" {
		t.Fatalf("client type = %q, want BackboneClientInterface", clientIface.Type())
	}

	time.Sleep(100 * time.Millisecond)

	msg := []byte("hello backbone")
	if err := clientIface.Send(msg); err != nil {
		t.Fatal(err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Fatalf("received data mismatch: expected %q, got %q", string(msg), string(data))
		}
	case <-time.After(800 * time.Millisecond):
		t.Fatal("timed out waiting for backbone data")
	}
}
