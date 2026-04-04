// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// golxmd is the Reticulum-based LXMF routing daemon.
//
// It provides a local LXMF router and delivery node, managing identities,
// peer pruning, and message propagation for the local system.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
)

type clientT struct {
	ac     *activeConfig
	lxmdir string

	lastPeerAnnounce time.Time
	lastNodeAnnounce time.Time

	ts rns.Transport
	tr *runtimeTracker

	now       func() time.Time
	tickCount int

	configpath   string
	identitypath string
	identity     *rns.Identity

	// Function pointers for mocking in tests
	mockRequestSync   func(id *rns.Identity, targetHash []byte, remoteIdentity *rns.Identity, timeout time.Duration, exitOnFail bool) (any, error)
	mockRequestUnpeer func(id *rns.Identity, targetHash []byte, remoteIdentity *rns.Identity, timeout time.Duration, exitOnFail bool) (any, error)
}

const (
	jobsInterval        = 5 * time.Second
	maintenanceInterval = 10 // Maintenance every 10 ticks (50s)
)

func main() {
	log.SetFlags(0)
	app := newApp()
	app.initFlags(flag.CommandLine)
	flag.Usage = app.usage
	flag.Parse()
	app.run()
}

func (a *appT) run() {
	if a.timeout == 0 {
		a.timeout = applyTimeoutDefaults(a.displayStatus, a.displayPeers, a.syncHash, a.unpeerHash)
	}

	if a.version {
		fmt.Printf("golxmd %v\n", rns.VERSION)
		return
	}

	if a.exampleConfig {
		fmt.Print(defaultLXMDaemonConfig)
		return
	}

	c := &clientT{
		ts:  rns.NewTransportSystem(),
		now: time.Now,
	}

	if a.displayStatus || a.displayPeers {
		c.getStatus(a.remoteHash, a.configDir, a.rnsConfigDir, int(a.verbosity), int(a.quietness), a.timeout, a.displayStatus, a.displayPeers, a.identityPath)
		return
	}

	if a.syncHash != "" {
		c.requestSync(a.syncHash, a.remoteHash, a.configDir, a.rnsConfigDir, int(a.verbosity), int(a.quietness), a.timeout, a.identityPath)
		return
	}

	if a.unpeerHash != "" {
		c.requestUnpeer(a.unpeerHash, a.remoteHash, a.configDir, a.rnsConfigDir, int(a.verbosity), int(a.quietness), a.timeout, a.identityPath)
		return
	}

	a.configDir = resolveConfigDir(a.configDir)
	if err := ensureConfig(a.configDir); err != nil {
		log.Fatalf("ensure config: %v", err)
	}

	var err error
	c.ac, err = loadConfig(a.configDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	rns.SetLogLevel(resolveLogLevel(c.ac.LogLevel, int(a.verbosity), int(a.quietness)))
	setupLogging(a.runAsService, a.configDir)
	rns.Logf("Configuration loaded from %v", rns.LogVerbose, false, a.configDir)

	rns.Logf("Substantiating Reticulum...", rns.LogInfo, false)
	if _, err := rns.NewReticulum(c.ts, a.rnsConfigDir); err != nil {
		log.Fatalf("initialize Reticulum: %v", err)
	}

	var storagePath, identityPath string
	resolvedStorage, resolvedIdentityPath, err := c.resolvePaths(storagePath, identityPath, a.configDir)
	if err != nil {
		log.Fatalf("resolve paths: %v", err)
	}

	identity, err := loadOrCreateIdentity(resolvedIdentityPath)
	if err != nil {
		log.Fatalf("load identity: %v", err)
	}

	if a.cmdOnInbound != "" {
		// Note: Python's lxmd.py accepts the --on-inbound CLI arg but fails to
		// actually apply it to the configuration (it's a Python bug). Go
		// correctly applies it here.
		c.ac.OnInbound = a.cmdOnInbound
	}

	router, err := lxmf.NewRouterFromConfig(c.ts, lxmf.RouterConfig{
		Identity:                   c.identity,
		StoragePath:                resolvedStorage,
		Autopeer:                   c.ac.Autopeer,
		AutopeerMaxdepth:           c.ac.AutopeerMaxdepth,
		PropagationLimit:           c.ac.PropagationTransferMaxAcceptedSize,
		SyncLimit:                  c.ac.PropagationSyncMaxAcceptedSize,
		DeliveryLimit:              c.ac.DeliveryTransferMaxAcceptedSize,
		MaxPeers:                   c.ac.MaxPeers,
		StaticPeers:                c.ac.StaticPeers,
		FromStaticOnly:             c.ac.FromStaticOnly,
		PropagationCost:            c.ac.PropagationStampCostTarget,
		PropagationCostFlexibility: c.ac.PropagationStampCostFlexibility,
		PeeringCost:                c.ac.PeeringCost,
		MaxPeeringCost:             c.ac.RemotePeeringCostMax,
		Name:                       c.ac.NodeName,
	})
	if err != nil {
		log.Fatalf("create LXMF router: %v", err)
	}

	router.RegisterDeliveryCallback(c.lxmfDelivery)

	for _, h := range c.ac.IgnoredLXMFDestinations {
		router.IgnoreDestination(h)
	}

	lxmfDestination, err := router.RegisterDeliveryIdentity(identity, c.ac.DisplayName, nil)
	if err != nil {
		log.Fatalf("register delivery destination: %v", err)
	}
	rns.Logf("LXMF Router ready to receive on %v", rns.LogInfo, false, rns.PrettyHex(lxmfDestination.Hash))

	c.ts.Remember(nil, lxmfDestination.Hash, identity.GetPublicKey(), nil)

	c.setupAuth(router)

	if a.runAsPropagationNode || c.ac.EnablePropagationNode {
		router.SetMessageStorageLimit(c.ac.MessageStorageLimit)
		for _, s := range c.ac.PrioritisedLXMFDestinations {
			if h, err := rns.HexToBytes(s); err == nil {
				router.Prioritise(h)
			}
		}
		for _, s := range c.ac.ControlAllowedIdentities {
			if h, err := rns.HexToBytes(s); err == nil {
				router.AllowControl(h)
			}
		}
		router.EnablePropagation()
		propDest, err := router.RegisterPropagationDestination()
		if err != nil {
			log.Fatalf("register propagation destination: %v", err)
		}
		rns.Logf("LXMF Propagation Node started on %v", rns.LogInfo, false, rns.PrettyHex(propDest.Hash))

		allowed := make([][]byte, 0, len(c.ac.ControlAllowedIdentities))
		for _, s := range c.ac.ControlAllowedIdentities {
			if h, err := rns.HexToBytes(s); err == nil {
				allowed = append(allowed, h)
			}
		}
		if _, err := router.RegisterPropagationControlDestination(allowed); err != nil {
			log.Fatalf("register control destination: %v", err)
		}
	}

	runtimeStatePath := filepath.Join(resolvedStorage, "lxmf", "golxmd-state.json")
	c.tr, err = newRuntimeTracker(runtimeStatePath)
	if err != nil {
		log.Fatalf("initialize runtime tracker: %v", err)
	}
	if c.tr.WasUncleanShutdown() {
		log.Printf("golxmd detected unclean previous shutdown; entering recovery-aware startup")
	}

	log.Printf("golxmd running with identity %x", identity.Hash)
	rns.Logf("Started golxmd version %v", rns.LogNotice, false, rns.VERSION)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	stopJobs := make(chan struct{})
	go c.runDeferredThenJobs(10*time.Second, router, lxmfDestination, stopJobs, jobsInterval)

	<-stop
	fmt.Println()
	close(stopJobs)
	if err := c.tr.MarkCleanShutdown(); err != nil {
		log.Printf("golxmd failed to persist clean shutdown marker: %v", err)
	}

	log.Printf("golxmd shutting down")
}

func applyTimeoutDefaults(displayStatus, displayPeers bool, syncHash, unpeerHash string) time.Duration {
	if displayStatus || displayPeers {
		return 5 * time.Second
	}
	if syncHash != "" || unpeerHash != "" {
		return 10 * time.Second
	}
	return 0
}
func (c *clientT) jobs(router *lxmf.Router, lxmfDestination *rns.Destination, stop <-chan struct{}, interval time.Duration) {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					rns.Logf("An error occurred while running periodic jobs. The contained exception was: %v", rns.LogError, false, r)
				}
			}()
			c.tick(router, lxmfDestination)
		}()
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
	}
}

func (c *clientT) tick(router *lxmf.Router, lxmfDestination *rns.Destination) {
	if c.ac == nil {
		return
	}

	c.tickCount++
	currentTime := c.now()

	if c.ac.PeerAnnounceInterval != nil {
		if currentTime.Sub(c.lastPeerAnnounce) > time.Duration(*c.ac.PeerAnnounceInterval)*time.Second {
			rns.Logf("Sending announce for LXMF delivery destination", rns.LogVerbose, false)
			if err := router.Announce(lxmfDestination.Hash); err != nil {
				rns.Logf("Failed to announce delivery destination: %v", rns.LogError, false, err)
			}
			c.lastPeerAnnounce = currentTime
			if c.tr != nil {
				if err := c.tr.RecordAnnounce(c.lastPeerAnnounce); err != nil {
					rns.Logf("Failed to record announce: %v", rns.LogWarning, false, err)
				}
			}
		}
	}

	if c.ac.NodeAnnounceInterval != nil {
		if currentTime.Sub(c.lastNodeAnnounce) > time.Duration(*c.ac.NodeAnnounceInterval)*time.Second {
			rns.Logf("Sending announce for LXMF Propagation Node", rns.LogVerbose, false)
			if err := router.Announce(lxmfDestination.Hash); err != nil {
				rns.Logf("Failed to announce propagation destination: %v", rns.LogError, false, err)
			}
			c.lastNodeAnnounce = currentTime
			if c.tr != nil {
				if err := c.tr.RecordSync(c.lastNodeAnnounce); err != nil {
					rns.Logf("Failed to record sync: %v", rns.LogWarning, false, err)
				}
			}
		}
	}

	if router != nil {
		router.ProcessOutbound()
	}

	if c.tickCount%maintenanceInterval == 0 && router != nil {
		pruned := router.PruneStalePeers()
		if pruned > 0 {
			rns.Logf("golxmd pruned %v stale peers", rns.LogInfo, false, pruned)
		}
	}
}

func setupLogging(service bool, configDir string) {
	if service {
		rns.SetLogDest(rns.LogDestFile)
		rns.SetLogFilePath(filepath.Join(configDir, "logfile"))
	} else {
		rns.SetLogDest(rns.LogStdout)
	}
}

func (c *clientT) lxmfDelivery(lxm *lxmf.Message) {
	writtenPath, err := lxm.WriteToDirectory(c.lxmdir)
	if err != nil {
		rns.Logf("Error occurred while processing received message %v. The contained exception was: %v", rns.LogError, false, lxm, err)
		return
	}
	rns.Logf("Received %v written to %v", rns.LogDebug, false, lxm, writtenPath)

	if c.ac != nil && c.ac.OnInbound != "" {
		rns.Logf("Calling external program to handle message", rns.LogDebug, false)
		parts := strings.Fields(c.ac.OnInbound)
		if len(parts) > 0 {
			cmd := exec.Command(parts[0], append(parts[1:], writtenPath)...)
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Run(); err != nil {
				rns.Logf("Error occurred while calling external program: %v", rns.LogError, false, err)
			}
		}
	} else {
		rns.Logf("No action defined for inbound messages, ignoring", rns.LogDebug, false)
	}
}

func (c *clientT) runDeferredThenJobs(delay time.Duration, router *lxmf.Router, lxmfDestination *rns.Destination, stopJobs <-chan struct{}, jobsInterval time.Duration) {
	time.Sleep(delay)
	rns.Logf("Running deferred start jobs", rns.LogDebug, false)

	if c.ac != nil && c.ac.PeerAnnounceAtStart && router != nil && lxmfDestination != nil {
		rns.Logf("Sending announce for LXMF delivery destination", rns.LogExtreme, false)
		if err := router.Announce(lxmfDestination.Hash); err != nil {
			rns.Logf("Failed to announce delivery destination at start: %v", rns.LogError, false, err)
		}
	}

	if c.ac != nil && c.ac.NodeAnnounceAtStart && router != nil {
		rns.Logf("Sending announce for LXMF Propagation Node", rns.LogExtreme, false)
		router.AnnouncePropagationNode()
	}

	c.lastPeerAnnounce = c.now()
	c.lastNodeAnnounce = c.now()

	c.jobs(router, lxmfDestination, stopJobs, jobsInterval)
}

func (c *clientT) setupAuth(router *lxmf.Router) {
	if c.ac.AuthRequired {
		router.SetAuthRequired(true)
		if len(c.ac.AllowedIdentities) > 0 {
			for _, h := range c.ac.AllowedIdentities {
				router.Allow(h)
			}
		} else {
			allowedPath := filepath.Join(filepath.Dir(c.configpath), "allowed")
			rns.Logf("Client authentication was enabled, but no identity hashes could be loaded from %v. Nobody will be able to sync messages from this propagation node.", rns.LogWarning, false, allowedPath)
		}
	}
}

func (c *clientT) resolvePaths(storagePath, identityPath, configDir string) (string, string, error) {
	if storagePath == "" {
		storagePath = filepath.Join(configDir, "storage")
	}
	c.lxmdir = filepath.Join(storagePath, "messages")

	if err := os.MkdirAll(c.lxmdir, 0o755); err != nil {
		return "", "", fmt.Errorf("create messages path %q: %w", c.lxmdir, err)
	}

	if identityPath == "" {
		identityPath = filepath.Join(configDir, "identity")
	}
	if err := os.MkdirAll(filepath.Dir(identityPath), 0o755); err != nil {
		return "", "", fmt.Errorf("create identity directory %q: %w", filepath.Dir(identityPath), err)
	}

	return storagePath, identityPath, nil
}

func loadOrCreateIdentity(identityPath string) (*rns.Identity, error) {
	if _, err := os.Stat(identityPath); err == nil {
		identity, err := rns.FromFile(identityPath)
		if err != nil {
			return nil, fmt.Errorf("read identity from %q: %w", identityPath, err)
		}
		if identity != nil {
			rns.Logf("Loaded Primary Identity %v", rns.LogInfo, false, identity)
		}
		return identity, nil
	}

	rns.Logf("No Primary Identity file found, creating new...", rns.LogInfo, false)
	identity, err := rns.NewIdentity(true)
	if err != nil {
		return nil, fmt.Errorf("create identity: %w", err)
	}
	if err := identity.ToFile(identityPath); err != nil {
		return nil, fmt.Errorf("persist identity to %q: %w", identityPath, err)
	}

	rns.Logf("Created new Primary Identity %v", rns.LogInfo, false, identity)
	return identity, nil
}

type daemonRuntimeState struct {
	CleanShutdown bool  `json:"clean_shutdown"`
	LastAnnounce  int64 `json:"last_announce_unix"`
	LastSync      int64 `json:"last_sync_unix"`
}

type runtimeTracker struct {
	path       string
	mu         sync.Mutex
	state      daemonRuntimeState
	wasUnclean bool
}

func newRuntimeTracker(statePath string) (*runtimeTracker, error) {
	state, err := loadRuntimeState(statePath)
	if err != nil {
		return nil, err
	}

	tracker := &runtimeTracker{
		path:       statePath,
		state:      state,
		wasUnclean: !state.CleanShutdown,
	}

	tracker.state.CleanShutdown = false
	if err := tracker.persistLocked(); err != nil {
		return nil, err
	}

	return tracker, nil
}

func (rt *runtimeTracker) WasUncleanShutdown() bool {
	return rt.wasUnclean
}

func (rt *runtimeTracker) RecordAnnounce(at time.Time) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.LastAnnounce = at.Unix()
	return rt.persistLocked()
}

func (rt *runtimeTracker) RecordSync(at time.Time) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.LastSync = at.Unix()
	return rt.persistLocked()
}

func (rt *runtimeTracker) MarkCleanShutdown() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.state.CleanShutdown = true
	return rt.persistLocked()
}

func (rt *runtimeTracker) persistLocked() error {
	data, err := json.MarshalIndent(rt.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(rt.path), 0o755); err != nil {
		return fmt.Errorf("create runtime state directory: %w", err)
	}
	if err := os.WriteFile(rt.path, data, 0o644); err != nil {
		return fmt.Errorf("write runtime state: %w", err)
	}
	return nil
}

func loadRuntimeState(statePath string) (daemonRuntimeState, error) {
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return daemonRuntimeState{CleanShutdown: true}, nil
		}
		return daemonRuntimeState{}, fmt.Errorf("read runtime state: %w", err)
	}

	var state daemonRuntimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return daemonRuntimeState{}, fmt.Errorf("unmarshal runtime state: %w", err)
	}

	return state, nil
}
