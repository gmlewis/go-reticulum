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
	"testing"
	"time"
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

	tmp, err := os.MkdirTemp("/tmp", "go-ret-local-*")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	socketPath := filepath.Join(tmp, "local.sock")

	server, err := NewLocalServerInterface("local-server", socketPath, 0, handler)
	if err != nil {
		t.Fatalf("NewLocalServerInterface first error: %v", err)
	}

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

	server2, err := NewLocalServerInterface("local-server-2", socketPath, 0, handler)
	if err != nil {
		t.Fatalf("NewLocalServerInterface restart error: %v", err)
	}

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

	tmp, err := os.MkdirTemp("/tmp", "go-ret-local-stale-*")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	socketPath := filepath.Join(tmp, "stale.sock")

	if err := os.WriteFile(socketPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("WriteFile stale socket placeholder error: %v", err)
	}

	server, err := NewLocalServerInterface("local-server-stale", socketPath, 0, nil)
	if err != nil {
		t.Fatalf("NewLocalServerInterface with stale path error: %v", err)
	}

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

	tmp, err := os.MkdirTemp("/tmp", "go-ret-local-active-*")
	if err != nil {
		t.Fatalf("MkdirTemp error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	socketPath := filepath.Join(tmp, "active.sock")

	server, err := NewLocalServerInterface("local-server-1", socketPath, 0, nil)
	if err != nil {
		t.Fatalf("NewLocalServerInterface first error: %v", err)
	}
	defer func() { _ = server.Detach() }()

	server2, err := NewLocalServerInterface("local-server-2", socketPath, 0, nil)
	if err == nil {
		_ = server2.Detach()
		t.Fatalf("expected second local server creation to fail while first server is active")
	}
}
