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
