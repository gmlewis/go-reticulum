// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestJobs_RecoverFromPanic(t *testing.T) {
	t.Parallel()

	// Set up a config that will trigger announce logic.
	peerInterval := 1
	currentTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	c := &clientT{
		ac: &activeConfig{
			PeerAnnounceInterval: &peerInterval,
		},
		now:              func() time.Time { return currentTime },
		lastPeerAnnounce: time.Time{},
		lastNodeAnnounce: time.Time{},
	}

	// Pass a nil router so that tick panics when it tries to call
	// router.Announce on a nil pointer. The jobs loop must recover
	// and keep running instead of crashing.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.jobs(nil, nil, stop, 1*time.Millisecond)
	}()

	// Let the jobs loop run a few iterations (it would crash without
	// panic recovery).
	time.Sleep(50 * time.Millisecond)
	close(stop)
	<-done
}

func TestTick(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)

	// Mock clock
	currentTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	peerInterval := 1 // 1 second for test
	nodeInterval := 1 // 1 second for test
	c := &clientT{
		ts: rns.NewTransportSystem(nil),
		ac: &activeConfig{
			PeerAnnounceInterval: &peerInterval,
			NodeAnnounceInterval: &nodeInterval,
		},
		now:              func() time.Time { return currentTime },
		lastPeerAnnounce: time.Time{},
		lastNodeAnnounce: time.Time{},
	}
	router, err := lxmf.NewRouter(c.ts, identity, tmpDir)
	mustTest(t, err)
	dest, err := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)
	mustTest(t, err)
	router.EnablePropagation()
	_, err = router.RegisterPropagationDestination()
	mustTest(t, err)

	// Initial tick should fire immediately
	c.tick(router, dest)

	if !c.lastPeerAnnounce.Equal(currentTime) {
		t.Errorf("lastPeerAnnounce got %v, want %v", c.lastPeerAnnounce, currentTime)
	}
	if !c.lastNodeAnnounce.Equal(currentTime) {
		t.Errorf("lastNodeAnnounce got %v, want %v", c.lastNodeAnnounce, currentTime)
	}

	// Advance time by 0.5s - should NOT fire
	currentTime = currentTime.Add(500 * time.Millisecond)
	c.tick(router, dest)
	if !c.lastPeerAnnounce.Equal(currentTime.Add(-500 * time.Millisecond)) {
		t.Error("lastPeerAnnounce updated prematurely")
	}

	// Advance time to 1.1s total - SHOULD fire
	currentTime = currentTime.Add(600 * time.Millisecond)
	c.tick(router, dest)
	if !c.lastPeerAnnounce.Equal(currentTime) {
		t.Errorf("lastPeerAnnounce not updated; got %v, want %v", c.lastPeerAnnounce, currentTime)
	}
	if !c.lastNodeAnnounce.Equal(currentTime) {
		t.Errorf("lastNodeAnnounce not updated; got %v, want %v", c.lastNodeAnnounce, currentTime)
	}
}
