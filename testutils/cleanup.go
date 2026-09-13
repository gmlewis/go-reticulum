// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// TempDir and its siblings already register t.Cleanup/b.Cleanup, so a test run
// that ends normally removes every directory it created. An interrupted run
// does not: t.Cleanup and defer never run when a test binary is killed by
// Ctrl-C, by kill, or by a `go test -timeout` panic (which calls os.Exit from
// the timeout goroutine), and each such run strands every directory it had
// created so far.
//
// A signal handler can still run on the way out, so every directory created
// here is recorded in a process-wide registry and removed by a handler
// installed on the first creation. SIGKILL cannot be caught, and neither can
// os.Exit from an unrelated goroutine — scripts/clean-test-tmp.sh reclaims
// what those leave behind at the start of the next suite run.

// tempRegistry holds the directories this test binary has created and not yet
// removed. A normally-terminating run empties it, leaving the signal handler
// nothing to do.
var tempRegistry = struct {
	mu   sync.Mutex
	dirs map[string]struct{}
}{dirs: map[string]struct{}{}}

var signalCleanupOnce sync.Once

// registerTempDir records a directory for removal if this process is
// interrupted, installing the signal handler on first use.
func registerTempDir(dir string) {
	signalCleanupOnce.Do(installSignalCleanup)

	tempRegistry.mu.Lock()
	defer tempRegistry.mu.Unlock()
	tempRegistry.dirs[dir] = struct{}{}
}

// unregisterTempDir drops a directory whose removal has already succeeded.
// A directory whose removal failed stays registered, so the signal handler
// gets another attempt at it.
func unregisterTempDir(dir string) {
	tempRegistry.mu.Lock()
	defer tempRegistry.mu.Unlock()
	delete(tempRegistry.dirs, dir)
}

// installSignalCleanup removes every registered directory when this process is
// interrupted, then re-raises the signal with its default disposition so the
// shell still reports the interruption (exit status 130 for SIGINT) instead of
// a success the run never achieved.
func installSignalCleanup() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig, ok := <-ch
		if !ok {
			return
		}

		// Restore the default disposition before cleaning up, so a second
		// Ctrl-C kills immediately rather than queueing behind the sweep.
		signal.Reset(sig)

		removeRegisteredTempDirs()

		s, isSignal := sig.(syscall.Signal)
		if !isSignal {
			os.Exit(1)
		}
		_ = syscall.Kill(os.Getpid(), s)

		// Not reached: the default disposition for SIGINT and SIGTERM
		// terminates the process. Guard against the signal being blocked so
		// this handler can never wedge an interrupted run.
		time.Sleep(100 * time.Millisecond)
		os.Exit(1)
	}()
}

// removeRegisteredTempDirs removes every registered directory, emptying the
// registry first so a concurrent creation is not removed twice. Removal
// failures are ignored: this runs on the way out of an interrupted process,
// which has nowhere to report them.
func removeRegisteredTempDirs() {
	tempRegistry.mu.Lock()
	dirs := make([]string, 0, len(tempRegistry.dirs))
	for dir := range tempRegistry.dirs {
		dirs = append(dirs, dir)
	}
	tempRegistry.dirs = map[string]struct{}{}
	tempRegistry.mu.Unlock()

	for _, dir := range dirs {
		_ = removeAllWithRetry(dir)
	}
}
