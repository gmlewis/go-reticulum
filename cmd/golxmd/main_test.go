// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
)

func TestJobs(t *testing.T) {
	tempDir := t.TempDir()
	identity, _ := rns.NewIdentity(true)
	router, _ := lxmf.NewRouter(identity, tempDir)
	dest, _ := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)

	// Actually, Python stores them in seconds.
	// active_configuration["peer_announce_interval"] = lxmd_config["lxmf"].as_int("announce_interval")*60

	peerInterval := 1 // 1 minute = 60s
	nodeInterval := 1 // 1 minute = 60s

	ac = &activeConfig{
		PeerAnnounceInterval: &peerInterval,
		NodeAnnounceInterval: &nodeInterval,
	}

	// We'll mock time.time() equivalent by manually setting lastPeerAnnounce/lastNodeAnnounce
	// But jobs() uses time.Now().
	// To test it without waiting, we'd need to mock time.Now() or use very small intervals.
	// Since intervals in config are in minutes (then *60 for seconds), we can't easily make them < 60s in config.

	// For testing, we'll implement jobsWithTimeout that takes a tick duration

	stop := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(stop)
	}()

	// This just verifies it runs and doesn't crash
	jobs(router, dest, stop, 10*time.Millisecond)
}

func TestAnnounceAtStart(t *testing.T) {
	tempDir := t.TempDir()
	identity, _ := rns.NewIdentity(true)
	router, _ := lxmf.NewRouter(identity, tempDir)
	dest, _ := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)

	ac = &activeConfig{
		PeerAnnounceAtStart: true,
		NodeAnnounceAtStart: true,
	}

	// We'll test runDeferredJobs with a small delay
	// It should call router.Announce and router.AnnouncePropagationNode
	// Since we can't easily mock these, we just verify it runs.
	runDeferredJobs(1*time.Millisecond, router, dest)
}

func TestDeferredStartDelay(t *testing.T) {
	start := time.Now()
	// We'll test a version that takes a duration for testing
	runDeferredJobs(100*time.Millisecond, nil, nil)
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("elapsed %v, want >= 100ms", elapsed)
	}
}

func TestLXMFDelivery(t *testing.T) {
	tempDir := t.TempDir()
	lxmdir = filepath.Join(tempDir, "messages")
	err := os.MkdirAll(lxmdir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Mock message
	id, _ := rns.NewIdentity(true)
	dest, _ := rns.NewDestination(id, rns.DestinationIn, rns.DestinationSingle, "lxmf", "delivery")
	lxm, _ := lxmf.NewMessage(dest, dest, "Hello", "Content", nil)

	// Case 1: No on_inbound
	ac = &activeConfig{OnInbound: ""}
	lxmfDelivery(lxm)
	// Check if file exists in lxmdir
	entries, _ := os.ReadDir(lxmdir)
	if len(entries) != 1 {
		t.Errorf("expected 1 message file, got %v", len(entries))
	}

	// Case 2: with on_inbound (mock script)
	scriptPath := filepath.Join(tempDir, "handler.sh")
	err = os.WriteFile(scriptPath, []byte("#!/bin/sh\necho $1 > "+filepath.Join(tempDir, "result")), 0755)
	if err != nil {
		t.Fatal(err)
	}

	ac = &activeConfig{OnInbound: scriptPath}
	lxmfDelivery(lxm)

	resultPath := filepath.Join(tempDir, "result")
	if _, err := os.Stat(resultPath); os.IsNotExist(err) {
		t.Errorf("on_inbound script was not called")
	}
}

func TestPropagationNodeSetup(t *testing.T) {
	tempDir := t.TempDir()
	identity, _ := rns.NewIdentity(true)
	router, _ := lxmf.NewRouter(identity, tempDir)

	prioritised := []string{"0102030405060708090a0b0c0d0e0f10"}
	controlAllowed := []string{"1112131415161718191a1b1c1d1e1f20"}

	router.SetMessageStorageLimit(500)
	for _, s := range prioritised {
		if h, err := rns.HexToBytes(s); err == nil {
			router.Prioritise(h)
		}
	}
	for _, s := range controlAllowed {
		if h, err := rns.HexToBytes(s); err == nil {
			router.AllowControl(h)
		}
	}
	router.EnablePropagation()

	if !router.PropagationEnabled() {
		t.Errorf("PropagationEnabled: got false, want true")
	}
}

func TestAuthSetup(t *testing.T) {
	tempDir := t.TempDir()
	identity, _ := rns.NewIdentity(true)
	router, _ := lxmf.NewRouter(identity, tempDir)

	allowed := [][]byte{
		{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
	}

	router.SetAuthRequired(true)
	for _, h := range allowed {
		router.Allow(h)
	}

	// We can't easily check if it's applied without calling private methods or testing in lxmf package.
	// But we can verify it doesn't crash and the methods exist.
}

func TestIdentityRemember(t *testing.T) {
	identity, _ := rns.NewIdentity(true)
	dest, _ := rns.NewDestination(identity, rns.DestinationIn, rns.DestinationSingle, "lxmf", "delivery")

	rns.Remember(nil, dest.Hash, identity.GetPublicKey(), nil)

	recalled := rns.Recall(dest.Hash, false)
	if recalled == nil {
		t.Errorf("recalled identity is nil")
	}
}

func TestIgnoreDestinations(t *testing.T) {
	tempDir := t.TempDir()
	identity, _ := rns.NewIdentity(true)
	router, _ := lxmf.NewRouter(identity, tempDir)

	ignored := [][]byte{
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
	}

	for _, h := range ignored {
		router.IgnoreDestination(h)
	}

	if !router.IsIgnored(ignored[0]) {
		t.Errorf("destination not ignored")
	}
}

func TestRouterConstruction(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "lxmd")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	cfg := map[string]map[string]string{
		"propagation": {
			"autopeer":                      "no",
			"autopeer_maxdepth":             "3",
			"propagation_stamp_cost_target": "25",
		},
	}
	ac, err := applyConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}

	identity, _ := rns.NewIdentity(true)
	router, err := lxmf.NewRouterFromConfig(lxmf.RouterConfig{
		Identity:         identity,
		StoragePath:      tempDir,
		Autopeer:         ac.Autopeer,
		AutopeerMaxdepth: ac.AutopeerMaxdepth,
		PropagationCost:  ac.PropagationStampCostTarget,
	})
	if err != nil {
		t.Fatal(err)
	}

	if router.PropagationEnabled() {
		// Should not be enabled yet
		t.Errorf("PropagationEnabled: got true, want false")
	}
	// Note: We can't easily check private fields of Router unless we add getters or the test is in lxmf package.
	// But NewRouterFromConfig is in lxmf package.
}

func TestServiceLogging(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "lxmd")
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// We'll test a function that sets up logging based on service flag and config dir
	setupLogging(true, configDir)

	if rns.GetLogDest() != rns.LogDestFile {
		t.Errorf("LogDest: got %v, want %v", rns.GetLogDest(), rns.LogDestFile)
	}
	wantLogPath := filepath.Join(configDir, "logfile")
	if rns.GetLogFilePath() != wantLogPath {
		t.Errorf("LogFilePath: got %q, want %q", rns.GetLogFilePath(), wantLogPath)
	}

	// Reset for other tests
	rns.SetLogDest(rns.LogStdout)
	rns.SetLogFilePath("")
}

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
	storagePath, identityPath, err := resolvePaths(storageRoot, "", storageRoot)
	if err != nil {
		t.Fatalf("resolvePaths: %v", err)
	}
	if storagePath != storageRoot {
		t.Fatalf("storagePath=%q want=%q", storagePath, storageRoot)
	}
	wantIdentity := filepath.Join(storageRoot, "identity")
	if identityPath != wantIdentity {
		t.Fatalf("identityPath=%q want=%q", identityPath, wantIdentity)
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
