// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

type liveHardwareGates struct {
	allowWrites      bool
	allowDestructive bool
}

func parseLiveHardwareGates(getenv func(string) string) liveHardwareGates {
	allowWrites := strings.TrimSpace(getenv("GORNODECONF_LIVE_ALLOW_WRITES")) == "1"
	allowDestructive := strings.TrimSpace(getenv("GORNODECONF_LIVE_ALLOW_DESTRUCTIVE")) == "1"
	if allowDestructive {
		allowWrites = true
	}
	return liveHardwareGates{
		allowWrites:      allowWrites,
		allowDestructive: allowDestructive,
	}
}

func resolveLiveHardwarePort(getenv func(string) string) string {
	return strings.TrimSpace(getenv("GORNODECONF_LIVE_SERIAL_PORT"))
}

func requireLiveHardwarePort(t *testing.T, safety liveSerialSafety) string {
	t.Helper()

	testutils.SkipShortIntegration(t)
	port := resolveLiveHardwarePort(os.Getenv)
	if port == "" {
		t.Skip("GORNODECONF_LIVE_SERIAL_PORT not set")
	}
	logLiveHardwareTest(t, port, safety)
	return port
}

func skipUnlessLiveWriteAllowed(t *testing.T) {
	t.Helper()

	if !parseLiveHardwareGates(os.Getenv).allowWrites {
		t.Skip("set GORNODECONF_LIVE_ALLOW_WRITES=1 to enable mutating live-hardware tests")
	}
}

func skipUnlessLiveDestructiveAllowed(t *testing.T) {
	t.Helper()

	if !parseLiveHardwareGates(os.Getenv).allowDestructive {
		t.Skip("set GORNODECONF_LIVE_ALLOW_DESTRUCTIVE=1 to enable destructive live-hardware tests")
	}
}

func logLiveHardwareTest(t *testing.T, port string, safety liveSerialSafety) {
	t.Helper()

	t.Logf("live hardware os=%v port=%v safety=%v", runtime.GOOS, port, safety)
}

func liveHardwareTempPath(t *testing.T, prefix, name string) string {
	t.Helper()

	return filepath.Join(liveHardwareTempDir(t, prefix), name)
}

func liveHardwareTempDir(t *testing.T, prefix string) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, prefix)
	t.Cleanup(cleanup)
	return dir
}
