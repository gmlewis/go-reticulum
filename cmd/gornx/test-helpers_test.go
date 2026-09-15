// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	tempDirPrefix = "gornx-test-"
)

func prepareGornxConfig(t *testing.T, configDir string) {
	prepareGornxConfigWithInstance(t, configDir, "gornx-"+filepath.Base(configDir), 0, 0)
}

// prepareGornxConfigWithInstance writes a per-test Reticulum configuration.
//
// The no-port variant (listenPort == 0) configures a standalone instance, as
// every other tool's integration helper here does. It must not ask for a shared
// instance: gornx requires one when the configuration shares one
// (WithRequireSharedInstance in run()), so share_instance = Yes makes these
// tests attach to whatever shared instance the machine happens to be running —
// the developer's own daemon locally, nothing in CI — instead of exercising the
// tool. A test that passes only because an unrelated instance is listening on
// the default port is not testing the tool at all.
//
// The ported variant configures two peers on private UDP loopback ports, so a
// listener and an initiator can talk to each other without a shared instance.
func prepareGornxConfigWithInstance(t *testing.T, configDir string, instanceName string, listenPort, forwardPort int) {
	t.Helper()

	if listenPort == 0 {
		configText := strings.Join([]string{
			"[reticulum]",
			"enable_transport = Yes",
			"share_instance = No",
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

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
