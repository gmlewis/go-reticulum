// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build windows

package main

import "os"

// lockFileExclusive is a no-op on Windows: the go-reticulum root module is
// stdlib-only (no golang.org/x/sys dependency), so the robust LockFileEx
// syscall is not available. The PID file written by acquireInstanceLock still
// serves as an advisory indicator, but no OS-level exclusive lock is enforced.
// The unix path (flock) is the robust one; Windows gornsd does not enforce
// single-instance at the OS level. If Windows enforcement is needed, add
// golang.org/x/sys as a dependency and use windows.LockFileEx here.
func lockFileExclusive(f *os.File) (bool, error) {
	return true, nil
}
