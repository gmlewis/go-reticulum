// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// enforceSingleInstance acquires a per-config-dir exclusive lock so only one
// gornsd runs per Reticulum config directory. This prevents the
// stale-second-instance situation where one gornsd stays the shared instance
// while a second's clients see stale or empty state. It returns a release
// function (callers may defer it; the OS also releases the lock on process
// exit/crash).
func enforceSingleInstance(configDir string) (func(), error) {
	lockPath := filepath.Join(configDir, "gornsd.lock")
	release, holderPID, err := acquireInstanceLock(lockPath)
	if err != nil {
		return nil, fmt.Errorf("acquire instance lock %v: %w", lockPath, err)
	}
	if release == nil {
		return nil, fmt.Errorf("gornsd is already running (PID %v) for config dir %v.\n"+
			"Stop the existing instance (or use a different --config dir) before starting a new one.",
			holderPID, configDir)
	}
	return release, nil
}

// acquireInstanceLock exclusively locks lockPath. It returns a non-nil release
// function when the lock was acquired (the OS releases it on process exit/crash;
// calling release closes the file too). It returns release==nil plus the
// holder's PID when another gornsd already holds the lock.
func acquireInstanceLock(lockPath string) (func(), int, error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, 0, err
	}
	locked, lerr := lockFileExclusive(f)
	if lerr != nil {
		_ = f.Close()
		return nil, 0, lerr
	}
	if !locked {
		holder := readPIDFromFile(f)
		_ = f.Close()
		return nil, holder, nil
	}
	if err := writePIDToFile(f, os.Getpid()); err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return func() { _ = f.Close() }, 0, nil
}

// readPIDFromFile reads a PID from the start of f.
func readPIDFromFile(f *os.File) int {
	_, _ = f.Seek(0, 0)
	buf := make([]byte, 32)
	n, _ := f.Read(buf)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	return pid
}

// writePIDToFile truncates f and writes pid into it.
func writePIDToFile(f *os.File, pid int) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	_, err := f.WriteString(strconv.Itoa(pid))
	return err
}
