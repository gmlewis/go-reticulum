// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPackedContainerGuardedByPersistLock verifies that PackedContainer and
// WriteToDirectory both take the per-Message persist mutex, so a goroutine
// that mutates message state under the same lock cannot race with a concurrent
// container snapshot or persist. This mirrors Python's __persist_lock guarding
// packed_container + write_to_directory (LXMessage.py:188, 660-693, v1.0.0).
// Run under -race: an unlocked PackedContainer trips the race detector on the
// concurrent State read/write.
func TestPackedContainerGuardedByPersistLock(t *testing.T) {
	t.Parallel()

	msg := newAtomicWriteMessage(t)
	dir := testutils.TempDir(t, tempDirPrefix)

	deadline := time.Now().Add(1200 * time.Millisecond)
	var wg sync.WaitGroup
	var failed atomic.Bool

	// Snapshot/persist goroutine: calls the exported PackedContainer (which
	// must take the persist lock) and WriteToDirectory.
	wg.Go(func() {
		for time.Now().Before(deadline) && !failed.Load() {
			data, err := msg.PackedContainer()
			if err != nil {
				t.Errorf("PackedContainer: %v", err)
				failed.Store(true)
				return
			}
			if _, err := msgpack.Unpack(data); err != nil {
				t.Errorf("PackedContainer returned invalid msgpack: %v", err)
				failed.Store(true)
				return
			}
			if _, err := msg.WriteToDirectory(dir); err != nil {
				t.Errorf("WriteToDirectory: %v", err)
				failed.Store(true)
				return
			}
		}
	})

	// Mutator goroutine: mutates a field PackedContainer reads, under the
	// persist lock, simulating a caller that synchronizes mutation with
	// persistence.
	wg.Go(func() {
		for i := 0; time.Now().Before(deadline) && !failed.Load(); i++ {
			msg.persistMu.Lock()
			msg.State = i
			msg.persistMu.Unlock()
		}
	})

	wg.Wait()
}
