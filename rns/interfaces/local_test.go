// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func allowClosedNetworkErr(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "closed network connection")
}

func TestLocalUnixServerClientLifecycleAndRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets not supported on windows")
	}

	received := make(chan []byte, 2)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	tmp := testutils.TempDir(t, "go-ret-local-*")

	socketPath := filepath.Join(tmp, "local.sock")

	server := mustTestNewLocalServerInterface(t, "local-server", socketPath, 0, handler)

	client, err := NewLocalClientInterface("local-client", socketPath, 0, nil)
	if err != nil {
		_ = server.Detach()
		t.Fatalf("NewLocalClientInterface first error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	msg1 := []byte("hello-unix-1")
	if err := client.Send(msg1); err != nil {
		_ = client.Detach()
		_ = server.Detach()
		t.Fatalf("client.Send first error: %v", err)
	}

	select {
	case got := <-received:
		if !bytes.Equal(got, msg1) {
			t.Fatalf("first receive mismatch: got %q want %q", got, msg1)
		}
	case <-time.After(750 * time.Millisecond):
		_ = client.Detach()
		_ = server.Detach()
		t.Fatalf("timeout waiting for first local unix payload")
	}

	if err := client.Detach(); err != nil {
		_ = server.Detach()
		t.Fatalf("client.Detach first error: %v", err)
	}
	if err := server.Detach(); !allowClosedNetworkErr(err) {
		t.Fatalf("server.Detach first error: %v", err)
	}

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket path removed after detach, stat err=%v", err)
	}

	server2 := mustTestNewLocalServerInterface(t, "local-server-2", socketPath, 0, handler)

	client2, err := NewLocalClientInterface("local-client-2", socketPath, 0, nil)
	if err != nil {
		_ = server2.Detach()
		t.Fatalf("NewLocalClientInterface restart error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	msg2 := []byte("hello-unix-2")
	if err := client2.Send(msg2); err != nil {
		_ = client2.Detach()
		_ = server2.Detach()
		t.Fatalf("client.Send restart error: %v", err)
	}

	select {
	case got := <-received:
		if !bytes.Equal(got, msg2) {
			t.Fatalf("restart receive mismatch: got %q want %q", got, msg2)
		}
	case <-time.After(750 * time.Millisecond):
		t.Fatalf("timeout waiting for restart local unix payload")
	}

	if err := client2.Detach(); err != nil {
		_ = server2.Detach()
		t.Fatalf("client2.Detach error: %v", err)
	}
	if err := server2.Detach(); !allowClosedNetworkErr(err) {
		t.Fatalf("server2.Detach error: %v", err)
	}
}

func TestLocalServerRemovesStaleSocketPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets not supported on windows")
	}

	tmp := testutils.TempDir(t, "go-ret-local-stale-*")

	socketPath := filepath.Join(tmp, "stale.sock")

	if err := os.WriteFile(socketPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile stale socket placeholder error: %v", err)
	}

	server := mustTestNewLocalServerInterface(t, "local-server-stale", socketPath, 0, nil)

	if err := server.Detach(); !allowClosedNetworkErr(err) {
		t.Fatalf("server.Detach error: %v", err)
	}

	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale socket path removed after detach, stat err=%v", err)
	}
}

func TestLocalServerRejectsTakeoverWhenSocketActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets not supported on windows")
	}

	tmp := testutils.TempDir(t, "go-ret-local-active-*")

	socketPath := filepath.Join(tmp, "active.sock")

	server := mustTestNewLocalServerInterface(t, "local-server-1", socketPath, 0, nil)
	defer func() { _ = server.Detach() }()

	if server2, err := NewLocalServerInterface("local-server-2", socketPath, 0, nil); err == nil {
		_ = server2.Detach()
		t.Fatalf("expected second local server creation to fail while first server is active")
	}
}

// drainPipe continuously reads frames from r into the returned channel until r
// is closed. net.Pipe writes block until the reader consumes the bytes, so a
// background drainer is required to exercise the LocalClientInterface Send path
// synchronously.
func drainPipe(r net.Conn) <-chan []byte {
	ch := make(chan []byte, 16)
	go func() {
		defer close(ch)
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				select {
				case ch <- cp:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// newTestLocalClientWithConn builds a LocalClientInterface backed by clientConn
// (one end of a net.Pipe) with running=1, for deterministic exercise of the
// Android client_sleep gating logic (LocalInterface.py:91-106,221-222,295).
func newTestLocalClientWithConn(t *testing.T, clientConn net.Conn) *LocalClientInterface {
	t.Helper()
	lci := &LocalClientInterface{
		BaseInterface:  NewBaseInterface("test-local", ModeFull, LocalBitrate),
		conn:           clientConn,
		inboundHandler: func([]byte, Interface) {},
	}
	atomic.StoreInt32(&lci.running, 1)
	return lci
}

// TestLocalClientSleepPauseAndKeepalive verifies the Android client_sleep
// behaviour ported from RNS/Interfaces/LocalInterface.py:
//   - process_outgoing drops the outbound packet when pause_on_client_sleep is
//     set and time.time() > pause_timeout (LocalInterface.py:221-222),
//   - receive refreshes pause_timeout to time.time()+CLIENT_SLEEP_PAUSE_TIMEOUT
//     (LocalInterface.py:295),
//   - send_keepalive emits an HDLC FLAG+FLAG frame (LocalInterface.py:195-203).
//
// The CLIENT_SLEEP_PAUSE_TIMEOUT window is 12 seconds
// (LocalInterface.py:65).
func TestLocalClientSleepPauseAndKeepalive(t *testing.T) {
	t.Parallel()

	payload := []byte("client-sleep-payload!!")

	t.Run("outbound drops after the pause window", func(t *testing.T) {
		t.Parallel()
		clientConn, readEnd := net.Pipe()
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = readEnd.Close() }()
		frames := drainPipe(readEnd)

		lci := newTestLocalClientWithConn(t, clientConn)
		t0 := time.Unix(5_000_000, 0)
		lci.pauseOnClientSleep = true
		lci.pauseTimeout = t0

		// Just before the window expires the send must go through.
		if err := lci.sendAt(payload, t0.Add(-1*time.Second)); err != nil {
			t.Fatalf("sendAt before window: %v", err)
		}
		select {
		case f := <-frames:
			if !bytes.Contains(f, payload) {
				t.Fatalf("pre-window frame % x missing payload %q", f, payload)
			}
		case <-time.After(time.Second):
			t.Fatal("pre-window send did not produce a frame")
		}

		// After the window the packet is dropped silently (nil error, no frame).
		if err := lci.sendAt(payload, t0.Add(1*time.Second)); err != nil {
			t.Fatalf("sendAt after window: want nil drop, got %v", err)
		}
		select {
		case f := <-frames:
			t.Fatalf("post-window send unexpectedly produced a frame: % x", f)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("pause disabled never drops", func(t *testing.T) {
		t.Parallel()
		clientConn, readEnd := net.Pipe()
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = readEnd.Close() }()
		frames := drainPipe(readEnd)

		lci := newTestLocalClientWithConn(t, clientConn)
		lci.pauseOnClientSleep = false
		lci.pauseTimeout = time.Unix(1, 0) // long expired

		// With pause_on_client_sleep disabled, an expired pause_timeout must not
		// gate the send (LocalInterface.py:221 guard).
		if err := lci.Send(payload); err != nil {
			t.Fatalf("Send with pause disabled: %v", err)
		}
		select {
		case f := <-frames:
			if !bytes.Contains(f, payload) {
				t.Fatalf("frame % x missing payload %q", f, payload)
			}
		case <-time.After(time.Second):
			t.Fatal("Send with pause disabled produced no frame")
		}
	})

	t.Run("refresh on receive extends the window", func(t *testing.T) {
		t.Parallel()
		clientConn, readEnd := net.Pipe()
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = readEnd.Close() }()
		frames := drainPipe(readEnd)

		lci := newTestLocalClientWithConn(t, clientConn)
		t0 := time.Unix(5_000_000, 0)
		lci.pauseOnClientSleep = true
		lci.pauseTimeout = t0

		// 13s after t0 would drop, but a receive at t0+5s pushes the window out
		// to t0+5s+12s = t0+17s (LocalInterface.py:295).
		lci.refreshPauseTimeoutAt(t0.Add(5 * time.Second))

		// t0+16s is within the refreshed window: the send must go through.
		if err := lci.sendAt(payload, t0.Add(16*time.Second)); err != nil {
			t.Fatalf("sendAt within refreshed window: %v", err)
		}
		select {
		case f := <-frames:
			if !bytes.Contains(f, payload) {
				t.Fatalf("refreshed-window frame % x missing payload %q", f, payload)
			}
		case <-time.After(time.Second):
			t.Fatal("refreshed-window send produced no frame")
		}

		// t0+18s is past the refreshed window: drop.
		if err := lci.sendAt(payload, t0.Add(18*time.Second)); err != nil {
			t.Fatalf("sendAt past refreshed window: want nil drop, got %v", err)
		}
		select {
		case f := <-frames:
			t.Fatalf("post-refresh-window send unexpectedly produced a frame: % x", f)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("sendKeepalive writes a two-flag frame", func(t *testing.T) {
		t.Parallel()
		clientConn, readEnd := net.Pipe()
		defer func() { _ = clientConn.Close() }()
		defer func() { _ = readEnd.Close() }()
		frames := drainPipe(readEnd)

		lci := newTestLocalClientWithConn(t, clientConn)

		if err := lci.sendKeepalive(); err != nil {
			t.Fatalf("sendKeepalive: %v", err)
		}
		select {
		case f := <-frames:
			want := []byte{HDLCFlag, HDLCFlag}
			if !bytes.Equal(f, want) {
				t.Fatalf("keepalive frame = % x, want % x", f, want)
			}
		case <-time.After(time.Second):
			t.Fatal("sendKeepalive produced no frame")
		}
	})
}

// TestLocalSpawnedClientTearsDownOnDisconnect is the regression test for the
// CPU hot-loop: a spawned (server-accepted) local client that disconnects must
// be torn down and forgotten by the server, never reconnected. Mirrors
// LocalInterface.py:313-314 (spawned -> teardown(nowarning=True)) and 345-361
// (remove from local_client_interfaces, parent.clients -= 1). The previous Go
// code unconditionally ran reconnectLoop for spawned clients with a zero
// backoff, busy-dialing 127.0.0.1:0 forever at ~100% CPU.
func TestLocalSpawnedClientTearsDownOnDisconnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets not supported on windows")
	}

	handler := func(data []byte, iface Interface) {}
	tmp := testutils.TempDir(t, "go-ret-local-spawn-*")
	socketPath := filepath.Join(tmp, "local.sock")

	server := mustTestNewLocalServerInterface(t, "local-server-spawn", socketPath, 0, handler)
	defer func() { _ = server.Detach() }()

	// Simulate another co-located Reticulum client connecting to the shared
	// instance. The server accepts it and spawns a LocalClientInterface.
	rawClient, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial shared instance: %v", err)
	}
	defer func() { _ = rawClient.Close() }()

	// Wait for the server to accept and register the spawned client.
	var spawned *LocalClientInterface
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		if len(server.spawnedInterfaces) > 0 {
			spawned = server.spawnedInterfaces[0]
		}
		clients := server.clients
		server.mu.Unlock()
		if spawned != nil {
			if clients != 1 {
				t.Fatalf("server.clients = %d, want 1 after accept", clients)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if spawned == nil {
		t.Fatalf("server never accepted the spawned client")
	}
	if spawned.isConnectedToSharedInstance {
		t.Fatalf("spawned client flagged as initiator; must be a spawned (non-initiator) client")
	}

	// Disconnect the remote client. The spawned interface's read loop must
	// tear down (not reconnect) and remove itself from the server's list.
	if err := rawClient.Close(); err != nil && !allowClosedNetworkErr(err) {
		t.Fatalf("close raw client: %v", err)
	}

	// The server must forget the spawned client (Python removes it from
	// local_client_interfaces). The old buggy code left it registered forever
	// while reconnectLoop busy-dialled, so this would time out and fail.
	deadline = time.Now().Add(2 * time.Second)
	forgotten := false
	for time.Now().Before(deadline) {
		if len(server.SpawnedClientInterfaces()) == 0 {
			forgotten = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !forgotten {
		t.Fatalf("spawned client was not removed from server.spawnedInterfaces after disconnect (busy reconnect loop?)")
	}

	if atomic.LoadInt32(&spawned.running) != 0 {
		t.Fatalf("spawned client still running after teardown: running=%d", atomic.LoadInt32(&spawned.running))
	}
	server.mu.Lock()
	clients := server.clients
	server.mu.Unlock()
	if clients != 0 {
		t.Fatalf("server.clients = %d after teardown, want 0", clients)
	}
}

// TestLocalClientReconnectWaitMatchesPython locks the initiator reconnect
// backoff to Reticulum's RECONNECT_WAIT = 8 (LocalInterface.py:63). The Go port
// previously used a 5s per-instance value.
func TestLocalClientReconnectWaitMatchesPython(t *testing.T) {
	if LocalClientReconnectWait != 8*time.Second {
		t.Fatalf("LocalClientReconnectWait = %v, want 8s (LocalInterface.py:63 RECONNECT_WAIT)", LocalClientReconnectWait)
	}
}
