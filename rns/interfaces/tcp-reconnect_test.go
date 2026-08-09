// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// TestTCPClientReconnectFiresOnConnect verifies that a TCP client interface
// whose initial connect is refused fires its onConnect hook when reconnectLoop
// later establishes the connection. The transport installs this hook in
// RegisterInterface to re-announce the local destinations after a reconnect, so
// a client that registered while disconnected does not stay unannounced to the
// peer until the periodic announce interval (minutes) fires. Without the
// onConnect call in reconnectLoop this test hangs and fails.
func TestTCPClientReconnectFiresOnConnect(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	// Build the client against a port with no listener yet, with a short
	// reconnect delay. We construct it manually (instead of
	// mustTestNewTCPClientInterface) so reconnectDelay is set BEFORE
	// reconnectLoop starts reading it — otherwise racing a test write against
	// the goroutine's read under -race. Production sets reconnectDelay once
	// in the constructor and never re-writes it, so this mirrors the
	// race-free production shape.
	bi := NewBaseInterface("client", ModeFull, TCPBitrateGuess)
	client := &TCPClientInterface{
		BaseInterface:  bi,
		targetHost:     "127.0.0.1",
		targetPort:     port,
		reconnectDelay: 50 * time.Millisecond,
		writeTimeout:   tcpWriteTimeout,
	}
	if err := client.connect(); err != nil {
		// First connect refused (no listener) — start the reconnect loop,
		// exactly as NewTCPClientInterface does.
		go client.reconnectLoop()
	} else {
		atomic.StoreInt32(&client.running, 1)
		go client.readLoop()
	}
	defer func() {
		if err := client.Detach(); err != nil {
			t.Fatalf("client detach failed: %v", err)
		}
	}()

	connected := make(chan struct{}, 4)
	client.SetOnConnect(func() {
		select {
		case connected <- struct{}{}:
		default:
		}
	})

	// While no listener is up, every reconnect attempt fails and onConnect
	// must not fire.
	select {
	case <-connected:
		t.Fatalf("onConnect fired before the interface was connected")
	case <-time.After(250 * time.Millisecond):
	}

	// Start a bare listener so the next dial succeeds. connect() only needs
	// the TCP handshake to complete; it does not require an RNS peer on the
	// other side, so a plain listener is enough to drive the reconnect.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	// Drain and hold accepted connections open so the client readLoop does
	// not get a spurious EOF (and trigger another reconnect) during the
	// assertion window.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	select {
	case <-connected:
		// onConnect fired after reconnect — the transport's re-announce
		// hook would run here, recovering the announce lost when the
		// interface registered while disconnected.
	case <-time.After(3 * time.Second):
		t.Fatalf("onConnect did not fire after reconnect within 3s")
	}
}