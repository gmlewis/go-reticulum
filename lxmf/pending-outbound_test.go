// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPendingOutboundSnapshot pins the Router.PendingOutbound accessor that
// nomadnet's conversation view reads to render a live transfer progress row,
// mirroring Python's scan of app.message_router.pending_outbound
// (Conversations.py:2711-2720). The accessor returns a defensive snapshot of
// each queued message's hash, progress, state and representation so the UI
// layer never touches router- or message-internal state directly.
func TestPendingOutboundSnapshot(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if got := router.PendingOutbound(); len(got) != 0 {
		t.Fatalf("empty router PendingOutbound() = %v entries, want 0", len(got))
	}

	source := mustTestNewIdentity(t, true)
	dest := mustTestNewIdentity(t, true)
	destDest := mustTestNewDestination(t, ts, dest, rns.DestinationOut, rns.DestinationSingle, "lxmf", "delivery")
	srcDest := mustTestNewDestination(t, ts, source, rns.DestinationOut, rns.DestinationSingle, "lxmf", "delivery")

	inTransit := mustTestNewMessage(t, destDest, srcDest, "a large body", "", nil)
	inTransit.Hash = []byte{0xAA, 0xBB, 0xCC}
	inTransit.SetProgress(0.45)
	inTransit.SetState(StateSending)
	inTransit.Representation = RepresentationResource

	settled := mustTestNewMessage(t, destDest, srcDest, "already sent", "", nil)
	settled.Hash = []byte{0xDD, 0xEE}
	settled.SetState(StateSent)
	settled.Representation = RepresentationPacket

	router.mu.Lock()
	router.pendingOutbound = append(router.pendingOutbound, inTransit, settled)
	router.mu.Unlock()

	got := router.PendingOutbound()
	if len(got) != 2 {
		t.Fatalf("PendingOutbound() = %v entries, want 2", len(got))
	}
	want := PendingOutboundSnapshot{
		Hash:           []byte{0xAA, 0xBB, 0xCC},
		Progress:       0.45,
		State:          StateSending,
		Representation: RepresentationResource,
	}
	if !bytes.Equal(got[0].Hash, want.Hash) ||
		got[0].Progress != want.Progress ||
		got[0].State != want.State ||
		got[0].Representation != want.Representation {
		t.Errorf("PendingOutbound()[0] = %+v, want %+v", got[0], want)
	}
	if got[1].State != StateSent || got[1].Representation != RepresentationPacket {
		t.Errorf("PendingOutbound()[1] = %+v, want state SENT / representation PACKET", got[1])
	}

	// The snapshot must be defensive: mutating a returned hash must not corrupt
	// the router's live message (the UI reads it every poll tick).
	got[0].Hash[0] = 0x00
	if fresh := router.PendingOutbound(); fresh[0].Hash[0] != 0xAA {
		t.Errorf("mutating a snapshot hash leaked into the router: got %v, want 0xAA", fresh[0].Hash[0])
	}

	// A nil router yields no entries rather than panicking.
	var nilRouter *Router
	if got := nilRouter.PendingOutbound(); got != nil {
		t.Errorf("nil router PendingOutbound() = %v, want nil", got)
	}
}
