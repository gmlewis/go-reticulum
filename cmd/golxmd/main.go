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

func init() {
	flag.Usage = func() {
		_, _ = fmt.Fprintf(flag.CommandLine.Output(), `
usage: golxmd [-h] [--config CONFIG] [--rnsconfig RNSCONFIG] [-p] [-i PATH] [-v] [-q] [-s] [--status] [--peers]
              [--sync SYNC] [-b UNPEER] [--timeout TIMEOUT] [-r REMOTE] [--identity IDENTITY] [--exampleconfig]
              [--version]

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
	flag.BoolVar(&verbose, "v", false, "enable verbose logging")
	flag.BoolVar(&quiet, "q", false, "reduce log verbosity")
	flag.BoolVar(&runAsService, "s", false, "golxmd is running as a service and should log to file")
	flag.BoolVar(&runAsService, "service", false, "golxmd is running as a service and should log to file")
	flag.BoolVar(&displayStatus, "status", false, "display node status")
	flag.BoolVar(&displayPeers, "peers", false, "display peered nodes")
	flag.StringVar(&syncHash, "sync", "", "request a sync with the specified peer")
	flag.StringVar(&unpeerHash, "b", "", "break peering with the specified peer")
	flag.StringVar(&unpeerHash, "break", "", "break peering with the specified peer")
	flag.DurationVar(&timeout, "timeout", 0, "timeout in seconds for query operations")
	flag.StringVar(&remoteHash, "r", "", "remote propagation node destination hash")
	flag.StringVar(&remoteHash, "remote", "", "remote propagation node destination hash")
	flag.StringVar(&identityPath, "identity", "", "path to identity used for remote requests (default: ~/.reticulum/identities/lxmd)")
	flag.BoolVar(&exampleConfig, "exampleconfig", false, "print verbose configuration example to stdout and exit")
	flag.BoolVar(&version, "version", false, "show program's version number and exit")
}

var (
	// storagePath         string
	// stampCost           int
	// registerPropagation bool
	// registerControl     bool
	// controlAllowed      string
	// peerMaxAge          time.Duration
	// maintenanceInterval time.Duration
	// outboundInterval    time.Duration
	// announceInterval    time.Duration
	// syncInterval        time.Duration

	configDir            string
	rnsConfigDir         string
	runAsPropagationNode bool
	cmdOnInbound         string
	verbose              bool
	quiet                bool
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

func main() {
	log.SetFlags(0)
	flag.Parse()

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
		fmt.Print(exampleLXMDaemonConfig)
		return
	}

	if verbose {
		rns.SetLogLevel(rns.LogVerbose)
	}
	if quiet {
		rns.SetLogLevel(rns.LogWarning)
	}

	if _, err := rns.NewReticulum(configDir); err != nil {
		log.Fatalf("initialize Reticulum: %v", err)
	}

	resolvedStorage, resolvedIdentityPath, err := resolvePaths(storagePath, identityPath)
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

	if _, err := router.RegisterDeliveryIdentity(identity, stampCost); err != nil {
		log.Fatalf("register delivery destination: %v", err)
	}

	if registerPropagation {
		if _, err := router.RegisterPropagationDestination(); err != nil {
			log.Fatalf("register propagation destination: %v", err)
		}
	}

	if registerControl {
		allowed, err := parseAllowedIdentities(controlAllowed)
		if err != nil {
			log.Fatalf("parse control-allowed hashes: %v", err)
		}
		if _, err := router.RegisterPropagationControlDestination(allowed); err != nil {
			log.Fatalf("register control destination: %v", err)
		}
	}

	if peerMaxAge > 0 {
		if err := router.SetPeerMaxAge(peerMaxAge); err != nil {
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
	if maintenanceInterval > 0 || outboundInterval > 0 || announceInterval > 0 || syncInterval > 0 {
		go runOperationalLoop(
			router,
			maintenanceInterval,
			outboundInterval,
			announceInterval,
			syncInterval,
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

const exampleLXMDaemonConfig = `
# This is an example LXM Daemon config file.
# You should probably edit it to suit your
# intended usage.

[propagation]

# Whether to enable propagation node

enable_node = no

# You can specify identity hashes for remotes
# that are allowed to control and query status
# for this propagation node.

# control_allowed = 7d7e542829b40f32364499b27438dba8, 437229f8e29598b2282b88bad5e44698

# An optional name for this node, included
# in announces.

# node_name = Anonymous Propagation Node

# Automatic announce interval in minutes.
# 6 hours by default.

announce_interval = 360

# Whether to announce when the node starts.

announce_at_start = yes

# Wheter to automatically peer with other
# propagation nodes on the network.

autopeer = yes

# The maximum peering depth (in hops) for
# automatically peered nodes.

autopeer_maxdepth = 6

# The maximum amount of storage to use for
# the LXMF Propagation Node message store,
# specified in megabytes. When this limit
# is reached, LXMF will periodically remove
# messages in its message store. By default,
# LXMF prioritises keeping messages that are
# new and small. Large and old messages will
# be removed first. This setting is optional
# and defaults to 500 megabytes.

# message_storage_limit = 500

# The maximum accepted transfer size per in-
# coming propagation message, in kilobytes.
# This sets the upper limit for the size of
# single messages accepted onto this node.

# propagation_message_max_accepted_size = 256

# The maximum accepted transfer size per in-
# coming propagation node sync.
#
# If a node wants to propagate a larger number
# of messages to this node, than what can fit
# within this limit, it will prioritise sending
# the smallest messages first, and try again
# with any remaining messages at a later point.

# propagation_sync_max_accepted_size = 10240

# You can configure the target stamp cost
# required to deliver messages via this node.

# propagation_stamp_cost_target = 16

# If set higher than 0, the stamp cost flexi-
# bility option will make this node accept
# messages with a lower stamp cost than the
# target from other propagation nodes (but
# not from peers directly). This allows the
# network to gradually adjust stamp cost.

# propagation_stamp_cost_flexibility = 3

# The peering_cost option configures the target
# value required for a remote node to peer with
# and deliver messages to this node.

# peering_cost = 18

# You can configure the maximum peering cost
# of remote nodes that this node will peer with.
# Setting this to a higher number will allow
# this node to peer with other nodes requiring
# a higher peering key value, but will require
# more computation time during initial peering
# when generating the peering key.

# remote_peering_cost_max = 26

# You can tell the LXMF message router to
# prioritise storage for one or more
# destinations. If the message store reaches
# the specified limit, LXMF will prioritise
# keeping messages for destinations specified
# with this option. This setting is optional,
# and generally you do not need to use it.

# prioritise_destinations = 41d20c727598a3fbbdf9106133a3a0ed, d924b81822ca24e68e2effea99bcb8cf

# You can configure the maximum number of other
# propagation nodes that this node will peer
# with automatically. The default is 20.

# max_peers = 20

# You can configure a list of static propagation
# node peers, that this node will always be
# peered with, by specifying a list of
# destination hashes.

# static_peers = e17f833c4ddf8890dd3a79a6fea8161d, 5a2d0029b6e5ec87020abaea0d746da4

# You can configure the propagation node to
# only accept incoming propagation messages
# from configured static peers.

# from_static_only = True

# By default, any destination is allowed to
# connect and download messages, but you can
# optionally restrict this. If you enable
# authentication, you must provide a list of
# allowed identity hashes in the a file named
# "allowed" in the lxmd config directory.

auth_required = no


[lxmf]

# The LXM Daemon will create an LXMF destination
# that it can receive messages on. This option sets
# the announced display name for this destination.

display_name = Anonymous Peer

# It is possible to announce the internal LXMF
# destination when the LXM Daemon starts up.

announce_at_start = no

# You can also announce the delivery destination
# at a specified interval. This is not enabled by
# default.

# announce_interval = 360

# The maximum accepted unpacked size for mes-
# sages received directly from other peers,
# specified in kilobytes. Messages larger than
# this will be rejected before the transfer
# begins.

delivery_transfer_max_accepted_size = 1000

# You can configure an external program to be run
# every time a message is received. The program
# will receive as an argument the full path to the
# message saved as a file. The example below will
# simply result in the message getting deleted as
# soon as it has been received.

# on_inbound = rm


[logging]
# Valid log levels are 0 through 7:
#   0: Log only critical information
#   1: Log errors and lower log levels
#   2: Log warnings and lower log levels
#   3: Log notices and lower log levels
#   4: Log info and lower (this is the default)
#   5: Verbose logging
#   6: Debug logging
#   7: Extreme logging

loglevel = 4
`
