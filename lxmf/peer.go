// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"fmt"
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
)

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
	handledMessagesQueue            [][]byte
	unhandledMessagesQueue          [][]byte
	hmCount                         int
	umCount                         int
	hmCountsSynced                  bool
	umCountsSynced                  bool

	link      *rns.Link
	state     int
	lastOffer [][]byte

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

	for i := len(handledQueue) - 1; i >= 0; i-- {
		transientID := handledQueue[i]
		if !containsByteSlice(handledMessages, transientID) {
			p.addHandledMessage(transientID)
		}
		if containsByteSlice(unhandledMessages, transientID) {
			p.removeUnhandledMessage(transientID)
		}
	}

	for i := len(unhandledQueue) - 1; i >= 0; i-- {
		transientID := unhandledQueue[i]
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

func (p *Peer) removeHandledMessage(transientID []byte) {
	if p == nil || p.router == nil {
		return
	}
	p.router.mu.Lock()
	defer p.router.mu.Unlock()

	entry, exists := p.router.propagationEntries[string(transientID)]
	if !exists {
		return
	}
	entry.handledBy = removeByteSlice(entry.handledBy, p.destinationHash)
	p.hmCountsSynced = false
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
	for key, value := range in {
		out[key] = value
	}
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
