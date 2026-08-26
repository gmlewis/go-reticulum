// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestTCPServerPrunesDeadSpawnedClient verifies that when a spawned client's
// connection dies, the parent TCPServerInterface drops it from its
// spawnedInterfaces list instead of retaining it forever.
//
// Parity: Python removes dead interfaces from Transport.interfaces
// (RNS/Interfaces/TCPInterface.py:453 calls Transport.remove_interface(self)
// when the socket for a remote client was closed). The Go port kept every
// spawned client alive forever, so after a fleet of clients reconnected on
// fresh ports the hub fanned announces out to zombie sockets for hours,
// logging "Could not transmit on Client …: interface … is not running" bursts
// on every rebroadcast round and partially dropping fan-out to live peers.
func TestTCPServerPrunesDeadSpawnedClient(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	var mu sync.Mutex
	spawned := 0

	handler := func(data []byte, iface Interface) {}
	onConnect := func(iface Interface) {
		mu.Lock()
		spawned++
		mu.Unlock()
	}

	tsi, err := newTCPServerInterface("prune-test-server", "127.0.0.1", port, handler, onConnect, TCPHWMTU)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer func() {
		if err := tsi.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	// Connect one raw client, then drop the socket from its end to simulate
	// the remote process dying without a clean shutdown.
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}
	waitForSpawns(t, 2*time.Second, &mu, &spawned, 1)

	tsi.mu.Lock()
	have := len(tsi.spawnedInterfaces)
	tsi.mu.Unlock()
	if have != 1 {
		t.Fatalf("expected 1 spawned client before close, got %d", have)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("client close failed: %v", err)
	}

	// The server must prune the dead client within a short grace period (the
	// client's readLoop unblocks on EOF and tears down via failConn).
	deadline := time.Now().Add(3 * time.Second)
	for {
		tsi.mu.Lock()
		n := len(tsi.spawnedInterfaces)
		live := 0
		for _, ci := range tsi.spawnedInterfaces {
			if ci.Status() {
				live++
			}
		}
		tsi.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"dead spawned client not pruned after close: retained=%d live=%d",
				n, live)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRemoveSpawnedIdempotent verifies removeSpawned is safe to call multiple
// times for the same client (failConn can race Detach) and never panics or
// corrupts the slice when the entry is absent.
func TestRemoveSpawnedIdempotent(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	tsi, err := newTCPServerInterface("prune-idem-server", "127.0.0.1", port,
		func(data []byte, iface Interface) {}, func(iface Interface) {}, TCPHWMTU)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer func() {
		if err := tsi.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	bi := NewBaseInterface("ghost", ModeFull, TCPBitrateGuess)
	ghost := &TCPClientInterface{BaseInterface: bi, spawned: true}

	// Absent entry: must be a harmless no-op.
	tsi.removeSpawned(ghost)
	tsi.removeSpawned(nil)

	tsi.mu.Lock()
	tsi.spawnedInterfaces = append(tsi.spawnedInterfaces, ghost)
	tsi.mu.Unlock()

	tsi.removeSpawned(ghost)
	tsi.removeSpawned(ghost)

	tsi.mu.Lock()
	n := len(tsi.spawnedInterfaces)
	tsi.mu.Unlock()
	if n != 0 {
		t.Fatalf("expected empty list after idempotent removals, got %d", n)
	}
}

// TestSpawnedOnRemoveFiresOnWireDrop verifies a spawned client's onRemove hook
// fires when the remote end drops the socket (EOF observed while up), and that
// the parent prunes it from the spawn list. The transport layer installs this
// hook via RegisterInterface so dead interfaces leave ts.interfaces exactly as
// Python's Transport.remove_interface does during remote-client teardown.
func TestSpawnedOnRemoveFiresOnWireDrop(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	spawnedCh := make(chan *TCPClientInterface, 1)
	onConnect := func(iface Interface) {
		if ci, ok := iface.(*TCPClientInterface); ok {
			select {
			case spawnedCh <- ci:
			default:
			}
		}
	}
	tsi, err := newTCPServerInterface("onremove-server", "127.0.0.1", port,
		func(data []byte, iface Interface) {}, onConnect, TCPHWMTU)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer func() {
		if err := tsi.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("client dial failed: %v", err)
	}

	var client *TCPClientInterface
	select {
	case client = <-spawnedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server never spawned the connected client")
	}

	fired := make(chan struct{}, 4)
	client.SetOnRemove(func() { fired <- struct{}{} })
	// parentServer is already set by handleConnection before connectHandler
	// fires, so no need (nor safe — it races failConn's read) to set it here.

	if err := conn.Close(); err != nil {
		t.Fatalf("client close failed: %v", err)
	}

	select {
	case <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("onRemove did not fire after wire drop")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		tsi.mu.Lock()
		n := len(tsi.spawnedInterfaces)
		tsi.mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("spawn list still holds %d entries after teardown", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForSpawns polls until the spawned-counter reaches want or the timeout
// expires, failing the test either way.
func waitForSpawns(t *testing.T, timeout time.Duration, mu *sync.Mutex, counter *int, want int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		mu.Lock()
		got := *counter
		mu.Unlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d spawns, saw %d", want, got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
