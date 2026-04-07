// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net"
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

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
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

func countMessageContains(messages []string, want string) int {
	count := 0
	for _, message := range messages {
		if strings.Contains(message, want) {
			count++
		}
	}
	return count
}

func TestProgramSetupAppliesVerbosityAndLogsNotice(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-setup-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "No", 4)

	logger := rns.NewLogger()
	var messages []string
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(message string) {
		messages = append(messages, message)
	})

	app := &appT{
		logger:    logger,
		configDir: configDir,
		verbose:   2,
		quiet:     1,
		service:   false,
	}
	ret, err := app.programSetup()
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
	if got, want := countMessageContains(messages, "Started gornsd version"), 1; got != want {
		t.Fatalf("startup notice count = %v, want %v; messages=%#v", got, want, messages)
	}
}

func TestProgramSetupServiceUsesFileLogging(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-service-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "No", 4)

	logger := rns.NewLogger()
	app := &appT{
		logger:    logger,
		configDir: configDir,
		verbose:   0,
		quiet:     0,
		service:   true,
	}
	ret, err := app.programSetup()
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

func TestProgramSetupServiceKeepsConfigLogLevel(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-service-level-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "No", 4)

	logger := rns.NewLogger()
	app := &appT{
		logger:    logger,
		configDir: configDir,
		verbose:   2,
		quiet:     0,
		service:   true,
	}
	ret, err := app.programSetup()
	if err != nil {
		t.Fatalf("programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("ret.Close error: %v", closeErr)
		}
	})

	if got, want := logger.GetLogLevel(), 4; got != want {
		t.Fatalf("log level = %v, want %v", got, want)
	}
}

func TestProgramSetupConnectedSharedInstanceLogsWarning(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-shared-")
	defer cleanup()
	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	config := fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`, "gornsd-program-shared", sharedPort, rpcPort)
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}

	logger := rns.NewLogger()
	var messages []string
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(message string) {
		messages = append(messages, message)
	})

	ts := rns.NewTransportSystem(logger)
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
	app := &appT{
		logger:    logger,
		configDir: configDir,
		verbose:   0,
		quiet:     0,
		service:   false,
	}
	ret, err := app.programSetup()
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

func TestProgramSetupAppliesVerbosityBeforeStartupLogging(t *testing.T) {
	configDir, cleanup := testutils.TempDir(t, "gornsd-program-verbosity-")
	defer cleanup()
	writeGornsdConfig(t, configDir, "No", 4)

	logger := rns.NewLogger()
	var messages []string
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(message string) {
		messages = append(messages, message)
	})

	app := &appT{
		logger:    logger,
		configDir: configDir,
		verbose:   2,
		quiet:     0,
		service:   false,
	}
	ret, err := app.programSetup()
	if err != nil {
		t.Fatalf("first programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret.Close(); closeErr != nil {
			t.Fatalf("first ret.Close error: %v", closeErr)
		}
	})

	messages = nil
	ret2, err := app.programSetup()
	if err != nil {
		t.Fatalf("second programSetup error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ret2.Close(); closeErr != nil {
			t.Fatalf("second ret.Close error: %v", closeErr)
		}
	})

	if got := countMessageContains(messages, "Loaded Transport Identity from storage"); got == 0 {
		t.Fatalf("expected startup verbosity message, got messages=%#v", messages)
	}
}
