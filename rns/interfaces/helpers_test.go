// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func allocateUDPPortPair(t *testing.T) (int, int) {
	t.Helper()
	p1 := testutils.ReserveUDPPort(t)
	p2 := testutils.ReserveUDPPort(t)
	return p1, p2
}

// waitUntil polls cond every few milliseconds until it returns true or timeout
// elapses. It returns the final cond() value. Use it in place of a fixed
// time.Sleep before an async assertion so the test waits exactly as long as
// needed and never fatals merely because a fixed delay was too short under
// scheduler load.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// waitForIfaceRunning polls a client interface's Status() until it reports
// running (connection established) or timeout elapses, replacing the fragile
// fixed "time.Sleep before Send" pattern that can fatal with "not running"
// under scheduler load when the connect has not completed in time.
func waitForIfaceRunning(t *testing.T, iface Interface, timeout time.Duration) {
	t.Helper()
	type runner interface{ Status() bool }
	r, ok := iface.(runner)
	if !ok {
		// No Status() to poll; fall back to a short fixed wait.
		time.Sleep(100 * time.Millisecond)
		return
	}
	if !waitUntil(timeout, r.Status) {
		t.Fatalf("interface did not report running within %v", timeout)
	}
}
