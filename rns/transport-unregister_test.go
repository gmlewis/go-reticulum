// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"net"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// startSingleClientServer builds a TCPServerInterface on an ephemeral local
// port, connects one raw TCP client to it, and returns the server plus the
// spawned client interface the server created for that connection.
func startSingleClientServer(t *testing.T) (*interfaces.TCPServerInterface, *interfaces.TCPClientInterface, net.Conn) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen failed: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("probe close failed: %v", err)
	}

	spawnedCh := make(chan *interfaces.TCPClientInterface, 1)
	onConnect := func(iface interfaces.Interface) {
		if ci, ok := iface.(*interfaces.TCPClientInterface); ok {
			select {
			case spawnedCh <- ci:
			default:
			}
		}
	}
	srv, err := interfaces.NewTCPServerInterface("prune-chain-test",
		"127.0.0.1", port,
		func(data []byte, iface interfaces.Interface) {},
		onConnect)
	if err != nil {
		t.Fatalf("server create failed: %v", err)
	}

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	select {
	case ci := <-spawnedCh:
		return srv, ci, conn
	case <-time.After(3 * time.Second):
		_ = conn.Close()
		t.Fatal("server never spawned the connected client")
		return nil, nil, nil
	}
}

// TestServerPruneUnregistersFromTransport verifies the full spawned-client
// cleanup chain: when a spawned client's connection dies, its parent
// TCPServerInterface prunes it from the spawn list AND the removal propagates
// to the transport registry, so flood/rebroadcast rounds stop targeting the
// corpse.
//
// Parity: Python removes dead remote-client interfaces from
// Transport.interfaces via Transport.remove_interface when the socket closes
// (RNS/Interfaces/TCPInterface.py:450-453). The Go port previously left both
// the parent's spawn list and ts.interfaces untouched forever, so hubs kept
// erroring "interface … is not running" against zombie clients on every
// announce fan-out and partially dropped rebroadcasts to live peers.
func TestServerPruneUnregistersFromTransport(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: mustHexDecode(t, "11223344556677889900112233445566")}

	server, client, rawConn := startSingleClientServer(t)
	defer func() {
		_ = rawConn.Close()
		_ = server.Detach()
	}()

	// Production registers each spawned client as its connectHandler fires
	// (rns.go wraps NewTCPServerInterface's onConnect). Mirror that here;
	// RegisterInterface installs the transport-removal onRemove hook.
	ts.RegisterInterface(server)
	ts.RegisterInterface(interfaces.Interface(client))

	waitForCondition(t, 3*time.Second, func() bool {
		return slices.Contains(ts.GetInterfaces(), interfaces.Interface(client))
	}, "spawned client never registered with transport")

	// Kill the client from its own side by dropping the socket, as a remote
	// process dying would — the server-side readLoop must observe EOF while
	// still up and run the full teardown chain (prune + onRemove).
	if err := rawConn.Close(); err != nil {
		t.Fatalf("raw conn close failed: %v", err)
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return !slices.Contains(ts.GetInterfaces(), interfaces.Interface(client))
	}, "transport registry still contains dead spawned client")
}

// waitForCondition polls cond until it returns true or the timeout expires,
// failing with msg.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
