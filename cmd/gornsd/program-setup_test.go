// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func writeGornsdConfig(t *testing.T, dir string, shareInstance string, loglevel int) {
	t.Helper()
	config := fmt.Sprintf(`[reticulum]
share_instance = %v

[logging]
loglevel = %v

[interfaces]
`, shareInstance, loglevel)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}
}

func captureLoggerState(t *testing.T) {
	t.Helper()
	level := rns.GetLogLevel()
	dest := rns.GetLogDest()
	filePath := rns.GetLogFilePath()
	callback := rns.GetLogCallback()
	t.Cleanup(func() {
		rns.SetLogLevel(level)
		rns.SetLogDest(dest)
		rns.SetLogFilePath(filePath)
		rns.SetLogCallback(callback)
	})
}

func waitForFileContains(t *testing.T, path string, want string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%v did not contain %q", path, want)
}

func waitForMessageContains(t *testing.T, messages *[]string, want string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		for i := len(*messages) - 1; i >= 0; i-- {
			if strings.Contains((*messages)[i], want) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages did not contain %q: %#v", want, *messages)
}

func TestProgramSetupAppliesVerbosityAndLogsNotice(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-setup-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "No", 4)

	captureLoggerState(t)
	var messages []string
	rns.SetLogDest(rns.LogCallback)
	rns.SetLogCallback(func(message string) {
		messages = append(messages, message)
	})

	ret, err := programSetup(configDir, 2, 1, false)
	if err != nil {
		t.Fatalf("programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("ret.Close error: %v", closeErr)
		}
	})

	if got, want := rns.GetLogLevel(), 5; got != want {
		t.Fatalf("log level = %v, want %v", got, want)
	}
	if got, want := rns.GetLogDest(), rns.LogCallback; got != want {
		t.Fatalf("log dest = %v, want %v", got, want)
	}
	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1], "Started gornsd version") {
		t.Fatalf("startup notice missing from messages: %#v", messages)
	}
}

func TestProgramSetupServiceUsesFileLogging(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-service-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "No", 4)

	captureLoggerState(t)
	ret, err := programSetup(configDir, 0, 0, true)
	if err != nil {
		t.Fatalf("programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("ret.Close error: %v", closeErr)
		}
	})

	if got, want := rns.GetLogDest(), rns.LogDestFile; got != want {
		t.Fatalf("log dest = %v, want %v", got, want)
	}
	if got, want := rns.GetLogFilePath(), filepath.Join(configDir, "logfile"); got != want {
		t.Fatalf("log file path = %q, want %q", got, want)
	}
	waitForFileContains(t, filepath.Join(configDir, "logfile"), "Started gornsd version")
}

func TestProgramSetupConnectedSharedInstanceLogsWarning(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-shared-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "Yes", 4)

	captureLoggerState(t)
	var messages []string
	rns.SetLogDest(rns.LogCallback)
	rns.SetLogCallback(func(message string) {
		messages = append(messages, message)
	})

	ts := rns.NewTransportSystem()
	shared, err := rns.NewReticulum(ts, configDir)
	if err != nil {
		t.Fatalf("failed to start shared instance: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := shared.Close(); closeErr != nil {
			t.Fatalf("shared.Close error: %v", closeErr)
		}
	})

	messages = nil
	ret, err := programSetup(configDir, 0, 0, false)
	if err != nil {
		t.Fatalf("programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("ret.Close error: %v", closeErr)
		}
	})

	if len(messages) == 0 || !strings.Contains(messages[len(messages)-1], "connected to another shared local instance") {
		waitForMessageContains(t, &messages, "connected to another shared local instance")
	}
}
