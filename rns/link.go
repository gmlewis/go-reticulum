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
	"sync"
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
	// AcceptNone strictly denies all incoming resource advertisements on the link.
	AcceptNone = 0x00
	// AcceptApp defers the decision to accept a resource advertisement to an application-provided callback.
	AcceptApp = 0x01
	// AcceptAll blindly accepts all incoming resource advertisements on the link.
	AcceptAll = 0x02
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
	destination *Destination
	initiator   bool
	status      int
	mode        int

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

	lastInbound  time.Time
	lastOutbound time.Time
	activatedAt  time.Time
	requestTime  time.Time

	callbacks LinkCallbacks
	mu        sync.Mutex

	remoteIdentity *Identity

	establishmentTimeout time.Duration
	attachedInterface    interfaces.Interface
	transport            Transport

	resourceStrategy     int
	outgoingResources    []*Resource
	incomingResources    []*Resource
	pendingRequests      []*RequestReceipt
	trafficTimeoutFactor float64
	channel              *Channel
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
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

// UpdateMDU proactively recalculates the Maximum Data Unit payload size based on the current MTU and header overhead.
func (l *Link) UpdateMDU() {
	// Simple calculation for now
	l.mdu = l.mtu - HeaderMaxSize - IFACMinSize
}

// GetHash returns the truncated cryptographic hash identifying this link.
func (l *Link) GetHash() []byte {
	return l.linkID
}

// GetType returns the destination type for a link.
func (l *Link) GetType() int {
	return DestinationLink
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
		destination:          destination,
		initiator:            true,
		status:               LinkPending,
		mode:                 LinkModeAES256CBC,
		mtu:                  MTU,
		transport:            ts,
		trafficTimeoutFactor: 6.0,
	}
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
		// In a real implementation, we'd calculate timeout based on hops
		l.establishmentTimeout = 5 * time.Second
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

	Logf("Establishing link to %v", LogVerbose, false, l.destination.name)

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

	// Register with Transport
	if l.transport != nil {
		l.transport.RegisterLink(l)
	}

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
func ValidateRequest(destination *Destination, data []byte, packet *Packet) (*Link, error) {
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

	if err := l.handshake(); err != nil {
		return nil, err
	}

	// Register link
	if l.transport != nil {
		l.transport.RegisterLink(l)
	}

	Logf("Incoming link request %x accepted", LogVerbose, false, l.linkID)

	// Send proof
	if err := l.Prove(); err != nil {
		return nil, err
	}

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
	l.mu.Unlock()

	if packet.Context == ContextLrproof {
		if err := l.ValidateProof(packet); err != nil {
			Logf("Failed to validate link proof: %v", LogDebug, false, err)
		}
		return
	}

	if packet.Context == ContextLrrtt {
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
		plaintext, err := l.Decrypt(packet.Data)
		if err != nil {
			Logf("Failed to decrypt packet for link %x: %v", LogDebug, false, l.linkID, err)
			return
		}
		packet.Data = plaintext
	}
	Logf("Link %x received packet: type=%v, context=%x, size=%v", LogDebug, false, l.linkID, packet.PacketType, packet.Context, len(packet.Data))

	switch packet.Context {
	case ContextResourceAdv:
		packet.Destination = l
		adv, err := UnpackResourceAdvertisement(packet.Data)
		if err != nil {
			Logf("Failed to unpack resource advertisement: %v", LogDebug, false, err)
			return
		}

		if adv.IsRequest {
			if _, err := Accept(packet, l.requestResourceConcluded, l.callbacks.ResourceStarted, nil); err != nil {
				Logf("Failed to accept request resource advertisement: %v", LogDebug, false, err)
			}
			return
		}

		if adv.IsResponse {
			var progressCB func(*Resource)
			l.mu.Lock()
			for _, rr := range l.pendingRequests {
				if bytes.Equal(rr.RequestID, adv.Q) {
					progressCB = rr.responseResourceProgress
					break
				}
			}
			l.mu.Unlock()
			if _, err := Accept(packet, l.responseResourceConcluded, l.callbacks.ResourceStarted, progressCB); err != nil {
				Logf("Failed to accept response resource advertisement: %v", LogDebug, false, err)
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
				Logf("Failed to accept resource advertisement: %v", LogDebug, false, err)
			}
		} else {
			if err := Reject(packet); err != nil {
				Logf("Failed to reject resource advertisement: %v", LogDebug, false, err)
			}
		}

	case ContextRequest:
		requestID := packet.GetTruncatedHash()
		unpackedRequest, err := msgpack.Unpack(packet.Data)
		if err != nil {
			Logf("Failed to unpack request: %v", LogError, false, err)
			return
		}
		go l.handleRequest(requestID, unpackedRequest.([]any))

	case ContextResponse:
		unpackedResponse, err := msgpack.Unpack(packet.Data)
		if err != nil {
			Logf("Failed to unpack response: %v", LogError, false, err)
			return
		}
		resList := unpackedResponse.([]any)
		requestID := resList[0].([]byte)
		responseData := resList[1]
		l.handleResponse(requestID, responseData, nil)

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
						Logf("Failed to handle resource request: %v", LogDebug, false, err)
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
					Logf("Failed receiving resource part: %v", LogDebug, false, err)
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

				id, err := NewIdentity(false)
				if err == nil {
					if err := id.LoadPublicKey(publicKey); err == nil {
						if id.Verify(signature, signedData) {
							l.mu.Lock()
							l.remoteIdentity = id
							l.mu.Unlock()
							if l.callbacks.RemoteIdentified != nil {
								go l.callbacks.RemoteIdentified(l, id)
							}
						}
					}
				}
			}
		}

	case ContextKeepalive:
		if !l.initiator && len(packet.Data) > 0 && packet.Data[0] == 0xFF {
			keepalivePacket := NewPacketWithTransport(l.transport, l, []byte{0xFE})
			keepalivePacket.Context = ContextKeepalive
			if err := l.send(keepalivePacket); err != nil {
				Logf("Failed sending keepalive response: %v", LogDebug, false, err)
			}
		}

	case ContextLinkClose:
		l.teardown(LinkClosed)

	case ContextChannel:
		l.mu.Lock()
		if l.channel != nil {
			l.channel.Receive(packet.Data)
		}
		l.mu.Unlock()

	default:
		l.mu.Lock()
		cb := l.callbacks.Packet
		l.mu.Unlock()
		if cb != nil {
			cb(l, packet)
		}
	}
}

func (l *Link) send(p *Packet) error {
	l.mu.Lock()
	l.lastOutbound = time.Now()
	l.mu.Unlock()
	return p.Send()
}

// ValidateProof evaluates an incoming link proof packet and formally transitions the link into an active state upon success.
func (l *Link) ValidateProof(packet *Packet) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.status != LinkPending {
		return errors.New("link is not in pending state")
	}

	// data = signature (64) + peerPubBytes (32) + signalling_bytes (optional, 3)
	if len(packet.Data) < 64+32 {
		return errors.New("invalid proof data length")
	}

	signature := packet.Data[:64]
	peerPubBytes := packet.Data[64:96]
	var sigBytes []byte
	if len(packet.Data) == 64+32+LinkMTUSize {
		sigBytes = packet.Data[96 : 96+LinkMTUSize]
	}

	// Receiver sig pub is in destination identity
	peerSigPubBytes := l.destination.identity.GetPublicKey()[32:64]

	if err := l.LoadPeer(peerPubBytes, peerSigPubBytes); err != nil {
		return err
	}

	if err := l.handshake(); err != nil {
		return err
	}

	signedData := make([]byte, 0, len(l.linkID)+len(l.peerPubBytes)+len(l.peerSigPubBytes)+len(sigBytes))
	signedData = append(signedData, l.linkID...)
	signedData = append(signedData, l.peerPubBytes...)
	signedData = append(signedData, l.peerSigPubBytes...)
	signedData = append(signedData, sigBytes...)

	if !l.destination.identity.Verify(signature, signedData) {
		return errors.New("invalid link proof signature")
	}

	l.status = LinkActive
	l.activatedAt = time.Now()
	l.rtt = time.Since(l.requestTime).Seconds()

	if l.transport != nil {
		l.transport.ActivateLink(l)
	}

	Logf("Link %x active, RTT is %v", LogVerbose, false, l.linkID, time.Duration(l.rtt*float64(time.Second)))
	// Send RTT packet with msgpack-packed RTT value
	rttData, err := msgpack.Pack(l.rtt)
	if err != nil {
		return fmt.Errorf("packing RTT data: %w", err)
	}
	rttPacket := NewPacketWithTransport(l.transport, l, rttData)
	rttPacket.PacketType = PacketData
	rttPacket.Context = ContextLrrtt
	if err := rttPacket.Send(); err != nil {
		return fmt.Errorf("sending RTT packet: %w", err)
	}

	if l.callbacks.LinkEstablished != nil {
		go l.callbacks.LinkEstablished(l)
	}

	return nil
}

// HandleRTT processes an incoming Round Trip Time packet to finalize activation for non-initiator link instances.
func (l *Link) HandleRTT(packet *Packet) {
	Logf("Handling RTT for %x", LogExtreme, false, l.linkID)
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.status == LinkHandshake || l.status == LinkPending {
		l.status = LinkActive
		l.activatedAt = time.Now()
		if l.transport != nil {
			l.transport.ActivateLink(l)
		}
		Logf("Link %x active after RTT", LogVerbose, false, l.linkID)
		if l.callbacks.LinkEstablished != nil {
			go l.callbacks.LinkEstablished(l)
		}
	}
}

// Handshake triggers the underlying Diffie-Hellman cryptographic exchange, deriving secure symmetric session keys.
func (l *Link) Handshake() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.handshake()
}

func (l *Link) handshake() error {
	if l.status != LinkPending && l.status != LinkHandshake {
		return fmt.Errorf("invalid link state for handshake: %v", l.status)
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

	l.status = LinkHandshake
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
	if !l.initiator || l.status != LinkActive {
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
	p := NewPacketWithTransport(o.link.transport, o.link, raw)
	p.Context = ContextChannel
	if err := o.link.send(p); err != nil {
		return nil, err
	}
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

// RTT exposes the current measured Round Trip Time from the underlying link strictly to aid the channel's retry metrics.
func (o *LinkChannelOutlet) RTT() float64 {
	return o.link.rtt
}

// IsUsable safely reports whether the physical link remains in an active state capable of sustaining channel traffic.
func (o *LinkChannelOutlet) IsUsable() bool {
	return o.link.status == LinkActive
}

// Teardown actively closes the link, destroying related channels, and notifying any observers that data transmission has halted.
func (l *Link) Teardown() {
	l.teardown(LinkClosed)
}

func (l *Link) teardown(reason int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.status == LinkClosed {
		return
	}

	l.status = LinkClosed
	if l.channel != nil {
		l.channel.Shutdown()
	}

	if l.callbacks.LinkClosed != nil {
		go l.callbacks.LinkClosed(l)
	}

	Logf("Link %x closed: reason=%v", LogVerbose, false, l.linkID, reason)
}

// Request fires a generalized structured request packet asynchronously, expecting a correlated logical response from the remote peer.
func (l *Link) Request(path string, data any, responseCallback, failedCallback, progressCallback func(*RequestReceipt), timeout time.Duration) (*RequestReceipt, error) {
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
		Logf("Sending request %v for %v over link %x", LogDebug, false, TruncatedHash(packedRequest), path, l.linkID)
		p := NewPacketWithTransport(l.transport, l, packedRequest)
		p.Context = ContextRequest

		if err := p.Pack(); err != nil {
			return nil, err
		}

		if err := l.send(p); err != nil {
			return nil, err
		}

		rr := &RequestReceipt{
			Link:             l,
			RequestID:        p.GetTruncatedHash(), // Match Reticulum behavior
			Status:           RequestSent,
			SentAt:           time.Now(),
			Timeout:          timeout,
			callback:         responseCallback,
			failedCallback:   failedCallback,
			progressCallback: progressCallback,
		}

		l.mu.Lock()
		l.pendingRequests = append(l.pendingRequests, rr)
		l.mu.Unlock()

		return rr, nil
	} else {
		requestID := TruncatedHash(packedRequest)
		Logf("Sending request %x as resource.", LogDebug, false, requestID)

		// request_resource = RNS.Resource(packed_request, self, request_id = request_id, is_response = False, timeout = timeout)
		r, err := NewResource(packedRequest, l)
		if err != nil {
			return nil, err
		}
		r.requestID = requestID
		r.isResponse = false

		rr := &RequestReceipt{
			Link:             l,
			RequestID:        requestID,
			Resource:         r,
			Status:           RequestSent,
			SentAt:           time.Now(),
			Timeout:          timeout,
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
	if l.status != LinkActive {
		return
	}

	if len(unpackedRequest) < 3 {
		Log("Received malformed request packet, ignoring", LogDebug, false)
		return
	}

	requestedAt := time.Unix(0, int64(unpackedRequest[0].(float64)*1e9))
	pathHash, ok1 := unpackedRequest[1].([]byte)
	requestData, ok2 := unpackedRequest[2].([]byte)
	if !ok1 {
		Log("Received malformed request packet (bad path hash), ignoring", LogDebug, false)
		return
	}
	// requestData can be nil
	if unpackedRequest[2] == nil {
		requestData = nil
		ok2 = true
	}
	if !ok2 {
		Log("Received malformed request packet (bad request data), ignoring", LogDebug, false)
		return
	}

	l.mu.Lock()
	handler, ok := l.destination.requestHandlers[string(pathHash)]
	l.mu.Unlock()

	Logf("Request handler lookup: pathHash=%x, ok=%v, handler.Path=%v", LogDebug, false, pathHash, ok, handler.Path)
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

		Logf("Request allowed check: allowed=%v, handler.Allow=%v", LogDebug, false, allowed, handler.Allow)
		if allowed {
			Logf("Handling request %v for %v", LogDebug, false, requestID, handler.Path)
			response := handler.ResponseGenerator(handler.Path, requestData, requestID, l.linkID, l.remoteIdentity, requestedAt)
			Logf("Handler response: %v (type: %T)", LogDebug, false, response, response)

			if response != nil {
				packedResponse, err := msgpack.Pack([]any{requestID, response})
				if err != nil {
					Logf("Failed to pack response: %v", LogError, false, err)
					return
				}

				if len(packedResponse) <= l.mdu {
					p := NewPacketWithTransport(l.transport, l, packedResponse)
					p.Context = ContextResponse
					if err := l.send(p); err != nil {
						Logf("Failed to send response packet: %v", LogError, false, err)
					}
				} else {
					// Send as resource
					r, err := NewResourceWithOptions(packedResponse, l, ResourceOptions{
						AutoCompress:      handler.AutoCompress,
						AutoCompressLimit: handler.AutoCompressLimit,
					})
					if err != nil {
						Logf("Failed to create response resource: %v", LogError, false, err)
						return
					}
					r.requestID = requestID
					r.isResponse = true
					if err := r.Advertise(); err != nil {
						Logf("Failed to advertise response resource: %v", LogError, false, err)
					}
				}
			}
		} else {
			Logf("Request %v not allowed", LogDebug, false, requestID)
		}
	}
}

func (l *Link) handleResponse(requestID []byte, responseData any, metadata any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.status != LinkActive {
		return
	}

	for i, rr := range l.pendingRequests {
		if bytes.Equal(rr.RequestID, requestID) {
			// Found it
			rr.responseReceived(responseData, metadata)
			// Remove from pending
			l.pendingRequests = append(l.pendingRequests[:i], l.pendingRequests[i+1:]...)
			break
		}
	}
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
		unpackedResponse, err := msgpack.Unpack(resource.data)
		if err != nil {
			Logf("Failed to unpack response resource: %v", LogError, false, err)
			return
		}

		resList, ok := unpackedResponse.([]any)
		if !ok || len(resList) < 2 {
			Logf("Unexpected response resource shape: %T", LogError, false, unpackedResponse)
			return
		}

		requestID, ok := resList[0].([]byte)
		if !ok {
			Logf("Unexpected response resource request ID type: %T", LogError, false, resList[0])
			return
		}

		responseData := resList[1]
		l.handleResponse(requestID, responseData, nil)
	}
}

func (l *Link) requestResourceConcluded(resource *Resource) {
	if resource.status == ResourceStatusComplete {
		unpackedRequest, err := msgpack.Unpack(resource.data)
		if err != nil {
			Logf("Failed to unpack request resource: %v", LogError, false, err)
			return
		}

		requestList, ok := unpackedRequest.([]any)
		if !ok {
			Logf("Unexpected request resource shape: %T", LogError, false, unpackedRequest)
			return
		}

		requestID := TruncatedHash(resource.data)
		go l.handleRequest(requestID, requestList)
	}
}
