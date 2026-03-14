// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMain(m *testing.M) {
	lockPath := filepath.Join(os.TempDir(), "go-reticulum-rns-integration.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Fatalf("failed to open integration lock file %v: %v", lockPath, err)
	}

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		log.Fatalf("failed to acquire integration lock %v: %v", lockPath, err)
		_ = lockFile.Close()
	}

	ResetTransport()
	code := m.Run()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN); err != nil {
		log.Printf("failed to release integration lock %v: %v", lockPath, err)
		if code == 0 {
			code = 1
		}
	}
	if err := lockFile.Close(); err != nil {
		log.Printf("failed to close integration lock file %v: %v", lockPath, err)
		if code == 0 {
			code = 1
		}
	}

	os.Exit(code)
}

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	log.Fatalf("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	return "" // unreachable
}
