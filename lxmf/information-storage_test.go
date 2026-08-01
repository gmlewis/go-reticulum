// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestInformationStorageSizeIsPythonStubZero pins Phase C.2: Python's
// LXMRouter.information_storage_size (LXMRouter.py:739-740) is an upstream
// stub — `def information_storage_size(self): pass` — that returns None and
// performs no computation regardless of how many messages occupy the
// propagation store. The Go port returns 0.0 unconditionally to match that
// no-op semantics. This test populates the store so the assertion proves the
// return is state-independent, not merely 0 on an empty router.
func TestInformationStorageSizeIsPythonStubZero(t *testing.T) {
	t.Parallel()

	router := mustTestNewRouter(t, rns.NewTransportSystem(nil), nil, testutils.TempDir(t, tempDirPrefix))
	router.EnablePropagation()

	// Populate the propagation store with real-sized entries so the test
	// proves InformationStorageSize ignores the actual storage (mirroring
	// Python's `pass`).
	router.mu.Lock()
	router.propagationEntries["transient-1"] = &propagationEntry{size: 1234, path: "p1"}
	router.propagationEntries["transient-2"] = &propagationEntry{size: 5678, path: "p2"}
	router.mu.Unlock()

	if got := router.InformationStorageSize(); got != 0 {
		t.Fatalf("InformationStorageSize = %v, want 0 (Python information_storage_size is a `pass` stub returning None)", got)
	}
}
