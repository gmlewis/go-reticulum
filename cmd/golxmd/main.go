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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
)

func init() {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
usage: golxmd [-h] [--config CONFIG] [--rnsconfig RNSCONFIG] [-p] [-i PATH] [-v] [-q] [-s]
              [--status] [--peers] [--sync SYNC] [-b UNPEER] [--timeout TIMEOUT] [-r REMOTE]
              [--identity IDENTITY] [--exampleconfig] [--version]

Go Lightweight Extensible Messaging Daemon

options:
  -h, --help            show this help message and exit
  --config CONFIG       path to alternative golxmd config directory
  --rnsconfig RNSCONFIG
                        path to alternative Reticulum config directory
  -p, --propagation-node
                        run an LXMF Propagation Node
  -i, --on-inbound PATH
                        executable to run when a message is received
  -v, --verbose
  -q, --quiet
  -s, --service         golxmd is running as a service and should log to file
  --status              display node status
  --peers               display peered nodes
  --sync SYNC           request a sync with the specified peer
  -b, --break UNPEER    break peering with the specified peer
  --timeout TIMEOUT     timeout in seconds for query operations
  -r, --remote REMOTE   remote propagation node destination hash
  --identity IDENTITY   path to identity used for remote requests
  --exampleconfig       print verbose configuration example to stdout and exit
  --version             show program's version number and exit
`)
	}
	flag.StringVar(&configDir, "config", "", "path to alternative golxmd config directory")
	flag.StringVar(&rnsConfigDir, "rnsconfig", "", "path to alternative Reticulum config directory")
	flag.BoolVar(&runAsPropagationNode, "p", false, "run an LXMF Propagation Node")
	flag.BoolVar(&runAsPropagationNode, "propagation-node", false, "run an LXMF Propagation Node")
	flag.StringVar(&cmdOnInbound, "i", "", "executable to run when a message is received")
	flag.StringVar(&cmdOnInbound, "on-inbound", "", "executable to run when a message is received")
	flag.BoolVar(&runAsService, "s", false, "golxmd is running as a service and should log to file")
	flag.BoolVar(&runAsService, "service", false, "golxmd is running as a service and should log to file")
	flag.BoolVar(&displayStatus, "status", false, "display node status")
	flag.BoolVar(&displayPeers, "peers", false, "display peered nodes")
	flag.StringVar(&syncHash, "sync", "", "request a sync with the specified peer")
	flag.StringVar(&unpeerHash, "b", "", "break peering with the specified peer")
	flag.StringVar(&unpeerHash, "break", "", "break peering with the specified peer")
	flag.Var((*timeoutFlag)(&timeout), "timeout", "timeout in seconds for query operations")
	flag.StringVar(&remoteHash, "r", "", "remote propagation node destination hash")
	flag.StringVar(&remoteHash, "remote", "", "remote propagation node destination hash")
	flag.StringVar(&identityPath, "identity", "", "path to identity used for remote requests (default: ~/.reticulum/identities/lxmd)")
	flag.BoolVar(&exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	flag.BoolVar(&version, "version", false, "show program's version number and exit")

	flag.Var(&verbosity, "v", "enable verbose logging (stackable)")
	flag.Var(&quietness, "q", "reduce log verbosity (stackable)")
}

type timeoutFlag time.Duration

func (t *timeoutFlag) String() string {
	return fmt.Sprint(float64(time.Duration(*t).Seconds()))
}

func (t *timeoutFlag) Set(s string) error {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*t = timeoutFlag(time.Duration(f * float64(time.Second)))
	return nil
}

type countFlag int

func (c *countFlag) String() string {
	return fmt.Sprint(int(*c))
}

func (c *countFlag) Set(s string) error {
	if s == "true" {
		*c++
	} else if s == "false" {
		// do nothing
	} else {
		// handle as count if multiple flags are passed?
		// Actually flag package calls Set once per occurrence for boolean flags with "true"
		*c++
	}
	return nil
}

func (c *countFlag) IsBoolFlag() bool {
	return true
}

var (
	configDir            string
	rnsConfigDir         string
	runAsPropagationNode bool
	cmdOnInbound         string
	verbosity            countFlag
	quietness            countFlag
	runAsService         bool
	displayStatus        bool
	displayPeers         bool
	syncHash             string
	unpeerHash           string
	timeout              time.Duration
	remoteHash           string
	identityPath         string
	exampleConfig        bool
	version              bool
)

var (
	ac     *activeConfig
	lxmdir string

	lastPeerAnnounce time.Time
	lastNodeAnnounce time.Time

	tr *runtimeTracker
)

const (
	jobsInterval        = 5 * time.Second
	maintenanceInterval = 10 // Maintenance every 10 ticks (50s)
)

var (
	now       = time.Now
	tickCount = 0
)

func applyTimeoutDefaults() {
	if timeout == 0 {
		if displayStatus || displayPeers {
			timeout = 5 * time.Second
		} else if syncHash != "" || unpeerHash != "" {
			timeout = 10 * time.Second
		}
	}
}

func jobs(router *lxmf.Router, lxmfDestination *rns.Destination, stop <-chan struct{}, interval time.Duration) {
	for {
		tick(router, lxmfDestination)
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
	}
}

func tick(router *lxmf.Router, lxmfDestination *rns.Destination) {
	if ac == nil {
		return
	}

	tickCount++

	if ac.PeerAnnounceInterval != nil {
		if now().Sub(lastPeerAnnounce) > time.Duration(*ac.PeerAnnounceInterval)*time.Second {
			rns.Logf("Sending announce for LXMF delivery destination", rns.LogVerbose, false)
			if lxmfDestination != nil {
				_ = router.Announce(lxmfDestination.Hash)
			}
			lastPeerAnnounce = now()
			if tr != nil {
				_ = tr.RecordAnnounce(lastPeerAnnounce)
			}
		}
	}

	if ac.NodeAnnounceInterval != nil {
		if now().Sub(lastNodeAnnounce) > time.Duration(*ac.NodeAnnounceInterval)*time.Second {
			rns.Logf("Sending announce for LXMF Propagation Node", rns.LogVerbose, false)
			router.AnnouncePropagationNode()
			lastNodeAnnounce = now()
			if tr != nil {
				_ = tr.RecordSync(lastNodeAnnounce)
			}
		}
	}

	// Go-specific enhancement: ensure outbound messages are processed periodically.
	router.ProcessOutbound()

	if tickCount%maintenanceInterval == 0 {
		// Go-specific enhancement: prune stale peers periodically.
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

func lxmfDelivery(lxm *lxmf.Message) {
	writtenPath, err := lxm.WriteToDirectory(lxmdir)
	if err != nil {
		rns.Logf("Error occurred while processing received message %v. The contained exception was: %v", rns.LogError, false, lxm, err)
		return
	}
	rns.Logf("Received %v written to %v", rns.LogDebug, false, lxm, writtenPath)

	if ac != nil && ac.OnInbound != "" {
		rns.Logf("Calling external program to handle message", rns.LogDebug, false)
		// Python: processing_command = command+" \""+written_path+"\""
		// return_code = subprocess.call(shlex.split(processing_command), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
		parts := strings.Fields(ac.OnInbound)
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

func runDeferredJobs(delay time.Duration, router *lxmf.Router, lxmfDestination *rns.Destination) {
	time.Sleep(delay)
	rns.Logf("Running deferred start jobs", rns.LogDebug, false)

	if ac != nil && ac.PeerAnnounceAtStart && router != nil && lxmfDestination != nil {
		rns.Logf("Sending announce for LXMF delivery destination", rns.LogExtreme, false)
		_ = router.Announce(lxmfDestination.Hash)
	}

	if ac != nil && ac.NodeAnnounceAtStart && router != nil {
		rns.Logf("Sending announce for LXMF Propagation Node", rns.LogExtreme, false)
		router.AnnouncePropagationNode()
	}
}

func setupAuth(router *lxmf.Router) {
	if ac.AuthRequired {
		router.SetAuthRequired(true)
		if len(ac.AllowedIdentities) > 0 {
			for _, h := range ac.AllowedIdentities {
				router.Allow(h)
			}
		} else {
			allowedPath := filepath.Join(configDir, "allowed")
			rns.Logf("Client authentication was enabled, but no identity hashes could be loaded from %v. Nobody will be able to sync messages from this propagation node.", rns.LogWarning, false, allowedPath)
		}
	}
}

func main() {
	log.SetFlags(0)
	flag.Parse()
	applyTimeoutDefaults()

	// if peerMaxAge < 0 {
	// 	log.Fatalf("peer-max-age must be >= 0")
	// }
	// if maintenanceInterval < 0 {
	// 	log.Fatalf("maintenance-interval must be >= 0")
	// }
	// if outboundInterval < 0 {
	// 	log.Fatalf("outbound-interval must be >= 0")
	// }
	// if announceInterval < 0 {
	// 	log.Fatalf("announce-interval must be >= 0")
	// }
	// if syncInterval < 0 {
	// 	log.Fatalf("sync-interval must be >= 0")
	// }

	if version {
		fmt.Printf("golxmd %v\n", rns.VERSION)
		return
	}

	if exampleConfig {
		fmt.Print(defaultLXMDaemonConfig)
		return
	}

	if displayStatus || displayPeers {
		getStatus(remoteHash, configDir, rnsConfigDir, int(verbosity), int(quietness), timeout, displayStatus, displayPeers, identityPath)
		return
	}

	if syncHash != "" {
		requestSync(syncHash, remoteHash, configDir, rnsConfigDir, int(verbosity), int(quietness), timeout, identityPath)
		return
	}

	if unpeerHash != "" {
		requestUnpeer(unpeerHash, remoteHash, configDir, rnsConfigDir, int(verbosity), int(quietness), timeout, identityPath)
		return
	}

	configDir = resolveConfigDir(configDir)

	if err := ensureConfig(configDir); err != nil {
		log.Fatalf("ensure config: %v", err)
	}

	var err error
	ac, err = loadConfig(configDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// T09: Log level calculation
	rns.SetLogLevel(resolveLogLevel(ac.LogLevel, int(verbosity), int(quietness)))
	setupLogging(runAsService, configDir)
	rns.Logf("Configuration loaded from %v", rns.LogVerbose, false, configDir)

	rns.Logf("Substantiating Reticulum...", rns.LogInfo, false)
	if _, err := rns.NewReticulum(rnsConfigDir); err != nil {
		log.Fatalf("initialize Reticulum: %v", err)
	}

	var storagePath, identityPath string
	resolvedStorage, resolvedIdentityPath, err := resolvePaths(storagePath, identityPath, configDir)
	if err != nil {
		log.Fatalf("resolve paths: %v", err)
	}

	identity, err := loadOrCreateIdentity(resolvedIdentityPath)
	if err != nil {
		log.Fatalf("load identity: %v", err)
	}

	if cmdOnInbound != "" {
		// Note: Python's lxmd.py accepts the --on-inbound CLI arg but fails to
		// actually apply it to the configuration (it's a Python bug). Go
		// correctly applies it here.
		ac.OnInbound = cmdOnInbound
	}

	// T11: LXMRouter config-driven construction
	router, err := lxmf.NewRouterFromConfig(lxmf.RouterConfig{
		Identity:                   identity,
		StoragePath:                resolvedStorage,
		Autopeer:                   ac.Autopeer,
		AutopeerMaxdepth:           ac.AutopeerMaxdepth,
		PropagationLimit:           ac.PropagationTransferMaxAcceptedSize,
		SyncLimit:                  ac.PropagationSyncMaxAcceptedSize,
		DeliveryLimit:              ac.DeliveryTransferMaxAcceptedSize,
		MaxPeers:                   ac.MaxPeers,
		StaticPeers:                ac.StaticPeers,
		FromStaticOnly:             ac.FromStaticOnly,
		PropagationCost:            ac.PropagationStampCostTarget,
		PropagationCostFlexibility: ac.PropagationStampCostFlexibility,
		PeeringCost:                ac.PeeringCost,
		MaxPeeringCost:             ac.RemotePeeringCostMax,
		Name:                       ac.NodeName,
	})
	if err != nil {
		log.Fatalf("create LXMF router: %v", err)
	}

	// T12: register_delivery_callback
	router.RegisterDeliveryCallback(lxmfDelivery)

	// T13: ignore_destination
	for _, h := range ac.IgnoredLXMFDestinations {
		router.IgnoreDestination(h)
	}

	// T14: register_delivery_identity with display_name
	lxmfDestination, err := router.RegisterDeliveryIdentity(identity, ac.DisplayName, nil)
	if err != nil {
		log.Fatalf("register delivery destination: %v", err)
	}
	rns.Logf("LXMF Router ready to receive on %v", rns.LogInfo, false, rns.PrettyHex(lxmfDestination.Hash))

	// T15: RNS.Identity.remember
	rns.Remember(nil, lxmfDestination.Hash, identity.GetPublicKey(), nil)

	// T16: Auth setup
	setupAuth(router)

	// T17: Propagation node enable
	if runAsPropagationNode || ac.EnablePropagationNode {
		router.SetMessageStorageLimit(ac.MessageStorageLimit)
		for _, s := range ac.PrioritisedLXMFDestinations {
			if h, err := rns.HexToBytes(s); err == nil {
				router.Prioritise(h)
			}
		}
		for _, s := range ac.ControlAllowedIdentities {
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

		// T09: Always register control destination for propagation nodes
		allowed := make([][]byte, 0, len(ac.ControlAllowedIdentities))
		for _, s := range ac.ControlAllowedIdentities {
			if h, err := rns.HexToBytes(s); err == nil {
				allowed = append(allowed, h)
			}
		}
		if _, err := router.RegisterPropagationControlDestination(allowed); err != nil {
			log.Fatalf("register control destination: %v", err)
		}
	}

	// The runtimeTracker is retained as a useful debugging feature for the Go port of golxmd,
	// providing persistence of operational state across restarts that is not present in the
	// original implementation.
	runtimeStatePath := filepath.Join(resolvedStorage, "lxmf", "golxmd-state.json")
	tr, err = newRuntimeTracker(runtimeStatePath)
	if err != nil {
		log.Fatalf("initialize runtime tracker: %v", err)
	}
	if tr.WasUncleanShutdown() {
		log.Printf("golxmd detected unclean previous shutdown; entering recovery-aware startup")
	}

	log.Printf("golxmd running with identity %x", identity.Hash)
	rns.Logf("Started golxmd version %v", rns.LogNotice, false, rns.VERSION)

	// T21: Initialize announce timers to current time + 10s (deferred delay)
	// Python doesn't explicitly initialize them, so they start as None and are set after first fire.
	// We'll set them to current time to avoid immediate fire BEFORE deferred start fires.
	lastPeerAnnounce = now().Add(10 * time.Second)
	lastNodeAnnounce = now().Add(10 * time.Second)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start jobs
	stopJobs := make(chan struct{})
	go jobs(router, lxmfDestination, stopJobs, jobsInterval)
	go runDeferredJobs(10*time.Second, router, lxmfDestination)

	<-stop
	fmt.Println() // T36: KeyboardInterrupt prints a blank line
	close(stopJobs)
	if err := tr.MarkCleanShutdown(); err != nil {
		log.Printf("golxmd failed to persist clean shutdown marker: %v", err)
	}

	log.Printf("golxmd shutting down")
}

func resolvePaths(storagePath, identityPath, configDir string) (string, string, error) {
	if storagePath == "" {
		storagePath = filepath.Join(configDir, "storage")
	}
	lxmdir = filepath.Join(storagePath, "messages")

	if err := os.MkdirAll(lxmdir, 0o755); err != nil {
		return "", "", fmt.Errorf("create messages path %q: %w", lxmdir, err)
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

func parseAllowedIdentities(value string) ([][]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	out := make([][]byte, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		hash, err := rns.HexToBytes(trimmed)
		if err != nil {
			return nil, fmt.Errorf("decode hash %q: %w", trimmed, err)
		}
		if len(hash) != rns.TruncatedHashLength/8 {
			return nil, fmt.Errorf("invalid hash length %v for %q", len(hash), trimmed)
		}
		out = append(out, hash)
	}

	return out, nil
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
