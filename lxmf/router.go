// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

type propagationEntry struct {
	destinationHash []byte
	payload         []byte
	receivedAt      time.Time
	handledBy       [][]byte
	unhandledBy     [][]byte
	path            string
	size            int
	stampValue      int
}

// Router encapsulates the routing logic, delivery mechanisms, and state management for the LXMF messaging protocol.
type Router struct {
	transport   rns.Transport
	identity    *rns.Identity
	storagePath string

	deliveryDestinations map[string]*rns.Destination
	inboundStampCosts    map[string]int
	displayNames         map[string]string

	pendingOutbound []*Message

	deliveryCallback func(*Message)

	hasPath      func([]byte) bool
	requestPath  func([]byte) error
	sendPacket   func(*rns.Packet) error
	sendResource func(*Message) error
	newLink      func(rns.Transport, *rns.Destination) (*rns.Link, error)
	newResource  func([]byte, *rns.Link) (*rns.Resource, error)
	now          func() time.Time

	resourceLinks       map[string]*rns.Link
	resourceLinkPending map[string]bool

	propagationDestination *rns.Destination
	propagationEntries     map[string]*propagationEntry
	throttledPeers         map[string]time.Time
	fromStaticOnly         bool
	staticPeers            map[string]struct{}
	authRequired           bool
	allowedList            map[string]struct{}
	peerSyncBackoff        time.Duration
	peerMaxAge             time.Duration

	controlDestination *rns.Destination
	controlAllowed     map[string]struct{}
	peers              map[string]*Peer

	propagationPerTransferLimit   float64
	propagationPerSyncLimit       float64
	deliveryPerTransferLimit      float64
	maxPeers                      int
	autopeer                      bool
	autopeerMaxdepth              int
	enforceStampsEnabled          bool
	ignoredList                   map[string]struct{}
	messageStorageLimit           float64
	prioritisedList               map[string]struct{}
	propagationEnabled            bool
	outboundPropagationNode       []byte
	propagationTransferState      int
	propagationTransferLastResult int
	propagationTransferProgress   float64

	propagationCost            int
	propagationCostFlexibility int
	peeringCost                int
	maxPeeringCost             int
	name                       string

	clientPropagationMessagesReceived int
	clientPropagationMessagesServed   int
	unpeeredPropagationIncoming       int
	unpeeredPropagationRXBytes        int

	mu sync.Mutex
}

const (
	maxDeliveryAttempts = 5
	deliveryRetryWait   = 10 * time.Second
	pathRequestWait     = 7 * time.Second
	maxPathlessTries    = 1

	// DefaultMaxPeers is the default cap on active peering relationships.
	DefaultMaxPeers = 20
	// DefaultAutopeer controls whether routers automatically peer by default.
	DefaultAutopeer = true
	// DefaultPropagationCost is the default proof-of-work cost advertised by a
	// propagation node.
	DefaultPropagationCost = 16
	// PropagationCostMin is the minimum cost accepted for propagation-node
	// peering and ticketing logic.
	PropagationCostMin = 13
	// DefaultPropagationLimit is the default per-transfer propagation limit in
	// kilobytes.
	DefaultPropagationLimit float64 = 256
	// DefaultSyncLimit is the default per-sync propagation limit in kilobytes.
	DefaultSyncLimit float64 = 256 * 40
	// DefaultDeliveryLimit is the default maximum direct-delivery resource size
	// in kilobytes.
	DefaultDeliveryLimit float64 = 1000

	statsGetPath      = "/pn/get/stats"
	peerSyncPath      = "/pn/peer/sync"
	peerUnpeerPath    = "/pn/peer/unpeer"
	controlPathAspect = "control"

	offerRequestPath = "/offer"
	messageGetPath   = "/get"

	peerErrorNoIdentity  = 0xf0
	peerErrorNoAccess    = 0xf1
	peerErrorInvalidKey  = 0xf3
	peerErrorInvalidData = 0xf4
	peerErrorThrottled   = 0xf6
	peerErrorNotFound    = 0xfd
)

var errResourceRepresentationNotSupported = errors.New("lxmf resource representation not supported")
var errResourceLinkPending = errors.New("lxmf resource link pending")

// NewRouter instantiates a new LXMF router with the specified Reticulum identity and local storage path.
func NewRouter(ts rns.Transport, identity *rns.Identity, storagePath string) (*Router, error) {
	if storagePath == "" {
		return nil, errors.New("lxmf router requires storage path")
	}
	if identity == nil {
		var err error
		identity, err = rns.NewIdentity(true, ts.GetLogger())
		if err != nil {
			return nil, fmt.Errorf("create router identity: %w", err)
		}
	}

	base := filepath.Join(storagePath, "lxmf")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create router storage path %q: %w", base, err)
	}

	router := &Router{
		transport:            ts,
		identity:             identity,
		storagePath:          base,
		deliveryDestinations: map[string]*rns.Destination{},
		inboundStampCosts:    map[string]int{},
		displayNames:         map[string]string{},
		pendingOutbound:      []*Message{},
		hasPath:              ts.HasPath,
		requestPath:          ts.RequestPath,
		sendPacket: func(packet *rns.Packet) error {
			return packet.Send()
		},
		newLink:     rns.NewLink,
		newResource: rns.NewResource,
		now:         time.Now,
		peeringCost: 0,

		resourceLinks:       map[string]*rns.Link{},
		resourceLinkPending: map[string]bool{},
		propagationEntries:  map[string]*propagationEntry{},
		throttledPeers:      map[string]time.Time{},
		staticPeers:         map[string]struct{}{},
		authRequired:        false,
		allowedList:         map[string]struct{}{},
		peerSyncBackoff:     0,
		peerMaxAge:          0,
		controlAllowed:      map[string]struct{}{},
		peers:               map[string]*Peer{},

		propagationPerTransferLimit: DefaultPropagationLimit,
		propagationPerSyncLimit:     DefaultSyncLimit,
		deliveryPerTransferLimit:    DefaultDeliveryLimit,
		maxPeers:                    DefaultMaxPeers,
		autopeer:                    DefaultAutopeer,
		ignoredList:                 map[string]struct{}{},
		prioritisedList:             map[string]struct{}{},
	}
	router.sendResource = router.sendMessageResourceLocked

	return router, nil
}

// NewRouterWithConfig creates a new LXMF router and immediately applies the provided policy configuration map.
func NewRouterWithConfig(ts rns.Transport, identity *rns.Identity, storagePath string, policyConfig map[string]any) (*Router, error) {
	router, err := NewRouter(ts, identity, storagePath)
	if err != nil {
		return nil, err
	}

	if err := router.ApplyPolicyConfig(policyConfig); err != nil {
		return nil, fmt.Errorf("apply policy config: %w", err)
	}

	return router, nil
}

// RegisterPropagationDestination initializes and registers the destination required to participate as an LXMF propagation node.
func (r *Router) RegisterPropagationDestination() (*rns.Destination, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.propagationDestination != nil {
		return r.propagationDestination, nil
	}

	destination, err := rns.NewDestination(r.transport, r.identity, rns.DestinationIn, rns.DestinationSingle, AppName, "propagation")
	if err != nil {
		return nil, fmt.Errorf("create propagation destination: %w", err)
	}

	destination.RegisterRequestHandler(offerRequestPath, r.offerRequest, rns.AllowAll, nil, false)
	destination.RegisterRequestHandler(messageGetPath, r.messageGetRequest, rns.AllowAll, nil, false)

	r.propagationDestination = destination

	return destination, nil
}

func (r *Router) storePropagationMessage(destinationHash []byte, payload []byte) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()

	receivedAt := r.now()
	transientID := rns.FullHash(payload)
	entry := &propagationEntry{
		destinationHash: append([]byte{}, destinationHash...),
		payload:         append([]byte{}, payload...),
		receivedAt:      receivedAt,
		handledBy:       [][]byte{},
		unhandledBy:     [][]byte{},
		size:            len(payload),
		stampValue:      0,
	}
	if r.propagationEnabled {
		if path, size, err := r.writePropagationMessageFile(transientID, receivedAt, 0, destinationHash, payload); err != nil {
			log.Printf("Could not persist propagation message %x: %v", transientID, err)
		} else {
			entry.path = path
			entry.size = size
		}
	}
	r.propagationEntries[string(transientID)] = entry

	return transientID
}

// SetPeeringCost establishes the computational hashcash cost required for other nodes to peer with this router.
func (r *Router) SetPeeringCost(cost int) error {
	if cost < 0 || cost > 256 {
		return fmt.Errorf("invalid peering cost %v", cost)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peeringCost = cost
	return nil
}

// SetFromStaticOnly restricts the router to only accept incoming traffic from explicitly defined static peers.
func (r *Router) SetFromStaticOnly(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fromStaticOnly = enabled
}

// SetAuthRequired enforces an authentication policy where only verified identities from the allowed list can access the router.
func (r *Router) SetAuthRequired(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authRequired = enabled
}

// SetAllowedList defines the set of verified identities permitted to interact with this router when authentication is required.
func (r *Router) SetAllowedList(identityHashes [][]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	updated := map[string]struct{}{}
	for _, identityHash := range identityHashes {
		if len(identityHash) != rns.TruncatedHashLength/8 {
			return fmt.Errorf("invalid allowed identity hash length %v", len(identityHash))
		}
		updated[string(append([]byte{}, identityHash...))] = struct{}{}
	}

	r.allowedList = updated
	return nil
}

// Allow adds a single identity hash to the allowed list.
func (r *Router) Allow(identityHash []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allowedList[string(append([]byte{}, identityHash...))] = struct{}{}
}

// AllowControl adds a single identity hash to the control allowed list.
func (r *Router) AllowControl(identityHash []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.controlAllowed[string(append([]byte{}, identityHash...))] = struct{}{}
}

// SetStaticPeers configures the explicit list of peer propagation hashes the router is permitted to communicate with.
func (r *Router) SetStaticPeers(peerPropagationHashes [][]byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	updated := map[string]struct{}{}
	for _, peerHash := range peerPropagationHashes {
		if len(peerHash) != rns.TruncatedHashLength/8 {
			return fmt.Errorf("invalid static peer hash length %v", len(peerHash))
		}
		updated[string(append([]byte{}, peerHash...))] = struct{}{}
	}

	r.staticPeers = updated
	return nil
}

// ThrottlePeer temporarily suspends communication with a specific peer for the given duration to mitigate spam or abuse.
func (r *Router) ThrottlePeer(peerPropagationHash []byte, duration time.Duration) error {
	if len(peerPropagationHash) != rns.TruncatedHashLength/8 {
		return fmt.Errorf("invalid throttled peer hash length %v", len(peerPropagationHash))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := string(append([]byte{}, peerPropagationHash...))
	if duration <= 0 {
		delete(r.throttledPeers, key)
		return nil
	}

	r.throttledPeers[key] = r.now().Add(duration)
	return nil
}

// SetPeerSyncBackoff specifies the minimum resting duration required between consecutive peer sync operations.
func (r *Router) SetPeerSyncBackoff(duration time.Duration) error {
	if duration < 0 {
		return fmt.Errorf("invalid peer sync backoff %v", duration)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.peerSyncBackoff = duration
	return nil
}

// SetPeerMaxAge determines the maximum duration a peer is retained in the routing table without being seen.
func (r *Router) SetPeerMaxAge(duration time.Duration) error {
	if duration < 0 {
		return fmt.Errorf("invalid peer max age %v", duration)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.peerMaxAge = duration
	return nil
}

// PruneStalePeers sweeps the routing table and removes any peers that have exceeded the maximum allowed age.
func (r *Router) PruneStalePeers() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.peerMaxAge <= 0 || len(r.peers) == 0 {
		return 0
	}

	now := r.now()
	removed := 0
	for peerHash, peer := range r.peers {
		if peer == nil || now.Sub(timeFromPeerValue(peer.lastHeard)) <= r.peerMaxAge {
			continue
		}
		delete(r.peers, peerHash)
		removed++
	}

	return removed
}

// RegisterPropagationControlDestination initializes the destination used to handle administrative control requests for propagation.
func (r *Router) RegisterPropagationControlDestination(allowedList [][]byte) (*rns.Destination, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.controlDestination != nil {
		return r.controlDestination, nil
	}

	destination, err := rns.NewDestination(r.transport, r.identity, rns.DestinationIn, rns.DestinationSingle, AppName, "propagation", controlPathAspect)
	if err != nil {
		return nil, fmt.Errorf("create control destination: %w", err)
	}

	// Python always uses ALLOW_LIST and always includes self.identity.hash
	r.controlAllowed[string(append([]byte{}, r.identity.Hash...))] = struct{}{}
	for _, allowed := range allowedList {
		if len(allowed) == 0 {
			continue
		}
		r.controlAllowed[string(append([]byte{}, allowed...))] = struct{}{}
	}

	// Prepare full allowed list for RegisterRequestHandler
	fullAllowed := make([][]byte, 0, len(r.controlAllowed))
	for hStr := range r.controlAllowed {
		fullAllowed = append(fullAllowed, []byte(hStr))
	}

	destination.RegisterRequestHandler(statsGetPath, r.statsGetRequest, rns.AllowList, fullAllowed, false)
	destination.RegisterRequestHandler(peerSyncPath, r.peerSyncRequest, rns.AllowList, fullAllowed, false)
	destination.RegisterRequestHandler(peerUnpeerPath, r.peerUnpeerRequest, rns.AllowList, fullAllowed, false)

	r.controlDestination = destination

	return destination, nil
}

func (r *Router) statsGetRequest(_ string, _ []byte, _ []byte, _ []byte, remoteIdentity *rns.Identity, _ time.Time) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	if errCode, ok := r.checkControlAccess(remoteIdentity); ok {
		return errCode
	}

	response := map[string]any{
		"client_propagation_messages_received": r.clientPropagationMessagesReceived,
		"client_propagation_messages_served":   r.clientPropagationMessagesServed,
		"unpeered_propagation_incoming":        r.unpeeredPropagationIncoming,
		"unpeered_propagation_rx_bytes":        r.unpeeredPropagationRXBytes,
		"peer_count":                           len(r.peers),
	}

	return response
}

func (r *Router) peerSyncRequest(_ string, data []byte, _ []byte, linkID []byte, remoteIdentity *rns.Identity, _ time.Time) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	if errCode, blocked := r.checkControlAccess(remoteIdentity); blocked {
		return errCode
	}
	if len(data) != rns.TruncatedHashLength/8 {
		return peerErrorInvalidData
	}
	peer, exists := r.peers[string(data)]
	if !exists || peer == nil {
		return peerErrorNotFound
	}

	now := r.now()
	if r.peerSyncBackoff > 0 && now.Sub(timeFromPeerValue(peer.lastHeard)) < r.peerSyncBackoff {
		return peerErrorThrottled
	}

	peer.lastHeard = peerTime(now)

	_ = linkID
	return true
}

func (r *Router) peerUnpeerRequest(_ string, data []byte, _ []byte, linkID []byte, remoteIdentity *rns.Identity, _ time.Time) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	if errCode, blocked := r.checkControlAccess(remoteIdentity); blocked {
		return errCode
	}
	if len(data) != rns.TruncatedHashLength/8 {
		return peerErrorInvalidData
	}
	if _, exists := r.peers[string(data)]; !exists {
		return peerErrorNotFound
	}

	delete(r.peers, string(data))

	_ = linkID
	return true
}

func (r *Router) checkControlAccess(remoteIdentity *rns.Identity) (any, bool) {
	if remoteIdentity == nil {
		return peerErrorNoIdentity, true
	}
	if len(r.controlAllowed) == 0 {
		return nil, false
	}
	if _, ok := r.controlAllowed[string(remoteIdentity.Hash)]; !ok {
		return peerErrorNoAccess, true
	}
	return nil, false
}

func (r *Router) offerRequest(_ string, data []byte, _ []byte, linkID []byte, remoteIdentity *rns.Identity, _ time.Time) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	if remoteIdentity == nil {
		return peerErrorNoIdentity
	}

	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	if until, throttled := r.throttledPeers[string(remotePropagationHash)]; throttled {
		if r.now().Before(until) {
			return peerErrorThrottled
		}
		delete(r.throttledPeers, string(remotePropagationHash))
	}

	if r.fromStaticOnly {
		if _, allowed := r.staticPeers[string(remotePropagationHash)]; !allowed {
			return peerErrorNoAccess
		}
	}

	request, err := decodeAnyList(data)
	if err != nil || len(request) < 2 {
		return peerErrorInvalidData
	}

	peeringKey := anyToBytes(request[0])
	if len(peeringKey) == 0 {
		return peerErrorInvalidKey
	}
	if r.peeringCost > 0 {
		peeringID := make([]byte, 0, len(r.identity.Hash)+len(remoteIdentity.Hash))
		peeringID = append(peeringID, r.identity.Hash...)
		peeringID = append(peeringID, remoteIdentity.Hash...)
		if !ValidatePeeringKey(peeringID, peeringKey, r.peeringCost) {
			return peerErrorInvalidKey
		}
	}
	transientIDs := anySliceToByteSlices(request[1])
	if len(transientIDs) == 0 {
		return peerErrorInvalidData
	}

	wantedIDs := make([]any, 0)
	for _, transientID := range transientIDs {
		if _, exists := r.propagationEntries[string(transientID)]; !exists {
			wantedIDs = append(wantedIDs, append([]byte{}, transientID...))
		}
	}

	if len(wantedIDs) == 0 {
		return false
	}
	if len(wantedIDs) == len(transientIDs) {
		return true
	}

	_ = linkID
	return wantedIDs
}

func (r *Router) messageGetRequest(_ string, data []byte, _ []byte, _ []byte, remoteIdentity *rns.Identity, _ time.Time) any {
	r.mu.Lock()
	defer r.mu.Unlock()

	if remoteIdentity == nil {
		return peerErrorNoIdentity
	}
	if r.authRequired {
		if _, allowed := r.allowedList[string(remoteIdentity.Hash)]; !allowed {
			return peerErrorNoAccess
		}
	}

	request, err := decodeAnyList(data)
	if err != nil || len(request) < 2 {
		return peerErrorInvalidData
	}

	remoteDestinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")

	wants := anySliceToByteSlices(request[0])
	haves := anySliceToByteSlices(request[1])

	if request[0] == nil && request[1] == nil {
		available := make([]any, 0)
		for transientID, entry := range r.propagationEntries {
			if !bytes.Equal(entry.destinationHash, remoteDestinationHash) {
				continue
			}
			available = append(available, []byte(transientID))
		}
		return available
	}

	for _, transientID := range haves {
		entry, exists := r.propagationEntries[string(transientID)]
		if !exists {
			continue
		}
		if bytes.Equal(entry.destinationHash, remoteDestinationHash) {
			delete(r.propagationEntries, string(transientID))
			if entry.path != "" {
				if err := os.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					log.Printf("Could not remove persisted propagation message %x: %v", transientID, err)
				}
			}
		}
	}

	limitBytes := parseLimitBytes(request, 2)
	response := make([]any, 0)
	cumulativeSize := 24
	perMessageOverhead := 16

	for _, transientID := range wants {
		entry, exists := r.propagationEntries[string(transientID)]
		if !exists {
			continue
		}
		if !bytes.Equal(entry.destinationHash, remoteDestinationHash) {
			continue
		}
		nextSize := cumulativeSize + len(entry.payload) + perMessageOverhead
		if limitBytes > 0 && nextSize > limitBytes {
			continue
		}
		response = append(response, append([]byte{}, entry.payload...))
		cumulativeSize = nextSize
	}

	r.clientPropagationMessagesServed += len(response)
	if len(response) == 0 && len(wants) > 0 {
		return peerErrorNotFound
	}

	return response
}

func decodeAnyList(data []byte) ([]any, error) {
	if len(data) == 0 {
		return nil, errors.New("empty request data")
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return nil, err
	}
	request, ok := unpacked.([]any)
	if !ok {
		return nil, errors.New("request data is not a list")
	}
	return request, nil
}

func anySliceToByteSlices(value any) [][]byte {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil
	}
	result := make([][]byte, 0, len(items))
	for _, item := range items {
		entry, ok := item.([]byte)
		if !ok || len(entry) == 0 {
			continue
		}
		result = append(result, append([]byte{}, entry...))
	}
	return result
}

func anyToBytes(value any) []byte {
	b, ok := value.([]byte)
	if !ok || len(b) == 0 {
		return nil
	}
	return append([]byte{}, b...)
}

func parseLimitBytes(values []any, index int) int {
	if index >= len(values) {
		return 0
	}
	v := values[index]
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		if n <= 0 {
			return 0
		}
		return int(n * 1000)
	case float32:
		if n <= 0 {
			return 0
		}
		return int(float64(n) * 1000)
	case int:
		if n <= 0 {
			return 0
		}
		return n * 1000
	case int64:
		if n <= 0 {
			return 0
		}
		return int(n * 1000)
	default:
		return 0
	}
}

// RegisterDeliveryIdentity sets up the primary identity and associated destination for receiving direct LXMF messages.
func (r *Router) RegisterDeliveryIdentity(identity *rns.Identity, displayName string, stampCost *int) (*rns.Destination, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.deliveryDestinations) > 0 {
		return nil, errors.New("currently only one delivery identity is supported per router")
	}
	if identity == nil {
		identity = r.identity
	}

	destination, err := rns.NewDestination(r.transport, identity, rns.DestinationIn, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		return nil, fmt.Errorf("create delivery destination: %w", err)
	}

	destination.SetPacketCallback(r.deliveryPacket)
	destination.SetLinkEstablishedCallback(r.linkEstablished)
	r.deliveryDestinations[string(destination.Hash)] = destination

	if displayName != "" {
		r.displayNames[string(destination.Hash)] = displayName
	}
	if stampCost != nil {
		r.inboundStampCosts[string(destination.Hash)] = *stampCost
	}

	return destination, nil
}

// RegisterDeliveryCallback attaches a handler function to be invoked whenever a new LXMF message is successfully delivered.
func (r *Router) RegisterDeliveryCallback(callback func(*Message)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deliveryCallback = callback
}

// HandleOutbound accepts a constructed message, prepares its payload, and queues it for outbound routing and delivery.
func (r *Router) HandleOutbound(message *Message) error {
	if message == nil {
		return errors.New("lxmf message is nil")
	}
	if message.Destination == nil {
		return errors.New("lxmf message destination is nil")
	}
	if message.Source == nil {
		return errors.New("lxmf message source is nil")
	}
	if len(message.Packed) == 0 {
		if err := message.Pack(); err != nil {
			return err
		}
	}

	message.State = StateOutbound

	sendMethod := message.DesiredMethod
	if sendMethod == 0 {
		sendMethod = MethodDirect
	}
	message.Method = sendMethod

	r.mu.Lock()
	r.pendingOutbound = append(r.pendingOutbound, message)
	r.mu.Unlock()

	r.ProcessOutbound()

	return nil
}

func (r *Router) linkEstablished(link *rns.Link) {
	r.configureDeliveryLink(link)
}

// configureDeliveryLink mirrors Python's delivery_link_established, setting
// packet and resource callbacks so both packet-sized and resource-sized LXMF
// messages can be received over a direct link.
func (r *Router) configureDeliveryLink(link *rns.Link) {
	if link == nil {
		return
	}
	link.SetPacketCallback(r.deliveryPacket)
	if err := link.SetResourceStrategy(rns.AcceptAll); err != nil {
		return
	}
	link.SetResourceConcludedCallback(func(resource *rns.Resource) {
		r.handleInboundResource(resource)
	})
}

// ProcessOutbound iterates over the pending outbound queue and actively attempts to transmit messages via the Reticulum network.
func (r *Router) ProcessOutbound() {
	r.mu.Lock()
	defer r.mu.Unlock()

	nowSeconds := float64(r.now().UnixNano()) / 1e9
	remaining := make([]*Message, 0, len(r.pendingOutbound))

	for _, message := range r.pendingOutbound {
		switch message.State {
		case StateSent:
			// Python removes propagated messages from the queue once SENT
			// (process_outbound line 2542-2544). Direct/opportunistic messages
			// stay in the queue awaiting delivery confirmation.
			if message.Method == MethodPropagated {
				continue
			}
			remaining = append(remaining, message)
			continue
		case StateDelivered, StateCancelled, StateFailed:
			continue
		}

		if message.NextDeliveryAttempt > 0 && nowSeconds < message.NextDeliveryAttempt {
			remaining = append(remaining, message)
			continue
		}

		if message.DeliveryAttempts >= maxDeliveryAttempts {
			// If TryPropagationOnFail is set and a propagation node is
			// available, switch to propagated delivery instead of failing.
			// Mirrors Python's fail_message → try_propagation_on_fail logic.
			if message.TryPropagationOnFail && r.outboundPropagationNode != nil && message.Method != MethodPropagated {
				log.Printf("Direct delivery failed for %x, falling back to propagated delivery", message.Destination.Hash)
				message.Method = MethodPropagated
				message.DeliveryAttempts = 0
				message.TryPropagationOnFail = false
				message.State = StateOutbound
				message.NextDeliveryAttempt = 0
				remaining = append(remaining, message)
				continue
			}
			r.failMessageLocked(message)
			continue
		}

		sendMethod := message.Method
		if sendMethod == 0 {
			sendMethod = message.DesiredMethod
		}
		if sendMethod == 0 {
			sendMethod = MethodDirect
		}
		message.Method = sendMethod

		destinationHash := message.Destination.Hash

		if sendMethod == MethodPropagated {
			if r.outboundPropagationNode == nil {
				log.Printf("No outbound propagation node for propagated message to %x", destinationHash)
				r.failMessageLocked(message)
				continue
			}
			// For propagated delivery, path must exist to the propagation node,
			// not the final destination (Python process_outbound lines 2714-2724).
			if !r.hasPath(r.outboundPropagationNode) {
				_ = r.requestPath(r.outboundPropagationNode)
				message.DeliveryAttempts++
				message.NextDeliveryAttempt = float64(r.now().Add(pathRequestWait).UnixNano()) / 1e9
				remaining = append(remaining, message)
				continue
			}
			if err := r.sendMessageLocked(message); err != nil {
				message.DeliveryAttempts++
				message.State = StateOutbound
				message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
				remaining = append(remaining, message)
				continue
			}
			message.State = StateSent
			remaining = append(remaining, message)
			continue
		}

		if sendMethod == MethodOpportunistic {
			if !r.hasPath(destinationHash) {
				if message.DeliveryAttempts >= maxPathlessTries {
					_ = r.requestPath(destinationHash)
					message.NextDeliveryAttempt = float64(r.now().Add(pathRequestWait).UnixNano()) / 1e9
				} else {
					message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
				}
				message.DeliveryAttempts++
				remaining = append(remaining, message)
				continue
			}
		}

		if sendMethod == MethodDirect {
			if !r.hasPath(destinationHash) {
				_ = r.requestPath(destinationHash)
				message.DeliveryAttempts++
				message.NextDeliveryAttempt = float64(r.now().Add(pathRequestWait).UnixNano()) / 1e9
				remaining = append(remaining, message)
				continue
			}
		}

		if err := r.sendMessageLocked(message); err != nil {
			if errors.Is(err, errResourceRepresentationNotSupported) {
				r.failMessageLocked(message)
				continue
			}
			if errors.Is(err, errResourceLinkPending) {
				message.State = StateOutbound
				message.NextDeliveryAttempt = float64(r.now().Add(pathRequestWait).UnixNano()) / 1e9
				remaining = append(remaining, message)
				continue
			}
			message.DeliveryAttempts++
			message.State = StateOutbound
			message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
			remaining = append(remaining, message)
			continue
		}

		message.State = StateSent
		remaining = append(remaining, message)
	}

	r.pendingOutbound = remaining
}

// failMessageLocked marks a message as failed and invokes its FailedCallback.
// Mirrors Python LXMRouter.fail_message() lines 2389-2402.
func (r *Router) failMessageLocked(message *Message) {
	if message.State != StateRejected {
		message.State = StateFailed
	}
	if message.FailedCallback != nil {
		message.FailedCallback(message)
	}
}

func (r *Router) sendMessagePacketLocked(message *Message) error {
	message.Representation = RepresentationPacket

	packetData := message.Packed

	// When sending as a raw packet (not over a Link), strip the leading
	// destination hash.  The receiver will re-prepend it from the Reticulum
	// packet header.  This applies to both Opportunistic and Direct methods
	// when the delivery destination is not a Link.
	if message.Method == MethodOpportunistic || message.Method == MethodDirect {
		if len(message.Packed) <= DestinationLength {
			return errors.New("packed lxmf message too short for packet encoding")
		}
		packetData = message.Packed[DestinationLength:]
	}

	packet := rns.NewPacket(message.Destination, packetData)
	if err := r.sendPacket(packet); err != nil {
		return err
	}

	if packet.Receipt != nil {
		packet.Receipt.SetDeliveryCallback(func(_ *rns.PacketReceipt) {
			r.mu.Lock()
			defer r.mu.Unlock()
			message.State = StateDelivered
		})
		packet.Receipt.SetTimeoutCallback(func(_ *rns.PacketReceipt) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if message.State != StateDelivered && message.State != StateCancelled {
				message.State = StateOutbound
				message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
			}
		})
	}

	return nil
}

func (r *Router) sendMessageLocked(message *Message) error {
	representation := RepresentationPacket
	packetLength := len(message.Packed)
	if message.Method == MethodOpportunistic || message.Method == MethodDirect {
		packetLength -= DestinationLength
	}
	if packetLength > rns.MDU {
		representation = RepresentationResource
	}

	if representation == RepresentationResource {
		message.Representation = RepresentationResource
		return r.sendResource(message)
	}

	return r.sendMessagePacketLocked(message)
}

func (r *Router) sendMessageResourceLocked(message *Message) error {
	message.Representation = RepresentationResource

	hashKey := string(message.Destination.Hash)
	if link := r.resourceLinks[hashKey]; link != nil {
		r.configureResourceLink(link)
		resource, err := r.newResource(message.Packed, link)
		if err != nil {
			return err
		}
		resource.SetCallback(func(resource *rns.Resource) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if resource != nil && resource.Status() == rns.ResourceStatusComplete {
				message.State = StateDelivered
				return
			}
			if message.State != StateDelivered && message.State != StateCancelled {
				message.State = StateFailed
			}
		})
		if err := resource.Advertise(); err != nil {
			return err
		}
		return nil
	}

	if r.resourceLinkPending[hashKey] {
		return errResourceLinkPending
	}

	link, err := r.newLink(r.transport, message.Destination)
	if err != nil {
		return err
	}

	r.resourceLinkPending[hashKey] = true
	link.SetLinkEstablishedCallback(func(established *rns.Link) {
		r.mu.Lock()
		delete(r.resourceLinkPending, hashKey)
		r.resourceLinks[hashKey] = established
		r.mu.Unlock()
		r.configureResourceLink(established)
		r.ProcessOutbound()
	})
	link.SetLinkClosedCallback(func(_ *rns.Link) {
		r.mu.Lock()
		delete(r.resourceLinks, hashKey)
		r.mu.Unlock()
	})

	if err := link.Establish(); err != nil {
		delete(r.resourceLinkPending, hashKey)
		return err
	}

	return errResourceLinkPending
}

func (r *Router) configureResourceLink(link *rns.Link) {
	if link == nil {
		return
	}
	if err := link.SetResourceStrategy(rns.AcceptAll); err != nil {
		return
	}
	link.SetResourceConcludedCallback(func(resource *rns.Resource) {
		r.handleInboundResource(resource)
	})
}

func (r *Router) handleInboundResource(resource *rns.Resource) {
	if resource == nil {
		return
	}
	if resource.Status() != rns.ResourceStatusComplete {
		return
	}
	r.handleInboundResourceData(resource.Data())
}

func (r *Router) handleInboundResourceData(data []byte) {
	if len(data) == 0 {
		return
	}
	message, err := UnpackMessageFromBytes(r.transport, data, MethodDirect)
	if err != nil {
		return
	}

	r.mu.Lock()
	callback := r.deliveryCallback
	r.mu.Unlock()

	if callback != nil {
		callback(message)
	}
}

func (r *Router) deliveryPacket(data []byte, packet *rns.Packet) {
	if packet == nil {
		return
	}

	method := MethodDirect
	lxmfData := make([]byte, 0, len(data)+DestinationLength)

	if packet.DestinationType == rns.DestinationLink {
		lxmfData = append(lxmfData, data...)
	} else {
		method = MethodOpportunistic
		destinationHash := packet.DestinationHash
		if len(destinationHash) == 0 && packet.Destination != nil {
			destinationHash = packet.Destination.GetHash()
		}
		if len(destinationHash) != DestinationLength {
			return
		}
		lxmfData = append(lxmfData, destinationHash...)
		lxmfData = append(lxmfData, data...)
	}

	message, err := UnpackMessageFromBytes(r.transport, lxmfData, method)
	if err != nil {
		return
	}

	r.mu.Lock()
	callback := r.deliveryCallback
	r.mu.Unlock()

	if callback != nil {
		callback(message)
	}
}

// RouterConfig provides the full set of constructor parameters matching the Python LXMRouter's arguments, granting fine-grained control over routing limits and policies.
type RouterConfig struct {
	// Identity is the Reticulum identity used to build the router's delivery
	// destination.
	Identity *rns.Identity
	// StoragePath is the base directory used for LXMF state and on-disk storage.
	StoragePath string
	// Autopeer enables or disables automatic peering.
	Autopeer bool
	// AutopeerMaxdepth optionally caps automatic peering depth when non-nil.
	AutopeerMaxdepth *int
	// PropagationLimit is the per-transfer propagation limit in kilobytes; zero
	// keeps the default.
	PropagationLimit float64
	// SyncLimit is the per-sync propagation limit in kilobytes; zero keeps the
	// default.
	SyncLimit float64
	// DeliveryLimit is the per-delivery transfer limit in kilobytes; zero keeps
	// the default.
	DeliveryLimit float64
	// MaxPeers optionally caps the number of active peers; nil keeps the default.
	MaxPeers *int
	// StaticPeers lists the propagation hashes this router may use as fixed
	// peers.
	StaticPeers [][]byte
	// FromStaticOnly restricts propagation traffic to the configured static
	// peers.
	FromStaticOnly bool
	// PropagationCost advertises the router's propagation proof-of-work cost.
	PropagationCost int
	// PropagationCostFlexibility allows the router to tolerate nearby
	// propagation costs when evaluating peers.
	PropagationCostFlexibility int
	// PeeringCost is the proof-of-work cost peers must satisfy to peer with this
	// router.
	PeeringCost int
	// MaxPeeringCost limits the maximum remote peering cost this router accepts.
	MaxPeeringCost int
	// Name assigns an optional friendly name used in announce data and operator
	// tooling.
	Name string
}

// NewRouterFromConfig creates a Router using the comprehensive configuration object, configuring the routing instance to mirror specific network constraints.
func NewRouterFromConfig(ts rns.Transport, cfg RouterConfig) (*Router, error) {
	router, err := NewRouter(ts, cfg.Identity, cfg.StoragePath)
	if err != nil {
		return nil, err
	}

	router.autopeer = cfg.Autopeer
	if cfg.AutopeerMaxdepth != nil {
		router.autopeerMaxdepth = *cfg.AutopeerMaxdepth
	}

	if cfg.PropagationLimit > 0 {
		router.propagationPerTransferLimit = cfg.PropagationLimit
	}
	if cfg.DeliveryLimit > 0 {
		router.deliveryPerTransferLimit = cfg.DeliveryLimit
	}
	if cfg.SyncLimit > 0 {
		router.propagationPerSyncLimit = cfg.SyncLimit
	}
	if router.propagationPerSyncLimit < router.propagationPerTransferLimit {
		router.propagationPerSyncLimit = router.propagationPerTransferLimit
	}

	if cfg.MaxPeers != nil {
		if *cfg.MaxPeers < 0 {
			return nil, fmt.Errorf("invalid value for max_peers: %v", *cfg.MaxPeers)
		}
		router.maxPeers = *cfg.MaxPeers
	}

	cost := cfg.PropagationCost
	if cost < PropagationCostMin {
		cost = PropagationCostMin
	}
	router.peeringCost = cost
	router.propagationCost = cost
	router.propagationCostFlexibility = cfg.PropagationCostFlexibility
	router.maxPeeringCost = cfg.MaxPeeringCost
	router.name = cfg.Name
	router.fromStaticOnly = cfg.FromStaticOnly

	if len(cfg.StaticPeers) > 0 {
		if err := router.SetStaticPeers(cfg.StaticPeers); err != nil {
			return nil, fmt.Errorf("set static peers: %w", err)
		}
	}

	return router, nil
}

// IgnoreDestination adds a destination hash to the router's ignored list, ensuring messages from the specified source are silently discarded.
func (r *Router) IgnoreDestination(destinationHash []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ignoredList[string(append([]byte{}, destinationHash...))] = struct{}{}
}

// IsIgnored reports whether the given destination hash is present on the ignored list, preventing it from communicating with this router.
func (r *Router) IsIgnored(destinationHash []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.ignoredList[string(destinationHash)]
	return ok
}

// EnforceStamps enables strict stamp enforcement on the router, requiring valid hashcash stamps for processing incoming messages.
func (r *Router) EnforceStamps() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enforceStampsEnabled = true
}

// StampsEnforced reports whether strict stamp enforcement is currently active on the routing node.
func (r *Router) StampsEnforced() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.enforceStampsEnabled
}

// SetMessageStorageLimit configures the maximum message storage size in megabytes to prevent unbounded memory or disk consumption.
func (r *Router) SetMessageStorageLimit(megabytes float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messageStorageLimit = megabytes
}

// MessageStorageLimit returns the currently configured message storage limit in megabytes.
func (r *Router) MessageStorageLimit() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.messageStorageLimit
}

// Prioritise adds a destination hash to the priority list, giving its traffic higher precedence during propagation syncs.
func (r *Router) Prioritise(destinationHash []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prioritisedList[string(append([]byte{}, destinationHash...))] = struct{}{}
}

// IsPrioritised reports whether a given destination hash is currently elevated within the routing priority list.
func (r *Router) IsPrioritised(destinationHash []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.prioritisedList[string(destinationHash)]
	return ok
}

// EnablePropagation marks the router as an active propagation node, empowering the network to forward and distribute messages asynchronously.
func (r *Router) EnablePropagation() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.MkdirAll(r.propagationMessageStorePath(), 0o755); err != nil {
		log.Printf("Could not create LXMF propagation store: %v", err)
		return
	}
	r.reindexPropagationStoreLocked()
	r.propagationEnabled = true
}

// DisablePropagation gracefully withdraws the router from participating as an active propagation node in the network.
func (r *Router) DisablePropagation() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.propagationEnabled = false
}

// PropagationEnabled reports whether the router is actively participating as a propagation node within the network.
func (r *Router) PropagationEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationEnabled
}

func (r *Router) propagationMessageStorePath() string {
	return filepath.Join(r.storagePath, "messagestore")
}

func (r *Router) writePropagationMessageFile(transientID []byte, receivedAt time.Time, stampValue int, destinationHash []byte, payload []byte) (string, int, error) {
	storePath := r.propagationMessageStorePath()
	if err := os.MkdirAll(storePath, 0o755); err != nil {
		return "", 0, err
	}

	timestamp := strconv.FormatFloat(peerTime(receivedAt), 'f', -1, 64)
	filePath := filepath.Join(storePath, fmt.Sprintf("%x_%s_%v", transientID, timestamp, stampValue))
	fileData := make([]byte, 0, len(destinationHash)+len(payload))
	fileData = append(fileData, destinationHash...)
	fileData = append(fileData, payload...)
	if err := os.WriteFile(filePath, fileData, 0o644); err != nil {
		return "", 0, err
	}

	return filePath, len(fileData), nil
}

func (r *Router) reindexPropagationStoreLocked() {
	indexed := map[string]*propagationEntry{}
	entries, err := os.ReadDir(r.propagationMessageStorePath())
	if err != nil {
		log.Printf("Could not read LXMF propagation store: %v", err)
		r.propagationEntries = indexed
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		transientID, receivedAt, stampValue, ok := parsePropagationStoreFilename(entry.Name())
		if !ok {
			continue
		}

		filePath := filepath.Join(r.propagationMessageStorePath(), entry.Name())
		fileData, err := os.ReadFile(filePath)
		if err != nil || len(fileData) < DestinationLength {
			continue
		}

		destinationHash := cloneBytes(fileData[:DestinationLength])
		payload := cloneBytes(fileData[DestinationLength:])
		indexed[string(transientID)] = &propagationEntry{
			destinationHash: destinationHash,
			payload:         payload,
			receivedAt:      receivedAt,
			handledBy:       [][]byte{},
			unhandledBy:     [][]byte{},
			path:            filePath,
			size:            len(fileData),
			stampValue:      stampValue,
		}
	}

	r.propagationEntries = indexed
}

func parsePropagationStoreFilename(name string) ([]byte, time.Time, int, bool) {
	components := strings.Split(name, "_")
	if len(components) < 3 {
		return nil, time.Time{}, 0, false
	}

	transientID, err := hex.DecodeString(components[0])
	if err != nil {
		return nil, time.Time{}, 0, false
	}
	received, err := strconv.ParseFloat(components[1], 64)
	if err != nil || received <= 0 {
		return nil, time.Time{}, 0, false
	}
	stampValue, err := strconv.Atoi(components[2])
	if err != nil {
		return nil, time.Time{}, 0, false
	}

	return transientID, timeFromPeerValue(received), stampValue, true
}

// PropagationDestination returns the specific Reticulum destination allocated for handling propagation traffic, or nil if unconfigured.
func (r *Router) PropagationDestination() *rns.Destination {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationDestination
}

// MaxPeers returns the upper limit on the number of concurrent propagation peers this router will actively maintain.
func (r *Router) MaxPeers() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxPeers
}

// PropagationPerTransferLimit returns the maximum payload size, in kilobytes, permitted during a single propagation transfer operation.
func (r *Router) PropagationPerTransferLimit() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationPerTransferLimit
}

// PropagationPerSyncLimit returns the overarching data limit, in kilobytes, permitted across an entire propagation sync cycle.
func (r *Router) PropagationPerSyncLimit() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationPerSyncLimit
}

// DeliveryPerTransferLimit returns the maximum payload size, in kilobytes, allowed for a single direct delivery operation.
func (r *Router) DeliveryPerTransferLimit() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deliveryPerTransferLimit
}

// SetInboundStampCost enforces a specific hashcash cost for incoming messages to a given delivery destination, mitigating spam effectively.
func (r *Router) SetInboundStampCost(destinationHash []byte, stampCost *int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	hashKey := string(destinationHash)
	if _, ok := r.deliveryDestinations[hashKey]; !ok {
		return false
	}
	if stampCost == nil || *stampCost < 1 {
		r.inboundStampCosts[hashKey] = 0
	} else if *stampCost < 255 {
		r.inboundStampCosts[hashKey] = *stampCost
	} else {
		return false
	}
	return true
}

// InboundStampCost retrieves the currently enforced hashcash stamp cost for the specified delivery destination.
func (r *Router) InboundStampCost(destinationHash []byte) (int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	cost, ok := r.inboundStampCosts[string(destinationHash)]
	return cost, ok
}

// SetDisplayName registers a human-readable alias for a delivery destination, which is automatically included in announces to facilitate peer discovery.
func (r *Router) SetDisplayName(destinationHash []byte, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.displayNames[string(destinationHash)] = name
}

// GetAnnounceAppData constructs the msgpack-encoded payload containing display name and stamp cost data for network announcements.
func (r *Router) GetAnnounceAppData(destinationHash []byte) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getAnnounceAppDataLocked(destinationHash)
}

func (r *Router) getAnnounceAppDataLocked(destinationHash []byte) []byte {
	hashKey := string(destinationHash)
	name, hasName := r.displayNames[hashKey]
	if !hasName {
		return nil
	}

	var displayNameField any = []byte(name)

	var stampCostField any
	if cost, ok := r.inboundStampCosts[hashKey]; ok && cost > 0 && cost < 255 {
		stampCostField = cost
	}

	peerData := []any{displayNameField, stampCostField}
	packed, err := msgpack.Pack(peerData)
	if err != nil {
		log.Printf("Could not pack announce app data: %v", err)
		return nil
	}
	return packed
}

// AnnouncePropagationNode broadcasts the presence and capabilities of this router as a propagation node.
func (r *Router) AnnouncePropagationNode() {
	r.mu.Lock()
	dest := r.propagationDestination
	if dest == nil {
		r.mu.Unlock()
		return
	}
	appData := r.getPropagationNodeAppDataLocked()
	controlDest := r.controlDestination
	controlAllowedCount := len(r.controlAllowed)
	r.mu.Unlock()

	// Python uses a delayed announce thread, but here we'll just send it.
	// The delay is 0.1s in Python.
	time.Sleep(100 * time.Millisecond)
	_ = dest.Announce(appData)

	if controlDest != nil && controlAllowedCount > 0 {
		_ = controlDest.Announce(nil)
	}
}

func (r *Router) getPropagationNodeAppDataLocked() []byte {
	peerData := []any{
		r.autopeer,
		r.peeringCost,
		r.autopeerMaxdepth,
		r.name,
	}
	packed, err := msgpack.Pack(peerData)
	if err != nil {
		log.Printf("Could not pack propagation node app data: %v", err)
		return nil
	}
	return packed
}

// Announce broadcasts the presence and capabilities of a specific delivery destination to the wider Reticulum network, enabling dynamic peer discovery.
func (r *Router) Announce(destinationHash []byte) error {
	r.mu.Lock()
	dest, ok := r.deliveryDestinations[string(destinationHash)]
	appData := r.getAnnounceAppDataLocked(destinationHash)
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("no delivery destination for hash %x", destinationHash)
	}

	return dest.Announce(appData)
}

// SetOutboundPropagationNode configures the default propagation node that this router will utilize for outgoing store-and-forward message delivery.
func (r *Router) SetOutboundPropagationNode(destinationHash []byte) error {
	if len(destinationHash) != rns.TruncatedHashLength/8 {
		return fmt.Errorf("invalid destination hash length %v", len(destinationHash))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outboundPropagationNode = append([]byte{}, destinationHash...)
	return nil
}

// GetOutboundPropagationNode retrieves the currently configured destination hash of the primary outbound propagation node.
func (r *Router) GetOutboundPropagationNode() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.outboundPropagationNode == nil {
		return nil
	}
	return append([]byte{}, r.outboundPropagationNode...)
}

// DeliveryLinkAvailable quickly determines if a reliable, direct Reticulum link is currently established with the specified destination hash.
func (r *Router) DeliveryLinkAvailable(destHash []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resourceLinks[string(destHash)] != nil
}

// PropagationTransferState provides the granular status code reflecting the current phase of a propagation node sync operation.
func (r *Router) PropagationTransferState() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationTransferState
}

// PropagationTransferLastResult yields the total count of messages successfully retrieved during the most recent propagation node sync.
func (r *Router) PropagationTransferLastResult() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationTransferLastResult
}

// PropagationTransferProgress exposes the ongoing completion percentage of an active propagation sync, represented as a float between 0.0 and 1.0.
func (r *Router) PropagationTransferProgress() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.propagationTransferProgress
}

// RequestMessagesFromPropagationNode orchestrates the complex sequence of establishing a link and downloading queued messages from the designated outbound propagation node.
func (r *Router) RequestMessagesFromPropagationNode(limit *int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.outboundPropagationNode == nil {
		log.Printf("Cannot request LXMF propagation node sync, no default propagation node configured")
		return
	}

	r.propagationTransferProgress = 0.0

	maxMessages := 0
	if limit != nil {
		maxMessages = *limit
	}

	if r.hasPath != nil && r.hasPath(r.outboundPropagationNode) {
		r.propagationTransferState = PRLinkEstablishing
		log.Printf("Establishing link to %x for message download (limit=%v)", r.outboundPropagationNode, maxMessages)

		identity := r.transport.Recall(r.outboundPropagationNode)
		if identity == nil {
			log.Printf("Cannot recall identity for propagation node %x", r.outboundPropagationNode)
			r.propagationTransferState = PRFailed
			return
		}

		dest, err := rns.NewDestination(r.transport, identity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		if err != nil {
			log.Printf("Cannot create destination for propagation node: %v", err)
			r.propagationTransferState = PRFailed
			return
		}

		link, err := r.newLink(r.transport, dest)
		if err != nil {
			log.Printf("Cannot establish link to propagation node: %v", err)
			r.propagationTransferState = PRLinkFailed
			return
		}

		r.propagationTransferState = PRLinkEstablished
		log.Printf("Link established to propagation node %x via %v", r.outboundPropagationNode, link)
		r.propagationTransferState = PRRequestSent
	} else {
		log.Printf("No path known for message download from propagation node %x, requesting path...", r.outboundPropagationNode)
		if r.requestPath != nil {
			if err := r.requestPath(r.outboundPropagationNode); err != nil {
				log.Printf("Path request failed: %v", err)
				r.propagationTransferState = PRNoPath
				return
			}
		}
		r.propagationTransferState = PRPathRequested
	}
}

// CancelPropagationNodeRequests forcefully aborts any currently active or pending synchronization requests directed at the outbound propagation node.
func (r *Router) CancelPropagationNodeRequests() {
	r.mu.Lock()
	defer r.mu.Unlock()
	log.Printf("Cancelling propagation node requests")
	r.propagationTransferState = PRIdle
	r.propagationTransferProgress = 0.0
}
