// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func closeReticulum(t *testing.T, r *rns.Reticulum) {
	t.Helper()
	if r == nil {
		return
	}
	if err := r.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("failed to close reticulum: %v", err)
	}
}

func writeRNSConfig(t *testing.T, dir string) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config")
	content := fmt.Sprintf(`[reticulum]
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 0

[interfaces]
`, reserveTCPPort(t), reserveTCPPort(t))
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("writeRNSConfig: %v", err)
	}
}

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort: %v", err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			t.Logf("failed to close listener: %v", err)
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRemoteInit(t *testing.T) {
	// Mock osExit
	var lastExitCode int
	osExit = func(code int) {
		lastExitCode = code
	}
	defer func() {
		osExit = os.Exit
	}()

	t.Run("config dir doesn't exist", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		nonExistent := filepath.Join(tmpDir, "nonexistent")
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		c := &clientT{}
		ret, err := c.remoteInit(nonExistent, rnsDir, 0, 0, "")
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if lastExitCode != 201 {
			t.Errorf("got exit code %v, want 201", lastExitCode)
		}
	})

	t.Run("identity file doesn't exist", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		c := &clientT{}
		ret, err := c.remoteInit(tmpDir, rnsDir, 0, 0, "")
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if lastExitCode != 202 {
			t.Errorf("got exit code %v, want 202", lastExitCode)
		}
	})

	t.Run("identity file from argument doesn't exist", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		nonExistentIdentity := filepath.Join(tmpDir, "nonexistent_identity")
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		c := &clientT{}
		ret, err := c.remoteInit("", rnsDir, 0, 0, nonExistentIdentity)
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if lastExitCode != 202 {
			t.Errorf("got exit code %v, want 202", lastExitCode)
		}
	})

	t.Run("load valid identity", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		identityPath := filepath.Join(tmpDir, "identity")

		// Create a valid identity
		id, err := rns.NewIdentity(true)
		mustTest(t, err)
		if err := id.ToFile(identityPath); err != nil {
			t.Fatal(err)
		}

		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		c := &clientT{}
		ret, err := c.remoteInit(tmpDir, rnsDir, 0, 0, "")
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if lastExitCode != 0 {
			t.Errorf("got exit code %v, want 0", lastExitCode)
		}
		if c.identity == nil {
			t.Error("identity was not loaded")
		} else if c.identity.HexHash != id.HexHash {
			t.Errorf("loaded identity hexhash %v, want %v", c.identity.HexHash, id.HexHash)
		}
	})

	t.Run("load valid identity from path argument", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		identityPath := filepath.Join(tmpDir, "identity_arg")

		// Create a valid identity
		id, err := rns.NewIdentity(true)
		mustTest(t, err)
		if err := id.ToFile(identityPath); err != nil {
			t.Fatal(err)
		}

		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		c := &clientT{}
		ret, err := c.remoteInit("", rnsDir, 0, 0, identityPath)
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if lastExitCode != 0 {
			t.Errorf("got exit code %v, want 0", lastExitCode)
		}
		if c.identity == nil {
			t.Error("identity was not loaded")
		} else if c.identity.HexHash != id.HexHash {
			t.Errorf("loaded identity hexhash %v, want %v", c.identity.HexHash, id.HexHash)
		}
	})

	t.Run("test log level and reticulum init", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		identityPath := filepath.Join(tmpDir, "identity")
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}
		if err := id.ToFile(identityPath); err != nil {
			t.Fatalf("ToFile: %v", err)
		}

		// Create a mock RNS config
		rnsConfigDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsConfigDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		content := fmt.Sprintf(`[reticulum]
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 1

[interfaces]
`, reserveTCPPort(t), reserveTCPPort(t))
		if err := os.WriteFile(filepath.Join(rnsConfigDir, "config"), []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		c := &clientT{}
		ret, err := c.remoteInit(tmpDir, rnsConfigDir, 2, 1, "") // 3 + 2 - 1 = 4
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if want := 4; rns.GetLogLevel() != want {
			t.Errorf("got log level %v, want %v", rns.GetLogLevel(), want)
		}

		if rns.GetLogDest() != rns.LogStdout {
			t.Errorf("got log dest %v, want LogStdout", rns.GetLogDest())
		}
	})

	t.Run("config-file loglevel applied in remoteInit", func(t *testing.T) {
		lastExitCode = 0
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		identityPath := filepath.Join(tmpDir, "identity")
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}
		if err := id.ToFile(identityPath); err != nil {
			t.Fatalf("ToFile: %v", err)
		}

		// Create an lxmd config with loglevel=6.
		lxmdConfig := "[logging]\nloglevel = 6\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte(lxmdConfig), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		rnsConfigDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsConfigDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsConfigDir)

		c := &clientT{}
		ret, err := c.remoteInit(tmpDir, rnsConfigDir, 0, 0, "")
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)
		if want := 6; rns.GetLogLevel() != want {
			t.Errorf("got log level %v, want %v", rns.GetLogLevel(), want)
		}
	})

	t.Run("testGetTargetIdentityLocal", func(t *testing.T) {
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		identityPath := filepath.Join(tmpDir, "identity")
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}
		if err := id.ToFile(identityPath); err != nil {
			t.Fatalf("ToFile: %v", err)
		}
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		c := &clientT{}
		ret, err := c.remoteInit(tmpDir, rnsDir, 0, 0, "")
		if err != nil {
			t.Fatalf("remoteInit: %v", err)
		}
		defer closeReticulum(t, ret)

		got := c.getTargetIdentity("", 5*time.Second)
		if got == nil {
			t.Fatal("got nil identity")
		}
		if got.HexHash != id.HexHash {
			t.Errorf("got identity hexhash %v, want %v", got.HexHash, id.HexHash)
		}
	})

	t.Run("testGetTargetIdentityInvalidHash", func(t *testing.T) {
		lastExitCode = 0
		c := &clientT{}
		_ = c.getTargetIdentity("invalid", 5*time.Second)
		if lastExitCode != 203 {
			t.Errorf("got exit code %v, want 203", lastExitCode)
		}
	})

	t.Run("testGetTargetIdentityRecall", func(t *testing.T) {
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}
		c := &clientT{
			ts: rns.NewTransportSystem(),
		}
		c.ts.Remember(nil, id.Hash, id.GetPublicKey(), nil)

		got := c.getTargetIdentity(id.HexHash, 5*time.Second)
		if got == nil {
			t.Fatal("got nil identity")
		}
		if got.HexHash != id.HexHash {
			t.Errorf("got identity hexhash %v, want %v", got.HexHash, id.HexHash)
		}
	})

	t.Run("testGetTargetIdentityNetwork", func(t *testing.T) {
		c := &clientT{
			ts: rns.NewTransportSystem(),
		}
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		ret, err := rns.NewReticulum(c.ts, rnsDir)
		if err != nil {
			t.Fatalf("NewReticulum: %v", err)
		}
		defer closeReticulum(t, ret)

		lastExitCode = 0
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}

		done := make(chan struct{})
		// Start a goroutine that will "find" the path and identity after a delay
		go func() {
			select {
			case <-time.After(200 * time.Millisecond):
				c.ts.Remember(nil, id.Hash, id.GetPublicKey(), nil)
			case <-done:
				return
			}
		}()
		defer close(done)

		got := c.getTargetIdentity(id.HexHash, 1*time.Second)
		if got == nil {
			t.Fatal("got nil identity")
		}
		if got.HexHash != id.HexHash {
			t.Errorf("got identity hexhash %v, want %v", got.HexHash, id.HexHash)
		}
	})

	t.Run("testGetTargetIdentityTimeout", func(t *testing.T) {
		c := &clientT{
			ts: rns.NewTransportSystem(),
		}
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)
		ret, err := rns.NewReticulum(c.ts, rnsDir)
		if err != nil {
			t.Fatalf("NewReticulum: %v", err)
		}
		defer closeReticulum(t, ret)

		lastExitCode = 0
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}

		// This should timeout because nothing is found
		_ = c.getTargetIdentity(id.HexHash, 200*time.Millisecond)
		mustTest(t, err)
		if lastExitCode != 200 {
			t.Errorf("got exit code %v, want 200", lastExitCode)
		}
	})

	t.Run("testQueryStatusTimeout", func(t *testing.T) {
		c := &clientT{
			ts: rns.NewTransportSystem(),
		}
		lastExitCode = 0
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}
		c.ts.Remember(nil, id.Hash, id.GetPublicKey(), nil)

		// Create a mock RNS config
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		rnsConfigDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsConfigDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsConfigDir)

		ret, err := rns.NewReticulum(c.ts, rnsConfigDir)
		if err != nil {
			t.Fatalf("NewReticulum: %v", err)
		}
		defer closeReticulum(t, ret)

		// TODO: fully test this.
		_, _ = c.queryStatus(id, id, 100*time.Millisecond, false)
	})

	t.Run("testGetStatusFormatting", func(t *testing.T) {
		if anyToFloat64(int(10)) != 10.0 {
			t.Errorf("anyToFloat64(int) failed")
		}

		tests := []struct {
			input float64
			want  string
		}{
			{0.0, "0.0"},
			{0.5, "0.5"},
			{0.333333, "0.33"},
			{1.0, "1.0"},
			{0.125, "0.13"},
			{0.999, "1.0"},
			{2.345, "2.35"},
			{0.005, "0.01"},
		}
		for _, tt := range tests {
			got := formatRound2(tt.input)
			if got != tt.want {
				t.Errorf("formatRound2(%v) = %q, want %q", tt.input, got, tt.want)
			}
		}
	})

	t.Run("testRequestSyncInternalTimeout", func(t *testing.T) {
		c := &clientT{
			ts: rns.NewTransportSystem(),
		}
		tmpDir, cleanup := tempDir(t)
		defer cleanup()
		rnsDir := filepath.Join(tmpDir, "rns")
		if err := os.MkdirAll(rnsDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeRNSConfig(t, rnsDir)

		ret, err := rns.NewReticulum(c.ts, rnsDir)
		if err != nil {
			t.Fatalf("NewReticulum: %v", err)
		}
		defer closeReticulum(t, ret)

		lastExitCode = 0
		id, err := rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("NewIdentity: %v", err)
		}
		c.ts.Remember(nil, id.Hash, id.GetPublicKey(), nil)

		// TODO: fix this.
		_, _ = c.requestSyncInternal(id, id.Hash, id, 100*time.Millisecond, false)
	})
}
