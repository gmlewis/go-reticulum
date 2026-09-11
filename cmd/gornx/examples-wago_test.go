// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file smoke-tests the shipped example command plugin from
// assets/wasm-plugins/echo/ against the gornx plugin host.

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestExampleEchoCommandWorks loads the shipped echo example into a command
// host and runs it through the rnx result-array mapping.
func TestExampleEchoCommandWorks(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	src, err := os.ReadFile(filepath.Join("..", "..", "assets", "wasm-plugins", "echo", "echo.wasm"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	writeFixtureFile(t, dir, "echo.wasm", src)

	ch := newCommandHosts(dir, 2*time.Second, nil)
	defer ch.close()
	host := ch.forCommand("echo")
	if host == nil {
		t.Fatal("forCommand(echo) = nil, want the loaded example")
	}

	result := execWasmCommand(host, "echo hello world", nil, nil, nil, testRequestTime(), 0, nil, nil)
	rows := result.([]any)
	if rows[0] != true {
		t.Errorf("executed flag = %v, want true", rows[0])
	}
	stdout, ok := rows[2].([]byte)
	if !ok || len(stdout) == 0 {
		t.Errorf("stdout = %v, want the plugin response", rows[2])
	}
}
