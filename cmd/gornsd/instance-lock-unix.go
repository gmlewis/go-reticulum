// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// lockFileExclusive tries to acquire an exclusive, non-blocking flock on f. It
// returns (true, nil) if acquired, (false, nil) if already held by another
// process, or (false, err) on a real error. The lock is held until f is closed;
// the OS releases it automatically on process exit or crash, so no stale-lock
// file can survive a crash.
func lockFileExclusive(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return false, err
}
