// Copyright (c) 2024 Glenn Lewis. All rights reserved.
// Use of this source code is governed by the Reticulum License.

package rns

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// sharedRoleConfigTemplate uses an AF_UNIX shared instance with a per-test
// instance name and a per-test control port, so parallel tests cannot reach
// each other's instance, the developer's own live instance, or another test's
// RPC listener. The shared-instance type is explicit because the default
// transport is TCP loopback on non-Linux hosts, which would attach these tests
// to whatever instance already owns that port. On Linux the address is the
// abstract socket @rns/<instance_name>, which is what a Linux node such as the
// raspberrypi actually uses.
const sharedRoleConfigTemplate = `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = unix
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`

func writeSharedRoleConfig(t *testing.T, dir, instanceName string, controlPort int) {
	t.Helper()
	writeConfig(t, dir, fmt.Sprintf(sharedRoleConfigTemplate, instanceName, controlPort))
}

// standaloneRoleConfigTemplate is a per-test standalone configuration: the
// same isolation as sharedRoleConfigTemplate, with sharing switched off.
const standaloneRoleConfigTemplate = `[reticulum]
instance_name = %v
share_instance = No
shared_instance_type = unix
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`

// TestRequireSharedInstanceHonorsStandaloneConfig covers the regression that
// broke the whole integration suite: a tool asked to require a shared instance
// on a configuration that says share_instance = No (which is what every
// subprocess integration test writes, because the tools under test are peers
// on a private loopback interface rather than clients of a daemon) refused to
// start at all. Python never consults require_shared_instance on that branch —
// share_instance = No makes the process standalone (Reticulum.py:448-452) — so
// the tool must come up standalone, owning its own interfaces, instead of
// demanding an instance the configuration never described.
func TestRequireSharedInstanceHonorsStandaloneConfig(t *testing.T) {
	t.Parallel()

	cfg := tempDir(t)
	writeConfig(t, cfg, fmt.Sprintf(standaloneRoleConfigTemplate, "rns-role-standalone", 47314))

	tool, err := NewReticulum(NewTransportSystem(nil), cfg, WithRequireSharedInstance())
	if err != nil {
		t.Fatalf("NewReticulum() on a share_instance = No config returned error: %v", err)
	}
	defer closeReticulum(t, tool)
	if !tool.IsStandaloneInstance() {
		t.Fatalf("expected the tool to run standalone, got shared=%v connected=%v",
			tool.IsSharedInstance(), tool.IsConnectedToSharedInstance())
	}
	if tool.IsSharedInstance() || tool.IsConnectedToSharedInstance() {
		t.Fatal("a share_instance = No config must not share or attach to an instance")
	}
}

// TestRequireSharedInstanceConfigKeyHonorsStandaloneConfig pins the same
// regression for the config-file spelling of the flag: require_shared_instance
// in the [reticulum] section behaves exactly like the constructor option, so a
// standalone configuration keeps running standalone with it set.
func TestRequireSharedInstanceConfigKeyHonorsStandaloneConfig(t *testing.T) {
	t.Parallel()

	cfg := tempDir(t)
	writeConfig(t, cfg, fmt.Sprintf(
		"[reticulum]\ninstance_name = %v\nshare_instance = No\nshared_instance_type = unix\ninstance_control_port = %v\nrequire_shared_instance = Yes\n\n[logging]\nloglevel = 4\n\n[interfaces]\n",
		"rns-role-standalone-key", 47315))

	r, err := NewReticulum(NewTransportSystem(nil), cfg)
	if err != nil {
		t.Fatalf("NewReticulum() on a share_instance = No + require_shared_instance = Yes config returned error: %v", err)
	}
	defer closeReticulum(t, r)
	if !r.IsStandaloneInstance() {
		t.Fatalf("expected the instance to run standalone, got shared=%v connected=%v",
			r.IsSharedInstance(), r.IsConnectedToSharedInstance())
	}
}

// TestRequireSharedInstanceToolFailsWithoutRunningInstance covers the incident
// this option exists for: a short-lived tool must report that no shared instance
// is running instead of becoming one, because an instance created by a tool
// disappears when the tool exits and leaves every process attached to it without
// a network stack.
func TestRequireSharedInstanceToolFailsWithoutRunningInstance(t *testing.T) {
	t.Parallel()

	cfg := tempDir(t)
	writeSharedRoleConfig(t, cfg, "rns-role-no-instance", 47311)

	_, err := NewReticulum(NewTransportSystem(nil), cfg, WithRequireSharedInstance())
	if err == nil {
		t.Fatal("expected an error when no shared instance is running")
	}
	if !strings.Contains(err.Error(), "shared instance") {
		t.Fatalf("unexpected error text: %v", err)
	}

	// The failed tool must have created nothing: the next process on this
	// configuration is still able to become the shared instance.
	owner, err := NewReticulum(NewTransportSystem(nil), cfg)
	if err != nil {
		t.Fatalf("failed to create the owning instance: %v", err)
	}
	defer closeReticulum(t, owner)
	if !owner.IsSharedInstance() {
		t.Fatalf("expected the application to become the shared instance, got connected=%v standalone=%v",
			owner.IsConnectedToSharedInstance(), owner.IsStandaloneInstance())
	}
}

// TestRequireSharedInstanceToolAttachesAndNeverOwns asserts that a caller that
// requires a shared instance attaches to a running one and reports the client
// role, so a probe can observe the live network without redefining it.
func TestRequireSharedInstanceToolAttachesAndNeverOwns(t *testing.T) {
	t.Parallel()

	// A tool and the application it probes share one configuration directory,
	// as they do on a real node. With an AF_UNIX shared instance the socket
	// address is derived from the configuration directory on non-Linux hosts,
	// so separate directories would describe separate instances.
	cfg := tempDir(t)
	writeSharedRoleConfig(t, cfg, "rns-role-attach", 47312)

	owner, err := NewReticulum(NewTransportSystem(nil), cfg)
	if err != nil {
		t.Fatalf("failed to create the owning instance: %v", err)
	}
	defer closeReticulum(t, owner)
	if !owner.IsSharedInstance() {
		t.Fatal("expected the first instance to own the shared instance")
	}

	tool, err := NewReticulum(NewTransportSystem(nil), cfg, WithRequireSharedInstance())
	if err != nil {
		t.Fatalf("failed to attach the tool to the shared instance: %v", err)
	}
	defer closeReticulum(t, tool)
	if !tool.IsConnectedToSharedInstance() {
		t.Fatal("expected the tool to attach to the running shared instance")
	}
	if tool.IsSharedInstance() {
		t.Fatal("a caller that requires a shared instance must never become one")
	}
}

// TestAttachedClientTakesOverWhenSharedInstanceStops covers the recovery path:
// when the instance a long-running application attached to stops, the
// application must not be left with a client interface that can never transmit.
// The watcher detaches it, re-runs the role decision and takes ownership.
func TestAttachedClientTakesOverWhenSharedInstanceStops(t *testing.T) {
	t.Parallel()

	cfg := tempDir(t)
	writeSharedRoleConfig(t, cfg, "rns-role-takeover", 47313)

	owner, err := NewReticulum(NewTransportSystem(nil), cfg)
	if err != nil {
		t.Fatalf("failed to create the owning instance: %v", err)
	}
	ownerClosed := false
	defer func() {
		if !ownerClosed {
			closeReticulum(t, owner)
		}
	}()
	if !owner.IsSharedInstance() {
		t.Fatal("expected the first instance to own the shared instance")
	}

	app, err := NewReticulum(NewTransportSystem(nil), cfg,
		withSharedInstanceWatchInterval(2*time.Millisecond),
		withSharedInstanceMissThreshold(2))
	if err != nil {
		t.Fatalf("failed to attach the application to the shared instance: %v", err)
	}
	defer closeReticulum(t, app)
	if !app.IsConnectedToSharedInstance() {
		t.Fatal("expected the application to attach to the shared instance")
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("failed to close the owning instance: %v", err)
	}
	ownerClosed = true

	// The watcher recovers within a few polling intervals. The deadline is
	// generous because this test also runs under the race detector alongside
	// the rest of the package, where goroutine scheduling can be delayed by
	// seconds; the recovery itself takes milliseconds.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !app.IsSharedInstance() {
		time.Sleep(2 * time.Millisecond)
	}
	if !app.IsSharedInstance() {
		t.Fatalf("expected the attached client to take over, got connected=%v standalone=%v",
			app.IsConnectedToSharedInstance(), app.IsStandaloneInstance())
	}

	// Owning the network also means serving local RPC: other local programs
	// attach to this instance over the shared-instance socket and then ask it
	// for interface stats, the path table and blackhole queries. An instance
	// that took over without an RPC listener would accept those clients and
	// fail every call, so probe the listener the way such a client does. The
	// listener is started a few steps after the role flips, and the closed
	// owner's socket file can still be on disk until it is rebound, so poll
	// with the same generous deadline as the role itself.
	deadline = time.Now().Add(15 * time.Second)
	var rpcErr error
	for time.Now().Before(deadline) {
		if _, rpcErr = app.callRPC(map[string]any{"get": "link_count"}); rpcErr == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rpcErr != nil {
		t.Fatalf("taken-over instance does not serve local RPC: %v", rpcErr)
	}
}

// TestAttachedClientTakesOverWhenInstanceStopsDuringAttach covers the recovery
// of TestAttachedClientTakesOverWhenSharedInstanceStops at the moment that used
// to break it: the owner is closed immediately after the client attaches, with
// no settling time, so the server may still be accepting that client's
// connection when the owner detaches. Detaching then closed the listener and a
// spawned list the connection had not reached yet, leaving it open and unowned:
// the client kept running=1 with a read loop blocked on a socket nothing would
// ever close, so its watcher never saw the instance stop and never took over.
// This surfaced in CI as "expected the attached client to take over, got
// connected=true standalone=false" after the full 15s deadline. The
// interface-level tests (TestLocalServerClosesConnectionAcceptedWhileDetaching
// and TestLocalServerDetachClosesEveryAcceptedConnection) pin the defect
// deterministically; this one pins the end-to-end contract, repeating the
// attach/close pair to vary the interleaving.
func TestAttachedClientTakesOverWhenInstanceStopsDuringAttach(t *testing.T) {
	t.Parallel()

	for i := range 3 {
		cfg := tempDir(t)
		writeSharedRoleConfig(t, cfg, fmt.Sprintf("rns-role-takeover-during-attach-%v", i), 47330+i)

		owner, err := NewReticulum(NewTransportSystem(nil), cfg)
		if err != nil {
			t.Fatalf("iteration %v: failed to create the owning instance: %v", i, err)
		}
		t.Cleanup(func() { closeReticulum(t, owner) })
		if !owner.IsSharedInstance() {
			t.Fatalf("iteration %v: expected the first instance to own the shared instance", i)
		}

		app, err := NewReticulum(NewTransportSystem(nil), cfg,
			withSharedInstanceWatchInterval(2*time.Millisecond),
			withSharedInstanceMissThreshold(2))
		if err != nil {
			t.Fatalf("iteration %v: failed to attach the application to the shared instance: %v", i, err)
		}
		t.Cleanup(func() { closeReticulum(t, app) })
		if !app.IsConnectedToSharedInstance() {
			t.Fatalf("iteration %v: expected the application to attach to the shared instance", i)
		}

		if err := owner.Close(); err != nil {
			t.Fatalf("iteration %v: failed to close the owning instance: %v", i, err)
		}

		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !app.IsSharedInstance() {
			time.Sleep(2 * time.Millisecond)
		}
		if !app.IsSharedInstance() {
			t.Fatalf("iteration %v: expected the attached client to take over, got connected=%v standalone=%v",
				i, app.IsConnectedToSharedInstance(), app.IsStandaloneInstance())
		}
	}
}

// TestSharedInstanceWatcherSurvivesAnInFlightTakeover pins the state the
// recovery watcher must not mistake for its own completion. A takeover clears
// the client interface before it re-decides the role (takeOverSharedInstance),
// and that decision is not instant: it binds a server and may then make up to
// four attach attempts with waits between them. The watcher can therefore tick
// with the interface cleared while the role still says "connected to a shared
// instance". Reading the cleared interface as "this process owns the instance
// now" ended the watch inside its own recovery — so when the attempt re-attached
// as a client rather than taking over, nothing was left to notice the next loss,
// and the instance kept reporting the client role with no recovery behind it.
// This surfaced as a rare failure of
// TestAttachedClientTakesOverWhenSharedInstanceStops in the full suite.
func TestSharedInstanceWatcherSurvivesAnInFlightTakeover(t *testing.T) {
	t.Parallel()

	cfg := tempDir(t)
	writeSharedRoleConfig(t, cfg, "rns-role-watch-window", 47317)

	owner, err := NewReticulum(NewTransportSystem(nil), cfg)
	if err != nil {
		t.Fatalf("failed to create the owning instance: %v", err)
	}
	ownerClosed := false
	defer func() {
		if !ownerClosed {
			closeReticulum(t, owner)
		}
	}()
	if !owner.IsSharedInstance() {
		t.Fatal("expected the first instance to own the shared instance")
	}

	app, err := NewReticulum(NewTransportSystem(nil), cfg,
		withSharedInstanceWatchInterval(2*time.Millisecond),
		withSharedInstanceMissThreshold(2))
	if err != nil {
		t.Fatalf("failed to attach the application to the shared instance: %v", err)
	}
	defer closeReticulum(t, app)
	if !app.IsConnectedToSharedInstance() {
		t.Fatal("expected the application to attach to the shared instance")
	}

	// The in-flight state: interface cleared, role not yet re-decided. This
	// reaches the field directly rather than through
	// currentSharedInstanceInterface/setInstanceRole because the read and the
	// clear must be one critical section: split across two accessor calls, a
	// watcher tick landing in between could publish a fresh interface that the
	// clear would then discard, breaking the very window this simulates.
	app.mu.Lock()
	iface := app.sharedInstanceInterface
	app.sharedInstanceInterface = nil
	app.mu.Unlock()
	if iface == nil {
		t.Fatal("expected the attached application to hold a shared-instance interface")
	}

	// Let the watcher tick through it several times (2ms interval).
	time.Sleep(60 * time.Millisecond)

	// The attempt re-attached as a client rather than taking over: the watch
	// must still be running, so the next loss is recovered. Same single
	// critical section as above, for the same reason.
	app.mu.Lock()
	app.sharedInstanceInterface = iface
	app.mu.Unlock()

	if err := owner.Close(); err != nil {
		t.Fatalf("failed to close the owning instance: %v", err)
	}
	ownerClosed = true

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) && !app.IsSharedInstance() {
		time.Sleep(2 * time.Millisecond)
	}
	if !app.IsSharedInstance() {
		t.Fatalf("the watch ended while a takeover was in flight: got connected=%v standalone=%v",
			app.IsConnectedToSharedInstance(), app.IsStandaloneInstance())
	}
}

// TestRequireSharedInstanceToolNeverTakesOverSharedInstance covers the other
// half of the contract: a caller that requires a shared instance must not
// recover its loss by becoming the shared instance. Python aborts rather than
// take over (Reticulum.py:403-406,445-446), and a takeover by a short-lived
// process would be torn down again when the tool exits, stranding every client
// that attached to it in the meantime — the incident this option exists for. It
// keeps a reconnecting client instead (LocalInterface.py:160-192), so the
// application case that DOES take over is covered by
// TestAttachedClientTakesOverWhenSharedInstanceStops.
func TestRequireSharedInstanceToolNeverTakesOverSharedInstance(t *testing.T) {
	t.Parallel()

	cfg := tempDir(t)
	writeSharedRoleConfig(t, cfg, "rns-role-tool-takeover", 47316)

	owner, err := NewReticulum(NewTransportSystem(nil), cfg)
	if err != nil {
		t.Fatalf("failed to create the owning instance: %v", err)
	}
	ownerClosed := false
	defer func() {
		if !ownerClosed {
			closeReticulum(t, owner)
		}
	}()
	if !owner.IsSharedInstance() {
		t.Fatal("expected the first instance to own the shared instance")
	}

	tool, err := NewReticulum(NewTransportSystem(nil), cfg, WithRequireSharedInstance(),
		withSharedInstanceWatchInterval(2*time.Millisecond),
		withSharedInstanceMissThreshold(2))
	if err != nil {
		t.Fatalf("failed to attach the tool to the shared instance: %v", err)
	}
	defer closeReticulum(t, tool)
	if !tool.IsConnectedToSharedInstance() {
		t.Fatal("expected the tool to attach to the running shared instance")
	}
	if tool.watchDone != nil {
		t.Fatal("a caller that requires a shared instance must not run the shared-instance recovery watcher")
	}

	if err := owner.Close(); err != nil {
		t.Fatalf("failed to close the owning instance: %v", err)
	}
	ownerClosed = true

	// The watcher that an ordinary application would run takes over within a
	// few milliseconds on this configuration
	// (TestAttachedClientTakesOverWhenSharedInstanceStops). Observe for far
	// longer than that: the tool must still never become the shared instance.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tool.IsSharedInstance() {
			t.Fatal("a caller that requires a shared instance became the shared instance after it stopped")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if tool.IsSharedInstance() || tool.IsStandaloneInstance() {
		t.Fatalf("expected the tool to stay a client, got shared=%v standalone=%v",
			tool.IsSharedInstance(), tool.IsStandaloneInstance())
	}
}

// TestIsTransientRPCErrorTreatsDialFailuresAsTransient pins the classification
// that lets a local program retry while a shared instance is starting or taking
// over, instead of reporting a hard failure for a socket that is about to
// exist. The socket is unbound for a moment during a takeover, and a dial then
// fails with ECONNREFUSED on TCP or ENOENT on a Unix socket path. Genuine
// protocol errors must stay hard failures.
func TestIsTransientRPCErrorTreatsDialFailuresAsTransient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "connection refused", err: errors.New("dial unix /tmp/x.sock: connect: connection refused"), want: true},
		{name: "socket not yet bound", err: errors.New("dial unix /tmp/x.sock: connect: no such file or directory"), want: true},
		{name: "socket replaced", err: errors.New("dial unix /tmp/x.sock: connect: not a directory"), want: true},
		{name: "eof", err: io.EOF, want: true},
		{name: "unexpected eof", err: io.ErrUnexpectedEOF, want: true},
		{name: "closed", err: net.ErrClosed, want: true},
		{name: "reset", err: errors.New("read unix /tmp/x.sock: connection reset by peer"), want: true},
		{name: "broken pipe", err: errors.New("write unix /tmp/x.sock: broken pipe"), want: true},
		{name: "protocol error", err: errors.New("rpc: unknown response type"), want: false},
		{name: "bad key", err: errors.New("rpc key unavailable"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isTransientRPCError(tt.err); got != tt.want {
				t.Fatalf("isTransientRPCError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
