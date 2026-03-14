// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseAllowedIdentities(t *testing.T) {
	validHash := "00112233445566778899aabbccddeeff"
	values, err := parseAllowedIdentities(validHash + ", " + validHash)
	if err != nil {
		t.Fatalf("parseAllowedIdentities: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("len(values)=%v want=2", len(values))
	}
	if len(values[0]) != 16 {
		t.Fatalf("len(values[0])=%v want=16", len(values[0]))
	}
}

func TestParseAllowedIdentitiesErrors(t *testing.T) {
	if _, err := parseAllowedIdentities("zz-not-hex"); err == nil {
		t.Fatal("expected parse error for invalid hex")
	}

	shortHash := "00112233"
	if _, err := parseAllowedIdentities(shortHash); err == nil {
		t.Fatal("expected length error for short hash")
	}
}

func TestResolvePathsDefaults(t *testing.T) {
	storageRoot := t.TempDir()
	storagePath, identityPath, err := resolvePaths(storageRoot, "")
	if err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if storagePath != storageRoot {
		t.Fatalf("storagePath=%q want=%q", storagePath, storageRoot)
	}
	wantIdentity := filepath.Join(storageRoot, "identities", "lxmd")
	if identityPath != wantIdentity {
		t.Fatalf("identityPath=%q want=%q", identityPath, wantIdentity)
	}
}

func TestTickChanNil(t *testing.T) {
	if ch := tickChan(nil); ch != nil {
		t.Fatal("expected nil channel for nil ticker")
	}
}

func TestTickChanTicker(t *testing.T) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	if ch := tickChan(ticker); ch == nil {
		t.Fatal("expected non-nil channel for ticker")
	}
}

func TestRunOperationalLoopWithHandlersTicksAndStops(t *testing.T) {
	done := make(chan struct{})
	finished := make(chan struct{})

	var maintenanceCount int32
	var outboundCount int32
	var announceCount int32
	var syncCount int32

	go func() {
		runOperationalLoopWithHandlers(
			5*time.Millisecond,
			7*time.Millisecond,
			9*time.Millisecond,
			11*time.Millisecond,
			done,
			func() { atomic.AddInt32(&maintenanceCount, 1) },
			func() { atomic.AddInt32(&outboundCount, 1) },
			func() { atomic.AddInt32(&announceCount, 1) },
			func() { atomic.AddInt32(&syncCount, 1) },
		)
		close(finished)
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&maintenanceCount) > 0 &&
			atomic.LoadInt32(&outboundCount) > 0 &&
			atomic.LoadInt32(&announceCount) > 0 &&
			atomic.LoadInt32(&syncCount) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if atomic.LoadInt32(&maintenanceCount) == 0 {
		t.Fatal("expected maintenance handler to be called at least once")
	}
	if atomic.LoadInt32(&outboundCount) == 0 {
		t.Fatal("expected outbound handler to be called at least once")
	}
	if atomic.LoadInt32(&announceCount) == 0 {
		t.Fatal("expected announce handler to be called at least once")
	}
	if atomic.LoadInt32(&syncCount) == 0 {
		t.Fatal("expected sync handler to be called at least once")
	}

	close(done)

	select {
	case <-finished:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for operational loop to stop")
	}

	stoppedMaintenance := atomic.LoadInt32(&maintenanceCount)
	stoppedOutbound := atomic.LoadInt32(&outboundCount)
	stoppedAnnounce := atomic.LoadInt32(&announceCount)
	stoppedSync := atomic.LoadInt32(&syncCount)
	time.Sleep(40 * time.Millisecond)

	if atomic.LoadInt32(&maintenanceCount) != stoppedMaintenance {
		t.Fatal("expected maintenance handler calls to stop after done close")
	}
	if atomic.LoadInt32(&outboundCount) != stoppedOutbound {
		t.Fatal("expected outbound handler calls to stop after done close")
	}
	if atomic.LoadInt32(&announceCount) != stoppedAnnounce {
		t.Fatal("expected announce handler calls to stop after done close")
	}
	if atomic.LoadInt32(&syncCount) != stoppedSync {
		t.Fatal("expected sync handler calls to stop after done close")
	}
}

func TestLoadOrCreateIdentityCreateThenReload(t *testing.T) {
	identityPath := filepath.Join(t.TempDir(), "identities", "lxmd")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
		t.Fatalf("mkdir identity dir: %v", err)
	}

	created, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity(create): %v", err)
	}
	if created == nil || len(created.Hash) == 0 {
		t.Fatal("expected created identity with non-empty hash")
	}

	reloaded, err := loadOrCreateIdentity(identityPath)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity(reload): %v", err)
	}
	if reloaded == nil || len(reloaded.Hash) == 0 {
		t.Fatal("expected reloaded identity with non-empty hash")
	}
	if string(reloaded.Hash) != string(created.Hash) {
		t.Fatalf("reloaded hash mismatch got=%x want=%x", reloaded.Hash, created.Hash)
	}
}

func TestLoadOrCreateIdentityCorruptFile(t *testing.T) {
	identityPath := filepath.Join(t.TempDir(), "identities", "lxmd")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
		t.Fatalf("mkdir identity dir: %v", err)
	}
	if err := os.WriteFile(identityPath, []byte("corrupt-identity"), 0o644); err != nil {
		t.Fatalf("write corrupt identity: %v", err)
	}

	if _, err := loadOrCreateIdentity(identityPath); err == nil {
		t.Fatal("expected error for corrupt identity file")
	}
}

func TestRuntimeTrackerLifecycle(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "lxmf", "golxmd-state.json")

	tracker, err := newRuntimeTracker(statePath)
	if err != nil {
		t.Fatalf("newRuntimeTracker: %v", err)
	}
	if tracker.WasUncleanShutdown() {
		t.Fatal("expected clean initial startup state")
	}

	now := time.Now()
	if err := tracker.RecordAnnounce(now); err != nil {
		t.Fatalf("RecordAnnounce: %v", err)
	}
	if err := tracker.RecordSync(now.Add(time.Second)); err != nil {
		t.Fatalf("RecordSync: %v", err)
	}

	loaded, err := loadRuntimeState(statePath)
	if err != nil {
		t.Fatalf("loadRuntimeState: %v", err)
	}
	if loaded.CleanShutdown {
		t.Fatal("expected runtime state to be unclean before explicit shutdown mark")
	}
	if loaded.LastAnnounce == 0 {
		t.Fatal("expected LastAnnounce to be persisted")
	}
	if loaded.LastSync == 0 {
		t.Fatal("expected LastSync to be persisted")
	}

	if err := tracker.MarkCleanShutdown(); err != nil {
		t.Fatalf("MarkCleanShutdown: %v", err)
	}
	loaded, err = loadRuntimeState(statePath)
	if err != nil {
		t.Fatalf("loadRuntimeState after clean mark: %v", err)
	}
	if !loaded.CleanShutdown {
		t.Fatal("expected clean shutdown marker to be persisted")
	}
}

func TestRuntimeTrackerDetectsUncleanRestart(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "lxmf", "golxmd-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	seed := []byte(`{"clean_shutdown":false,"last_announce_unix":1,"last_sync_unix":2}`)
	if err := os.WriteFile(statePath, seed, 0o644); err != nil {
		t.Fatalf("write seed state: %v", err)
	}

	tracker, err := newRuntimeTracker(statePath)
	if err != nil {
		t.Fatalf("newRuntimeTracker: %v", err)
	}
	if !tracker.WasUncleanShutdown() {
		t.Fatal("expected unclean-shutdown detection to be true")
	}
}

func TestLoadRuntimeStateCorruptData(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "lxmf", "golxmd-state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	if _, err := loadRuntimeState(statePath); err == nil {
		t.Fatal("expected loadRuntimeState to fail for corrupt data")
	}
}
