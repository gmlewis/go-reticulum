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
	"sort"
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

type outboundStampCostEntry struct {
	updatedAt time.Time
	stampCost int
}

type validatedPropagationMessage struct {
	transientID []byte
	lxmfData    []byte
	stampValue  int
}

type peerDistributionEntry struct {
	transientID  []byte
	fromPeerHash []byte
}

const messageExpiry = 30 * 24 * time.Hour
const stampCostExpiry = 45 * 24 * time.Hour
const transientIDCacheExpiry = messageExpiry * 6

// Router encapsulates the routing logic, delivery mechanisms, and state management for the LXMF messaging protocol.
type Router struct {
	transport   rns.Transport
	identity    *rns.Identity
	storagePath string

	deliveryDestinations map[string]*rns.Destination
	inboundStampCosts    map[string]int
	outboundStampCosts   map[string]outboundStampCostEntry
	displayNames         map[string]string
	ticketStore          *TicketStore
	locallyDeliveredIDs  map[string]time.Time
	locallyProcessedIDs  map[string]time.Time

	pendingOutbound       []*Message
	pendingDeferredStamps map[string]*Message
	peerDistributionQueue []peerDistributionEntry

	deliveryCallback func(*Message)

	hasPath                     func([]byte) bool
	hopsTo                      func([]byte) int
	requestPath                 func([]byte) error
	sendPacket                  func(*rns.Packet) error
	sendResource                func(*Message) error
	processOutbound             func()
	newLink                     func(rns.Transport, *rns.Destination) (*rns.Link, error)
	newResource                 func([]byte, *rns.Link) (*rns.Resource, error)
	linkStatus                  func(*rns.Link) int
	setLinkEstablishedCallback  func(*rns.Link, func(*rns.Link))
	identifyLink                func(*rns.Link, *rns.Identity) error
	establishLink               func(*rns.Link) error
	requestLink                 func(*rns.Link, string, any, func(*rns.RequestReceipt), func(*rns.RequestReceipt), func(*rns.RequestReceipt), time.Duration) (*rns.RequestReceipt, error)
	requestProgress             func(*rns.RequestReceipt) float64
	startRequestMessagesPathJob func()
	pathWaitSleep               func(time.Duration)
	teardownLink                func(*rns.Link)
	now                         func() time.Time
	processingDeferredStamps    bool

	resourceLinks       map[string]*rns.Link
	resourceLinkPending map[string]bool

	propagationDestination *rns.Destination
	propagationEntries     map[string]*propagationEntry
	throttledPeers         map[string]time.Time
	validatedPeerLinks     map[string]bool
	fromStaticOnly         bool
	staticPeers            map[string]struct{}
	authRequired           bool
	allowedList            map[string]struct{}
	peerSyncBackoff        time.Duration
	peerMaxAge             time.Duration

	controlDestination *rns.Destination
	controlAllowed     map[string]struct{}
	peers              map[string]*Peer

	propagationPerTransferLimit       float64
	propagationPerSyncLimit           float64
	deliveryPerTransferLimit          float64
	maxPeers                          int
	autopeer                          bool
	autopeerMaxdepth                  int
	enforceStampsEnabled              bool
	ignoredList                       map[string]struct{}
	messageStorageLimit               float64
	prioritisedList                   map[string]struct{}
	propagationEnabled                bool
	outboundPropagationNode           []byte
	outboundPropagationLink           *rns.Link
	outboundPropagationLinkMessage    *Message
	wantsDownloadOnPathAvailableFrom  []byte
	wantsDownloadOnPathAvailableTo    *rns.Identity
	wantsDownloadOnPathAvailableAt    time.Time
	propagationTransferState          int
	propagationTransferLastResult     int
	propagationTransferLastDuplicates int
	propagationTransferMaxMessages    int
	propagationTransferProgress       float64
	retainSyncedOnNode                bool

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
	maxDeliveryAttempts    = 5
	deliveryRetryWait      = 10 * time.Second
	pathRequestWait        = 7 * time.Second
	maxPathlessTries       = 1
	propagationPathTimeout = 10 * time.Second
	pnStampThrottle        = 180 * time.Second

	// DefaultMaxPeers is the default cap on active peering relationships.
	DefaultMaxPeers = 20
	// DefaultAutopeer controls whether routers automatically peer by default.
	DefaultAutopeer = true
	// DefaultAutopeerMaxDepth matches Python's LXMRouter.AUTOPEER_MAXDEPTH.
	DefaultAutopeerMaxDepth = 4
	// DefaultMaxPeeringCost matches Python's LXMRouter.MAX_PEERING_COST.
	DefaultMaxPeeringCost = 26
	// DefaultPeeringCost matches Python's LXMRouter.PEERING_COST.
	DefaultPeeringCost = 18
	// DefaultPropagationCost is the default proof-of-work cost advertised by a
	// propagation node.
	DefaultPropagationCost = 16
	// DefaultPropagationCostFlexibility matches Python's
	// LXMRouter.PROPAGATION_COST_FLEX.
	DefaultPropagationCostFlexibility = 3
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

	peerErrorNoIdentity   = 0xf0
	peerErrorNoAccess     = 0xf1
	peerErrorInvalidKey   = 0xf3
	peerErrorInvalidData  = 0xf4
	peerErrorInvalidStamp = 0xf5
	peerErrorThrottled    = 0xf6
	peerErrorNotFound     = 0xfd
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
		transport:             ts,
		identity:              identity,
		storagePath:           base,
		deliveryDestinations:  map[string]*rns.Destination{},
		inboundStampCosts:     map[string]int{},
		outboundStampCosts:    map[string]outboundStampCostEntry{},
		displayNames:          map[string]string{},
		ticketStore:           NewTicketStore(),
		locallyDeliveredIDs:   map[string]time.Time{},
		locallyProcessedIDs:   map[string]time.Time{},
		pendingOutbound:       []*Message{},
		pendingDeferredStamps: map[string]*Message{},
		peerDistributionQueue: []peerDistributionEntry{},
		hasPath:               ts.HasPath,
		hopsTo:                ts.HopsTo,
		requestPath:           ts.RequestPath,
		sendPacket: func(packet *rns.Packet) error {
			return packet.Send()
		},
		newLink:     rns.NewLink,
		newResource: rns.NewResource,
		linkStatus: func(link *rns.Link) int {
			return link.GetStatus()
		},
		setLinkEstablishedCallback: func(link *rns.Link, callback func(*rns.Link)) {
			link.SetLinkEstablishedCallback(callback)
		},
		identifyLink: func(link *rns.Link, identity *rns.Identity) error {
			return link.Identify(identity)
		},
		establishLink: func(link *rns.Link) error {
			return link.Establish()
		},
		requestLink: func(link *rns.Link, path string, data any, responseCallback, failedCallback, progressCallback func(*rns.RequestReceipt), timeout time.Duration) (*rns.RequestReceipt, error) {
			return link.Request(path, data, responseCallback, failedCallback, progressCallback, timeout)
		},
		requestProgress: func(receipt *rns.RequestReceipt) float64 {
			return receipt.GetProgress()
		},
		pathWaitSleep: time.Sleep,
		teardownLink: func(link *rns.Link) {
			link.Teardown()
		},
		now:                        time.Now,
		peeringCost:                DefaultPeeringCost,
		propagationCost:            DefaultPropagationCost,
		propagationCostFlexibility: DefaultPropagationCostFlexibility,

		resourceLinks:       map[string]*rns.Link{},
		resourceLinkPending: map[string]bool{},
		propagationEntries:  map[string]*propagationEntry{},
		throttledPeers:      map[string]time.Time{},
		validatedPeerLinks:  map[string]bool{},
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
		autopeerMaxdepth:            DefaultAutopeerMaxDepth,
		maxPeeringCost:              DefaultMaxPeeringCost,
		ignoredList:                 map[string]struct{}{},
		prioritisedList:             map[string]struct{}{},
	}
	router.startRequestMessagesPathJob = func() {
		go router.requestMessagesPathJob()
	}
	router.sendResource = router.sendMessageResourceLocked
	router.processOutbound = router.ProcessOutbound
	router.registerAnnounceHandlers()
	if err := router.LoadAvailableTickets(); err != nil {
		log.Printf("Could not load available tickets from storage: %v", err)
	}
	if err := router.LoadLocalTransientIDCaches(); err != nil {
		log.Printf("Could not load local transient ID caches from storage: %v", err)
	}
	if err := router.LoadOutboundStampCosts(); err != nil {
		log.Printf("Could not load outbound stamp costs from storage: %v", err)
	}

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
	destination.SetLinkEstablishedCallback(r.propagationLinkEstablished)
	destination.SetPacketCallback(r.propagationPacket)

	r.propagationDestination = destination

	return destination, nil
}

func (r *Router) propagationLinkEstablished(link *rns.Link) {
	r.configurePropagationIngressLink(link)
}

func (r *Router) configurePropagationIngressLink(link *rns.Link) {
	if link == nil {
		return
	}
	link.SetPacketCallback(r.propagationPacket)
	if err := link.SetResourceStrategy(rns.AcceptApp); err != nil {
		return
	}
	link.SetResourceCallback(func(adv *rns.ResourceAdvertisement) bool {
		return r.propagationResourceAdvertised(link, adv)
	})
	link.SetResourceStartedCallback(func(resource *rns.Resource) {
		r.propagationResourceBegan(link, resource)
	})
	link.SetResourceConcludedCallback(func(resource *rns.Resource) {
		r.propagationResourceConcluded(link, resource)
	})
}

func (r *Router) propagationResourceAdvertised(link *rns.Link, adv *rns.ResourceAdvertisement) bool {
	if adv == nil {
		return false
	}
	if r.fromStaticOnly {
		remoteIdentity := link.GetRemoteIdentity()
		if remoteIdentity == nil {
			return false
		}
		remoteHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
		r.mu.Lock()
		_, allowed := r.staticPeers[string(remoteHash)]
		r.mu.Unlock()
		if !allowed {
			return false
		}
	}

	limit := r.PropagationPerSyncLimit()
	if limit > 0 && float64(adv.D) > limit*1000 {
		return false
	}
	return true
}

func (r *Router) propagationPacket(data []byte, packet *rns.Packet) {
	if packet == nil || packet.DestinationType != rns.DestinationLink {
		return
	}

	entries, err := decodeAnyList(data)
	if err != nil || len(entries) != 2 {
		return
	}
	if _, err := anyToFloat64(entries[0]); err != nil {
		return
	}

	messages := anySliceToByteSlices(entries[1])
	if len(messages) == 0 {
		return
	}

	minAcceptedCost := r.propagationCost - r.propagationCostFlexibility
	if minAcceptedCost < 0 {
		minAcceptedCost = 0
	}

	validated := validatePropagationMessages(messages, minAcceptedCost)
	for _, entry := range validated {
		if r.ingestPropagationMessage(entry.lxmfData, nil, entry.stampValue) {
			r.mu.Lock()
			r.clientPropagationMessagesReceived++
			r.mu.Unlock()
		}
	}

	if len(validated) == len(messages) {
		packet.Prove(nil)
		return
	}

	rejectData, err := msgpack.Pack([]any{peerErrorInvalidStamp})
	if err == nil {
		rejectPacket := rns.NewPacket(packet.Destination, rejectData)
		if err := rejectPacket.Send(); err != nil {
			log.Printf("Could not send invalid propagation stamp signal: %v", err)
		}
	}
	if link, ok := packet.Destination.(*rns.Link); ok {
		link.Teardown()
	}
}

func (r *Router) propagationResourceBegan(_ *rns.Link, _ *rns.Resource) {}

func (r *Router) propagationResourceConcluded(link *rns.Link, resource *rns.Resource) {
	if link == nil || resource == nil || resource.Status() != rns.ResourceStatusComplete {
		return
	}

	entries, err := decodeAnyList(resource.Data())
	if err != nil || len(entries) != 2 {
		return
	}
	if _, err := anyToFloat64(entries[0]); err != nil {
		return
	}

	messages := anySliceToByteSlices(entries[1])
	if len(messages) == 0 {
		return
	}

	remoteIdentity := link.GetRemoteIdentity()
	var remotePropagationHash []byte
	var peer *Peer
	peeringKeyValid := false
	if remoteIdentity != nil {
		remotePropagationHash = rns.CalculateHash(remoteIdentity, AppName, "propagation")
		r.mu.Lock()
		peer = r.peers[string(remotePropagationHash)]
		peeringKeyValid = r.validatedPeerLinks[string(link.GetHash())]
		r.mu.Unlock()
	}

	if !peeringKeyValid && len(messages) > 1 {
		link.Teardown()
		return
	}

	minAcceptedCost := r.propagationCost - r.propagationCostFlexibility
	if minAcceptedCost < 0 {
		minAcceptedCost = 0
	}

	validated := validatePropagationMessages(messages, minAcceptedCost)
	for _, entry := range validated {
		r.mu.Lock()
		switch {
		case peer != nil:
			peer.incoming++
			peer.rxBytes += len(entry.lxmfData)
		case remoteIdentity != nil:
			r.unpeeredPropagationIncoming++
			r.unpeeredPropagationRXBytes += len(entry.lxmfData)
		default:
			r.clientPropagationMessagesReceived++
		}
		r.mu.Unlock()
		r.ingestPropagationMessage(entry.lxmfData, peer, entry.stampValue)
		if peer != nil {
			peer.QueueHandledMessage(entry.transientID)
		}
	}

	if len(validated) != len(messages) {
		if len(remotePropagationHash) == rns.TruncatedHashLength/8 {
			r.mu.Lock()
			r.throttledPeers[string(append([]byte{}, remotePropagationHash...))] = r.now().Add(pnStampThrottle)
			r.mu.Unlock()
		}
		link.Teardown()
	}
}

func (r *Router) storePropagationMessage(destinationHash []byte, payload []byte) []byte {
	return r.storePropagationMessageStamped(destinationHash, payload, 0, nil)
}

func (r *Router) storePropagationMessageStamped(destinationHash []byte, payload []byte, stampValue int, fromPeer *Peer) []byte {
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
		size:            len(destinationHash) + len(payload),
		stampValue:      stampValue,
	}
	if r.propagationEnabled {
		if path, size, err := r.writePropagationMessageFile(transientID, receivedAt, stampValue, destinationHash, payload); err != nil {
			log.Printf("Could not persist propagation message %x: %v", transientID, err)
		} else {
			entry.path = path
			entry.size = size
		}
	}
	r.propagationEntries[string(transientID)] = entry
	r.enqueuePeerDistributionLocked(transientID, fromPeer)

	return transientID
}

func (r *Router) enqueuePeerDistribution(transientID []byte, fromPeer *Peer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enqueuePeerDistributionLocked(transientID, fromPeer)
}

func (r *Router) enqueuePeerDistributionLocked(transientID []byte, fromPeer *Peer) {
	if len(transientID) == 0 {
		return
	}
	entry := peerDistributionEntry{
		transientID: cloneBytes(transientID),
	}
	if fromPeer != nil {
		entry.fromPeerHash = cloneBytes(fromPeer.destinationHash)
	}
	r.peerDistributionQueue = append(r.peerDistributionQueue, entry)
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
	if len(linkID) > 0 {
		r.validatedPeerLinks[string(append([]byte{}, linkID...))] = true
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
		type availableEntry struct {
			transientID []byte
			size        int
		}
		availableMessages := make([]availableEntry, 0)
		for transientID, entry := range r.propagationEntries {
			if !bytes.Equal(entry.destinationHash, remoteDestinationHash) {
				continue
			}
			availableMessages = append(availableMessages, availableEntry{
				transientID: []byte(transientID),
				size:        entry.size,
			})
		}
		sort.Slice(availableMessages, func(i, j int) bool {
			return availableMessages[i].size < availableMessages[j].size
		})
		available := make([]any, 0, len(availableMessages))
		for _, entry := range availableMessages {
			available = append(available, append([]byte{}, entry.transientID...))
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
	switch v := value.(type) {
	case []byte:
		if len(v) == 0 {
			return nil
		}
		return append([]byte{}, v...)
	case string:
		if v == "" {
			return nil
		}
		return []byte(v)
	default:
		return nil
	}
}

func anyToMap(value any) (map[any]any, bool) {
	switch v := value.(type) {
	case map[any]any:
		return v, true
	case map[string]any:
		result := make(map[any]any, len(v))
		for key, entry := range v {
			result[key] = entry
		}
		return result, true
	default:
		return nil, false
	}
}

func messageField(fields map[any]any, key uint8) (any, bool) {
	if len(fields) == 0 {
		return nil, false
	}
	if value, ok := fields[key]; ok {
		return value, true
	}
	for candidate, value := range fields {
		switch typed := candidate.(type) {
		case uint8:
			if typed == key {
				return value, true
			}
		case int:
			if typed == int(key) {
				return value, true
			}
		case int64:
			if typed == int64(key) {
				return value, true
			}
		case uint64:
			if typed == uint64(key) {
				return value, true
			}
		}
	}
	return nil, false
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

	message.State = StateOutbound

	sendMethod := message.DesiredMethod
	if sendMethod == 0 {
		sendMethod = MethodDirect
	}
	message.Method = sendMethod

	destinationHash := message.Destination.Hash
	if message.StampCost == nil {
		if stampCost, ok := r.OutboundStampCost(destinationHash); ok {
			message.StampCost = cloneOptionalInt(&stampCost)
		}
	}
	if r.ticketStore != nil {
		if outboundTicket := r.ticketStore.OutboundTicket(destinationHash, r.now()); len(outboundTicket) > 0 {
			message.OutboundTicket = outboundTicket
		}
	}
	if len(message.OutboundTicket) > 0 && message.DeferStamp {
		message.DeferStamp = false
	}
	if message.StampCost == nil && message.DeferStamp {
		message.DeferStamp = false
	}

	if len(message.Packed) == 0 {
		if _, hasTicket := messageField(message.Fields, FieldTicket); message.IncludeTicket && message.Destination != nil && !hasTicket && r.ticketStore != nil {
			if message.Fields == nil {
				message.Fields = map[any]any{}
			}
			if ticketEntry := r.ticketStore.GenerateInboundTicket(message.Destination.Hash, r.now(), DefaultTicketExpirySeconds); ticketEntry != nil {
				message.Fields[FieldTicket] = []any{ticketEntry.Expires, cloneBytes(ticketEntry.Ticket)}
			}
		}
		if err := message.Pack(); err != nil {
			return err
		}
	}
	message.DetermineTransportEncryption()

	queueDeferred := message.DeferStamp || (message.DesiredMethod == MethodPropagated && message.DeferPropagationStamp)
	r.mu.Lock()
	if queueDeferred {
		r.pendingDeferredStamps[string(message.MessageID)] = message
	} else {
		r.pendingOutbound = append(r.pendingOutbound, message)
	}
	r.mu.Unlock()

	if !queueDeferred {
		r.processOutbound()
	}

	return nil
}

// GetOutboundProgress returns the current progress of an outbound message by its
// LXMF hash, scanning both the active outbound queue and deferred-stamp queue.
func (r *Router) GetOutboundProgress(lxmHash []byte) *float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, message := range r.pendingOutbound {
		if bytes.Equal(message.Hash, lxmHash) {
			progress := message.Progress
			return &progress
		}
	}
	for _, message := range r.pendingDeferredStamps {
		if bytes.Equal(message.Hash, lxmHash) {
			progress := message.Progress
			return &progress
		}
	}

	return nil
}

// GetOutboundLXMStampCost returns the direct-delivery stamp cost for an
// outbound message by its LXMF hash, or nil when a cached outbound ticket is in
// use or when the message is unknown.
func (r *Router) GetOutboundLXMStampCost(lxmHash []byte) *int {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, message := range r.pendingOutbound {
		if bytes.Equal(message.Hash, lxmHash) {
			if len(message.OutboundTicket) > 0 {
				return nil
			}
			return cloneOptionalInt(message.StampCost)
		}
	}
	for _, message := range r.pendingDeferredStamps {
		if bytes.Equal(message.Hash, lxmHash) {
			if len(message.OutboundTicket) > 0 {
				return nil
			}
			return cloneOptionalInt(message.StampCost)
		}
	}

	return nil
}

// GetOutboundLXMPropagationStampCost returns the propagation-node stamp cost
// associated with an outbound message by its LXMF hash.
func (r *Router) GetOutboundLXMPropagationStampCost(lxmHash []byte) *int {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, message := range r.pendingOutbound {
		if bytes.Equal(message.Hash, lxmHash) {
			return cloneOptionalInt(message.PropagationTargetCost)
		}
	}
	for _, message := range r.pendingDeferredStamps {
		if bytes.Equal(message.Hash, lxmHash) {
			return cloneOptionalInt(message.PropagationTargetCost)
		}
	}

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
	r.ProcessDeferredStamps()

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
		case StateDelivered, StateFailed:
			continue
		case StateCancelled, StateRejected:
			if r.outboundPropagationLinkMessage == message {
				r.outboundPropagationLinkMessage = nil
			}
			if message.FailedCallback != nil {
				message.FailedCallback(message)
			}
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

		activePropagationLink := sendMethod == MethodPropagated &&
			r.outboundPropagationLink != nil &&
			r.linkStatus(r.outboundPropagationLink) == rns.LinkActive
		if message.NextDeliveryAttempt > 0 && nowSeconds < message.NextDeliveryAttempt && !activePropagationLink {
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

		destinationHash := message.Destination.Hash

		if sendMethod == MethodPropagated {
			if r.outboundPropagationNode == nil {
				log.Printf("No outbound propagation node for propagated message to %x", destinationHash)
				r.failMessageLocked(message)
				continue
			}
			if link := r.outboundPropagationLink; link != nil {
				r.configureOutboundPropagationLink(link)
				switch r.linkStatus(link) {
				case rns.LinkActive:
					if message.State == StateSending {
						remaining = append(remaining, message)
						continue
					}
					message.setDeliveryDestination(link)
					if err := r.sendMessageLocked(message); err != nil {
						message.DeliveryAttempts++
						message.State = StateOutbound
						message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
						remaining = append(remaining, message)
						continue
					}
					if message.State != StateSending {
						message.State = StateSent
					}
					remaining = append(remaining, message)
					continue
				case rns.LinkClosed:
					r.outboundPropagationLink = nil
					message.setDeliveryDestination(nil)
					message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
					remaining = append(remaining, message)
					continue
				default:
					remaining = append(remaining, message)
					continue
				}
			}

			message.setDeliveryDestination(nil)
			if !r.hasPath(r.outboundPropagationNode) {
				_ = r.requestPath(r.outboundPropagationNode)
				message.DeliveryAttempts++
				message.NextDeliveryAttempt = float64(r.now().Add(pathRequestWait).UnixNano()) / 1e9
				remaining = append(remaining, message)
				continue
			}

			message.DeliveryAttempts++
			message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9

			peerIdentity := r.transport.Recall(r.outboundPropagationNode)
			if peerIdentity == nil {
				log.Printf("Cannot recall identity for propagation node %x", r.outboundPropagationNode)
				r.failMessageLocked(message)
				continue
			}

			dest, err := rns.NewDestination(r.transport, peerIdentity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
			if err != nil {
				log.Printf("Cannot create destination for propagation node: %v", err)
				remaining = append(remaining, message)
				continue
			}

			link, err := r.newLink(r.transport, dest)
			if err != nil {
				log.Printf("Cannot establish link to propagation node: %v", err)
				remaining = append(remaining, message)
				continue
			}

			r.configureOutboundPropagationLink(link)
			r.setLinkEstablishedCallback(link, func(_ *rns.Link) {
				r.ProcessOutbound()
			})
			r.outboundPropagationLink = link
			if err := r.establishLink(link); err != nil {
				if r.outboundPropagationLink == link {
					r.outboundPropagationLink = nil
				}
				log.Printf("Cannot establish link to propagation node: %v", err)
			}
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

// ProcessDeferredStamps mirrors Python's process_deferred_stamps by moving at
// most one deferred message back into the outbound queue once stamp generation
// completes or by failing/cancelling it if that work cannot finish.
func (r *Router) ProcessDeferredStamps() {
	r.mu.Lock()
	if len(r.pendingDeferredStamps) == 0 || r.processingDeferredStamps {
		r.mu.Unlock()
		return
	}
	r.processingDeferredStamps = true
	keys := make([]string, 0, len(r.pendingDeferredStamps))
	for messageID := range r.pendingDeferredStamps {
		keys = append(keys, messageID)
	}
	sort.Strings(keys)
	selectedMessageID := keys[0]
	selected := r.pendingDeferredStamps[selectedMessageID]
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.processingDeferredStamps = false
		r.mu.Unlock()
	}()

	if selected == nil {
		return
	}

	if selected.State == StateCancelled {
		r.mu.Lock()
		delete(r.pendingDeferredStamps, selectedMessageID)
		selected.StampGenerationFailed = true
		failedCallback := selected.FailedCallback
		r.mu.Unlock()
		if failedCallback != nil {
			failedCallback(selected)
		}
		return
	}

	stampGenerationSuccess := !selected.DeferStamp || len(selected.Stamp) > 0
	propagationStampGenerationSuccess := selected.DesiredMethod != MethodPropagated ||
		!selected.DeferPropagationStamp || len(selected.PropagationStamp) > 0

	if !stampGenerationSuccess {
		generatedStamp, err := selected.GetStamp()
		if err != nil || len(generatedStamp) == 0 {
			r.mu.Lock()
			delete(r.pendingDeferredStamps, selectedMessageID)
			selected.StampGenerationFailed = true
			if selected.State == StateCancelled {
				failedCallback := selected.FailedCallback
				r.mu.Unlock()
				if failedCallback != nil {
					failedCallback(selected)
				}
				return
			}
			r.failMessageLocked(selected)
			r.mu.Unlock()
			return
		}

		selected.Stamp = cloneBytes(generatedStamp)
		selected.DeferStamp = false
		selected.resetPackedState(false)
		if err := selected.Pack(); err != nil {
			r.mu.Lock()
			delete(r.pendingDeferredStamps, selectedMessageID)
			selected.StampGenerationFailed = true
			r.failMessageLocked(selected)
			r.mu.Unlock()
			return
		}
		stampGenerationSuccess = true
	}

	if !propagationStampGenerationSuccess {
		targetCost, ok := r.getOutboundPropagationStampCost()
		if !ok {
			r.mu.Lock()
			delete(r.pendingDeferredStamps, selectedMessageID)
			selected.StampGenerationFailed = true
			r.failMessageLocked(selected)
			r.mu.Unlock()
			return
		}

		propagationStamp, err := selected.GetPropagationStamp(targetCost)
		if err != nil || len(propagationStamp) == 0 {
			r.mu.Lock()
			delete(r.pendingDeferredStamps, selectedMessageID)
			selected.StampGenerationFailed = true
			if selected.State == StateCancelled {
				failedCallback := selected.FailedCallback
				r.mu.Unlock()
				if failedCallback != nil {
					failedCallback(selected)
				}
				return
			}
			r.failMessageLocked(selected)
			r.mu.Unlock()
			return
		}

		selected.PropagationStamp = cloneBytes(propagationStamp)
		selected.DeferPropagationStamp = false
		selected.resetPackedState(true)
		if err := selected.Pack(); err != nil {
			r.mu.Lock()
			delete(r.pendingDeferredStamps, selectedMessageID)
			selected.StampGenerationFailed = true
			r.failMessageLocked(selected)
			r.mu.Unlock()
			return
		}
		propagationStampGenerationSuccess = true
	}

	if stampGenerationSuccess && propagationStampGenerationSuccess {
		r.mu.Lock()
		delete(r.pendingDeferredStamps, selectedMessageID)
		r.pendingOutbound = append(r.pendingOutbound, selected)
		r.mu.Unlock()
	}
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

	if message.Method == MethodPropagated && message.deliveryDestination != nil {
		r.outboundPropagationLinkMessage = message
		packet, err := message.asPacket()
		if err != nil {
			return err
		}
		message.PacketRepresentation = packet
		if err := r.sendPacket(packet); err != nil {
			return err
		}
		if packet.Receipt != nil {
			message.State = StateSending
			message.Progress = 0.50
			packet.Receipt.SetDeliveryCallback(func(_ *rns.PacketReceipt) {
				var deliveryCallback func(*Message)
				r.mu.Lock()
				message.State = StateSent
				message.Progress = 1.0
				if r.outboundPropagationLinkMessage == message {
					r.outboundPropagationLinkMessage = nil
				}
				deliveryCallback = message.DeliveryCallback
				r.mu.Unlock()
				if deliveryCallback != nil {
					deliveryCallback(message)
				}
			})
			packet.Receipt.SetTimeoutCallback(func(_ *rns.PacketReceipt) {
				r.mu.Lock()
				defer r.mu.Unlock()
				if message.State != StateCancelled && message.State != StateSent {
					message.State = StateOutbound
					message.Progress = 0.0
					message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
				}
				if r.outboundPropagationLinkMessage == message {
					r.outboundPropagationLinkMessage = nil
				}
			})
		}
		return nil
	}

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
			r.markTicketDeliveryLocked(message)
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
	if message.Method == MethodPropagated {
		if len(message.PropagationPacked) == 0 {
			if err := message.packPropagated(); err != nil {
				return err
			}
		}
		packetLength = len(message.PropagationPacked)
		if message.Representation != RepresentationUnknown {
			representation = message.Representation
		}
	} else if message.Method == MethodOpportunistic || message.Method == MethodDirect {
		packetLength -= DestinationLength
	}
	if representation == RepresentationPacket && packetLength > rns.MDU {
		representation = RepresentationResource
	}

	if representation == RepresentationResource {
		message.Representation = RepresentationResource
		return r.sendResource(message)
	}

	return r.sendMessagePacketLocked(message)
}

// CancelOutbound cancels a deferred or queued outbound message and mirrors
// Python's cancel_outbound() state transition behavior.
func (r *Router) CancelOutbound(messageID []byte, cancelState int) {
	if cancelState == 0 {
		cancelState = StateCancelled
	}

	processOutbound := false

	r.mu.Lock()
	if deferred := r.pendingDeferredStamps[string(messageID)]; deferred != nil {
		deferred.State = cancelState
	}
	for _, message := range r.pendingOutbound {
		if !bytes.Equal(message.MessageID, messageID) {
			continue
		}
		message.State = cancelState
		if message.Representation == RepresentationResource && message.ResourceRepresentation != nil {
			message.ResourceRepresentation.Cancel()
		}
		processOutbound = true
		break
	}
	r.mu.Unlock()

	if processOutbound {
		r.processOutbound()
	}
}

func (r *Router) sendMessageResourceLocked(message *Message) error {
	message.Representation = RepresentationResource

	if message.Method == MethodPropagated && message.deliveryDestination != nil {
		r.outboundPropagationLinkMessage = message
		resource, err := message.asResource()
		if err != nil {
			return err
		}
		message.ResourceRepresentation = resource
		message.State = StateSending
		message.Progress = 0.10
		resource.SetProgressCallback(func(resource *rns.Resource) {
			r.mu.Lock()
			defer r.mu.Unlock()
			message.Progress = 0.10 + (resource.GetProgress() * 0.90)
		})
		resource.SetCallback(func(resource *rns.Resource) {
			var deliveryCallback func(*Message)
			r.mu.Lock()
			if resource != nil && resource.Status() == rns.ResourceStatusComplete {
				message.State = StateSent
				message.Progress = 1.0
				if r.outboundPropagationLinkMessage == message {
					r.outboundPropagationLinkMessage = nil
				}
				deliveryCallback = message.DeliveryCallback
			} else if message.State != StateCancelled {
				message.State = StateOutbound
				message.Progress = 0.0
				message.NextDeliveryAttempt = float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
				if r.outboundPropagationLinkMessage == message {
					r.outboundPropagationLinkMessage = nil
				}
			}
			r.mu.Unlock()
			if deliveryCallback != nil {
				deliveryCallback(message)
			}
		})
		if err := resource.Advertise(); err != nil {
			return err
		}
		return nil
	}

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
				r.markTicketDeliveryLocked(message)
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
	r.handleInboundMessage(message)
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
	r.handleInboundMessage(message)
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
	if r.propagationEnabled {
		r.mu.Unlock()
		return
	}
	if err := os.MkdirAll(r.propagationMessageStorePath(), 0o755); err != nil {
		r.mu.Unlock()
		log.Printf("Could not create LXMF propagation store: %v", err)
		return
	}
	r.reindexPropagationStoreLocked()
	r.propagationEnabled = true
	r.cleanMessageStoreLocked()
	r.mu.Unlock()

	if err := r.LoadPeers(); err != nil {
		log.Printf("Could not load propagation peers from storage: %v", err)
	}
	if err := r.LoadNodeStats(); err != nil {
		log.Printf("Could not load propagation node stats from storage: %v", err)
	}
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

func (r *Router) peersPath() string {
	return filepath.Join(r.storagePath, "peers")
}

func (r *Router) nodeStatsPath() string {
	return filepath.Join(r.storagePath, "node_stats")
}

func (r *Router) outboundStampCostsPath() string {
	return filepath.Join(r.storagePath, "outbound_stamp_costs")
}

func (r *Router) availableTicketsPath() string {
	return filepath.Join(r.storagePath, "available_tickets")
}

func (r *Router) localDeliveriesPath() string {
	return filepath.Join(r.storagePath, "local_deliveries")
}

func (r *Router) locallyProcessedPath() string {
	return filepath.Join(r.storagePath, "locally_processed")
}

// FlushQueues merges queued peer message bookkeeping into the in-memory
// propagation-entry state before persistence.
func (r *Router) FlushQueues() {
	r.flushPeerDistributionQueue()

	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	r.mu.Unlock()

	for _, peer := range peers {
		peer.ProcessQueues()
	}
}

func (r *Router) flushPeerDistributionQueue() {
	r.mu.Lock()
	entries := make([]peerDistributionEntry, len(r.peerDistributionQueue))
	copy(entries, r.peerDistributionQueue)
	r.peerDistributionQueue = nil
	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	r.mu.Unlock()

	for _, peer := range peers {
		if peer == nil {
			continue
		}
		for _, entry := range entries {
			if len(entry.transientID) == 0 {
				continue
			}
			if len(entry.fromPeerHash) > 0 && bytes.Equal(peer.destinationHash, entry.fromPeerHash) {
				continue
			}
			peer.QueueUnhandledMessage(entry.transientID)
		}
	}
}

// SavePeers persists propagation peer synchronisation state using the Python
// msgpack list-of-bytes layout.
func (r *Router) SavePeers() error {
	r.mu.Lock()
	enabled := r.propagationEnabled
	peers := make([]*Peer, 0, len(r.peers))
	for _, peer := range r.peers {
		peers = append(peers, peer)
	}
	r.mu.Unlock()

	if !enabled {
		return nil
	}
	if err := os.MkdirAll(r.storagePath, 0o755); err != nil {
		return err
	}

	serialised := make([]any, 0, len(peers))
	for _, peer := range peers {
		peerBytes, err := peer.ToBytes()
		if err != nil {
			return err
		}
		serialised = append(serialised, peerBytes)
	}

	packed, err := msgpack.Pack(serialised)
	if err != nil {
		return err
	}
	return os.WriteFile(r.peersPath(), packed, 0o644)
}

// LoadPeers restores persisted propagation peer synchronisation state.
func (r *Router) LoadPeers() error {
	data, err := os.ReadFile(r.peersPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	entries, err := decodeAnyList(data)
	if err != nil {
		return err
	}

	loaded := make(map[string]*Peer, len(entries))
	for _, entry := range entries {
		peerBytes := anyToBytes(entry)
		if len(peerBytes) == 0 {
			continue
		}
		peer, err := r.PeerFromBytes(peerBytes)
		if err != nil {
			return err
		}
		if peer.identity == nil {
			continue
		}
		loaded[string(peer.destinationHash)] = peer
	}

	r.mu.Lock()
	for destinationHash, peer := range loaded {
		r.peers[destinationHash] = peer
	}
	r.mu.Unlock()
	return nil
}

// SaveNodeStats persists local propagation-node accounting.
func (r *Router) SaveNodeStats() error {
	r.mu.Lock()
	nodeStats := map[string]any{
		"client_propagation_messages_received": r.clientPropagationMessagesReceived,
		"client_propagation_messages_served":   r.clientPropagationMessagesServed,
		"unpeered_propagation_incoming":        r.unpeeredPropagationIncoming,
		"unpeered_propagation_rx_bytes":        r.unpeeredPropagationRXBytes,
	}
	r.mu.Unlock()

	if err := os.MkdirAll(r.storagePath, 0o755); err != nil {
		return err
	}
	packed, err := msgpack.Pack(nodeStats)
	if err != nil {
		return err
	}
	return os.WriteFile(r.nodeStatsPath(), packed, 0o644)
}

// SaveAvailableTickets persists inbound/outbound delivery ticket state using the
// Python available_tickets dictionary shape.
func (r *Router) SaveAvailableTickets() error {
	if r.ticketStore == nil {
		return nil
	}

	r.ticketStore.mu.RLock()
	lastDeliveries := make(map[string]any, len(r.ticketStore.lastDeliveries))
	for destinationHash, deliveredAt := range r.ticketStore.lastDeliveries {
		lastDeliveries[destinationHash] = deliveredAt
	}
	outbound := make(map[string]any, len(r.ticketStore.outbound))
	for destinationHash, entry := range r.ticketStore.outbound {
		outbound[destinationHash] = []any{entry.Expires, cloneBytes(entry.Ticket)}
	}
	inbound := make(map[string]any, len(r.ticketStore.inbound))
	for destinationHash, ticketEntries := range r.ticketStore.inbound {
		destinationTickets := make(map[string]any, len(ticketEntries))
		for ticket, entry := range ticketEntries {
			destinationTickets[ticket] = []any{entry.Expires}
		}
		inbound[destinationHash] = destinationTickets
	}
	r.ticketStore.mu.RUnlock()

	payload := map[string]any{
		"outbound":        outbound,
		"inbound":         inbound,
		"last_deliveries": lastDeliveries,
	}

	if err := os.MkdirAll(r.storagePath, 0o755); err != nil {
		return err
	}
	packed, err := msgpack.Pack(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(r.availableTicketsPath(), packed, 0o644)
}

// LoadAvailableTickets restores available ticket state and drops expired
// outbound and stale inbound entries.
func (r *Router) LoadAvailableTickets() error {
	data, err := os.ReadFile(r.availableTicketsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return err
	}

	root, ok := anyToMap(unpacked)
	if !ok {
		return fmt.Errorf("invalid available_tickets payload type %T", unpacked)
	}

	nowSeconds := float64(r.now().UnixNano()) / 1e9
	store := NewTicketStore()

	if lastDeliveries, ok := anyToMap(root["last_deliveries"]); ok {
		for destinationHashValue, deliveredAtValue := range lastDeliveries {
			destinationHash := anyToBytes(destinationHashValue)
			if len(destinationHash) == 0 {
				continue
			}
			deliveredAt, err := anyToFloat64(deliveredAtValue)
			if err != nil {
				continue
			}
			store.lastDeliveries[string(destinationHash)] = deliveredAt
		}
	}

	if outbound, ok := anyToMap(root["outbound"]); ok {
		for destinationHashValue, entryValue := range outbound {
			destinationHash := anyToBytes(destinationHashValue)
			if len(destinationHash) == 0 {
				continue
			}
			items, ok := entryValue.([]any)
			if !ok || len(items) < 2 {
				continue
			}
			expires, err := anyToFloat64(items[0])
			if err != nil || expires <= nowSeconds {
				continue
			}
			ticket := anyToBytes(items[1])
			if len(ticket) != TicketLength {
				continue
			}
			store.outbound[string(destinationHash)] = TicketEntry{
				Expires: expires,
				Ticket:  ticket,
			}
		}
	}

	if inbound, ok := anyToMap(root["inbound"]); ok {
		for destinationHashValue, destinationTicketsValue := range inbound {
			destinationHash := anyToBytes(destinationHashValue)
			if len(destinationHash) == 0 {
				continue
			}
			destinationTickets, ok := anyToMap(destinationTicketsValue)
			if !ok {
				continue
			}
			for ticketValue, entryValue := range destinationTickets {
				ticket := anyToBytes(ticketValue)
				if len(ticket) != TicketLength {
					continue
				}
				items, ok := entryValue.([]any)
				if !ok || len(items) == 0 {
					continue
				}
				expires, err := anyToFloat64(items[0])
				if err != nil || nowSeconds > expires+DefaultTicketGraceSeconds {
					continue
				}
				destinationKey := string(destinationHash)
				if store.inbound[destinationKey] == nil {
					store.inbound[destinationKey] = map[string]TicketEntry{}
				}
				store.inbound[destinationKey][string(ticket)] = TicketEntry{
					Expires: expires,
					Ticket:  ticket,
				}
			}
		}
	}

	r.ticketStore = store
	return nil
}

// SaveLocalTransientIDCaches persists the Python local_deliveries and
// locally_processed dictionaries used for duplicate suppression.
func (r *Router) SaveLocalTransientIDCaches() error {
	r.mu.Lock()
	r.cleanTransientIDCachesLocked()
	delivered := make(map[string]any, len(r.locallyDeliveredIDs))
	for transientID, deliveredAt := range r.locallyDeliveredIDs {
		delivered[transientID] = peerTime(deliveredAt)
	}
	processed := make(map[string]any, len(r.locallyProcessedIDs))
	for transientID, processedAt := range r.locallyProcessedIDs {
		processed[transientID] = peerTime(processedAt)
	}
	r.mu.Unlock()

	if err := os.MkdirAll(r.storagePath, 0o755); err != nil {
		return err
	}
	if len(delivered) > 0 {
		packed, err := msgpack.Pack(delivered)
		if err != nil {
			return err
		}
		if err := os.WriteFile(r.localDeliveriesPath(), packed, 0o644); err != nil {
			return err
		}
	}
	if len(processed) > 0 {
		packed, err := msgpack.Pack(processed)
		if err != nil {
			return err
		}
		if err := os.WriteFile(r.locallyProcessedPath(), packed, 0o644); err != nil {
			return err
		}
	}

	return nil
}

// LoadLocalTransientIDCaches restores and cleans the duplicate-suppression
// caches used for direct delivery and propagation processing.
func (r *Router) LoadLocalTransientIDCaches() error {
	delivered, err := r.loadTransientIDCache(r.localDeliveriesPath())
	if err != nil {
		return err
	}
	processed, err := r.loadTransientIDCache(r.locallyProcessedPath())
	if err != nil {
		return err
	}

	r.mu.Lock()
	r.locallyDeliveredIDs = delivered
	r.locallyProcessedIDs = processed
	r.cleanTransientIDCachesLocked()
	r.mu.Unlock()
	return nil
}

func (r *Router) loadTransientIDCache(path string) (map[string]time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]time.Time{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]time.Time{}, nil
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return nil, err
	}
	cache, ok := anyToMap(unpacked)
	if !ok {
		return map[string]time.Time{}, nil
	}

	loaded := make(map[string]time.Time, len(cache))
	for transientIDValue, timestampValue := range cache {
		transientID := anyToBytes(transientIDValue)
		if len(transientID) == 0 {
			continue
		}
		timestampSeconds, err := anyToFloat64(timestampValue)
		if err != nil {
			continue
		}
		loaded[string(transientID)] = timeFromPeerValue(timestampSeconds)
	}
	return loaded, nil
}

func (r *Router) cleanTransientIDCachesLocked() {
	now := r.now()
	for transientID, deliveredAt := range r.locallyDeliveredIDs {
		if now.After(deliveredAt.Add(transientIDCacheExpiry)) {
			delete(r.locallyDeliveredIDs, transientID)
		}
	}
	for transientID, processedAt := range r.locallyProcessedIDs {
		if now.After(processedAt.Add(transientIDCacheExpiry)) {
			delete(r.locallyProcessedIDs, transientID)
		}
	}
}

func (r *Router) hasDeliveredTransientIDLocked(transientID []byte) bool {
	if len(transientID) == 0 {
		return false
	}
	_, ok := r.locallyDeliveredIDs[string(transientID)]
	return ok
}

func (r *Router) hasProcessedTransientIDLocked(transientID []byte) bool {
	if len(transientID) == 0 {
		return false
	}
	_, ok := r.locallyProcessedIDs[string(transientID)]
	return ok
}

func responseErrorCode(response any) (int64, bool) {
	switch value := response.(type) {
	case int:
		return int64(value), true
	case int8:
		return int64(value), true
	case int16:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case uint:
		return int64(value), true
	case uint8:
		return int64(value), true
	case uint16:
		return int64(value), true
	case uint32:
		return int64(value), true
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}

func transientIDsFromResponse(response any) ([][]byte, bool) {
	switch values := response.(type) {
	case [][]byte:
		result := make([][]byte, 0, len(values))
		for _, value := range values {
			result = append(result, append([]byte{}, value...))
		}
		return result, true
	case []any:
		result := make([][]byte, 0, len(values))
		for _, value := range values {
			entry, ok := value.([]byte)
			if !ok {
				return nil, false
			}
			result = append(result, append([]byte{}, entry...))
		}
		return result, true
	default:
		return nil, false
	}
}

func validatePropagationMessages(messages [][]byte, targetCost int) []validatedPropagationMessage {
	validated := make([]validatedPropagationMessage, 0, len(messages))
	for _, message := range messages {
		entry, ok := validatePropagationMessage(message, targetCost)
		if !ok {
			continue
		}
		validated = append(validated, entry)
	}
	return validated
}

func validatePropagationMessage(transientData []byte, targetCost int) (validatedPropagationMessage, bool) {
	if len(transientData) <= (2*DestinationLength)+SignatureLength+StampSize {
		return validatedPropagationMessage{}, false
	}

	lxmfData := cloneBytes(transientData[:len(transientData)-StampSize])
	stampData := transientData[len(transientData)-StampSize:]
	transientID := rns.FullHash(lxmfData)
	workblock, err := StampWorkblock(transientID, WorkblockExpandRoundsPN)
	if err != nil || !StampValid(stampData, targetCost, workblock) {
		return validatedPropagationMessage{}, false
	}

	return validatedPropagationMessage{
		transientID: transientID,
		lxmfData:    lxmfData,
		stampValue:  StampValue(workblock, stampData),
	}, true
}

func (r *Router) ingestPropagationMessage(lxmfData []byte, fromPeer *Peer, stampValue int) bool {
	if len(lxmfData) < DestinationLength {
		return false
	}

	transientID := rns.FullHash(lxmfData)
	destinationHash := append([]byte{}, lxmfData[:DestinationLength]...)

	r.mu.Lock()
	if _, ok := r.propagationEntries[string(transientID)]; ok || r.hasProcessedTransientIDLocked(transientID) {
		r.mu.Unlock()
		return false
	}
	r.locallyProcessedIDs[string(append([]byte{}, transientID...))] = r.now()
	_, isLocalDelivery := r.deliveryDestinations[string(destinationHash)]
	propagationEnabled := r.propagationEnabled
	r.mu.Unlock()

	if isLocalDelivery {
		if r.handlePropagatedInbound(lxmfData) {
			return true
		}
		return true
	}
	if !propagationEnabled {
		return false
	}

	storedID := r.storePropagationMessageStamped(destinationHash, lxmfData, stampValue, fromPeer)
	return len(storedID) > 0
}

func (r *Router) handleInboundMessage(message *Message) {
	if message == nil {
		return
	}

	r.mu.Lock()
	if r.hasDeliveredTransientIDLocked(message.Hash) {
		r.mu.Unlock()
		return
	}
	r.locallyDeliveredIDs[string(append([]byte{}, message.Hash...))] = r.now()
	if r.ticketStore != nil && message.SignatureValidated {
		if ticketEntry := outboundTicketFieldEntry(message.Fields, r.now()); ticketEntry != nil {
			r.ticketStore.RememberOutboundTicket(message.SourceHash, *ticketEntry)
		}
	}
	callback := r.deliveryCallback
	r.mu.Unlock()

	if callback != nil {
		callback(message)
	}
}

func (r *Router) handlePropagatedInbound(payload []byte) bool {
	if len(payload) < DestinationLength {
		return false
	}

	transientID := rns.FullHash(payload)
	destinationHash := append([]byte{}, payload[:DestinationLength]...)

	r.mu.Lock()
	if _, ok := r.propagationEntries[string(transientID)]; ok || r.hasProcessedTransientIDLocked(transientID) {
		r.mu.Unlock()
		return true
	}
	r.locallyProcessedIDs[string(append([]byte{}, transientID...))] = r.now()
	_, isLocalDelivery := r.deliveryDestinations[string(destinationHash)]
	r.mu.Unlock()

	if !isLocalDelivery {
		return false
	}

	message, err := UnpackMessageFromBytes(r.transport, payload, MethodPropagated)
	if err != nil {
		return false
	}

	r.handleInboundMessage(message)
	r.mu.Lock()
	r.locallyDeliveredIDs[string(append([]byte{}, transientID...))] = r.now()
	r.mu.Unlock()
	return false
}

func (r *Router) markTicketDeliveryLocked(message *Message) {
	if r.ticketStore == nil || message == nil || !message.IncludeTicket || message.Destination == nil {
		return
	}
	if outboundTicketFieldEntry(message.Fields, r.now()) == nil {
		return
	}
	r.ticketStore.MarkDelivery(message.Destination.Hash, r.now())
}

func outboundTicketFieldEntry(fields map[any]any, now time.Time) *TicketEntry {
	value, ok := messageField(fields, FieldTicket)
	if !ok {
		return nil
	}
	items, ok := value.([]any)
	if !ok || len(items) < 2 {
		return nil
	}
	expires, err := anyToFloat64(items[0])
	if err != nil || expires <= float64(now.UnixNano())/1e9 {
		return nil
	}
	ticket := anyToBytes(items[1])
	if len(ticket) != TicketLength {
		return nil
	}
	entry := TicketEntry{
		Expires: expires,
		Ticket:  cloneBytes(ticket),
	}
	return &entry
}

// SaveOutboundStampCosts persists cached outbound delivery stamp costs using the
// Python msgpack dictionary layout.
func (r *Router) SaveOutboundStampCosts() error {
	r.mu.Lock()
	payload := make(map[string]any, len(r.outboundStampCosts))
	for destinationHash, entry := range r.outboundStampCosts {
		payload[destinationHash] = []any{peerTime(entry.updatedAt), entry.stampCost}
	}
	r.mu.Unlock()

	if err := os.MkdirAll(r.storagePath, 0o755); err != nil {
		return err
	}
	packed, err := msgpack.Pack(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(r.outboundStampCostsPath(), packed, 0o644)
}

// LoadOutboundStampCosts restores cached outbound delivery stamp costs from
// storage and drops expired entries.
func (r *Router) LoadOutboundStampCosts() error {
	data, err := os.ReadFile(r.outboundStampCostsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return err
	}
	dictionary := map[any]any{}
	switch v := unpacked.(type) {
	case map[any]any:
		dictionary = v
	case map[string]any:
		for key, value := range v {
			dictionary[key] = value
		}
	default:
		return fmt.Errorf("invalid outbound stamp cost payload type %T", unpacked)
	}

	now := r.now()
	loaded := make(map[string]outboundStampCostEntry, len(dictionary))
	for destinationHashValue, entryValue := range dictionary {
		destinationHash := anyToBytes(destinationHashValue)
		if len(destinationHash) == 0 {
			continue
		}
		items, ok := entryValue.([]any)
		if !ok || len(items) < 2 {
			continue
		}
		updatedAtSeconds, err := anyToFloat64(items[0])
		if err != nil {
			continue
		}
		stampCost, err := anyToInt(items[1])
		if err != nil || stampCost <= 0 {
			continue
		}
		updatedAt := time.Unix(0, 0).Add(time.Duration(updatedAtSeconds * float64(time.Second)))
		if now.Sub(updatedAt) > stampCostExpiry {
			continue
		}
		loaded[string(destinationHash)] = outboundStampCostEntry{
			updatedAt: updatedAt,
			stampCost: stampCost,
		}
	}

	r.mu.Lock()
	r.outboundStampCosts = loaded
	r.mu.Unlock()
	return nil
}

// LoadNodeStats restores local propagation-node accounting from storage.
func (r *Router) LoadNodeStats() error {
	data, err := os.ReadFile(r.nodeStatsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return err
	}
	nodeStats, ok := unpacked.(map[any]any)
	if !ok {
		return fmt.Errorf("invalid node stats payload type %T", unpacked)
	}

	mustInt := func(value any) int {
		n, err := anyToInt(value)
		if err != nil {
			return 0
		}
		return n
	}

	r.mu.Lock()
	r.clientPropagationMessagesReceived = mustInt(nodeStats["client_propagation_messages_received"])
	r.clientPropagationMessagesServed = mustInt(nodeStats["client_propagation_messages_served"])
	r.unpeeredPropagationIncoming = mustInt(nodeStats["unpeered_propagation_incoming"])
	r.unpeeredPropagationRXBytes = mustInt(nodeStats["unpeered_propagation_rx_bytes"])
	r.mu.Unlock()
	return nil
}

// Close flushes in-memory propagation state to disk.
func (r *Router) Close() error {
	r.FlushQueues()

	var closeErr error
	if err := r.SaveAvailableTickets(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := r.SaveLocalTransientIDCaches(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := r.SaveOutboundStampCosts(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := r.SavePeers(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if err := r.SaveNodeStats(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	return closeErr
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

func (r *Router) messageStorageSize() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.messageStorageSizeLocked()
}

func (r *Router) messageStorageSizeLocked() float64 {
	if !r.propagationEnabled {
		return 0
	}
	var total int
	for _, entry := range r.propagationEntries {
		if entry != nil {
			total += entry.size
		}
	}
	return float64(total)
}

func (r *Router) getWeightLocked(transientID string) float64 {
	entry := r.propagationEntries[transientID]
	if entry == nil {
		return 0
	}

	ageWeight := (r.now().Sub(entry.receivedAt).Seconds() / 60 / 60 / 24 / 4)
	if ageWeight < 1 {
		ageWeight = 1
	}

	priorityWeight := 1.0
	if _, ok := r.prioritisedList[string(entry.destinationHash)]; ok {
		priorityWeight = 0.1
	}

	return priorityWeight * ageWeight * float64(entry.size)
}

func (r *Router) removePropagationEntryLocked(transientID string) {
	entry, ok := r.propagationEntries[transientID]
	if !ok {
		return
	}
	delete(r.propagationEntries, transientID)
	if entry.path != "" {
		if err := os.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("Could not remove persisted propagation message %x: %v", transientID, err)
		}
	}
}

func (r *Router) cleanMessageStore() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanMessageStoreLocked()
}

func (r *Router) cleanMessageStoreLocked() {
	now := r.now()
	removed := make([]string, 0)
	for transientID, entry := range r.propagationEntries {
		if entry == nil {
			continue
		}
		if entry.path == "" {
			continue
		}
		filename := filepath.Base(entry.path)
		parsedID, timestamp, stampValue, ok := parsePropagationStoreFilename(filename)
		if !ok || !bytes.Equal(parsedID, []byte(transientID)) || peerTime(timestamp) != peerTime(entry.receivedAt) || stampValue != entry.stampValue {
			removed = append(removed, transientID)
			continue
		}
		if now.After(timestamp.Add(messageExpiry)) {
			removed = append(removed, transientID)
		}
	}
	for _, transientID := range removed {
		r.removePropagationEntryLocked(transientID)
	}

	if r.messageStorageLimit <= 0 {
		return
	}
	messageStorageSize := r.messageStorageSizeLocked()
	if messageStorageSize <= r.messageStorageLimit {
		return
	}

	bytesNeeded := messageStorageSize - r.messageStorageLimit
	type weightedEntry struct {
		transientID string
		weight      float64
	}
	weightedEntries := make([]weightedEntry, 0, len(r.propagationEntries))
	for transientID := range r.propagationEntries {
		weightedEntries = append(weightedEntries, weightedEntry{
			transientID: transientID,
			weight:      r.getWeightLocked(transientID),
		})
	}
	sort.Slice(weightedEntries, func(i, j int) bool {
		return weightedEntries[i].weight > weightedEntries[j].weight
	})

	var bytesCleaned float64
	for _, entry := range weightedEntries {
		if bytesCleaned >= bytesNeeded {
			break
		}
		size := float64(r.propagationEntries[entry.transientID].size)
		r.removePropagationEntryLocked(entry.transientID)
		bytesCleaned += size
	}
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
	metadata := map[any]any{}
	if r.name != "" {
		metadata[PNMetaName] = []byte(r.name)
	}

	nodeState := r.propagationEnabled && !r.fromStaticOnly
	stampCost := []any{
		r.propagationCost,
		r.propagationCostFlexibility,
		r.peeringCost,
	}
	announceData := []any{
		false,
		int(r.now().Unix()),
		nodeState,
		r.propagationPerTransferLimit,
		r.propagationPerSyncLimit,
		stampCost,
		metadata,
	}

	packed, err := msgpack.Pack(announceData)
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
	if r.outboundPropagationNode == nil {
		r.mu.Unlock()
		log.Printf("Cannot request LXMF propagation node sync, no default propagation node configured")
		return
	}
	outboundNode := append([]byte{}, r.outboundPropagationNode...)
	activeLink := r.outboundPropagationLink
	r.propagationTransferProgress = 0.0
	maxMessages := 0
	if limit != nil {
		maxMessages = *limit
	}
	r.propagationTransferMaxMessages = maxMessages
	identity := r.identity
	r.mu.Unlock()

	if activeLink != nil && r.linkStatus(activeLink) == rns.LinkActive {
		r.configureOutboundPropagationLink(activeLink)
		r.mu.Lock()
		r.wantsDownloadOnPathAvailableFrom = nil
		r.wantsDownloadOnPathAvailableTo = nil
		r.wantsDownloadOnPathAvailableAt = time.Time{}
		r.propagationTransferState = PRLinkEstablished
		r.mu.Unlock()
		log.Printf("Requesting message list from propagation node")
		if err := r.identifyLink(activeLink, identity); err != nil {
			log.Printf("Could not identify to propagation node: %v", err)
			r.mu.Lock()
			r.propagationTransferState = PRFailed
			r.mu.Unlock()
			return
		}
		if _, err := r.requestLink(activeLink, messageGetPath, []any{nil, nil}, r.messageListResponse, r.messageGetFailed, nil, 0); err != nil {
			log.Printf("Could not request message list from propagation node: %v", err)
			r.mu.Lock()
			r.propagationTransferState = PRFailed
			r.mu.Unlock()
			return
		}
		r.mu.Lock()
		r.propagationTransferState = PRRequestSent
		r.mu.Unlock()
		return
	}
	if activeLink != nil {
		log.Printf("Waiting for propagation node link to become active")
		return
	}

	if r.hasPath != nil && r.hasPath(outboundNode) {
		r.mu.Lock()
		r.wantsDownloadOnPathAvailableFrom = nil
		r.wantsDownloadOnPathAvailableTo = nil
		r.wantsDownloadOnPathAvailableAt = time.Time{}
		r.propagationTransferState = PRLinkEstablishing
		r.mu.Unlock()
		log.Printf("Establishing link to %x for message download (limit=%v)", outboundNode, maxMessages)

		peerIdentity := r.transport.Recall(outboundNode)
		if peerIdentity == nil {
			log.Printf("Cannot recall identity for propagation node %x", outboundNode)
			r.mu.Lock()
			r.propagationTransferState = PRFailed
			r.mu.Unlock()
			return
		}

		dest, err := rns.NewDestination(r.transport, peerIdentity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")
		if err != nil {
			log.Printf("Cannot create destination for propagation node: %v", err)
			r.mu.Lock()
			r.propagationTransferState = PRFailed
			r.mu.Unlock()
			return
		}

		link, err := r.newLink(r.transport, dest)
		if err != nil {
			log.Printf("Cannot establish link to propagation node: %v", err)
			r.mu.Lock()
			r.propagationTransferState = PRLinkFailed
			r.mu.Unlock()
			return
		}

		r.configureOutboundPropagationLink(link)
		r.setLinkEstablishedCallback(link, func(_ *rns.Link) {
			var nextLimit *int
			r.mu.Lock()
			maxMessages := r.propagationTransferMaxMessages
			r.mu.Unlock()
			if maxMessages != 0 {
				nextLimit = &maxMessages
			}
			r.RequestMessagesFromPropagationNode(nextLimit)
		})
		r.mu.Lock()
		r.outboundPropagationLink = link
		r.mu.Unlock()
		if err := r.establishLink(link); err != nil {
			log.Printf("Cannot establish link to propagation node: %v", err)
			r.mu.Lock()
			if r.outboundPropagationLink == link {
				r.outboundPropagationLink = nil
			}
			r.propagationTransferState = PRLinkFailed
			r.mu.Unlock()
			return
		}
	} else {
		log.Printf("No path known for message download from propagation node %x, requesting path...", outboundNode)
		if r.requestPath != nil {
			if err := r.requestPath(outboundNode); err != nil {
				log.Printf("Path request failed: %v", err)
				r.mu.Lock()
				r.propagationTransferState = PRNoPath
				r.mu.Unlock()
				return
			}
		}
		r.mu.Lock()
		r.wantsDownloadOnPathAvailableFrom = append([]byte{}, outboundNode...)
		r.wantsDownloadOnPathAvailableTo = identity
		r.wantsDownloadOnPathAvailableAt = r.now().Add(propagationPathTimeout)
		r.propagationTransferState = PRPathRequested
		r.mu.Unlock()
		r.startRequestMessagesPathJob()
	}
}

func (r *Router) requestMessagesPathJob() {
	r.mu.Lock()
	from := append([]byte{}, r.wantsDownloadOnPathAvailableFrom...)
	deadline := r.wantsDownloadOnPathAvailableAt
	maxMessages := r.propagationTransferMaxMessages
	r.mu.Unlock()

	for len(from) > 0 && (deadline.IsZero() || r.now().Before(deadline)) {
		if r.hasPath != nil && r.hasPath(from) {
			var limit *int
			if maxMessages != 0 {
				limit = &maxMessages
			}
			r.RequestMessagesFromPropagationNode(limit)
			return
		}
		r.pathWaitSleep(100 * time.Millisecond)
	}

	log.Printf("Propagation node path request timed out")
	failureState := PRNoPath
	r.acknowledgeSyncCompletion(false, &failureState)
}

func (r *Router) configureOutboundPropagationLink(link *rns.Link) {
	if link == nil {
		return
	}
	link.SetPacketCallback(r.propagationTransferSignallingPacket)
	link.SetLinkClosedCallback(func(closed *rns.Link) {
		r.handleOutboundPropagationLinkClosed(closed)
	})
}

func (r *Router) handleOutboundPropagationLinkClosed(link *rns.Link) {
	r.mu.Lock()
	if r.outboundPropagationLink == nil || (link != nil && r.outboundPropagationLink != link) {
		r.mu.Unlock()
		return
	}
	state := r.propagationTransferState
	retryAt := float64(r.now().Add(deliveryRetryWait).UnixNano()) / 1e9
	for _, message := range r.pendingOutbound {
		if message.Method == MethodPropagated && message.State == StateSending {
			message.State = StateOutbound
			message.Progress = 0.0
			message.NextDeliveryAttempt = retryAt
			message.setDeliveryDestination(nil)
		}
	}
	r.outboundPropagationLink = nil
	r.outboundPropagationLinkMessage = nil
	r.mu.Unlock()

	switch {
	case state == PRComplete:
		r.acknowledgeSyncCompletion(false, nil)
	case state < PRLinkEstablished:
		failureState := PRLinkFailed
		r.acknowledgeSyncCompletion(false, &failureState)
	case state >= PRLinkEstablished && state < PRComplete:
		failureState := PRTransferFailed
		r.acknowledgeSyncCompletion(false, &failureState)
	default:
		r.acknowledgeSyncCompletion(false, nil)
	}
}

func (r *Router) getOutboundPropagationStampCost() (int, bool) {
	if cost, ok := r.cachedOutboundPropagationStampCost(); ok {
		return cost, true
	}
	if len(r.outboundPropagationNode) == 0 {
		return 0, false
	}

	log.Printf("Could not retrieve cached propagation node config. Requesting path to propagation node to get target propagation cost...")
	_ = r.requestPath(r.outboundPropagationNode)

	const waitStep = 500 * time.Millisecond
	waitSteps := int(pathRequestWait / waitStep)
	if waitSteps < 1 {
		waitSteps = 1
	}
	for i := 0; i < waitSteps; i++ {
		if cost, ok := r.cachedOutboundPropagationStampCost(); ok {
			return cost, true
		}
		r.pathWaitSleep(waitStep)
	}

	if cost, ok := r.cachedOutboundPropagationStampCost(); ok {
		return cost, true
	}

	log.Printf("Propagation node stamp cost still unavailable after path request")
	return 0, false
}

func (r *Router) cachedOutboundPropagationStampCost() (int, bool) {
	if len(r.outboundPropagationNode) == 0 {
		return 0, false
	}
	identity := r.transport.Recall(r.outboundPropagationNode)
	if identity == nil || len(identity.AppData) == 0 {
		return 0, false
	}
	announceData, ok := decodePropagationAnnounceData(identity.AppData)
	if !ok || announceData.propagationStampCost <= 0 {
		return 0, false
	}
	return announceData.propagationStampCost, true
}

func (r *Router) messageListResponse(receipt *rns.RequestReceipt) {
	if receipt == nil {
		return
	}
	if code, ok := responseErrorCode(receipt.Response); ok {
		switch code {
		case peerErrorNoIdentity:
			log.Printf("Propagation node indicated missing identification on list request, tearing down link.")
			r.mu.Lock()
			link := r.outboundPropagationLink
			r.propagationTransferState = PRNoIdentityRcvd
			r.mu.Unlock()
			if link != nil {
				r.teardownLink(link)
			}
			return
		case peerErrorNoAccess:
			log.Printf("Propagation node did not allow list request, tearing down link.")
			r.mu.Lock()
			link := r.outboundPropagationLink
			r.propagationTransferState = PRNoAccess
			r.mu.Unlock()
			if link != nil {
				r.teardownLink(link)
			}
			return
		}
	}

	transientIDs, ok := transientIDsFromResponse(receipt.Response)
	if !ok {
		log.Printf("Invalid message list data received from propagation node")
		r.mu.Lock()
		link := r.outboundPropagationLink
		r.mu.Unlock()
		if link != nil {
			r.teardownLink(link)
		}
		return
	}

	if len(transientIDs) == 0 {
		r.mu.Lock()
		r.propagationTransferState = PRComplete
		r.propagationTransferProgress = 1.0
		r.propagationTransferLastResult = 0
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	maxMessages := r.propagationTransferMaxMessages
	retainSynced := r.retainSyncedOnNode
	deliveryLimit := r.deliveryPerTransferLimit
	r.mu.Unlock()

	haves := make([][]byte, 0, len(transientIDs))
	wants := make([][]byte, 0, len(transientIDs))
	for _, transientID := range transientIDs {
		r.mu.Lock()
		hasMessage := r.hasDeliveredTransientIDLocked(transientID)
		r.mu.Unlock()
		if hasMessage {
			if !retainSynced {
				haves = append(haves, append([]byte{}, transientID...))
			}
			continue
		}
		if maxMessages == 0 || len(wants) < maxMessages {
			wants = append(wants, append([]byte{}, transientID...))
		}
	}

	if _, err := r.requestLink(receipt.Link, messageGetPath, []any{wants, haves, deliveryLimit}, r.messageGetResponse, r.messageGetFailed, r.messageGetProgress, 0); err != nil {
		log.Printf("Could not request messages from propagation node: %v", err)
		r.mu.Lock()
		r.propagationTransferState = PRFailed
		r.mu.Unlock()
	}
}

// CancelPropagationNodeRequests forcefully aborts any currently active or pending synchronization requests directed at the outbound propagation node.
func (r *Router) CancelPropagationNodeRequests() {
	r.mu.Lock()
	link := r.outboundPropagationLink
	r.outboundPropagationLink = nil
	r.outboundPropagationLinkMessage = nil
	r.mu.Unlock()
	if link != nil {
		r.teardownLink(link)
	}
	r.acknowledgeSyncCompletion(true, nil)
	log.Printf("Cancelling propagation node requests")
}

func (r *Router) propagationTransferSignallingPacket(data []byte, _ *rns.Packet) {
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		return
	}
	signals, ok := unpacked.([]any)
	if !ok || len(signals) == 0 {
		return
	}
	signal, ok := responseErrorCode(signals[0])
	if !ok || signal != peerErrorInvalidStamp {
		return
	}

	r.mu.Lock()
	message := r.outboundPropagationLinkMessage
	r.mu.Unlock()
	if message == nil {
		return
	}

	log.Printf("Message rejected by propagation node")
	r.CancelOutbound(message.MessageID, StateRejected)
}

func (r *Router) messageGetResponse(receipt *rns.RequestReceipt) {
	if receipt == nil {
		return
	}
	if code, ok := responseErrorCode(receipt.Response); ok {
		switch code {
		case peerErrorNoIdentity:
			log.Printf("Propagation node indicated missing identification on get request, tearing down link.")
			r.mu.Lock()
			link := r.outboundPropagationLink
			r.propagationTransferState = PRNoIdentityRcvd
			r.mu.Unlock()
			if link != nil {
				r.teardownLink(link)
			}
			return
		case peerErrorNoAccess:
			log.Printf("Propagation node did not allow get request, tearing down link.")
			r.mu.Lock()
			link := r.outboundPropagationLink
			r.propagationTransferState = PRNoAccess
			r.mu.Unlock()
			if link != nil {
				r.teardownLink(link)
			}
			return
		}
	}

	payloads, ok := transientIDsFromResponse(receipt.Response)
	if !ok {
		payloadList, listOK := receipt.Response.([]any)
		if !listOK {
			payloads = nil
		} else {
			payloads = make([][]byte, 0, len(payloadList))
			for _, value := range payloadList {
				payload, ok := value.([]byte)
				if !ok {
					payloads = nil
					break
				}
				payloads = append(payloads, append([]byte{}, payload...))
			}
		}
	}
	if payloads == nil {
		log.Printf("Invalid message data received from propagation node")
		return
	}

	duplicates := 0
	haves := make([][]byte, 0, len(payloads))
	for _, payload := range payloads {
		if r.handlePropagatedInbound(payload) {
			duplicates++
		}
		haves = append(haves, rns.FullHash(payload))
	}
	if len(haves) > 0 {
		if _, err := r.requestLink(receipt.Link, messageGetPath, []any{nil, haves}, nil, r.messageGetFailed, nil, 0); err != nil {
			log.Printf("Could not acknowledge propagation sync completion: %v", err)
		}
	}

	r.mu.Lock()
	r.propagationTransferState = PRComplete
	r.propagationTransferProgress = 1.0
	r.propagationTransferLastDuplicates = duplicates
	r.propagationTransferLastResult = len(payloads)
	r.mu.Unlock()
	if err := r.SaveLocalTransientIDCaches(); err != nil {
		log.Printf("Could not save local transient ID caches: %v", err)
	}
}

func (r *Router) messageGetProgress(receipt *rns.RequestReceipt) {
	if receipt == nil {
		return
	}
	r.mu.Lock()
	r.propagationTransferState = PRReceiving
	r.propagationTransferProgress = r.requestProgress(receipt)
	r.mu.Unlock()
}

func (r *Router) messageGetFailed(_ *rns.RequestReceipt) {
	log.Printf("Message list/get request failed")
	r.mu.Lock()
	link := r.outboundPropagationLink
	r.mu.Unlock()
	if link != nil {
		r.teardownLink(link)
	}
}

func (r *Router) acknowledgeSyncCompletion(resetState bool, failureState *int) {
	r.mu.Lock()
	r.propagationTransferLastResult = 0
	if resetState || r.propagationTransferState <= PRComplete {
		if failureState == nil {
			r.propagationTransferState = PRIdle
		} else {
			r.propagationTransferState = *failureState
		}
	}
	r.propagationTransferProgress = 0.0
	r.wantsDownloadOnPathAvailableFrom = nil
	r.wantsDownloadOnPathAvailableTo = nil
	r.wantsDownloadOnPathAvailableAt = time.Time{}
	r.mu.Unlock()
}
