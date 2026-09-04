// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// goroutineProfileText returns the full live-goroutine profile (stacks
// included, debug=1 text form), used to confirm transport loop goroutines are
// running before shutdown.
func goroutineProfileText() string {
	p := pprof.Lookup("goroutine")
	if p == nil {
		return ""
	}
	var buf bytes.Buffer
	_ = p.WriteTo(&buf, 1)
	return buf.String()
}

// waitAroundFor polls cond until it returns true or the deadline elapses.
func waitAroundFor(cond func() bool, deadline time.Duration) bool {
	return testutils.PollUntil(deadline, cond)
}

// completesWithin runs fn on a goroutine and reports whether it returned
// before the deadline. It is the leak-check primitive: a loop that ignores its
// stop signal hangs the join (Close/Stop), so the deadline fires.
func completesWithin(fn func(), deadline time.Duration) bool {
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(deadline):
		return false
	}
}

// TestTransportLoopsExitOnStop verifies that the transport job loop
// (maintenance), the traffic-counter loop, and the shared-instance RPC
// listener loop all check the running/stop signal and exit when the
// Reticulum is closed — no goroutine leak (Python _should_run loop control,
// Transport.py:213,517,3524). Stop/Close join every background goroutine, so
// a loop that fails to exit hangs the join and trips the deadline.
//
// This test deliberately does not run in parallel so the goroutine profile
// (a process-global snapshot) reflects only this test's loops.
func TestTransportLoopsExitOnStop(t *testing.T) {
	sharedPort := reserveTCPPort(t)
	controlPort := reserveTCPPort(t)
	configDir := testutils.TempDir(t, tempDirPrefix)

	config := "[reticulum]\n" +
		"share_instance = Yes\n" +
		"shared_instance_type = tcp\n" +
		"shared_instance_port = " + strconv.Itoa(sharedPort) + "\n" +
		"instance_control_port = " + strconv.Itoa(controlPort) + "\n" +
		"\n[logging]\nloglevel = 4\n\n[interfaces]\n"
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)

	// Sanity: the loops are actually running before shutdown. The profile
	// renders receiver methods as "(*TransportSystem).maintenance" etc.
	loops := []string{
		"TransportSystem).maintenance",
		"TransportSystem).countTrafficLoop",
		"Reticulum).rpcLoop",
	}
	if !waitAroundFor(func() bool {
		prof := goroutineProfileText()
		for _, fn := range loops {
			if !strings.Contains(prof, fn) {
				return false
			}
		}
		return true
	}, 2*time.Second) {
		closeReticulum(t, r)
		t.Fatalf("one or more transport loops were not running before stop")
	}

	// Close joins maintenance (doneCh), countTrafficLoop (trafficDone), and
	// rpcLoop (rpcDone). If any loop leaks, Close hangs -> deadline fails.
	if !completesWithin(func() { closeReticulum(t, r) }, 5*time.Second) {
		t.Fatalf("Reticulum.Close hung: a transport loop did not exit on stop (goroutine leak)")
	}
}

// TestDiscoveryMonitorLoopExitsOnStop verifies that the interface
// discovery monitor goroutine checks its stop signal and exits when
// InterfaceDiscovery.Stop is called. Stop joins the monitor goroutine
// (monitorDone), so a leak hangs the join and trips the deadline.
func TestDiscoveryMonitorLoopExitsOnStop(t *testing.T) {
	configDir := testutils.TempDir(t, tempDirPrefix)
	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, configDir)
	defer closeReticulum(t, r)

	discovery := NewInterfaceDiscovery(r)
	discovery.monitorInterval = 10 * time.Millisecond
	iface := newBootstrapConstructorTestInterface("monitoree", "TCPServerInterface")
	discovery.monitorInterface(iface)

	if !waitAroundFor(func() bool {
		return strings.Contains(goroutineProfileText(), "InterfaceDiscovery).monitorLoop")
	}, 2*time.Second) {
		t.Fatalf("monitorLoop was not running before stop")
	}

	// Stop joins monitorLoop via monitorDone; a leak hangs Stop.
	if !completesWithin(discovery.Stop, 5*time.Second) {
		t.Fatalf("InterfaceDiscovery.Stop hung: monitorLoop did not exit on stop (goroutine leak)")
	}
}
