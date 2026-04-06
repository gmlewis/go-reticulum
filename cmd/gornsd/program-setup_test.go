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

func captureLoggerState(t *testing.T, logger *rns.Logger) {
	t.Helper()
	level := logger.GetLogLevel()
	dest := logger.GetLogDest()
	filePath := logger.GetLogFilePath()
	callback := logger.GetLogCallback()
	t.Cleanup(func() {
		logger.SetLogLevel(level)
		logger.SetLogDest(dest)
		logger.SetLogFilePath(filePath)
		logger.SetLogCallback(callback)
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

	logger := rns.NewLogger()
	captureLoggerState(t, logger)
	var messages []string
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(message string) {
		messages = append(messages, message)
	})

	ret, err := programSetup(logger, configDir, 2, 1, false)
	if err != nil {
		t.Fatalf("programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("ret.Close error: %v", closeErr)
		}
	})

	if got, want := logger.GetLogLevel(), 5; got != want {
		t.Fatalf("log level = %v, want %v", got, want)
	}
	if got, want := logger.GetLogDest(), rns.LogCallback; got != want {
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

	logger := rns.NewLogger()
	captureLoggerState(t, logger)
	ret, err := programSetup(logger, configDir, 0, 0, true)
	if err != nil {
		t.Fatalf("programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("ret.Close error: %v", closeErr)
		}
	})

	if got, want := logger.GetLogDest(), rns.LogDestFile; got != want {
		t.Fatalf("log dest = %v, want %v", got, want)
	}
	if got, want := logger.GetLogFilePath(), filepath.Join(configDir, "logfile"); got != want {
		t.Fatalf("log file path = %q, want %q", got, want)
	}
	waitForFileContains(t, filepath.Join(configDir, "logfile"), "Started gornsd version")
}

func TestProgramSetupConnectedSharedInstanceLogsWarning(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-shared-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "Yes", 4)

	logger := rns.NewLogger()
	captureLoggerState(t, logger)
	var messages []string
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(message string) {
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
	ret, err := programSetup(logger, configDir, 0, 0, false)
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
