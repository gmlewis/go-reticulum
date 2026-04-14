// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tempDirPrefix = "gornx-test-"
)

func prepareGornxConfig(t *testing.T, configDir string) {
	prepareGornxConfigWithInstance(t, configDir, "gornx-"+filepath.Base(configDir), 0, 0)
}

func prepareGornxConfigWithInstance(t *testing.T, configDir string, instanceName string, listenPort, forwardPort int) {
	t.Helper()

	if listenPort == 0 {
		configText := strings.Join([]string{
			"[reticulum]",
			"enable_transport = Yes",
			"share_instance = Yes",
			"instance_name = " + instanceName,
			"",
			"[logging]",
			"loglevel = 4",
			"",
			"[interfaces]",
			"  [[Default Interface]]",
			"    type = AutoInterface",
			"    enabled = Yes",
			"",
		}, "\n")
		if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
			t.Fatalf("failed to write gornx config: %v", err)
		}
		return
	}

	configText := strings.Join([]string{
		"[reticulum]",
		"enable_transport = False",
		"share_instance = No",
		"instance_name = " + instanceName,
		"",
		"[logging]",
		"loglevel = 4",
		"",
		"[interfaces]",
		"  [[UDP Interface]]",
		"    type = UDPInterface",
		"    listen_ip = 127.0.0.1",
		"    listen_port = " + fmt.Sprintf("%v", listenPort),
		"    forward_ip = 127.0.0.1",
		"    forward_port = " + fmt.Sprintf("%v", forwardPort),
		"    enabled = Yes",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write gornx config: %v", err)
	}
}

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	return ""
}

func getRnshPythonPath() string {
	if path := os.Getenv("ORIGINAL_RNSH_REPO_DIR"); path != "" {
		return path
	}
	return ""
}

func reserveUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveUDPPort: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

type safeBuffer struct {
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	return s.buf.String()
}
