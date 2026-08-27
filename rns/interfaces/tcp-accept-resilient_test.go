// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// TestTCPServerAcceptLoopSurvivesTransientErrors verifies the server's accept
// loop keeps serving after transient Accept failures instead of breaking
// forever with running==1 — the silent "new inbound connections hang while
// everything looks up" mode that gradually rotted fleet path tables.
//
// The real listener keeps accepting beneath the seam; the injected accept
// function fails twice (EMFILE-style) then delegates to the real listener, so
// a subsequent successful dial proves the loop is still alive and spawning.
func TestTCPServerAcceptLoopSurvivesTransientErrors(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	spawnedCh := make(chan Interface, 1)
	tsi, err := newTCPServerInterface("accept-resilience", "127.0.0.1", port,
		func(data []byte, iface Interface) {},
		func(iface Interface) {
			select {
			case spawnedCh <- iface:
			default:
			}
		}, TCPHWMTU)
	if err != nil {
		t.Fatalf("server listen failed: %v", err)
	}
	defer func() {
		if err := tsi.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	var failures atomic.Int32
	realAccept := acceptFunc(tsi.listener.Accept)
	wrapped := acceptFunc(func() (net.Conn, error) {
		if failures.Add(1) <= 2 {
			return nil, errors.New("accept4: too many open files")
		}
		return realAccept()
	})
	tsi.accept.Store(&wrapped)

	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 2*time.Second)
	if err != nil {
		t.Fatalf("client dial failed after injected transient errors: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case <-spawnedCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not spawn client after transient accept errors; accept loop died")
	}
}
