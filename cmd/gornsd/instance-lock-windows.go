// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build windows

package main

import "os"

// lockFileExclusive is a no-op on Windows, where only the standard library is
// imported and no OS-level exclusive lock is available. The PID file written by
// acquireInstanceLock remains an advisory indicator only; the Unix flock path
// is the one that enforces single-instance.
func lockFileExclusive(f *os.File) (bool, error) {
	return true, nil
}
