// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

func closeReticulum(t *testing.T, r *Reticulum) {
	t.Helper()
	if r == nil {
		return
	}
	if err := r.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("failed to close reticulum: %v", err)
	}
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort listen error: %v", err)
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("reserveTCPPort unexpected addr type: %T", l.Addr())
	}
	return addr.Port
}

func tempDir(t *testing.T) (string, func()) {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "rns-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

// newTestTransportSystem creates a minimal TransportSystem for testing.
func newTestTransportSystem(t *testing.T) *TransportSystem {
	t.Helper()
	id := mustTestNewIdentity(t, true)
	ts := NewTransportSystem()
	ts.identity = id
	return ts
}

// newTestPipes creates a pair of connected PipeInterfaces wired to the
// given transport systems and returns a cleanup func.
func newTestPipes(t *testing.T, tsA, tsB *TransportSystem) (*interfaces.PipeInterface, *interfaces.PipeInterface, func()) {
	t.Helper()
	pipeA := interfaces.NewPipeInterface("initiator", func(data []byte, iface interfaces.Interface) {
		tsA.Inbound(data, iface)
	})
	pipeB := interfaces.NewPipeInterface("receiver", func(data []byte, iface interfaces.Interface) {
		tsB.Inbound(data, iface)
	})
	pipeA.Other = pipeB
	pipeB.Other = pipeA
	cleanup := func() {
		_ = pipeA.Detach()
		_ = pipeB.Detach()
	}
	return pipeA, pipeB, cleanup
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config error: %v", err)
	}
}

func TestNewReticulumSharedInstanceServerThenClient(t *testing.T) {
	t.Parallel()
	ts1 := NewTransportSystem()
	ts2 := NewTransportSystem()

	port := reserveTCPPort(t)
	controlPort := reserveTCPPort(t)

	configTemplate := `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`

	cfg1, cleanup1 := tempDir(t)
	defer cleanup1()
	cfg2, cleanup2 := tempDir(t)
	defer cleanup2()
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, t.Name(), port, controlPort))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, t.Name(), port, controlPort))

	r1, err := NewReticulum(ts1, cfg1)
	if err != nil {
		t.Fatalf("failed to create reticulum 1: %v", err)
	}
	defer closeReticulum(t, r1)
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(ts2, cfg2)
	if err != nil {
		t.Fatalf("failed to create reticulum 2: %v", err)
	}
	defer closeReticulum(t, r2)
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}
}

func TestNewReticulumShareInstanceNoStandalone(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem()

	cfg, cleanup := tempDir(t)
	defer cleanup()
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
share_instance = No
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`, reserveTCPPort(t)))

	r, err := NewReticulum(ts, cfg)
	if err != nil {
		t.Fatalf("failed to create reticulum: %v", err)
	}
	defer closeReticulum(t, r)
	if r.isSharedInstance || r.isConnectedToSharedInstance || !r.isStandaloneInstance {
		t.Fatalf("instance role mismatch: shared=%v connected=%v standalone=%v", r.isSharedInstance, r.isConnectedToSharedInstance, r.isStandaloneInstance)
	}
}

func TestNewReticulumSharedInstanceUnixServerThenClientSameConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shared-instance transport is not used on windows")
	}

	t.Parallel()
	ts1 := NewTransportSystem()
	ts2 := NewTransportSystem()

	cfg, cleanup := tempDir(t)
	defer cleanup()
	// Use a shorter name for the socket to avoid path length limits on macOS
	instanceName := "rns-test"

	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = unix

[logging]
loglevel = 4

[interfaces]
`, instanceName))

	r1, err := NewReticulum(ts1, cfg)
	if err != nil {
		t.Fatalf("failed to create reticulum 1: %v", err)
	}
	defer closeReticulum(t, r1)
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(ts2, cfg)
	if err != nil {
		t.Fatalf("failed to create reticulum 2: %v", err)
	}
	defer closeReticulum(t, r2)
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}

	if r2.sharedInstanceInterface == nil || r2.sharedInstanceInterface.Type() != "LocalInterface" {
		t.Fatalf("expected second instance to use LocalInterface shared-instance client")
	}
}

func TestParseBoolLike(t *testing.T) {
	t.Parallel()
	truthy := []string{"1", "true", "True", "yes", "Y", "on"}
	for _, v := range truthy {
		if !parseBoolLike(v) {
			t.Fatalf("parseBoolLike(%q) = false, want true", v)
		}
	}

	falsy := []string{"0", "false", "False", "no", "N", "off", "unexpected"}
	for _, v := range falsy {
		if parseBoolLike(v) {
			t.Fatalf("parseBoolLike(%q) = true, want false", v)
		}
	}
}
