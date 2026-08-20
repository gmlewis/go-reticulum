// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestStopTearsDownLinksBeforeDetach verifies that Transport.Stop
// must tear down every active and pending link (sending a teardown packet)
// BEFORE detaching interfaces, and provide a 150ms flush window so the
// teardown packets leave the local transport. It is the Go port of Python
// Transport.detach_interfaces (Transport.py:3172-3184), which iterates
// Transport.active_links and Transport.pending_links calling link.teardown(),
// counts the closed links, and sleeps 0.15s when any were closed.
//
// The observable contract is that a peer holding the other end of an
// established link receives the teardown packet (ContextLinkClose) and closes
// its link as a result — proving the packet was emitted before detach.
func TestStopTearsDownLinksBeforeDetach(t *testing.T) {
	t.Parallel()

	initiator, receiver, _ := establishLoopbackLinkPair(t)
	tsInit, ok := initiator.transport.(*TransportSystem)
	if !ok {
		t.Fatalf("initiator.transport = %T, want *TransportSystem", initiator.transport)
	}

	// Stop only runs its teardown/detach body when the transport has been
	// started (the running flag guards against double-Stop). Start the
	// initiator transport with an isolated storage path so Stop exercises the
	// real link-teardown path. Start preserves the already-configured
	// transport identity, so the established link is unaffected.
	storage := testutils.TempDir(t, tempDirPrefix)
	if err := tsInit.Start(storage); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Stop the initiator's transport. This must tear down the active link,
	// emitting a teardown packet to the receiver before detaching the pipe.
	tsInit.Stop()

	// The receiver should have received the teardown packet and closed its
	// link. The 150ms flush window inside Stop gives the async pipe delivery
	// time to complete; poll briefly as a backstop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if receiver.status.Load() == LinkClosed {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := receiver.status.Load(); got != LinkClosed {
		t.Fatalf("receiver link status = %v after transport Stop, want LinkClosed (teardown packet not emitted before detach)", got)
	}
}

// TestStopTeardownFlushIsBounded asserts the 150ms teardown flush only runs
// when at least one link was actually torn down, matching Python's
// `if closed_links: time.sleep(0.15)` guard (Transport.py:3184). A started
// transport with no links must not pay the flush sleep. This keeps the
// no-link stop path fast while preserving the flush exactly when it is
// needed.
func TestStopTeardownFlushIsBounded(t *testing.T) {
	t.Parallel()

	ts := newTestTransportSystem(t)
	storage := testutils.TempDir(t, tempDirPrefix)
	if err := ts.Start(storage); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// No links registered. Stop must return promptly (no 150ms flush).
	start := time.Now()
	ts.Stop()
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Fatalf("Stop with no links took %v, want < 100ms (no flush expected)", elapsed)
	}
}
