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

	configTemplate := `[reticulum]
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v

[logging]
loglevel = 4

[interfaces]
`

	cfg1 := t.TempDir()
	cfg2 := t.TempDir()
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, port))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, port))

	r1, err := NewReticulum(cfg1)
	if err != nil {
		t.Fatalf("NewReticulum(first) error: %v", err)
	}
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(cfg2)
	if err != nil {
		t.Fatalf("NewReticulum(second) error: %v", err)
	}
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}
}

func TestNewReticulumShareInstanceNoStandalone(t *testing.T) {
	ResetTransport()
	defer ResetTransport()

	cfg := t.TempDir()
	writeConfig(t, cfg, `[reticulum]
share_instance = No

[logging]
loglevel = 4

[interfaces]
`)

	r, err := NewReticulum(cfg)
	if err != nil {
		t.Fatalf("NewReticulum error: %v", err)
	}
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

	cfg, err := os.MkdirTemp("/tmp", "go-ret-shared-*")
	if err != nil {
		t.Fatalf("MkdirTemp config dir error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cfg) })

	writeConfig(t, cfg, `[reticulum]
share_instance = Yes

[logging]
loglevel = 4

[interfaces]
`)

	r1, err := NewReticulum(cfg)
	if err != nil {
		t.Fatalf("NewReticulum(first) error: %v", err)
	}
	if !r1.isSharedInstance || r1.isConnectedToSharedInstance || r1.isStandaloneInstance {
		t.Fatalf("first instance role mismatch: shared=%v connected=%v standalone=%v", r1.isSharedInstance, r1.isConnectedToSharedInstance, r1.isStandaloneInstance)
	}

	r2, err := NewReticulum(cfg)
	if err != nil {
		if r1.sharedInstanceInterface != nil {
			_ = r1.sharedInstanceInterface.Detach()
		}
		if r1.rpcListener != nil {
			_ = r1.rpcListener.Close()
		}
		t.Fatalf("NewReticulum(second) error: %v", err)
	}
	if r2.isSharedInstance || !r2.isConnectedToSharedInstance || r2.isStandaloneInstance {
		t.Fatalf("second instance role mismatch: shared=%v connected=%v standalone=%v", r2.isSharedInstance, r2.isConnectedToSharedInstance, r2.isStandaloneInstance)
	}

	if r2.sharedInstanceInterface == nil || r2.sharedInstanceInterface.Type() != "LocalInterface" {
		t.Fatalf("expected second instance to use LocalInterface shared-instance client")
	}

	if r1.sharedInstanceInterface != nil {
		_ = r1.sharedInstanceInterface.Detach()
	}
	if r2.sharedInstanceInterface != nil {
		_ = r2.sharedInstanceInterface.Detach()
	}
	if r1.rpcListener != nil {
		_ = r1.rpcListener.Close()
	}
	if r2.rpcListener != nil {
		_ = r2.rpcListener.Close()
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
