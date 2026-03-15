// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
	ResetTransport()
	defer ResetTransport()

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

	cfg1 := t.TempDir()
	cfg2 := t.TempDir()
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, t.Name(), port, controlPort))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, t.Name(), port, controlPort))

	r1, err := NewReticulum(cfg1)
	if err != nil {
		t.Fatalf("NewReticulum(first) error: %v", err)
	}
	defer func() { _ = r1.Close() }()
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(cfg2)
	if err != nil {
		t.Fatalf("NewReticulum(second) error: %v", err)
	}
	defer func() { _ = r2.Close() }()
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}
}

func TestNewReticulumShareInstanceNoStandalone(t *testing.T) {
	ResetTransport()
	defer ResetTransport()

	cfg := t.TempDir()
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
share_instance = No
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`, reserveTCPPort(t)))

	r, err := NewReticulum(cfg)
	if err != nil {
		t.Fatalf("NewReticulum error: %v", err)
	}
	defer func() { _ = r.Close() }()
	if r.isSharedInstance || r.isConnectedToSharedInstance || !r.isStandaloneInstance {
		t.Fatalf("instance role mismatch: shared=%v connected=%v standalone=%v", r.isSharedInstance, r.isConnectedToSharedInstance, r.isStandaloneInstance)
	}
}

func TestNewReticulumSharedInstanceUnixServerThenClientSameConfigDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix shared-instance transport is not used on windows")
	}

	ResetTransport()
	defer ResetTransport()

	tempDir := t.TempDir()
	if runtime.GOOS == "darwin" {
		tempDir = "/tmp"
	}
	cfg, err := os.MkdirTemp(tempDir, "go-ret-shared-*")
	if err != nil {
		t.Fatalf("MkdirTemp config dir error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cfg) })

	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = unix

[logging]
loglevel = 4

[interfaces]
`, t.Name()))

	r1, err := NewReticulum(cfg)
	if err != nil {
		t.Fatalf("NewReticulum(first) error: %v", err)
	}
	defer func() { _ = r1.Close() }()
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(cfg)
	if err != nil {
		t.Fatalf("NewReticulum(second) error: %v", err)
	}
	defer func() { _ = r2.Close() }()
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}

	if r2.sharedInstanceInterface == nil || r2.sharedInstanceInterface.Type() != "LocalInterface" {
		t.Fatalf("expected second instance to use LocalInterface shared-instance client")
	}
}

func TestParseBoolLike(t *testing.T) {
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
