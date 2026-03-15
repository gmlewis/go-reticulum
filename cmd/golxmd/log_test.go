// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestStartupLogMessages(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "lxmd")
	os.MkdirAll(configDir, 0755)

	var capturedLogs []string
	rns.SetLogDest(rns.LogCallback)
	rns.SetLogCallback(func(msg string) {
		capturedLogs = append(capturedLogs, msg)
	})
	defer func() {
		rns.SetLogDest(rns.LogStdout)
		rns.SetLogCallback(nil)
	}()

	// We set LogLevel high enough to see all messages
	rns.SetLogLevel(rns.LogVerbose)

	// Mocking the environment
	identityPath := filepath.Join(configDir, "identity")

	// Call loadOrCreateIdentity - should log something after fix
	_, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity: %v", err)
	}

	expectedSubstrings := []string{
		"No Primary Identity file found, creating new...",
		"Created new Primary Identity",
	}

	for _, substr := range expectedSubstrings {
		found := false
		for _, logMsg := range capturedLogs {
			if strings.Contains(logMsg, substr) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Missing log message containing: %q", substr)
		}
	}

	// Now test loading existing identity
	capturedLogs = nil
	_, err = loadOrCreateIdentity(identityPath)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity (existing): %v", err)
	}

	found := false
	for _, logMsg := range capturedLogs {
		if strings.Contains(logMsg, "Loaded Primary Identity") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Missing log message containing: \"Loaded Primary Identity\"")
	}
}
