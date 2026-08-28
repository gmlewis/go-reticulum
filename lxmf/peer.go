// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

const (
	// PeerStateIdle indicates that no peer sync work is currently active.
	PeerStateIdle = 0x00
	// PeerStateLinkEstablishing indicates that the peer is trying to create a propagation link.
	PeerStateLinkEstablishing = 0x01
	// PeerStateLinkReady indicates that the propagation link is available for requests.
	PeerStateLinkReady = 0x02
	// PeerStateRequestSent indicates that an offer request is currently in flight.
	PeerStateRequestSent = 0x03
	// PeerStateResponseReceived indicates that an offer response has been received.
	PeerStateResponseReceived = 0x04
	// PeerStateResourceTransferring indicates that a propagation resource transfer is active.
	PeerStateResourceTransferring = 0x05
)

const (
	// PeerStrategyLazy matches Python's lazy peer sync strategy.
	PeerStrategyLazy = 0x01
	// PeerStrategyPersistent matches Python's persistent peer sync strategy.
	PeerStrategyPersistent = 0x02
	// DefaultPeerSyncStrategy matches Python's default peer sync strategy.
	DefaultPeerSyncStrategy = PeerStrategyPersistent

	// PeerSyncBackoffStep matches Python's SYNC_BACKOFF_STEP (12 minutes
	// in seconds). After each successful sync, the backoff is increased by
	// this amount so that the peer is not re-synced too aggressively.
	PeerSyncBackoffStep = 12 * 60

	// PeerPathRequestGrace matches Python's PATH_REQUEST_GRACE (7.5
	// seconds). Time to wait after requesting a path before checking
	// availability.
	PeerPathRequestGrace = 7.5

	// PeerOfferRequestPath matches Python's OFFER_REQUEST_PATH.
	PeerOfferRequestPath = "/offer"
)

// pendingOfferEntry is one entry prepared for a sync offer: the transient ID,
// its weight, and its transfer size. It mirrors Python's unhandled_entry
// [transient_id, weight, size] built in the LXMPeer.sync LINK_READY branch
// (LXMPeer.py:351-356). The slice is rebuilt on each sync and is not persisted.
type pendingOfferEntry struct {
	transientID []byte
	weight      float64
	size        int
}

// Peer models a propagation peer and its persisted sync state.
type Peer struct {
	router          *Router
	destinationHash []byte
	identity        *rns.Identity
	destination     *rns.Destination

	alive        bool
	lastHeard    float64
	syncStrategy int
	peeringKey   []any
	peeringCost  *int
	metadata     map[any]any

	nextSyncAttempt float64
	lastSyncAttempt float64
	syncBackoff     float64
	peeringTimebase float64

	linkEstablishmentRate float64
	syncTransferRate      float64

	propagationTransferLimit        *float64
	propagationSyncLimit            *int
	propagationStampCost            *int
	propagationStampCostFlexibility *int
	currentlyTransferringMessages   [][]byte
	// currentSyncTransferStarted records the peer-time at which the current
	// outbound sync resource transfer started (Python's
	// current_sync_transfer_started, LXMPeer.py:464). Zero means no transfer is
	// in flight; ResourceConcluded uses it to compute the transfer rate.
	currentSyncTransferStarted float64
	handledMessagesQueue       [][]byte
	unhandledMessagesQueue     [][]byte
	hmCount                    int
	umCount                    int
	hmCountsSynced             bool
	umCountsSynced             bool

	link      *rns.Link
	state     int
	lastOffer [][]byte
	// pendingOfferEntries holds the unhandled entries collected during the
	// most recent LINK_READY offer-preparation pass (Python's
	// unhandled_entries, LXMPeer.py:344-356). Subsequent sync steps filter
	// and trim it into the final offer list.
	pendingOfferEntries []pendingOfferEntry
	// pendingOfferIDs holds the transient IDs that survive the
	// transfer/sync size-limit filtering of pendingOfferEntries (Python's
	// unhandled_ids, LXMPeer.py:367-381). It is the candidate offer list
	// before the empty-check (24.B.4) and offer send (24.B.5).
	pendingOfferIDs [][]byte

	// syncHook is an optional test hook that fires when sync() would have
	// been called. It allows tests to exercise peer-selection logic in
	// sync_peers without actually performing a network sync.
	syncHook func()

	// syncPostponeHook is an optional test hook that fires when Sync()
	// postpones due to unmet preconditions. It receives the postponement
	// reason string, allowing tests to verify which precondition failed.
	syncPostponeHook func(reason string)

	// identifyLinkHook is an optional test hook that, when set, replaces
	// the default link.Identify() call during link_established.
	identifyLinkHook func(*rns.Link, *rns.Identity) error

	// now is an injectable time function for testing. Defaults to time.Now.
	now func() time.Time

	// generatePeeringKeyFn is an injectable function for generating peering
	// keys. When nil, defaults to spawning p.GeneratePeeringKey in a
	// goroutine. Tests can override to run synchronously or skip entirely.
	generatePeeringKeyFn func()

	// hasPathFn is an optional test override for the HasPath check during
	// sync. When nil, the real p.router.transport.HasPath is used.
	hasPathFn func(destHash []byte) bool

	// requestPathFn is an optional test override for the RequestPath call
	// during sync. When nil, the real p.router.transport.RequestPath is used.
	requestPathFn func(destHash []byte) error

	// recallIdentityFn is an optional test override for the identity recall
	// step during sync. When nil, the real rns.RecallIdentity is used.
	recallIdentityFn func(destHash []byte) *rns.Identity

	// newDestinationFn is an optional test override for creating the
	// propagation destination during sync. When nil, the real
	// rns.NewDestination is used.
	newDestinationFn func(identity *rns.Identity) (*rns.Destination, error)

	// unhandledMessagesFn is an optional test override for the
	// UnhandledMessages check during sync. When nil, the real
	// p.UnhandledMessages is used. Tests can use this to simulate
	// unhandled messages without needing to populate router.propagationEntries.
	unhandledMessagesFn func() [][]byte

	// establishLinkFn is an optional test override for the link
	// establishment step during sync. When nil, a real rns.Link is
	// created from p.destination.
	establishLinkFn func()

	// requestLinkFn is an optional test override for the outbound offer
	// request (Python's self.link.request, LXMPeer.py:389). When nil, Sync
	// uses the router's requestLink seam. It receives the link, path, offer
	// data, and callbacks so tests can capture the offer bytes (and avoid a
	// live link) without exercising the network.
	requestLinkFn func(link *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), timeout time.Duration) (*rns.RequestReceipt, error)

	// pathRequestSleep is an injectable sleep function for the path
	// request grace period. When nil, defaults to sleeping for
	// PeerPathRequestGrace seconds. Tests can override to skip the delay.
	pathRequestSleep func()

	linkBackoffStep time.Duration

	offered  int
	outgoing int
	incoming int
	rxBytes  int
	txBytes  int

	mu             sync.Mutex
	peeringKeyLock sync.Mutex
}

// NewPeer constructs a new Peer with Python-compatible defaults.
func NewPeer(router *Router, destinationHash []byte) *Peer {
	peer := &Peer{
		router:                        router,
		destinationHash:               cloneBytes(destinationHash),
		syncStrategy:                  DefaultPeerSyncStrategy,
		handledMessagesQueue:          [][]byte{},
		unhandledMessagesQueue:        [][]byte{},
		state:                         PeerStateIdle,
		lastOffer:                     [][]byte{},
		currentlyTransferringMessages: nil,
	}

	if router == nil || router.transport == nil || len(destinationHash) == 0 {
		return peer
	}

	peer.identity = rns.RecallIdentity(router.transport, destinationHash)
	if peer.identity == nil {
		return peer
	}

	destination, err := rns.NewDestination(router.transport, peer.identity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
	if err == nil {
		peer.destination = destination
	}

	return peer
}

// PeerFromBytes reconstructs a persisted peer from Python-compatible msgpack bytes.
func (r *Router) PeerFromBytes(peerBytes []byte) (*Peer, error) {
	unpacked, err := msgpack.Unpack(peerBytes)
	if err != nil {
		return nil, err
	}

	dictionary, err := peerDictionary(unpacked)
	if err != nil {
		return nil, err
	}

	destinationHash := anyToBytes(peerDictionaryValue(dictionary, "destination_hash"))
	if len(destinationHash) == 0 {
		return nil, fmt.Errorf("peer payload missing destination_hash")
	}

	peer := NewPeer(r, destinationHash)
	peer.peeringTimebase = peerFloat(dictionary, "peering_timebase", 0)
	peer.alive = peerBool(dictionary, "alive")
	peer.lastHeard = peerFloat(dictionary, "last_heard", 0)
	peer.linkEstablishmentRate = peerOptionalFloat(dictionary, "link_establishment_rate")
	peer.syncTransferRate = peerOptionalFloat(dictionary, "sync_transfer_rate")
	peer.propagationTransferLimit = peerOptionalFloatPtr(dictionary, "propagation_transfer_limit")
	peer.propagationSyncLimit = peerOptionalIntPtr(dictionary, "propagation_sync_limit")
	if peer.propagationSyncLimit == nil && peer.propagationTransferLimit != nil {
		fallback := int(*peer.propagationTransferLimit)
		peer.propagationSyncLimit = &fallback
	}
	peer.propagationStampCost = peerOptionalIntPtr(dictionary, "propagation_stamp_cost")
	peer.propagationStampCostFlexibility = peerOptionalIntPtr(dictionary, "propagation_stamp_cost_flexibility")
	peer.peeringCost = peerOptionalIntPtr(dictionary, "peering_cost")
	peer.syncStrategy = DefaultPeerSyncStrategy
	if value, ok := peerDictionaryLookup(dictionary, "sync_strategy"); ok {
		if parsed, err := anyToInt(value); err == nil {
			peer.syncStrategy = parsed
		}
	}
	peer.offered = peerOptionalInt(dictionary, "offered")
	peer.outgoing = peerOptionalInt(dictionary, "outgoing")
	peer.incoming = peerOptionalInt(dictionary, "incoming")
	peer.rxBytes = peerOptionalInt(dictionary, "rx_bytes")
	peer.txBytes = peerOptionalInt(dictionary, "tx_bytes")
	peer.lastSyncAttempt = peerFloat(dictionary, "last_sync_attempt", 0)
	peer.peeringKey = clonePeerPeeringKey(peerDictionaryValue(dictionary, "peering_key"))
	peer.metadata = peerMetadata(peerDictionaryValue(dictionary, "metadata"))

	hmCount := 0
	for _, transientID := range anySliceToByteSlices(peerDictionaryValue(dictionary, "handled_ids")) {
		if _, exists := r.propagationEntries[string(transientID)]; exists {
			peer.addHandledMessage(transientID)
			hmCount++
		}
	}

	umCount := 0
	for _, transientID := range anySliceToByteSlices(peerDictionaryValue(dictionary, "unhandled_ids")) {
		if _, exists := r.propagationEntries[string(transientID)]; exists {
			peer.addUnhandledMessage(transientID)
			umCount++
		}
	}

	peer.hmCount = hmCount
	peer.umCount = umCount
	peer.hmCountsSynced = true
	peer.umCountsSynced = true

	return peer, nil
}

// ToBytes serializes a peer using the Python LXMPeer msgpack dictionary layout.
func (p *Peer) ToBytes() ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("peer is nil")
	}

	dictionary := map[string]any{
		"peering_timebase":                   p.peeringTimebase,
		"alive":                              p.alive,
		"metadata":                           cloneMetadata(p.metadata),
		"last_heard":                         p.lastHeard,
		"sync_strategy":                      p.syncStrategy,
		"peering_key":                        clonePeerPeeringKey(p.peeringKey),
		"destination_hash":                   cloneBytes(p.destinationHash),
		"link_establishment_rate":            p.linkEstablishmentRate,
		"sync_transfer_rate":                 p.syncTransferRate,
		"propagation_transfer_limit":         cloneOptionalFloat64(p.propagationTransferLimit),
		"propagation_sync_limit":             cloneOptionalInt(p.propagationSyncLimit),
		"propagation_stamp_cost":             cloneOptionalInt(p.propagationStampCost),
		"propagation_stamp_cost_flexibility": cloneOptionalInt(p.propagationStampCostFlexibility),
		"peering_cost":                       cloneOptionalInt(p.peeringCost),
		"last_sync_attempt":                  p.lastSyncAttempt,
		"offered":                            p.offered,
		"outgoing":                           p.outgoing,
		"incoming":                           p.incoming,
		"rx_bytes":                           p.rxBytes,
		"tx_bytes":                           p.txBytes,
	}

	handledIDs := make([]any, 0)
	for _, transientID := range p.HandledMessages() {
		handledIDs = append(handledIDs, cloneBytes(transientID))
	}
	unhandledIDs := make([]any, 0)
	for _, transientID := range p.UnhandledMessages() {
		unhandledIDs = append(unhandledIDs, cloneBytes(transientID))
	}
	dictionary["handled_ids"] = handledIDs
	dictionary["unhandled_ids"] = unhandledIDs

	return msgpack.Pack(dictionary)
}

// QueueUnhandledMessage appends a transient message ID to the unhandled queue.
func (p *Peer) QueueUnhandledMessage(transientID []byte) {
	if p == nil || len(transientID) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unhandledMessagesQueue = append(p.unhandledMessagesQueue, cloneBytes(transientID))
}

// QueueHandledMessage appends a transient message ID to the handled queue.
func (p *Peer) QueueHandledMessage(transientID []byte) {
	if p == nil || len(transientID) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.handledMessagesQueue = append(p.handledMessagesQueue, cloneBytes(transientID))
}

// ProcessQueues merges queued handled/unhandled message updates into router propagation entries.
func (p *Peer) ProcessQueues() {
	if p == nil {
		return
	}

	p.mu.Lock()
	handledQueue := cloneByteSlices(p.handledMessagesQueue)
	unhandledQueue := cloneByteSlices(p.unhandledMessagesQueue)
	p.handledMessagesQueue = nil
	p.unhandledMessagesQueue = nil
	p.mu.Unlock()

	if len(handledQueue) == 0 && len(unhandledQueue) == 0 {
		return
	}

	handledMessages := p.HandledMessages()
	unhandledMessages := p.UnhandledMessages()

	for _, transientID := range slices.Backward(handledQueue) {

		if !containsByteSlice(handledMessages, transientID) {
			p.addHandledMessage(transientID)
		}
		if containsByteSlice(unhandledMessages, transientID) {
			p.removeUnhandledMessage(transientID)
		}
	}

	for _, transientID := range slices.Backward(unhandledQueue) {

		if !containsByteSlice(handledMessages, transientID) && !containsByteSlice(unhandledMessages, transientID) {
			p.addUnhandledMessage(transientID)
		}
	}
}

// PeeringKeyReady reports whether the stored peering key satisfies the current cost requirement.
func (p *Peer) PeeringKeyReady() bool {
	if p == nil || p.peeringCost == nil {
		return false
	}

	value := p.PeeringKeyValue()
	if value != nil && *value >= *p.peeringCost {
		return true
	}
	if value != nil {
		p.peeringKey = nil
	}
	return false
}

// PeeringKeyValue returns the numeric work value stored in the current peering key.
func (p *Peer) PeeringKeyValue() *int {
	if p == nil || len(p.peeringKey) != 2 {
		return nil
	}
	value, err := anyToInt(p.peeringKey[1])
	if err != nil {
		return nil
	}
	return &value
}

// GeneratePeeringKey creates a new peering key that satisfies the configured peering cost.
func (p *Peer) GeneratePeeringKey() bool {
	if p == nil || p.peeringCost == nil {
		return false
	}

	p.peeringKeyLock.Lock()
	defer p.peeringKeyLock.Unlock()

	if p.peeringKey != nil {
		return true
	}
	if p.router == nil || p.router.identity == nil {
		return false
	}
	if p.identity == nil && p.router.transport != nil {
		p.identity = rns.RecallIdentity(p.router.transport, p.destinationHash)
	}
	if p.identity == nil {
		return false
	}

	keyMaterial := append(cloneBytes(p.identity.Hash), p.router.identity.Hash...)
	peeringKey, value, _, err := GenerateStamp(keyMaterial, *p.peeringCost, WorkblockExpandRoundsPeering)
	if err != nil || value < *p.peeringCost {
		return false
	}

	p.peeringKey = []any{peeringKey, value}
	return true
}

// HandledMessages returns the transient IDs already handled for this peer.
func (p *Peer) HandledMessages() [][]byte {
	if p == nil || p.router == nil {
		return nil
	}

	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	result := make([][]byte, 0)
	for transientID, entry := range p.router.propagationEntries {
		if entry == nil || !containsByteSlice(entry.handledBy, p.destinationHash) {
			continue
		}
		result = append(result, []byte(transientID))
	}
	p.hmCount = len(result)
	p.hmCountsSynced = true
	return cloneByteSlices(result)
}

// UnhandledMessages returns the transient IDs still queued for this peer.
func (p *Peer) UnhandledMessages() [][]byte {
	if p == nil || p.router == nil {
		return nil
	}

	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	result := make([][]byte, 0)
	for transientID, entry := range p.router.propagationEntries {
		if entry == nil || !containsByteSlice(entry.unhandledBy, p.destinationHash) {
			continue
		}
		result = append(result, []byte(transientID))
	}
	p.umCount = len(result)
	p.umCountsSynced = true
	return cloneByteSlices(result)
}

// HandledMessageCount returns the cached or recomputed handled-message count.
func (p *Peer) HandledMessageCount() int {
	if p == nil {
		return 0
	}
	if !p.hmCountsSynced {
		_ = p.HandledMessages()
	}
	return p.hmCount
}

// UnhandledMessageCount returns the cached or recomputed unhandled-message count.
func (p *Peer) UnhandledMessageCount() int {
	if p == nil {
		return 0
	}
	if !p.umCountsSynced {
		_ = p.UnhandledMessages()
	}
	return p.umCount
}

// AcceptanceRate returns the outgoing/offered acceptance ratio.
func (p *Peer) AcceptanceRate() float64 {
	if p == nil || p.offered == 0 {
		return 0
	}
	return float64(p.outgoing) / float64(p.offered)
}

// MinAcceptedStampCost returns the minimum accepted proof-of-work stamp cost
// for offers prepared for this peer: max(0, propagationStampCost -
// propagationStampCostFlexibility). It is the Go port of Python's
// `min_accepted_cost = max(0, self.propagation_stamp_cost-
// self.propagation_stamp_cost_flexibility)` (LXMPeer.py:331, v1.1.0), computed
// in the Peer.Sync offer-preparation branch (PeerStateLinkReady). The max(0,
// ...) lower bound stops a flexibility exceeding the advertised cost from
// yielding a negative minimum; the validation path applies the same bound on
// the router side (router.go max(propagationCost-propagationCostFlexibility,
// 0)). The ok result is false when the peer's stamp cost or flexibility is not
// yet known (either pointer nil); Python's sync gate (stamp_costs_known)
// guarantees both are set before offer preparation runs, so a false result
// means the caller should postpone rather than prepare an offer.
func (p *Peer) MinAcceptedStampCost() (cost int, ok bool) {
	if p == nil || p.propagationStampCost == nil || p.propagationStampCostFlexibility == nil {
		return 0, false
	}
	return max(*p.propagationStampCost-*p.propagationStampCostFlexibility, 0), true
}

// peerLogger returns the router transport's logger if available, or nil. It
// centralises the nil-safe lookup used by Sync/OfferResponse logging paths.
func (p *Peer) peerLogger() *rns.Logger {
	if p == nil || p.router == nil || p.router.transport == nil {
		return nil
	}
	return p.router.transport.GetLogger()
}

// identifyLink identifies the link to the remote peer using the router's
// identity, respecting the identifyLinkHook test seam. It mirrors Python's
// self.link.identify(self.router.identity) (LXMPeer.py:408, 819).
func (p *Peer) identifyLink(link *rns.Link) {
	if p.identifyLinkHook != nil {
		_ = p.identifyLinkHook(link, p.router.identity)
	} else if link != nil && p.router != nil && p.router.identity != nil {
		_ = link.Identify(p.router.identity)
	}
}

func (p *Peer) addHandledMessage(transientID []byte) {
	if p == nil || p.router == nil {
		return
	}
	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	entry, exists := p.router.propagationEntries[string(transientID)]
	if !exists || containsByteSlice(entry.handledBy, p.destinationHash) {
		return
	}
	entry.handledBy = append(entry.handledBy, cloneBytes(p.destinationHash))
	p.hmCountsSynced = false
}

func (p *Peer) addUnhandledMessage(transientID []byte) {
	if p == nil || p.router == nil {
		return
	}
	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	entry, exists := p.router.propagationEntries[string(transientID)]
	if !exists || containsByteSlice(entry.unhandledBy, p.destinationHash) {
		return
	}
	entry.unhandledBy = append(entry.unhandledBy, cloneBytes(p.destinationHash))
	p.umCount++
}

func (p *Peer) removeUnhandledMessage(transientID []byte) {
	if p == nil || p.router == nil {
		return
	}
	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	entry, exists := p.router.propagationEntries[string(transientID)]
	if !exists {
		return
	}
	entry.unhandledBy = removeByteSlice(entry.unhandledBy, p.destinationHash)
	p.umCountsSynced = false
}

func peerDictionaryValue(dictionary map[any]any, key string) any {
	value, _ := peerDictionaryLookup(dictionary, key)
	return value
}

func peerDictionary(value any) (map[any]any, error) {
	switch dictionary := value.(type) {
	case map[any]any:
		return dictionary, nil
	case map[string]any:
		out := make(map[any]any, len(dictionary))
		for key, item := range dictionary {
			out[key] = item
		}
		return out, nil
	default:
		return nil, fmt.Errorf("peer payload is %T, want map", value)
	}
}

func peerDictionaryLookup(dictionary map[any]any, key string) (any, bool) {
	for candidate, value := range dictionary {
		if candidate == key {
			return value, true
		}
	}
	return nil, false
}

func peerOptionalFloat(dictionary map[any]any, key string) float64 {
	value, ok := peerDictionaryLookup(dictionary, key)
	if !ok {
		return 0
	}
	parsed, err := anyToFloat64(value)
	if err != nil {
		return 0
	}
	return parsed
}

func peerFloat(dictionary map[any]any, key string, fallback float64) float64 {
	value, ok := peerDictionaryLookup(dictionary, key)
	if !ok {
		return fallback
	}
	parsed, err := anyToFloat64(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func peerOptionalFloatPtr(dictionary map[any]any, key string) *float64 {
	value, ok := peerDictionaryLookup(dictionary, key)
	if !ok {
		return nil
	}
	parsed, err := anyToFloat64(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func peerOptionalIntPtr(dictionary map[any]any, key string) *int {
	value, ok := peerDictionaryLookup(dictionary, key)
	if !ok {
		return nil
	}
	parsed, err := anyToInt(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func peerOptionalInt(dictionary map[any]any, key string) int {
	value, ok := peerDictionaryLookup(dictionary, key)
	if !ok {
		return 0
	}
	parsed, err := anyToInt(value)
	if err != nil {
		return 0
	}
	return parsed
}

func peerBool(dictionary map[any]any, key string) bool {
	value, ok := peerDictionaryLookup(dictionary, key)
	if !ok {
		return false
	}
	b, ok := value.(bool)
	return ok && b
}

func peerMetadata(value any) map[any]any {
	switch metadata := value.(type) {
	case nil:
		return nil
	case map[any]any:
		return cloneMetadata(metadata)
	case map[string]any:
		out := make(map[any]any, len(metadata))
		for key, item := range metadata {
			out[key] = item
		}
		return out
	default:
		return nil
	}
}

func cloneMetadata(in map[any]any) map[any]any {
	if in == nil {
		return nil
	}
	out := make(map[any]any, len(in))
	maps.Copy(out, in)
	return out
}

func cloneOptionalFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneOptionalInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func clonePeerPeeringKey(value any) []any {
	items, ok := value.([]any)
	if !ok || len(items) != 2 {
		return nil
	}

	cloned := make([]any, 2)
	cloned[0] = cloneBytes(anyToBytes(items[0]))
	cloned[1] = items[1]
	return cloned
}

func cloneByteSlices(in [][]byte) [][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make([][]byte, 0, len(in))
	for _, item := range in {
		out = append(out, cloneBytes(item))
	}
	return out
}

func containsByteSlice(items [][]byte, target []byte) bool {
	for _, item := range items {
		if bytes.Equal(item, target) {
			return true
		}
	}
	return false
}

func removeByteSlice(items [][]byte, target []byte) [][]byte {
	if len(items) == 0 {
		return nil
	}

	out := make([][]byte, 0, len(items))
	for _, item := range items {
		if bytes.Equal(item, target) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func peerTime(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

func timeFromPeerValue(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(value*float64(time.Second)))
}

// LinkEstablished is the Go port of Python's LXMPeer.link_established.
// It identifies the link with the router's identity, marks the peer
// LINK_READY, resets the sync backoff, and triggers a peer sync.
func (p *Peer) LinkEstablished(link *rns.Link) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.identifyLink(link)
	p.state = PeerStateLinkReady
	p.nextSyncAttempt = 0
	p.mu.Unlock()
	p.Sync()
}

// LinkClosed is the Go port of Python's LXMPeer.link_closed. It clears
// the link and reverts the peer to IDLE.
func (p *Peer) LinkClosed(_ *rns.Link) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.link = nil
	p.state = PeerStateIdle
	p.mu.Unlock()
}

// RequestFailed is the Go port of Python's LXMPeer.request_failed. It
// tears down the link (if any) and reverts the peer to IDLE.
func (p *Peer) RequestFailed(_ *rns.RequestReceipt) {
	if p == nil {
		return
	}
	p.mu.Lock()
	link := p.link
	p.link = nil
	p.state = PeerStateIdle
	p.mu.Unlock()
	if link != nil {
		link.Teardown()
	}
}

// collectPendingOfferEntries mirrors the first pass of Python's LXMPeer.sync
// LINK_READY branch (LXMPeer.py:344-366): for each unhandled transient ID, if
// it still exists in the propagation store it is collected as a
// [transient_id, weight, size] entry unless its stamp value is below the peer's
// minimum accepted cost (the 1.1.0 low-stamp-value drop, LXMPeer.py:347-348,
// 4a93697), in which case it is reported as low-value; IDs absent from the
// store are reported as purged. The caller drops purged and low-value IDs via
// removeUnhandledMessage. Weight and size come from the router's
// get_weight/get_size equivalents (LXMRouter.py:1052-1067); the stamp-value
// floor is MinAcceptedStampCost (max(0, cost - flexibility)).
func (p *Peer) collectPendingOfferEntries(unhandledMessages [][]byte) (entries []pendingOfferEntry, purgedIDs [][]byte, lowValueIDs [][]byte) {
	if p == nil || p.router == nil {
		return nil, nil, nil
	}
	minAcceptedCost, _ := p.MinAcceptedStampCost()
	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	entries = make([]pendingOfferEntry, 0, len(unhandledMessages))
	for _, transientID := range unhandledMessages {
		entry, exists := p.router.propagationEntries[string(transientID)]
		if !exists {
			purgedIDs = append(purgedIDs, cloneBytes(transientID))
			continue
		}
		if entry.stampValue < minAcceptedCost {
			lowValueIDs = append(lowValueIDs, cloneBytes(transientID))
			continue
		}
		entries = append(entries, pendingOfferEntry{
			transientID: cloneBytes(transientID),
			weight:      p.router.getWeightLocked(string(transientID)),
			size:        entry.size,
		})
	}
	return entries, purgedIDs, lowValueIDs
}

// Sync initiates a propagation-node sync with this peer. It is the Go
// port of Python's LXMPeer.sync(). The IDLE branch establishes a sync link;
// the LINK_READY branch begins offer preparation (collecting unhandled
// entries and dropping purged ones so far). The remaining offer-preparation
// steps (low-stamp-value drop, size limits, offer send) and the offer-
// response / resource-transfer callbacks are implemented in later tasks.
func (p *Peer) Sync() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.lastSyncAttempt = peerTime(p.nowFn()())
	p.mu.Unlock()

	syncTimeReached := p.nowFn()().After(timeFromPeerValue(p.nextSyncAttempt))
	stampCostsKnown := p.propagationStampCost != nil && p.propagationStampCostFlexibility != nil && p.peeringCost != nil
	peeringKeyReady := p.PeeringKeyReady()

	if !syncTimeReached || !stampCostsKnown || !peeringKeyReady {
		var postponeReason string
		switch {
		case !syncTimeReached:
			postponeReason = " due to previous failures"
			if p.lastSyncAttempt > p.lastHeard {
				p.alive = false
			}
		case !stampCostsKnown:
			postponeReason = " since its required stamp costs are not yet known"
		case !peeringKeyReady:
			postponeReason = " since a peering key has not been generated yet"
			if p.generatePeeringKeyFn != nil {
				p.generatePeeringKeyFn()
			} else {
				go p.GeneratePeeringKey()
			}
		}
		if p.syncPostponeHook != nil {
			p.syncPostponeHook(postponeReason)
		}
		return
	}

	hasPath := p.router.transport.HasPath
	if p.hasPathFn != nil {
		hasPath = p.hasPathFn
	}
	requestPath := p.router.transport.RequestPath
	if p.requestPathFn != nil {
		requestPath = p.requestPathFn
	}

	if !hasPath(p.destinationHash) {
		if err := requestPath(p.destinationHash); err != nil {
			p.peerLogger().Error("Peer.Sync: path request for %x failed: %v", p.destinationHash, err)
		}
		if p.pathRequestSleep != nil {
			p.pathRequestSleep()
		} else {
			time.Sleep(time.Duration(PeerPathRequestGrace * float64(time.Second)))
		}
	}

	if !hasPath(p.destinationHash) {
		return
	}

	if p.identity == nil {
		recallIdentity := func(hash []byte) *rns.Identity {
			return rns.RecallIdentity(p.router.transport, hash)
		}
		if p.recallIdentityFn != nil {
			recallIdentity = p.recallIdentityFn
		}
		p.identity = recallIdentity(p.destinationHash)
		if p.identity != nil {
			newDest := func(id *rns.Identity) (*rns.Destination, error) {
				return rns.NewDestination(p.router.transport, id, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
			}
			if p.newDestinationFn != nil {
				newDest = p.newDestinationFn
			}
			if dst, err := newDest(p.identity); err == nil {
				p.destination = dst
			}
		}
	}

	if p.destination == nil {
		return
	}

	unhandledMessages := p.UnhandledMessages()
	if p.unhandledMessagesFn != nil {
		unhandledMessages = p.unhandledMessagesFn()
	}
	if len(unhandledMessages) == 0 {
		return
	}

	if p.syncHook != nil {
		p.syncHook()
		return
	}

	if p.currentlyTransferringMessages != nil {
		return
	}

	if p.state == PeerStateIdle {
		p.syncBackoff += PeerSyncBackoffStep
		p.nextSyncAttempt = peerTime(p.nowFn()()) + p.syncBackoff
		if p.establishLinkFn != nil {
			p.establishLinkFn()
		} else if p.destination != nil {
			p.link, _ = rns.NewLink(p.router.transport, p.destination)
		}
		p.state = PeerStateLinkEstablishing
	} else if p.state == PeerStateLinkReady {
		// Mirror Python's LXMPeer.sync LINK_READY branch (LXMPeer.py:337-389).
		now := p.nowFn()()
		p.alive = true
		p.lastHeard = peerTime(now)
		p.syncBackoff = 0

		entries, purgedIDs, lowValueIDs := p.collectPendingOfferEntries(unhandledMessages)
		for _, transientID := range purgedIDs {
			p.removeUnhandledMessage(transientID)
		}
		for _, transientID := range lowValueIDs {
			p.removeUnhandledMessage(transientID)
		}
		p.pendingOfferEntries = entries

		// Apply transfer/sync size limits (Python LXMPeer.py:367-381). Sort
		// by weight ascending, then walk the entries: a single message whose
		// transfer size exceeds propagation_transfer_limit*1000 is dropped and
		// marked handled; once the cumulative size reaches
		// propagation_sync_limit*1000 no further IDs are added. Nil limits are
		// treated as unset (Python checks `!= None`).
		slices.SortStableFunc(entries, func(a, b pendingOfferEntry) int {
			switch {
			case a.weight < b.weight:
				return -1
			case a.weight > b.weight:
				return 1
			default:
				return 0
			}
		})
		const perMessageOverhead = 16
		cumulativeSize := 24
		unhandledIDs := make([][]byte, 0, len(entries))
		for _, e := range entries {
			lxmTransferSize := e.size + perMessageOverhead
			nextSize := cumulativeSize + lxmTransferSize
			if p.propagationTransferLimit != nil && float64(lxmTransferSize) > *p.propagationTransferLimit*1000 {
				p.removeUnhandledMessage(e.transientID)
				p.addHandledMessage(e.transientID)
				continue
			}
			if p.propagationSyncLimit != nil && nextSize >= *p.propagationSyncLimit*1000 {
				continue
			}
			cumulativeSize += lxmTransferSize
			unhandledIDs = append(unhandledIDs, e.transientID)
		}
		p.pendingOfferIDs = unhandledIDs

		// Post-filter early return (1.1.0 delta, 982c9fc, LXMPeer.py:383-385):
		// if every unhandled message was filtered out, sync is complete for
		// now — no offer is sent, lastOffer stays unset, and the state
		// remains PeerStateLinkReady.
		if len(unhandledIDs) == 0 {
			if logger := p.peerLogger(); logger != nil {
				logger.Debug("Sync requested for %x, but no unhandled messages exist after offer preparation. Sync complete.", p.destinationHash)
			}
			return
		}

		// Send the offer request (LXMPeer.py:386-389). The offer is
		// [peering_key[0], unhandled_ids]; lastOffer records the offered IDs
		// for OfferResponse to reconcile; state advances to RequestSent.
		offer := []any{p.peeringKey[0], unhandledIDs}
		p.lastOffer = unhandledIDs
		if logger := p.peerLogger(); logger != nil {
			logger.Verbose("Offering %v messages to peer %x", len(unhandledIDs), p.destinationHash)
		}
		requestLink := p.router.requestLink
		if p.requestLinkFn != nil {
			requestLink = p.requestLinkFn
		}
		if _, err := requestLink(p.link, PeerOfferRequestPath, offer, p.OfferResponse, p.RequestFailed, nil, 0); err != nil {
			if logger := p.peerLogger(); logger != nil {
				logger.Error("Sending sync offer request to peer %x failed: %v", p.destinationHash, err)
			}
		}
		p.state = PeerStateRequestSent
	}
}

// OfferResponse is the Go port of Python's LXMPeer.offer_response. It
// processes a peer's response to a propagation-node offer and updates
// the peer's message queues and state machine accordingly.
//
// The full implementation supports: ERROR_NO_IDENTITY, ERROR_NO_ACCESS,
// ERROR_THROTTLED, a `true`/`false` "wants everything/nothing"
// response, and a list of wanted transient IDs. For now, this method
// focuses on the "wants nothing" path that is sufficient for
// offer-response tests; the full resource-transfer path is implemented
// in a later task.
func (p *Peer) OfferResponse(receipt *rns.RequestReceipt) {
	if p == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	if receipt == nil {
		return
	}

	p.mu.Lock()
	p.state = PeerStateResponseReceived
	response := receipt.Response
	offer := p.lastOffer
	link := p.link
	p.mu.Unlock()

	// Error-code responses are dispatched before the wants-nothing /
	// wants-everything / wanted-list branches (LXMPeer.py:405-422).
	if code, ok := responseErrorCode(response); ok {
		switch code {
		case peerErrorNoIdentity:
			// The remote peer did not receive our identification: re-identify
			// the link, drop back to LINK_READY, and re-enter Sync to rebuild
			// and resend the offer (1.1.0 delta, 548be10).
			if link != nil {
				if logger := p.peerLogger(); logger != nil {
					logger.Verbose("Remote peer indicated that no identification was received, retrying...")
				}
				p.identifyLink(link)
				p.mu.Lock()
				p.state = PeerStateLinkReady
				p.mu.Unlock()
				p.Sync()
			}
			return
		case peerErrorNoAccess:
			// The remote peer denied access: break the peering. The peer is
			// removed from the router's peer set; sync state is left as-is
			// (LXMPeer.py:413-416).
			if logger := p.peerLogger(); logger != nil {
				logger.Verbose("Remote indicated that access was denied, breaking peering")
			}
			p.router.Unpeer(p.destinationHash)
			return
		case peerErrorThrottled:
			// The remote peer is throttling us: postpone the next sync attempt
			// by PN_STAMP_THROTTLE (LXMPeer.py:418-421).
			if logger := p.peerLogger(); logger != nil {
				logger.Verbose("Remote indicated that we're throttled, postponing sync for %v", pnStampThrottle)
			}
			p.mu.Lock()
			p.nextSyncAttempt = peerTime(p.nowFn()()) + float64(pnStampThrottle)/float64(time.Second)
			p.mu.Unlock()
			return
		}
	}

	switch response {
	case false:
		// Peer already has every advertised message.
		for _, tid := range offer {
			p.addHandledMessage(tid)
			p.removeUnhandledMessage(tid)
		}
	case true:
		// Peer wants all advertised messages: pack every offered
		// message's stored LXM data into a single Resource and start the
		// transfer (LXMPeer.py:430-435, 452-465). The resource data is
		// msgpack [time, [lxm_data, ...]], matching what the inbound
		// propagationResourceConcluded handler decodes.
		p.startSyncTransfer(offer)
		return
	default:
		// A list response means the peer wants a subset of the advertised
		// messages (LXMPeer.py:476-484). Any offered-but-not-wanted ID is
		// marked handled + removed; the wanted IDs drive a sync transfer.
		wantedIDs, isList := transientIDsFromResponse(response)
		if !isList {
			// Not a recognised list: treat as wants-nothing so the sync
			// completes cleanly for unrecognised scalar responses.
			for _, tid := range offer {
				p.addHandledMessage(tid)
				p.removeUnhandledMessage(tid)
			}
			break
		}
		wantedSet := make(map[string]bool, len(wantedIDs))
		for _, tid := range wantedIDs {
			wantedSet[string(tid)] = true
		}
		for _, tid := range offer {
			if !wantedSet[string(tid)] {
				p.addHandledMessage(tid)
				p.removeUnhandledMessage(tid)
			}
		}
		p.startSyncTransfer(wantedIDs)
		return
	}

	p.mu.Lock()
	p.offered += len(offer)
	if p.link != nil {
		p.link.Teardown()
	}
	p.link = nil
	p.state = PeerStateIdle
	p.mu.Unlock()
}

// startSyncTransfer packs the stored LXM data for the wanted transient IDs
// into a single Resource, starts the outbound transfer, and records the
// transferring IDs. It mirrors the common transfer block of Python's
// offer_response (LXMPeer.py:452-465): the resource data is msgpack
// [time, [lxm_data, ...]], matching what the inbound
// propagationResourceConcluded handler decodes. If no wanted message data is
// available the sync completes without a resource (Python's else branch,
// LXMPeer.py:467-474). On a packing or resource-creation error it tears down
// the link and returns the peer to Idle.
func (p *Peer) startSyncTransfer(wantedIDs [][]byte) {
	now := p.nowFn()()
	lxmList := make([][]byte, 0, len(wantedIDs))
	p.router.mu.Lock()
	for _, tid := range wantedIDs {
		if entry, ok := p.router.propagationEntries[string(tid)]; ok && len(entry.payload) > 0 {
			lxmList = append(lxmList, append([]byte{}, entry.payload...))
		}
	}
	p.router.mu.Unlock()

	if len(lxmList) == 0 {
		// No transferable message data: complete the sync without a
		// resource, mirroring Python's else branch.
		p.mu.Lock()
		p.offered += len(p.lastOffer)
		if p.link != nil {
			p.link.Teardown()
		}
		p.link = nil
		p.state = PeerStateIdle
		p.mu.Unlock()
		return
	}

	data, err := msgpack.Pack([]any{peerTime(now), lxmList})
	if err != nil {
		if logger := p.peerLogger(); logger != nil {
			logger.Error("Packing sync transfer resource for peer %x failed: %v", p.destinationHash, err)
		}
		p.mu.Lock()
		if p.link != nil {
			p.link.Teardown()
		}
		p.link = nil
		p.state = PeerStateIdle
		p.mu.Unlock()
		return
	}

	p.mu.Lock()
	link := p.link
	p.mu.Unlock()

	resource, err := p.router.newResource(data, link)
	if err != nil {
		if logger := p.peerLogger(); logger != nil {
			logger.Error("Sending sync transfer resource to peer %x failed: %v", p.destinationHash, err)
		}
		p.mu.Lock()
		if p.link != nil {
			p.link.Teardown()
		}
		p.link = nil
		p.state = PeerStateIdle
		p.mu.Unlock()
		return
	}
	resource.SetCallback(p.ResourceConcluded)

	p.mu.Lock()
	p.currentlyTransferringMessages = wantedIDs
	p.currentSyncTransferStarted = peerTime(now)
	p.state = PeerStateResourceTransferring
	p.mu.Unlock()
}

// ResourceConcluded is the Go port of Python's LXMPeer.resource_concluded
// (LXMPeer.py:492-533). On a completed transfer it marks the transferred
// messages handled + removed, tears down the link, returns the peer to Idle,
// updates sync statistics, and re-enters Sync for persistent peers that still
// have unhandled messages. On a failed transfer it tears down and idles
// without touching the message store.
func (p *Peer) ResourceConcluded(resource *rns.Resource) {
	if p == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	if resource == nil {
		return
	}

	if resource.Status() != rns.ResourceStatusComplete {
		// Transfer failed or was cancelled: tear down and idle without
		// altering the message store (LXMPeer.py:524-533).
		if logger := p.peerLogger(); logger != nil {
			logger.Verbose("Resource transfer for LXMF peer sync to %x failed", p.destinationHash)
		}
		p.mu.Lock()
		link := p.link
		p.link = nil
		p.state = PeerStateIdle
		p.currentlyTransferringMessages = nil
		p.currentSyncTransferStarted = 0
		p.mu.Unlock()
		if link != nil {
			link.Teardown()
		}
		return
	}

	transferring := func() [][]byte {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.currentlyTransferringMessages
	}()

	// Mark every transferred message handled + removed (LXMPeer.py:503-506).
	for _, tid := range transferring {
		p.addHandledMessage(tid)
		p.removeUnhandledMessage(tid)
	}

	now := p.nowFn()()
	p.mu.Lock()
	link := p.link
	p.link = nil
	p.state = PeerStateIdle
	if p.currentSyncTransferStarted != 0 {
		elapsed := peerTime(now) - p.currentSyncTransferStarted
		if elapsed > 0 {
			p.syncTransferRate = float64(resource.GetTransferSize()*8) / elapsed
		}
	}
	p.alive = true
	p.lastHeard = peerTime(now)
	p.offered += len(p.lastOffer)
	p.outgoing += len(transferring)
	p.txBytes += resource.GetDataSize()
	p.currentlyTransferringMessages = nil
	p.currentSyncTransferStarted = 0
	p.mu.Unlock()
	if link != nil {
		link.Teardown()
	}

	if logger := p.peerLogger(); logger != nil {
		logger.Verbose("Syncing %v messages to peer %x completed", len(transferring), p.destinationHash)
	}

	// Persistent peers re-sync immediately when unhandled messages remain
	// (LXMPeer.py:531-533).
	if p.syncStrategy == PeerStrategyPersistent && p.UnhandledMessageCount() > 0 {
		p.Sync()
	}
}

// Name returns the peer's display name extracted from its metadata
// (PN_META_NAME). It is the Go port of Python's LXMPeer.name property.
func (p *Peer) Name() string {
	if p == nil {
		return ""
	}
	if p.metadata == nil {
		return ""
	}
	v, ok := p.metadata[PNMetaName]
	if !ok {
		return ""
	}
	switch value := v.(type) {
	case []byte:
		return string(value)
	case string:
		return value
	}
	return ""
}

func (p *Peer) nowFn() func() time.Time {
	if p != nil && p.now != nil {
		return p.now
	}
	return time.Now
}
