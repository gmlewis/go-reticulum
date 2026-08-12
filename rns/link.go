// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

const (
	// LinkPending indicates that the link has been requested but a handshake has not yet begun.
	LinkPending = 0x00
	// LinkHandshake indicates that the link is currently performing a cryptographic handshake.
	LinkHandshake = 0x01
	// LinkActive indicates that the link is fully established and actively passing data.
	LinkActive = 0x02
	// LinkStale indicates that the link has not seen traffic recently and may be dropping.
	LinkStale = 0x03
	// LinkClosed indicates that the link has been explicitly torn down or timed out.
	LinkClosed = 0x04
)

const (
	// LinkModeAES128CBC specifies the use of 128-bit AES in CBC mode for link encryption.
	LinkModeAES128CBC = 0x00
	// LinkModeAES256CBC specifies the use of 256-bit AES in CBC mode for link encryption.
	LinkModeAES256CBC = 0x01
)

const (
	// LinkECPubSize defines the combined byte length of the ephemeral encryption and signing public keys.
	LinkECPubSize = 32 + 32
	// LinkKeySize defines the byte length of a standard 256-bit X25519 key.
	LinkKeySize = 32
	// LinkMTUSize defines the number of bytes used to encode the Maximum Transmission Unit during signalling.
	LinkMTUSize = 3
	// MTUBytemask defines a bitmask used to extract the MTU from combined link signalling bytes.
	MTUBytemask = 0x1FFFFF
	// ModeBytemask defines a bitmask used to extract the cryptographic mode from link signalling bytes.
	ModeBytemask = 0xE0
)

const (
	// LinkKeepaliveMaxRTT matches Python's Link.KEEPALIVE_MAX_RTT.
	LinkKeepaliveMaxRTT = 1.75
	// LinkKeepaliveTimeoutFactor matches Python's Link.KEEPALIVE_TIMEOUT_FACTOR.
	LinkKeepaliveTimeoutFactor = 4.0
	// LinkStaleGrace matches Python's Link.STALE_GRACE.
	LinkStaleGrace = 5 * time.Second
	// LinkKeepaliveMax matches Python's Link.KEEPALIVE_MAX.
	LinkKeepaliveMax = 360 * time.Second
	// LinkKeepaliveMin matches Python's Link.KEEPALIVE_MIN.
	LinkKeepaliveMin = 5 * time.Second
	// LinkKeepaliveDefault matches Python's Link.KEEPALIVE default.
	LinkKeepaliveDefault = LinkKeepaliveMax
	// LinkStaleFactor matches Python's Link.STALE_FACTOR.
	LinkStaleFactor = 2
	// LinkWatchdogMaxSleep matches Python's Link.WATCHDOG_MAX_SLEEP.
	LinkWatchdogMaxSleep = 5 * time.Second
)

const (
	// AcceptNone strictly denies all incoming resource advertisements on the link.
	AcceptNone = 0x00
	// AcceptApp defers the decision to accept a resource advertisement to an application-provided callback.
	AcceptApp = 0x01
	// AcceptAll blindly accepts all incoming resource advertisements on the link.
	AcceptAll = 0x02
)

const (
	// TeardownTimeout indicates that the link was closed because a communication timeout occurred.
	TeardownTimeout = 0x00
	// TeardownDestinationClosed indicates that the link was closed by the remote destination.
	TeardownDestinationClosed = 0x01
	// TeardownInitiatorClosed indicates that the link was closed by the local initiator.
	TeardownInitiatorClosed = 0x02
	// TeardownTransportClosed indicates that the link was closed by the transport system.
	TeardownTransportClosed = 0x03
	// TeardownStale indicates that the link was closed because it became stale.
	TeardownStale = 0x04
)

// LinkCallbacks aggregates optional application-level hooks for asynchronous events occurring over a link's lifecycle.
type LinkCallbacks struct {
	LinkEstablished   func(*Link)
	LinkClosed        func(*Link)
	Packet            func(*Link, *Packet)
	RemoteIdentified  func(*Link, *Identity)
	Resource          func(*ResourceAdvertisement) bool
	ResourceStarted   func(*Resource)
	ResourceConcluded func(*Resource)
}

// Link manages a stateful, encrypted, and authenticated bidirectional connection between two Reticulum endpoints.
type Link struct {
	logger      *Logger
	destination *Destination
	initiator   bool
	// status is accessed atomically because it is read by background
	// goroutines (receive/handleRequest/handleResponse/watchdog) concurrently
	// with Teardown and state transitions that write it under l.mu.
	status atomic.Int32
	mode   int

	prv         *crypto.X25519PrivateKey
	pubBytes    []byte
	sigPrv      *crypto.Ed25519PrivateKey
	sigPubBytes []byte

	peerPub         *crypto.X25519PublicKey
	peerPubBytes    []byte
	peerSigPub      *crypto.Ed25519PublicKey
	peerSigPubBytes []byte

	linkID []byte
	hash   []byte

	sharedKey  []byte
	derivedKey []byte
	token      *crypto.Token

	rtt float64
	mtu int
	mdu int

	// expectedHops is the hop count the link expects its peer's link-proof
	// (LRPROOF) and RTT packets to carry. It is the Go port of Python
	// Link.expected_hops: set to Transport.hops_to(destination) at link
	// request time on the initiator side (Link.py:281) and to packet.hops
	// on the destination side at activation (Link.py:525, rtt_packet). A
	// pending link only accepts a proof whose hop count matches this value
	// (after an optional path re-balance — see Transport LRPROOF handling).
	// Zero means "unset" (Python uses None); hops_to returns PathfinderM
	// when the path is unknown, so an initiator with no known path starts
	// at PathfinderM (128) and re-balances down to the proof's hop count.
	expectedHops int

	// rebalancedAt records the wall-clock time of the first successful path
	// re-balance at the link terminus. It is the Go port of Python
	// Link.rebalanced (Link.py:268 `self.rebalanced = None`), set under the
	// `if not link.rebalanced:` guard in Transport's LRPROOF handler
	// (Transport.py:2298-2300) so only the earliest re-balance is timestamped.
	// The zero value means "no re-balance has occurred" (Python None).
	rebalancedAt time.Time

	// establishmentCost accumulates the on-wire byte cost of the link
	// establishment handshake (link request + proof packets, both
	// directions). It is the Go port of Python Link.establishment_cost,
	// used to derive establishmentRate = establishmentCost/rtt.
	establishmentCost float64
	// establishmentRate is establishmentCost/rtt once the link is active,
	// or zero (meaning "unset") while the link is still establishing. It
	// is the Go port of Python Link.establishment_rate.
	establishmentRate float64
	// expectedRate is the most recently measured in-flight data rate of
	// a completed resource transfer, in bytes/second (Python stores
	// size*8/transfer_time and get_expected_rate returns it raw). Zero
	// means "no transfer has completed yet" (Python None).
	expectedRate float64

	lastInbound   time.Time
	lastOutbound  time.Time
	lastKeepalive time.Time
	lastData      time.Time
	activatedAt   time.Time
	requestTime   time.Time
	lastProof     time.Time

	now func() time.Time

	trackPHYStats bool
	rssi          float64
	snr           float64
	q             float64

	// tx, rx, txbytes and rxbytes are the link traffic counters, the Go
	// port of Python Link.py's self.tx / self.rx / self.txbytes /
	// self.rxbytes (Link.py:250-253). tx/rx count packets; txbytes/rxbytes
	// count bytes, where txbytes is the ciphertext length (Python
	// Packet.send: self.destination.txbytes += len(self.ciphertext) for a
	// LINK destination) and rxbytes is the on-wire data length (Python
	// Link.receive: self.rxbytes += len(packet.data)).
	tx      uint64
	rx      uint64
	txbytes uint64
	rxbytes uint64

	callbacks LinkCallbacks
	mu        sync.Mutex

	remoteIdentity *Identity

	establishmentTimeout time.Duration
	attachedInterface    interfaces.Interface
	transport            Transport

	resourceStrategy       int
	outgoingResources      []*Resource
	incomingResources      []*Resource
	pendingRequests        []*RequestReceipt
	trafficTimeoutFactor   float64
	keepaliveTimeoutFactor float64
	keepalive              time.Duration
	staleTime              time.Duration
	channel                *Channel
	teardownReason         int
	watchdogOnce           sync.Once
	watchdogStop           chan struct{}
}

func (l *Link) signallingBytes() []byte {
	if l.transport != nil && !l.transport.LinkMTUDiscovery() {
		return nil
	}
	// signalling_value = (mtu & Link.MTU_BYTEMASK)+(((mode<<5) & Link.MODE_BYTEMASK)<<16)
	signallingValue := uint32(l.mtu&MTUBytemask) + uint32(((l.mode<<5)&ModeBytemask)<<16)
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, signallingValue)
	return buf[1:]
}

// GetStatus returns the current status of the link.
func (l *Link) GetStatus() int {
	return int(l.status.Load())
}

// String returns a stable, race-free identifier for the link. It exists so
// that formatting a *Link with %v does not reflectively read every struct
// field (including the sync.Mutex state), which would race with Lock/Unlock
// under the race detector. Mirrors Destination.String.
func (l *Link) String() string {
	return fmt.Sprintf("<Link:%x>", l.linkID)
}

// ExpectedHops returns the hop count the link expects its peer's link-proof
// and RTT packets to carry (Python Link.expected_hops). It is the value the
// Transport LRPROOF handler gates proof delivery on.
func (l *Link) ExpectedHops() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expectedHops
}

// SetExpectedHops records the expected hop count for the link. It exists to
// let the Transport path re-balance adopt a proof's hop count (Python
// Transport.py:2301 `link.expected_hops = packet.hops`) and to support tests.
func (l *Link) SetExpectedHops(hops int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.expectedHops = hops
}

// RebalancedAt returns the wall-clock time of the first successful path
// re-balance, or the zero time if no re-balance has occurred (Python
// Link.rebalanced, None until the first re-balance). The caller can test for
// "has rebalanced" with !RebalancedAt().IsZero().
func (l *Link) RebalancedAt() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rebalancedAt
}

// markRebalancedLocked records the re-balance timestamp the first time only,
// mirroring Python's `if not link.rebalanced: link.rebalanced = time.time()`
// guard (Transport.py:2298-2300). The caller must hold l.mu.
func (l *Link) markRebalancedLocked(now time.Time) {
	if l.rebalancedAt.IsZero() {
		l.rebalancedAt = now
	}
}

// MarkRebalanced is the transport-facing, lock-acquiring wrapper around
// markRebalancedLocked; it records the first re-balance timestamp for this
// link. It is called from the Transport LRPROOF handler once a proof signature
// has authorized adopting a new hop count.
func (l *Link) MarkRebalanced(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.markRebalancedLocked(now)
}

// TeardownReason returns the reason code recorded when the link was torn down.
func (l *Link) TeardownReason() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.teardownReason
}

// UpdateMDU proactively recalculates the Maximum Data Unit payload size based on the current MTU and header overhead.
func (l *Link) UpdateMDU() {
	l.mdu = l.mtu - HeaderMaxSize - IFACMinSize
	l.mdu = int(math.Floor(float64(l.mtu-IFACMinSize-HeaderMinSize-TokenOverhead)/float64(AES128BlockSize)))*AES128BlockSize - 1
}

// ProvePacket generates and sends a cryptographic proof for the given packet over this link.
func (l *Link) ProvePacket(packet *Packet) {
	if l.sigPrv == nil {
		l.logger.Error("Link cannot sign proof: no private key")
		return
	}
	signature := l.sigPrv.Sign(packet.PacketHash)

	proofData := make([]byte, 0, len(packet.PacketHash)+len(signature))
	proofData = append(proofData, packet.PacketHash...)
	proofData = append(proofData, signature...)

	proof := NewPacketWithTransport(l.transport, l, proofData)
	proof.PacketType = PacketProof
	l.mu.Lock()
	iface := l.attachedInterface
	l.mu.Unlock()
	proof.AttachedInterface = iface
	if err := l.send(proof); err != nil {
		l.logger.Debug("Failed to send link proof: %v", err)
	}
	l.hadOutbound()
}

// GetHash returns the truncated cryptographic hash identifying this link.
func (l *Link) GetHash() []byte {
	return l.linkID
}

func (l *Link) hadOutbound() {
	l.mu.Lock()
	l.lastOutbound = time.Now()
	// Non-keepalive outbound traffic also advances last_data, mirroring
	// Python Link.had_outbound(is_keepalive=False).
	l.lastData = l.lastOutbound
	l.mu.Unlock()
}

func (l *Link) hadKeepaliveOutbound() {
	now := time.Now()
	l.mu.Lock()
	l.lastOutbound = now
	l.lastKeepalive = now
	l.mu.Unlock()
}

func (l *Link) updateKeepaliveLocked() {
	keepaliveSeconds := l.rtt * (float64(LinkKeepaliveMax) / float64(time.Second)) / LinkKeepaliveMaxRTT
	keepalive := min(max(time.Duration(keepaliveSeconds*float64(time.Second)), LinkKeepaliveMin), LinkKeepaliveMax)
	l.keepalive = keepalive
	l.staleTime = time.Duration(LinkStaleFactor) * keepalive
}

// GetType returns the destination type for a link.
func (l *Link) GetType() int {
	return DestinationLink
}

// AttachedInterface returns the interface this link is directly attached to,
// if any. A link established over a single interface (e.g. a shared-instance
// local client, or a direct pipe/TCP peer) records that interface as its
// attached interface when the link request is received, and all link packets
// routed through it are delivered directly. A link established over a
// multi-hop transport path has no single attached interface (returns nil) and
// its packets route via the transport path table instead.
func (l *Link) AttachedInterface() interfaces.Interface {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attachedInterface
}

// GetTransport returns the transport system associated with this link.
func (l *Link) GetTransport() Transport {
	return l.transport
}

// NewLink constructs a link explicitly bound to a custom transport system.
func NewLink(ts Transport, destination *Destination) (*Link, error) {
	if destination != nil && destination.Type != DestinationSingle {
		return nil, errors.New("links can only be established to the SINGLE destination type")
	}

	l := &Link{
		logger:                 ts.GetLogger(),
		destination:            destination,
		initiator:              true,
		mode:                   LinkModeAES256CBC,
		mtu:                    MTU,
		transport:              ts,
		trafficTimeoutFactor:   6.0,
		keepaliveTimeoutFactor: LinkKeepaliveTimeoutFactor,
		keepalive:              LinkKeepaliveDefault,
		staleTime:              LinkStaleFactor * LinkKeepaliveDefault,
		watchdogStop:           make(chan struct{}),
	}
	l.status.Store(LinkPending)
	l.UpdateMDU()

	var err error
	l.prv, err = crypto.GenerateX25519PrivateKey()
	if err != nil {
		return nil, err
	}
	l.pubBytes = l.prv.PublicKey().PublicBytes()

	l.sigPrv, err = crypto.GenerateEd25519PrivateKey()
	if err != nil {
		return nil, err
	}
	l.sigPubBytes = l.sigPrv.PublicKey().PublicBytes()

	if destination != nil {
		// Initiator side
		l.initiator = true
		l.establishmentTimeout = establishmentTimeoutPerHop
		// expected_hops = RNS.Transport.hops_to(self.destination.hash)
		// (Link.py:281). hops_to returns PathfinderM when the path is
		// unknown; the value is re-balanced down to the proof's hop count
		// once the LRPROOF arrives (see Transport LRPROOF handling).
		if ts != nil {
			l.expectedHops = ts.HopsTo(destination.Hash)
		}
	} else {
		// Receiver side
		l.initiator = false
	}

	return l, nil
}

// Establish actively dispatches the initial link request packet onto the network to begin the Diffie-Hellman handshake.
func (l *Link) Establish() error {
	if !l.initiator {
		return errors.New("only the initiator can start establishment")
	}

	l.logger.Notice("Establishing link to %v (destination hash=%x)", l.destination.name, l.destination.Hash)

	// requestData = self.pub_bytes+self.sig_pub_bytes+signalling_bytes
	sigBytes := l.signallingBytes()
	requestData := make([]byte, 0, len(l.pubBytes)+len(l.sigPubBytes)+len(sigBytes))
	requestData = append(requestData, l.pubBytes...)
	requestData = append(requestData, l.sigPubBytes...)
	requestData = append(requestData, sigBytes...)

	p := NewPacketWithTransport(l.transport, l.destination, requestData)
	p.PacketType = PacketLinkRequest

	if err := p.Pack(); err != nil {
		return err
	}

	l.linkID = LinkIDFromLR(p)
	l.hash = l.linkID
	l.requestTime = time.Now()

	// Set the establishment timeout based on the number of hops to the
	// destination, matching Python's
	//   establishment_timeout = first_hop_timeout + PER_HOP * max(1, hops)
	// where first_hop_timeout defaults to PER_HOP when latency is unknown.
	hops := 1
	if l.transport != nil {
		hops = max(1, l.transport.HopsTo(l.destination.Hash))
	}
	l.establishmentTimeout = establishmentTimeoutPerHop + establishmentTimeoutPerHop*time.Duration(hops)

	l.startWatchdog()

	// Register with Transport
	if l.transport != nil {
		l.transport.RegisterLink(l)
	}

	l.logger.Notice("Sending link request to %x, timeout=%v, hops=%v", l.destination.Hash, l.establishmentTimeout, hops)
	return l.send(p)
}

// LinkIDFromLR deterministically calculates the unique link identifier based on the payload of a link request packet.
func LinkIDFromLR(packet *Packet) []byte {
	hashablePart := packet.GetHashablePart()
	if len(packet.Data) > LinkECPubSize {
		diff := len(packet.Data) - LinkECPubSize
		hashablePart = hashablePart[:len(hashablePart)-diff]
	}
	return TruncatedHash(hashablePart)
}

// ValidateRequest intercepts an inbound link request, validates its structure, and conditionally spawns a responding link instance.
func ValidateRequest(logger *Logger, destination *Destination, data []byte, packet *Packet) (*Link, error) {
	if len(data) < LinkECPubSize {
		return nil, fmt.Errorf("invalid link request payload size: %v", len(data))
	}

	l, err := NewLink(destination.transport, nil) // Receiver side link
	if err != nil {
		return nil, err
	}
	l.initiator = false
	l.destination = destination
	l.attachedInterface = packet.ReceivingInterface
	l.callbacks.LinkEstablished = destination.callbacks.LinkEstablished

	// Receiver side uses the destination's identity for signing
	if destination.identity != nil {
		l.sigPrv = destination.identity.sigPrv
		l.sigPubBytes = destination.identity.sigPub.PublicBytes()
	}

	peerPubBytes := data[:32]
	peerSigPubBytes := data[32:64]

	if err := l.LoadPeer(peerPubBytes, peerSigPubBytes); err != nil {
		return nil, err
	}

	l.linkID = LinkIDFromLR(packet)
	l.hash = l.linkID
	l.establishmentTimeout = establishmentTimeoutPerHop * time.Duration(max(1, packet.Hops))
	l.establishmentTimeout += LinkKeepaliveDefault

	if err := l.handshake(); err != nil {
		return nil, err
	}

	// Register link
	if l.transport != nil {
		l.transport.RegisterLink(l)
	}
	l.requestTime = time.Now()
	l.lastInbound = l.requestTime
	// Receiving the link request contributes its on-wire size to the
	// establishment cost (Python Link.py line 208).
	l.establishmentCost += float64(len(packet.Raw))
	l.startWatchdog()

	l.logger.Notice("Incoming link request %x accepted, proof key ready", l.linkID)

	// Send proof
	if err := l.Prove(); err != nil {
		l.logger.Notice("Failed to send proof for link request %x: %v", l.linkID, err)
		return nil, err
	}
	l.logger.Notice("Link request %x: proof sent, link registered and awaiting RTT", l.linkID)

	return l, nil
}

// Prove responds to a link request by transmitting a cryptographic proof affirming successful session key derivation.
func (l *Link) Prove() error {
	// signedData = self.link_id+self.pub_bytes+self.sig_pub_bytes+signalling_bytes
	sigBytes := l.signallingBytes()
	signedData := make([]byte, 0, len(l.linkID)+len(l.pubBytes)+len(l.sigPubBytes)+len(sigBytes))
	signedData = append(signedData, l.linkID...)
	signedData = append(signedData, l.pubBytes...)
	signedData = append(signedData, l.sigPubBytes...)
	signedData = append(signedData, sigBytes...)

	// Use destination identity to sign if available (receiver side)
	var signature []byte
	var err error
	if l.destination != nil && l.destination.identity != nil {
		signature, err = l.destination.identity.Sign(signedData)
	} else {
		signature, err = l.sigPrv.Sign(signedData), nil
	}

	if err != nil {
		return err
	}

	// proofData = signature+self.pub_bytes+signalling_bytes
	proofData := make([]byte, 0, len(signature)+len(l.pubBytes)+len(sigBytes))
	proofData = append(proofData, signature...)
	proofData = append(proofData, l.pubBytes...)
	proofData = append(proofData, sigBytes...)

	p := NewPacketWithTransport(l.transport, l, proofData)
	p.PacketType = PacketProof
	p.Context = ContextLrproof

	return l.send(p)
}

// receive processes incoming packets targeting this link, handling decryption and delegating to context-specific routines.
func (l *Link) receive(packet *Packet) {
	l.mu.Lock()
	l.lastInbound = time.Now()
	// Payload data (non-keepalive) advances last_data, mirroring Python
	// Link.receive: `if packet.context != RNS.Packet.KEEPALIVE:
	// self.last_data = self.last_inbound`.
	if packet.Context != ContextKeepalive {
		l.lastData = l.lastInbound
	}
	// Count the inbound packet, mirroring Python Link.receive (Link.py:937-938):
	// self.rx += 1; self.rxbytes += len(packet.data). Python guards the
	// increment with `not self.status == Link.CLOSED and not (self.initiator
	// and packet.context == KEEPALIVE and packet.data == bytes([0xFF]))`, so
	// the initiator's own keepalive echo is not counted toward rx.
	if l.status.Load() != LinkClosed &&
		!(l.initiator && packet.Context == ContextKeepalive && len(packet.Data) > 0 && packet.Data[0] == 0xFF) {
		l.rx++
		l.rxbytes += uint64(len(packet.Data))
	}
	l.mu.Unlock()

	l.logger.Verbose("Link %x receive: packet context=%v", l.linkID, packet.Context)
	if packet.Context == ContextLrproof {
		l.logger.Notice("Link %x: received link proof packet", l.linkID)
		if err := l.ValidateProof(packet); err != nil {
			l.logger.Notice("Failed to validate link proof for %x: %v", l.linkID, err)
		}
		return
	}

	if packet.Context == ContextLrrtt {
		l.logger.Notice("Link %x: received RTT packet", l.linkID)
		if !l.initiator {
			l.HandleRTT(packet)
		}
		return
	}

	shouldDecrypt := packet.Context != ContextResource &&
		packet.Context != ContextResourcePrf &&
		packet.Context != ContextKeepalive &&
		packet.Context != ContextCacheRequest &&
		packet.Context != ContextLrproof

	if shouldDecrypt {
		status := l.status.Load()
		if status == LinkClosed {
			l.logger.Debug("Skipping decrypt on closed link %x", l.linkID)
			return
		}
		plaintext, err := l.Decrypt(packet.Data)
		if err != nil {
			l.logger.Debug("Failed to decrypt packet for link %x: %v", l.linkID, err)
			return
		}
		packet.Data = plaintext
		l.logger.Verbose("Link %x decrypted packet, new len=%v", l.linkID, len(packet.Data))
	}
	l.logger.Verbose("Link %x received packet: type=%v, context=%x, size=%v", l.linkID, packet.PacketType, packet.Context, len(packet.Data))

	switch packet.Context {
	case ContextResourceAdv:
		packet.Destination = l
		adv, err := UnpackResourceAdvertisement(packet.Data)
		if err != nil {
			l.logger.Debug("Failed to unpack resource advertisement: %v", err)
			return
		}

		if adv.IsRequest {
			// Enforce the destination's max request size on request-carrying
			// resource advertisements (Link.py:1031-1037): when the
			// advertised data size (ResourceAdvertisement.read_size == adv.D)
			// exceeds the limit, reject the transfer rather than accepting it.
			if max := l.destination.MaxRequestSize(); max > 0 && adv.D > max {
				if err := Reject(packet); err != nil {
					l.logger.Debug("Failed to reject oversized request resource advertisement: %v", err)
				} else {
					l.logger.Debug("Rejected request resource with excessive size %v on %v", adv.D, l)
				}
				return
			}
			if _, err := Accept(packet, l.requestResourceConcluded, l.callbacks.ResourceStarted, nil); err != nil {
				l.logger.Debug("Failed to accept request resource advertisement: %v", err)
			}
			return
		}

		if adv.IsResponse {
			var pendingRR *RequestReceipt
			var progressCB func(*Resource)
			l.mu.Lock()
			for _, rr := range l.pendingRequests {
				if bytes.Equal(rr.RequestID, adv.Q) {
					pendingRR = rr
					progressCB = rr.responseResourceProgress
					break
				}
			}
			l.mu.Unlock()

			// Enforce the per-receipt max response size on the advertised
			// (uncompressed) response size (Link.py:1038-1056): when the
			// advertisement's read size (adv.D) exceeds the limit, reject the
			// transfer and fail the receipt rather than accepting. l.mu is
			// released before ResponseRejected (which re-enters
			// removePendingRequest under l.mu) to avoid a self-deadlock.
			if pendingRR != nil && pendingRR.MaxResponseSize() > 0 && adv.D > pendingRR.MaxResponseSize() {
				if err := Reject(packet); err != nil {
					l.logger.Debug("Failed to reject oversized response resource advertisement: %v", err)
				} else {
					l.logger.Debug("Rejected response with excessive size %v on %v", adv.D, l)
				}
				pendingRR.ResponseRejected()
				return
			}

			acceptedResource, err := Accept(packet, l.responseResourceConcluded, l.callbacks.ResourceStarted, progressCB)
			if err != nil {
				l.logger.Debug("Failed to accept response resource advertisement: %v", err)
				return
			}
			// Record the response/transfer sizes at accept time
			// (Link.py:1049-1054): response_size = read_size (adv.D, the
			// uncompressed data size), set only if still unset; and
			// response_transfer_size += read_transfer_size (adv.T, the
			// on-wire transfer size). This is the resource-path counterpart
			// to handleResponse's update_sizes accumulation — the conclude
			// path does not re-accumulate, so this single accept-time
			// recording is exactly once per response resource.
			if pendingRR != nil && acceptedResource != nil {
				pendingRR.recordResponseResourceSize(adv.D, adv.T)
			}
			return
		}

		accept := false
		if l.resourceStrategy == AcceptAll {
			accept = true
		} else if l.resourceStrategy == AcceptApp && l.callbacks.Resource != nil {
			accept = l.callbacks.Resource(adv)
		}

		if accept {
			if _, err := Accept(packet, l.callbacks.ResourceConcluded, l.callbacks.ResourceStarted, nil); err != nil {
				l.logger.Debug("Failed to accept resource advertisement: %v", err)
			}
		} else {
			if err := Reject(packet); err != nil {
				l.logger.Debug("Failed to reject resource advertisement: %v", err)
			}
		}

	case ContextRequest:
		requestID := packet.GetTruncatedHash()
		// Enforce the destination's max request size on the decrypted
		// (packed) request payload (Link.py:992-997): when it exceeds the
		// limit, drop the request before unpacking and dispatching it.
		if max := l.destination.MaxRequestSize(); max > 0 && int64(len(packet.Data)) > max {
			l.logger.Debug("Ignored request with excessive size %v on %v", len(packet.Data), l.destination)
			return
		}
		unpackedRequest, err := unpackRequestResponseData(packet.Data)
		if err != nil {
			l.logger.Error("Failed to unpack request: %v", err)
			return
		}
		reqList, ok := unpackedRequest.([]any)
		if !ok {
			l.logger.Error("Received malformed request packet (payload is not an array), ignoring")
			return
		}
		go l.handleRequest(requestID, reqList)

	case ContextResponse:
		l.logger.Verbose("Received ContextResponse packet, data len=%v", len(packet.Data))
		unpackedResponse, err := unpackRequestResponseData(packet.Data)
		if err != nil {
			l.logger.Error("Failed to unpack response: %v", err)
			return
		}
		resList, ok := unpackedResponse.([]any)
		if !ok || len(resList) < 2 {
			l.logger.Error("Received malformed response packet (not an array or too few elements), ignoring")
			return
		}
		requestID, ok := resList[0].([]byte)
		if !ok {
			l.logger.Error("Received malformed response packet (request id is not bytes), ignoring")
			return
		}
		responseData := resList[1]
		// Compute the on-wire response size the same way Python does
		// (Link.py:1009): transfer_size = len(umsgpack.packb(response_data))-2,
		// where the -2 strips the msgpack array wrapper overhead. Clamp at 0
		// so a degenerate encoding never yields a negative size.
		responseSize := packedResponseSize(responseData)
		l.logger.Verbose("Calling handleResponse for requestID=%x, responseData=%v (type: %T)", requestID, responseData, responseData)
		// Python (Link.py:1009-1010) passes transfer_size for both
		// response_size and response_transfer_size, with update_sizes=True
		// and check_size=True on the inline response-data path.
		l.handleResponse(requestID, responseData, nil, responseSize, responseSize, true, true)

	case ContextResourceReq:
		offset := 1
		if len(packet.Data) < offset {
			return
		}
		if packet.Data[0] == 0xFF {
			offset += ResourceMapHashLen
		}

		l.mu.Lock()
		for _, r := range l.outgoingResources {
			if len(packet.Data) < offset+len(r.hash) {
				continue
			}
			resourceHash := packet.Data[offset : offset+len(r.hash)]
			if bytes.Equal(r.hash, resourceHash) {
				go func(resource *Resource, requestData []byte) {
					if err := resource.Request(requestData); err != nil {
						l.logger.Debug("Failed to handle resource request: %v", err)
					}
				}(r, append([]byte(nil), packet.Data...))
				break
			}
		}
		l.mu.Unlock()

	case ContextResource:
		l.mu.Lock()
		for _, r := range l.incomingResources {
			go func(resource *Resource, part *Packet) {
				if err := resource.ReceivePart(part); err != nil {
					l.logger.Debug("Failed receiving resource part: %v", err)
				}
			}(r, packet)
		}
		l.mu.Unlock()

	case ContextResourcePrf:
		if packet.PacketType != PacketProof {
			return
		}
		if len(packet.Data) < 64 {
			return
		}

		proofHash := packet.Data[:32]
		l.mu.Lock()
		for _, r := range l.outgoingResources {
			if bytes.Equal(r.hash, proofHash) {
				go r.ValidateProof(packet.Data)
				break
			}
		}
		l.mu.Unlock()

	case ContextLinkIdentify:
		if !l.initiator {
			keySize := IdentityKeySize / 8
			if len(packet.Data) == keySize+64 {
				publicKey := packet.Data[:keySize]
				signature := packet.Data[keySize:]
				signedData := append(l.linkID, publicKey...)

				id, err := NewIdentity(false, l.logger)
				if err == nil {
					if err := id.LoadPublicKey(publicKey); err == nil {
						if id.Verify(signature, signedData) {
							// Blackholed-identity guard (RNS/Link.py:974-976,
							// v1.3.2): if the verified identity is on the local
							// blackhole list, terminate the incoming link and
							// skip the remote-identified callback rather than
							// accepting the identity.
							if l.transport != nil && l.transport.IsBlackholed(id.Hash) {
								l.logger.Debug("Terminating incoming link from blackholed identity %x", id.Hash)
								l.Teardown()
							} else {
								l.mu.Lock()
								l.remoteIdentity = id
								l.mu.Unlock()
								if l.callbacks.RemoteIdentified != nil {
									l.callbacks.RemoteIdentified(l, id)
								}
							}
						}
					}
				}
			}
		}

	case ContextKeepalive:
		if !l.initiator && len(packet.Data) > 0 && packet.Data[0] == 0xFF {
			// v1.4.0: only echo a 0xFE keepalive when nothing has been sent
			// for a full keepalive interval (RNS/Link.py:1124-1127:
			// `if time.time() >= self.last_outbound + self.keepalive`).
			// Recent outbound traffic already serves as the keepalive echo,
			// so a redundant 0xFE would just waste bandwidth.
			l.mu.Lock()
			lastOutbound := l.lastOutbound
			keepalive := l.keepalive
			l.mu.Unlock()
			if !l.nowTime().Before(lastOutbound.Add(keepalive)) {
				keepalivePacket := NewPacketWithTransport(l.transport, l, []byte{0xFE})
				keepalivePacket.Context = ContextKeepalive
				if err := l.send(keepalivePacket); err != nil {
					l.logger.Debug("Failed sending keepalive response: %v", err)
				} else {
					l.hadKeepaliveOutbound()
				}
			}
		}

	case ContextLinkClose:
		if bytes.Equal(packet.Data, l.linkID) {
			if l.initiator {
				l.teardown(TeardownDestinationClosed)
			} else {
				l.teardown(TeardownInitiatorClosed)
			}
		}

	case ContextChannel:
		l.mu.Lock()
		channel := l.channel
		l.mu.Unlock()
		if channel != nil {
			channel.Receive(packet.Data, packet)
		}

	default:
		l.mu.Lock()
		cb := l.callbacks.Packet
		l.mu.Unlock()
		status := l.status.Load()
		if status == LinkClosed || status == LinkPending {
			l.logger.Debug("Link %x: dropping packet on %v link", l.linkID, status)
			return
		}
		if packet.PacketType != PacketProof {
			l.logger.Debug("Link %x: received DATA packet %x, generating PROOF", l.linkID, packet.PacketHash)
			l.ProvePacket(packet)
		}
		if cb != nil {
			cb(l, packet)
		}
	}
}

func (l *Link) send(p *Packet) error {
	l.mu.Lock()
	l.lastOutbound = time.Now()
	iface := l.attachedInterface
	if l.status.Load() == LinkClosed {
		l.mu.Unlock()
		return fmt.Errorf("link %x is closed", l.linkID)
	}
	l.mu.Unlock()
	l.logger.Extreme("Link.send context=%v packetType=%v rawLen=%v attachedInterface=%v\n", p.Context, p.PacketType, len(p.Raw), iface != nil)
	l.logger.Verbose("Link.send: packet Context=%v, Data len=%v, attachedInterface=%v", p.Context, len(p.Data), iface != nil)
	if p.PacketType == PacketLinkRequest || p.PacketType == PacketProof {
		l.logger.Notice("Link.send: sending packet type=%v context=%v len=%v via interface=%v", p.PacketType, p.Context, len(p.Data), iface != nil)
	}
	if iface != nil {
		if !p.Packed {
			if err := p.Pack(); err != nil {
				return err
			}
		}
		l.accumulateEstablishmentCost(p)
		// Count the outbound packet on the link, mirroring Python
		// Packet.send's LINK-destination branch (Packet.py:294-295):
		// self.destination.tx += 1;
		// self.destination.txbytes += len(self.ciphertext). This
		// attached-interface path bypasses Packet.Send, so the counter
		// is bumped here; the transport path below goes through
		// Packet.Send which records it itself.
		l.recordOutbound(len(p.Ciphertext))
		// Send directly through the attached interface for link-specific packets
		if err := iface.Send(p.Raw); err != nil {
			l.logger.Error("Link.send: failed to send via attached interface: %v", err)
			return err
		}
		p.Sent = true
		p.SentAt = float64(time.Now().UnixNano()) / 1e9
		if p.Receipt != nil {
			p.Receipt.MarkSent(p.SentAt)
		}
		l.logger.Extreme("Link.send sent context=%v packetType=%v rawLen=%v via interface\n", p.Context, p.PacketType, len(p.Raw))
		l.logger.Verbose("Link.send: packet sent via attached interface, err=<nil>")
		return nil
	}
	if !p.Packed {
		if err := p.Pack(); err != nil {
			return err
		}
	}
	l.accumulateEstablishmentCost(p)
	err := p.Send()
	l.logger.Extreme("Link.send sent context=%v packetType=%v rawLen=%v via transport err=%v\n", p.Context, p.PacketType, len(p.Raw), err)
	l.logger.Verbose("Link.send: packet sent via transport, err=%v", err)
	return err
}

// accumulateEstablishmentCost adds the on-wire size of a link-request or
// link-proof packet to the link's establishment_cost, mirroring Python's
// `self.establishment_cost += len(packet.raw)` for the handshake packets
// (Link.py lines 319 and 379).
func (l *Link) accumulateEstablishmentCost(p *Packet) {
	if p == nil || (p.PacketType != PacketLinkRequest && p.PacketType != PacketProof) {
		return
	}
	l.mu.Lock()
	l.establishmentCost += float64(len(p.Raw))
	l.mu.Unlock()
}

// verifyProofSignatureLocked reconstructs the signed data for a link request
// proof (LRPROOF) from the packet and verifies it against the link's
// destination identity, WITHOUT performing the Diffie-Hellman handshake. It
// mirrors the signature check Python performs in two places: Link.validate_proof
// (Link.py:411-422) and Transport's path re-balance at the link terminus
// (Transport.py:2283-2296). The re-balance uses this lighter check to authorize
// adopting a new hop count before the full handshake runs in validate_proof.
// The caller must hold l.mu.
func (l *Link) verifyProofSignatureLocked(packet *Packet) bool {
	if l.destination == nil || l.destination.identity == nil || len(packet.Data) < 64+32 {
		return false
	}
	signature := packet.Data[:64]
	peerPubBytes := packet.Data[64:96]
	var sigBytes []byte
	if len(packet.Data) == 64+32+LinkMTUSize {
		sigBytes = packet.Data[96 : 96+LinkMTUSize]
	}
	peerSigPubBytes := l.destination.identity.GetPublicKey()[32:64]
	signedData := make([]byte, 0, len(l.linkID)+len(peerPubBytes)+len(peerSigPubBytes)+len(sigBytes))
	signedData = append(signedData, l.linkID...)
	signedData = append(signedData, peerPubBytes...)
	signedData = append(signedData, peerSigPubBytes...)
	signedData = append(signedData, sigBytes...)
	return l.destination.identity.Verify(signature, signedData)
}

// verifyProofSignature is the transport-facing, lock-acquiring wrapper around
// verifyProofSignatureLocked used by the path re-balance in the Transport
// LRPROOF handler.
func (l *Link) verifyProofSignature(packet *Packet) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.verifyProofSignatureLocked(packet)
}

// ValidateProof evaluates an incoming link proof packet and formally transitions the link into an active state upon success.
func (l *Link) ValidateProof(packet *Packet) error {
	l.logger.Info("ValidateProof: link %x, status=%v", l.linkID, l.status.Load())
	l.mu.Lock()
	if l.status.Load() != LinkPending {
		l.mu.Unlock()
		return errors.New("link is not in pending state")
	}

	// data = signature (64) + peerPubBytes (32) + signalling_bytes (optional, 3)
	if len(packet.Data) < 64+32 {
		l.mu.Unlock()
		return errors.New("invalid proof data length")
	}

	peerPubBytes := packet.Data[64:96]
	// Receiver sig pub is in destination identity
	peerSigPubBytes := l.destination.identity.GetPublicKey()[32:64]

	if err := l.LoadPeer(peerPubBytes, peerSigPubBytes); err != nil {
		l.mu.Unlock()
		return err
	}

	if err := l.handshake(); err != nil {
		l.mu.Unlock()
		return err
	}

	if !l.verifyProofSignatureLocked(packet) {
		l.mu.Unlock()
		return errors.New("invalid link proof signature")
	}

	l.attachedInterface = packet.ReceivingInterface
	l.status.Store(LinkActive)
	l.activatedAt = time.Now()
	l.rtt = time.Since(l.requestTime).Seconds()
	// Receiving the proof contributes its on-wire size to the
	// establishment cost (Python Link.py line 416), and once the RTT is
	// known the establishment rate is cost/rtt (Python line 436).
	l.establishmentCost += float64(len(packet.Raw))
	if l.rtt > 0 && l.establishmentCost > 0 {
		l.establishmentRate = l.establishmentCost / l.rtt
	}
	l.updateKeepaliveLocked()
	callback := l.callbacks.LinkEstablished
	l.mu.Unlock()

	l.logger.Notice("Link %x is now ACTIVE (ValidateProof), attachedInterface=%v, RTT=%v", l.linkID, l.attachedInterface != nil, time.Duration(l.rtt*float64(time.Second)))

	if l.transport != nil {
		l.transport.ActivateLink(l)
	}

	l.logger.Verbose("Link %x active, RTT is %v", l.linkID, time.Duration(l.rtt*float64(time.Second)))
	// Send RTT packet with msgpack-packed RTT value
	rttData, err := msgpack.Pack(l.rtt)
	if err != nil {
		return fmt.Errorf("packing RTT data: %w", err)
	}
	rttPacket := NewPacketWithTransport(l.transport, l, rttData)
	rttPacket.PacketType = PacketData
	rttPacket.Context = ContextLrrtt
	if err := l.send(rttPacket); err != nil {
		return fmt.Errorf("sending RTT packet: %w", err)
	}

	if callback != nil {
		callback(l)
	}

	return nil
}

// HandleRTT processes an incoming Round Trip Time packet to finalize activation for non-initiator link instances.
func (l *Link) HandleRTT(packet *Packet) {
	l.logger.Info("Handling RTT for link %x, current status=%v", l.linkID, l.status.Load())
	l.mu.Lock()
	if l.status.Load() == LinkHandshake || l.status.Load() == LinkPending {
		measuredRTT := time.Since(l.requestTime).Seconds()
		l.mu.Unlock()

		plaintext, err := l.Decrypt(packet.Data)
		if err != nil {
			l.logger.Error("Error occurred while processing RTT packet, tearing down link: %v", err)
			l.Teardown()
			return
		}

		unpackedRTT, err := msgpack.Unpack(plaintext)
		if err != nil {
			l.logger.Error("Error occurred while processing RTT packet, tearing down link: %v", err)
			l.Teardown()
			return
		}
		receivedRTT, ok := unpackedRTT.(float64)
		if !ok {
			l.logger.Error("Error occurred while processing RTT packet, tearing down link: invalid RTT type %T", unpackedRTT)
			l.Teardown()
			return
		}

		l.mu.Lock()
		l.rtt = math.Max(measuredRTT, receivedRTT)
		l.status.Store(LinkActive)
		l.activatedAt = time.Now()
		// expected_hops = packet.hops (Link.py:525, rtt_packet): the
		// destination side records the RTT packet's hop count as the
		// expected hops once the link is active.
		l.expectedHops = packet.Hops
		// Once the RTT is known the establishment rate is cost/rtt,
		// mirroring Python Link.rtt_packet (Link.py line 545).
		if l.rtt > 0 && l.establishmentCost > 0 {
			l.establishmentRate = l.establishmentCost / l.rtt
		}
		l.updateKeepaliveLocked()
		callback := l.callbacks.LinkEstablished
		l.mu.Unlock()
		l.logger.Notice("Link %x is now ACTIVE (HandleRTT), RTT=%v", l.linkID, time.Duration(l.rtt*float64(time.Second)))
		if l.transport != nil {
			l.transport.ActivateLink(l)
		}
		l.logger.Verbose("Link %x active after RTT", l.linkID)
		if callback != nil {
			callback(l)
		}
		return
	}
	l.mu.Unlock()
}

func (l *Link) startWatchdog() {
	l.watchdogOnce.Do(func() {
		go l.watchdogJob()
	})
}

func (l *Link) watchdogJob() {
	for {
		sleep := l.watchdogStep(time.Now())

		l.mu.Lock()
		closed := l.status.Load() == LinkClosed
		l.mu.Unlock()
		if closed {
			return
		}

		if sleep <= 0 {
			sleep = time.Millisecond
		}

		timer := time.NewTimer(sleep)
		select {
		case <-timer.C:
		case <-l.watchdogStop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (l *Link) watchdogStep(now time.Time) time.Duration {
	var callback func(*Link)

	l.mu.Lock()
	switch l.status.Load() {
	case LinkClosed:
		l.mu.Unlock()
		return 0

	case LinkPending, LinkHandshake:
		nextCheck := l.requestTime.Add(l.establishmentTimeout)
		sleep := nextCheck.Sub(now)
		if !now.Before(nextCheck) {
			l.status.Store(LinkClosed)
			l.teardownReason = TeardownTimeout
			if l.channel != nil {
				l.channel.Shutdown()
			}
			callback = l.callbacks.LinkClosed
			sleep = time.Millisecond
		}
		l.mu.Unlock()
		if callback != nil {
			go callback(l)
		}
		if sleep > LinkWatchdogMaxSleep {
			return LinkWatchdogMaxSleep
		}
		return sleep

	case LinkActive:
		lastInbound := l.activatedAt
		if l.lastInbound.After(lastInbound) {
			lastInbound = l.lastInbound
		}
		if l.lastProof.After(lastInbound) {
			lastInbound = l.lastProof
		}

		// v1.4.0: the keepalive/stale check also fires on outbound inactivity
		// (RNS/Link.py:749: `now >= last_inbound + keepalive or now >=
		// last_outbound + keepalive`). This makes a silent initiator that is
		// still receiving destination traffic send keepalives so the remote
		// side does not time it out, instead of only reacting to inbound
		// silence. The stale check below remains gated on last_inbound.
		if !now.Before(lastInbound.Add(l.keepalive)) || !now.Before(l.lastOutbound.Add(l.keepalive)) {
			sendKeepalive := l.initiator && !now.Before(l.lastKeepalive.Add(l.keepalive))
			if sendKeepalive {
				l.lastKeepalive = now
			}

			if !now.Before(lastInbound.Add(l.staleTime)) {
				l.status.Store(LinkStale)
				sleep := time.Duration(l.rtt*l.keepaliveTimeoutFactor*float64(time.Second)) + LinkStaleGrace
				l.mu.Unlock()
				if sendKeepalive {
					l.sendKeepalive()
				}
				if sleep > LinkWatchdogMaxSleep {
					return LinkWatchdogMaxSleep
				}
				return sleep
			}

			sleep := l.keepalive
			l.mu.Unlock()
			if sendKeepalive {
				l.sendKeepalive()
			}
			if sleep > LinkWatchdogMaxSleep {
				return LinkWatchdogMaxSleep
			}
			return sleep
		}

		sleep := lastInbound.Add(l.keepalive).Sub(now)
		l.mu.Unlock()
		if sleep > LinkWatchdogMaxSleep {
			return LinkWatchdogMaxSleep
		}
		return sleep

	case LinkStale:
		l.status.Store(LinkClosed)
		l.teardownReason = TeardownTimeout
		if l.channel != nil {
			l.channel.Shutdown()
		}
		callback = l.callbacks.LinkClosed
		l.mu.Unlock()
		if callback != nil {
			go callback(l)
		}
		return time.Millisecond
	}

	l.mu.Unlock()
	return LinkWatchdogMaxSleep
}

func (l *Link) sendKeepalive() {
	keepalivePacket := NewPacketWithTransport(l.transport, l, []byte{0xFF})
	keepalivePacket.Context = ContextKeepalive
	if err := l.send(keepalivePacket); err != nil {
		l.logger.Debug("Failed sending keepalive: %v", err)
		return
	}
	l.hadKeepaliveOutbound()
}

// Handshake triggers the underlying Diffie-Hellman cryptographic exchange, deriving secure symmetric session keys.
func (l *Link) Handshake() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handshake()
}

func (l *Link) handshake() error {
	if l.status.Load() != LinkPending && l.status.Load() != LinkHandshake {
		return fmt.Errorf("invalid link state for handshake: %v", l.status.Load())
	}

	if l.peerPub == nil {
		return errors.New("cannot perform handshake without peer public key")
	}

	var err error
	l.sharedKey, err = l.prv.Exchange(l.peerPub)
	if err != nil {
		return err
	}

	derivedKeyLength := 64
	if l.mode == LinkModeAES128CBC {
		derivedKeyLength = 32
	}

	l.derivedKey, err = crypto.HKDF(derivedKeyLength, l.sharedKey, l.linkID, nil)
	if err != nil {
		return err
	}

	l.token, err = crypto.NewToken(l.derivedKey)
	if err != nil {
		return err
	}

	l.status.Store(LinkHandshake)
	return nil
}

// Sign hashes and uniquely signs a given byte slice utilizing the private ephemeral signing key tied to this link.
func (l *Link) Sign(data []byte) ([]byte, error) {
	if l.sigPrv == nil {
		return nil, errors.New("link does not hold a private signing key")
	}
	return l.sigPrv.Sign(data), nil
}

// Verify guarantees data authenticity by comparing a signature against the remote peer's established public signing key.
func (l *Link) Verify(signature, data []byte) bool {
	if l.peerSigPub == nil {
		return false
	}
	return l.peerSigPub.Verify(signature, data)
}

// Encrypt obscures arbitrary plaintext data securely using the derived symmetric session token established during handshake.
func (l *Link) Encrypt(plaintext []byte) ([]byte, error) {
	if l.token == nil {
		return nil, errors.New("link session keys not initialized")
	}
	return l.token.Encrypt(plaintext)
}

// Decrypt strips away link-level encryption using the derived symmetric session token, returning original plaintext.
func (l *Link) Decrypt(ciphertext []byte) ([]byte, error) {
	if l.token == nil {
		return nil, errors.New("link session keys not initialized")
	}
	return l.token.Decrypt(ciphertext)
}

// Identify explicitly reveals and cryptographically proves the initiator's long-term identity to the remote peer over this active link.
func (l *Link) Identify(identity *Identity) error {
	if !l.initiator || l.status.Load() != LinkActive {
		return errors.New("invalid state for identification")
	}
	if identity == nil {
		return errors.New("identity is required")
	}

	pubKey := identity.GetPublicKey()
	if len(pubKey) == 0 {
		return errors.New("identity has no public key")
	}
	signedData := make([]byte, 0, len(l.linkID)+len(pubKey))
	signedData = append(signedData, l.linkID...)
	signedData = append(signedData, pubKey...)
	signature, err := identity.Sign(signedData)
	if err != nil {
		return err
	}

	proofData := append(pubKey, signature...)
	if len(proofData) == 0 {
		return errors.New("invalid identify proof data")
	}

	p := NewPacketWithTransport(l.transport, l, proofData)
	p.Context = ContextLinkIdentify
	return l.send(p)
}

// LoadPeer parses and permanently associates the remote peer's ephemeral public encryption and signature keys into link state.
func (l *Link) LoadPeer(pubBytes, sigPubBytes []byte) error {
	var err error
	l.peerPubBytes = pubBytes
	l.peerPub, err = crypto.NewX25519PublicKeyFromBytes(pubBytes)
	if err != nil {
		return err
	}

	l.peerSigPubBytes = sigPubBytes
	l.peerSigPub, err = crypto.NewEd25519PublicKeyFromBytes(sigPubBytes)
	if err != nil {
		return err
	}

	return nil
}

// SetPacketCallback registers a handler function that executes precisely when standard decrypted data packets traverse the link.
func (l *Link) SetPacketCallback(callback func([]byte, *Packet)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if callback != nil {
		l.callbacks.Packet = func(_ *Link, p *Packet) {
			callback(p.Data, p)
		}
	} else {
		l.callbacks.Packet = nil
	}
}

// SetResourceCallback defines a handler function consulted whenever a remote peer advertises a potential resource transfer over the link.
func (l *Link) SetResourceCallback(callback func(*ResourceAdvertisement) bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks.Resource = callback
}

// SetResourceStrategy explicitly overrides the default behavior dictating whether new incoming resource advertisements should be accepted.
func (l *Link) SetResourceStrategy(strategy int) error {
	if strategy != AcceptNone && strategy != AcceptApp && strategy != AcceptAll {
		return fmt.Errorf("invalid resource strategy %v", strategy)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resourceStrategy = strategy
	return nil
}

// SetResourceStartedCallback assigns a notification function to fire synchronously when an incoming resource transfer physically begins.
func (l *Link) SetResourceStartedCallback(callback func(*Resource)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks.ResourceStarted = callback
}

// SetResourceConcludedCallback defines a notification handler to fire precisely when an inbound resource transfer reaches completion.
func (l *Link) SetResourceConcludedCallback(callback func(*Resource)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks.ResourceConcluded = callback
}

// SetLinkEstablishedCallback maps a custom application routine to trigger immediately upon the successful activation of this link.
func (l *Link) SetLinkEstablishedCallback(callback func(*Link)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks.LinkEstablished = callback
}

// SetRemoteIdentifiedCallback maps an application hook firing when a remote peer safely proves its long-term identity via an in-band packet.
func (l *Link) SetRemoteIdentifiedCallback(callback func(*Link, *Identity)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks.RemoteIdentified = callback
}

// SetLinkClosedCallback defines a mandatory notification hook designed to safely clean up logic when the link connection terminates.
func (l *Link) SetLinkClosedCallback(callback func(*Link)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.callbacks.LinkClosed = callback
}

// GetRemoteIdentity securely retrieves the underlying structural Identity, if the peer has opted to reveal and prove it.
func (l *Link) GetRemoteIdentity() *Identity {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.remoteIdentity
}

// GetChannel allocates and automatically starts a high-level stream-oriented Channel built seamlessly over this discrete link.
func (l *Link) GetChannel() *Channel {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.channel == nil {
		l.channel = NewChannel(&LinkChannelOutlet{link: l})
		l.channel.Start()
	}
	return l.channel
}

// GetPublicBytes retrieves the raw byte representation of the local node's ephemeral encryption public key.
func (l *Link) GetPublicBytes() []byte {
	return l.pubBytes
}

// GetSigPublicBytes retrieves the raw byte representation of the local node's ephemeral signature public key.
func (l *Link) GetSigPublicBytes() []byte {
	return l.sigPubBytes
}

// LinkChannelOutlet serves as a structural bridge integrating an abstract Channel directly atop a physical link.
type LinkChannelOutlet struct {
	link *Link
}

// Send dynamically wraps raw channel data into a formatted packet and delegates physical transmission to the link transport.
func (o *LinkChannelOutlet) Send(raw []byte) (*Packet, error) {
	o.link.logger.Extreme("LinkChannelOutlet.Send rawLen=%v\n", len(raw))
	p := NewPacketWithTransport(o.link.transport, o.link, raw)
	p.Context = ContextChannel
	o.link.logger.Extreme("LinkChannelOutlet.Send built context=%v packetType=%v rawLen=%v\n", p.Context, p.PacketType, len(p.Raw))
	if err := o.link.send(p); err != nil {
		o.link.logger.Extreme("LinkChannelOutlet.Send failed context=%v packetType=%v err=%v\n", p.Context, p.PacketType, err)
		return nil, err
	}
	o.link.logger.Extreme("LinkChannelOutlet.Send done context=%v packetType=%v rawLen=%v\n", p.Context, p.PacketType, len(p.Raw))
	return p, nil
}

// Resend attempts to retransmit a previously stalled packet without altering its fundamental cryptographic identity.
func (o *LinkChannelOutlet) Resend(p *Packet) (*Packet, error) {
	if p == nil {
		return nil, errors.New("cannot resend nil packet")
	}
	if o.link == nil || o.link.transport == nil {
		return nil, errors.New("link transport unavailable for resend")
	}
	if err := o.link.transport.Outbound(p); err != nil {
		return nil, err
	}
	return p, nil
}

// MDU forwards the calculated Maximum Data Unit safely available to the channel from the underlying link limitations.
func (o *LinkChannelOutlet) MDU() int {
	return o.link.mdu
}

// GetPacketID returns the packet hash used to match a sent packet against
// envelopes in the channel txRing, or nil when the packet has no on-wire
// identity — a "ghost" envelope whose packet was never packed/sent (empty
// Raw). Mirrors Python LinkChannelOutlet.get_packet_id (Channel.py:600-603),
// guarding with `raw is not None` so a ghost envelope is never matched by a
// delivery or timeout callback.
func (o *LinkChannelOutlet) GetPacketID(p *Packet) []byte {
	if p == nil || len(p.Raw) == 0 {
		return nil
	}
	return p.PacketHash
}

// RTT exposes the current measured Round Trip Time from the underlying link strictly to aid the channel's retry metrics.
func (o *LinkChannelOutlet) RTT() float64 {
	return o.link.rtt
}

// IsUsable safely reports whether the physical link remains in an active state capable of sustaining channel traffic.
func (o *LinkChannelOutlet) IsUsable() bool {
	return o.link.status.Load() == LinkActive
}

// TimedOut is called by the channel when it has exceeded its maximum retry count.
func (o *LinkChannelOutlet) TimedOut() {
	o.link.Teardown()
}

// Teardown actively closes the link, destroying related channels, and notifying any observers that data transmission has halted.
func (l *Link) Teardown() {
	status := l.status.Load()
	if status != LinkPending && status != LinkClosed {
		l.sendTeardownPacket()
	}
	if l.initiator {
		l.teardown(TeardownInitiatorClosed)
		return
	}
	l.teardown(TeardownDestinationClosed)
}

func (l *Link) teardown(reason int) {
	l.mu.Lock()
	if l.status.Load() == LinkClosed {
		l.mu.Unlock()
		return
	}

	l.status.Store(LinkClosed)
	l.teardownReason = reason
	if l.channel != nil {
		l.channel.Shutdown()
	}
	callback := l.callbacks.LinkClosed
	watchdogStop := l.watchdogStop
	l.mu.Unlock()

	if watchdogStop != nil {
		select {
		case <-watchdogStop:
		default:
			close(watchdogStop)
		}
	}

	if callback != nil {
		go callback(l)
	}

	l.logger.Verbose("Link %x closed: reason=%v", l.linkID, reason)
}

func (l *Link) sendTeardownPacket() {
	if len(l.linkID) == 0 {
		return
	}
	teardownPacket := NewPacketWithTransport(l.transport, l, l.linkID)
	teardownPacket.Context = ContextLinkClose
	if err := l.send(teardownPacket); err != nil {
		l.logger.Debug("Failed sending teardown packet: %v", err)
	}
}

// Request fires a generalized structured request packet asynchronously, expecting a correlated logical response from the remote peer.
func (l *Link) Request(path string, data any, responseCallback, failedCallback, progressCallback func(*RequestReceipt), timeout time.Duration, maxResponseSize int64) (*RequestReceipt, error) {
	requestPathHash := TruncatedHash([]byte(path))
	// unpacked_request  = [time.time(), request_path_hash, data]
	unpackedRequest := []any{float64(time.Now().UnixNano()) / 1e9, requestPathHash, data}
	packedRequest, err := msgpack.Pack(unpackedRequest)
	if err != nil {
		return nil, err
	}

	if timeout == 0 {
		// Calculate default timeout
		timeout = time.Duration(l.rtt*l.trafficTimeoutFactor*float64(time.Second)) + 10*time.Second
	}

	if len(packedRequest) <= l.mdu {
		l.logger.Debug("Sending request %v for %v over link %x", TruncatedHash(packedRequest), path, l.linkID)
		p := NewPacketWithTransport(l.transport, l, packedRequest)
		p.Context = ContextRequest

		if err := p.Pack(); err != nil {
			return nil, err
		}

		rr := &RequestReceipt{
			logger:           l.logger,
			Link:             l,
			RequestID:        p.GetTruncatedHash(), // Match Reticulum behavior
			Status:           RequestSent,
			SentAt:           time.Now(),
			Timeout:          timeout,
			maxResponseSize:  maxResponseSize,
			callback:         responseCallback,
			failedCallback:   failedCallback,
			progressCallback: progressCallback,
		}

		l.mu.Lock()
		l.pendingRequests = append(l.pendingRequests, rr)
		l.mu.Unlock()

		if err := l.send(p); err != nil {
			l.removePendingRequest(rr)
			return nil, err
		}

		// An inline request has been dispatched over the link; mark it
		// delivered and arm the response timeout so a missing response fires
		// failedCallback at `timeout` — mirroring the resource-request path
		// (requestResourceConcluded) and Python Link.request. Without this the
		// inline receipt never times out: failedCallback is never invoked and
		// the receipt leaks into pendingRequests until the link is torn down.
		rr.mu.Lock()
		if rr.Status == RequestSent {
			rr.Status = RequestDelivered
			if rr.StartedAt.IsZero() {
				rr.StartedAt = time.Now()
			}
			rr.mu.Unlock()
			go rr.responseTimeoutJob(time.Now().Add(timeout))
		} else {
			rr.mu.Unlock()
		}

		return rr, nil
	} else {
		requestID := TruncatedHash(packedRequest)
		l.logger.Debug("Sending request %x as resource.", requestID)

		// request_resource = RNS.Resource(packed_request, self, request_id = request_id, is_response = False, timeout = timeout)
		r, err := NewResource(packedRequest, l)
		if err != nil {
			return nil, err
		}
		r.requestID = requestID
		r.isResponse = false

		rr := &RequestReceipt{
			logger:           l.logger,
			Link:             l,
			RequestID:        requestID,
			Resource:         r,
			Status:           RequestSent,
			SentAt:           time.Now(),
			Timeout:          timeout,
			maxResponseSize:  maxResponseSize,
			callback:         responseCallback,
			failedCallback:   failedCallback,
			progressCallback: progressCallback,
		}

		l.mu.Lock()
		l.pendingRequests = append(l.pendingRequests, rr)
		l.mu.Unlock()

		r.callback = rr.requestResourceConcluded
		if err := r.Advertise(); err != nil {
			return nil, err
		}

		return rr, nil
	}
}

func (l *Link) handleRequest(requestID []byte, unpackedRequest []any) {
	if l.status.Load() != LinkActive {
		return
	}

	if len(unpackedRequest) < 3 {
		l.logger.Debug("Received malformed request packet, ignoring")
		return
	}

	ts, ok0 := unpackedRequest[0].(float64)
	if !ok0 {
		l.logger.Debug("Received malformed request packet (bad timestamp), ignoring")
		return
	}
	requestedAt := time.Unix(0, int64(ts*1e9))
	pathHash, ok1 := unpackedRequest[1].([]byte)
	requestData, ok2 := unpackedRequest[2].([]byte)
	if !ok1 {
		l.logger.Debug("Received malformed request packet (bad path hash), ignoring")
		return
	}
	// requestData can be nil
	if unpackedRequest[2] == nil {
		requestData = nil
		ok2 = true
	}
	if !ok2 {
		l.logger.Debug("Received malformed request packet (bad request data), ignoring")
		return
	}

	l.mu.Lock()
	handler, ok := l.destination.requestHandlers[string(pathHash)]
	l.mu.Unlock()

	handlerPath := "<nil>"
	if handler != nil {
		handlerPath = handler.Path
	}
	l.logger.Verbose("Request handler lookup: pathHash=%x, ok=%v, handler.Path=%v", pathHash, ok, handlerPath)
	if ok {
		allowed := false
		if handler.Allow == AllowAll {
			allowed = true
		} else if handler.Allow == AllowList {
			if l.remoteIdentity != nil {
				for _, addr := range handler.AllowedList {
					if bytes.Equal(addr, l.remoteIdentity.Hash) {
						allowed = true
						break
					}
				}
			}
		}

		l.logger.Verbose("Request allowed check: allowed=%v, handler.Allow=%v", allowed, handler.Allow)
		if allowed {
			l.logger.Verbose("Handling request %v for %v", requestID, handler.Path)
			response := handler.ResponseGenerator(handler.Path, requestData, requestID, l.linkID, l.remoteIdentity, requestedAt)
			l.logger.Verbose("Handler response: %v (type: %T)", response, response)

			if response != nil {
				l.logger.Verbose("Sending response for request %x", requestID)
				packedResponse, err := msgpack.Pack([]any{requestID, response})
				if err != nil {
					l.logger.Error("Failed to pack response: %v", err)
					return
				}

				if len(packedResponse) <= l.mdu {
					p := NewPacketWithTransport(l.transport, l, packedResponse)
					p.Context = ContextResponse
					if err := l.send(p); err != nil {
						l.logger.Error("Failed to send response packet: %v", err)
					}
				} else {
					// Send as resource
					r, err := NewResourceWithOptions(packedResponse, l, ResourceOptions{
						AutoCompress:      handler.AutoCompress,
						AutoCompressLimit: handler.AutoCompressLimit,
					})
					if err != nil {
						l.logger.Error("Failed to create response resource: %v", err)
						return
					}
					r.requestID = requestID
					r.isResponse = true
					if err := r.Advertise(); err != nil {
						l.logger.Error("Failed to advertise response resource: %v", err)
					}
				}
			}
		} else {
			l.logger.Debug("Request %v not allowed", requestID)
		}
	}
}

// handleResponse is the Go port of Python Link.handle_response
// (Link.py:857-883). It locates the pending request matching requestID,
// optionally records the response/transfer sizes, optionally enforces the
// per-receipt max response size, then either completes the receipt
// (responseReceived) or rejects it (ResponseRejected). The receipt is always
// removed from pendingRequests once matched.
//
// Mirroring Python: size_ok is True when checkSize is false or the receipt has
// no limit (maxResponseSize == 0, Python None); otherwise
// response_size <= max_response_size. When updateSizes is true the receipt's
// response size is set and transfer size accumulated (Link.py:867-870); this
// is the response-data (inline ContextResponse) path. The resource-conclude
// path passes updateSizes=false so it does not accumulate — the resource
// path's sizes are recorded at advertisement-accept time instead
// (recordResponseResourceSize).
//
// Locking: the receipt is located and removed under l.mu, then l.mu is
// released before invoking responseReceived/ResponseRejected. ResponseRejected
// calls back into removePendingRequest (which locks l.mu), so calling it
// under l.mu would deadlock; the up-front removal also makes that call a
// no-op.
func (l *Link) handleResponse(requestID []byte, responseData any, metadata any, responseSize, responseTransferSize int64, checkSize, updateSizes bool) {
	l.logger.Verbose("handleResponse called: requestID=%x, responseData=%v (type: %T)", requestID, responseData, responseData)
	l.mu.Lock()

	if l.status.Load() != LinkActive {
		l.mu.Unlock()
		l.logger.Verbose("handleResponse: link not active, status=%v", l.status.Load())
		return
	}

	var found *RequestReceipt
	idx := -1
	for i, rr := range l.pendingRequests {
		if bytes.Equal(rr.RequestID, requestID) {
			found = rr
			idx = i
			break
		}
	}
	if found == nil {
		l.mu.Unlock()
		l.logger.Verbose("handleResponse: no pending request for requestID=%x", requestID)
		return
	}
	// Remove the receipt from the pending list now, under the lock. Python
	// removes `remove` after the callback; we remove first so the success/
	// failure callbacks never observe a stale pending entry, and so
	// ResponseRejected's removePendingRequest is a no-op (avoiding a
	// double-remove and, critically, a self-deadlock on l.mu).
	l.pendingRequests = append(l.pendingRequests[:idx], l.pendingRequests[idx+1:]...)
	maxResponseSize := found.MaxResponseSize()
	l.mu.Unlock()

	// Record sizes before the size gate, mirroring Python (Link.py:867-870):
	// the response-data path accumulates regardless of whether the response
	// is accepted, so an oversized inline response still records its size
	// before being rejected.
	if updateSizes {
		found.recordResponseSize(responseSize, responseTransferSize)
	}

	sizeOK := !checkSize || maxResponseSize == 0 || responseSize <= maxResponseSize
	if sizeOK {
		l.logger.Verbose("handleResponse: found pending request, calling responseReceived")
		found.responseReceived(responseData, metadata)
	} else {
		l.logger.Debug("Rejected response with excessive size %v on %v", responseSize, l)
		found.ResponseRejected()
	}
	l.logger.Verbose("handleResponse: done")
}

func (l *Link) removePendingRequest(rr *RequestReceipt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, pending := range l.pendingRequests {
		if pending == rr {
			l.pendingRequests = append(l.pendingRequests[:i], l.pendingRequests[i+1:]...)
			return
		}
	}
}

func (l *Link) responseResourceConcluded(resource *Resource) {
	if resource.status == ResourceStatusComplete {
		unpackedResponse, err := unpackRequestResponseData(resource.data)
		if err != nil {
			l.logger.Error("Failed to unpack response resource: %v", err)
			return
		}

		resList, ok := unpackedResponse.([]any)
		if !ok || len(resList) < 2 {
			l.logger.Error("Unexpected response resource shape: %T", unpackedResponse)
			return
		}

		requestID, ok := resList[0].([]byte)
		if !ok {
			l.logger.Error("Unexpected response resource request ID type: %T", resList[0])
			return
		}

		responseData := resList[1]
		// The response-size gate already ran when the resource advertisement
		// was accepted (ContextResourceAdv IsResponse branch), so the
		// concluded resource is delivered without re-checking — Python passes
		// resource.total_size/size with check_size=False here
		// (Link.py:903,912). The response/transfer sizes were already
		// recorded at advertisement-accept time (recordResponseResourceSize),
		// so the conclude path passes updateSizes=False to avoid
		// double-accumulation — mirroring Python's update_sizes=False default
		// for response_resource_concluded.
		l.handleResponse(requestID, responseData, resource.Metadata(), 0, 0, false, false)
		return
	}

	// The response resource failed (watchdog part-timeout, exhausted
	// retries, or a bad completion proof) — the reply will never arrive via
	// this resource. Fail the matching pending receipt so the caller's
	// failedCallback fires and the receipt is dropped from pendingRequests.
	// Without this branch a failed response resource is silently ignored:
	// responseTimeoutJob disarmed itself when the first part arrived (status
	// flipped to RequestReceiving), so nothing fires failedCallback and the
	// receipt leaks until the caller's own backstop. This mirrors
	// RequestReceipt.requestResourceConcluded's failure branch (the
	// outgoing-request-as-resource path). requestTimedOut is
	// terminal-guarded, so a race with the deadline still resolves to one
	// callback.
	l.failPendingResponseRequest(resource.requestID)
}

// failPendingResponseRequest resolves the pending request receipt that was
// waiting on a response resource which has now failed. It looks the receipt up
// by request ID under the link lock, then fails it outside the lock (keeping
// lock order rr.mu-then-l.mu). No-op if the receipt already resolved — e.g.
// the response-timeout fired first — or if the resource ID matches nothing.
func (l *Link) failPendingResponseRequest(requestID []byte) {
	if len(requestID) == 0 {
		return
	}
	l.mu.Lock()
	var found *RequestReceipt
	for _, rr := range l.pendingRequests {
		if bytes.Equal(rr.RequestID, requestID) {
			found = rr
			break
		}
	}
	l.mu.Unlock()
	if found != nil {
		found.requestTimedOut()
	}
}

func (l *Link) requestResourceConcluded(resource *Resource) {
	if resource.status == ResourceStatusComplete {
		unpackedRequest, err := unpackRequestResponseData(resource.data)
		if err != nil {
			l.logger.Error("Failed to unpack request resource: %v", err)
			return
		}

		requestList, ok := unpackedRequest.([]any)
		if !ok {
			l.logger.Error("Unexpected request resource shape: %T", unpackedRequest)
			return
		}

		requestID := TruncatedHash(resource.data)
		go l.handleRequest(requestID, requestList)
	}
}

func unpackRequestResponseData(data []byte) (any, error) {
	return msgpack.UnpackPreserveBinMapKeys(data)
}

// packedResponseSize returns the on-wire size of an unpacked response value,
// mirroring Python's `transfer_size = len(umsgpack.packb(response_data))-2`
// (Link.py:1009). The -2 strips the msgpack array-wrapper overhead so the
// size reflects the response payload's serialized footprint, matching what
// Python compares against max_response_size. A packing failure or an
// underflow from the -2 clamp yields 0 rather than a negative size.
func packedResponseSize(responseData any) int64 {
	packed, err := msgpack.Pack(responseData)
	if err != nil || len(packed) < 2 {
		return 0
	}
	return int64(len(packed) - 2)
}

// NoInboundFor returns how long it has been since the link last
// received a packet from the remote peer. It is the Go port of
// Python's Link.no_inbound_for().
func (l *Link) NoInboundFor() time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	last := l.lastInbound
	l.mu.Unlock()
	return l.nowTime().Sub(last)
}

// NoOutboundFor returns how long it has been since the link last
// sent a packet to the remote peer. It is the Go port of Python's
// Link.no_outbound_for().
func (l *Link) NoOutboundFor() time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	last := l.lastOutbound
	l.mu.Unlock()
	return l.nowTime().Sub(last)
}

// NoDataFor returns the time in seconds since payload data (excluding
// keepalive packets) traversed the link. It is the Go port of Python's
// Link.no_data_for(), which is `time.time() - self.last_data`. last_data
// is advanced only by non-keepalive inbound and outbound packets.
func (l *Link) NoDataFor() time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	last := l.lastData
	l.mu.Unlock()
	return l.nowTime().Sub(last)
}

// InactiveFor returns the time in seconds since any activity (including
// keepalive packets) on the link. It is the Go port of Python's
// Link.inactive_for(), which is min(no_inbound_for, no_outbound_for).
func (l *Link) InactiveFor() time.Duration {
	in := l.NoInboundFor()
	out := l.NoOutboundFor()
	if in < out {
		return in
	}
	return out
}

// nowTime returns the current time, honoring the optional test
// clock. If no clock has been installed, it falls back to time.Now.
func (l *Link) nowTime() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// TrackPHYStats enables or disables the link-level physical-layer
// statistics (RSSI, SNR, link quality). It is the Go port of
// Python's Link.track_phy_stats().
func (l *Link) TrackPHYStats(track bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.trackPHYStats = track
}

// GetRSSI returns the link's last-recorded RSSI in dBm, or nil if
// PHY stats tracking is disabled. It is the Go port of Python's
// Link.get_rssi().
func (l *Link) GetRSSI() *float64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.trackPHYStats {
		return nil
	}
	v := l.rssi
	return &v
}

// GetSNR returns the link's last-recorded SNR in dB, or nil if
// PHY stats tracking is disabled. It is the Go port of Python's
// Link.get_snr().
func (l *Link) GetSNR() *float64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.trackPHYStats {
		return nil
	}
	v := l.snr
	return &v
}

// GetQ returns the link's last-recorded link quality, or nil if
// PHY stats tracking is disabled. It is the Go port of Python's
// Link.get_q().
func (l *Link) GetQ() *float64 {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.trackPHYStats {
		return nil
	}
	v := l.q
	return &v
}

// RegisterOutgoingResource adds the resource to the link's outgoing
// resources list. It is the Go port of Python's
// Link.register_outgoing_resource().
func (l *Link) RegisterOutgoingResource(r *Resource) {
	if l == nil || r == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.outgoingResources = append(l.outgoingResources, r)
}

// RegisterIncomingResource adds the resource to the link's incoming
// resources list. It is the Go port of Python's
// Link.register_incoming_resource().
func (l *Link) RegisterIncomingResource(r *Resource) {
	if l == nil || r == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.incomingResources = append(l.incomingResources, r)
}

// HasIncomingResource reports whether the link currently has the
// given resource registered as incoming. It is the Go port of
// Python's Link.has_incoming_resource().
func (l *Link) HasIncomingResource(r *Resource) bool {
	if l == nil || r == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Contains(l.incomingResources, r)
}

// CancelOutgoingResource removes the resource from the link's
// outgoing resources list. It is the Go port of Python's
// Link.cancel_outgoing_resource().
func (l *Link) CancelOutgoingResource(r *Resource) {
	if l == nil || r == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, existing := range l.outgoingResources {
		if existing == r {
			l.outgoingResources = append(l.outgoingResources[:i], l.outgoingResources[i+1:]...)
			return
		}
	}
}

// CancelIncomingResource removes the resource from the link's
// incoming resources list. It is the Go port of Python's
// Link.cancel_incoming_resource().
func (l *Link) CancelIncomingResource(r *Resource) {
	if l == nil || r == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, existing := range l.incomingResources {
		if existing == r {
			l.incomingResources = append(l.incomingResources[:i], l.incomingResources[i+1:]...)
			return
		}
	}
}

// ReadyForNewResource reports whether the link is ready to accept
// new incoming resources. It is the Go port of Python's
// Link.ready_for_new_resource().
func (l *Link) ReadyForNewResource() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.outgoingResources) == 0
}

// SendPacket sends a pre-packed packet through the link's attached interface,
// falling back to the transport's outbound path if no attached interface exists.
// This is the exported equivalent of the internal send method, enabling
// applications to send raw data packets through an established link.
func (l *Link) SendPacket(p *Packet) error {
	return l.send(p)
}
