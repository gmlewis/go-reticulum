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
)

func TestTickFiring(t *testing.T) {
	tempDir := t.TempDir()
	identity, _ := rns.NewIdentity(true)
	router, _ := lxmf.NewRouter(identity, tempDir)
	dest, _ := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)
	router.EnablePropagation()
	_, _ = router.RegisterPropagationDestination()

	peerInterval := 1 // 1 second for test
	nodeInterval := 1 // 1 second for test

	ac = &activeConfig{
		PeerAnnounceInterval: &peerInterval,
		NodeAnnounceInterval: &nodeInterval,
	}

	// Mock clock
	currentTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return currentTime }
	defer func() { now = time.Now }() // Restore after test

	lastPeerAnnounce = time.Time{}
	lastNodeAnnounce = time.Time{}

	// Initial tick should fire immediately
	tick(router, dest)

	if !lastPeerAnnounce.Equal(currentTime) {
		t.Errorf("lastPeerAnnounce got %v, want %v", lastPeerAnnounce, currentTime)
	}
	if !lastNodeAnnounce.Equal(currentTime) {
		t.Errorf("lastNodeAnnounce got %v, want %v", lastNodeAnnounce, currentTime)
	}

	// Advance time by 0.5s - should NOT fire
	currentTime = currentTime.Add(500 * time.Millisecond)
	tick(router, dest)
	if !lastPeerAnnounce.Equal(currentTime.Add(-500 * time.Millisecond)) {
		t.Error("lastPeerAnnounce updated prematurely")
	}

	// Advance time to 1.1s total - SHOULD fire
	currentTime = currentTime.Add(600 * time.Millisecond)
	tick(router, dest)
	if !lastPeerAnnounce.Equal(currentTime) {
		t.Errorf("lastPeerAnnounce not updated; got %v, want %v", lastPeerAnnounce, currentTime)
	}
	if !lastNodeAnnounce.Equal(currentTime) {
		t.Errorf("lastNodeAnnounce not updated; got %v, want %v", lastNodeAnnounce, currentTime)
	}
}
