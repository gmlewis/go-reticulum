// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package testutils provides shared helper functions for tests across this
// repository.
package testutils

import (
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

type testMainTB struct{}

func (testMainTB) Helper() {}

func (t testMainTB) Fatalf(format string, args ...any) {
	log.Fatalf(format, args...)
}

type tempDirTB interface {
	Helper()
	Fatalf(format string, args ...any)
}

func tempBaseDir() string {
	if runtime.GOOS == "darwin" {
		return "/tmp"
	}
	return ""
}

// TempDir creates a temporary directory for a test and returns a cleanup
// function that removes it.
func TempDir(t *testing.T, prefix string) (string, func()) {
	return tempDir(t, prefix)
}

// TempDirBench creates a temporary directory for a benchmark and returns a cleanup
// function that removes it.
func TempDirBench(b *testing.B, prefix string) (string, func()) {
	return tempDir(b, prefix)
}

// TempDirMain creates a temporary directory for a TestMain suite and returns a cleanup
// function that removes it.
func TempDirMain(prefix string) (string, func()) {
	return tempDir(testMainTB{}, prefix)
}

func tempDir(t tempDirTB, prefix string) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp(tempBaseDir(), prefix)
	if err != nil {
		t.Fatalf("TempDir error: %v", err)
	}

	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("os.RemoveAll: %v", err)
		}
	}

	return dir, cleanup
}

// TempDirWithConfig creates a temporary directory containing a config file and
// returns a cleanup function that removes it.
func TempDirWithConfig(t *testing.T, prefix string, config func(dir string) string) (string, func()) {
	t.Helper()

	dir, cleanup := TempDir(t, prefix)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config(dir)), 0o600); err != nil {
		cleanup()
		t.Fatalf("TempDirWithConfig error: %v", err)
	}

	return dir, cleanup
}

// SkipShortIntegration skips integration-heavy tests when testing.Short is set.
func SkipShortIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
}

// Global TCP port counter for integration tests.
var testTCPPortCounter atomic.Uint32

// ReserveTCPPort reserves a unique TCP port for integration tests.
// It uses a global counter to ensure ports don't conflict between tests.
func ReserveTCPPort(t *testing.T) int {
	t.Helper()
	for {
		port := 43000 + int(testTCPPortCounter.Add(1)%20000)
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
		if err != nil {
			continue
		}
		addr, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			_ = l.Close()
			t.Fatalf("ReserveTCPPort: unexpected addr type %T", l.Addr())
		}
		if err := l.Close(); err != nil {
			t.Fatalf("ReserveTCPPort: close error: %v", err)
		}
		return addr.Port
	}
}
