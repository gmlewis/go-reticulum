// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

const (
	// PacketData represents a standard data packet containing user or application payload.
	PacketData = 0x00
	// PacketAnnounce represents an announce packet used for identity and destination discovery.
	PacketAnnounce = 0x01
	// PacketLinkRequest represents a request to establish a reliable cryptographic link.
	PacketLinkRequest = 0x02
	// PacketProof represents a cryptographic proof packet acknowledging receipt of data.
	PacketProof = 0x03
)

const (
	// Header1 represents a standard single-hop or direct header type without a transport ID.
	Header1 = 0x00
	// Header2 represents a multi-hop or routed header type that requires a transport ID.
	Header2 = 0x01
)

const (
	// TransportBroadcast is used for packets broadcast to all reachable interfaces and nodes.
	TransportBroadcast = 0x00
	// TransportForward is used for packets being forwarded directly towards a specific destination.
	TransportForward = 0x01
	// TransportRelay is used for packets being relayed through intermediate helper nodes.
	TransportRelay = 0x02
	// TransportTunnel is used for packets securely encapsulated within a tunnel.
	TransportTunnel = 0x03
)

const (
	// ContextNone signifies that a packet carries no special context.
	ContextNone = 0x00
	// ContextResource identifies a packet as carrying resource data chunks.
	ContextResource = 0x01
	// ContextResourceAdv advertises the availability of a specific resource.
	ContextResourceAdv = 0x02
	// ContextResourceReq requests a previously advertised resource.
	ContextResourceReq = 0x03
	// ContextResourceHmu provides hashmap updates for resuming resource transfers.
	ContextResourceHmu = 0x04
	// ContextResourcePrf proves receipt of a complete resource transmission.
	ContextResourcePrf = 0x05
	// ContextResourceIcl initiates the cancellation of a resource transfer.
	ContextResourceIcl = 0x06
	// ContextResourceRcl confirms the cancellation of a resource transfer.
	ContextResourceRcl = 0x07
	// ContextCacheRequest requests cached packets from nearby nodes.
	ContextCacheRequest = 0x08
	// ContextRequest signifies an RPC or general inquiry request packet.
	ContextRequest = 0x09
	// ContextResponse signifies a response to a prior context request.
	ContextResponse = 0x0a
	// ContextPathResponse provides information about a discovered path through the network.
	ContextPathResponse = 0x0b
	// ContextCommand represents an administrative or operational command packet.
	ContextCommand = 0x0c
	// ContextCommandStatus relays the status or result of an executed command.
	ContextCommandStatus = 0x0d
	// ContextChannel identifies a packet related to symmetric messaging channels.
	ContextChannel = 0x0e
	// ContextKeepalive sends a small payload to keep a network link active.
	ContextKeepalive = 0xfa
	// ContextLinkIdentify identifies a node's full identity over an established link.
	ContextLinkIdentify = 0xfb
	// ContextLinkClose gracefully shuts down an active cryptographic link.
	ContextLinkClose = 0xfc
	// ContextLinkProof provides cryptographic proof for packets transmitted over a link.
	ContextLinkProof = 0xfd
	// ContextLrrtt is used to measure link request round-trip time.
	ContextLrrtt = 0xfe
	// ContextLrproof acknowledges receipt of a link request proof.
	ContextLrproof = 0xff
)

const (
	// FlagSet indicates that a specific contextual packet flag is active.
	FlagSet = 0x01
	// FlagUnset indicates that a specific contextual packet flag is inactive.
	FlagUnset = 0x00
)

// PacketDestination defines the operational contract for any entity that can
// serve as a network destination, including Destinations and Links.
type PacketDestination interface {
	GetHash() []byte
	GetType() int
	GetTransport() Transport
	Encrypt([]byte) ([]byte, error)
	Decrypt([]byte) ([]byte, error)
	Sign([]byte) ([]byte, error)
	Verify([]byte, []byte) bool
}

// Packet represents a Reticulum network packet.
type Packet struct {
	Hops          int
	HeaderType    int
	PacketType    int
	TransportType int
	Context       int
	ContextFlag   int
	Destination   PacketDestination
	TransportID   []byte
	Data          []byte
	Flags         byte
	Raw           []byte
	Packed        bool
	Sent          bool
	CreateReceipt bool
	Receipt       *PacketReceipt
	FromPacked    bool
	MTU           int
	SentAt        float64
	PacketHash    []byte
	RatchetID     []byte

	DestinationHash []byte
	DestinationType int
	Ciphertext      []byte

	ReceivingInterface interfaces.Interface
	AttachedInterface  interfaces.Interface

	// Optional fields for received packets
	RSSI *float64
	SNR  *float64
	Q    *float64

	transport Transport
}

// NewPacket creates a new packet for the given destination and data.
// It uses the transport defined by the destination.
func NewPacket(destination PacketDestination, data []byte) *Packet {
	ts := destination.GetTransport()
	return NewPacketWithTransport(ts, destination, data)
}

// NewPacketWithTransport creates a new Reticulum packet with a specific transport system.
func NewPacketWithTransport(ts Transport, destination PacketDestination, data []byte) *Packet {
	p := &Packet{
		Destination:   destination,
		Data:          data,
		PacketType:    PacketData,
		Context:       ContextNone,
		TransportType: 0, // Broadcast by default
		HeaderType:    Header1,
		CreateReceipt: true,
		ContextFlag:   FlagUnset,
		MTU:           MTU,
		transport:     ts,
	}
	if destination != nil {
		p.DestinationType = destination.GetType()
		// Set ratchet ID if destination is a Destination and has one
		if d, ok := destination.(*Destination); ok {
			if d.latestRatchetID != nil {
				p.RatchetID = d.latestRatchetID
			}
		}
		p.Flags = p.calculatePackedFlags()
	}
	return p
}

// NewPacketFromRaw creates a packet from raw bytes (usually received from an interface).
func NewPacketFromRaw(data []byte) *Packet {
	return &Packet{
		Raw:           data,
		Packed:        true,
		FromPacked:    true,
		CreateReceipt: false,
	}
}

func (p *Packet) calculatePackedFlags() byte {
	if p.Context == ContextLrproof {
		return byte((p.HeaderType << 6) | (p.ContextFlag << 5) | (p.TransportType << 4) | (DestinationLink << 2) | p.PacketType)
	}
	destType := DestinationPlain
	if p.Destination != nil {
		destType = p.Destination.GetType()
	}
	return byte((p.HeaderType << 6) | (p.ContextFlag << 5) | (p.TransportType << 4) | (destType << 2) | p.PacketType)
}

// Pack prepares the packet for transmission.
func (p *Packet) Pack() error {
	if p.Destination == nil {
		return errors.New("cannot pack packet without destination")
	}

	p.DestinationHash = p.Destination.GetHash()
	p.Flags = p.calculatePackedFlags()

	header := make([]byte, 0, 32)
	header = append(header, p.Flags)
	header = append(header, byte(p.Hops))

	switch p.HeaderType {
	case Header1:
		header = append(header, p.DestinationHash...)

		// Determine if encryption is needed
		shouldEncrypt := true
		if p.PacketType == PacketAnnounce ||
			p.PacketType == PacketLinkRequest ||
			(p.PacketType == PacketProof && p.Context == ContextResourcePrf) ||
			(p.PacketType == PacketProof && p.DestinationType == DestinationLink) ||
			p.Context == ContextResource ||
			p.Context == ContextKeepalive ||
			p.Context == ContextCacheRequest ||
			p.Context == ContextLrproof { // LRPROOF is not encrypted
			shouldEncrypt = false
		}

		if shouldEncrypt {
			var err error
			// If destination is a Link, use Link.Encrypt
			if l, ok := p.Destination.(*Link); ok {
				p.Ciphertext, err = l.Encrypt(p.Data)
			} else if d, ok := p.Destination.(*Destination); ok {
				p.Ciphertext, err = d.Encrypt(p.Data)
			} else {
				return errors.New("unsupported destination type for encryption")
			}

			if err != nil {
				return err
			}
		} else {
			p.Ciphertext = p.Data
		}
	case Header2:
		if len(p.TransportID) == 0 {
			return errors.New("packet with header type 2 must have a transport ID")
		}
		header = append(header, p.TransportID...)
		header = append(header, p.Destination.GetHash()...)
		p.Ciphertext = p.Data
	}

	header = append(header, byte(p.Context))
	p.Raw = append(header, p.Ciphertext...)

	if len(p.Raw) > p.MTU {
		return fmt.Errorf("packet size %v exceeds MTU %v", len(p.Raw), p.MTU)
	}

	p.Packed = true
	p.UpdateHash()
	return nil
}

// Send sends the packet.
func (p *Packet) Send() error {
	if p.Sent {
		return errors.New("packet was already sent")
	}
	if !p.Packed {
		if err := p.Pack(); err != nil {
			return err
		}
	}
	if p.CreateReceipt && p.Receipt == nil {
		p.Receipt = &PacketReceipt{
			Hash:          append([]byte(nil), p.PacketHash...),
			TruncatedHash: append([]byte(nil), p.GetTruncatedHash()...),
			Status:        ReceiptSent,
			Destination:   p.Destination,
		}
	}
	if p.transport != nil {
		return p.transport.Outbound(p)
	}
	if p.Destination != nil {
		ts := p.Destination.GetTransport()
		if ts != nil {
			return ts.Outbound(p)
		}
	}
	return errors.New("unknown transport for packet")
}

// Unpack parses a raw packet.
func (p *Packet) Unpack() error {
	if len(p.Raw) < 3 {
		return errors.New("packet too short")
	}

	p.Flags = p.Raw[0]
	p.Hops = int(p.Raw[1])

	p.HeaderType = int((p.Flags & 0b01000000) >> 6)
	p.ContextFlag = int((p.Flags & 0b00100000) >> 5)
	p.TransportType = int((p.Flags & 0b00010000) >> 4)
	p.DestinationType = int((p.Flags & 0b00001100) >> 2)
	p.PacketType = int(p.Flags & 0b00000011)

	dstLen := TruncatedHashLength / 8

	if p.HeaderType == Header2 {
		if len(p.Raw) < 2*dstLen+3 {
			return errors.New("packet too short for Header2")
		}
		p.TransportID = p.Raw[2 : dstLen+2]
		p.DestinationHash = p.Raw[dstLen+2 : 2*dstLen+2]
		p.Context = int(p.Raw[2*dstLen+2])
		p.Data = p.Raw[2*dstLen+3:]
	} else {
		if len(p.Raw) < dstLen+3 {
			return errors.New("packet too short for Header1")
		}
		p.TransportID = nil
		p.DestinationHash = p.Raw[2 : dstLen+2]
		p.Context = int(p.Raw[dstLen+2])
		p.Data = p.Raw[dstLen+3:]
	}

	p.Packed = false
	p.UpdateHash()
	return nil
}

// UpdateHash updates the packet's hash.
func (p *Packet) UpdateHash() {
	p.PacketHash = p.GetHash()
}

// GetHash returns the SHA-256 hash of the packet.
func (p *Packet) GetHash() []byte {
	return FullHash(p.GetHashablePart())
}

// GetHashablePart returns the part of the packet used for hashing.
func (p *Packet) GetHashablePart() []byte {
	// Only the lower 4 bits of the flags byte are used for hashing
	hashablePart := []byte{p.Raw[0] & 0b00001111}

	dstLen := TruncatedHashLength / 8
	if p.HeaderType == Header2 {
		hashablePart = append(hashablePart, p.Raw[dstLen+2:]...)
	} else {
		hashablePart = append(hashablePart, p.Raw[2:]...)
	}
	return hashablePart
}

// GetTruncatedHash returns the truncated SHA-256 hash of the packet.
func (p *Packet) GetTruncatedHash() []byte {
	return TruncatedHash(p.GetHashablePart())
}

type proofDestination struct {
	hash      []byte
	transport Transport
}

func (pd *proofDestination) GetHash() []byte         { return pd.hash }
func (pd *proofDestination) GetType() int            { return DestinationSingle }
func (pd *proofDestination) GetTransport() Transport { return pd.transport }
func (pd *proofDestination) Decrypt(ciphertext []byte) ([]byte, error) {
	return ciphertext, nil
}
func (pd *proofDestination) Encrypt(plaintext []byte) ([]byte, error) {
	return plaintext, nil
}
func (pd *proofDestination) Sign(data []byte) ([]byte, error) {
	return nil, errors.New("proof destination cannot sign")
}
func (pd *proofDestination) Verify(signature, data []byte) bool {
	return false
}

// Prove generates and sends a cryptographic proof for this packet.
func (p *Packet) Prove(destination PacketDestination) {
	if p.FromPacked {
		if p.Destination != nil {
			if d, ok := p.Destination.(*Destination); ok {
				identity := d.identity
				if identity != nil && identity.GetPrivateKey() != nil {
					identity.Prove(p, destination)
					return
				}
			} else if l, ok := p.Destination.(*Link); ok {
				l.ProvePacket(p)
				return
			}
		}
	}
}

// GenerateProofDestination generates a special destination that allows Reticulum
// to direct the proof back to the proved packet's sender.
func (p *Packet) GenerateProofDestination() PacketDestination {
	return &proofDestination{
		hash:      p.PacketHash[:TruncatedHashLength/8],
		transport: p.transport,
	}
}

// PacketReceipt represents a receipt for a sent packet.
type PacketReceipt struct {
	Hash          []byte
	TruncatedHash []byte
	Sent          bool
	SentAt        float64
	Timeout       float64
	Proved        bool
	Status        int
	Destination   PacketDestination
	ConcludedAt   float64
	ProofPacket   *Packet

	timeoutCallback  func(*PacketReceipt)
	deliveryCallback func(*PacketReceipt)
	mu               sync.Mutex
}

const (
	// ExplLength is the length of an explicit proof.
	ExplLength = TruncatedHashLength/8 + 64
	// ImplLength is the length of an implicit proof.
	ImplLength = 64
)

// ValidateProofPacket evaluates a raw proof packet against the receipt.
func (pr *PacketReceipt) ValidateProofPacket(proofPacket *Packet) bool {
	if l, ok := pr.Destination.(*Link); ok {
		return pr.ValidateLinkProof(proofPacket.Data, l, proofPacket)
	}
	return pr.ValidateProof(proofPacket.Data, proofPacket)
}

// ValidateLinkProof validates a raw proof for a link.
func (pr *PacketReceipt) ValidateLinkProof(proof []byte, link *Link, proofPacket *Packet) bool {
	if len(proof) == ExplLength {
		proofHash := proof[:TruncatedHashLength/8]
		signature := proof[TruncatedHashLength/8 : TruncatedHashLength/8+64]
		if bytes.Equal(proofHash, pr.TruncatedHash) {
			if link.remoteIdentity != nil && link.remoteIdentity.Verify(signature, pr.Hash) {
				pr.mu.Lock()
				pr.Status = ReceiptDelivered
				pr.Proved = true
				pr.ConcludedAt = float64(time.Now().UnixNano()) / 1e9
				pr.ProofPacket = proofPacket
				link.lastProof = pr.ConcludedAt
				cb := pr.deliveryCallback
				pr.mu.Unlock()
				if cb != nil {
					cb(pr)
				}
				return true
			}
		}
	}
	return false
}

// ValidateProof validates a raw proof for a destination.
func (pr *PacketReceipt) ValidateProof(proof []byte, proofPacket *Packet) bool {
	if len(proof) == ExplLength {
		proofHash := proof[:TruncatedHashLength/8]
		signature := proof[TruncatedHashLength/8 : TruncatedHashLength/8+64]
		if bytes.Equal(proofHash, pr.TruncatedHash) {
			// Get destination identity to verify
			var id *Identity
			if d, ok := pr.Destination.(*Destination); ok {
				id = d.identity
			}
			if id != nil && id.Verify(signature, pr.Hash) {
				pr.mu.Lock()
				pr.Status = ReceiptDelivered
				pr.Proved = true
				pr.ConcludedAt = float64(time.Now().UnixNano()) / 1e9
				pr.ProofPacket = proofPacket
				cb := pr.deliveryCallback
				pr.mu.Unlock()
				if cb != nil {
					cb(pr)
				}
				return true
			}
		}
	}
	return false
}

// SetTimeoutCallback assigns a function to be executed when the receipt's timeout window expires without delivery.
func (pr *PacketReceipt) SetTimeoutCallback(cb func(*PacketReceipt)) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.timeoutCallback = cb
}

// SetDeliveryCallback assigns a function to be executed when a valid proof of delivery is received for this packet.
func (pr *PacketReceipt) SetDeliveryCallback(cb func(*PacketReceipt)) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.deliveryCallback = cb
}

// SetTimeout establishes the duration, in seconds, before the receipt is considered failed if no proof is received.
func (pr *PacketReceipt) SetTimeout(timeout float64) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.Timeout = timeout
}

// MarkSent updates the receipt's internal state to indicate that the packet was physically dispatched at the given time.
func (pr *PacketReceipt) MarkSent(sentAt float64) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.Sent = true
	pr.SentAt = sentAt
	pr.Status = ReceiptSent
}

// TriggerDelivery fires the delivery callback, safely transitioning the receipt's status to delivered.
func (pr *PacketReceipt) TriggerDelivery() {
	pr.mu.Lock()
	pr.Proved = true
	pr.Status = ReceiptDelivered
	cb := pr.deliveryCallback
	pr.mu.Unlock()
	if cb != nil {
		cb(pr)
	}
}

// TriggerTimeout fires the timeout callback, safely transitioning the receipt's status to failed.
func (pr *PacketReceipt) TriggerTimeout() {
	pr.mu.Lock()
	pr.Status = ReceiptFailed
	cb := pr.timeoutCallback
	pr.mu.Unlock()
	if cb != nil {
		cb(pr)
	}
}

const (
	// ReceiptFailed indicates that a packet failed to be delivered within its timeout window.
	ReceiptFailed = 0x00
	// ReceiptSent indicates that a packet was successfully transmitted onto the physical interface.
	ReceiptSent = 0x01
	// ReceiptDelivered indicates that a valid cryptographic proof of delivery was received.
	ReceiptDelivered = 0x02
	// ReceiptCulled indicates that the packet receipt was culled from memory before delivery or timeout.
	ReceiptCulled = 0xff
)
