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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
)

const appVersion = "0.1.0"

func main() {
	log.SetFlags(0)

	configDir := flag.String("config", "", "path to alternate Reticulum config directory")
	storagePath := flag.String("storage", "", "path to LXMF storage root (default: ~/.reticulum)")
	identityPath := flag.String("identity", "", "path to daemon identity file (default: ~/.reticulum/identities/lxmd)")
	stampCost := flag.Int("stamp-cost", 0, "delivery destination stamp cost")
	registerPropagation := flag.Bool("propagation-node", true, "register propagation destination")
	registerControl := flag.Bool("control-node", true, "register propagation control destination")
	controlAllowed := flag.String("control-allowed", "", "comma-separated identity hashes allowed on control API")
	peerMaxAge := flag.Duration("peer-max-age", 0, "max peer age before pruning (0 disables stale peer pruning)")
	maintenanceInterval := flag.Duration("maintenance-interval", 30*time.Second, "maintenance loop interval")
	outboundInterval := flag.Duration("outbound-interval", 5*time.Second, "outbound queue processing interval (0 disables)")
	announceInterval := flag.Duration("announce-interval", 0, "announce cadence interval (0 disables)")
	syncInterval := flag.Duration("sync-interval", 0, "propagation sync cadence interval (0 disables)")
	version := flag.Bool("version", false, "print program version and exit")
	verbose := flag.Bool("v", false, "enable verbose logging")
	quiet := flag.Bool("q", false, "reduce log verbosity")
	flag.Parse()

	if *peerMaxAge < 0 {
		log.Fatalf("peer-max-age must be >= 0")
	}
	if *maintenanceInterval < 0 {
		log.Fatalf("maintenance-interval must be >= 0")
	}
	if *outboundInterval < 0 {
		log.Fatalf("outbound-interval must be >= 0")
	}
	if *announceInterval < 0 {
		log.Fatalf("announce-interval must be >= 0")
	}
	if *syncInterval < 0 {
		log.Fatalf("sync-interval must be >= 0")
	}

	if *version {
		fmt.Printf("golxmd %v\n", appVersion)
		return
	}

	if *verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if *quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

	if _, err := rns.NewReticulum(*configDir); err != nil {
		log.Fatalf("initialize Reticulum: %v", err)
	}

	resolvedStorage, resolvedIdentityPath, err := resolvePaths(*storagePath, *identityPath)
	if err != nil {
		log.Fatalf("resolve paths: %v", err)
	}

	identity, err := loadOrCreateIdentity(resolvedIdentityPath)
	if err != nil {
		log.Fatalf("load identity: %v", err)
	}

	router, err := lxmf.NewRouter(identity, resolvedStorage)
	if err != nil {
		log.Fatalf("create LXMF router: %v", err)
	}

	if _, err := router.RegisterDeliveryIdentity(identity, *stampCost); err != nil {
		log.Fatalf("register delivery destination: %v", err)
	}

	if *registerPropagation {
		if _, err := router.RegisterPropagationDestination(); err != nil {
			log.Fatalf("register propagation destination: %v", err)
		}
	}

	if *registerControl {
		allowed, err := parseAllowedIdentities(*controlAllowed)
		if err != nil {
			log.Fatalf("parse control-allowed hashes: %v", err)
		}
		if _, err := router.RegisterPropagationControlDestination(allowed); err != nil {
			log.Fatalf("register control destination: %v", err)
		}
	}

	if *peerMaxAge > 0 {
		if err := router.SetPeerMaxAge(*peerMaxAge); err != nil {
			log.Fatalf("set peer max age: %v", err)
		}
	}

	runtimeStatePath := filepath.Join(resolvedStorage, "lxmf", "golxmd-state.json")
	tracker, err := newRuntimeTracker(runtimeStatePath)
	if err != nil {
		log.Fatalf("initialize runtime tracker: %v", err)
	}
	if tracker.WasUncleanShutdown() {
		log.Printf("golxmd detected unclean previous shutdown; entering recovery-aware startup")
	}

	log.Printf("golxmd running with identity %x", identity.Hash)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	done := make(chan struct{})
	if *maintenanceInterval > 0 || *outboundInterval > 0 || *announceInterval > 0 || *syncInterval > 0 {
		go runOperationalLoop(
			router,
			*maintenanceInterval,
			*outboundInterval,
			*announceInterval,
			*syncInterval,
			done,
			tracker,
		)
	}
	<-stop
	close(done)
	if err := tracker.MarkCleanShutdown(); err != nil {
		log.Printf("golxmd failed to persist clean shutdown marker: %v", err)
	}

	log.Printf("golxmd shutting down")
}

func runOperationalLoop(router *lxmf.Router, maintenanceInterval, outboundInterval, announceInterval, syncInterval time.Duration, done <-chan struct{}, tracker *runtimeTracker) {
	runOperationalLoopWithHandlers(
		maintenanceInterval,
		outboundInterval,
		announceInterval,
		syncInterval,
		done,
		func() {
			pruned := router.PruneStalePeers()
			if pruned > 0 {
				log.Printf("golxmd pruned %v stale peers", pruned)
			}
		},
		func() {
			router.ProcessOutbound()
		},
		func() {
			if tracker != nil {
				if err := tracker.RecordAnnounce(time.Now()); err != nil {
					log.Printf("golxmd failed to persist announce marker: %v", err)
				}
			}
			log.Printf("golxmd announce cadence tick")
		},
		func() {
			if tracker != nil {
				if err := tracker.RecordSync(time.Now()); err != nil {
					log.Printf("golxmd failed to persist sync marker: %v", err)
				}
			}
			log.Printf("golxmd sync cadence tick")
		},
	)
}

func runOperationalLoopWithHandlers(maintenanceInterval, outboundInterval, announceInterval, syncInterval time.Duration, done <-chan struct{}, onMaintenance func(), onOutbound func(), onAnnounce func(), onSync func()) {
	var maintenanceTicker *time.Ticker
	if maintenanceInterval > 0 {
		maintenanceTicker = time.NewTicker(maintenanceInterval)
		defer maintenanceTicker.Stop()
	}

	var outboundTicker *time.Ticker
	if outboundInterval > 0 {
		outboundTicker = time.NewTicker(outboundInterval)
		defer outboundTicker.Stop()
	}

	var announceTicker *time.Ticker
	if announceInterval > 0 {
		announceTicker = time.NewTicker(announceInterval)
		defer announceTicker.Stop()
	}

	var syncTicker *time.Ticker
	if syncInterval > 0 {
		syncTicker = time.NewTicker(syncInterval)
		defer syncTicker.Stop()
	}

	for {
		select {
		case <-done:
			return
		case <-tickChan(maintenanceTicker):
			if onMaintenance != nil {
				onMaintenance()
			}
		case <-tickChan(outboundTicker):
			if onOutbound != nil {
				onOutbound()
			}
		case <-tickChan(announceTicker):
			if onAnnounce != nil {
				onAnnounce()
			}
		case <-tickChan(syncTicker):
			if onSync != nil {
				onSync()
			}
		}
	}
}

func tickChan(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func resolvePaths(storagePath string, identityPath string) (string, string, error) {
	if storagePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", fmt.Errorf("determine home directory: %w", err)
		}
		storagePath = filepath.Join(home, ".reticulum")
	}

	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return "", "", fmt.Errorf("create storage path %q: %w", storagePath, err)
	}

	if identityPath == "" {
		identityPath = filepath.Join(storagePath, "identities", "lxmd")
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
		return identity, nil
	}

	identity, err := rns.NewIdentity(true)
	if err != nil {
		return nil, fmt.Errorf("create identity: %w", err)
	}
	if err := identity.ToFile(identityPath); err != nil {
		return nil, fmt.Errorf("persist identity to %q: %w", identityPath, err)
	}

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
