// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// Transport defines the required methods for any transport system within the Reticulum network.
type Transport interface {
	ActivateLink(l *Link)
	AnnounceHandlers() []*AnnounceHandler
	BlackholeIdentity(identityHash []byte, until *int64, reason string) bool
	DiscoverInterfaces()
	DiscoverInterfacesCallCount() int
	DropAnnounceQueues() int
	Enabled() bool
	FindLink(linkID []byte) *Link
	GetBlackholedIdentities() []map[string]any
	GetInterfaces() []interfaces.Interface
	GetPacketQ(packetHash []byte) (float64, bool)
	GetPacketRSSI(packetHash []byte) (float64, bool)
	GetPacketSNR(packetHash []byte) (float64, bool)
	GetPathEntry(destHash []byte) *PathInfo
	GetPathTable() []PathInfo
	GetRateTable() []map[string]any
	HasPath(destHash []byte) bool
	HopsTo(destHash []byte) int
	Identity() *Identity
	Inbound(raw []byte, iface interfaces.Interface)
	InvalidatePath(destHash []byte) bool
	InvalidatePathsViaNextHop(nextHop []byte) int
	LinkMTUDiscovery() bool
	LinkTable() map[string]*LinkEntry
	NetworkIdentityHash() []byte
	Outbound(packet *Packet) error
	RegisterAnnounceHandler(handler *AnnounceHandler)
	RegisterDestination(d *Destination)
	RegisterInterface(iface interfaces.Interface)
	RegisterLink(l *Link)
	RequestPath(destHash []byte) error
	SetEnabled(enabled bool)
	SetLinkMTUDiscovery(enabled bool)
	SetNetworkIdentity(identity *Identity)
	Start(storagePath string) error
	StartedAt() time.Time
	UnblackholeIdentity(identityHash []byte) bool
}

// TransportSystem manages routing, packet forwarding, and global state.
type TransportSystem struct {
	identity    *Identity
	networkID   *Identity
	storagePath string
	running     bool
	startedAt   time.Time
	stopCh      chan struct{}
	doneCh      chan struct{}

	pathRequestHash []byte

	interfaces   []interfaces.Interface
	destinations []*Destination

	pendingLinks []*Link
	activeLinks  []*Link

	// Routing tables
	pathTable            map[string]*PathEntry
	reverseTable         map[string]*ReverseEntry
	linkTable            map[string]*LinkEntry
	packetHashes         map[string]time.Time
	packetHashesPrev     map[string]time.Time
	packetHashRotateAt   int
	announceTable        map[string]*AnnounceEntry
	announceRateTable    map[string]*AnnounceRateEntry
	pathRequests         map[string]time.Time
	pendingPathRequests  map[string][]interfaces.Interface
	pendingPathRequestAt map[string]time.Time

	packetRSSICache map[string]float64
	packetSNRCache  map[string]float64
	packetQCache    map[string]float64

	blackholedIdentities map[string]BlackholeIdentityEntry
	discoverCalls        int
	announceHandlers     []*AnnounceHandler

	enabled          bool
	linkMTUDiscovery bool
	mu               sync.Mutex
}

// AnnounceEntry represents a stored network announce within the transport system.
type AnnounceEntry struct {
	PacketRaw         []byte
	SourceInterface   interfaces.Interface
	Hops              int
	NextRebroadcastAt time.Time
	Retries           int
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
	AspectFilter     string
	ReceivedAnnounce func(destinationHash []byte, announcedIdentity *Identity, appData []byte)
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

const (
	pathfinderRetries        = 1
	pathfinderGrace          = 5 * time.Second
	pathfinderRandomWindow   = 500 * time.Millisecond
	localRebroadcastsMax     = 2
	announceCheckInterval    = 1 * time.Second
	pathRequestMinInterval   = 20 * time.Second
	pathRequestCullAfter     = 2 * pathRequestMinInterval
	pendingPathRequestTTL    = 20 * time.Second
	pathTablePersistInterval = 30 * time.Second
	packetHashRotateDefault  = 50000
	reverseEntryTimeout      = 8 * time.Minute
	linkEntryTimeout         = 8 * time.Minute

	// establishmentTimeoutPerHop matches Python's
	// Link.ESTABLISHMENT_TIMEOUT_PER_HOP = Reticulum.DEFAULT_PER_HOP_TIMEOUT = 6 seconds.
	establishmentTimeoutPerHop = 6 * time.Second

	// maxRandomBlobs is the maximum number of random blobs per destination
	// for announce replay protection, matching Python's Transport.MAX_RANDOM_BLOBS.
	maxRandomBlobs = 64
)

// NewTransportSystem constructs an independent TransportSystem.
func NewTransportSystem() *TransportSystem {
	return &TransportSystem{
		interfaces:           make([]interfaces.Interface, 0),
		destinations:         make([]*Destination, 0),
		pendingLinks:         make([]*Link, 0),
		activeLinks:          make([]*Link, 0),
		pathTable:            make(map[string]*PathEntry),
		reverseTable:         make(map[string]*ReverseEntry),
		linkTable:            make(map[string]*LinkEntry),
		packetHashes:         make(map[string]time.Time),
		packetHashesPrev:     make(map[string]time.Time),
		packetHashRotateAt:   packetHashRotateDefault,
		announceTable:        make(map[string]*AnnounceEntry),
		announceRateTable:    make(map[string]*AnnounceRateEntry),
		pathRequests:         make(map[string]time.Time),
		pendingPathRequests:  make(map[string][]interfaces.Interface),
		pendingPathRequestAt: make(map[string]time.Time),
		packetRSSICache:      make(map[string]float64),
		packetSNRCache:       make(map[string]float64),
		packetQCache:         make(map[string]float64),
		blackholedIdentities: make(map[string]BlackholeIdentityEntry),
	}
}

func (ts *TransportSystem) Identity() *Identity {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.identity
}

func (ts *TransportSystem) StartedAt() time.Time {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.startedAt
}

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

// LinkMTUDiscovery returns whether link MTU discovery is enabled.
func (ts *TransportSystem) LinkMTUDiscovery() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.linkMTUDiscovery
}

// SetLinkMTUDiscovery sets whether link MTU discovery is enabled.
func (ts *TransportSystem) SetLinkMTUDiscovery(enabled bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.linkMTUDiscovery = enabled
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
	ts.running = true
	ts.startedAt = time.Now()

	ts.storagePath = storagePath
	if _, err := os.Stat(ts.storagePath); os.IsNotExist(err) {
		if err := os.MkdirAll(ts.storagePath, 0700); err != nil {
			ts.mu.Unlock()
			return err
		}
	}

	// Load or create transport identity
	identityPath := filepath.Join(ts.storagePath, "transport_identity")
	if _, err := os.Stat(identityPath); err == nil {
		id, err := FromFile(identityPath)
		if err != nil {
			Log(fmt.Sprintf("Could not load transport identity: %v", err), LogError, false)
		} else {
			ts.identity = id
			Log("Loaded Transport Identity from storage", LogVerbose, false)
		}
	}

	if ts.identity == nil {
		Log("No valid Transport Identity in storage, creating...", LogVerbose, false)
		id, err := NewIdentity(true)
		if err != nil {
			ts.mu.Unlock()
			return err
		}
		ts.identity = id
		if err := ts.identity.ToFile(identityPath); err != nil {
			Logf("Could not save transport identity: %v", LogError, false, err)
		}
	}
	ts.loadPathTableLocked()
	ts.mu.Unlock()

	// Setup control destinations
	pathRequestDst, err := NewDestination(ts, nil, DestinationIn, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		return err
	}
	ts.pathRequestHash = copyBytes(pathRequestDst.Hash)
	pathRequestDst.SetPacketCallback(ts.handlePathRequest)
	ts.RegisterDestination(pathRequestDst)

	// Start maintenance loop
	go ts.maintenance()

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
	ts.running = false
	ts.mu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
	if doneCh != nil {
		<-doneCh
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, iface := range ts.interfaces {
		if err := iface.Detach(); err != nil {
			Logf("Error detaching interface %v during transport stop: %v", LogError, false, iface.Name(), err)
		}
	}
	ts.interfaces = nil
	ts.pendingLinks = nil
	ts.activeLinks = nil
}

// SetNetworkIdentity sets the primary identity used by the transport system for network-level operations.
func (ts *TransportSystem) SetNetworkIdentity(identity *Identity) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.networkID = identity
	ts.identity = identity
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
	defer ts.mu.Unlock()
	ts.discoverCalls++
	// TODO - unimplemented
}

// DiscoverInterfacesCallCount returns the number of times the discovery interface process has been called.
func (ts *TransportSystem) DiscoverInterfacesCallCount() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.discoverCalls
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
	if handler == nil || handler.ReceivedAnnounce == nil {
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
	return iface.Type() == "LocalInterface" && strings.Contains(iface.Name(), "Local Client")
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

func (ts *TransportSystem) maintenance() {
	defer close(ts.doneCh)
	ratchetTicker := time.NewTicker(24 * time.Hour)
	announceTicker := time.NewTicker(announceCheckInterval)
	pathPersistTicker := time.NewTicker(pathTablePersistInterval)
	defer ratchetTicker.Stop()
	defer announceTicker.Stop()
	defer pathPersistTicker.Stop()

	// Initial clean
	CleanRatchets()

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
		case <-pathPersistTicker.C:
			ts.persistPathTable()
		case <-ratchetTicker.C:
			CleanRatchets()
		}
	}
}

func pathTableFile(storagePath string) string {
	return filepath.Join(storagePath, "destination_table")
}

func anyToInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		return int64(v), true
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int64(v), true
	default:
		return 0, false
	}
}

func (ts *TransportSystem) pathTableSnapshotLocked() []any {
	ts.ensureStateLocked()
	entries := make([]any, 0, len(ts.pathTable))
	for destHash, entry := range ts.pathTable {
		ifaceName := entry.InterfaceName
		if entry.Interface != nil {
			ifaceName = entry.Interface.Name()
		}
		// Convert RandomBlobs to []any for msgpack compatibility.
		blobs := make([]any, len(entry.RandomBlobs))
		for i, b := range entry.RandomBlobs {
			blobs[i] = b
		}
		entries = append(entries, []any{
			[]byte(destHash),
			entry.NextHop,
			entry.Hops,
			entry.Timestamp.UnixNano(),
			entry.Expires.UnixNano(),
			blobs,
			ifaceName,
			entry.Packet,
		})
	}
	return entries
}

func (ts *TransportSystem) persistPathTable() {
	ts.mu.Lock()
	if ts.storagePath == "" {
		ts.mu.Unlock()
		return
	}
	filePath := pathTableFile(ts.storagePath)
	snapshot := ts.pathTableSnapshotLocked()
	ts.mu.Unlock()

	packed, err := msgpack.Pack(snapshot)
	if err != nil {
		Logf("Failed to pack path table for persistence: %v", LogError, false, err)
		return
	}

	tmp := filePath + ".out"
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		Logf("Failed to create path table directory: %v", LogError, false, err)
		return
	}
	if err := os.WriteFile(tmp, packed, 0o600); err != nil {
		Logf("Failed to write path table temp file: %v", LogError, false, err)
		return
	}
	if err := os.Rename(tmp, filePath); err != nil {
		Logf("Failed to persist path table atomically: %v", LogError, false, err)
		return
	}
}

func (ts *TransportSystem) resolvePathInterfacesLocked() {
	interfaceByName := map[string]interfaces.Interface{}
	for _, iface := range ts.interfaces {
		interfaceByName[iface.Name()] = iface
	}
	for _, entry := range ts.pathTable {
		if entry.Interface != nil {
			entry.InterfaceName = entry.Interface.Name()
			continue
		}
		if entry.InterfaceName == "" {
			continue
		}
		if iface, ok := interfaceByName[entry.InterfaceName]; ok {
			entry.Interface = iface
		}
	}
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
			Logf("Failed reading path table from storage: %v", LogError, false, err)
		}
		return
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		Logf("Failed unpacking path table from storage: %v", LogError, false, err)
		return
	}

	list, ok := unpacked.([]any)
	if !ok {
		Log("Invalid persisted path table format; expected list", LogError, false)
		return
	}

	ts.pathTable = make(map[string]*PathEntry, len(list))
	for _, rawEntry := range list {
		fields, ok := rawEntry.([]any)
		if !ok || len(fields) < 7 {
			continue
		}

		// Support both old format (7 fields) and new format (8 fields with RandomBlobs).
		var (
			destHash  []byte
			nextHop   []byte
			hops64    int64
			ts64      int64
			exp64     int64
			blobs     [][]byte
			ifaceName string
			packetB   []byte
		)

		var ok1, ok2, ok3, ok4, ok5, ok6, ok7 bool
		destHash, ok1 = fields[0].([]byte)
		nextHop, ok2 = fields[1].([]byte)
		hops64, ok3 = anyToInt64(fields[2])
		ts64, ok4 = anyToInt64(fields[3])
		exp64, ok5 = anyToInt64(fields[4])

		if len(fields) >= 8 {
			// New format: field 5 is random blobs, 6 is iface name, 7 is packet.
			if rawBlobs, isSlice := fields[5].([]any); isSlice {
				for _, rb := range rawBlobs {
					if b, bOk := rb.([]byte); bOk {
						blobs = append(blobs, copyBytes(b))
					}
				}
			}
			ifaceName, ok6 = fields[6].(string)
			packetB, ok7 = fields[7].([]byte)
		} else {
			// Old format: field 5 is iface name, 6 is packet.
			ifaceName, ok6 = fields[5].(string)
			packetB, ok7 = fields[6].([]byte)
		}

		if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) {
			continue
		}

		entry := &PathEntry{
			Timestamp:     time.Unix(0, ts64),
			NextHop:       copyBytes(nextHop),
			Hops:          int(hops64),
			Expires:       time.Unix(0, exp64),
			RandomBlobs:   blobs,
			InterfaceName: ifaceName,
			Packet:        copyBytes(packetB),
		}
		ts.pathTable[string(destHash)] = entry
	}

	ts.resolvePathInterfacesLocked()
	Logf("Loaded %v path table entries from storage", LogVerbose, false, len(ts.pathTable))
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
	_, ok := ts.pathTable[destinationHash]
	if ok {
		delete(ts.pathTable, destinationHash)
	}
	delete(ts.announceTable, destinationHash)
	delete(ts.pathRequests, destinationHash)
	return ok
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

func (ts *TransportSystem) processAnnounceTable(now time.Time) {
	type sendJob struct {
		iface interfaces.Interface
		raw   []byte
	}

	jobs := make([]sendJob, 0)

	ts.mu.Lock()
	ts.ensureStateLocked()
	for destinationHash, entry := range ts.announceTable {
		if now.Before(entry.NextRebroadcastAt) {
			continue
		}

		if entry.Retries >= localRebroadcastsMax || entry.Retries > pathfinderRetries {
			delete(ts.announceTable, destinationHash)
			continue
		}

		for _, outIface := range ts.interfaces {
			if outIface == entry.SourceInterface {
				continue
			}
			raw := make([]byte, len(entry.PacketRaw))
			copy(raw, entry.PacketRaw)
			jobs = append(jobs, sendJob{iface: outIface, raw: raw})
		}

		entry.Retries++
		entry.NextRebroadcastAt = now.Add(pathfinderGrace + ts.randomDuration(pathfinderRandomWindow))
		if entry.Retries >= localRebroadcastsMax || entry.Retries > pathfinderRetries {
			delete(ts.announceTable, destinationHash)
		}
	}
	ts.mu.Unlock()

	for _, job := range jobs {
		raw := job.raw
		if ts.identity != nil {
			parsed := NewPacketFromRaw(job.raw)
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

		if err := job.iface.Send(raw); err != nil {
			Logf("Failed to re-broadcast announce on %v: %v", LogError, false, job.iface.Name(), err)
			ts.InvalidatePathsViaInterface(job.iface)
		}
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
	for _, existing := range requesters {
		if existing == iface {
			return true
		}
	}
	return false
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

	ts.mu.Lock()
	ts.ensureStateLocked()
	if !ts.hasPendingPathRequesterLocked(targetKey, source) {
		ts.pendingPathRequests[targetKey] = append(ts.pendingPathRequests[targetKey], source)
	}
	ts.pendingPathRequestAt[targetKey] = time.Now()
	ts.mu.Unlock()

	pathReqDst, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		Logf("Failed creating relay path request destination: %v", LogError, false, err)
		return
	}

	relayReq := NewPacket(pathReqDst, copyBytes(packet.Data))
	relayReq.TransportType = TransportBroadcast
	if err := relayReq.Pack(); err != nil {
		Logf("Failed packing relay path request packet: %v", LogError, false, err)
		return
	}

	type sendJob struct {
		iface interfaces.Interface
		raw   []byte
	}
	jobs := make([]sendJob, 0)
	ts.mu.Lock()
	ts.ensureStateLocked()
	for _, outIface := range ts.interfaces {
		if outIface == source {
			continue
		}
		raw := make([]byte, len(relayReq.Raw))
		copy(raw, relayReq.Raw)
		jobs = append(jobs, sendJob{iface: outIface, raw: raw})
	}
	ts.mu.Unlock()

	for _, job := range jobs {
		raw := job.raw
		if ifac, ok := job.iface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err != nil {
				Logf("Failed IFAC egress for forwarded path request on %v: %v", LogError, false, job.iface.Name(), err)
				continue
			}
			raw = processed
		}

		if err := job.iface.Send(raw); err != nil {
			Logf("Failed forwarding path request on %v: %v", LogError, false, job.iface.Name(), err)
			ts.InvalidatePathsViaInterface(job.iface)
		}
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

	forwarded := false
	for _, job := range jobs {
		raw := job.raw
		if ifac, ok := job.iface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err != nil {
				Logf("Failed IFAC egress for forwarded path response on %v: %v", LogError, false, job.iface.Name(), err)
				continue
			}
			raw = processed
		}

		if err := job.iface.Send(raw); err != nil {
			Logf("Failed forwarding path response on %v: %v", LogError, false, job.iface.Name(), err)
			ts.InvalidatePathsViaInterface(job.iface)
			continue
		}
		forwarded = true
	}

	return forwarded
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

func (ts *TransportSystem) handlePathRequest(data []byte, packet *Packet) {
	if len(data) < TruncatedHashLength/8 {
		return
	}

	targetHash := data[:TruncatedHashLength/8]
	Logf("Path request for %x", LogDebug, false, targetHash)

	ts.mu.Lock()
	var localDest *Destination
	for _, d := range ts.destinations {
		if bytes.Equal(d.Hash, targetHash) {
			localDest = d
			break
		}
	}
	ts.mu.Unlock()

	if localDest != nil {
		Logf("Answering path request for %x, destination is local", LogDebug, false, targetHash)
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
			Logf("Failed to build path response announce: %v", LogError, false, err)
			return
		}

		announcePacket.Context = ContextPathResponse
		announcePacket.HeaderType = Header2
		announcePacket.TransportType = TransportForward
		if ts.identity != nil {
			announcePacket.TransportID = copyBytes(ts.identity.Hash)
		}

		if err := announcePacket.Pack(); err != nil {
			Logf("Failed to pack path response announce: %v", LogError, false, err)
			return
		}

		if packet != nil && packet.ReceivingInterface != nil {
			raw := announcePacket.Raw
			if ifac, ok := packet.ReceivingInterface.(ifacOutboundHook); ok {
				processed, err := ifac.ApplyIFACOutbound(raw)
				if err != nil {
					Logf("Failed IFAC egress for path response on %v: %v", LogError, false, packet.ReceivingInterface.Name(), err)
					return
				}
				raw = processed
			}

			if err := packet.ReceivingInterface.Send(raw); err != nil {
				Logf("Failed to send path response announce on %v: %v", LogError, false, packet.ReceivingInterface.Name(), err)
				return
			}
			return
		}

		if err := ts.Outbound(announcePacket); err != nil {
			Logf("Failed broadcasting fallback path response announce: %v", LogError, false, err)
		}
	}
}

// RegisterDestination adds a destination to the transport system.
func (ts *TransportSystem) RegisterDestination(d *Destination) {
	if d.direction == DestinationIn {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		for _, existing := range ts.destinations {
			if bytes.Equal(d.Hash, existing.Hash) {
				Logf("Attempt to register an already registered destination %x", LogError, false, d.Hash)
				return
			}
		}
		ts.destinations = append(ts.destinations, d)
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
	ts.mu.Lock()
	defer ts.mu.Unlock()

	// Find in pending
	for i, pl := range ts.pendingLinks {
		if pl == l {
			// Remove from pending
			ts.pendingLinks = append(ts.pendingLinks[:i], ts.pendingLinks[i+1:]...)
			// Add to active
			ts.activeLinks = append(ts.activeLinks, l)
			Log(fmt.Sprintf("Activated link %x", l.linkID), LogVerbose, false)
			return
		}
	}
	Log(fmt.Sprintf("Attempted to activate a link %x that was not in the pending table", l.linkID), LogError, false)
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

// RegisterInterface adds a network interface to the transport system.
func (ts *TransportSystem) RegisterInterface(iface interfaces.Interface) {
	ts.mu.Lock()
	ts.interfaces = append(ts.interfaces, iface)
	ts.resolvePathInterfacesLocked()
	ts.mu.Unlock()

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
}

// GetInterfaces returns the list of network interfaces.
func (ts *TransportSystem) GetInterfaces() []interfaces.Interface {
	return ts.interfaces
}

// GetMutex returns the transport system's mutex.
// TODO: kill this
func (ts *TransportSystem) GetMutex() *sync.Mutex {
	return &ts.mu
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

// GetPacketRSSI returns RSSI metadata for a packet hash when available.
func (ts *TransportSystem) GetPacketRSSI(packetHash []byte) (float64, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	v, ok := ts.packetRSSICache[string(packetHash)]
	return v, ok
}

// GetPacketSNR returns SNR metadata for a packet hash when available.
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
		Reason:       reason,
	}
	if until != nil && *until > 0 {
		t := time.Unix(*until, 0)
		entry.Until = &t
	}
	ts.blackholedIdentities[string(identityHash)] = entry
	return true
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

// GetBlackholedIdentities returns the current local blackhole registry snapshot.
func (ts *TransportSystem) GetBlackholedIdentities() []map[string]any {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.ensureStateLocked()

	out := make([]map[string]any, 0, len(ts.blackholedIdentities))
	for _, entry := range ts.blackholedIdentities {
		item := map[string]any{
			"identity_hash": copyBytes(entry.IdentityHash),
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

// HasPath returns true if a path to the destination is known.
func (ts *TransportSystem) HasPath(destHash []byte) bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	_, ok := ts.pathTable[string(destHash)]
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
	if lastRequested, ok := ts.pathRequests[destinationHash]; ok && now.Sub(lastRequested) < pathRequestMinInterval {
		ts.mu.Unlock()
		Logf("Suppressing path request for %x due to minimum interval", LogDebug, false, destHash)
		return nil
	}
	ts.pathRequests[destinationHash] = now
	ts.mu.Unlock()

	requestTag := make([]byte, TruncatedHashLength/8)
	if _, err := rand.Read(requestTag); err != nil {
		return err
	}

	data := make([]byte, 0, len(destHash)+len(requestTag))
	data = append(data, destHash...)
	data = append(data, requestTag...)

	p := NewPacket(pathRequestDst, data)
	p.TransportType = TransportBroadcast
	return ts.Outbound(p)
}

// Inbound processes a raw packet received from an interface.
func (ts *TransportSystem) Inbound(raw []byte, iface interfaces.Interface) {
	Logf("Inbound: received packet of %d bytes from %s", LogDebug, false, len(raw), iface.Name())
	if ifac, ok := iface.(ifacInboundHook); ok {
		processed, accepted := ifac.ApplyIFACInbound(raw)
		if !accepted {
			Logf("Dropped packet by IFAC ingress policy on %v", LogDebug, false, iface.Name())
			return
		}
		raw = processed
	}

	packet := NewPacketFromRaw(raw)
	packet.ReceivingInterface = iface
	if err := packet.Unpack(); err != nil {
		Logf("Received malformed packet, dropping it: %v", LogExtreme, false, err)
		return
	}
	Logf("Inbound packet: type=%v, dest=%x, hops=%v, hash=%x", LogDebug, false, packet.PacketType, packet.DestinationHash, packet.Hops, packet.PacketHash)

	packet.Hops++

	// Duplicate detection
	ts.mu.Lock()
	if ts.seenOrRememberPacketHashLocked(packet.PacketHash, time.Now()) {
		Logf("Inbound: dropping duplicate packet %x", LogVerbose, false, packet.PacketHash)
		ts.mu.Unlock()
		return
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
		ts.handlePathRequest(packet.Data, packet)
		ts.forwardPathRequest(packet, iface)
		return
	}

	// Check if it's for us or a local destination
	for _, d := range ts.destinations {
		if string(d.Hash) == destHash {
			// Delivery to local destination
			d.receive(packet)
			return
		}
	}

	// Check if it's for a local link
	if link := ts.FindLink(packet.DestinationHash); link != nil {
		link.receive(packet)
		return
	}

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
					Logf("Inbound: transmitting forwarded packet on %s", LogDebug, false, entry.Interface.Name())
					if err := entry.Interface.Send(newRaw); err != nil {
						Log(fmt.Sprintf("Failed to forward packet: %v", err), LogError, false)
						ts.InvalidatePath(packet.DestinationHash)
					}
					return
				}
				Logf("Inbound: no path found in ts.pathTable for %x", LogDebug, false, packet.DestinationHash)
				ts.mu.Unlock()
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

func (ts *TransportSystem) handleAnnounce(packet *Packet, iface interfaces.Interface) {
	if !ValidateAnnounce(packet) {
		Log(fmt.Sprintf("Received invalid announce for %x, dropping", packet.DestinationHash), LogDebug, false)
		return
	}

	destHash := string(packet.DestinationHash)

	var handlers []*AnnounceHandler
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
			// If new path is shorter or equal, update
			if packet.Hops <= entry.Hops {
				nextHop, err := nextHopFromAnnounce(packet)
				if err != nil {
					Logf("Announce next-hop extraction failed for %x: %v", LogDebug, false, packet.DestinationHash, err)
					return
				}
				entry.Timestamp = time.Now()
				entry.Hops = packet.Hops
				entry.NextHop = nextHop
				entry.Interface = iface
				entry.InterfaceName = iface.Name()
				entry.Expires = time.Now().Add(24 * 7 * time.Hour)
				if randomBlob != nil && !containsBlob(entry.RandomBlobs, randomBlob) {
					entry.RandomBlobs = append(entry.RandomBlobs, randomBlob)
					if len(entry.RandomBlobs) > maxRandomBlobs {
						entry.RandomBlobs = entry.RandomBlobs[len(entry.RandomBlobs)-maxRandomBlobs:]
					}
				}
			}
		} else {
			nextHop, err := nextHopFromAnnounce(packet)
			if err != nil {
				Logf("Announce next-hop extraction failed for %x: %v", LogDebug, false, packet.DestinationHash, err)
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
				Expires:       time.Now().Add(24 * 7 * time.Hour), // 1 week default
			}
			Log(fmt.Sprintf("Learned path to %x via %v, %v hops", packet.DestinationHash, iface.Name(), packet.Hops), LogInfo, false)
		}

		// Propagation logic (re-broadcasting announces)
		if packet.Hops < ReticulumHopsMax && packet.Context != ContextPathResponse {
			raw := make([]byte, len(packet.Raw))
			copy(raw, packet.Raw)
			hops := packet.Hops + 1
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

	// Call announce handlers
	if len(handlers) > 0 {
		announceIdentity := Recall(ts, packet.DestinationHash, false)
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

				if executeCallback {
					handler.ReceivedAnnounce(packet.DestinationHash, announceIdentity, announceIdentity.AppData)
				}
			}
		}
	}
}

func (d *Destination) receive(packet *Packet) {
	if packet.PacketType == PacketLinkRequest {
		if _, err := ValidateRequest(d, packet.Data, packet); err != nil {
			Log(fmt.Sprintf("Failed to validate link request for %v: %v", d.name, err), LogDebug, false)
		}
		return
	}

	// For DATA packets, decrypt and call callback
	if d.callbacks.Packet != nil {
		plaintext, err := d.Decrypt(packet.Data)
		if err != nil {
			Log(fmt.Sprintf("Failed to decrypt packet for %v: %v", d.name, err), LogDebug, false)
			return
		}
		d.callbacks.Packet(plaintext, packet)
	}
}

// Outbound sends a packet over the network.
func (ts *TransportSystem) Outbound(packet *Packet) error {
	if !packet.Packed {
		if err := packet.Pack(); err != nil {
			return err
		}
	}

	ts.mu.Lock()
	interfacesSnapshot := append([]interfaces.Interface(nil), ts.interfaces...)
	pathEntry, hasPath := ts.pathTable[string(packet.DestinationHash)]
	ts.mu.Unlock()

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
				Log(fmt.Sprintf("Could not apply IFAC egress on %v: %v", pathEntry.Interface.Name(), err), LogError, false)
				return nil
			}
			raw = processed
		}

		if err := pathEntry.Interface.Send(raw); err != nil {
			Log(fmt.Sprintf("Could not transmit on %v: %v", pathEntry.Interface.Name(), err), LogError, false)
			ts.InvalidatePath(packet.DestinationHash)
		}

		ts.mu.Lock()
		packet.Sent = true
		packet.SentAt = float64(time.Now().UnixNano()) / 1e9
		if packet.Receipt != nil {
			packet.Receipt.MarkSent(packet.SentAt)
		}
		ts.mu.Unlock()
		return nil
	}

	for _, iface := range interfacesSnapshot {
		raw := packet.Raw
		if ifac, ok := iface.(ifacOutboundHook); ok {
			processed, err := ifac.ApplyIFACOutbound(raw)
			if err != nil {
				Log(fmt.Sprintf("Could not apply IFAC egress on %v: %v", iface.Name(), err), LogError, false)
				continue
			}
			raw = processed
		}

		if err := iface.Send(raw); err != nil {
			Log(fmt.Sprintf("Could not transmit on %v: %v", iface.Name(), err), LogError, false)
			ts.InvalidatePathsViaInterface(iface)
		}
	}

	ts.mu.Lock()
	packet.Sent = true
	packet.SentAt = float64(time.Now().UnixNano()) / 1e9
	if packet.Receipt != nil {
		packet.Receipt.MarkSent(packet.SentAt)
	}
	ts.mu.Unlock()
	return nil
}
