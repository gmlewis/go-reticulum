// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Transport is the interface implemented by Reticulum transport systems.
type Transport interface {
	// ActivateLink moves a pending link into the active link set.
	ActivateLink(l *Link)
	// AnnounceHandlers returns the registered announce handlers.
	AnnounceHandlers() []*AnnounceHandler
	// BlackholeIdentity blocks traffic from the given identity until it is
	// explicitly cleared or expires.
	BlackholeIdentity(identityHash []byte, until *int64, reason string) bool
	// CacheRequest asks the transport to re-request a packet by hash from the
	// network cache (Python RNS.Transport.cache_request). The Go port has no
	// network packet cache, so the default implementation is a best-effort
	// no-op; the call site (resource AWAITING_PROOF watchdog) is still issued
	// faithfully.
	CacheRequest(packetHash []byte, link *Link)
	// EnableBlackholeUpdater starts any configured remote blackhole update flow.
	EnableBlackholeUpdater()
	// EnableBlackholeUpdaterCallCount reports how many times the updater has been enabled.
	EnableBlackholeUpdaterCallCount() int
	// DiscoverInterfaces refreshes dynamic transport interface discovery.
	DiscoverInterfaces()
	// DiscoverInterfacesCallCount reports how many discovery passes have run.
	DiscoverInterfacesCallCount() int
	// DropAnnounceQueues clears queued announce rebroadcast state.
	DropAnnounceQueues() int
	// Enabled reports whether the transport currently accepts traffic.
	Enabled() bool
	// FindLink returns the active link matching linkID, if any.
	FindLink(linkID []byte) *Link
	// GetBlackholedIdentities returns the current blackhole list in RPC-friendly
	// form.
	GetBlackholedIdentities() []map[string]any
	// GetInterfaces returns the registered transport interfaces.
	GetInterfaces() []interfaces.Interface
	// GetPacketQ returns the cached link-quality estimate for packetHash.
	GetPacketQ(packetHash []byte) (float64, bool)
	// GetPacketRSSI returns the cached Received Signal Strength Indicator (RSSI)
	// for packetHash.
	GetPacketRSSI(packetHash []byte) (float64, bool)
	// GetPacketSNR returns the cached Signal-to-Noise Ratio (SNR) for
	// packetHash.
	GetPacketSNR(packetHash []byte) (float64, bool)
	// GetPathEntry returns routing information for destHash, if known.
	GetPathEntry(destHash []byte) *PathInfo
	// GetPathTable returns the current known path table.
	GetPathTable() []PathInfo
	// GetRateTable returns current announce-rate tracking state.
	GetRateTable() []map[string]any
	// HasPath reports whether a path to destHash is known.
	HasPath(destHash []byte) bool
	// HopsTo returns the hop count to destHash, or a sentinel when unknown.
	HopsTo(destHash []byte) int
	// Identity returns the transport's local identity.
	Identity() *Identity
	// Inbound processes a raw inbound frame received on iface.
	Inbound(raw []byte, iface interfaces.Interface)
	// InvalidatePath removes any known path to destHash.
	InvalidatePath(destHash []byte) bool
	// InvalidatePathsViaNextHop removes all paths that depend on nextHop.
	InvalidatePathsViaNextHop(nextHop []byte) int
	// IsBlackholed reports whether the given identity hash is currently on the
	// local blackhole list (RNS.Reticulum.is_blackholed). The link identify
	// path uses it to terminate incoming links from blocked identities
	// (RNS/Link.py:974-976, v1.3.2).
	IsBlackholed(identityHash []byte) bool
	// LinkMTUDiscovery reports whether link MTU discovery is enabled.
	LinkMTUDiscovery() bool
	// UseImplicitProof reports whether identity proofs omit the packet hash and
	// send only the signature.
	UseImplicitProof() bool
	// LinkTable returns the active transport link table.
	LinkTable() map[string]*LinkEntry
	// NetworkIdentityHash returns the hash of the transport's network identity.
	NetworkIdentityHash() []byte
	// Outbound processes an outbound packet before transmission.
	Outbound(packet *Packet) error
	// RegisterAnnounceHandler registers an announce handler.
	RegisterAnnounceHandler(handler *AnnounceHandler)
	// RegisterDestination registers a destination with the transport.
	RegisterDestination(d *Destination)
	// RegisterInterface registers a transport interface.
	RegisterInterface(iface interfaces.Interface)
	// RegisterLink registers a link with the transport.
	RegisterLink(l *Link)
	// RequestPath asks the network to discover a path to destHash.
	RequestPath(destHash []byte) error
	// SetEnabled enables or disables transport processing.
	SetEnabled(enabled bool)
	// SetLinkMTUDiscovery enables or disables link MTU discovery.
	SetLinkMTUDiscovery(enabled bool)
	// SetUseImplicitProof enables or disables implicit identity proofs.
	SetUseImplicitProof(enabled bool)
	// SetNetworkIdentity sets the network identity used by the transport.
	SetNetworkIdentity(identity *Identity)
	// Start starts the transport using the provided storage path.
	Start(storagePath string) error
	// StartedAt returns when the transport last started.
	StartedAt() time.Time
	// Stop stops the transport and its background processing.
	Stop()
	// TrafficRxb returns the cumulative received-byte delta total driven by the
	// traffic counter loop (Python Transport.traffic_rxb).
	TrafficRxb() uint64
	// TrafficTxb returns the cumulative transmitted-byte delta total
	// (Python Transport.traffic_txb).
	TrafficTxb() uint64
	// SpeedRx returns the current aggregate receive speed in bits/sec
	// (Python Transport.speed_rx).
	SpeedRx() float64
	// SpeedTx returns the current aggregate transmit speed in bits/sec
	// (Python Transport.speed_tx).
	SpeedTx() float64
	// UnblackholeIdentity removes a previously blackholed identity.
	UnblackholeIdentity(identityHash []byte) bool

	// Remember stores identity information associated with packetHash and
	// destHash for later recall.
	Remember(packetHash, destHash, publicKey, appData []byte)
	// Recall retrieves a previously remembered identity by hash.
	Recall(targetHash []byte) *Identity
	// RecallNoUse recalls a previously remembered identity by hash WITHOUT
	// marking the destination used (Python Identity.recall(..., _no_use=True),
	// RNS/Identity.py:116-160). Callers that do not represent actual
	// application use (message unpacking, path-table scans, announce
	// rebroadcasts) use this to avoid inflating a destination's last-used
	// time, which drives known-destination retention/cleanup.
	RecallNoUse(targetHash []byte) *Identity
	// RecallAppData returns the cached app_data for a known destination, or
	// nil if the destination is unknown (Python Identity.recall_app_data).
	RecallAppData(targetHash []byte) []byte
	// GetRatchet returns the ratchet public key recorded for destHash.
	GetRatchet(destHash []byte) []byte
	// SetRatchet stores a ratchet public key for destHash.
	SetRatchet(destHash, ratchetPub []byte)

	// LoadKnownDestinations loads persisted recalled destination data.
	LoadKnownDestinations(storagePath string)
	// SaveKnownDestinations persists recalled destination data.
	SaveKnownDestinations(storagePath string)

	// UsedDestinationData marks a known destination in-use (Python
	// RNS.Identity._used_destination_data).
	UsedDestinationData(destHash []byte) bool
	// RetainDestinationData pins a known destination so CleanKnownDestinations
	// never drops it (Python RNS.Identity._retain_destination_data).
	RetainDestinationData(destHash []byte) bool
	// UnretainDestinationData clears the retain flag on a known destination
	// (Python RNS.Identity._unretain_destination_data).
	UnretainDestinationData(destHash []byte) bool
	// RetainIdentity pins every known destination owned by identityHash
	// (Python RNS.Identity._retain_identity).
	RetainIdentity(identityHash []byte) bool

	// GetLogger returns the logger associated with the transport.
	GetLogger() *Logger
}

// TransportSystem manages routing, packet forwarding, and global state.
type TransportSystem struct {
	logger      *Logger
	identity    *Identity
	networkID   *Identity
	storagePath string
	running     bool
	// ready is set true at the END of Start (after the maintenance and
	// traffic-counter goroutines are launched), mirroring Python
	// Transport.ready (Transport.py:427). Inbound waits on it during the
	// startup window so packets that arrive before Start finishes are not
	// lost (Transport.py:1430-1437). Guarded by ts.mu.
	ready             bool
	readyWaitTimeout  time.Duration
	readyPollInterval time.Duration
	startedAt         time.Time
	stopCh            chan struct{}
	doneCh            chan struct{}

	pathRequestHash []byte

	interfaces   []interfaces.Interface
	destinations []*Destination
	// destinationsMap is the hash-keyed index over destinations (Python
	// Transport.destinations_map, Transport.py:104,1216-1218,2478-2496). It
	// lets inbound announce/link-request/data handling resolve a local
	// destination by hash in O(1) instead of scanning the destinations list.
	// Guarded by ts.mu alongside destinations.
	destinationsMap map[string]*Destination

	pendingLinks []*Link
	activeLinks  []*Link

	// Routing tables
	pathTable        map[string]*PathEntry
	reverseTable     map[string]*ReverseEntry
	linkTable        map[string]*LinkEntry
	packetHashes     map[string]time.Time
	packetHashesPrev map[string]time.Time
	tunnels          map[string]*Tunnel

	// allowLinkPathRebalance mirrors RNS/Transport.py:90
	// `ALLOW_LINK_PATH_REBALANCE = True`. When true, a pending link that
	// receives a link-proof (LRPROOF) whose hop count differs from the
	// link's expectedHops attempts a path re-balance: the proof signature
	// is re-validated and, if it verifies, the link adopts the proof's hop
	// count (Transport.py:2276-2311). It is per-instance so tests can
	// toggle it without racing on a process-wide global.
	allowLinkPathRebalance bool

	// knownDestDirty is set by Remember when a destination is added/updated.
	// The maintenance loop flushes it to disk at most every
	// pathTablePersistInterval (and Stop does a final flush), coalescing a
	// burst of announces into a single serialize+write instead of one per
	// announce. Under an announce storm the per-announce SaveKnownDestinations
	// previously repacked+rewrote the entire known_destinations file for every
	// announce — the dominant allocation churn source on a long-running node
	// (64% of 30s allocation once the table grew). Guarded by ts.mu.
	knownDestDirty bool

	// remoteStatusHandlerFn and remotePathHandlerFn are the registered
	// remote-management request handlers. They are populated by Reticulum
	// during NewReticulumWithLogger if remote management is enabled.
	remoteStatusHandlerFn func(data []any) any
	remotePathHandlerFn   func(data []any) any
	packetHashRotateAt    int
	announceTable         map[string]*AnnounceEntry
	announceRateTable     map[string]*AnnounceRateEntry
	announceQueues        map[interfaces.Interface]*announceQueueState
	pathRequests          map[string]time.Time
	pendingPathRequests   map[string][]interfaces.Interface
	pendingPathRequestAt  map[string]time.Time

	// downNotified is the once-per-down-transition latch for the outbound
	// fan-out paths (sendRebroadcast, dispatchForwardSend). When an
	// interface's Send fails while it was up, the first caller claims the
	// latch and performs the log + InvalidatePathsViaInterface; the dozens
	// of concurrent queued sends that fail on the same now-dead connection
	// suppress, so a half-open peer whose write deadline fires cannot
	// trigger a burst of full pathTable scans and error lines under ts.mu
	// (which starves inbound link-handshake processing). onDown (registered
	// for TCP interfaces in RegisterInterface) already invalidates once on
	// the down transition; this latch keeps the fan-out paths from redo it
	// per queued send. The latch is cleared by processAnnounceTable once the
	// interface is observed up again, and by RegisterInterface.
	downNotified map[interfaces.Interface]struct{}

	packetRSSICache map[string]float64
	packetSNRCache  map[string]float64
	packetQCache    map[string]float64

	blackholedIdentities map[string]BlackholeIdentityEntry
	enableBlackholeCalls int
	discoverCalls        int
	blackholeUpdaterOn   bool
	discoveryOn          bool
	discoverHook         func()

	// blackholeUpdater is the running BlackholeUpdater (Python
	// Transport.blackhole_updater), started by EnableBlackholeUpdater when
	// blackhole_sources are configured.
	blackholeUpdater *BlackholeUpdater

	// blackholePath is the on-disk directory holding per-source blackhole
	// lists (Python RNS.Reticulum.blackholepath). blackholeSources is the
	// configured set of remote source identity hashes whose lists are
	// accepted by reloadBlackholeAt (Python RNS.Reticulum.blackhole_sources).
	blackholePath    string
	blackholeSources [][]byte

	// blackholeUpdateInterval is the configured minimum interval between
	// fetches of any single blackhole source (Python
	// RNS.Reticulum.__blackhole_update_interval, Reticulum.py:269,601-604).
	// Defaults to BlackholeUpdateInterval (1h); set from the
	// [reticulum] blackhole_update_interval config key (float minutes, clamped
	// to ≥2) before EnableBlackholeUpdater runs.
	blackholeUpdateInterval time.Duration

	// publishBlackhole mirrors RNS.Reticulum.publish_blackhole_enabled: when
	// true, Start registers the rnstransport.info.blackhole request
	// destination serving the /list RPC (Python Transport.py:229-232).
	// blackholeDestination holds that destination once created.
	publishBlackhole     bool
	blackholeDestination *Destination

	// tunnelSynthesizeDestination is the inbound PLAIN destination
	// rnstransport.tunnel.synthesize that receives tunnel establishment
	// packets (Python Transport.py:215-216). Its packet callback is
	// tunnelSynthesizeHandler.
	tunnelSynthesizeDestination *Destination

	knownDestinations map[string][]any
	knownRatchets     map[string][]byte

	// saveMu serializes known-destinations persistence so only one
	// temp-file+rename sequence runs at a time, mirroring Python's
	// saving_known_destinations flag (RNS/Identity.py:186-205). Unlike ts.mu
	// (which only guards the in-memory map snapshot), saveMu spans the pack,
	// temp-file write, and atomic rename so concurrent SaveKnownDestinations
	// calls cannot stomp each other's temp files or race the rename.
	saveMu sync.Mutex
	// saveSeq makes each temp-file name unique across back-to-back saves
	// (Python uses time.time(); a monotonic counter is robust against
	// coarse-clock collisions on rapid successive flushes).
	saveSeq uint64

	announceHandlers []*AnnounceHandler

	receipts []*PacketReceipt

	enabled          bool
	linkMTUDiscovery bool
	useImplicitProof bool
	// staticTransportIdentity mirrors RNS.Reticulum.static_transport_identity:
	// when true, a non-transport instance keeps its persistent transport
	// identity instead of generating an ephemeral one (Python
	// Transport.py:235-237).
	staticTransportIdentity bool
	// persistentIdentity is the saved/loaded transport identity (Python
	// Transport._identity). It is always persisted to disk so it is stable
	// across restarts. ts.identity is the operative identity for
	// transport-level operations: equal to persistentIdentity when transport
	// is enabled (or static_transport_identity is set), or a fresh ephemeral
	// identity when transport is disabled (Python Transport.py:234-237).
	persistentIdentity *Identity

	// connectedToSharedInstance mirrors RNS.Reticulum.is_connected_to_shared_instance:
	// true when this transport owns no network interfaces and instead routes
	// everything through a co-located shared Reticulum instance over a
	// LocalClientInterface. When true, the transport must NOT load or persist
	// the path table (the shared instance owns it) — Python skips both at
	// Transport.py:259 (load) and gates persistence likewise. It also must NOT
	// add packets to the on-disk hashlist (Transport.py:1183) or run the
	// packet filter (Transport.py:1190).
	connectedToSharedInstance bool

	mu sync.Mutex

	// persistMu guards PersistData so a second persist invoked while one is
	// already in flight is skipped rather than queued or re-entered. It is the
	// Go analog of Python's module-level persist_lock with its
	// "if persist_lock.locked(): return" guard (Transport.py:152,3509-3510),
	// implemented with TryLock so the skip is non-blocking and non-reentrant.
	persistMu sync.Mutex

	// trafficDone is closed by countTrafficLoop when it exits, so Stop can join
	// the traffic goroutine and confirm it did not leak (Python _should_run loop
	// control, Transport.py:213,517).
	trafficDone chan struct{}

	// cacheCleanMu is the non-reentrant guard for the announce-cache cleaning
	// job (Python Transport.cache_clean_lock, Transport.py:151,2600-2615). It is
	// acquired with TryLock so a clean already in flight causes the next
	// scheduler tick to postpone instead of queueing a second sweep.
	cacheCleanMu sync.Mutex
	// cacheLastCleaned records when a cache clean was last dispatched, and
	// cacheCleanSleep is the per-entry yield (Python time.sleep(0.001) at
	// Transport.py:2636). cacheCleanSleep is injectable for deterministic tests.
	cacheLastCleaned time.Time
	cacheCleanSleep  func(time.Duration)

	// outboundWG tracks outbound sends dispatched on their own goroutines by
	// the fan-out paths (processAnnounceTable, forwardPathRequest,
	// forwardPathResponseToRequesters) so a single stalled peer cannot block
	// the transport maintenance loop or an interface readLoop. The
	// maintenance loop never waits on it — that would re-introduce exactly
	// the stall it exists to prevent. It is only drained by tests (via
	// WaitOutboundSends) that need deterministic delivery before asserting.
	outboundWG sync.WaitGroup

	// Traffic counters driven by count_traffic_loop (Python
	// Transport.count_traffic_loop, Transport.py:419-451). trafficCounters
	// holds the per-interface last sample ({ts, rxb, txb}); trafficRxb/txb
	// are the cumulative byte deltas since startup (Transport.traffic_rxb /
	// traffic_txb); speedRx/tx are the current aggregate bit-per-second
	// speeds (Transport.speed_rx / speed_tx). trafficMu guards all of them.
	trafficCounters map[interfaces.Interface]*trafficCounter
	trafficRxb      uint64
	trafficTxb      uint64
	speedRx         float64
	speedTx         float64
	trafficMu       sync.Mutex
}

// AnnounceEntry represents a stored network announce within the transport system.
type AnnounceEntry struct {
	PacketRaw         []byte
	SourceInterface   interfaces.Interface
	Hops              int
	NextRebroadcastAt time.Time
	Retries           int
}

type announceQueueEntry struct {
	destinationHash string
	queuedAt        time.Time
	hops            int
	emitted         uint64
	raw             []byte
}

type announceQueueState struct {
	allowedAt time.Time
	queue     []announceQueueEntry
	timer     *time.Timer
}

// AnnounceRateEntry tracks the rate of announces received for a specific destination.
type AnnounceRateEntry struct {
	Last           time.Time
	RateViolations int
	BlockedUntil   time.Time
	Timestamps     []time.Time
}

// BlackholeIdentityEntry defines an identity that is temporarily or permanently blocked from communication.
type BlackholeIdentityEntry struct {
	IdentityHash []byte
	Source       []byte
	Until        *time.Time
	Reason       string
}

// PathEntry represents an entry in the path table.
type PathEntry struct {
	Timestamp     time.Time
	NextHop       []byte
	Hops          int
	Expires       time.Time
	RandomBlobs   [][]byte // Random blobs for announce replay protection
	Interface     interfaces.Interface
	InterfaceName string
	Packet        []byte
	// IfaceHash is the on-disk identity of the receiving interface
	// (SHA-256 of "{Type}[{Name}]", matching Python interface.get_hash()).
	// It is the persisted key resolvePathInterfacesLocked uses to reattach
	// the live Interface after interfaces register on startup, since Go loads
	// the path table before creating network interfaces (rns.go loads at :316,
	// initInterfaces at :331). Python stores the same value at
	// destination_table field [6].
	IfaceHash []byte
	// PacketHash is the 32-byte SHA-256 hash of the cached announce packet
	// (FullHash of the announce's hashable part, matching Python
	// announce_packet.packet_hash). It is the cache/announces/<hex> filename
	// key Python's get_cached_packet uses to recover the raw announce, and is
	// stored at destination_table field [7] instead of the raw packet bytes.
	PacketHash      []byte
	Unresponsive    bool // Whether the path has been marked unresponsive.
	ResponsiveState int  // 0=unknown, 1=responsive, 2=unresponsive.
}

// ReverseEntry represents an entry in the reverse table.
type ReverseEntry struct {
	ReceivedInterface interfaces.Interface
	OutboundInterface interfaces.Interface
	Timestamp         time.Time
}

// LinkEntry represents an entry in the link table.
type LinkEntry struct {
	Timestamp         time.Time
	NextHop           []byte
	OutboundInterface interfaces.Interface
	RemainingHops     int
	ReceivedInterface interfaces.Interface
	Hops              int
	DestinationHash   []byte
	Validated         bool
	ProofTimeout      time.Time
}

// AnnounceHandler is registered with the Transport to receive announces
// matching a given aspect filter. It mirrors the Python
// RNS.Transport.register_announce_handler() pattern.
type AnnounceHandler struct {
	AspectFilter                string
	ReceivePathResponses        bool
	ReceivedAnnounce            func(destinationHash []byte, announcedIdentity *Identity, appData []byte)
	ReceivedAnnounceWithContext func(destinationHash []byte, announcedIdentity *Identity, appData []byte, isPathResponse bool)
}

type ifacInboundHook interface {
	ApplyIFACInbound(data []byte) ([]byte, bool)
}

type ifacOutboundHook interface {
	ApplyIFACOutbound(data []byte) ([]byte, error)
}

// PathfinderM is the maximum number of hops in path finding,
// matching Python's Transport.PATHFINDER_M = 128.
const PathfinderM = 128

// Path expiration durations, matching Python Transport.py:71-73:
//
//	PATHFINDER_E      = 60*60*24*7  # one week (default)
//	AP_PATH_TIME      = 60*60*24    # one day (Access Point interface)
//	ROAMING_PATH_TIME = 60*60*6     # six hours (Roaming interface)
const (
	pathfinderE     = 7 * 24 * time.Hour
	apPathTime      = 24 * time.Hour
	roamingPathTime = 6 * time.Hour
)

const (
	pathfinderRetries        = 1
	pathfinderGrace          = 5 * time.Second
	pathfinderRandomWindow   = 500 * time.Millisecond
	localRebroadcastsMax     = 2
	announceCheckInterval    = 1 * time.Second
	announceCapDefault       = 2
	maxQueuedAnnounces       = 16384
	queuedAnnounceLife       = 24 * time.Hour
	pathRequestMinInterval   = 20 * time.Second
	pathRequestCullAfter     = 2 * pathRequestMinInterval
	pendingPathRequestTTL    = 20 * time.Second
	pathTablePersistInterval = 30 * time.Second
	packetHashRotateDefault  = 50000
	reverseEntryTimeout      = 8 * time.Minute
	linkEntryTimeout         = 8 * time.Minute

	// interfaceJobsInterval matches Python Transport.interface_jobs_interval
	// (Transport.py:194): the cadence at which per-interface ingress-control
	// state machines are advanced and held announces are released.
	interfaceJobsInterval = 5 * time.Second

	// cacheCleanInterval matches Python Transport.cache_clean_interval
	// (Transport.py:188): the announce-cache cleaning job is dispatched at most
	// once every 5 minutes. cacheCleanYieldSleep is the per-entry yield
	// (Transport.py:2636 time.sleep(0.001)) so a large sweep stays low priority.
	cacheCleanInterval   = 5 * time.Minute
	cacheCleanYieldSleep = time.Millisecond

	// establishmentTimeoutPerHop matches Python's
	// Link.ESTABLISHMENT_TIMEOUT_PER_HOP = Reticulum.DEFAULT_PER_HOP_TIMEOUT = 6 seconds.
	establishmentTimeoutPerHop = 6 * time.Second

	// maxRandomBlobs is the maximum number of random blobs per destination
	// for announce replay protection, matching Python's Transport.MAX_RANDOM_BLOBS.
	maxRandomBlobs = 64
)

// NewTransportSystem constructs an independent TransportSystem.
func NewTransportSystem(logger *Logger) *TransportSystem {
	return &TransportSystem{
		logger:                  logger,
		interfaces:              make([]interfaces.Interface, 0),
		destinations:            make([]*Destination, 0),
		destinationsMap:         make(map[string]*Destination),
		pendingLinks:            make([]*Link, 0),
		activeLinks:             make([]*Link, 0),
		pathTable:               make(map[string]*PathEntry),
		reverseTable:            make(map[string]*ReverseEntry),
		linkTable:               make(map[string]*LinkEntry),
		packetHashes:            make(map[string]time.Time),
		packetHashesPrev:        make(map[string]time.Time),
		packetHashRotateAt:      packetHashRotateDefault,
		announceTable:           make(map[string]*AnnounceEntry),
		announceRateTable:       make(map[string]*AnnounceRateEntry),
		announceQueues:          make(map[interfaces.Interface]*announceQueueState),
		pathRequests:            make(map[string]time.Time),
		pendingPathRequests:     make(map[string][]interfaces.Interface),
		pendingPathRequestAt:    make(map[string]time.Time),
		downNotified:            make(map[interfaces.Interface]struct{}),
		packetRSSICache:         make(map[string]float64),
		packetSNRCache:          make(map[string]float64),
		packetQCache:            make(map[string]float64),
		blackholedIdentities:    make(map[string]BlackholeIdentityEntry),
		knownDestinations:       make(map[string][]any),
		knownRatchets:           make(map[string][]byte),
		blackholeUpdateInterval: BlackholeUpdateInterval,
		allowLinkPathRebalance:  true,
		readyWaitTimeout:        60 * time.Second,
		readyPollInterval:       250 * time.Millisecond,
	}
}

// SetAllowLinkPathRebalance toggles per-instance path re-balancing for link
// proofs, mirroring Python's Transport.ALLOW_LINK_PATH_REBALANCE class
// attribute (RNS/Transport.py:90). It is per-instance so tests and the
// config layer can toggle it without racing on a process-wide global.
func (ts *TransportSystem) SetAllowLinkPathRebalance(v bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.allowLinkPathRebalance = v
}

// AllowLinkPathRebalance reports whether path re-balancing is enabled for this
// transport instance.
func (ts *TransportSystem) AllowLinkPathRebalance() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.allowLinkPathRebalance
}

// GetLogger returns the logger associated with this transport system.
func (ts *TransportSystem) GetLogger() *Logger {
	if ts == nil {
		return nil
	}
	return ts.logger
}

// Identity returns the local identity assigned to the transport system.
func (ts *TransportSystem) Identity() *Identity {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.identity
}

// StartedAt returns the time when the transport system was started.
func (ts *TransportSystem) StartedAt() time.Time {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.startedAt
}

// LinkTable returns the active link table managed by the transport system.
func (ts *TransportSystem) LinkTable() map[string]*LinkEntry {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.linkTable
}

func (ts *TransportSystem) ensureStateLocked() {
	if ts.packetHashes == nil {
		ts.packetHashes = make(map[string]time.Time)
	}
	if ts.packetHashesPrev == nil {
		ts.packetHashesPrev = make(map[string]time.Time)
	}
	if ts.packetHashRotateAt <= 0 {
		ts.packetHashRotateAt = packetHashRotateDefault
	}
	if ts.pathTable == nil {
		ts.pathTable = make(map[string]*PathEntry)
	}
	if ts.destinationsMap == nil {
		ts.destinationsMap = make(map[string]*Destination)
	}
	if ts.reverseTable == nil {
		ts.reverseTable = make(map[string]*ReverseEntry)
	}
	if ts.linkTable == nil {
		ts.linkTable = make(map[string]*LinkEntry)
	}
	if ts.announceTable == nil {
		ts.announceTable = make(map[string]*AnnounceEntry)
	}
	if ts.announceRateTable == nil {
		ts.announceRateTable = make(map[string]*AnnounceRateEntry)
	}
	if ts.announceQueues == nil {
		ts.announceQueues = make(map[interfaces.Interface]*announceQueueState)
	}
	if ts.pathRequests == nil {
		ts.pathRequests = make(map[string]time.Time)
	}
	if ts.pendingPathRequests == nil {
		ts.pendingPathRequests = make(map[string][]interfaces.Interface)
	}
	if ts.pendingPathRequestAt == nil {
		ts.pendingPathRequestAt = make(map[string]time.Time)
	}
	if ts.packetRSSICache == nil {
		ts.packetRSSICache = make(map[string]float64)
	}
	if ts.packetSNRCache == nil {
		ts.packetSNRCache = make(map[string]float64)
	}
	if ts.packetQCache == nil {
		ts.packetQCache = make(map[string]float64)
	}
	if ts.blackholedIdentities == nil {
		ts.blackholedIdentities = make(map[string]BlackholeIdentityEntry)
	}
}

// SetEnabled sets whether the transport system is enabled.
func (ts *TransportSystem) SetEnabled(enabled bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.enabled = enabled
}

// Enabled returns whether the transport system is enabled.
func (ts *TransportSystem) Enabled() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.enabled
}

// SetStaticTransportIdentity mirrors RNS.Reticulum.static_transport_identity:
// when true, a non-transport instance keeps its persistent transport identity
// instead of generating an ephemeral one. Must be set before Start so the
// identity-init branch in Start sees it (Python applies the flag during
// __apply_config, before Transport.start).
func (ts *TransportSystem) SetStaticTransportIdentity(v bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.staticTransportIdentity = v
}

// PersistentIdentity returns the saved/loaded transport identity (Python
// Transport._identity), or nil if Start has not yet initialized it.
func (ts *TransportSystem) PersistentIdentity() *Identity {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.persistentIdentity
}

// LinkMTUDiscovery returns whether link MTU discovery is enabled.
func (ts *TransportSystem) LinkMTUDiscovery() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.linkMTUDiscovery
}

// UseImplicitProof returns whether identity proofs should omit the packet hash.
func (ts *TransportSystem) UseImplicitProof() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.useImplicitProof
}

// SetLinkMTUDiscovery sets whether link MTU discovery is enabled.
func (ts *TransportSystem) SetLinkMTUDiscovery(enabled bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.linkMTUDiscovery = enabled
}

// SetUseImplicitProof sets whether identity proofs should omit the packet hash.
func (ts *TransportSystem) SetUseImplicitProof(enabled bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.useImplicitProof = enabled
}

// Start initializes the transport system.
func (ts *TransportSystem) Start(storagePath string) error {
	ts.mu.Lock()
	ts.ensureStateLocked()
	if ts.running {
		ts.mu.Unlock()
		return nil
	}
	ts.stopCh = make(chan struct{})
	ts.doneCh = make(chan struct{})
	ts.trafficDone = make(chan struct{})
	ts.running = true
	// Reset the ready flag for a re-Start so Inbound waits through this
	// startup window again; it is set true at the end of Start.
	ts.ready = false
	ts.startedAt = time.Now()
	// Defer the first announce-cache clean by 60s past startup, matching
	// Python Transport.start (Transport.py:293 sets cache_last_cleaned to
	// time.time()+60) so a freshly started transport does not immediately sweep.
	ts.cacheLastCleaned = ts.startedAt.Add(60 * time.Second)
	if ts.cacheCleanSleep == nil {
		ts.cacheCleanSleep = time.Sleep
	}

	ts.storagePath = storagePath
	if _, err := os.Stat(ts.storagePath); os.IsNotExist(err) {
		if err := os.MkdirAll(ts.storagePath, 0700); err != nil {
			ts.mu.Unlock()
			return err
		}
	}

	// Ensure the blackhole list directory exists (Python
	// RNS.Reticulum.__init__ creates configdir/storage/blackhole).
	ts.blackholePath = filepath.Join(ts.storagePath, "blackhole")
	if err := os.MkdirAll(ts.blackholePath, 0o700); err != nil {
		ts.mu.Unlock()
		return err
	}

	// Load or create the persistent transport identity (Python
	// Transport.py:222-230). The persistent identity is always saved to disk
	// so it is stable across restarts and available if transport is later
	// enabled. ts.persistentIdentity mirrors Python Transport._identity.
	identityPath := filepath.Join(ts.storagePath, "transport_identity")
	var persistent *Identity
	if _, err := os.Stat(identityPath); err == nil {
		id, err := FromFile(identityPath, ts.logger)
		if err != nil {
			ts.logger.Error("Could not load transport identity: %v", err)
		} else {
			persistent = id
			ts.logger.Verbose("Loaded Transport Identity from storage")
		}
	}
	if persistent == nil {
		ts.logger.Verbose("No valid Transport Identity in storage, creating...")
		id, err := NewIdentity(true, ts.logger)
		if err != nil {
			ts.mu.Unlock()
			return err
		}
		persistent = id
		if err := persistent.ToFile(identityPath); err != nil {
			ts.logger.Error("Could not save transport identity: %v", err)
		}
	}
	ts.persistentIdentity = persistent

	// Python Transport.py:234-237: a non-transport instance (without
	// static_transport_identity) uses a fresh ephemeral identity for all
	// transport-level operations (rebroadcast transport_id, blackhole/probe
	// destinations, path-request signing) so it never advertises a persistent
	// transport identity it does not actually operate. Transport-enabled
	// instances use the persistent identity directly. When a network identity
	// was already installed by SetNetworkIdentity (which sets ts.identity
	// before Start runs), it remains the operative identity for backward
	// compatibility — the ephemeral override only applies to the transport
	// identity, not the configured network identity.
	if ts.identity == nil {
		if ts.enabled || ts.staticTransportIdentity {
			ts.identity = persistent
		} else {
			id, err := NewIdentity(true, ts.logger)
			if err != nil {
				ts.mu.Unlock()
				return err
			}
			ts.identity = id
			ts.logger.Verbose("Initialized ephemeral transport identity %x", ts.identity.Hash)
		}
	}
	// The path table is loaded separately via LoadPathTable() so that the
	// caller (NewReticulum) can gate it on the instance role: a process
	// connected to a shared Reticulum instance must NOT load the shared
	// instance's destination_table (Python Transport.py:259), since those
	// entries reference interfaces the client does not own (Interface=nil),
	// which would break outbound transport forwarding. Start() therefore
	// leaves the path table empty; NewReticulum loads it after it has
	// determined whether this process is a shared instance, a client of
	// one, or standalone.
	ts.mu.Unlock()

	// Setup control destinations
	pathRequestDst, err := NewDestination(ts, nil, DestinationIn, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		return err
	}
	ts.pathRequestHash = copyBytes(pathRequestDst.Hash)
	// handlePathRequest returns a bool for the direct Inbound caller (to gate
	// onward relaying); the packet-callback signature has no return, so wrap
	// it. Inbound short-circuits path requests before callback delivery, so
	// this wrapper is only a compile-time type adapter in practice.
	pathRequestDst.SetPacketCallback(func(data []byte, p *Packet) { ts.handlePathRequest(data, p) })

	ts.mu.Lock()
	_, found := ts.destinationsMap[string(pathRequestDst.Hash)]
	ts.mu.Unlock()
	if !found {
		ts.RegisterDestination(pathRequestDst)
	}

	// Register the inbound tunnel-synthesis control destination (Python
	// Transport.py:215-217): a PLAIN IN destination
	// rnstransport.tunnel.synthesize whose packet callback validates and
	// establishes tunnels from remote transports.
	if err := ts.registerTunnelSynthesizeDestination(); err != nil {
		return err
	}

	// Register the blackhole list publishing destination (Python
	// Transport.py:227-233), gated on publish_blackhole_enabled. The
	// destination is a SINGLE IN destination on the transport identity,
	// serving the /list request handler that returns the current
	// blackholed_identities map.
	if ts.publishBlackhole && ts.identity != nil {
		if err := ts.registerBlackholeDestination(); err != nil {
			return err
		}
	}

	// Start maintenance loop
	go ts.maintenance()

	// Start the traffic counter loop (Python Transport.start launches
	// Transport.count_traffic_loop as a daemon thread, Transport.py:252). It
	// closes trafficDone on exit so Stop can join it (leak check).
	go ts.countTrafficLoop(ts.stopCh, ts.trafficDone)

	// Mark the transport ready for inbound processing (Python
	// Transport.py:427 sets Transport.ready = True at the end of start).
	ts.mu.Lock()
	ts.ready = true
	ts.mu.Unlock()

	return nil
}

// registerBlackholeDestination creates the rnstransport.info.blackhole
// request destination and registers the /list handler on it, mirroring
// Python Transport.py:229-230.
func (ts *TransportSystem) registerBlackholeDestination() error {
	dst, err := NewDestination(ts, ts.identity, DestinationIn, DestinationSingle, "rnstransport", "info", "blackhole")
	if err != nil {
		return err
	}
	dst.RegisterRequestHandler("/list", ts.blackholeListHandler, AllowAll, nil, false)
	ts.mu.Lock()
	ts.blackholeDestination = dst
	_, already := ts.destinationsMap[string(dst.Hash)]
	ts.mu.Unlock()
	if !already {
		ts.RegisterDestination(dst)
	}
	return nil
}

// SetPublishBlackhole enables or disables blackhole list publishing
// (Python RNS.Reticulum.publish_blackhole_enabled). It must be called
// before Start for the publishing destination to be registered during
// startup.
func (ts *TransportSystem) SetPublishBlackhole(enabled bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.publishBlackhole = enabled
}

// registerTunnelSynthesizeDestination creates the inbound PLAIN destination
// rnstransport.tunnel.synthesize and wires tunnelSynthesizeHandler as its
// packet callback (Python Transport.py:215-217). Unlike the blackhole
// destination it is always registered — every transport accepts inbound
// tunnel establishment.
func (ts *TransportSystem) registerTunnelSynthesizeDestination() error {
	dst, err := NewDestination(ts, nil, DestinationIn, DestinationPlain, "rnstransport", "tunnel", "synthesize")
	if err != nil {
		return err
	}
	dst.SetPacketCallback(ts.tunnelSynthesizeHandler)
	ts.mu.Lock()
	ts.tunnelSynthesizeDestination = dst
	_, already := ts.destinationsMap[string(dst.Hash)]
	ts.mu.Unlock()
	if !already {
		ts.RegisterDestination(dst)
	}
	return nil
}

// Stop halts the transport system, shutting down all network interfaces and closing active connections.
func (ts *TransportSystem) Stop() {
	ts.mu.Lock()
	if !ts.running {
		ts.mu.Unlock()
		return
	}
	stopCh := ts.stopCh
	doneCh := ts.doneCh
	trafficDone := ts.trafficDone
	ts.running = false
	ts.ready = false
	updater := ts.blackholeUpdater
	// Snapshot the link lists so the teardown below can run without holding
	// ts.mu (Teardown sends a packet, which re-enters the transport).
	active := append([]*Link(nil), ts.activeLinks...)
	pending := append([]*Link(nil), ts.pendingLinks...)
	ts.mu.Unlock()

	if updater != nil {
		updater.Stop()
	}

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}
	// Join the traffic-counter loop so Stop returns only once every background
	// goroutine has exited (leak check). countTrafficLoop closes trafficDone on
	// its way out after observing stopCh.
	if trafficDone != nil {
		<-trafficDone
	}

	// Final flush of any destinations learned since the last periodic save, so
	// the debounce in Remember doesn't lose up to one interval on shutdown.
	ts.flushKnownDestinationsIfDirty()

	// Tear down active and pending links before detaching interfaces,
	// mirroring Python Transport.detach_interfaces (Transport.py:3172-3184):
	// iterate active_links then pending_links calling link.teardown(), count
	// the closed links, and sleep 150ms when any were closed so the teardown
	// packets leave the local transport before the interfaces go away. The
	// snapshot was taken under ts.mu above; Teardown itself takes the per-link
	// lock and re-enters the transport to send, so it must run unlocked.
	closedLinks := 0
	for _, l := range active {
		if l == nil {
			continue
		}
		l.Teardown()
		closedLinks++
	}
	for _, l := range pending {
		if l == nil {
			continue
		}
		l.Teardown()
		closedLinks++
	}
	if closedLinks > 0 {
		time.Sleep(150 * time.Millisecond)
	}

	// Final flush of the path table, packet-hash list, and tunnel table to
	// storage before interfaces detach, mirroring Python's
	// Transport.exit_handler → persist_data (Transport.py:3402-3407). This
	// caches paths learned during this run so a subsequent process that
	// reuses this storage directory loads them instead of rediscovering every
	// path. PersistData gates itself on the shared-instance role; the write
	// happens before Detach so SavePathTable's announce-cache files still see
	// live interfaces. Errors are logged rather than aborting shutdown.
	if err := ts.PersistData(); err != nil {
		ts.logger.Error("Error persisting transport data during stop: %v", err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, state := range ts.announceQueues {
		ts.stopAnnounceQueueTimerLocked(state)
	}
	for _, iface := range ts.interfaces {
		if err := iface.Detach(); err != nil {
			ts.logger.Error("Error detaching interface %v during transport stop: %v", iface.Name(), err)
		}
	}
	// Drop the in-memory queues so a stopped transport holds no pending
	// announces, receipts, or reverse-table entries (Python
	// Transport.void_queues, Transport.py:3517-3521, called from exit_handler).
	ts.voidQueuesLocked()
	ts.interfaces = nil
	ts.pendingLinks = nil
	ts.activeLinks = nil
}

// voidQueuesLocked clears the in-memory transport queues: outstanding packet
// receipts, the reverse table, and each interface's held-announce deque. It is
// the Go port of Python's Transport.void_queues (Transport.py:3517-3521) and
// must be called under ts.mu. Python's held_announces is a single global dict;
// Go distributes held announces across interfaces, so each interface's deque is
// cleared in turn.
func (ts *TransportSystem) voidQueuesLocked() {
	ts.receipts = nil
	ts.reverseTable = make(map[string]*ReverseEntry)
	for _, iface := range ts.interfaces {
		if clearer, ok := iface.(interface{ ClearHeldAnnounces() }); ok {
			clearer.ClearHeldAnnounces()
		}
	}
}

// SetNetworkIdentity sets the primary identity used by the transport system for network-level operations.
func (ts *TransportSystem) SetNetworkIdentity(identity *Identity) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.networkID = identity
	ts.identity = identity
}

// NetworkIdentity returns the network identity configured for transport-level
// operations, if one is available.
func (ts *TransportSystem) NetworkIdentity() *Identity {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.networkID
}

// NetworkIdentityHash retrieves the hash of the current network identity, if one is configured.
func (ts *TransportSystem) NetworkIdentityHash() []byte {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.networkID == nil || len(ts.networkID.Hash) == 0 {
		return nil
	}
	h := make([]byte, len(ts.networkID.Hash))
	copy(h, ts.networkID.Hash)
	return h
}

// DiscoverInterfaces initiates a discovery process to find available interfaces on the network.
func (ts *TransportSystem) DiscoverInterfaces() {
	ts.mu.Lock()
	if ts.discoveryOn {
		ts.mu.Unlock()
		return
	}
	ts.discoveryOn = true
	ts.discoverCalls++
	hook := ts.discoverHook
	ts.mu.Unlock()
	if hook != nil {
		go hook()
	}
}

// EnableBlackholeUpdater starts the configured blackhole updater flow
// (Python Transport.enable_blackhole_updater, Transport.py:413-417): it
// constructs a BlackholeUpdater bound to the configured blackhole_sources
// and the real /list fetch, and starts its loop. It is idempotent.
func (ts *TransportSystem) EnableBlackholeUpdater() {
	ts.mu.Lock()
	if ts.blackholeUpdaterOn {
		ts.mu.Unlock()
		return
	}
	ts.blackholeUpdaterOn = true
	ts.enableBlackholeCalls++
	ts.mu.Unlock()

	updater := NewBlackholeUpdater(ts, func() [][]byte {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		return append([][]byte(nil), ts.blackholeSources...)
	}, ts.blackholeFetch)
	// Propagate the configured update interval (Python
	// RNS.Reticulum.blackhole_update_interval(), Reticulum.py:601-604) so the
	// loop honors [reticulum] blackhole_update_interval instead of the default.
	updater.SetUpdateInterval(ts.BlackholeUpdateInterval())
	ts.mu.Lock()
	ts.blackholeUpdater = updater
	ts.mu.Unlock()
	updater.Start()
}

// EnableBlackholeUpdaterCallCount reports how many updater-enable calls have run.
func (ts *TransportSystem) EnableBlackholeUpdaterCallCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.enableBlackholeCalls
}

// BlackholeUpdater returns the running BlackholeUpdater, or nil if
// EnableBlackholeUpdater has not been called.
func (ts *TransportSystem) BlackholeUpdater() *BlackholeUpdater {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.blackholeUpdater
}

// StopBlackholeUpdater stops the running BlackholeUpdater if one was started.
func (ts *TransportSystem) StopBlackholeUpdater() {
	ts.mu.Lock()
	updater := ts.blackholeUpdater
	ts.mu.Unlock()
	if updater != nil {
		updater.Stop()
	}
}

// DiscoverInterfacesCallCount returns the number of times the discovery interface process has been called.
func (ts *TransportSystem) DiscoverInterfacesCallCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.discoverCalls
}

// SetDiscoverInterfacesHook registers the callback that should run when
// DiscoverInterfaces is invoked.
func (ts *TransportSystem) SetDiscoverInterfacesHook(hook func()) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.discoverHook = hook
}

// HopsTo returns the number of hops to the given destination hash,
// or PathfinderM if the path is unknown, matching Python's
// Transport.hops_to().
func (ts *TransportSystem) HopsTo(destinationHash []byte) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	entry, ok := ts.pathTable[string(destinationHash)]
	if ok && entry != nil {
		return entry.Hops
	}
	return PathfinderM
}

// RegisterAnnounceHandler registers a handler that will be called when
// an announce matching the handler's AspectFilter is received.
func (ts *TransportSystem) RegisterAnnounceHandler(handler *AnnounceHandler) {
	if handler == nil || (handler.ReceivedAnnounce == nil && handler.ReceivedAnnounceWithContext == nil) {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.announceHandlers = append(ts.announceHandlers, handler)
}

// AnnounceHandlers returns the currently registered announce handlers.
func (ts *TransportSystem) AnnounceHandlers() []*AnnounceHandler {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	result := make([]*AnnounceHandler, len(ts.announceHandlers))
	copy(result, ts.announceHandlers)
	return result
}

func (ts *TransportSystem) isLocalClientInterface(iface interfaces.Interface) bool {
	if iface == nil {
		return false
	}
	// On Linux, local client interfaces are typically LocalClientInterface.
	// We can check the type name or use an interface check.
	// We check for both names used in the Go port and original Python implementation.
	name := iface.Name()
	return iface.Type() == "LocalInterface" && (strings.Contains(name, "Local Client") || strings.Contains(name, "Local shared instance"))
}

func (ts *TransportSystem) isForLocalClient(p *Packet) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if entry, ok := ts.pathTable[string(p.DestinationHash)]; ok {
		return entry.Hops == 0
	}
	return false
}

func (ts *TransportSystem) isForLocalClientLink(p *Packet) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if entry, ok := ts.linkTable[string(p.DestinationHash)]; ok {
		return ts.isLocalClientInterface(entry.ReceivedInterface) || ts.isLocalClientInterface(entry.OutboundInterface)
	}
	return false
}

// RatchetExpiry is how long a received forward-secrecy ratchet is retained
// before CleanRatchets discards it (Python Identity.RATCHET_EXPIRY,
// RNS/Identity.py:69). Thirty days.
const RatchetExpiry = 30 * 24 * time.Hour

// CleanRatchets removes forward-secrecy ratchet files from storage that are
// expired, corrupted, or whose destination is no longer known, mirroring
// Python Identity._clean_ratchets (RNS/Identity.py:446-496). For each ratchet
// file in <storagePath>/ratchets it removes the file when ANY of these hold:
//   - expired: now > received + RatchetExpiry,
//   - corrupted: the file fails to unpack or lacks a numeric "received",
//   - unknown: the hex filename decodes to a destination hash that is NOT a
//     key in knownDestinations (RNS/Identity.py:470-471,474-475).
//
// When a file is removed, the in-memory knownRatchets cache is reset so a
// stale cached entry is not served after its file is gone.
func (ts *TransportSystem) CleanRatchets() {
	ts.mu.Lock()
	path := ts.storagePath
	ts.mu.Unlock()

	if path == "" {
		return
	}

	ratchetDir := filepath.Join(path, "ratchets")
	entries, err := os.ReadDir(ratchetDir)
	if err != nil {
		if !os.IsNotExist(err) {
			ts.logger.Error("Failed to read ratchet directory for cleaning: %v", err)
		}
		return
	}

	now := float64(time.Now().UnixNano()) / 1e9
	expiry := RatchetExpiry.Seconds()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".out") || strings.HasSuffix(entry.Name(), ".tmp") {
			continue
		}

		name := entry.Name()
		p := filepath.Join(ratchetDir, name)

		// A ratchet is "unknown" when its filename (the destination hash in
		// lowercase hex) decodes to a hash that is not a key in
		// knownDestinations. This is independent of whether the file unpacks,
		// so a corrupted ratchet for an unknown destination is still removed.
		unknown := false
		if destHash, decErr := hex.DecodeString(name); decErr == nil {
			ts.mu.Lock()
			_, known := ts.knownDestinations[string(destHash)]
			ts.mu.Unlock()
			if !known {
				unknown = true
			}
		}

		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		expired := false
		corrupted := false
		unpacked, err := msgpack.Unpack(data)
		if err != nil {
			ts.logger.Error("Corrupted ratchet data while reading %s, removing file", p)
			corrupted = true
		} else if m, ok := unpacked.(map[any]any); ok {
			if received, ok := numericValue(m["received"]); ok {
				if now > received+expiry {
					expired = true
				}
			} else {
				corrupted = true
			}
		} else {
			corrupted = true
		}

		if expired || corrupted || unknown {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				ts.logger.Error("Failed to remove ratchet file %v: %v", name, err)
			}
			// Reset the in-memory ratchet cache so a stale cached entry is not
			// served after its file is removed. The cache is small and lazily
			// repopulated by GetRatchet on demand.
			ts.mu.Lock()
			ts.knownRatchets = make(map[string][]byte)
			ts.mu.Unlock()
		}
	}
}

// numericValue returns the float64 form of any msgpack numeric kind (int*,
// uint*, float*) used for the known-destinations timestamp and use-timestamp
// elements, which Python stores as an int (0, -1) or a float (time.time()) and
// msgpack unpacks into the corresponding Go numeric type. The second result is
// false when v is not a numeric kind.
func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

// CleanKnownDestinations drops stale, pathless known destinations from the
// in-memory table and removes their on-disk ratchet files, then persists the
// trimmed table. It mirrors Python Identity.clean_known_destinations
// (RNS/Identity.py:285-340).
//
// An entry is stale when ALL of the following hold:
//   - it is NOT retained (use-timestamp != -1), and
//   - it has no current path (Transport.has_path is false), and
//   - either it was never used (use-timestamp == 0) AND its last announce is
//     older than UnusedDestinationLinger, or it was used but its last use is
//     older than DestinationTimeout*1.25.
//
// Retained entries (use-timestamp == -1) and entries with a current path are
// always kept. Stale entries are removed from knownDestinations and their
// ratchet file (ratchets/<hexhash>) is unlinked best-effort, like Python's
// os.unlink in the stale-cleanup loop (RNS/Identity.py:333-340).
func (ts *TransportSystem) CleanKnownDestinations() {
	now := time.Now()
	nowSec := float64(now.UnixNano()) / 1e9
	lingerSec := UnusedDestinationLinger.Seconds()
	timeoutSec := DestinationTimeout.Seconds() * 1.25

	ts.mu.Lock()
	storagePath := ts.storagePath
	total := len(ts.knownDestinations)
	stale := make([]string, 0)
	for dh, entry := range ts.knownDestinations {
		if len(entry) < 5 {
			continue
		}
		_, hasPath := ts.pathTable[dh]
		lastAnnounce, _ := numericValue(entry[0])
		useTS, _ := numericValue(entry[4])
		isRetained := useTS == -1
		wasUsed := useTS > 0
		if isRetained || hasPath {
			continue
		}
		unusedFor := nowSec - useTS
		if !wasUsed && (nowSec-lastAnnounce) > lingerSec {
			stale = append(stale, dh)
		} else if unusedFor > timeoutSec {
			stale = append(stale, dh)
		}
	}
	removed := 0
	for _, dh := range stale {
		if _, ok := ts.knownDestinations[dh]; ok {
			delete(ts.knownDestinations, dh)
			removed++
		}
	}
	if removed > 0 && storagePath != "" {
		ts.knownDestDirty = true
	}
	ts.mu.Unlock()

	if removed > 0 && storagePath != "" {
		ratchetDir := filepath.Join(storagePath, "ratchets")
		for _, dh := range stale {
			hexHash := fmt.Sprintf("%x", []byte(dh))
			ratchetPath := filepath.Join(ratchetDir, hexHash)
			if err := os.Remove(ratchetPath); err != nil && !os.IsNotExist(err) {
				ts.logger.Warning("Could not clean stale ratchets for %x: %v", []byte(dh), err)
			}
		}
		ts.SaveKnownDestinations(storagePath)
	}
	ts.logger.Pathing("Cleaned known destinations: total %v, removed %v", total, removed)
}

func (ts *TransportSystem) maintenance() {
	defer close(ts.doneCh)
	ratchetTicker := time.NewTicker(24 * time.Hour)
	announceTicker := time.NewTicker(announceCheckInterval)
	pathPersistTicker := time.NewTicker(pathTablePersistInterval)
	interfaceJobsTicker := time.NewTicker(interfaceJobsInterval)
	cacheCleanTicker := time.NewTicker(cacheCleanInterval)
	knownDestCleanTicker := time.NewTicker(KnownDestinationsInterval)
	defer ratchetTicker.Stop()
	defer announceTicker.Stop()
	defer pathPersistTicker.Stop()
	defer interfaceJobsTicker.Stop()
	defer cacheCleanTicker.Stop()
	defer knownDestCleanTicker.Stop()

	// Initial clean
	ts.CleanRatchets()

	for {
		select {
		case <-ts.stopCh:
			return
		case <-announceTicker.C:
			now := time.Now()
			ts.processAnnounceTable(now)
			ts.cullPathRequests(now)
			ts.cullExpiredPaths(now)
			ts.cullStaleTransportTables(now)
			ts.cullTunnels(now)
		case <-pathPersistTicker.C:
			ts.persistPathTable()
			ts.flushKnownDestinationsIfDirty()
		case <-interfaceJobsTicker.C:
			ts.runInterfaceJobs()
		case <-ratchetTicker.C:
			ts.CleanRatchets()
		case <-cacheCleanTicker.C:
			// Dispatch the cache clean on its own goroutine, matching Python
			// Transport.jobs (Transport.py:964-968) which spawns a daemon thread.
			// cleanCache's non-blocking lock postpones overlapping sweeps.
			go ts.cleanCache()
			// Reconcile the destinations hash index with the destinations list
			// (Python Transport.clean_destinations_map, Transport.py:2478-2496).
			ts.CleanDestinationsMap()
		case <-knownDestCleanTicker.C:
			// Periodically drop stale/pathless known destinations and their
			// ratchet files (Python Transport.jobs scheduling
			// Identity.clean_known_destinations, Transport.py:971-976). Run on
			// a goroutine so a large table sweep does not block the loop, and
			// recover so a faulty entry cannot kill the sweep (Python wraps the
			// job in try/except).
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						ts.logger.Error("Error while running scheduled known destinations cleaning: %v", rec)
					}
				}()
				ts.CleanKnownDestinations()
			}()
		}
	}
}

// runInterfaceJobs advances the per-interface ingress-control state machines
// and releases held announces, mirroring Python's interface-jobs block
// (Transport.py:951-959). Every interface_jobs_interval it:
//   - calls ShouldIngressLimit to refresh each interface's burst state, and
//   - calls ProcessHeldAnnounces to release the fewest-hops held announce once
//     the burst has subsided, re-injecting it into Inbound on its original
//     receiving interface.
//
// The should_ingress_limit_pr and phy_keepalive/send_keepalive halves of the
// Python loop (PR limiting and LocalInterface sleep) are intentionally
// omitted here.
func (ts *TransportSystem) runInterfaceJobs() {
	for _, iface := range ts.GetInterfaces() {
		iface.ShouldIngressLimit()
		iface.ShouldIngressLimitPr()
		raw, recv, ok := iface.ProcessHeldAnnounces()
		if ok {
			// Re-inject on a goroutine to match Python's daemon-thread release
			// (Interface.py:254) and to avoid blocking the maintenance loop
			// behind a full Inbound pass.
			go ts.Inbound(raw, recv)
		}
	}
}

func pathTableFile(storagePath string) string {
	return filepath.Join(storagePath, "destination_table")
}

func anyToInt64(value any) (int64, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v := rv.Uint()
		if v > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

// anyToFloatSeconds decodes a destination_table timestamp/expires field that
// Python writes as a float (time.time() seconds). It also accepts an integer
// (whole seconds) defensively. Returns false for anything else.
func anyToFloatSeconds(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		rv := reflect.ValueOf(value)
		switch rv.Kind() {
		case reflect.Float32, reflect.Float64:
			return rv.Float(), true
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return float64(rv.Int()), true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return float64(rv.Uint()), true
		}
		return 0, false
	}
}

// floatToTime converts a float-seconds timestamp (Python time.time() style)
// into a time.Time without losing the sub-second part to float->int64 overflow.
func floatToTime(sec float64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	s := int64(sec)
	ns := int64((sec - float64(s)) * 1e9)
	return time.Unix(s, ns)
}

// interfaceHash returns Python's interface.get_hash(): the SHA-256 of the
// interface's string identity "{Type}[{Name}]" (e.g. "AutoInterface[myauto]").
// It is the value Python stores at destination_table field [6] and resolves
// via find_interface_from_hash. Returns nil for a nil interface.
//
// When the interface exposes MemoizedHash (all BaseInterface-embedding types),
// the hash is computed once and cached on the instance — matching Python's
// Interface.get_hash memoization (RNS/Interfaces/Interface.py:144-146) instead
// of recomputing the SHA-256 on every call.
// interfaceHashString returns the exact string Python's Interface.get_hash
// hashes (`RNS.Identity.full_hash(str(self))`). Most interface types implement
// `__str__` as `f"{Type}[{name}]"`, which equals `Type()[Name()]` — the default
// below. Interface types whose Python `__str__` embeds extra fields (TCP, UDP,
// Local, Backbone all append `/ip:port`) implement the optional HashString
// method to reproduce Python's string byte-for-byte. Without this, a
// destination_table written by Python (storing Python interface hashes in
// field [6]) could never have its interfaces resolved by Go —
// findInterfaceByHash would compare a Go `Type[Name]` hash to a Python
// `Type[Name/host:port]` hash and never match, leaving PathEntry.Interface nil.
// InterfaceString returns the Python __str__ equivalent of an interface —
// the exact string Python's Interface.get_hash hashes and that rnpath's
// "on <interface>" / get_next_hop_if_name (str(next_hop_interface)) print.
// Callers that render paths for parity with Python RNS tools should use this
// rather than Name() (which omits the Type prefix and, for TCP/UDP/Local/
// Backbone, the /host:port suffix). A nil interface returns "None", matching
// Python str(None).
func InterfaceString(iface interfaces.Interface) string {
	if iface == nil {
		return "None"
	}
	return interfaceHashString(iface)
}

func interfaceHashString(iface interfaces.Interface) string {
	if hs, ok := iface.(interface{ HashString() string }); ok {
		return hs.HashString()
	}
	return fmt.Sprintf("%v[%v]", iface.Type(), iface.Name())
}

func interfaceHash(iface interfaces.Interface) []byte {
	if iface == nil {
		return nil
	}
	if mh, ok := iface.(interface{ MemoizedHash(func() []byte) []byte }); ok {
		return mh.MemoizedHash(func() []byte {
			return FullHash([]byte(interfaceHashString(iface)))
		})
	}
	return FullHash([]byte(interfaceHashString(iface)))
}

// pathExpiryForInterface returns the path-table expiration duration for a
// path learned from an announce received on iface, matching Python
// Transport.py:1932-1939:
//
//	if   interface.mode == MODE_ACCESS_POINT: expires = now + AP_PATH_TIME
//	elif interface.mode == MODE_ROAMING:      expires = now + ROAMING_PATH_TIME
//	else:                                    expires = now + PATHFINDER_E
//
// The Go port previously used a fixed 1-week expiry for all interface modes,
// which caused Access-Point and Roaming paths to persist far longer than
// Python intended — up to 168× longer for Roaming paths. A stale path to a
// mobile/ephemeral peer that has moved away would remain in the table long
// after the peer was unreachable, causing "No path to destination known"
// when the path existed but was unusable.
func pathExpiryForInterface(iface interfaces.Interface) time.Duration {
	if iface == nil {
		return pathfinderE
	}
	switch iface.Mode() {
	case interfaces.ModeAccessPoint:
		return apPathTime
	case interfaces.ModeRoaming:
		return roamingPathTime
	default:
		return pathfinderE
	}
}

// findInterfaceByHash is the Go port of Python Transport.find_interface_from_hash.
func (ts *TransportSystem) findInterfaceByHash(hash []byte) interfaces.Interface {
	if len(hash) == 0 {
		return nil
	}
	for _, iface := range ts.interfaces {
		if bytes.Equal(interfaceHash(iface), hash) {
			return iface
		}
	}
	return nil
}

// announceCacheDirFor returns the cache/announces directory that corresponds
// to a given storage path: ~/.reticulum/storage -> ~/.reticulum/cache/announces
// (the sibling cache dir Python's Transport.cache / get_cached_packet use).
// Returns "" when storagePath is unset.
func announceCacheDirFor(storagePath string) string {
	if storagePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(storagePath), "cache", "announces")
}

// announceCacheDir returns the cache/announces dir for this transport's own
// storage path.
func (ts *TransportSystem) announceCacheDir() string {
	return announceCacheDirFor(ts.storagePath)
}

// writeCachedAnnounce writes the raw announce + receiving-interface reference
// to <cacheDir>/<hex(packetHash)> as msgpack [raw, interface_reference],
// matching Python Transport.cache(packet, packet_type="announce"). Python's
// get_cached_packet reads this back to reconstruct the announce referenced by
// destination_table field [7]. cacheDir is computed by the caller so the
// cache files land next to whichever storagePath the table is being written to.
func writeCachedAnnounce(logger *Logger, cacheDir string, packetHash, raw []byte, iface interfaces.Interface) {
	if cacheDir == "" || len(packetHash) == 0 || len(raw) == 0 {
		return
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		logger.Error("Failed to create announce cache dir: %v", err)
		return
	}
	ifaceRef := ""
	if iface != nil {
		ifaceRef = fmt.Sprintf("%v[%v]", iface.Type(), iface.Name())
	}
	packed, err := msgpack.Pack([]any{raw, ifaceRef})
	if err != nil {
		logger.Error("Failed to pack cached announce: %v", err)
		return
	}
	path := filepath.Join(cacheDir, hex.EncodeToString(packetHash))
	if err := os.WriteFile(path, packed, 0o600); err != nil {
		logger.Error("Failed to write cached announce: %v", err)
	}
}

// cacheAnnounce writes a cached announce next to this transport's own storage
// path (used by the periodic persistPathTable, which always writes to
// ts.storagePath).
func (ts *TransportSystem) cacheAnnounce(packetHash, raw []byte, iface interfaces.Interface) {
	writeCachedAnnounce(ts.logger, ts.announceCacheDir(), packetHash, raw, iface)
}

// loadCachedAnnounce reads cache/announces/<hex(packetHash)> and returns the
// raw announce bytes plus the stored interface-reference string. ok is false
// when the file is absent or malformed (mirroring Python get_cached_packet
// returning None).
func (ts *TransportSystem) loadCachedAnnounce(packetHash []byte) (raw []byte, ifaceRef string, ok bool) {
	dir := ts.announceCacheDir()
	if dir == "" || len(packetHash) == 0 {
		return nil, "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, hex.EncodeToString(packetHash)))
	if err != nil {
		return nil, "", false
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return nil, "", false
	}
	arr, good := unpacked.([]any)
	if !good || len(arr) < 1 {
		return nil, "", false
	}
	raw, _ = arr[0].([]byte)
	if len(arr) >= 2 {
		ifaceRef, _ = arr[1].(string)
	}
	return raw, ifaceRef, true
}

// pathCacheItem pairs a cached announce's hash with its raw bytes and
// receiving interface, collected under the path-table lock and written to
// cache/announces after the lock is released.
type pathCacheItem struct {
	hash  []byte
	raw   []byte
	iface interfaces.Interface
}

// pathTableSnapshot builds the Python-compatible destination_table snapshot
// AND the set of announce cache files that must accompany it. Both
// persistPathTable and SavePathTable use it so the on-disk destination_table
// and cache/announces stay consistent.
//
// The serialised entry layout matches Python Transport.py:3390 exactly:
//
//	[destHash, timestamp(float s), next_hop, hops, expires(float s),
//	 random_blobs, interface_hash, packet_hash]
//
// Entries whose receiving interface is nil (no longer active) are skipped,
// matching Python's "interface no longer active" skip at Transport.py:3374.
// Entries without a cached announce packet / hash are skipped too, since
// Python's loader would drop them (get_cached_packet returns None).
//
// Unlike a single held-lock iteration, the destination hashes are snapshotted
// under the lock and then each entry is re-checked against the LIVE pathTable
// under a fresh short lock before serializing. This mirrors Python's
// "if not destination_hash in path_table: skip" intent at Transport.py:3370
// (a no-op there only because of local-variable shadowing): an entry removed
// concurrently mid-save is skipped instead of serializing stale data or
// panicking on a nil lookup. The interface hash is computed OUTSIDE the lock
// so a slow interface (or a test that synchronizes on Name) cannot block
// other writers.
func (ts *TransportSystem) pathTableSnapshot() (snapshot []any, caches []pathCacheItem) {
	ts.mu.Lock()
	ts.ensureStateLocked()
	hashes := make([]string, 0, len(ts.pathTable))
	for h := range ts.pathTable {
		hashes = append(hashes, h)
	}
	ts.mu.Unlock()

	snapshot = make([]any, 0, len(hashes))
	for _, destHash := range hashes {
		ts.mu.Lock()
		entry, ok := ts.pathTable[destHash]
		if !ok {
			// Disappeared between the key snapshot and serialization: skip
			// it rather than serializing a stale/nil entry.
			ts.mu.Unlock()
			continue
		}
		if entry.Interface == nil {
			ts.mu.Unlock()
			continue
		}
		packetHash := entry.PacketHash
		if len(packetHash) == 0 || len(entry.Packet) == 0 {
			ts.mu.Unlock()
			continue
		}
		// Capture every field we serialize under the lock so a concurrent
		// mutation of the entry after Unlock cannot tear the packed output.
		timestamp := entry.Timestamp
		nextHop := append([]byte(nil), entry.NextHop...)
		hops := entry.Hops
		expires := entry.Expires
		blobs := make([]any, len(entry.RandomBlobs))
		for i, b := range entry.RandomBlobs {
			blobs[i] = b
		}
		packetCopy := append([]byte(nil), entry.Packet...)
		packetHashCopy := append([]byte(nil), packetHash...)
		iface := entry.Interface
		ts.mu.Unlock()

		// interfaceHash may call the interface's Name()/Type(); compute it
		// outside the lock so it cannot block other table writers.
		ifaceHash := interfaceHash(iface)
		if ifaceHash == nil {
			continue
		}
		snapshot = append(snapshot, []any{
			[]byte(destHash),
			float64(timestamp.UnixNano()) / 1e9,
			nextHop,
			hops,
			float64(expires.UnixNano()) / 1e9,
			blobs,
			ifaceHash,
			packetHashCopy,
		})
		caches = append(caches, pathCacheItem{hash: packetHashCopy, raw: packetCopy, iface: iface})
	}
	return snapshot, caches
}

func (ts *TransportSystem) persistPathTable() {
	ts.mu.Lock()
	if ts.storagePath == "" || ts.connectedToSharedInstance {
		// A client of a shared Reticulum instance must not persist its path
		// table: it shares the storage path with the shared instance, so
		// writing would clobber the shared instance's destination_table with
		// the client's (forwarded-announce-only) view. Python never persists
		// for a connected-to-shared instance.
		ts.mu.Unlock()
		return
	}
	filePath := pathTableFile(ts.storagePath)
	ts.mu.Unlock()

	snapshot, caches := ts.pathTableSnapshot()

	packed, err := msgpack.Pack(snapshot)
	if err != nil {
		ts.logger.Error("Failed to pack path table for persistence: %v", err)
		return
	}

	tmp := filePath + ".out"
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		ts.logger.Error("Failed to create path table directory: %v", err)
		return
	}
	if err := os.WriteFile(tmp, packed, 0o600); err != nil {
		ts.logger.Error("Failed to write path table temp file: %v", err)
		return
	}
	if err := os.Rename(tmp, filePath); err != nil {
		ts.logger.Error("Failed to persist path table atomically: %v", err)
		return
	}
	// Write the announce cache files that accompany the destination_table so
	// Python's get_cached_packet (and Go's own reload) can recover the raw
	// announce referenced by each entry's packet_hash. See pathTableSnapshot.
	for _, c := range caches {
		ts.cacheAnnounce(c.hash, c.raw, c.iface)
	}
}

func (ts *TransportSystem) resolvePathInterfacesLocked() {
	for _, entry := range ts.pathTable {
		if entry.Interface != nil {
			entry.InterfaceName = entry.Interface.Name()
			entry.IfaceHash = interfaceHash(entry.Interface)
			continue
		}
		// Loaded entries carry IfaceHash (Python destination_table field [6])
		// but no live Interface, because Go loads the path table before network
		// interfaces are created (rns.go:316 vs :331). Reattach the live
		// interface by hash — the Python find_interface_from_hash equivalent —
		// each time an interface registers. This runs under RegisterInterface.
		if len(entry.IfaceHash) == 0 {
			continue
		}
		if iface := ts.findInterfaceByHash(entry.IfaceHash); iface != nil {
			entry.Interface = iface
			entry.InterfaceName = iface.Name()
		}
	}
}

// LoadPathTable loads the persisted destination_table from storage into the
// in-memory path table. It is the public, lock-acquiring entry point for the
// private loadPathTableLocked; NewReticulum calls it after Start(), gated on
// the instance role, so that a client of a shared Reticulum instance does not
// load the shared instance's path table (Python Transport.py:259).
func (ts *TransportSystem) LoadPathTable() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.loadPathTableLocked()
}

// SetConnectedToSharedInstance records whether this transport is a client of
// a co-located shared Reticulum instance (Python
// Reticulum.is_connected_to_shared_instance). When true the transport must not
// load or persist the path table (the shared instance owns it) and must not
// queue announce rebroadcasts (the shared instance is the only egress; a
// client rebroadcasting announces back to it would echo them around the
// network). NewReticulum sets this from startLocalInterface's outcome.
func (ts *TransportSystem) SetConnectedToSharedInstance(connected bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.connectedToSharedInstance = connected
}

// ConnectedToSharedInstance reports whether this transport is a client of a
// co-located shared Reticulum instance.
func (ts *TransportSystem) ConnectedToSharedInstance() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.connectedToSharedInstance
}

func (ts *TransportSystem) loadPathTableLocked() {
	ts.ensureStateLocked()
	if ts.storagePath == "" {
		return
	}
	filePath := pathTableFile(ts.storagePath)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if !os.IsNotExist(err) {
			ts.logger.Error("Failed reading path table from storage: %v", err)
		}
		return
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		ts.logger.Error("Failed unpacking path table from storage: %v", err)
		return
	}

	list, ok := unpacked.([]any)
	if !ok {
		ts.logger.Error("Invalid persisted path table format; expected list")
		return
	}

	ts.pathTable = make(map[string]*PathEntry, len(list))
	for _, rawEntry := range list {
		fields, ok := rawEntry.([]any)
		// Python's destination_table layout is an 8-field array:
		//   [destHash, timestamp, next_hop, hops, expires, blobs, iface_hash, packet_hash]
		// (Transport.py:3390). No back-compat with the old Go 7/8-field layout —
		// entries that do not match are skipped, matching Python dropping
		// unresolvable entries. Paths are re-learned from announces.
		if !ok || len(fields) < 8 {
			continue
		}

		destHash, ok1 := fields[0].([]byte)
		tsFloat, ok2 := anyToFloatSeconds(fields[1])
		nextHop, ok3 := fields[2].([]byte)
		hops64, ok4 := anyToInt64(fields[3])
		expFloat, ok5 := anyToFloatSeconds(fields[4])
		ifaceHash, ok6 := fields[6].([]byte)
		packetHash, ok7 := fields[7].([]byte)
		if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || !ok6 || !ok7 {
			continue
		}

		var blobs [][]byte
		if rawBlobs, isSlice := fields[5].([]any); isSlice {
			for _, rb := range rawBlobs {
				if b, bOk := rb.([]byte); bOk {
					blobs = append(blobs, copyBytes(b))
				}
			}
		}

		// Recover the raw announce from cache/announces/<hex(packetHash)>. If
		// the cache file is missing the entry cannot support cached path
		// responses, but the path itself is still usable for forwarding once
		// the interface reattaches, so keep the entry with a nil Packet (Python
		// drops it; Go keeps the path and re-learns the announce later).
		var packetRaw []byte
		if raw, _, cacheOK := ts.loadCachedAnnounce(packetHash); cacheOK {
			packetRaw = copyBytes(raw)
		}

		entry := &PathEntry{
			Timestamp:   floatToTime(tsFloat),
			NextHop:     copyBytes(nextHop),
			Hops:        int(hops64),
			Expires:     floatToTime(expFloat),
			RandomBlobs: blobs,
			IfaceHash:   copyBytes(ifaceHash),
			Packet:      packetRaw,
			PacketHash:  copyBytes(packetHash),
		}
		ts.pathTable[string(destHash)] = entry
	}

	ts.resolvePathInterfacesLocked()
	ts.logger.Verbose("Loaded %v path table entries from storage", len(ts.pathTable))
}

// extraLinkProofTimeout returns additional timeout based on interface bitrate
// to account for slow links, matching Python's Transport.extra_link_proof_timeout.
func (ts *TransportSystem) extraLinkProofTimeout(iface interfaces.Interface) time.Duration {
	if iface == nil {
		return 0
	}
	bitrate := iface.Bitrate()
	if bitrate <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) * (1.0 / float64(bitrate)) * 8.0 * float64(MTU))
}

// InvalidatePath removes a known path for a destination hash.
func (ts *TransportSystem) InvalidatePath(destHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	destinationHash := string(destHash)
	_, ok1 := ts.pathTable[destinationHash]
	if ok1 {
		delete(ts.pathTable, destinationHash)
	}
	_, ok2 := ts.knownDestinations[destinationHash]
	if ok2 {
		delete(ts.knownDestinations, destinationHash)
	}
	delete(ts.announceTable, destinationHash)
	delete(ts.pathRequests, destinationHash)
	return ok1 || ok2
}

// claimDownNotify reports whether the caller is the first to observe the given
// interface failing while it was up. It is the once-per-down-transition latch
// for the outbound fan-out paths (sendRebroadcast, dispatchForwardSend): the
// first failing send claims the latch and performs the log + path invalidation,
// while the concurrent queued sends that fail on the same now-dead connection
// suppress. Without this, a half-open TCP peer whose write deadline fires
// drains a burst of dozens of queued sends onto the closed socket, each running
// a full InvalidatePathsViaInterface scan under ts.mu — starving inbound
// link-handshake processing and timing out otherwise-healthy links. The latch
// is cleared by processAnnounceTable once the interface is back up, and by
// RegisterInterface.
func (ts *TransportSystem) claimDownNotify(iface interfaces.Interface) bool {
	ts.mu.Lock()
	if _, ok := ts.downNotified[iface]; ok {
		ts.mu.Unlock()
		return false
	}
	ts.downNotified[iface] = struct{}{}
	ts.mu.Unlock()
	return true
}

// InvalidatePathsViaInterface removes all known paths that route via an interface.
func (ts *TransportSystem) InvalidatePathsViaInterface(iface interfaces.Interface) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	removed := 0
	for destinationHash, entry := range ts.pathTable {
		if entry.Interface == iface {
			delete(ts.pathTable, destinationHash)
			delete(ts.knownDestinations, destinationHash)
			delete(ts.announceTable, destinationHash)
			delete(ts.pathRequests, destinationHash)
			removed++
		}
	}
	return removed
}

// InvalidatePathsViaNextHop removes all known paths with a matching next-hop.
func (ts *TransportSystem) InvalidatePathsViaNextHop(nextHop []byte) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	removed := 0
	for destinationHash, entry := range ts.pathTable {
		if bytes.Equal(entry.NextHop, nextHop) {
			delete(ts.pathTable, destinationHash)
			delete(ts.knownDestinations, destinationHash)
			delete(ts.announceTable, destinationHash)
			delete(ts.pathRequests, destinationHash)
			removed++
		}
	}
	return removed
}

func (ts *TransportSystem) randomDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return time.Duration(int64(b[0]) * int64(max) / 255)
}

func (ts *TransportSystem) announceQueueStateLocked(iface interfaces.Interface) *announceQueueState {
	state := ts.announceQueues[iface]
	if state == nil {
		state = &announceQueueState{}
		ts.announceQueues[iface] = state
	}
	return state
}

func announceWaitDuration(rawLen, bitrate int) time.Duration {
	if rawLen <= 0 || bitrate <= 0 {
		return 0
	}
	txTime := float64(rawLen*8) / float64(bitrate)
	wait := txTime / announceCapDefault
	return time.Duration(wait * float64(time.Second))
}

func announceEmissionFromPacket(packet *Packet) uint64 {
	if packet == nil {
		return 0
	}
	randomBlobStart := IdentityKeySize/8 + NameHashLength/8
	randomBlobEnd := randomBlobStart + 10
	if len(packet.Data) < randomBlobEnd {
		return 0
	}
	var emitted uint64
	for _, b := range packet.Data[randomBlobStart+5 : randomBlobEnd] {
		emitted = (emitted << 8) | uint64(b)
	}
	return emitted
}

// timebaseFromRandomBlobs returns the maximum announce-emission timebase
// across the given replay-protection random blobs. Each blob's timebase is
// its big-endian uint40 at bytes [5:10], the same field
// announceEmissionFromPacket reads. Mirrors Python
// Transport.timebase_from_random_blobs (RNS/Transport.py:3276-3281, v1.4.1).
func timebaseFromRandomBlobs(blobs [][]byte) uint64 {
	var timebase uint64
	for _, b := range blobs {
		if len(b) < 10 {
			continue
		}
		var emitted uint64
		for _, x := range b[5:10] {
			emitted = (emitted << 8) | uint64(x)
		}
		if emitted > timebase {
			timebase = emitted
		}
	}
	return timebase
}

func announceEmissionFromRaw(raw []byte) uint64 {
	packet := NewPacketFromRaw(raw)
	if err := packet.Unpack(); err != nil {
		return 0
	}
	return announceEmissionFromPacket(packet)
}

func (ts *TransportSystem) stopAnnounceQueueTimerLocked(state *announceQueueState) {
	if state == nil || state.timer == nil {
		return
	}
	state.timer.Stop()
	state.timer = nil
}

func (ts *TransportSystem) scheduleAnnounceQueueTimerLocked(iface interfaces.Interface, state *announceQueueState, delay time.Duration) {
	if state == nil {
		return
	}
	ts.stopAnnounceQueueTimerLocked(state)
	if delay < 0 {
		delay = 0
	}
	state.timer = time.AfterFunc(delay, func() {
		ts.processAnnounceQueue(iface, time.Now())
	})
}

func (ts *TransportSystem) queueOrSendAnnounceLocked(now time.Time, iface interfaces.Interface, destinationHash string, raw []byte, hops int) []byte {
	state := ts.announceQueueStateLocked(iface)
	queuedAnnounces := len(state.queue) > 0
	bitrate := iface.Bitrate()
	if bitrate <= 0 {
		return append([]byte(nil), raw...)
	}
	if !queuedAnnounces && now.After(state.allowedAt) {
		state.allowedAt = now.Add(announceWaitDuration(len(raw), bitrate))
		return append([]byte(nil), raw...)
	}
	if len(state.queue) >= maxQueuedAnnounces {
		return nil
	}

	emitted := announceEmissionFromRaw(raw)
	for i := range state.queue {
		if state.queue[i].destinationHash != destinationHash {
			continue
		}
		if emitted > state.queue[i].emitted {
			state.queue[i].queuedAt = now
			state.queue[i].hops = hops
			state.queue[i].emitted = emitted
			state.queue[i].raw = append(state.queue[i].raw[:0], raw...)
		}
		return nil
	}

	state.queue = append(state.queue, announceQueueEntry{
		destinationHash: destinationHash,
		queuedAt:        now,
		hops:            hops,
		emitted:         emitted,
		raw:             append([]byte(nil), raw...),
	})
	if !queuedAnnounces {
		delay := max(state.allowedAt.Sub(now), 0)
		ts.scheduleAnnounceQueueTimerLocked(iface, state, delay)
	}
	return nil
}

func (ts *TransportSystem) announceQueueLength(iface interfaces.Interface) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	state := ts.announceQueues[iface]
	if state == nil {
		return 0
	}
	return len(state.queue)
}

func (ts *TransportSystem) processAnnounceQueue(iface interfaces.Interface, now time.Time) {
	if iface == nil {
		return
	}

	var raw []byte
	var nextDelay time.Duration

	ts.mu.Lock()
	ts.ensureStateLocked()
	state := ts.announceQueues[iface]
	if state == nil {
		ts.mu.Unlock()
		return
	}
	state.timer = nil

	pruned := state.queue[:0]
	for _, entry := range state.queue {
		if now.Sub(entry.queuedAt) <= queuedAnnounceLife {
			pruned = append(pruned, entry)
		}
	}
	state.queue = pruned
	if len(state.queue) == 0 {
		ts.mu.Unlock()
		return
	}

	selectedIndex := 0
	for i := 1; i < len(state.queue); i++ {
		selected := state.queue[selectedIndex]
		candidate := state.queue[i]
		if candidate.hops < selected.hops || (candidate.hops == selected.hops && candidate.queuedAt.Before(selected.queuedAt)) {
			selectedIndex = i
		}
	}
	selected := state.queue[selectedIndex]
	raw = append([]byte(nil), selected.raw...)
	state.queue = append(state.queue[:selectedIndex], state.queue[selectedIndex+1:]...)
	nextDelay = announceWaitDuration(len(raw), iface.Bitrate())
	state.allowedAt = now.Add(nextDelay)
	if len(state.queue) > 0 {
		ts.scheduleAnnounceQueueTimerLocked(iface, state, nextDelay)
	}
	ts.mu.Unlock()

	ts.sendRebroadcast(iface, raw)
}

func (ts *TransportSystem) sendRebroadcast(iface interfaces.Interface, raw []byte) {
	if iface == nil || len(raw) == 0 {
		return
	}
	if ts.identity != nil {
		parsed := NewPacketFromRaw(raw)
		if err := parsed.Unpack(); err == nil && parsed.PacketType == PacketAnnounce {
			newFlags := byte((Header2 << 6) | (parsed.ContextFlag << 5) | (TransportForward << 4) | (parsed.DestinationType << 2) | parsed.PacketType)
			rebuilt := make([]byte, 0, 2+TruncatedHashLength/8+TruncatedHashLength/8+1+len(parsed.Data))
			rebuilt = append(rebuilt, newFlags, byte(parsed.Hops))
			rebuilt = append(rebuilt, ts.identity.Hash...)
			rebuilt = append(rebuilt, parsed.DestinationHash...)
			rebuilt = append(rebuilt, byte(parsed.Context))
			rebuilt = append(rebuilt, parsed.Data...)
			raw = rebuilt
		}
	}
	// Capture whether the interface was up before Send. A down interface
	// fast-fails Send with "is not running"; that is expected and must not
	// spam the log or trigger a full pathTable scan on every rebroadcast,
	// which the announce-queue timer would otherwise do every few
	// milliseconds for high-bitrate TCP interfaces. Only a real failure on
	// an interface that was up warrants logging and path invalidation.
	wasUp := iface.Status()
	if err := iface.Send(raw); err != nil {
		if wasUp && ts.claimDownNotify(iface) {
			ts.logger.Error("Failed to re-broadcast announce on %v: %v", iface.Name(), err)
			ts.InvalidatePathsViaInterface(iface)
		}
	}
}

// dispatchForwardSend sends one forwarded/rebroadcast frame to a single
// outbound interface, applying IFAC egress and invalidating paths on a real
// failure. It is meant to run in its own goroutine so a stalled peer — one
// whose conn.Write blocks until the per-interface write deadline fires —
// cannot block the caller.
//
// Both the transport maintenance loop and each interface readLoop fan out
// across multiple outbound interfaces. Doing those sends sequentially meant
// a single half-open TCP peer stalled every other outbound write (and, on a
// readLoop, every subsequent inbound packet) for the whole write-deadline
// window. Go gives us a goroutine per send for free; a stalled peer should
// only ever stall its own goroutine, never the loop that dispatched it.
func (ts *TransportSystem) dispatchForwardSend(iface interfaces.Interface, raw []byte, what string) {
	if ifac, ok := iface.(ifacOutboundHook); ok {
		processed, err := ifac.ApplyIFACOutbound(raw)
		if err != nil {
			ts.logger.Error("Failed IFAC egress for %v on %v: %v", what, iface.Name(), err)
			return
		}
		raw = processed
	}

	wasUp := iface.Status()
	if err := iface.Send(raw); err != nil {
		if wasUp && ts.claimDownNotify(iface) {
			ts.logger.Error("Failed %v on %v: %v", what, iface.Name(), err)
			ts.InvalidatePathsViaInterface(iface)
		}
	}
}

// WaitOutboundSends blocks until every outbound send dispatched on its own
// goroutine by processAnnounceTable/forwardPathRequest/
// forwardPathResponseToRequesters has completed. It is intended for tests
// that assert on the side effects of those fan-out paths, which dispatch
// non-blocking so a stalled peer cannot wedge the transport. Production
// code never calls this — waiting would re-introduce the stall.
func (ts *TransportSystem) WaitOutboundSends() {
	ts.outboundWG.Wait()
}

// announceTransmitDecision classifies how an announce rebroadcast should be
// handled on a candidate outbound interface. It mirrors the elif chain at
// RNS/Transport.py:1207-1290 (v1.4.1) that gates `should_transmit` for ANNOUNCE
// packets broadcast on all outgoing interfaces.
type announceTransmitDecision int

const (
	// announceBlock drops the rebroadcast on this interface entirely
	// (Python: should_transmit = False).
	announceBlock announceTransmitDecision = iota
	// announceDirect transmits immediately without announce-cap rate
	// limiting (Python: should_transmit stays True outside the else branch,
	// or packet.hops == 0 in the else branch).
	announceDirect
	// announceCapped transmits subject to the announce-cap rate limiter
	// (Python: the else branch with packet.hops > 0).
	announceCapped
)

// shouldTransmitAnnounce evaluates the announce-broadcast filter
// (RNS/Transport.py:1207-1290) for one candidate outbound interface.
// outIface is the interface being considered for transmission; fromIface is
// the next-hop interface the announce was received on (the path's interface,
// Python `Transport.next_hop_interface`); localDestination reports whether
// the announce's destination hash is a locally-registered IN destination
// (Python `Transport.destinations_map`); receivedHops is the announce's hop
// count as received (Python `packet.hops`, before the rebroadcast increment).
//
// The elif ordering is significant: the first matching branch wins, matching
// Python's elif chain exactly. Notably the MODE_ACCESS_POINT branch (B3) and
// the roaming/boundary branches (B5/B6) have no local-destination guard — a
// local destination is still subject to AP blocking and the roaming/boundary
// next-hop filters — while the MODE_INTERNAL outbound branch (B4) is guarded
// by `!localDestination`, so a local destination's announce onto an internal
// interface falls through to the else (announce-cap) branch.
func (ts *TransportSystem) shouldTransmitAnnounce(outIface, fromIface interfaces.Interface, localDestination bool, receivedHops int) announceTransmitDecision {
	switch {
	// B1: no next-hop interface exists for the destination.
	case !localDestination && fromIface == nil:
		return announceBlock
	// B2: announces_from_internal block — an internal-mode next-hop interface
	// is blocked when the outbound interface opts out of internal announces
	// (Task: announces_from_internal).
	case !localDestination && fromIface != nil && !outIface.AnnouncesFromInternal() && fromIface.Mode() == interfaces.ModeInternal:
		return announceBlock
	// B3: access-point interfaces never rebroadcast announces.
	case outIface.Mode() == interfaces.ModeAccessPoint:
		return announceBlock
	// B4: outbound interface is internal. Guarded by !localDestination: a
	// local destination's announce onto an internal interface falls through
	// to the else (announce-cap) branch.
	case !localDestination && outIface.Mode() == interfaces.ModeInternal:
		// fromIface is non-nil here: B1 already returned for !local && nil.
		if fromIface == nil {
			return announceBlock
		}
		// B4b: announces_to_internal allowance overrides the boundary block
		// (Task: announces_to_internal boundary→internal allowance).
		if ati := outIface.AnnouncesToInternal(); ati != nil && *ati {
			return announceDirect
		}
		// B4c: boundary-mode next-hop interface onto an internal outbound
		// is blocked (Task: MODE_INTERNAL announce-broadcast filter).
		if fromIface.Mode() == interfaces.ModeBoundary {
			return announceBlock
		}
		// B4d: any other next-hop mode (full/p2p/gateway/roaming/internal) is
		// allowed onto the internal outbound, without announce-cap.
		return announceDirect
	// B5: outbound interface is roaming. No local-destination guard.
	case outIface.Mode() == interfaces.ModeRoaming:
		if localDestination {
			// B5a: a local destination's announce is allowed onto roaming
			// (Task: local-destination rebroadcast allowance).
			return announceDirect
		}
		if fromIface == nil {
			return announceBlock
		}
		switch fromIface.Mode() {
		case interfaces.ModeRoaming:
			// B5c: roaming→roaming blocked.
			return announceBlock
		case interfaces.ModeBoundary:
			// B5d: boundary→roaming blocked.
			return announceBlock
		default:
			// B5e: internal/full/gateway/p2p next-hop allowed onto roaming.
			return announceDirect
		}
	// B6: outbound interface is boundary. No local-destination guard.
	case outIface.Mode() == interfaces.ModeBoundary:
		if localDestination {
			// B6a: a local destination's announce is allowed onto boundary.
			return announceDirect
		}
		if fromIface == nil {
			return announceBlock
		}
		if fromIface.Mode() == interfaces.ModeRoaming {
			// B6c: roaming→boundary blocked.
			return announceBlock
		}
		// B6d: boundary/internal/full/gateway/p2p next-hop allowed onto
		// boundary (boundary→boundary is allowed here).
		return announceDirect
	// B7: full/point-to-point/gateway outbound — announce-cap applies, but
	// only for forwarded announces (receivedHops > 0). A locally-originated
	// announce (receivedHops == 0) is sent immediately.
	default:
		if receivedHops > 0 {
			return announceCapped
		}
		return announceDirect
	}
}

// isLocalDestinationLocked reports whether destHash is a locally-registered
// IN destination (Python `Transport.destinations_map[destination_hash]`).
// Callers must hold ts.mu.
func (ts *TransportSystem) isLocalDestinationLocked(destHash []byte) bool {
	_, ok := ts.destinationsMap[string(destHash)]
	return ok
}

// localDestinationLocked returns the locally-registered IN destination for
// destHash, or nil if none matches. It is the O(1) hash lookup replacing the
// linear scan over destinations (Python Transport.destinations_map). Callers
// must hold ts.mu.
func (ts *TransportSystem) localDestinationLocked(destHash []byte) *Destination {
	return ts.destinationsMap[string(destHash)]
}

func (ts *TransportSystem) processAnnounceTable(now time.Time) {
	jobs := make([]outgoingAnnounceJob, 0)

	ts.mu.Lock()
	ts.ensureStateLocked()
	// Clear the down-notification latch for any interface that is back up,
	// so a future down transition can log + invalidate again. TCP client
	// interfaces reconnect without re-registering, so this observed-up sweep
	// (which already holds ts.mu and iterates interfaces below) is the up
	// signal for the latch.
	for _, ifc := range ts.interfaces {
		if ifc.Status() {
			delete(ts.downNotified, ifc)
		}
	}
	for destinationHash, entry := range ts.announceTable {
		if now.Before(entry.NextRebroadcastAt) {
			continue
		}

		if entry.Retries >= localRebroadcastsMax || entry.Retries > pathfinderRetries {
			delete(ts.announceTable, destinationHash)
			continue
		}

		// nil-guard while waiting for rebroadcast (Python Transport.py:611-615,
		// v1.2.5): if the destination's identity can no longer be recalled —
		// the known-destination was cleaned between the announce being queued
		// and this rebroadcast tick — complete the announce entry instead of
		// proceeding to rebuild/send with a nil identity. A locally-registered
		// destination still recalls via destinationsMap, so originator
		// announces are not affected.
		if ts.recallLocked([]byte(destinationHash), true) == nil {
			ts.logger.Pathing("Completed announce processing for %x, the path was cleaned while waiting for announce rebroadcast", []byte(destinationHash))
			delete(ts.announceTable, destinationHash)
			continue
		}

		// The announce's destination hash as bytes, for the local-destination
		// lookup (Python destinations_map). The map key is the string-cast
		// hash, so []byte(destinationHash) is the original destination hash.
		destHashBytes := []byte(destinationHash)
		localDest := ts.isLocalDestinationLocked(destHashBytes)
		// receivedHops is the announce's hop count as received (Python
		// packet.hops); entry.Hops is packet.Hops after inbound++ (matching
		// Python's announce_hops = packet.hops). The announce-cap branch (B7)
		// only rate-limits forwarded announces (receivedHops > 0); a
		// locally-originated announce (receivedHops == 0) is sent immediately.
		receivedHops := entry.Hops
		for _, outIface := range ts.interfaces {
			if outIface == entry.SourceInterface {
				continue
			}
			// Don't enqueue rebroadcasts for down interfaces. Send would
			// fast-fail "is not running", but the announce-queue timer fires
			// every few milliseconds for high-bitrate TCP interfaces, so
			// enqueueing anyway would churn ts.mu, log, and run a full
			// pathTable scan on every tick. Skipping here stops the
			// down-interface storm at its source; paths via a down interface
			// are invalidated when it transitions down (via failConn/Send
			// error), not lazily here.
			if !outIface.Status() {
				continue
			}
			// Python broadcasts only on `if interface.OUT:` interfaces.
			if !outIface.IsOut() {
				continue
			}
			switch ts.shouldTransmitAnnounce(outIface, entry.SourceInterface, localDest, receivedHops) {
			case announceBlock:
				continue
			case announceDirect:
				// Allowed without announce-cap (AP/internal/roaming/boundary
				// pass branches, or a locally-originated announce on a
				// full/p2p/gateway interface). Send immediately.
				jobs = append(jobs, outgoingAnnounceJob{iface: outIface, raw: copyBytes(entry.PacketRaw), hops: entry.Hops})
			case announceCapped:
				// Full/p2p/gateway outbound for a forwarded announce: apply the
				// announce-cap rate limiter (Python else branch).
				raw := ts.queueOrSendAnnounceLocked(now, outIface, destinationHash, entry.PacketRaw, entry.Hops)
				if len(raw) > 0 {
					jobs = append(jobs, outgoingAnnounceJob{iface: outIface, raw: raw, hops: entry.Hops})
				}
			}
		}

		entry.Retries++
		entry.NextRebroadcastAt = now.Add(pathfinderGrace + ts.randomDuration(pathfinderRandomWindow))
		if entry.Retries >= localRebroadcastsMax || entry.Retries > pathfinderRetries {
			delete(ts.announceTable, destinationHash)
		}
	}
	ts.mu.Unlock()

	// Dispatch the sorted batch on a single background goroutine.
	// handleOutgoingAnnounces sorts the batch by hops ascending before sending
	// (RNS/Transport.py:1065-1066: `for packet in sorted(outgoing, key=lambda
	// p: p.hops): packet.send()`). Running it in its own goroutine keeps the
	// maintenance loop responsive while preserving Python's sequential, sorted
	// send order; a stalled peer can only delay the rest of this batch, bounded
	// by each interface's write deadline, never the maintenance loop itself.
	ts.outboundWG.Go(func() { ts.handleOutgoingAnnounces(jobs) })
}

// outgoingAnnounceJob is a single queued announce rebroadcast bound for an
// outbound interface. hops is the rebroadcast hop count (entry.Hops, i.e.
// packet.Hops+1) used to sort the outgoing batch by ascending hop count.
type outgoingAnnounceJob struct {
	iface interfaces.Interface
	raw   []byte
	hops  int
}

// handleOutgoingAnnounces dispatches a batch of outgoing announce rebroadcasts
// in ascending hop order, mirroring RNS/Transport.py:1065-1066. Sorting by hops
// sends closer/fresher paths first so peers learn the best route sooner. The
// sends run sequentially within the caller's goroutine (the maintenance loop
// dispatches the whole batch on one background goroutine), matching Python's
// sequential sorted send.
func (ts *TransportSystem) handleOutgoingAnnounces(jobs []outgoingAnnounceJob) {
	// Sort the batch by ascending hop count before sending, mirroring
	// RNS/Transport.py:1065-1066. Python's sorted() is stable, so same-hop
	// announces keep their enqueue (map-iteration) order; sort.SliceStable
	// preserves that contract.
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].hops < jobs[j].hops })
	for _, job := range jobs {
		ts.sendRebroadcast(job.iface, job.raw)
	}
}

func (ts *TransportSystem) cullPathRequests(now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	for destinationHash, lastRequested := range ts.pathRequests {
		if now.Sub(lastRequested) > pathRequestCullAfter {
			delete(ts.pathRequests, destinationHash)
		}
	}
	for destinationHash, requestedAt := range ts.pendingPathRequestAt {
		if now.Sub(requestedAt) > pendingPathRequestTTL {
			delete(ts.pendingPathRequests, destinationHash)
			delete(ts.pendingPathRequestAt, destinationHash)
		}
	}
}

func (ts *TransportSystem) hasPendingPathRequesterLocked(destinationHash string, iface interfaces.Interface) bool {
	requesters := ts.pendingPathRequests[destinationHash]
	return slices.Contains(requesters, iface)
}

func (ts *TransportSystem) forwardPathRequest(packet *Packet, source interfaces.Interface) {
	if packet == nil || source == nil {
		return
	}
	if len(packet.Data) < TruncatedHashLength/8 {
		return
	}

	targetHash := copyBytes(packet.Data[:TruncatedHashLength/8])
	targetKey := string(targetHash)

	// PR ingress-limit gate (Python Transport.py:3005 + 3107-3110):
	// `should_ingress_limit = attached_interface.should_ingress_limit_pr()`
	// is computed on the receiving interface and aborts recursive
	// path-request discovery (the should_search_for_unknown branch) when
	// active. forwardPathRequest is the Go equivalent of that recursive
	// forward — it only runs when handlePathRequest could not answer the
	// request locally or from a cached path. Path requests originating
	// from a local client are forwarded unconditionally (Python's
	// is_from_local_client branch is not ingress-gated), so the carve-out
	// preserves that behavior.
	if !ts.isLocalClientInterface(source) && source.ShouldIngressLimitPr() {
		ts.logger.Debug("Not forwarding recursive path request for %x due to active PR ingress limiting on %v", targetHash, source.Name())
		return
	}

	// Compute the recursive-search decision and the optional boundary
	// search_mode_filter from the attached (receiving) interface, mirroring
	// RNS/Transport.py:3006-3011. The elif chain is order-sensitive:
	//   - recursive_prs enables recursive search regardless of mode and
	//     leaves the filter unset (Transport.py:3007-3008);
	//   - a mode in DISCOVER_PATHS_FOR enables recursive search with no
	//     filter (Transport.py:3008);
	//   - a boundary-mode attached interface enables recursive search and
	//     restricts egress to BOUNDARY_SEARCH_MODES = [boundary, gateway]
	//     (Transport.py:3009-3011).
	// The filter is applied in the forward loop below; an unset filter means
	// no mode restriction, preserving the existing forward-to-all behavior for
	// local-client and discover-mode sources.
	var searchModeFilter []int
	switch {
	case source.RecursivePrs():
		// should_search_for_unknown = true; search_mode_filter stays nil.
	case interfaces.ModeIn(source.Mode(), interfaces.DiscoverPathsFor):
		// should_search_for_unknown = true; search_mode_filter stays nil.
	case source.Mode() == interfaces.ModeBoundary:
		searchModeFilter = interfaces.BoundarySearchModes
	}

	ts.mu.Lock()
	ts.ensureStateLocked()
	if !ts.hasPendingPathRequesterLocked(targetKey, source) {
		ts.pendingPathRequests[targetKey] = append(ts.pendingPathRequests[targetKey], source)
	}
	ts.pendingPathRequestAt[targetKey] = time.Now()
	ts.mu.Unlock()

	pathReqDst, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		ts.logger.Error("Failed creating relay path request destination: %v", err)
		return
	}

	relayReq := NewPacket(pathReqDst, copyBytes(packet.Data))
	relayReq.TransportType = TransportBroadcast
	if err := relayReq.Pack(); err != nil {
		ts.logger.Error("Failed packing relay path request packet: %v", err)
		return
	}

	type sendJob struct {
		iface interfaces.Interface
		raw   []byte
	}
	jobs := make([]sendJob, 0)
	ts.mu.Lock()
	ts.ensureStateLocked()
	now := time.Now()
	for _, outIface := range ts.interfaces {
		if outIface == source {
			continue
		}
		// search_mode_filter (Python Transport.py:3124-3127): when the
		// attached interface is boundary-mode, recursive path requests only
		// egress on interfaces whose mode is in BOUNDARY_SEARCH_MODES. A nil
		// filter (recursive_prs, discover-mode, or local-client source) skips
		// this restriction.
		if len(searchModeFilter) > 0 && !interfaces.ModeIn(outIface.Mode(), searchModeFilter) {
			continue
		}
		if !outIface.Status() {
			continue
		}
		if state := ts.announceQueues[outIface]; state != nil {
			if len(state.queue) > 0 || now.Before(state.allowedAt) {
				continue
			}
		}
		// PR egress-limit gate (Python Transport.py:3131): skip interfaces
		// whose outgoing path-request frequency is above ec_pr_freq and has
		// accumulated enough samples to confidently report a burst.
		if outIface.ShouldEgressLimitPr() {
			ts.logger.Extreme("Not forwarding recursive path request on %v due to active PR egress limiting", outIface.Name())
			continue
		}
		raw := make([]byte, len(relayReq.Raw))
		copy(raw, relayReq.Raw)
		jobs = append(jobs, sendJob{iface: outIface, raw: raw})
	}
	ts.mu.Unlock()

	// Forward the path request to every other interface concurrently. This
	// runs on the readLoop of the interface that received the request; an
	// inline sequential send to a stalled peer would block that readLoop for
	// the write-deadline window and stall every subsequent inbound packet —
	// including any link handshake in flight. A goroutine per send keeps the
	// readLoop reading.
	for _, job := range jobs {
		ts.outboundWG.Go(func() { ts.dispatchForwardSend(job.iface, job.raw, "forwarding path request") })
	}
}

func (ts *TransportSystem) forwardPathResponseToRequesters(packet *Packet, source interfaces.Interface) bool {
	if packet == nil || source == nil {
		return false
	}
	destinationKey := string(packet.DestinationHash)

	type sendJob struct {
		iface interfaces.Interface
		raw   []byte
	}

	ts.mu.Lock()
	ts.ensureStateLocked()
	requesters := ts.pendingPathRequests[destinationKey]
	if len(requesters) == 0 {
		ts.mu.Unlock()
		return false
	}

	jobs := make([]sendJob, 0, len(requesters))
	for _, requesterIface := range requesters {
		if requesterIface == nil || requesterIface == source {
			continue
		}
		if !requesterIface.Status() {
			continue
		}
		raw := make([]byte, len(packet.Raw))
		copy(raw, packet.Raw)
		if len(raw) > 1 {
			raw[1] = byte(packet.Hops)
		}
		jobs = append(jobs, sendJob{iface: requesterIface, raw: raw})
	}
	delete(ts.pendingPathRequests, destinationKey)
	delete(ts.pendingPathRequestAt, destinationKey)
	ts.mu.Unlock()

	if len(jobs) == 0 {
		return false
	}

	// Dispatch each response send on its own goroutine (see forwardPathRequest
	// for why a readLoop must not block on a stalled outbound peer). The
	// caller ignores the return, so we report whether we dispatched to at
	// least one requester rather than waiting on the writes.
	for _, job := range jobs {
		ts.outboundWG.Go(func() { ts.dispatchForwardSend(job.iface, job.raw, "forwarding path response") })
	}

	return len(jobs) > 0
}

func (ts *TransportSystem) cullExpiredPaths(now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	for destinationHash, entry := range ts.pathTable {
		if !entry.Expires.IsZero() && now.After(entry.Expires) {
			delete(ts.pathTable, destinationHash)
			delete(ts.announceTable, destinationHash)
			delete(ts.pathRequests, destinationHash)
		}
	}
}

func (ts *TransportSystem) cullStaleTransportTables(now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	for packetHash, entry := range ts.reverseTable {
		if now.Sub(entry.Timestamp) > reverseEntryTimeout {
			delete(ts.reverseTable, packetHash)
		}
	}

	for linkID, entry := range ts.linkTable {
		if now.Sub(entry.Timestamp) > linkEntryTimeout {
			delete(ts.linkTable, linkID)
			continue
		}
		if !entry.ProofTimeout.IsZero() && now.After(entry.ProofTimeout) {
			delete(ts.linkTable, linkID)
		}
	}
}

func (ts *TransportSystem) seenOrRememberPacketHashLocked(packetHash []byte, now time.Time) bool {
	ts.ensureStateLocked()
	hashKey := string(packetHash)

	if _, exists := ts.packetHashes[hashKey]; exists {
		return true
	}
	if _, exists := ts.packetHashesPrev[hashKey]; exists {
		return true
	}

	if len(ts.packetHashes) >= ts.packetHashRotateAt {
		ts.packetHashesPrev = ts.packetHashes
		ts.packetHashes = make(map[string]time.Time, ts.packetHashRotateAt)
	}

	ts.packetHashes[hashKey] = now
	return false
}

// handlePathRequest processes a path request received for the
// rnstransport.path.request destination. It returns true when the request was
// answered (or intentionally consumed) by this node, in which case the caller
// must NOT relay the request onward — matching Python Reticulum's elif chain
// in Transport.path_request (local answer, cached-path answer, then forward).
// It returns false when the request is unknown here and should be relayed.
func (ts *TransportSystem) handlePathRequest(data []byte, packet *Packet) bool {
	if len(data) < TruncatedHashLength/8 {
		return false
	}

	targetHash := data[:TruncatedHashLength/8]
	ts.logger.Debug("Path request for %x", targetHash)

	// The requestor's transport-instance ID (when present) follows the
	// requested destination hash. It is used for loop prevention when
	// answering from a cached path: if the requestor IS the next hop toward
	// the destination, replying would echo the announce back along the path
	// it already traveled. Mirrors Python's
	// "if requestor_transport_id != None and next_hop == requestor_transport_id".
	hashLen := TruncatedHashLength / 8
	var requestorTransportID []byte
	if len(data) > hashLen {
		requestorTransportID = data[hashLen:min(len(data), 2*hashLen)]
	}

	ts.mu.Lock()
	localDest := ts.localDestinationLocked(targetHash)
	// If the destination is not local to this node, look for a cached/known
	// path. A transport node that already knows a path answers the request
	// from cache instead of relaying it onward, so the requestor gets the
	// path from the nearest node that has it (and does not have to wait for
	// the remote node's own announce interval). This is the branch Python
	// Reticulum implements as
	//   "elif (transport_enabled or is_from_local_client) and
	//    (destination_hash in Transport.path_table)"
	// go-reticulum previously only answered for local destinations and
	// otherwise relied on a full round-trip to the remote node, which times
	// out for sparsely-announcing destinations (e.g. a nomadnet node
	// announcing every 60 minutes requested by a freshly-started client).
	var cachedPath *PathEntry
	if localDest == nil && !ts.connectedToSharedInstance {
		cachedPath = ts.pathTable[string(targetHash)]
		// When the requested destination's known path lives on a local-client
		// interface, register it as in-use: a co-located client owns the
		// destination and just had its path requested. Mirrors Python
		// Transport.py:3018-3026 (`destination_exists_on_local_client` →
		// `_used_destination_data`), which runs before the local-destination
		// and cached-path answer branches. The lock is held here, so the
		// lock-held core is safe.
		if cachedPath != nil && ts.isLocalClientInterface(cachedPath.Interface) {
			ts.usedDestinationDataLocked(targetHash)
		}
	}
	ts.mu.Unlock()

	if localDest != nil {
		ts.logger.Debug("Answering path request for %x, destination is local", targetHash)
		// Extract tag if present
		var tag []byte
		if len(data) > (TruncatedHashLength/8)*2 {
			tag = data[(TruncatedHashLength/8)*2:]
		} else if len(data) > TruncatedHashLength/8 {
			tag = data[TruncatedHashLength/8:]
		}
		if len(tag) > TruncatedHashLength/8 {
			tag = tag[:TruncatedHashLength/8]
		}

		announcePacket, err := localDest.buildAnnouncePacket(tag)
		if err != nil {
			ts.logger.Error("Failed to build path response announce: %v", err)
			return true
		}

		announcePacket.Context = ContextPathResponse
		announcePacket.HeaderType = Header2
		announcePacket.TransportType = TransportForward
		if ts.identity != nil {
			announcePacket.TransportID = copyBytes(ts.identity.Hash)
		}

		if err := announcePacket.Pack(); err != nil {
			ts.logger.Error("Failed to pack path response announce: %v", err)
			return true
		}

		if packet != nil && packet.ReceivingInterface != nil {
			raw := announcePacket.Raw
			if ifac, ok := packet.ReceivingInterface.(ifacOutboundHook); ok {
				processed, err := ifac.ApplyIFACOutbound(raw)
				if err != nil {
					ts.logger.Error("Failed IFAC egress for path response on %v: %v", packet.ReceivingInterface.Name(), err)
					return true
				}
				raw = processed
			}

			if err := packet.ReceivingInterface.Send(raw); err != nil {
				ts.logger.Error("Failed to send path response announce on %v: %v", packet.ReceivingInterface.Name(), err)
				return true
			}
			return true
		}

		if err := ts.Outbound(announcePacket); err != nil {
			ts.logger.Error("Failed broadcasting fallback path response announce: %v", err)
		}
		return true
	}

	// Answer from a cached path: the destination is remote but this node
	// knows a path to it. Replay the cached announce as a path response back
	// on the interface the request arrived on; intermediate nodes forward it
	// toward the requestor via the pending-path-request machinery.
	if cachedPath != nil && len(cachedPath.Packet) > 0 && ts.identity != nil &&
		packet != nil && packet.ReceivingInterface != nil {

		// Loop prevention: the next hop toward the destination is the
		// requestor itself. Replying would send the announce back along the
		// path it arrived on. Consume the request without flooding onward.
		if requestorTransportID != nil && len(cachedPath.NextHop) == hashLen &&
			bytes.Equal(cachedPath.NextHop, requestorTransportID) {
			ts.logger.Debug("Not answering path request for %x from cache: requestor is next hop", targetHash)
			return true
		}

		// Recover the announce payload (Data) and header flags
		// (ContextFlag/ratchet presence, DestinationType) from the cached
		// raw announce. The announce signature covers only
		// destination_hash+pubkey+name_hash+random_hash+ratchet+app_data,
		// NOT the header, so rebuilding the header with this node as the
		// next hop and a PathResponse context preserves signature validity.
		cached := &Packet{Raw: append([]byte(nil), cachedPath.Packet...)}
		if err := cached.Unpack(); err != nil {
			ts.logger.Debug("Cached path-response replay: unpack failed for %x: %v", targetHash, err)
			return true
		}
		if !bytes.Equal(cached.DestinationHash, targetHash) {
			ts.logger.Debug("Cached path-response: dest hash mismatch for %x, skipping", targetHash)
			return true
		}

		flags := byte((Header2 << 6) | (cached.ContextFlag << 5) |
			(TransportForward << 4) | (cached.DestinationType << 2) | PacketAnnounce)
		raw := make([]byte, 0, 2+2*hashLen+1+len(cached.Data))
		raw = append(raw, flags)
		raw = append(raw, byte(cachedPath.Hops)) // Python: packet.hops = path_table[IDX_PT_HOPS]
		raw = append(raw, ts.identity.Hash...)   // TransportID = this node (next hop)
		raw = append(raw, targetHash...)         // DestinationHash
		raw = append(raw, byte(ContextPathResponse))
		raw = append(raw, cached.Data...) // announce payload + signature (unchanged)

		ts.logger.Debug("Answering path request for %x from cached path (%v hops)", targetHash, cachedPath.Hops)

		// Answering a path request from a known/cached path is an active use
		// of the destination: mark it in-use, mirroring Python
		// Transport.py:3097 (`if not is_connected_to_shared_instance:
		// _used_destination_data` after the announce_table insertion in the
		// known-path answer branch). cachedPath is only populated when not
		// connected to a shared instance (see the lookup above), so the
		// shared-instance guard is implicitly satisfied. The lock is not
		// held here, so use the self-locking public method.
		ts.UsedDestinationData(targetHash)

		if ifac, ok := packet.ReceivingInterface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err != nil {
				ts.logger.Error("Failed IFAC egress for cached path response on %v: %v", packet.ReceivingInterface.Name(), err)
				return true
			}
			raw = processed
		}
		if err := packet.ReceivingInterface.Send(raw); err != nil {
			ts.logger.Error("Failed to send cached path response announce on %v: %v", packet.ReceivingInterface.Name(), err)
		}
		return true
	}

	return false
}

// RegisterDestination adds a destination to the transport system.
func (ts *TransportSystem) RegisterDestination(d *Destination) {
	if d.direction == DestinationIn {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		if _, exists := ts.destinationsMap[string(d.Hash)]; exists {
			ts.logger.Error("Attempt to register an already registered destination %x", d.Hash)
			return
		}
		ts.destinations = append(ts.destinations, d)
		ts.destinationsMap[string(d.Hash)] = d
	}
}

// RegisterLink adds a link to the transport system as pending.
func (ts *TransportSystem) RegisterLink(l *Link) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.pendingLinks = append(ts.pendingLinks, l)
}

// ActivateLink transitions a link from pending to active.
func (ts *TransportSystem) ActivateLink(l *Link) {
	ts.logger.Debug("Go Transport.ActivateLink(%x)", l.linkID)
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Find in pending
	for i, pl := range ts.pendingLinks {
		if pl == l {
			// Remove from pending
			ts.pendingLinks = append(ts.pendingLinks[:i], ts.pendingLinks[i+1:]...)
			// Add to active
			ts.activeLinks = append(ts.activeLinks, l)
			ts.logger.Verbose("Activated link %x", l.linkID)
			return
		}
	}
	ts.logger.Error("Attempted to activate a link %x that was not in the pending table", l.linkID)
}

// FindLink finds a link by its ID.
func (ts *TransportSystem) FindLink(linkID []byte) *Link {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, l := range ts.activeLinks {
		if bytes.Equal(l.linkID, linkID) {
			return l
		}
	}
	for _, l := range ts.pendingLinks {
		if bytes.Equal(l.linkID, linkID) {
			return l
		}
	}
	return nil
}

// deliverLinkProof applies the expected-hops gate and optional path re-balance
// to an LRPROOF destined for the given local link, then delivers it for final
// validation when the hop count matches (after any re-balance). It mirrors the
// Python Transport LRPROOF handler's local pending-link case
// (Transport.py:2272-2312):
//
//	if packet.hops != link.expected_hops and link.status == PENDING and ALLOW_LINK_PATH_REBALANCE:
//	    # re-validate signature; on success adopt packet.hops + rewrite path table hops
//	if packet.hops == link.expected_hops:
//	    pending_link = link  # delivered to validate_proof
//
// In the common direct-hop case the initiator records expectedHops =
// hops_to(destination) which is PathfinderM when the path is unknown, so the
// first proof always mismatches and re-balances down to the proof's hop count.
// A proof whose hops mismatch and whose signature does not verify (or when
// re-balance is disabled) is rejected — the link is not delivered the proof.
func (ts *TransportSystem) deliverLinkProof(l *Link, packet *Packet) {
	if l.GetStatus() != LinkPending {
		ts.logger.Debug("Inbound: ignoring LRPROOF %x for link %x: not pending (status=%v)", packet.PacketHash, l.linkID, l.GetStatus())
		return
	}
	expected := l.ExpectedHops()
	if packet.Hops != expected {
		// Hop mismatch: attempt a path re-balance by re-validating the
		// proof signature (Transport.py:2283-2307). A valid signature
		// authorizes adopting the proof's hop count.
		if ts.AllowLinkPathRebalance() && l.verifyProofSignature(packet) {
			l.SetExpectedHops(packet.Hops)
			// Record the re-balance timestamp the first time only, mirroring
			// Python's `if not link.rebalanced: link.rebalanced = time.time()`
			// guard (Transport.py:2298-2300).
			l.MarkRebalanced(time.Now())
			if l.destination != nil {
				ts.mu.Lock()
				if entry, ok := ts.pathTable[string(l.destination.Hash)]; ok {
					entry.Hops = packet.Hops
					ts.logger.Debug("Inbound: re-balanced path table hops for %x to %v", l.destination.Hash, packet.Hops)
				}
				ts.mu.Unlock()
			}
			ts.logger.Debug("Inbound: re-balanced link %x expected hops %v -> %v", packet.DestinationHash, expected, packet.Hops)
		} else {
			ts.logger.Debug("Inbound: rejecting link proof %x for link %x: hop mismatch (%v != expected %v)", packet.PacketHash, packet.DestinationHash, packet.Hops, expected)
			return
		}
	}
	// After a successful re-balance expectedHops == packet.Hops, so this
	// delivers the proof to the link for full validation (Transport.py:2310).
	if packet.Hops != l.ExpectedHops() {
		return
	}
	packet.Destination = l
	l.receive(packet)
}

// validateRelayLinkProofLocked validates an LRPROOF signature on behalf of a
// relay that is forwarding the proof for a remote link, using the identity
// recalled from the link's destination hash. It mirrors the signature check
// Python performs in both the relay re-balance (Transport.py:2225-2232) and
// the relay forward (Transport.py:2251-2258):
//
//	peer_identity = RNS.Identity.recall(link_entry[IDX_LT_DSTHASH])
//	signed_data = packet.destination_hash + peer_pub_bytes + peer_sig_pub_bytes + signalling_bytes
//	peer_identity.validate(signature, signed_data)
//
// The caller must hold ts.mu (for recallLocked).
func (ts *TransportSystem) validateRelayLinkProofLocked(packet *Packet, destHash []byte) bool {
	if len(packet.Data) < 64+32 {
		return false
	}
	peerIdentity := ts.recallLocked(destHash, true)
	if peerIdentity == nil {
		return false
	}
	signature := packet.Data[:64]
	peerPubBytes := packet.Data[64:96]
	var sigBytes []byte
	if len(packet.Data) == 64+32+LinkMTUSize {
		sigBytes = packet.Data[96 : 96+LinkMTUSize]
	}
	peerSigPubBytes := peerIdentity.GetPublicKey()[32:64]
	signedData := make([]byte, 0, len(packet.DestinationHash)+len(peerPubBytes)+len(peerSigPubBytes)+len(sigBytes))
	signedData = append(signedData, packet.DestinationHash...)
	signedData = append(signedData, peerPubBytes...)
	signedData = append(signedData, peerSigPubBytes...)
	signedData = append(signedData, sigBytes...)
	return peerIdentity.Verify(signature, signedData)
}

// relayLinkProof handles an LRPROOF that this node is transporting for a
// remote link (a link_table entry exists for the proof's link ID). It applies
// the hop-mismatch path re-balance and signature-validated forward from
// RNS/Transport.py:2207-2265:
//
//	if packet.hops != link_entry[IDX_LT_REM_HOPS] and ALLOW_LINK_PATH_REBALANCE:
//	    if receiving_interface == link_entry[IDX_LT_NH_IF] and signature valid and not validated:
//	        link_entry[IDX_LT_REM_HOPS] = packet.hops; path_table[dst][IDX_PT_HOPS] = packet.hops
//	if packet.hops == link_entry[IDX_LT_REM_HOPS] and receiving_interface == link_entry[IDX_LT_NH_IF]:
//	    if signature valid: mark validated; transmit on link_entry[IDX_LT_RCVD_IF]
//
// It returns true when the proof was handled as a relayed proof (whether
// forwarded or dropped for a hop mismatch / bad signature), and false when no
// link_table entry exists so the caller can fall through to local-link delivery.
func (ts *TransportSystem) relayLinkProof(packet *Packet, iface interfaces.Interface) bool {
	ts.mu.Lock()
	entry, ok := ts.linkTable[string(packet.DestinationHash)]
	if !ok || entry == nil {
		ts.mu.Unlock()
		return false
	}
	// The proof must arrive on the outbound interface (the interface the
	// link request was forwarded out on, toward the receiver), matching
	// Python's `packet.receiving_interface == link_entry[IDX_LT_NH_IF]`.
	if iface != entry.OutboundInterface {
		ts.mu.Unlock()
		ts.logger.Debug("Inbound: link proof %x received on wrong interface, not transporting", packet.PacketHash)
		return true
	}
	// Re-balance: a hop mismatch vs the link-table entry means the proof
	// found a different-length path than the request. Re-validate the
	// signature and adopt the proof's hop count (Transport.py:2211-2236).
	if packet.Hops != entry.RemainingHops && ts.allowLinkPathRebalance && !entry.Validated {
		if ts.validateRelayLinkProofLocked(packet, entry.DestinationHash) {
			ts.logger.Debug("Inbound: re-balancing link %x remaining hops %v -> %v", packet.DestinationHash, entry.RemainingHops, packet.Hops)
			entry.RemainingHops = packet.Hops
			if pathEntry, ok := ts.pathTable[string(entry.DestinationHash)]; ok && pathEntry != nil {
				pathEntry.Hops = packet.Hops
				ts.logger.Debug("Inbound: re-balanced path table hops for %x to %v", entry.DestinationHash, packet.Hops)
			}
		} else {
			ts.mu.Unlock()
			ts.logger.Debug("Inbound: aborting link proof re-balance for %x: invalid signature", packet.DestinationHash)
			return true
		}
	}
	// Forward: only transport the proof when the hop count now matches the
	// link-table entry (Transport.py:2238-2265) and the signature validates.
	if packet.Hops != entry.RemainingHops {
		ts.mu.Unlock()
		ts.logger.Debug("Inbound: link proof %x hop mismatch (%v/%v), not transporting", packet.PacketHash, packet.Hops, entry.RemainingHops)
		return true
	}
	if !ts.validateRelayLinkProofLocked(packet, entry.DestinationHash) {
		ts.mu.Unlock()
		ts.logger.Debug("Inbound: invalid link proof signature for %x, dropping", packet.DestinationHash)
		return true
	}
	newRaw := make([]byte, len(packet.Raw))
	copy(newRaw, packet.Raw)
	newRaw[1] = byte(packet.Hops)
	entry.Validated = true
	// A validated link-request proof is an active use of the proof's
	// destination: mark it in-use, mirroring Python Transport.py:2263
	// (`if not Transport.owner.is_connected_to_shared_instance:
	// RNS.Identity._used_destination_data(link_entry[IDX_LT_DSTHASH])` after
	// `Transport.transmit` of the validated proof). The lock is held here,
	// so the lock-held core is safe.
	if !ts.connectedToSharedInstance {
		ts.usedDestinationDataLocked(entry.DestinationHash)
	}
	rcvdIface := entry.ReceivedInterface
	ts.mu.Unlock()
	ts.logger.Debug("Inbound: forwarding validated link proof %x", packet.PacketHash)
	if err := rcvdIface.Send(newRaw); err != nil {
		ts.logger.Error("Failed to forward link proof: %v", err)
	}
	return true
}

// RegisterInterface adds a network interface to the transport system. It is
// the canonical add path (Python Transport.add_interface, Transport.py:438-441):
// a repeated registration of the same interface is a no-op so the interface
// appears at most once in the transport's interface list.
func (ts *TransportSystem) RegisterInterface(iface interfaces.Interface) {
	ts.mu.Lock()
	if slices.Contains(ts.interfaces, iface) {
		// Already registered: skip, matching Python's
		// "if not interface in Transport.interfaces: append".
		ts.mu.Unlock()
		return
	}
	interfacesBefore := len(ts.interfaces)
	destinationsBefore := len(ts.destinations)
	ts.interfaces = append(ts.interfaces, iface)
	ts.resolvePathInterfacesLocked()
	// A freshly registered interface is up; clear any stale down-notify
	// latch so a future down transition can log + invalidate.
	delete(ts.downNotified, iface)

	destinationsToAnnounce := make([]*Destination, len(ts.destinations))
	copy(destinationsToAnnounce, ts.destinations)
	interfacesAfter := len(ts.interfaces)
	ts.mu.Unlock()
	ts.logger.Debug("[Transport] RegisterInterface: %s, interfaces before: %d, destinations: %d", iface.Name(), interfacesBefore, destinationsBefore)
	ts.logger.Debug("[Transport] RegisterInterface: %s, interfaces after: %d, will announce %d destinations", iface.Name(), interfacesAfter, len(destinationsToAnnounce))

	// For TCP client interfaces, eagerly invalidate paths routed through
	// the interface when its connection fails, so pathfinding re-routes
	// without waiting for stale paths to expire. The hook fires once per
	// up->down transition (inside failConn) instead of on every failed
	// rebroadcast/forward Send as the old lazy path did.
	//
	// Also wire an onConnect hook that re-announces the local destinations
	// each time the client (re)connects. A TCP client is registered (and
	// announced) even when its initial connect is refused, so that first
	// announce is sent to a dead socket and lost; without re-announcing on
	// reconnect the peer never learns this node's destinations until the
	// periodic announce interval (minutes) fires. The hook is a no-op for
	// spawned (server-accepted) clients, which never run reconnectLoop.
	if tci, ok := iface.(*interfaces.TCPClientInterface); ok {
		tci.SetOnDown(func() { ts.InvalidatePathsViaInterface(tci) })
		tci.SetOnConnect(func() { ts.announceDestinationsOnInterface(tci, nil) })
	}

	// Start inbound processor for this interface
	if reader, ok := iface.(interface {
		Read() ([]byte, error)
	}); ok {
		go func() {
			for {
				data, err := reader.Read()
				if err != nil {
					return
				}
				ts.Inbound(data, iface)
			}
		}()
	}

	ts.announceDestinationsOnInterface(iface, destinationsToAnnounce)
}

// announceDestinationsOnInterface re-announces the local single destinations
// over the given interface. It is called by RegisterInterface when an interface
// is freshly registered, and by a TCP client interface's onConnect hook after
// it (re)connects — so a TCP client that registered while disconnected does not
// silently lose its announce to the peer until the periodic interval. When
// alreadyAnnounced is non-nil it is used as the destination set (the snapshot
// taken under the transport lock in RegisterInterface); otherwise the current
// destinations are snapshotted here, so a reconnect re-announces any
// destinations added since the interface was first registered.
func (ts *TransportSystem) announceDestinationsOnInterface(iface interfaces.Interface, alreadyAnnounced []*Destination) {
	destinations := alreadyAnnounced
	if destinations == nil {
		ts.mu.Lock()
		destinations = make([]*Destination, len(ts.destinations))
		copy(destinations, ts.destinations)
		ts.mu.Unlock()
	}
	for _, d := range destinations {
		if d.direction == DestinationIn && d.Type == DestinationSingle {
			ts.logger.Debug("[Transport] Re-announcing destination %x on new interface %v", d.Hash, iface.Name())
			if err := d.Announce(nil); err != nil {
				ts.logger.Debug("Failed to re-announce destination %x on new interface %v: %v", d.Hash, iface.Name(), err)
			} else {
				ts.logger.Debug("[Transport] Re-announce of %x on %v completed", d.Hash, iface.Name())
			}
		}
	}
}

// GetInterfaces returns the list of network interfaces.
func (ts *TransportSystem) GetInterfaces() []interfaces.Interface {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]interfaces.Interface(nil), ts.interfaces...)
}

// RemoveInterface removes a previously registered interface from the transport.
func (ts *TransportSystem) RemoveInterface(iface interfaces.Interface) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, existing := range ts.interfaces {
		if existing == iface {
			ts.interfaces = append(ts.interfaces[:i], ts.interfaces[i+1:]...)
			ts.resolvePathInterfacesLocked()
			return
		}
	}
}

// PathInfo represents a flattened path table entry.
type PathInfo struct {
	Timestamp time.Time
	Hash      []byte
	NextHop   []byte
	Hops      int
	Interface interfaces.Interface
	Expires   time.Time
}

// GetPathTable returns a snapshot of the current path table.
func (ts *TransportSystem) GetPathTable() []PathInfo {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	table := make([]PathInfo, 0, len(ts.pathTable))
	for h, e := range ts.pathTable {
		table = append(table, PathInfo{
			Timestamp: e.Timestamp,
			Hash:      []byte(h),
			NextHop:   e.NextHop,
			Hops:      e.Hops,
			Interface: e.Interface,
			Expires:   e.Expires,
		})
	}
	return table
}

// GetPathEntry returns path info for a specific destination.
func (ts *TransportSystem) GetPathEntry(destHash []byte) *PathInfo {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if e, ok := ts.pathTable[string(destHash)]; ok {
		return &PathInfo{
			Timestamp: e.Timestamp,
			Hash:      destHash,
			NextHop:   e.NextHop,
			Hops:      e.Hops,
			Interface: e.Interface,
			Expires:   e.Expires,
		}
	}
	return nil
}

// GetRateTable returns a snapshot of observed announce-rate state.
func (ts *TransportSystem) GetRateTable() []map[string]any {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	out := make([]map[string]any, 0, len(ts.announceRateTable))
	for hash, entry := range ts.announceRateTable {
		timestamps := make([]float64, 0, len(entry.Timestamps))
		for _, ts := range entry.Timestamps {
			timestamps = append(timestamps, float64(ts.UnixNano())/1e9)
		}
		out = append(out, map[string]any{
			"hash":            []byte(hash),
			"last":            float64(entry.Last.UnixNano()) / 1e9,
			"rate_violations": entry.RateViolations,
			"blocked_until":   float64(entry.BlockedUntil.UnixNano()) / 1e9,
			"timestamps":      timestamps,
		})
	}
	return out
}

// GetPacketRSSI returns Received Signal Strength Indicator (RSSI) metadata for
// a packet hash when available.
func (ts *TransportSystem) GetPacketRSSI(packetHash []byte) (float64, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	v, ok := ts.packetRSSICache[string(packetHash)]
	return v, ok
}

// CacheRequest asks the network to re-send a packet by hash (Python
// RNS.Transport.cache_request, Transport.py). The Go port does not implement
// the network-wide packet cache, so this is a faithful best-effort no-op that
// records nothing; the resource AWAITING_PROOF watchdog still issues it so
// the call site and retry behaviour match Python.
func (ts *TransportSystem) CacheRequest(packetHash []byte, link *Link) {
	// No network packet cache in the Go port; intentionally a no-op.
	_ = packetHash
	_ = link
}

// GetPacketSNR returns Signal-to-Noise Ratio (SNR) metadata for a packet hash
// when available.
func (ts *TransportSystem) GetPacketSNR(packetHash []byte) (float64, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	v, ok := ts.packetSNRCache[string(packetHash)]
	return v, ok
}

// GetPacketQ returns quality metadata for a packet hash when available.
func (ts *TransportSystem) GetPacketQ(packetHash []byte) (float64, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	v, ok := ts.packetQCache[string(packetHash)]
	return v, ok
}

// DropAnnounceQueues clears transport announce rebroadcast and pending path-forward queues.
func (ts *TransportSystem) DropAnnounceQueues() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	count := len(ts.announceTable)
	for k := range ts.announceTable {
		delete(ts.announceTable, k)
	}
	for _, state := range ts.announceQueues {
		ts.stopAnnounceQueueTimerLocked(state)
		if state != nil {
			state.queue = nil
		}
	}
	for k := range ts.pendingPathRequests {
		delete(ts.pendingPathRequests, k)
	}
	for k := range ts.pendingPathRequestAt {
		delete(ts.pendingPathRequestAt, k)
	}
	return count
}

// BlackholeIdentity stores an identity hash in the local blackhole registry.
func (ts *TransportSystem) BlackholeIdentity(identityHash []byte, until *int64, reason string) bool {
	if len(identityHash) == 0 {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	entry := BlackholeIdentityEntry{
		IdentityHash: copyBytes(identityHash),
		Source:       copyBytes(ts.identityHash()),
		Reason:       reason,
	}
	if until != nil && *until > 0 {
		t := time.Unix(*until, 0)
		entry.Until = &t
	}
	ts.blackholedIdentities[string(identityHash)] = entry
	return true
}

// Remember caches a newly discovered identity and its associated routing context in local ephemeral or persistent storage.
//
// The entry layout is the 5-element list [timestamp, packet_hash, public_key,
// app_data, use_timestamp] (Python Identity.known_destinations,
// RNS/Identity.py:107, v1.3.0). The 5th use_timestamp element is 0 when the
// destination has never been used, -1 when retained, or the Unix time of last
// use. A re-Remember of an existing destination updates elements 0-3 but
// preserves the 5th so a fresh announce does not reset the use/retain state
// (Python Identity.remember, RNS/Identity.py:108-113).
func (ts *TransportSystem) Remember(packetHash, destHash, publicKey, appData []byte) {
	ts.mu.Lock()
	var useTimestamp any = int64(0)
	if existing, ok := ts.knownDestinations[string(destHash)]; ok && len(existing) > 4 {
		useTimestamp = existing[4]
	}
	ts.knownDestinations[string(destHash)] = []any{
		float64(time.Now().UnixNano()) / 1e9,
		packetHash,
		publicKey,
		appData,
		useTimestamp,
	}
	// Don't serialize+write per announce: mark the table dirty and let the
	// maintenance loop's periodic flush coalesce a burst of announces into one
	// save. See flushKnownDestinationsIfDirty. Stop performs a final flush so
	// nothing is lost on graceful shutdown.
	if ts.storagePath != "" && ts.running {
		ts.knownDestDirty = true
	}
	ts.mu.Unlock()
}

// Recall searches for a known identity matching the given target hash.
// It checks both destination hashes and truncated identity hashes.
func (ts *TransportSystem) Recall(targetHash []byte) *Identity {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	// The app-facing Recall marks the destination used (Python default
	// _no_use=False, RNS/Identity.py:135,145,170).
	return ts.recallLocked(targetHash, false)
}

// RecallNoUse recalls a known destination's identity WITHOUT marking it used,
// for callers that do not represent actual application use (message unpacking,
// path-table scans, announce rebroadcasts, link-proof validation, announce-
// handler dispatch). It mirrors Python Identity.recall(..., _no_use=True)
// (RNS/Identity.py:116-160).
func (ts *TransportSystem) RecallNoUse(targetHash []byte) *Identity {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.recallLocked(targetHash, true)
}

// RecallAppData returns the cached app_data (element 3) for a known
// destination, or nil if the destination is unknown (Python
// Identity.recall_app_data, RNS/Identity.py:162-175). It does not mutate the
// use-timestamp element; the use-marking side effect of Python's
// recall_app_data is threaded separately via the noUse flag.
func (ts *TransportSystem) RecallAppData(targetHash []byte) []byte {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	data, ok := ts.knownDestinations[string(targetHash)]
	if !ok || len(data) < 4 {
		return nil
	}
	appData, _ := data[3].([]byte)
	return appData
}

// RetainDestinationData marks the known destination destHash as retained by
// setting its use-timestamp (element 4) to -1, so CleanKnownDestinations never
// drops it. It returns true when destHash is known and was retained, false
// otherwise (Python Identity._retain_destination_data, RNS/Identity.py:252-258).
func (ts *TransportSystem) RetainDestinationData(destHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	data, ok := ts.knownDestinations[string(destHash)]
	if !ok || len(data) < 5 {
		return false
	}
	data[4] = int64(-1)
	if ts.storagePath != "" && ts.running {
		ts.knownDestDirty = true
	}
	return true
}

// UnretainDestinationData clears the retained flag by setting the use-timestamp
// (element 4) to the current time, marking the destination as recently used.
// It returns true when destHash is known and was unretained, false otherwise
// (Python Identity._unretain_destination_data, RNS/Identity.py:261-267).
func (ts *TransportSystem) UnretainDestinationData(destHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	data, ok := ts.knownDestinations[string(destHash)]
	if !ok || len(data) < 5 {
		return false
	}
	data[4] = float64(time.Now().UnixNano()) / 1e9
	if ts.storagePath != "" && ts.running {
		ts.knownDestDirty = true
	}
	return true
}

// UsedDestinationData marks the known destination destHash as in use by
// setting its use-timestamp (element 4) to the current time, but ONLY when it
// is not currently retained (element 4 is not < 0). A retained destination is
// left untouched. Returns true when the use-timestamp was updated, false
// otherwise (Python Identity._used_destination_data, RNS/Identity.py:242-250).
func (ts *TransportSystem) UsedDestinationData(destHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.usedDestinationDataLocked(destHash)
}

// KnownDestinationUseTimestamp returns the use-timestamp (element 4) of the
// known-destination entry for destHash, or ok=false if destHash is unknown.
// It is a read-only accessor for the last-used time that drives
// CleanKnownDestinations retention (Python Identity.known_destinations element
// 4, RNS/Identity.py:310-316). A value of 0 means never used; a negative value
// means the destination is retained and never expires.
func (ts *TransportSystem) KnownDestinationUseTimestamp(destHash []byte) (float64, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	data, ok := ts.knownDestinations[string(destHash)]
	if !ok || len(data) < 5 {
		return 0, false
	}
	useTS, _ := numericValue(data[4])
	return useTS, true
}

// usedDestinationDataLocked is the lock-held core of UsedDestinationData,
// also called by recallLocked(!noUse) to mark a destination used on a
// successful app-facing recall (Python Identity.recall's
// `_used_destination_data` side effect, RNS/Identity.py:135,145,170). The
// caller must hold ts.mu.
func (ts *TransportSystem) usedDestinationDataLocked(destHash []byte) bool {
	data, ok := ts.knownDestinations[string(destHash)]
	if !ok || len(data) < 5 {
		return false
	}
	useTS, _ := numericValue(data[4])
	if useTS < 0 {
		// Retained destinations are not touched by use-marking.
		return false
	}
	data[4] = float64(time.Now().UnixNano()) / 1e9
	if ts.storagePath != "" && ts.running {
		ts.knownDestDirty = true
	}
	return true
}

// RetainIdentity retains every known destination whose public key hashes
// (truncated) to identityHash, so the destinations owned by that identity are
// never dropped by CleanKnownDestinations. It returns true when at least one
// destination was retained, false otherwise (Python Identity._retain_identity,
// RNS/Identity.py:270-283).
func (ts *TransportSystem) RetainIdentity(identityHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	retained := false
	for _, data := range ts.knownDestinations {
		if len(data) < 5 {
			continue
		}
		pubKey, ok := data[2].([]byte)
		if !ok {
			continue
		}
		if !bytes.Equal(TruncatedHash(pubKey), identityHash) {
			continue
		}
		data[4] = int64(-1)
		retained = true
	}
	if retained && ts.storagePath != "" && ts.running {
		ts.knownDestDirty = true
	}
	return retained
}

// recallLocked is the lock-held core of Recall. It is also used by
// removeBlackholedPathsLocked, which already holds ts.mu, to recall each
// path-table destination's identity without re-locking.
//
// When noUse is false (the app-facing default), a successful recall of a known
// destination marks it used by stamping the current time into its use-
// timestamp element (Python Identity.recall's `_used_destination_data` side
// effect, RNS/Identity.py:135,145,170). Transport-internal callers pass
// noUse=true so path-table scans, announce rebroadcasts, link-proof
// validation, and announce-handler dispatch do not inflate a destination's
// last-used time (Python Identity.recall(..., _no_use=True)).
// The registered-local-destination branch never marks used, matching Python
// (RNS/Identity.py:152-158).
func (ts *TransportSystem) recallLocked(targetHash []byte, noUse bool) *Identity {
	// Check destination hashes
	if data, ok := ts.knownDestinations[string(targetHash)]; ok {
		pubKey := data[2].([]byte)
		id, err := NewIdentity(false, ts.logger)
		if err != nil {
			ts.logger.Error("Failed to create identity during recall: %v", err)
			return nil
		}
		if err := id.LoadPublicKey(pubKey); err != nil {
			ts.logger.Error("Failed to load recalled public key: %v", err)
			return nil
		}
		if data[3] != nil {
			id.AppData = data[3].([]byte)
		}
		if !noUse {
			ts.usedDestinationDataLocked(targetHash)
		}
		return id
	}

	// Check identity hashes
	for destHash, data := range ts.knownDestinations {
		pubKey := data[2].([]byte)
		if bytes.Equal(targetHash, TruncatedHash(pubKey)) {
			id, err := NewIdentity(false, ts.logger)
			if err != nil {
				ts.logger.Error("Failed to create identity during recall: %v", err)
				return nil
			}
			if err := id.LoadPublicKey(pubKey); err != nil {
				ts.logger.Error("Failed to load recalled public key: %v", err)
				return nil
			}
			if data[3] != nil {
				id.AppData = data[3].([]byte)
			}
			if !noUse {
				ts.usedDestinationDataLocked([]byte(destHash))
			}
			return id
		}
	}

	// Also check registered destinations in transport (O(1) hash lookup,
	// Python Transport.destinations_map).
	if d := ts.localDestinationLocked(targetHash); d != nil {
		id, err := NewIdentity(false, ts.logger)
		if err != nil {
			ts.logger.Error("Failed to create identity during transport recall: %v", err)
			return nil
		}
		if err := id.LoadPublicKey(d.identity.GetPublicKey()); err != nil {
			ts.logger.Error("Failed to load transport destination public key: %v", err)
			return nil
		}
		return id
	}

	return nil
}

// GetRatchet retrieves the most recently observed valid forward-secrecy ratchet public key for a known destination.
func (ts *TransportSystem) GetRatchet(destHash []byte) []byte {
	ts.mu.Lock()
	destHashStr := string(destHash)
	if pub, ok := ts.knownRatchets[destHashStr]; ok {
		ts.mu.Unlock()
		return pub
	}
	path := ts.storagePath
	ts.mu.Unlock()

	if path == "" {
		return nil
	}

	// Try to load from storage
	hexHash := fmt.Sprintf("%x", destHash)
	ratchetPath := filepath.Join(path, "ratchets", hexHash)
	if _, err := os.Stat(ratchetPath); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(ratchetPath)
	if err != nil {
		ts.logger.Error("Failed to read ratchet file for %v: %v", hexHash, err)
		return nil
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		ts.logger.Error("Failed to unpack ratchet data for %v: %v", hexHash, err)
		return nil
	}

	if m, ok := unpacked.(map[any]any); ok {
		ratchetPub := m["ratchet"].([]byte)
		received := m["received"].(float64)

		// Check expiry (30 days)
		if float64(time.Now().UnixNano())/1e9 < received+30*24*3600 {
			ts.mu.Lock()
			ts.knownRatchets[destHashStr] = ratchetPub
			ts.mu.Unlock()
			return ratchetPub
		}
		// Expired
		if err := os.Remove(ratchetPath); err != nil && !os.IsNotExist(err) {
			ts.logger.Error("Failed to remove expired ratchet file for %v: %v", hexHash, err)
		}
	}

	return nil
}

// SetRatchet securely registers and optionally persists a forward-secrecy ratchet public key associated with a specific destination.
func (ts *TransportSystem) SetRatchet(destHash, ratchetPub []byte) {
	ts.mu.Lock()
	destHashStr := string(destHash)
	if bytes.Equal(ts.knownRatchets[destHashStr], ratchetPub) {
		ts.mu.Unlock()
		return
	}
	ts.knownRatchets[destHashStr] = ratchetPub
	path := ts.storagePath
	running := ts.running
	ts.mu.Unlock()

	if path != "" && running {
		ts.persistRatchet(path, destHash, ratchetPub)
	}
}

func (ts *TransportSystem) persistRatchet(storagePath string, destHash, ratchetPub []byte) {
	if storagePath == "" {
		return
	}

	ratchetDir := filepath.Join(storagePath, "ratchets")
	if err := os.MkdirAll(ratchetDir, 0o700); err != nil {
		ts.logger.Error("Failed to create ratchet directory: %v", err)
		return
	}

	hexHash := fmt.Sprintf("%x", destHash)
	finalPath := filepath.Join(ratchetDir, hexHash)

	// Use a unique temp file to avoid races when two concurrent persistRatchet
	// calls target the same destHash. Without uniqueness, both would write to
	// the same ".out" path and the second Rename would fail with ENOENT.
	tmpFile, err := os.CreateTemp(ratchetDir, hexHash+".*.tmp")
	if err != nil {
		ts.logger.Error("Failed to create temp ratchet file for %v: %v", hexHash, err)
		return
	}
	tmpPath := tmpFile.Name()

	ratchetData := map[string]any{
		"ratchet":  ratchetPub,
		"received": float64(time.Now().UnixNano()) / 1e9,
	}

	data, err := msgpack.Pack(ratchetData)
	if err != nil {
		ts.logger.Error("Failed to pack ratchet data for %v: %v", hexHash, err)
		if err := tmpFile.Close(); err != nil {
			ts.logger.Error("tmpFile.Close: %v", err)
		}
		if err := os.Remove(tmpPath); err != nil {
			ts.logger.Error("os.Remove: %v", err)
		}
		return
	}

	if _, err := tmpFile.Write(data); err != nil {
		ts.logger.Error("Failed to write ratchet file for %v: %v", hexHash, err)
		if err := tmpFile.Close(); err != nil {
			ts.logger.Error("tmpFile.Close: %v", err)
		}
		if err := os.Remove(tmpPath); err != nil {
			ts.logger.Error("os.Remove: %v", err)
		}
		return
	}
	if err := tmpFile.Close(); err != nil {
		ts.logger.Error("tmpFile.Close: %v", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		ts.logger.Error("Failed to finalize ratchet file for %v: %v", hexHash, err)
		if err := os.Remove(tmpPath); err != nil {
			ts.logger.Error("os.Remove: %v", err)
		}
	}
}

// LoadKnownDestinations populates the local identity cache using serialized data retrieved from disk.
func (ts *TransportSystem) LoadKnownDestinations(storagePath string) {
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	path := filepath.Join(storagePath, "known_destinations")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		ts.logger.Error("Failed to read known destinations: %v", err)
		return
	}

	unpacked, err := Unpack(data)
	if err != nil {
		ts.logger.Error("Failed to unpack known destinations: %v", err)
		return
	}

	if m, ok := unpacked.(map[any]any); ok {
		ts.mu.Lock()
		for k, v := range m {
			ks, okKey := k.(string)
			if !okKey {
				continue
			}
			vs, okVal := v.([]any)
			if !okVal {
				continue
			}
			// Migrate pre-v1.3.0 4-element entries to the 5-element layout
			// by appending a use-timestamp of 0 (never used), matching
			// Python Identity.load_known_destinations (RNS/Identity.py:226-228).
			// int64(0) packs as the positive fixint 0x00, the same byte
			// Python's literal 0 produces.
			if len(vs) < 5 {
				vs = append(vs, int64(0))
			}
			ts.knownDestinations[ks] = vs
		}
		ts.mu.Unlock()
		ts.logger.Verbose("Loaded %v known destination from storage", len(ts.knownDestinations))
	}
}

// SaveKnownDestinations serializes and safely flushes the currently cached
// known network identities to persistent storage. It writes to a temp file
// and atomically renames it into place (Python os.replace,
// RNS/Identity.py:197-208), so a crash or write error can never leave the
// canonical known_destinations file partially written — the previous complete
// file remains intact until the rename succeeds. Concurrent saves are
// serialized by saveMu (Python saving_known_destinations flag).
func (ts *TransportSystem) SaveKnownDestinations(storagePath string) {
	if storagePath == "" {
		return
	}

	path := filepath.Join(storagePath, "known_destinations")
	ts.mu.Lock()
	// Snapshot a copy of the map before packing so a concurrent Remember does
	// not mutate the slice we serialize — Python's
	// Identity.known_destinations.copy() (RNS/Identity.py:197).
	// Emit binary (msgpack bin 0xc4) map keys, matching Python RNS, which keys
	// Identity.known_destinations by the raw destination_hash bytes. Packing the
	// in-memory map[string][]any directly would emit str keys (fixstr 0xa0-0xbf)
	// that Python's umsgpack tries to utf-8-decode and rejects with
	// InvalidStringException. Go maps cannot key on []byte (non-comparable), so
	// build an OrderedMap with []byte keys; Pack routes []byte through packBin.
	ordered := make(msgpack.OrderedMap, 0, len(ts.knownDestinations))
	for k, v := range ts.knownDestinations {
		ordered = append(ordered, msgpack.OrderedMapEntry{Key: copyBytes([]byte(k)), Value: v})
	}
	count := len(ts.knownDestinations)
	ts.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		ts.logger.Error("Failed to create known destinations directory: %v", err)
		return
	}

	// Serialize the pack + temp write + rename so concurrent saves cannot
	// collide on the same temp file or race the rename (Python
	// saving_known_destinations, RNS/Identity.py:191-205).
	ts.saveMu.Lock()
	defer ts.saveMu.Unlock()
	ts.saveSeq++
	tempPath := fmt.Sprintf("%s.tmp.%d", path, ts.saveSeq)

	// Pack outside ts.mu but inside saveMu. A pack error leaves no temp file to
	// clean up; log and abort without touching the canonical file.
	data, err := msgpack.Pack(ordered)
	if err != nil {
		ts.logger.Error("Failed to pack known destinations: %v", err)
		return
	}

	// Write the temp file, then atomically rename it into place. On any write
	// or rename error, unlink the temp file (best-effort, like Python's
	// try/except around os.unlink, RNS/Identity.py:204-206) and abort without
	// modifying the canonical file.
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		ts.logger.Error("Error while serializing and writing known destinations: %v", err)
		removeBestEffort(tempPath, ts.logger)
		return
	}
	if err := os.Rename(tempPath, path); err != nil {
		ts.logger.Error("Error while renaming known destinations temp file: %v", err)
		removeBestEffort(tempPath, ts.logger)
		return
	}
	ts.logger.Debug("Saved %v known destinations to storage", count)
}

// removeBestEffort deletes path, logging (not returning) any error, mirroring
// Python's `try: os.unlink(temp_file) except Exception as e: RNS.log(...)`
// (RNS/Identity.py:204-206) used to clean up a temp file after a failed save.
func removeBestEffort(path string, logger *Logger) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.Warning("Could not clean up temporary file %s: %v", path, err)
	}
}

// SaveKnownDestinationsWithRecombine first merges any known-destinations
// entries currently on disk that are NOT in the in-memory table, then
// atomically persists the merged table via SaveKnownDestinations. This is the
// historical recombine=True behavior of Python Identity.save_known_destinations
// (RNS/Identity.py, pre-b408699e "Periodically clean known destinations"):
// load the disk file, and for each disk entry whose hash is missing from
// memory, copy it in. The merge is "memory wins" — a disk entry already
// present in knownDestinations is never overwritten. The current Python port
// deprecates and ignores the recombine argument; this method preserves the
// merge semantics for callers that want a disk-backed save.
func (ts *TransportSystem) SaveKnownDestinationsWithRecombine(storagePath string) {
	if storagePath == "" {
		return
	}
	path := filepath.Join(storagePath, "known_destinations")
	if _, err := os.Stat(path); err != nil {
		// No existing disk file to recombine from; a plain save suffices.
		ts.SaveKnownDestinations(storagePath)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		ts.logger.Warning("Skipped recombining known destinations from disk, since an error occurred: %v", err)
		ts.SaveKnownDestinations(storagePath)
		return
	}
	unpacked, err := Unpack(data)
	if err != nil {
		ts.logger.Warning("Skipped recombining known destinations from disk, since an error occurred: %v", err)
		ts.SaveKnownDestinations(storagePath)
		return
	}
	diskMap, ok := unpacked.(map[any]any)
	if !ok {
		ts.SaveKnownDestinations(storagePath)
		return
	}
	ts.mu.Lock()
	for k, v := range diskMap {
		ks, okKey := k.(string)
		if !okKey {
			continue
		}
		if _, exists := ts.knownDestinations[ks]; exists {
			// Memory wins; do not overwrite an in-memory entry with the disk copy.
			continue
		}
		vs, okVal := v.([]any)
		if !okVal {
			continue
		}
		// Migrate pre-v1.3.0 4-element disk entries to 5 elements on merge,
		// matching load_known_destinations (RNS/Identity.py:226-228).
		if len(vs) < 5 {
			vs = append(vs, int64(0))
		}
		ts.knownDestinations[ks] = vs
	}
	ts.mu.Unlock()
	ts.SaveKnownDestinations(storagePath)
}

// flushKnownDestinationsIfDirty writes the known-destinations table to disk if
// Remember has marked it dirty since the last flush, then clears the flag. It
// coalesces many announces into a single serialize+write. Called periodically
// from the maintenance loop and once from Stop. Safe to call when not running
// (used by Stop's final flush); SaveKnownDestinations itself is a no-op on an
// empty storagePath.
func (ts *TransportSystem) flushKnownDestinationsIfDirty() {
	ts.mu.Lock()
	if !ts.knownDestDirty {
		ts.mu.Unlock()
		return
	}
	ts.knownDestDirty = false
	path := ts.storagePath
	ts.mu.Unlock()
	if path == "" {
		return
	}
	ts.SaveKnownDestinations(path)
}

// UnblackholeIdentity removes an identity hash from the local blackhole registry.
func (ts *TransportSystem) UnblackholeIdentity(identityHash []byte) bool {
	if len(identityHash) == 0 {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	key := string(identityHash)
	if _, ok := ts.blackholedIdentities[key]; !ok {
		return false
	}
	delete(ts.blackholedIdentities, key)
	return true
}

// IsBlackholed reports whether identityHash is currently on the local
// blackhole list (RNS.Reticulum.is_blackholed). The link identify path calls
// it to terminate incoming links from blocked identities
// (RNS/Link.py:974-976, v1.3.2).
func (ts *TransportSystem) IsBlackholed(identityHash []byte) bool {
	if len(identityHash) == 0 {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	_, ok := ts.blackholedIdentities[string(identityHash)]
	return ok
}

// GetBlackholedIdentities returns the current local blackhole registry snapshot.
func (ts *TransportSystem) GetBlackholedIdentities() []map[string]any {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	out := make([]map[string]any, 0, len(ts.blackholedIdentities))
	for _, entry := range ts.blackholedIdentities {
		item := map[string]any{
			"identity_hash": copyBytes(entry.IdentityHash),
			"source":        copyBytes(entry.Source),
			"reason":        entry.Reason,
		}
		if entry.Until != nil {
			item["until"] = entry.Until.Unix()
		} else {
			item["until"] = int64(0)
		}
		out = append(out, item)
	}
	return out
}

func (ts *TransportSystem) identityHash() []byte {
	if ts == nil || ts.identity == nil {
		return nil
	}
	return ts.identity.Hash
}

// blackholeListHandler is the /list request handler for the
// rnstransport.info.blackhole destination (Python
// Transport.blackhole_list_handler, Transport.py:3243-3250). It returns the
// current blackholed_identities map serialised as a msgpack map with binary
// identity-hash keys — the exact form Python's umsgpack produces and the
// Discovery.BlackholeUpdater consumes. The return value implements
// msgpack.Marshaler so the link response path writes the hand-packed bytes
// verbatim (Go maps cannot hold []byte keys, so reflection packing cannot
// produce the bin-keyed map Python emits).
func (ts *TransportSystem) blackholeListHandler(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	entries := make(blackholeListMsgpack, 0, len(ts.blackholedIdentities))
	for identityHash, entry := range ts.blackholedIdentities {
		entries = append(entries, blackholeListEntry{
			identityHash: copyBytes([]byte(identityHash)),
			source:       copyBytes(entry.Source),
			until:        entry.Until,
			reason:       entry.Reason,
		})
	}
	return entries
}

// blackholeListMsgpack is a blackhole list that msgpack-encodes itself with
// binary identity-hash keys via packBlackholeList, satisfying
// msgpack.Marshaler.
type blackholeListMsgpack []blackholeListEntry

// MarshalMsgpack encodes the blackhole list as a msgpack map with binary
// keys, matching Python umsgpack.packb(Transport.blackholed_identities).
func (b blackholeListMsgpack) MarshalMsgpack() ([]byte, error) {
	return packBlackholeList([]blackholeListEntry(b))
}

// SetBlackholeSources configures the set of remote source identity hashes
// whose blackhole lists reloadBlackholeAt accepts (Python
// RNS.Reticulum.__blackhole_sources, set from the "blackhole_sources"
// config option).
func (ts *TransportSystem) SetBlackholeSources(sources [][]byte) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.blackholeSources = append(ts.blackholeSources[:0:0], sources...)
}

// SetBlackholeUpdateInterval sets the minimum interval between fetches of any
// single blackhole source (Python RNS.Reticulum.__blackhole_update_interval,
// Reticulum.py:601-604). It must be called before EnableBlackholeUpdater so
// the constructed updater picks up the configured interval.
func (ts *TransportSystem) SetBlackholeUpdateInterval(d time.Duration) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.blackholeUpdateInterval = d
}

// BlackholeUpdateInterval returns the configured blackhole source fetch
// interval (Python RNS.Reticulum.blackhole_update_interval(), Reticulum.py:1826-1827).
func (ts *TransportSystem) BlackholeUpdateInterval() time.Duration {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.blackholeUpdateInterval
}

// blackholeSourceEnabled reports whether sourceHash is a configured
// blackhole source (Python RNS.Reticulum.blackhole_sources() membership).
func (ts *TransportSystem) blackholeSourceEnabled(sourceHash []byte) bool {
	for _, s := range ts.blackholeSources {
		if bytes.Equal(s, sourceHash) {
			return true
		}
	}
	return false
}

// ReloadBlackhole reloads the in-memory blackhole set from the on-disk
// blackholepath, mirroring Python Transport.reload_blackhole
// (Transport.py:3183-3220), using the current wall clock for the until
// expiry check.
func (ts *TransportSystem) ReloadBlackhole() {
	ts.reloadBlackholeAt(time.Now())
}

// reloadBlackholeAt is the lock-holding core of ReloadBlackhole. now
// replaces time.time() so golden tests can drive the until expiry check
// deterministically (Python Transport.py:3184). It reads every file in
// blackholepath, derives the source identity hash from the filename
// ("local" -> own identity, else hex-decoded), skips disabled sources,
// msgpack-unpacks the per-source dict, and merges non-local entries into
// the in-memory set, then drops paths associated with blackholed
// identities.
func (ts *TransportSystem) reloadBlackholeAt(now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	ownHash := ts.identityHash()
	hexLen := (TruncatedHashLength / 8) * 2

	entries, err := os.ReadDir(ts.blackholePath)
	if err != nil {
		// No blackhole directory yet: nothing to reload.
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()

		var sourceIdentityHash []byte
		if filename == "local" {
			sourceIdentityHash = copyBytes(ownHash)
		} else {
			if len(filename) != hexLen {
				ts.logger.Error("Identity hash length for blackhole source %s is invalid", filename)
				continue
			}
			srcHash, err := hex.DecodeString(filename)
			if err != nil {
				ts.logger.Error("Could not decode blackhole source filename %s: %v", filename, err)
				continue
			}
			sourceIdentityHash = srcHash
			if !ts.blackholeSourceEnabled(sourceIdentityHash) {
				ts.logger.Verbose("Skipping disabled blackhole source %x", sourceIdentityHash)
				continue
			}
		}

		sourcepath := filepath.Join(ts.blackholePath, filename)
		packed, err := os.ReadFile(sourcepath)
		if err != nil {
			ts.logger.Error("Could not read blackhole source file %s: %v", filename, err)
			continue
		}
		obj, err := msgpack.Unpack(packed)
		if err != nil {
			ts.logger.Error("Could not unpack blackhole source file %s: %v", filename, err)
			continue
		}
		sourceList, ok := obj.(map[any]any)
		if !ok {
			ts.logger.Error("Unexpected blackhole source payload type %T in %s", obj, filename)
			continue
		}

		for k, v := range sourceList {
			identityHash := blackholeMapKey(k)
			if identityHash == nil || len(identityHash) != TruncatedHashLength/8 {
				continue
			}
			// Python: do not overwrite entries sourced from our own
			// identity (Transport.py:3203-3204).
			if existing, exists := ts.blackholedIdentities[string(identityHash)]; exists {
				if ownHash != nil && bytes.Equal(existing.Source, ownHash) {
					continue
				}
			}
			se, _ := v.(map[any]any)
			until := blackholeUntil(se)
			reason := blackholeReason(se)
			if until != nil && !now.Before(*until) {
				// Expired: skip (Python Transport.py:3212).
				continue
			}
			ts.blackholedIdentities[string(identityHash)] = BlackholeIdentityEntry{
				IdentityHash: copyBytes(identityHash),
				Source:       copyBytes(sourceIdentityHash),
				Until:        until,
				Reason:       reason,
			}
		}
	}

	ts.removeBlackholedPathsLocked()
}

// RemoveBlackholedPaths walks the path table and drops every destination
// whose associated identity is blackholed, mirroring Python
// Transport.remove_blackholed_paths (Transport.py:3222-3241).
func (ts *TransportSystem) RemoveBlackholedPaths() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	ts.removeBlackholedPathsLocked()
}

// removeBlackholedPathsLocked is the lock-held core of
// RemoveBlackholedPaths. It recalls each path-table destination's
// identity and deletes the entry when that identity is blackholed.
func (ts *TransportSystem) removeBlackholedPathsLocked() {
	drop := make([]string, 0)
	for destinationHash := range ts.pathTable {
		// Recall must not re-lock ts.mu; use the lock-free recall helper.
		associated := ts.recallLocked([]byte(destinationHash), true)
		if associated != nil && len(associated.Hash) != 0 {
			if _, blackholed := ts.blackholedIdentities[string(associated.Hash)]; blackholed {
				drop = append(drop, destinationHash)
			}
		}
	}
	for _, destinationHash := range drop {
		delete(ts.pathTable, destinationHash)
	}
	if len(drop) > 0 {
		ms := ""
		if len(drop) > 1 {
			ms = "s"
		}
		ts.logger.Info("Removed %d destination%s associated with blackholed identities from path table", len(drop), ms)
	}
}

// PersistBlackhole atomically writes the locally-sourced portion of the
// in-memory blackhole set to blackholepath/local, mirroring Python
// Transport.persist_blackhole (Transport.py:3252-3275). Only entries
// whose source is the local transport identity are written; each entry is
// serialised as {source: own, until, reason} under a binary (msgpack bin)
// identity-hash key, matching the on-disk format Python's umsgpack
// produces.
func (ts *TransportSystem) PersistBlackhole() {
	ts.mu.Lock()
	ownHash := ts.identityHash()
	entries := make([]blackholeListEntry, 0, len(ts.blackholedIdentities))
	for identityHash, entry := range ts.blackholedIdentities {
		if ownHash != nil && bytes.Equal(entry.Source, ownHash) {
			entries = append(entries, blackholeListEntry{
				identityHash: copyBytes([]byte(identityHash)),
				source:       copyBytes(ownHash),
				until:        entry.Until,
				reason:       entry.Reason,
			})
		}
	}
	localPath := filepath.Join(ts.blackholePath, "local")
	ts.mu.Unlock()

	packed, err := packBlackholeList(entries)
	if err != nil {
		ts.logger.Error("Error while packing blackhole list: %v", err)
		return
	}
	tmpPath := localPath + ".tmp"
	if err := os.WriteFile(tmpPath, packed, 0o600); err != nil {
		ts.logger.Error("Error while writing blackhole list: %v", err)
		return
	}
	// os.Rename atomically replaces an existing destination on POSIX, so no
	// prior Remove is needed (Python opens the path with "wb", truncating in
	// place; the rename is an atomic equivalent).
	if err := os.Rename(tmpPath, localPath); err != nil {
		ts.logger.Error("Error while persisting blackhole list: %v", err)
	}
}

// packBlackholeList serialises a blackhole list as a msgpack map whose
// keys are binary identity hashes (msgpack bin format, as Python's
// umsgpack writes) and whose values are {source, until, reason} sub-maps.
// Keys are sorted for deterministic output. The sub-map is packed with
// msgpack.Pack so its str keys ("source"/"until"/"reason") and bin
// "source" value match Python's encoding.
func packBlackholeList(entries []blackholeListEntry) ([]byte, error) {
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].identityHash, entries[j].identityHash) < 0
	})

	var buf bytes.Buffer
	n := len(entries)
	switch {
	case n <= 0x0f:
		buf.WriteByte(byte(0x80 | n))
	case n <= 0xffff:
		buf.WriteByte(0xde)
		buf.WriteByte(byte(n >> 8))
		buf.WriteByte(byte(n))
	default:
		buf.WriteByte(0xdf)
		buf.WriteByte(byte(n >> 24))
		buf.WriteByte(byte(n >> 16))
		buf.WriteByte(byte(n >> 8))
		buf.WriteByte(byte(n))
	}

	for _, e := range entries {
		if err := writeMsgpackBin(&buf, e.identityHash); err != nil {
			return nil, err
		}
		sub := map[any]any{
			"source": copyBytes(e.source),
			"until":  blackholeUntilValue(e.until),
			"reason": e.reason,
		}
		packed, err := msgpack.Pack(sub)
		if err != nil {
			return nil, err
		}
		buf.Write(packed)
	}
	return buf.Bytes(), nil
}

// blackholeListEntry is a single serialised blackhole entry.
type blackholeListEntry struct {
	identityHash []byte
	source       []byte
	until        *time.Time
	reason       string
}

// writeMsgpackBin writes a msgpack bin container holding data to w.
func writeMsgpackBin(w *bytes.Buffer, data []byte) error {
	n := len(data)
	switch {
	case n <= 0xff:
		w.WriteByte(0xc4)
		w.WriteByte(byte(n))
	case n <= 0xffff:
		w.WriteByte(0xc5)
		w.WriteByte(byte(n >> 8))
		w.WriteByte(byte(n))
	default:
		w.WriteByte(0xc6)
		w.WriteByte(byte(n >> 24))
		w.WriteByte(byte(n >> 16))
		w.WriteByte(byte(n >> 8))
		w.WriteByte(byte(n))
	}
	_, err := w.Write(data)
	return err
}

// blackholeMapKey extracts the identity-hash bytes from a msgpack-unpacked
// map key. Default msgpack.Unpack converts bin keys to string (the raw
// bytes); str keys are returned as-is. Either way the byte content is the
// identity hash.
func blackholeMapKey(k any) []byte {
	switch v := k.(type) {
	case string:
		return []byte(v)
	case []byte:
		return v
	}
	// The msgpack unpacker decodes bin-format map keys as an unexported
	// string-kind type (binaryMapKey); handle any string-kind value via
	// reflection so bin-keyed maps round-trip regardless of the concrete
	// key type.
	if rv := reflect.ValueOf(k); rv.IsValid() && rv.Kind() == reflect.String {
		return []byte(rv.String())
	}
	return nil
}

// blackholeUntil extracts the "until" field from a blackhole sub-entry.
// Python stores a float epoch (or None); Go materialises it as *time.Time.
func blackholeUntil(se map[any]any) *time.Time {
	v, ok := se["until"]
	if !ok || v == nil {
		return nil
	}
	switch u := v.(type) {
	case float64:
		t := time.Unix(int64(u), int64((u-float64(int64(u)))*1e9))
		return &t
	case int64:
		t := time.Unix(u, 0)
		return &t
	case uint64:
		t := time.Unix(int64(u), 0)
		return &t
	case int:
		t := time.Unix(int64(u), 0)
		return &t
	}
	return nil
}

// blackholeUntilValue converts a *time.Time back to the float epoch value
// Python persists (seconds with fractional component), or nil for no
// expiry. Unix()+Nanosecond() is used instead of UnixNano() so far-future
// expiries (e.g. 9.9e9) do not overflow int64.
func blackholeUntilValue(until *time.Time) any {
	if until == nil {
		return nil
	}
	return float64(until.Unix()) + float64(until.Nanosecond())/1e9
}

// blackholeReason extracts the "reason" string from a blackhole sub-entry.
func blackholeReason(se map[any]any) string {
	if v, ok := se["reason"]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		if b, ok := v.([]byte); ok {
			return string(b)
		}
	}
	return ""
}

// HasPath returns true if a path to the destination is known.
func (ts *TransportSystem) HasPath(destHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok := ts.pathTable[string(destHash)]
	ts.logger.Debug("Go Transport.HasPath(%x) = %v (pathTable size=%v)", destHash, ok, len(ts.pathTable))
	return ok
}

// RequestPath requests a path to the destination from the network.
func (ts *TransportSystem) RequestPath(destHash []byte) error {
	pathRequestDst, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		return err
	}

	now := time.Now()
	destinationHash := string(destHash)
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.pathRequests[destinationHash] = now
	ts.mu.Unlock()

	// Release any held announces for this destination on all interfaces.
	// On busy networks the announce frequency never drops below the ingress-
	// limit threshold, so held announces for unknown destinations are never
	// released by processHeldAnnounces. When a path request is explicitly
	// made (e.g. on-send), the held announce should be processed immediately
	// so the path is learned from the announce rather than from the (potentially
	// longer) path-request response. This mirrors the Python RNS behavior where
	// Transport.path_requests bypasses the ingress-limit hold at arrival time.
	for _, iface := range ts.GetInterfaces() {
		if raw, recv, ok := iface.ReleaseHeldAnnounce(destHash); ok {
			go ts.Inbound(raw, recv)
		}
	}

	requestTag, err := RandomHash()
	if err != nil {
		return err
	}

	var data []byte
	if ts.identity != nil {
		data = make([]byte, 0, len(destHash)+len(ts.identity.Hash)+len(requestTag))
		data = append(data, destHash...)
		data = append(data, ts.identity.Hash...)
		data = append(data, requestTag...)
	} else {
		data = make([]byte, 0, len(destHash)+len(requestTag))
		data = append(data, destHash...)
		data = append(data, requestTag...)
	}

	p := NewPacket(pathRequestDst, data)
	p.TransportType = TransportBroadcast
	// Transport.py:2896: packet.is_outbound_pr = True before send, so the
	// egress loop records an outgoing path request on each transmitting
	// interface (Transport.py:1354).
	p.IsOutboundPR = true
	return ts.Outbound(p)
}

// Inbound processes a raw packet received from an interface.
func (ts *TransportSystem) Inbound(raw []byte, iface interfaces.Interface) {
	// READY_WAIT: if Start is in progress but has not yet completed, wait for
	// the transport to become ready before processing the packet (Python
	// Transport.py:1430-1437). The wait only applies during the startup
	// window — a transport that was never Started (running=false, as in the
	// loopback test harness) is processed immediately. Poll every
	// readyPollInterval up to readyWaitTimeout (60s); on timeout log a
	// warning and drop the packet.
	ts.mu.Lock()
	running := ts.running
	ready := ts.ready
	ts.mu.Unlock()
	if running && !ready {
		waitStart := time.Now()
		for {
			time.Sleep(ts.readyPollInterval)
			ts.mu.Lock()
			ready = ts.ready
			running = ts.running
			ts.mu.Unlock()
			if ready || !running {
				break
			}
			if time.Since(waitStart) >= ts.readyWaitTimeout {
				ts.logger.Warning("Inbound packet timed out waiting for transport startup, dropping")
				return
			}
		}
	}

	if len(raw) == 0 {
		ts.logger.Debug("Go Transport.Inbound received empty frame from %v, dropping", iface.Name())
		return
	}
	ts.logger.Debug("Go Transport.Inbound received %v bytes from %v, type=%v\n", len(raw), iface.Name(), raw[0]>>4)
	if ifac, ok := iface.(ifacInboundHook); ok {
		processed, accepted := ifac.ApplyIFACInbound(raw)
		if !accepted {
			ts.logger.Debug("Dropped packet by IFAC ingress policy on %v", iface.Name())
			return
		}
		raw = processed
	}

	packet := NewPacketFromRaw(raw)
	packet.ReceivingInterface = iface
	if err := packet.Unpack(); err != nil {
		ts.logger.Extreme("Received malformed packet, dropping it: %v", err)
		return
	}
	ts.logger.Debug("Inbound packet: type=%v, dest=%x, hops=%v, hash=%x", packet.PacketType, packet.DestinationHash, packet.Hops, packet.PacketHash)

	packet.Hops++

	// Duplicate detection
	ts.mu.Lock()
	if ts.seenOrRememberPacketHashLocked(packet.PacketHash, time.Now()) {
		// Python's Transport.packet_filter (Transport.py:1417-1426) exempts
		// SINGLE-destination ANNOUNCE packets from the hashlist drop: a
		// duplicate announce is still passed to the announce handler, whose
		// own random-blob replay protection (Transport.py:1821-1845) decides
		// whether to accept or replace the path. This lets a single announce
		// for an unknown destination be accepted across interfaces within a
		// short window (e.g. a higher-gravity copy replacing the entry)
		// instead of being dropped just because an earlier copy was seen.
		if packet.PacketType == PacketAnnounce && packet.DestinationType == DestinationSingle {
			ts.logger.Verbose("Inbound: accepting duplicate SINGLE announce %x (announce-handler replay protection applies)", packet.PacketHash)
		} else {
			if packet.PacketType == PacketLinkRequest {
				ts.logger.Notice("Inbound: dropping DUPLICATE link request packet %x (type=%v)", packet.PacketHash, packet.PacketType)
			} else {
				ts.logger.Verbose("Inbound: dropping duplicate packet %x", packet.PacketHash)
			}
			ts.mu.Unlock()
			return
		}
	}
	if packet.RSSI != nil {
		ts.packetRSSICache[string(packet.PacketHash)] = *packet.RSSI
	}
	if packet.SNR != nil {
		ts.packetSNRCache[string(packet.PacketHash)] = *packet.SNR
	}
	if packet.Q != nil {
		ts.packetQCache[string(packet.PacketHash)] = *packet.Q
	}
	ts.mu.Unlock()

	// Destination management
	destHash := string(packet.DestinationHash)

	if packet.PacketType == PacketData && len(ts.pathRequestHash) > 0 && bytes.Equal(packet.DestinationHash, ts.pathRequestHash) {
		// Record the incoming path request on the receiving interface's PR
		// frequency deque (Python Transport.py:2983:
		// `if packet.receiving_interface: packet.receiving_interface.received_path_request()`).
		// Python gates this on a tag being present in the request data
		// (tag_bytes != None, i.e. len(data) > TRUNCATED_HASHLENGTH//8);
		// go-reticulum path requests always carry a tag, so the same gate
		// applies.
		if iface != nil && len(packet.Data) > TruncatedHashLength/8 {
			iface.ReceivedPathRequest()
		}
		// If this node answered the request (local destination or cached
		// path), do not relay it onward — matching Python's elif chain. Only
		// relay when the destination is unknown here.
		if ts.handlePathRequest(packet.Data, packet) {
			return
		}
		ts.forwardPathRequest(packet, iface)
		return
	}

	// Check if it's for us or a local destination (O(1) hash lookup,
	// Python Transport.destinations_map).
	ts.mu.Lock()
	localDest := ts.localDestinationLocked([]byte(destHash))
	ts.mu.Unlock()

	if localDest != nil {
		// Delivery to local destination
		if packet.PacketType == PacketLinkRequest {
			ts.logger.Notice("Inbound: delivering LINK REQUEST packet %x to local destination %x (name=%v)", packet.PacketHash, packet.DestinationHash, localDest.name)
		} else {
			ts.logger.Debug("Inbound: delivering packet %x to local destination %v", packet.PacketHash, localDest)
		}
		packet.Destination = localDest
		localDest.receive(packet)
		return
	}

	// Check if it's for a local link
	if link := ts.FindLink(packet.DestinationHash); link != nil {
		// A link request proof (LRPROOF) is gated by the expected-hops
		// check and may trigger a path re-balance before delivery
		// (RNS/Transport.py:2272-2312). All other link packets (data,
		// keepalive, RTT) are delivered directly to the link.
		if packet.Context == ContextLrproof && packet.PacketType == PacketProof {
			ts.deliverLinkProof(link, packet)
			return
		}
		ts.logger.Info("Inbound: delivering packet %x (type=%v, context=%v) to local link %x", packet.PacketHash, packet.PacketType, packet.Context, link.linkID)
		packet.Destination = link
		link.receive(packet)
		return
	}

	ts.logger.Debug("Inbound: no local destination or link found for packet %x (dest=%x, type=%v)", packet.PacketHash, packet.DestinationHash, packet.PacketType)

	// Transport handling
	if packet.PacketType != PacketAnnounce {
		// Check special conditions for local clients
		fromLocalClient := ts.isLocalClientInterface(iface)
		forLocalClient := packet.PacketType != PacketAnnounce && ts.isForLocalClient(packet)
		forLocalClientLink := packet.PacketType != PacketAnnounce && ts.isForLocalClientLink(packet)

		if ts.Enabled() || fromLocalClient || forLocalClient || forLocalClientLink {
			// If transport ID matches ours, we are the next hop
			if packet.TransportID != nil && ts.identity != nil && bytes.Equal(packet.TransportID, ts.identity.Hash) {
				ts.mu.Lock()
				if entry, ok := ts.pathTable[destHash]; ok {
					// A path-table entry loaded from disk may carry only an
					// interface name if that interface is no longer
					// registered, leaving Interface nil. We cannot forward
					// without an outbound interface, so drop the packet
					// instead of dereferencing a nil Interface below.
					if entry.Interface == nil {
						ts.mu.Unlock()
						ts.logger.Debug("Inbound: path entry for %x has no resolved interface, dropping", packet.DestinationHash)
						return
					}
					// Forwarding logic
					remainingHops := entry.Hops
					var newRaw []byte

					if remainingHops > 1 {
						newRaw = make([]byte, len(packet.Raw))
						copy(newRaw, packet.Raw)
						newRaw[1] = byte(packet.Hops)
						copy(newRaw[2:TruncatedHashLength/8+2], entry.NextHop)
					} else if remainingHops == 1 {
						// Strip transport header
						newFlags := (Header1 << 6) | (packet.Flags & 0b00001111)
						newRaw = []byte{newFlags, byte(packet.Hops)}
						newRaw = append(newRaw, packet.Raw[TruncatedHashLength/8+2:]...)
					} else {
						newRaw = make([]byte, len(packet.Raw))
						copy(newRaw, packet.Raw)
						newRaw[1] = byte(packet.Hops)
					}

					if packet.PacketType == PacketLinkRequest {
						now := time.Now()
						proofTimeout := ts.extraLinkProofTimeout(iface)
						proofTimeout += time.Duration(max(1, remainingHops)) * establishmentTimeoutPerHop
						linkID := LinkIDFromLR(packet)
						ts.linkTable[string(linkID)] = &LinkEntry{
							Timestamp:         now,
							NextHop:           copyBytes(entry.NextHop),
							OutboundInterface: entry.Interface,
							RemainingHops:     remainingHops,
							ReceivedInterface: iface,
							Hops:              packet.Hops,
							DestinationHash:   copyBytes(packet.DestinationHash),
							Validated:         false,
							ProofTimeout:      now.Add(proofTimeout),
						}
					} else {
						// Add reverse table entry for proofs/responses
						ts.reverseTable[string(packet.PacketHash)] = &ReverseEntry{
							ReceivedInterface: iface,
							OutboundInterface: entry.Interface,
							Timestamp:         time.Now(),
						}
					}

					ts.mu.Unlock()
					ts.logger.Debug("Inbound: transmitting forwarded packet on %s", entry.Interface.Name())
					if err := entry.Interface.Send(newRaw); err != nil {
						ts.logger.Error("Failed to forward packet: %v", err)
						ts.InvalidatePath(packet.DestinationHash)
					}
					return
				}
				ts.logger.Debug("Inbound: no path found in ts.pathTable for %x", packet.DestinationHash)
				ts.mu.Unlock()
			}

			// Link transport handling. Directs packets according to entries
			// in the link tables (Python Transport.py:1512-1549). This
			// forwards data packets (including the link-handshake RTT and
			// keepalives) addressed to a link ID along the link's recorded
			// interfaces, independent of any transport_id. It is what lets a
			// client behind a shared instance exchange link traffic with a
			// remote node: the client has no path to the peer's link ID, so
			// it broadcasts link packets as Header1 on its local interface;
			// the shared instance receives them and this block forwards them
			// on the opposite link interface. Without it the shared instance
			// drops every post-handshake link packet, so the remote side never
			// activates (the RTT never arrives) and the link is unusable even
			// though the initiator side reached ACTIVE on the proof.
			if packet.PacketType != PacketAnnounce && packet.PacketType != PacketLinkRequest && packet.Context != ContextLrproof {
				ts.mu.Lock()
				linkEntry, ok := ts.linkTable[string(packet.DestinationHash)]
				if !ok {
					ts.mu.Unlock()
				} else {
					var outboundIface interfaces.Interface
					if linkEntry.OutboundInterface == linkEntry.ReceivedInterface {
						// Receiving and outbound interface are the same, so
						// direction doesn't matter; just confirm the taken
						// hop count matches one of the expected values.
						if packet.Hops == linkEntry.RemainingHops || packet.Hops == linkEntry.Hops {
							outboundIface = linkEntry.OutboundInterface
						}
					} else {
						// Interfaces differ: transmit on the opposite
						// interface from the one the packet arrived on.
						if iface == linkEntry.OutboundInterface {
							if packet.Hops == linkEntry.RemainingHops {
								outboundIface = linkEntry.ReceivedInterface
							}
						} else if iface == linkEntry.ReceivedInterface {
							if packet.Hops == linkEntry.Hops {
								outboundIface = linkEntry.OutboundInterface
							}
						}
					}
					if outboundIface != nil {
						// The packet hash was already entered into the
						// duplicate filter at the top of Inbound, so we do
						// not need to re-add it here (Python's
						// add_packet_hash at Transport.py:1543).
						newRaw := make([]byte, len(packet.Raw))
						copy(newRaw, packet.Raw)
						if len(newRaw) > 1 {
							newRaw[1] = byte(packet.Hops)
						}
						linkEntry.Timestamp = time.Now()
						ts.mu.Unlock()
						ts.logger.Debug("Inbound: forwarding link-transport packet %x for link %x on %s", packet.PacketHash, packet.DestinationHash, outboundIface.Name())
						if err := outboundIface.Send(newRaw); err != nil {
							ts.logger.Error("Failed to forward link-transport packet: %v", err)
						}
						return
					}
					ts.mu.Unlock()
				}
			}
		}
	}

	// Proof handling
	if packet.PacketType == PacketProof {
		ts.logger.Debug("Inbound: processing PROOF packet %x for dest %x", packet.PacketHash, packet.DestinationHash)
		if packet.Context == ContextLrproof {
			// This is a link request proof. If we are transporting it for a
			// remote link (a link_table entry exists), re-balance and forward
			// it (RNS/Transport.py:2207-2265). Otherwise deliver it to a local
			// pending link via the expected-hops gate (deliverLinkProof).
			if ts.relayLinkProof(packet, iface) {
				return
			}
			if l := ts.FindLink(packet.DestinationHash); l != nil {
				ts.deliverLinkProof(l, packet)
				return
			}
		} else {
			// Normal proof
			var proofHash []byte
			if packet.Context == ContextLinkProof {
				if len(packet.Data) >= TruncatedHashLength/8 {
					proofHash = packet.Data[:TruncatedHashLength/8]
				}
			}

			ts.mu.Lock()
			// Forward to local client interfaces if they match the proof hash
			for _, ifaceEntry := range ts.interfaces {
				if ts.isLocalClientInterface(ifaceEntry) {
					// Check if this interface hash matches the proof destination
					if ifaceHash, ok := ifaceEntry.(interface{ GetHash() []byte }); ok {
						if bytes.Equal(ifaceHash.GetHash(), packet.DestinationHash) {
							ts.logger.Debug("Inbound: delivering proof %x to local client interface %v", packet.PacketHash, ifaceEntry.Name())
							newRaw := make([]byte, len(packet.Raw))
							copy(newRaw, packet.Raw)
							newRaw[1] = byte(packet.Hops)
							ts.mu.Unlock()
							if err := ifaceEntry.Send(newRaw); err != nil {
								ts.logger.Error("Failed to deliver proof to local client: %v", err)
							}
							return
						}
					}
				}
			}

			// Check if this proof needs to be transported
			if entry, ok := ts.reverseTable[string(packet.DestinationHash)]; ok {
				newRaw := make([]byte, len(packet.Raw))
				copy(newRaw, packet.Raw)
				newRaw[1] = byte(packet.Hops)
				ts.mu.Unlock()
				if err := entry.ReceivedInterface.Send(newRaw); err != nil {
					ts.logger.Error("Failed to forward proof: %v", err)
				}
				return
			}

			// Match against outstanding receipts
			var validatedReceipts []*PacketReceipt
			ts.logger.Debug("Inbound: matching proof against %v outstanding receipts", len(ts.receipts))
			for i := 0; i < len(ts.receipts); i++ {
				r := ts.receipts[i]
				validated := false
				if len(proofHash) > 0 {
					if bytes.Equal(r.TruncatedHash, proofHash) {
						validated = r.ValidateProofPacket(packet)
					}
				} else {
					validated = r.ValidateProofPacket(packet)
				}

				if validated {
					ts.logger.Debug("Inbound: successfully matched proof to receipt for packet %x", r.Hash)
					validatedReceipts = append(validatedReceipts, r)
					ts.receipts = append(ts.receipts[:i], ts.receipts[i+1:]...)
					i--
				}
			}
			ts.mu.Unlock()
			if len(validatedReceipts) > 0 {
				return
			}
		}
	}

	// Announce propagation
	if packet.PacketType == PacketAnnounce {
		if packet.Context == ContextPathResponse {
			ts.forwardPathResponseToRequesters(packet, iface)
		}
		ts.handleAnnounce(packet, iface)
		return
	}
}

func copyBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// containsBlob checks if a blob already exists in the slice.
func containsBlob(blobs [][]byte, blob []byte) bool {
	for _, b := range blobs {
		if bytes.Equal(b, blob) {
			return true
		}
	}
	return false
}

func nextHopFromAnnounce(packet *Packet) ([]byte, error) {
	if packet == nil {
		return nil, errors.New("nil packet")
	}
	if len(packet.TransportID) > 0 {
		return copyBytes(packet.TransportID), nil
	}
	if len(packet.DestinationHash) > 0 {
		return copyBytes(packet.DestinationHash), nil
	}
	return nil, errors.New("announce has no next-hop material")
}

// forwardAnnounceToLocalClients retransmits an accepted announce to every
// co-located client of this shared Reticulum instance, mirroring Python
// Transport.py:1790-1833. The shared instance owns the network interfaces;
// its clients (ping-nomadnet-node, lxmd, rncp, ...) own only a
// LocalClientInterface to the shared instance and would otherwise never learn
// any path. Each announce is rewritten as a Header2 transport packet with
// transport_id = ts.identity.Hash and the announce's current hop count, so the
// receiving client records a path entry whose NextHop is the shared instance
// and whose Interface is its local link — enabling it to inject its own
// packets into transport via the shared instance (Outbound Header2 branch).
// The interface the announce was received on is excluded so a client's own
// announce is not echoed back to it. Sends are dispatched on their own
// goroutines so a stalled co-located client cannot block the readLoop that
// received the announce.
func (ts *TransportSystem) forwardAnnounceToLocalClients(packet *Packet, receivedOn interfaces.Interface) {
	if ts.identity == nil || len(ts.identity.Hash) == 0 {
		return
	}

	// Build the Header2 forward frame once; it is identical for every client.
	// Layout: [flags][hops][transport_id][destination_hash][context][data].
	newFlags := byte((Header2 << 6) | (packet.ContextFlag << 5) | (TransportForward << 4) | (packet.DestinationType << 2) | packet.PacketType)
	raw := make([]byte, 0, 2+len(ts.identity.Hash)+len(packet.DestinationHash)+1+len(packet.Data))
	raw = append(raw, newFlags, byte(packet.Hops))
	raw = append(raw, ts.identity.Hash...)
	raw = append(raw, packet.DestinationHash...)
	raw = append(raw, byte(packet.Context))
	raw = append(raw, packet.Data...)

	// Snapshot the LocalServerInterfaces registered with this transport. We
	// release the transport lock before sending: a stalled co-located client
	// must not block the readLoop that received the announce.
	ts.mu.Lock()
	servers := make([]*interfaces.LocalServerInterface, 0, 2)
	for _, iface := range ts.interfaces {
		if lsi, ok := iface.(*interfaces.LocalServerInterface); ok {
			servers = append(servers, lsi)
		}
	}
	ts.mu.Unlock()

	for _, lsi := range servers {
		for _, sc := range lsi.SpawnedClientInterfaces() {
			if sc == receivedOn {
				continue
			}
			frame := append([]byte(nil), raw...)
			ts.outboundWG.Add(1)
			go func(iface interfaces.Interface, f []byte) {
				defer ts.outboundWG.Done()
				ts.dispatchForwardSend(iface, f, "local announce forward")
			}(sc, frame)
		}
	}
}

func (ts *TransportSystem) handleAnnounce(packet *Packet, iface interfaces.Interface) {
	// Announces carry the most variable untrusted structure on the network
	// (public keys, signatures, ratchet keys, app data). A single malformed
	// or truncated announce must never crash the node: recover, log, and
	// drop the packet so the readLoop keeps serving its interface instead of
	// taking the whole process down with it.
	defer func() {
		if r := recover(); r != nil {
			ts.logger.Error("Recovered from malformed announce on %v (dest=%x): %v", iface.Name(), packet.DestinationHash, r)
		}
	}()
	if !ValidateAnnounce(ts, packet) {
		ts.logger.Debug("Received invalid announce for %x, dropping", packet.DestinationHash)
		return
	}

	// Record the incoming announce on the receiving interface's frequency
	// deque (Python Transport.py:1751: `elif interface != None:
	// interface.received_announce()`).
	if iface != nil {
		iface.ReceivedAnnounce()
	}

	destHash := string(packet.DestinationHash)

	// Ingress-limit gate for unknown destinations (Python Transport.py
	// :1752-1765). Already-known destinations have re-announces controlled by
	// normal announce rate limiting, so the gate only applies when the
	// destination is not in the path table. A pending path request for the
	// destination bypasses the gate so path-finding is never starved by
	// ingress limiting. The discovery_path_requests half of the bypass
	// (Transport.py:1759) is pending the discovery port.
	if held := ts.shouldHoldAnnounce(packet, iface, destHash); held {
		return
	}

	var handlers []*AnnounceHandler
	// shouldForwardToLocalClients is set when this announce was accepted into
	// the path table (new or equal-or-shorter path). Only then does the shared
	// instance retransmit it to co-located clients (Python Transport.py:1790,
	// inside the should_add block).
	shouldForwardToLocalClients := false
	func() {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.ensureStateLocked()
		if rate, ok := ts.announceRateTable[destHash]; ok {
			rate.Last = time.Now()
			rate.Timestamps = append(rate.Timestamps, rate.Last)
			if len(rate.Timestamps) > 32 {
				rate.Timestamps = rate.Timestamps[len(rate.Timestamps)-32:]
			}
		} else {
			now := time.Now()
			ts.announceRateTable[destHash] = &AnnounceRateEntry{
				Last:       now,
				Timestamps: []time.Time{now},
			}
		}

		// Extract random blob from announce data for replay protection.
		// The random blob is at packet.Data[KEYSIZE/8 + NAME_HASH_LENGTH/8 : +10].
		var randomBlob []byte
		randomBlobStart := IdentityKeySize/8 + NameHashLength/8
		randomBlobEnd := randomBlobStart + 10
		if len(packet.Data) >= randomBlobEnd {
			randomBlob = make([]byte, 10)
			copy(randomBlob, packet.Data[randomBlobStart:randomBlobEnd])
		}

		// Check if we already have a path
		if entry, ok := ts.pathTable[destHash]; ok {
			// Decide whether to replace the existing path table entry,
			// mirroring RNS/Transport.py:1821-1891 (v1.4.1). The shouldReplace
			// flag is set by either the shorter-or-equal-hops branch or the
			// longer-hops branch, and the shared update code runs once after.
			announceEmitted := announceEmissionFromPacket(packet)
			pathTimebase := timebaseFromRandomBlobs(entry.RandomBlobs)
			newBlob := randomBlob != nil && !containsBlob(entry.RandomBlobs, randomBlob)

			shouldReplace := false
			if packet.Hops <= entry.Hops {
				// Shorter or equal hops (Transport.py:1830-1845):
				// a previously-unseen announce with a newer emission timebase
				// always replaces (after marking the path unknown); an announce
				// with the *same* timebase only replaces when it arrived on a
				// higher-gravity interface than the current path entry.
				if newBlob && announceEmitted > pathTimebase {
					ts.markPathUnknownStateLocked(destHash)
					shouldReplace = true
				} else if announceEmitted == pathTimebase {
					if entry.Interface != nil {
						currentGravity := entry.Interface.Gravity()
						announceGravity := iface.Gravity()
						if announceGravity > currentGravity {
							ts.logger.Pathing("Replacing path table entry for %x with new announce due to higher gravity (%v->%v)", packet.DestinationHash, currentGravity, announceGravity)
							shouldReplace = true
						}
					}
				}
			} else {
				// More hops than existing (Transport.py:1846-1891):
				// a longer-hop announce is ignored unless the path is expired,
				// the emission is more recent (with a new random blob), or the
				// existing path has been marked unresponsive.
				now := time.Now()
				if !now.Before(entry.Expires) {
					if newBlob {
						ts.logger.Pathing("Replacing path table entry for %x with new announce due to expired path", packet.DestinationHash)
						ts.markPathUnknownStateLocked(destHash)
						shouldReplace = true
					}
				} else if announceEmitted > pathTimebase {
					if newBlob {
						ts.logger.Pathing("Replacing path table entry for %x with new announce, since it was more recently emitted", packet.DestinationHash)
						ts.markPathUnknownStateLocked(destHash)
						shouldReplace = true
					}
				} else if announceEmitted == pathTimebase {
					if entry.Unresponsive {
						ts.logger.Pathing("Replacing path table entry for %x with new announce, since previously tried path was unresponsive", packet.DestinationHash)
						shouldReplace = true
					}
				}
			}

			if shouldReplace {
				nextHop, err := nextHopFromAnnounce(packet)
				if err != nil {
					ts.logger.Debug("Announce next-hop extraction failed for %x: %v", packet.DestinationHash, err)
					return
				}
				entry.Timestamp = time.Now()
				entry.Hops = packet.Hops
				entry.NextHop = nextHop
				entry.Interface = iface
				entry.InterfaceName = iface.Name()
				entry.IfaceHash = interfaceHash(iface)
				entry.Expires = time.Now().Add(pathExpiryForInterface(iface))
				// Cache the raw announce so a later path request for this
				// destination can be answered from the known path (see
				// handlePathRequest's cached-path branch) instead of relaying
				// the request all the way to the remote node. Mirrors Python
				// Reticulum caching the announce packet at IDX_PT_PACKET.
				entry.Packet = copyBytes(packet.Raw)
				entry.PacketHash = append([]byte(nil), packet.GetHash()...)
				if randomBlob != nil && !containsBlob(entry.RandomBlobs, randomBlob) {
					entry.RandomBlobs = append(entry.RandomBlobs, randomBlob)
					if len(entry.RandomBlobs) > maxRandomBlobs {
						entry.RandomBlobs = entry.RandomBlobs[len(entry.RandomBlobs)-maxRandomBlobs:]
					}
				}
				shouldForwardToLocalClients = true
			}
		} else {
			nextHop, err := nextHopFromAnnounce(packet)
			if err != nil {
				ts.logger.Debug("Announce next-hop extraction failed for %x: %v", packet.DestinationHash, err)
				return
			}
			// New path
			var blobs [][]byte
			if randomBlob != nil {
				blobs = [][]byte{randomBlob}
			}
			ts.pathTable[destHash] = &PathEntry{
				Timestamp:     time.Now(),
				NextHop:       nextHop,
				Hops:          packet.Hops,
				RandomBlobs:   blobs,
				Interface:     iface,
				InterfaceName: iface.Name(),
				IfaceHash:     interfaceHash(iface),
				Expires:       time.Now().Add(pathExpiryForInterface(iface)),
				Packet:        copyBytes(packet.Raw),
				PacketHash:    append([]byte(nil), packet.GetHash()...),
			}
			// Mark the freshly-inserted path's responsiveness state as
			// "unknown", mirroring RNS/Transport.py:2053 where
			// Transport.mark_path_unknown_state runs immediately after the new
			// path_table entry is written. The Go PathEntry zero value already
			// encodes the unknown state (ResponsiveState == 0), so this is a
			// parity call that also defends against any future constructor
			// that seeds a non-default state; markPathUnknownStateLocked is a
			// no-op when the entry is absent (Python Transport.py:2826).
			ts.markPathUnknownStateLocked(destHash)
			ts.logger.Info("Learned path to %x via %v, %v hops", packet.DestinationHash, iface.Name(), packet.Hops)
			shouldForwardToLocalClients = true
		}

		// When this announce installed or refreshed a path for a destination
		// this node has an outstanding path request for, mark the destination
		// in-use, mirroring Python Transport.py:2056-2057
		// (`if packet.destination_hash in Transport.path_requests:
		// RNS.Reticulum.get_instance()._used_destination_data(...)`). The
		// check runs after the path install and is gated on an actual install
		// (shouldForwardToLocalClients is set only in the install/replace
		// branches). The lock is held here, so the lock-held core is safe.
		if shouldForwardToLocalClients {
			if _, hasPR := ts.pathRequests[destHash]; hasPR {
				ts.usedDestinationDataLocked([]byte(destHash))
			}
		}

		// Propagation logic (re-broadcasting announces). Python gates the
		// announce_table insertion on
		//   (RNS.Reticulum.transport_enabled() or is_from_local_client)
		//   and packet.context != RNS.Packet.PATH_RESPONSE
		// (Transport.py:1948). A standalone node with enable_transport=False
		// has transport_enabled=False and is_from_local_client=False, so it
		// must NOT rebroadcast. The Go port previously gated on
		// !connectedToSharedInstance instead, which caused standalone
		// non-transport nodes to rebroadcast announces using an ephemeral
		// transport identity. Other nodes receiving these rebroadcasts
		// learned paths pointing to the ephemeral identity — paths that could
		// never be used because the non-transport node does not forward
		// transport-id-addressed packets (Inbound's transport-handling block
		// is gated on ts.Enabled()). This was a root cause of "No path to
		// destination known": the node knew the destination (from the announce
		// handler) but the learned path pointed to a non-functional ephemeral
		// next-hop. ts.enabled is read directly (the lock is already held
		// here); isLocalClientInterface does not acquire ts.mu.
		isFromLocalClient := ts.isLocalClientInterface(iface)
		if (ts.enabled || isFromLocalClient) && packet.Context != ContextPathResponse {
			raw := make([]byte, len(packet.Raw))
			copy(raw, packet.Raw)
			// Python (Transport.py:1927,632): announce_hops = packet.hops
			// (the already-incremented value from inbound), and the rebroadcast
			// packet carries raw[1] = announce_hops. Using packet.Hops + 1
			// here double-incremented the hop count, inflating it by 1 at every
			// rebroadcast hop and compounding across multi-hop paths (N actual
			// hops showed as 2N-1 instead of N).
			hops := packet.Hops
			if len(raw) > 1 {
				raw[1] = byte(hops)
			}

			existing, ok := ts.announceTable[destHash]
			if !ok || hops <= existing.Hops {
				ts.announceTable[destHash] = &AnnounceEntry{
					PacketRaw:         raw,
					SourceInterface:   iface,
					Hops:              hops,
					NextRebroadcastAt: time.Now().Add(pathfinderGrace + ts.randomDuration(pathfinderRandomWindow)),
					Retries:           0,
				}
			}
		}

		// Copy handlers to call them without the lock
		if len(ts.announceHandlers) > 0 {
			handlers = make([]*AnnounceHandler, len(ts.announceHandlers))
			copy(handlers, ts.announceHandlers)
		}
	}()

	// Forward the accepted announce to co-located clients of this shared
	// instance. Clients own no network interfaces, so without this forwarding
	// they would never learn any path and could not inject their own packets
	// (link requests, messages) into transport via the shared instance.
	if shouldForwardToLocalClients {
		ts.forwardAnnounceToLocalClients(packet, iface)
	}

	// Call announce handlers
	if len(handlers) > 0 {
		announceIdentity := ts.RecallNoUse(packet.DestinationHash)
		if announceIdentity != nil {
			for _, handler := range handlers {
				executeCallback := false
				if handler.AspectFilter == "" {
					executeCallback = true
				} else {
					parts := strings.Split(handler.AspectFilter, ".")
					appName := parts[0]
					aspects := parts[1:]
					expectedHash := CalculateHash(announceIdentity, appName, aspects...)
					if bytes.Equal(packet.DestinationHash, expectedHash) {
						executeCallback = true
					}
				}

				if packet.Context == ContextPathResponse && !handler.ReceivePathResponses {
					executeCallback = false
				}

				if executeCallback {
					isPathResponse := packet.Context == ContextPathResponse
					if handler.ReceivedAnnounceWithContext != nil {
						handler.ReceivedAnnounceWithContext(packet.DestinationHash, announceIdentity, announceIdentity.AppData, isPathResponse)
					} else if handler.ReceivedAnnounce != nil {
						handler.ReceivedAnnounce(packet.DestinationHash, announceIdentity, announceIdentity.AppData)
					}
				}
			}
		}
	}
}

// shouldHoldAnnounce is the ingress-limit gate for inbound announces
// (Python Transport.py:1752-1765). It returns true when the announce must be
// dropped from further processing because the receiving interface is holding it
// pending release by the interface-jobs loop.
//
// The gate applies only to announces for destinations that are not yet in the
// path table: already-known destinations have re-announces controlled by normal
// announce rate limiting. A destination with a pending path request bypasses
// the gate so path-finding is never starved by ingress limiting. The
// discovery_path_requests half of the bypass (Transport.py:1759) is pending the
// discovery port — no such map exists in the Go port yet.
//
// The path-table and pending-path-request lookups run under ts.mu; the
// ShouldIngressLimit/HoldAnnounce calls on the interface run outside the lock
// because they acquire the interface's own mutex and never touch ts.mu (so
// there is no lock-ordering hazard with handleAnnounce's outer critical
// section, which is already released by the time this is called).
func (ts *TransportSystem) shouldHoldAnnounce(packet *Packet, iface interfaces.Interface, destHash string) bool {
	if iface == nil {
		return false
	}
	known, pending, originated := func() (bool, bool, bool) {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		ts.ensureStateLocked()
		_, known := ts.pathTable[destHash]
		_, pending := ts.pendingPathRequests[destHash]
		_, originated := ts.pathRequests[destHash]
		return known, pending, originated
	}()
	// Mirrors Python Transport.py:1701-1706, which bypasses the ingress-limit
	// hold for announces whose destination this node ORIGINATED a path request
	// for (Transport.path_requests) OR is currently relaying one for
	// (Transport.discovery_path_requests). Go previously bypassed only the
	// relayed table (pendingPathRequests), so a path-response announce for a
	// destination the node itself requested could still be held on an
	// ingress-limiting interface.
	if known || pending || originated {
		return false
	}
	if !iface.ShouldIngressLimit() {
		return false
	}
	// Store a defensive copy of the raw frame: the held announce is re-injected
	// into Inbound later by the interface-jobs loop, and packet.Raw is the
	// already-IFAC-stripped frame (matching Python's packet.raw, which is set
	// from the post-IFAC bytes in Transport.inbound). Re-injection via Inbound
	// faithfully re-runs the IFAC step, mirroring Python's
	// RNS.Transport.inbound(packet.raw, receiving_interface).
	rawCopy := append([]byte(nil), packet.Raw...)
	iface.HoldAnnounce(rawCopy, iface, packet.Hops, packet.DestinationHash)
	ts.logger.Debug("Holding announce for %x on %v due to ingress limiting", packet.DestinationHash, iface.Name())
	return true
}

// Outbound sends a packet over the network.
// recordOutboundPR records an outgoing path request on iface when the packet
// is a PLAIN outbound path request, mirroring Python Transport.py:1354
// (`if packet.destination.type == RNS.Destination.PLAIN and packet.is_outbound_pr:
// interface.sent_path_request()`). Called after each successful transmit in
// Outbound's branches.
func (ts *TransportSystem) recordOutboundPR(packet *Packet, iface interfaces.Interface) {
	if packet.DestinationType == DestinationPlain && packet.IsOutboundPR && iface != nil {
		iface.SentPathRequest()
	}
}

func (ts *TransportSystem) Outbound(packet *Packet) error {
	if !packet.Packed {
		if err := packet.Pack(); err != nil {
			return err
		}
	}

	ts.mu.Lock()
	attachedIface := packet.AttachedInterface
	interfacesSnapshot := append([]interfaces.Interface(nil), ts.interfaces...)
	pathEntry, hasPath := ts.pathTable[string(packet.DestinationHash)]
	ts.mu.Unlock()

	// Link packets (resource advertise/part/request/proof, etc.) are built with
	// NewPacket(link, ...) and sent via p.Send() rather than Link.send, so they
	// reach Outbound without AttachedInterface set. For a link directly attached
	// to a single interface — most importantly a shared-instance local client,
	// whose socket lives on a spawned LocalClientInterface that is NOT in
	// ts.interfaces — the broadcast fallback below would hit the
	// LocalServerInterface listener whose Send is a no-op, silently dropping the
	// packet. Resolve the attached interface from the local link so branch 1
	// routes it directly, mirroring Python where Link.send stamps
	// packet.attached_interface before Transport.outbound runs. Multi-hop links
	// have no attached interface (nil) and fall through to the path/broadcast
	// branches unchanged.
	if attachedIface == nil && packet.DestinationType == DestinationLink {
		if link := ts.FindLink(packet.DestinationHash); link != nil {
			attachedIface = link.AttachedInterface()
		}
	}

	if attachedIface != nil {
		raw := packet.Raw
		if ifac, ok := attachedIface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err == nil {
				raw = processed
			}
		}
		if err := attachedIface.Send(raw); err != nil {
			ts.logger.Error("Could not transmit on %v: %v", attachedIface.Name(), err)
		}
		ts.recordOutboundPR(packet, attachedIface)

		ts.mu.Lock()
		packet.Sent = true
		packet.SentAt = float64(time.Now().UnixNano()) / 1e9
		if packet.Receipt != nil {
			packet.Receipt.MarkSent(packet.SentAt)
			// Register in TransportSystem if it's a DATA packet
			if packet.PacketType == PacketData &&
				packet.DestinationType != DestinationPlain &&
				!(packet.Context >= ContextKeepalive && packet.Context <= ContextLrproof) &&
				!(packet.Context >= ContextResource && packet.Context <= ContextResourceRcl) {
				ts.receipts = append(ts.receipts, packet.Receipt)
			}
		}
		ts.mu.Unlock()
		return nil
	}

	if hasPath && packet.PacketType != PacketAnnounce && packet.DestinationType != DestinationPlain && packet.DestinationType != DestinationGroup && pathEntry != nil && pathEntry.Interface != nil {
		raw := packet.Raw
		if pathEntry.Hops > 1 && len(pathEntry.NextHop) == TruncatedHashLength/8 {
			newFlags := byte((Header2 << 6) | (packet.ContextFlag << 5) | (TransportForward << 4) | (packet.DestinationType << 2) | packet.PacketType)
			newRaw := make([]byte, 0, len(packet.Raw)+TruncatedHashLength/8)
			newRaw = append(newRaw, newFlags, packet.Raw[1])
			newRaw = append(newRaw, pathEntry.NextHop...)
			newRaw = append(newRaw, packet.Raw[2:]...)
			raw = newRaw
		}

		if ifac, ok := pathEntry.Interface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err != nil {
				ts.logger.Error("Could not apply IFAC egress on %v: %v", pathEntry.Interface.Name(), err)
				return nil
			}
			raw = processed
		}

		if err := pathEntry.Interface.Send(raw); err != nil {
			ts.logger.Error("Could not transmit on %v: %v", pathEntry.Interface.Name(), err)
			ts.InvalidatePath(packet.DestinationHash)
		}
		ts.recordOutboundPR(packet, pathEntry.Interface)

		ts.mu.Lock()
		packet.Sent = true
		packet.SentAt = float64(time.Now().UnixNano()) / 1e9
		if packet.Receipt != nil {
			packet.Receipt.MarkSent(packet.SentAt)
			// Register in TransportSystem if it's a DATA packet
			if packet.PacketType == PacketData &&
				packet.DestinationType != DestinationPlain &&
				!(packet.Context >= ContextKeepalive && packet.Context <= ContextLrproof) &&
				!(packet.Context >= ContextResource && packet.Context <= ContextResourceRcl) {
				ts.receipts = append(ts.receipts, packet.Receipt)
			}
		}
		ts.mu.Unlock()
		return nil
	}

	for _, iface := range interfacesSnapshot {
		raw := packet.Raw
		if ifac, ok := iface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err != nil {
				ts.logger.Error("Could not apply IFAC egress on %v: %v", iface.Name(), err)
				continue
			}
			raw = processed
		}

		if err := iface.Send(raw); err != nil {
			ts.logger.Error("Could not transmit on %v: %v", iface.Name(), err)
			ts.InvalidatePathsViaInterface(iface)
		}
		ts.recordOutboundPR(packet, iface)
	}

	ts.mu.Lock()
	packet.Sent = true
	packet.SentAt = float64(time.Now().UnixNano()) / 1e9
	if packet.Receipt != nil {
		packet.Receipt.MarkSent(packet.SentAt)
		// Register in TransportSystem if it's a DATA packet
		if packet.PacketType == PacketData &&
			packet.DestinationType != DestinationPlain &&
			!(packet.Context >= ContextKeepalive && packet.Context <= ContextLrproof) &&
			!(packet.Context >= ContextResource && packet.Context <= ContextResourceRcl) {
			ts.receipts = append(ts.receipts, packet.Receipt)
		}
	}
	ts.mu.Unlock()
	return nil
}

// CleanCache performs a single sweep over the packet-hash cache, removing
// entries that are older than the cache timeout. It is a no-op when the
// transport is connected to a shared instance, matching Python's
// Transport.clean_cache(). The sweep is guarded by a non-reentrant lock so
// overlapping calls postpone instead of running concurrently.
func (ts *TransportSystem) CleanCache() {
	ts.cleanCache()
}

// cleanCache is the Go port of Python's Transport.clean_cache
// (Transport.py:2598-2615). A client of a shared instance never cleans. The
// cache_clean_lock is acquired non-blocking (TryLock): if a sweep is already
// in flight the call postpones until the next scheduler interval instead of
// queueing a second concurrent sweep.
func (ts *TransportSystem) cleanCache() {
	ts.mu.Lock()
	connected := ts.connectedToSharedInstance
	ts.mu.Unlock()
	if connected {
		return
	}
	if !ts.cacheCleanMu.TryLock() {
		if ts.logger != nil {
			ts.logger.Debug("Cache clean job still running, postponing until next scheduler interval")
		}
		return
	}
	defer ts.cacheCleanMu.Unlock()
	ts.cleanAnnounceCache()
	ts.mu.Lock()
	ts.cacheLastCleaned = time.Now()
	ts.mu.Unlock()
}

// cleanAnnounceCache removes packet-hash cache entries older than the cache
// timeout (30 minutes). It mirrors Python's Transport.clean_announce_cache
// (Transport.py:2617-2636): the expired keys are snapshotted under a short
// lock, then each is re-checked and deleted under a per-entry lock while
// yielding between entries so a large sweep stays low priority and never
// blocks the transport's main critical section.
func (ts *TransportSystem) cleanAnnounceCache() {
	ts.mu.Lock()
	if ts.packetHashes == nil {
		ts.mu.Unlock()
		return
	}
	now := time.Now()
	type cachedEntry struct {
		key string
		t   time.Time
	}
	expired := make([]cachedEntry, 0, len(ts.packetHashes))
	for k, t := range ts.packetHashes {
		if now.Sub(t) > 30*time.Minute {
			expired = append(expired, cachedEntry{key: k, t: t})
		}
	}
	ts.mu.Unlock()

	sleep := ts.cacheCleanSleep
	if sleep == nil {
		sleep = time.Sleep
	}
	for _, e := range expired {
		ts.mu.Lock()
		// Re-check under the lock: another goroutine may have refreshed the
		// entry's timestamp since the snapshot.
		if t, ok := ts.packetHashes[e.key]; ok && now.Sub(t) > 30*time.Minute {
			delete(ts.packetHashes, e.key)
		}
		ts.mu.Unlock()
		// Low-priority yield between entries (Python Transport.py:2636).
		sleep(cacheCleanYieldSleep)
	}
}

// DestinationTimeout is how long a destination or tunnel table entry is
// retained after last use (Python Transport.DESTINATION_TIMEOUT,
// Transport.py:89). One week.
const DestinationTimeout = 7 * 24 * time.Hour

// UnusedDestinationLinger is how long a pathless, never-used known
// destination lingers after its last announce before
// CleanKnownDestinations drops it (Python Transport.UNUSED_DESTINATION_LINGER,
// Transport.py:93). Six minutes.
const UnusedDestinationLinger = 6 * time.Minute

// KnownDestinationsInterval is the maintenance-loop period between
// CleanKnownDestinations sweeps (Python Transport.known_destinations_interval,
// Transport.py:190). Five minutes.
const KnownDestinationsInterval = 5 * time.Minute

// TunnelTimeout is how long a synthesized tunnel table entry is retained
// after it was last established/reconfirmed (Python Transport.TUNNEL_TIMEOUT,
// Transport.py:94). Eight hours.
const TunnelTimeout = 8 * time.Hour

// TunnelPathTimeout is how long an individual tunnel-path entry is retained
// after its last announce before the cull drops it (Python
// Transport.TUNNEL_PATH_TIMEOUT, Transport.py:95). Eight hours.
const TunnelPathTimeout = 8 * time.Hour

// Tunnel represents a synthesized Reticulum tunnel that exposes a
// virtual interface to a remote network over an existing link. It is
// the Go port of the Python Transport.tunnels[tunnel_id] entry
// [tunnel_id, interface, paths, expires].
type Tunnel struct {
	ID        []byte
	Interface interfaces.Interface
	Paths     map[string]*PathEntry
	Expires   time.Time
}

// tunnelTableFile returns the path of the on-disk tunnel table.
func tunnelTableFile(storagePath string) string {
	return filepath.Join(storagePath, "tunnels")
}

// SynthesizeTunnel builds and sends a tunnel-establishment packet on the
// given interface, requesting a remote transport to synthesize a tunnel back
// (Python Transport.synthesize_tunnel, Transport.py:2366-2418). The whole
// body is wrapped in error handling: any failure (missing transport identity,
// sign error, packet error) is logged and the transport keeps running, and a
// deferred recover guards against a panic so a malformed interface cannot
// crash the maintenance path (Python except clause at Transport.py:2417).
func (ts *TransportSystem) SynthesizeTunnel(iface interfaces.Interface) {
	if ts == nil {
		return
	}
	logger := ts.GetLogger()
	defer func() {
		if rec := recover(); rec != nil {
			if logger != nil {
				// Use %T, not iface.Name(): the panic may originate in
				// Name() itself, and calling it again here would re-panic
				// out of the recover handler.
				logger.Error("Could not synthesize tunnel for %T: %v", iface, rec)
			}
		}
	}()
	if iface == nil {
		return
	}
	if ts.identity == nil {
		if logger != nil {
			logger.Error("Could not synthesize tunnel for %v: no transport identity", iface.Name())
		}
		return
	}
	interfaceHash := interfaceHash(iface)
	publicKey := ts.identity.GetPublicKey()
	randomHash, err := RandomHash()
	if err != nil {
		if logger != nil {
			logger.Error("Could not synthesize tunnel for %v: %v", iface.Name(), err)
		}
		return
	}
	tunnelIDData := append(append([]byte{}, publicKey...), interfaceHash...)
	signedData := append(append([]byte{}, tunnelIDData...), randomHash...)
	signature, err := ts.identity.Sign(signedData)
	if err != nil {
		if logger != nil {
			logger.Error("Could not synthesize tunnel for %v: %v", iface.Name(), err)
		}
		return
	}
	data := append(append([]byte{}, signedData...), signature...)

	dst, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "tunnel", "synthesize")
	if err != nil {
		if logger != nil {
			logger.Error("Could not synthesize tunnel for %v: %v", iface.Name(), err)
		}
		return
	}
	packet := NewPacket(dst, data)
	packet.TransportType = TransportBroadcast
	packet.AttachedInterface = iface
	if err := packet.Pack(); err != nil {
		if logger != nil {
			logger.Error("Could not synthesize tunnel for %v: %v", iface.Name(), err)
		}
		return
	}
	if err := ts.Outbound(packet); err != nil {
		if logger != nil {
			logger.Error("Could not synthesize tunnel for %v: %v", iface.Name(), err)
		}
	}
}

// VoidTunnelInterface clears the interface of a synthesized tunnel,
// leaving the entry (and its paths) in place (Python
// Transport.void_tunnel_interface, Transport.py:2165-2168).
func (ts *TransportSystem) VoidTunnelInterface(tunnelID []byte) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if entry, ok := ts.tunnels[string(tunnelID)]; ok {
		entry.Interface = nil
	}
}

// SaveTunnelTable writes the in-memory tunnel table to disk. It is
// the Go port of Python's Transport.save_tunnel_table().
func (ts *TransportSystem) SaveTunnelTable(storagePath string) error {
	if ts == nil {
		return errors.New("nil transport")
	}
	if storagePath == "" {
		return nil
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.tunnels) == 0 {
		return nil
	}
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return err
	}
	// For now, write an empty marker file; full tunnel table persistence
	// is wired in a later task.
	path := tunnelTableFile(storagePath)
	return os.WriteFile(path, []byte("{}"), 0o644)
}

// HandleTunnel registers an interface as a synthesized tunnel endpoint
// (Python Transport.handle_tunnel, Transport.py:2171-2178). For a new
// tunnel_id it creates the entry [tunnel_id, interface, paths{}, expires];
// for an existing one it restores the interface and expiry. Unlike the old
// int-based stub, it does NOT re-register the interface in
// Transport.interfaces — Python's handle_tunnel never does that; the
// receiving interface is already registered.
func (ts *TransportSystem) HandleTunnel(tunnelID []byte, iface interfaces.Interface) error {
	if iface == nil {
		return errors.New("nil interface")
	}
	// Guard against a missing/empty tunnel ID so handle_tunnel does not
	// create a malformed entry keyed on the empty string (Python
	// Transport.handle_tunnel, Transport.py:2421-2422 keys on tunnel_id).
	if len(tunnelID) == 0 {
		return errors.New("empty tunnel ID")
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tunnels == nil {
		ts.tunnels = map[string]*Tunnel{}
	}
	key := string(tunnelID)
	// Python Transport.py:2422 sets expires = time.time() + TUNNEL_TIMEOUT
	// (8h), not the week-long DESTINATION_TIMEOUT.
	expires := time.Now().Add(TunnelTimeout)
	if entry, ok := ts.tunnels[key]; ok {
		entry.Interface = iface
		entry.Expires = expires
		return nil
	}
	ts.tunnels[key] = &Tunnel{
		ID:        copyBytes(tunnelID),
		Interface: iface,
		Paths:     map[string]*PathEntry{},
		Expires:   expires,
	}
	return nil
}

// interfaceRegisteredLocked reports whether iface is currently registered in
// ts.interfaces. Caller must hold ts.mu.
func (ts *TransportSystem) interfaceRegisteredLocked(iface interfaces.Interface) bool {
	return slices.Contains(ts.interfaces, iface)
}

// cullTunnels is the tunnel-table cull job run from the maintenance loop
// (Python Transport.jobs "Cull the tunnel table", Transport.py:824-877). It
// removes tunnels with an excessive or expired TUNNEL_TIMEOUT, nulls the
// interface of a tunnel whose interface is no longer registered, and drops
// individual tunnel paths that are either past TUNNEL_PATH_TIMEOUT or
// superseded by an active path-table entry with a more recent announce
// timebase (Transport.py:851-867).
func (ts *TransportSystem) cullTunnels(now time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.tunnels) == 0 {
		return
	}
	ts.ensureStateLocked()
	var staleTunnels []string
	for id, tunnel := range ts.tunnels {
		// Excessive expiry (Transport.py:833-834) or past expiry
		// (Transport.py:837-838): drop the whole tunnel.
		if tunnel.Expires.After(now.Add(2 * TunnelTimeout)) {
			staleTunnels = append(staleTunnels, id)
			continue
		}
		if now.After(tunnel.Expires) {
			staleTunnels = append(staleTunnels, id)
			continue
		}
		// Null the interface of a tunnel whose interface dropped off
		// (Transport.py:842-844).
		if tunnel.Interface != nil && !ts.interfaceRegisteredLocked(tunnel.Interface) {
			tunnel.Interface = nil
		}
		var stalePaths []string
		for destHash, tp := range tunnel.Paths {
			// TUNNEL_PATH_TIMEOUT expiry (Transport.py:851-852).
			if now.Sub(tp.Timestamp) > TunnelPathTimeout {
				stalePaths = append(stalePaths, destHash)
				continue
			}
			// Drop when the active path-table entry for the same
			// destination has a more recent announce timebase
			// (Transport.py:860-867). Guard against a missing active
			// path: no active path means nothing supersedes the tunnel
			// path, so it is retained.
			if active, ok := ts.pathTable[destHash]; ok {
				currentTimebase := timebaseFromRandomBlobs(active.RandomBlobs)
				tunnelTimebase := timebaseFromRandomBlobs(tp.RandomBlobs)
				if currentTimebase > tunnelTimebase {
					stalePaths = append(stalePaths, destHash)
				}
			}
		}
		for _, dh := range stalePaths {
			delete(tunnel.Paths, dh)
		}
	}
	for _, id := range staleTunnels {
		delete(ts.tunnels, id)
	}
}

// tunnelSynthesizeHandler is the packet callback for the inbound tunnel
// synthesis destination (Python Transport.tunnel_synthesize_handler,
// Transport.py:2141-2158). It expects a 176-byte payload laid out as
// public_key(64) + interface_hash(32) + random_hash(16) + signature(64),
// derives tunnel_id = FullHash(public_key+interface_hash), validates the
// signature over (public_key+interface_hash+random_hash) with the embedded
// public key, and on success dispatches to HandleTunnel on the packet's
// receiving interface. Any parse/validation failure is logged and dropped.
func (ts *TransportSystem) tunnelSynthesizeHandler(data []byte, packet *Packet) {
	keySize := IdentityKeySize / 8     // 64
	hashLen := len(FullHash(nil))      // 32 (FullHashLength/8)
	randLen := TruncatedHashLength / 8 // 16
	sigLen := 64                       // Ed25519 signature length
	expectedLength := keySize + hashLen + randLen + sigLen
	if len(data) != expectedLength {
		return
	}
	logger := ts.GetLogger()

	publicKey := data[:keySize]
	interfaceHash := data[keySize : keySize+hashLen]
	tunnelIDData := append(append([]byte{}, publicKey...), interfaceHash...)
	tunnelID := FullHash(tunnelIDData)
	randomHash := data[keySize+hashLen : keySize+hashLen+randLen]
	signature := data[keySize+hashLen+randLen : expectedLength]
	signedData := append(append([]byte{}, tunnelIDData...), randomHash...)

	remote, err := NewIdentity(false, logger)
	if err != nil {
		return
	}
	if err := remote.LoadPublicKey(publicKey); err != nil {
		return
	}
	if !remote.Verify(signature, signedData) {
		return
	}
	if packet == nil {
		return
	}
	if err := ts.HandleTunnel(tunnelID, packet.ReceivingInterface); err != nil {
		if logger != nil {
			logger.Debug("Error handling tunnel establishment: %v", err)
		}
	}
}

// MarkPathUnresponsive marks the path to the given destination hash
// as unresponsive. It is the Go port of Python's
// Transport.mark_path_unresponsive().
func (ts *TransportSystem) MarkPathUnresponsive(destinationHash []byte) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry, ok := ts.pathTable[string(destinationHash)]
	if !ok {
		return
	}
	entry.Unresponsive = true
	entry.ResponsiveState = 2
}

// MarkPathResponsive marks the path to the given destination hash as
// responsive. It is the Go port of Python's
// Transport.mark_path_responsive().
func (ts *TransportSystem) MarkPathResponsive(destinationHash []byte) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry, ok := ts.pathTable[string(destinationHash)]
	if !ok {
		return
	}
	entry.Unresponsive = false
	entry.ResponsiveState = 1
}

// MarkPathUnknownState resets the path's responsiveness state to
// "unknown". It is the Go port of Python's
// Transport.mark_path_unknown_state().
func (ts *TransportSystem) MarkPathUnknownState(destinationHash []byte) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.markPathUnknownStateLocked(string(destinationHash))
}

// markPathUnknownStateLocked is the lock-free inner core of
// MarkPathUnknownState, for callers already holding ts.mu.
func (ts *TransportSystem) markPathUnknownStateLocked(destHash string) {
	entry, ok := ts.pathTable[destHash]
	if !ok {
		return
	}
	entry.Unresponsive = false
	entry.ResponsiveState = 0
}

// PathIsUnresponsive reports whether the path to the given
// destination hash is currently marked unresponsive. It is the Go
// port of Python's Transport.path_is_unresponsive().
func (ts *TransportSystem) PathIsUnresponsive(destinationHash []byte) bool {
	if ts == nil {
		return false
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	entry, ok := ts.pathTable[string(destinationHash)]
	if !ok {
		return false
	}
	return entry.Unresponsive
}

// ExpirePath removes the path to the given destination hash from the
// path table. It is the Go port of Python's Transport.expire_path().
func (ts *TransportSystem) ExpirePath(destinationHash []byte) {
	if ts == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.pathTable, string(destinationHash))
}

// Packet-hashlist storage file names and yield thresholds, mirroring
// Python's Transport.save_packet_hashlist (RNS/Transport.py:3292-3329) and
// the load path (RNS/Transport.py:242-254).
const (
	packetHashRawName      = "packet_hashlist.raw"
	packetHashLegacyName   = "packet_hashlist"
	hashlistYieldThreshold = 10 * time.Millisecond
	hashlistYieldSleep     = time.Millisecond
)

// SavePacketHashlist serializes the in-memory packet-hash set to
// storagePath/packet_hashlist.raw as raw concatenated HashLength/8-byte
// hashes, mirroring Python's Transport.save_packet_hashlist
// (RNS/Transport.py:3292-3329). It is gated on transport enabled (Python
// lines 3294,3310); a disabled transport returns nil without writing.
func (ts *TransportSystem) SavePacketHashlist(storagePath string) error {
	return ts.savePacketHashlist(storagePath, false)
}

// savePacketHashlist is the background-aware implementation. When background
// is true it periodically yields (Python lines 3318-3322) so a low-priority
// background persist does not starve inbound processing.
func (ts *TransportSystem) savePacketHashlist(storagePath string, background bool) error {
	if ts == nil {
		return errors.New("nil transport")
	}
	if storagePath == "" {
		return nil
	}
	if !ts.Enabled() {
		return nil
	}
	hashLen := HashLength / 8

	ts.mu.Lock()
	keys := make([]string, 0, len(ts.packetHashes))
	for k := range ts.packetHashes {
		if len(k) == hashLen {
			keys = append(keys, k)
		}
	}
	ts.mu.Unlock()

	var buf bytes.Buffer
	buf.Grow(len(keys) * hashLen)
	roundStartedAt := time.Now()
	for _, k := range keys {
		buf.WriteString(k)
		if background && time.Since(roundStartedAt) > hashlistYieldThreshold {
			roundStartedAt = time.Now()
			time.Sleep(hashlistYieldSleep)
		}
	}

	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	return os.WriteFile(filepath.Join(storagePath, packetHashRawName), buf.Bytes(), 0o600)
}

// LoadPacketHashlist reads the packet-hash set from
// storagePath/packet_hashlist.raw (raw concatenated HashLength/8-byte
// hashes), falling back to the legacy msgpack packet_hashlist file for
// migration when the .raw file is absent. It mirrors Python's load path
// (RNS/Transport.py:242-254), gated on transport enabled. A parse failure
// logs and returns nil (Python catches the exception and continues).
func (ts *TransportSystem) LoadPacketHashlist(storagePath string) error {
	if ts == nil {
		return errors.New("nil transport")
	}
	if storagePath == "" {
		return nil
	}
	if !ts.Enabled() {
		return nil
	}
	hashLen := HashLength / 8

	rawPath := filepath.Join(storagePath, packetHashRawName)
	data, err := os.ReadFile(rawPath)
	if err == nil {
		return ts.loadPacketHashlistRaw(data, hashLen)
	}
	if !os.IsNotExist(err) {
		if ts.logger != nil {
			ts.logger.Error("Could not load packet hashlist from storage, the contained exception was: %v", err)
		}
		return nil
	}

	// Fall back to the legacy msgpack packet_hashlist file for migration
	// from pre-v1.4.0 storage (the old format was a msgpack array of
	// hash byte strings).
	legacyPath := filepath.Join(storagePath, packetHashLegacyName)
	legacy, err := os.ReadFile(legacyPath)
	if err != nil {
		if !os.IsNotExist(err) && ts.logger != nil {
			ts.logger.Error("Could not load legacy packet hashlist from storage, the contained exception was: %v", err)
		}
		return nil
	}
	return ts.loadPacketHashlistLegacy(legacy, hashLen)
}

// loadPacketHashlistRaw populates packetHashes from a raw concatenated
// hash file (Python RNS/Transport.py:246-252). Each HashLength/8-byte chunk
// is a packet hash; a short trailing remainder is ignored (Python stops when
// a read returns fewer than hashlen bytes).
func (ts *TransportSystem) loadPacketHashlistRaw(data []byte, hashLen int) error {
	now := time.Now()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	for i := 0; i+hashLen <= len(data); i += hashLen {
		ts.packetHashes[string(data[i:i+hashLen])] = now
	}
	return nil
}

// loadPacketHashlistLegacy migrates a pre-v1.4.0 msgpack packet_hashlist
// file (an array of hash byte strings) into packetHashes.
func (ts *TransportSystem) loadPacketHashlistLegacy(data []byte, hashLen int) error {
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		if ts.logger != nil {
			ts.logger.Error("Could not load packet hashlist from storage, the contained exception was: %v", err)
		}
		return nil
	}
	arr, ok := unpacked.([]any)
	if !ok {
		if ts.logger != nil {
			ts.logger.Error("Could not load packet hashlist from storage, the contained exception was: unexpected type %T", unpacked)
		}
		return nil
	}
	now := time.Now()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	for _, item := range arr {
		if h, ok := item.([]byte); ok && len(h) == hashLen {
			ts.packetHashes[string(h)] = now
		}
	}
	return nil
}

// SavePathTable writes the current path table to storagePath. It is
// the Go port of Python's Transport.save_path_table().
func (ts *TransportSystem) SavePathTable(storagePath string) error {
	if ts == nil {
		return errors.New("nil transport")
	}
	if storagePath == "" {
		return nil
	}
	snapshot, caches := ts.pathTableSnapshot()

	packed, err := msgpack.Pack(snapshot)
	if err != nil {
		return fmt.Errorf("pack path table: %w", err)
	}
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "destination_table"), packed, 0o600); err != nil {
		return fmt.Errorf("write path table: %w", err)
	}
	// Write the announce cache files that accompany the destination_table,
	// relative to this storagePath (which may differ from ts.storagePath when
	// callers persist to a custom directory). See pathTableSnapshot.
	cacheDir := announceCacheDirFor(storagePath)
	for _, c := range caches {
		writeCachedAnnounce(ts.logger, cacheDir, c.hash, c.raw, c.iface)
	}
	return nil
}

// PersistData triggers a save of the path table, packet-hash list, and
// tunnel table to the configured storage path. It is the Go port of
// Python's Transport.persist_data() (Transport.py:3387-3393), invoked both
// periodically from the job loop and once at shutdown from exit_handler.
func (ts *TransportSystem) PersistData() error {
	if ts == nil {
		return errors.New("nil transport")
	}
	if ts.storagePath == "" {
		return nil
	}
	// A client of a shared Reticulum instance must not persist these tables:
	// it shares the storage path with the shared instance, so writing would
	// clobber the shared instance's tables with the client's
	// (forwarded-announce-only) view. Python gates each save_* helper on
	// is_connected_to_shared_instance (Transport.py:3235/3198/3313); gating
	// once here covers all three.
	ts.mu.Lock()
	connected := ts.connectedToSharedInstance
	ts.mu.Unlock()
	if connected {
		return nil
	}
	// Non-reentrant guard: skip if a persist is already in flight (Python
	// Transport.py:3509-3510 "if persist_lock.locked(): return").
	if !ts.persistMu.TryLock() {
		return nil
	}
	defer ts.persistMu.Unlock()
	if err := ts.SavePathTable(ts.storagePath); err != nil {
		return err
	}
	if err := ts.SavePacketHashlist(ts.storagePath); err != nil {
		return err
	}
	return ts.SaveTunnelTable(ts.storagePath)
}

// DeregisterDestination removes a destination from the registered
// destinations list. It is the Go port of Python's
// Transport.deregister_destination().
func (ts *TransportSystem) DeregisterDestination(d *Destination) {
	if ts == nil || d == nil {
		return
	}
	ts.mu.Lock()
	defer ts.mu.Unlock()
	for i, existing := range ts.destinations {
		if existing == d {
			ts.destinations = append(ts.destinations[:i], ts.destinations[i+1:]...)
			delete(ts.destinationsMap, string(d.Hash))
			return
		}
	}
}

// CleanDestinationsMap reconciles the destinationsMap index with the
// destinations list: it re-adds any registered destination whose hash is
// missing from the map, and drops any map entry whose destination is no
// longer registered. It is the Go port of Python's
// Transport.clean_destinations_map (Transport.py:2478-2496), run as a
// periodic reconcile job so the hash index can never drift out of sync with
// the list even if a register/deregister path bypassed the map update.
func (ts *TransportSystem) CleanDestinationsMap() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()
	for _, d := range ts.destinations {
		if _, ok := ts.destinationsMap[string(d.Hash)]; !ok {
			ts.destinationsMap[string(d.Hash)] = d
		}
	}
	registered := make(map[string]bool, len(ts.destinations))
	for _, d := range ts.destinations {
		registered[string(d.Hash)] = true
	}
	for hash := range ts.destinationsMap {
		if !registered[hash] {
			delete(ts.destinationsMap, hash)
		}
	}
}

// interfaceStatsForRemote returns a basic interface-stats snapshot
// suitable for serving to remote management clients. It is the
// transport-side helper that the remote_status_handler uses.
func (ts *TransportSystem) interfaceStatsForRemote() map[string]any {
	ts.mu.Lock()
	ifaceCount := len(ts.interfaces)
	ts.mu.Unlock()
	return map[string]any{
		"interfaces": ifaceCount,
		"transport":  "rns-go",
		"running":    true,
	}
}

// AwaitPath blocks until a path to the given destination hash is
// known or the timeout elapses. It is the Go port of Python's
// Transport.await_path().
func (ts *TransportSystem) AwaitPath(destinationHash []byte, timeout time.Duration) ([]byte, error) {
	if ts == nil {
		return nil, errors.New("nil transport")
	}
	deadline := time.Now().Add(timeout)
	// awaitPathPollInterval bounds how long a single iteration can sleep
	// before re-checking the path table. It is intentionally short so the
	// caller is not woken long after a path becomes known; the sleep is
	// further capped to the remaining time so the deadline is respected
	// rather than overshot by a full interval.
	const awaitPathPollInterval = 5 * time.Millisecond
	for {
		ts.mu.Lock()
		_, ok := ts.pathTable[string(destinationHash)]
		ts.mu.Unlock()
		if ok {
			return destinationHash, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		if remaining > awaitPathPollInterval {
			remaining = awaitPathPollInterval
		}
		time.Sleep(remaining)
	}
}
