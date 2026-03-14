// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"fmt"
	"sync"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// Packet types
const (
	PacketData        = 0x00
	PacketAnnounce    = 0x01
	PacketLinkRequest = 0x02
	PacketProof       = 0x03
)

// Header types
const (
	Header1 = 0x00
	Header2 = 0x01
)

// Transport types
const (
	TransportBroadcast = 0x00
	TransportForward   = 0x01
	TransportRelay     = 0x02
	TransportTunnel    = 0x03
)

// Packet context types
const (
	ContextNone          = 0x00
	ContextResource      = 0x01
	ContextResourceAdv   = 0x02
	ContextResourceReq   = 0x03
	ContextResourceHmu   = 0x04
	ContextResourcePrf   = 0x05
	ContextResourceIcl   = 0x06
	ContextResourceRcl   = 0x07
	ContextCacheRequest  = 0x08
	ContextRequest       = 0x09
	ContextResponse      = 0x0a
	ContextPathResponse  = 0x0b
	ContextCommand       = 0x0c
	ContextCommandStatus = 0x0d
	ContextChannel       = 0x0e
	ContextKeepalive     = 0xfa
	ContextLinkIdentify  = 0xfb
	ContextLinkClose     = 0xfc
	ContextLinkProof     = 0xfd
	ContextLrrtt         = 0xfe
	ContextLrproof       = 0xff
)

// Context flag values
const (
	FlagSet   = 0x01
	FlagUnset = 0x00
)

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

	// Optional fields for received packets
	RSSI *float64
	SNR  *float64
	Q    *float64

	transport *TransportSystem
}

// NewPacket creates a new packet for the given destination and data.
func NewPacket(destination PacketDestination, data []byte) *Packet {
	var ts *TransportSystem
	if destination != nil {
		// Use type assertion to get transport from Destination if possible
		if d, ok := destination.(*Destination); ok {
			ts = d.transport
		} else if l, ok := destination.(*Link); ok {
			ts = l.transport
		} else {
			ts = GetTransport()
		}
	} else {
		ts = GetTransport()
	}
	return NewPacketWithTransport(ts, destination, data)
}

// NewPacketWithTransport creates a new packet with a specific transport system.
func NewPacketWithTransport(ts *TransportSystem, destination PacketDestination, data []byte) *Packet {
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
		if d, ok := p.Destination.(*Destination); ok && d.transport != nil {
			return d.transport.Outbound(p)
		}
		if l, ok := p.Destination.(*Link); ok && l.transport != nil {
			return l.transport.Outbound(p)
		}
	}
	return Transport.Outbound(p)
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

	timeoutCallback  func(*PacketReceipt)
	deliveryCallback func(*PacketReceipt)
	mu               sync.Mutex
}

func (pr *PacketReceipt) SetTimeoutCallback(cb func(*PacketReceipt)) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.timeoutCallback = cb
}

func (pr *PacketReceipt) SetDeliveryCallback(cb func(*PacketReceipt)) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.deliveryCallback = cb
}

func (pr *PacketReceipt) SetTimeout(timeout float64) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.Timeout = timeout
}

func (pr *PacketReceipt) MarkSent(sentAt float64) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.Sent = true
	pr.SentAt = sentAt
	pr.Status = ReceiptSent
}

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

func (pr *PacketReceipt) TriggerTimeout() {
	pr.mu.Lock()
	pr.Status = ReceiptFailed
	cb := pr.timeoutCallback
	pr.mu.Unlock()
	if cb != nil {
		cb(pr)
	}
}

// Receipt status constants
const (
	ReceiptFailed    = 0x00
	ReceiptSent      = 0x01
	ReceiptDelivered = 0x02
	ReceiptCulled    = 0xff
)
