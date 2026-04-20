// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

const tempDirPrefix = "golxmd-test-"

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("golxmd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--config", "/tmp/config", "--rnsconfig", "/tmp/rns", "--status", "--verbose", "--quiet", "--timeout", "2"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.configDir != "/tmp/config" {
		t.Fatalf("configDir = %q, want %q", app.configDir, "/tmp/config")
	}
	if app.rnsConfigDir != "/tmp/rns" {
		t.Fatalf("rnsConfigDir = %q, want %q", app.rnsConfigDir, "/tmp/rns")
	}
	if !app.displayStatus {
		t.Fatal("displayStatus = false, want true")
	}
	if app.verbosity != 1 {
		t.Fatalf("verbosity = %v, want %v", app.verbosity, 1)
	}
	if app.quietness != 1 {
		t.Fatalf("quietness = %v, want %v", app.quietness, 1)
	}
	if app.timeout != 2*time.Second {
		t.Fatalf("timeout = %v, want 2s", app.timeout)
	}
}

func TestNewRuntimeOwnsLogger(t *testing.T) {
	t.Parallel()
	runtime := newRuntime(newApp())
	if runtime == nil {
		t.Fatal("newRuntime returned nil")
	}
	if runtime.logger == nil {
		t.Fatal("runtime logger is nil")
	}
	if runtime.client == nil {
		t.Fatal("runtime client is nil")
	}
	if runtime.client.logger != runtime.logger {
		t.Fatalf("client logger %p does not match runtime logger %p", runtime.client.logger, runtime.logger)
	}
	if runtime.client.ts == nil {
		t.Fatal("runtime client transport system is nil")
	}
	if runtime.client.now == nil {
		t.Fatal("runtime client clock is nil")
	}
}

func TestJobs(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	c := &clientT{
		ts:  rns.NewTransportSystem(nil),
		now: time.Now,
	}
	router, err := lxmf.NewRouter(c.ts, identity, tmpDir)
	mustTest(t, err)
	dest, err := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)
	mustTest(t, err)

	// Actually, Python stores them in seconds.
	// active_configuration["peer_announce_interval"] = lxmd_config["lxmf"].as_int("announce_interval")*60

	peerInterval := 1 // 1 minute = 60s
	nodeInterval := 1 // 1 minute = 60s

	c.ac = &activeConfig{
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
	c.jobs(router, dest, stop, 10*time.Millisecond)
}

func TestJobsRecovery(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	c := &clientT{
		ts:  rns.NewTransportSystem(nil),
		now: func() time.Time { panic("boom") },
	}
	router, err := lxmf.NewRouter(c.ts, identity, tmpDir)
	mustTest(t, err)
	dest, err := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)
	mustTest(t, err)
	c.ac = &activeConfig{}

	stop := make(chan struct{})
	close(stop)

	c.jobs(router, dest, stop, 10*time.Millisecond)
}

func TestAnnounceAtStart(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	c := &clientT{
		ts:  rns.NewTransportSystem(nil),
		now: time.Now,
	}
	router, err := lxmf.NewRouter(c.ts, identity, tmpDir)
	mustTest(t, err)
	dest, err := router.RegisterDeliveryIdentity(identity, "Test Peer", nil)
	mustTest(t, err)

	c.ac = &activeConfig{
		PeerAnnounceAtStart: true,
		NodeAnnounceAtStart: true,
	}

	stopJobs := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(stopJobs)
	}()
	c.runDeferredThenJobs(1*time.Millisecond, router, dest, stopJobs, 1*time.Second)
}

func TestDeferredStartDelay(t *testing.T) {
	start := time.Now()
	stopJobs := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		close(stopJobs)
	}()
	c := &clientT{
		now: time.Now,
	}
	c.runDeferredThenJobs(100*time.Millisecond, nil, nil, stopJobs, 1*time.Second)
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("elapsed %v, want >= 100ms", elapsed)
	}
}

func TestJobsStartAfterDeferred(t *testing.T) {
	currentTime := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	c := &clientT{
		ac:  &activeConfig{},
		now: func() time.Time { return currentTime },
	}

	stopJobs := make(chan struct{})

	// Schedule stop after deferred delay + a few job ticks.
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(stopJobs)
	}()

	// runDeferredThenJobs blocks: first deferred, then jobs loop until
	// stopJobs is closed. When it returns, everything has stopped.
	c.runDeferredThenJobs(50*time.Millisecond, nil, nil, stopJobs, 1*time.Millisecond)

	// After deferred completes and jobs are stopped, announce times
	// should have been set to currentTime by runDeferredThenJobs.
	if !c.lastPeerAnnounce.Equal(currentTime) {
		t.Errorf("lastPeerAnnounce = %v, want %v", c.lastPeerAnnounce, currentTime)
	}
	if !c.lastNodeAnnounce.Equal(currentTime) {
		t.Errorf("lastNodeAnnounce = %v, want %v", c.lastNodeAnnounce, currentTime)
	}
}

func TestLXMFDelivery(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	c := &clientT{
		lxmdir: filepath.Join(tmpDir, "messages"),
	}
	err := os.MkdirAll(c.lxmdir, 0o755)
	mustTest(t, err)

	// Mock message
	id, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	dest, err := rns.NewDestination(ts, id, rns.DestinationIn, rns.DestinationSingle, "lxmf", "delivery")
	mustTest(t, err)
	lxm, err := lxmf.NewMessage(dest, dest, "Hello", "Content", nil)
	mustTest(t, err)

	// Case 1: No on_inbound
	c.ac = &activeConfig{OnInbound: ""}
	c.lxmfDelivery(lxm)
	// Check if file exists in lxmdir
	entries, err := os.ReadDir(c.lxmdir)
	mustTest(t, err)
	if len(entries) != 1 {
		t.Errorf("expected 1 message file, got %v", len(entries))
	}

	// Case 2: with on_inbound (mock script)
	scriptPath := filepath.Join(tmpDir, "handler.sh")
	err = os.WriteFile(scriptPath, []byte("#!/bin/sh\necho $1 > "+filepath.Join(tmpDir, "result")), 0o755)
	mustTest(t, err)

	c.ac = &activeConfig{OnInbound: scriptPath}
	c.lxmfDelivery(lxm)

	resultPath := filepath.Join(tmpDir, "result")
	if _, err := os.Stat(resultPath); os.IsNotExist(err) {
		t.Errorf("on_inbound script was not called")
	}

	// Case 3: Multi-word command
	_ = os.Remove(resultPath)
	c.ac = &activeConfig{OnInbound: scriptPath + " --some-arg"}
	c.lxmfDelivery(lxm)
	if _, err := os.Stat(resultPath); os.IsNotExist(err) {
		t.Errorf("multi-word on_inbound script was not called")
	}

	// Case 4: Quoted argument parsing should match Python shlex.split behavior.
	quotedResultPath := filepath.Join(tmpDir, "quoted-result")
	quotedScriptPath := filepath.Join(tmpDir, "quoted-handler.sh")
	err = os.WriteFile(quotedScriptPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\" > "+quotedResultPath), 0o755)
	mustTest(t, err)

	c.ac = &activeConfig{OnInbound: quotedScriptPath + " --label 'quoted value'"}
	c.lxmfDelivery(lxm)

	quotedResult, err := os.ReadFile(quotedResultPath)
	mustTest(t, err)
	gotLines := strings.Split(strings.TrimSpace(string(quotedResult)), "\n")
	wantLines := []string{"--label", "quoted value"}
	if len(gotLines) != len(wantLines) {
		t.Fatalf("quoted on_inbound arg count = %v, want %v (%q)", len(gotLines), len(wantLines), quotedResult)
	}
	for i, want := range wantLines {
		if gotLines[i] != want {
			t.Fatalf("quoted on_inbound arg %v = %q, want %q", i, gotLines[i], want)
		}
	}
}

func TestPropagationNodeSetup(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	c := &clientT{
		ts: rns.NewTransportSystem(nil),
	}
	router, err := lxmf.NewRouter(c.ts, identity, tmpDir)
	mustTest(t, err)

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

	// Verify control destination is created
	cd, err := router.RegisterPropagationControlDestination(nil)
	if err != nil {
		t.Fatalf("RegisterPropagationControlDestination: %v", err)
	}
	if cd == nil {
		t.Fatal("expected non-nil control destination")
	}
}

func TestAuthWarningMessage(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	var capturedLog string
	c := &clientT{
		ts:         rns.NewTransportSystem(nil),
		ac:         &activeConfig{AuthRequired: true, AllowedIdentities: nil},
		configpath: filepath.Join(tmpDir, "config"),
		logger: func() *rns.Logger {
			logger := rns.NewLogger()
			logger.SetLogDest(rns.LogCallback)
			logger.SetLogCallback(func(s string) {
				capturedLog += s
			})
			logger.SetLogLevel(rns.LogInfo)
			return logger
		}(),
	}

	id, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	router, _ := lxmf.NewRouter(c.ts, id, tmpDir)

	c.setupAuth(router)

	want := "Client authentication was enabled, but no identity hashes could be loaded from " + filepath.Join(tmpDir, "allowed") + ". Nobody will be able to sync messages from this propagation node."
	if !strings.Contains(capturedLog, want) {
		t.Errorf("captured log does not contain expected message.\ngot: %q\nwant: %q", capturedLog, want)
	}
}

func TestAuthSetup(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	router, _ := lxmf.NewRouter(ts, identity, tmpDir)

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
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	dest, err := rns.NewDestination(ts, identity, rns.DestinationIn, rns.DestinationSingle, "lxmf", "delivery")
	mustTest(t, err)

	ts.Remember(nil, dest.Hash, identity.GetPublicKey(), nil)

	recalled := ts.Recall(dest.Hash)
	if recalled == nil {
		t.Errorf("recalled identity is nil")
	}
}

func TestIgnoreDestinations(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	router, _ := lxmf.NewRouter(ts, identity, tmpDir)

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
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	configDir := filepath.Join(tmpDir, "lxmd")
	err := os.MkdirAll(configDir, 0o755)
	mustTest(t, err)

	cfg := map[string]map[string]string{
		"propagation": {
			"autopeer":                      "no",
			"autopeer_maxdepth":             "3",
			"propagation_stamp_cost_target": "25",
		},
	}
	c := &clientT{}
	ac, err := c.applyConfig(cfg)
	mustTest(t, err)

	identity, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	router, err := lxmf.NewRouterFromConfig(ts, lxmf.RouterConfig{
		Identity:         identity,
		StoragePath:      tmpDir,
		Autopeer:         ac.Autopeer,
		AutopeerMaxdepth: ac.AutopeerMaxdepth,
		PropagationCost:  ac.PropagationStampCostTarget,
	})
	mustTest(t, err)

	if router.PropagationEnabled() {
		// Should not be enabled yet
		t.Errorf("PropagationEnabled: got true, want false")
	}
	// Note: We can't easily check private fields of Router unless we add getters or the test is in lxmf package.
	// But NewRouterFromConfig is in lxmf package.
}

func TestApplyTimeoutDefaults(t *testing.T) {
	tests := []struct {
		name          string
		displayStatus bool
		displayPeers  bool
		syncHash      string
		unpeerHash    string
		want          time.Duration
	}{
		{"nothing", false, false, "", "", 0},
		{"status-default", true, false, "", "", 5 * time.Second},
		{"peers-default", false, true, "", "", 5 * time.Second},
		{"sync-default", false, false, "hash", "", 10 * time.Second},
		{"unpeer-default", false, false, "", "hash", 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyTimeoutDefaults(tt.displayStatus, tt.displayPeers, tt.syncHash, tt.unpeerHash)
			if got != tt.want {
				t.Errorf("applyTimeoutDefaults = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolvePathsDefaults(t *testing.T) {
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	storageRoot := td
	c := &clientT{}
	storagePath, identityPath, err := c.resolvePaths(storageRoot, "", storageRoot)
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

	if !isDir(c.lxmdir) {
		t.Errorf("lxmdir %q not created", c.lxmdir)
	}
	wantLxmdir := filepath.Join(storageRoot, "messages")
	if c.lxmdir != wantLxmdir {
		t.Errorf("lxmdir=%q want=%q", c.lxmdir, wantLxmdir)
	}
}

func TestLoadOrCreateIdentityCreateThenReload(t *testing.T) {
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identityPath := filepath.Join(td, "identities", "lxmd")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
		t.Fatalf("mkdir identity dir: %v", err)
	}

	c := &clientT{}
	created, err := c.loadOrCreateIdentity(identityPath)
	if err != nil {
		t.Fatalf("loadOrCreateIdentity(create): %v", err)
	}
	if created == nil || len(created.Hash) == 0 {
		t.Fatal("expected created identity with non-empty hash")
	}

	reloaded, err := c.loadOrCreateIdentity(identityPath)
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
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	identityPath := filepath.Join(td, "identities", "lxmd")
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
		t.Fatalf("mkdir identity dir: %v", err)
	}
	if err := os.WriteFile(identityPath, []byte("corrupt-identity"), 0o644); err != nil {
		t.Fatalf("write corrupt identity: %v", err)
	}

	c := &clientT{}
	if _, err := c.loadOrCreateIdentity(identityPath); err == nil {
		t.Fatal("expected error for corrupt identity file")
	}
}

func TestRuntimeTrackerLifecycle(t *testing.T) {
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	statePath := filepath.Join(td, "lxmf", "golxmd-state.json")

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
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	statePath := filepath.Join(td, "lxmf", "golxmd-state.json")
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
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	statePath := filepath.Join(td, "lxmf", "golxmd-state.json")
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
