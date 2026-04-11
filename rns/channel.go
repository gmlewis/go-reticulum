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
	"sync"
	"sync/atomic"
	"time"
)

const (
	// SMTStreamData defines the system-reserved message type code for internal binary stream data transmission.
	SMTStreamData = 0xff00
)

// Message defines the standard interface that any custom data structure must implement to be transmitted over a Channel.
type Message interface {
	GetMsgType() uint16
	Pack() ([]byte, error)
	Unpack(data []byte) error
}

// ChannelOutlet defines the required transport layer interface that a Channel uses to physically send and manage packets.
type ChannelOutlet interface {
	Send(raw []byte) (*Packet, error)
	Resend(p *Packet) (*Packet, error)
	MDU() int
	RTT() float64
	IsUsable() bool
}

// MessageState defines an enumeration representing the various lifecycle stages of a message in transit.
type MessageState int

const (
	// MsgStateNew indicates that the message has been instantiated but not yet queued for transmission.
	MsgStateNew MessageState = iota
	// MsgStateSent indicates that the message has been transmitted and is currently awaiting an acknowledgment.
	MsgStateSent
	// MsgStateDelivered indicates that the message has been successfully received and acknowledged by the remote peer.
	MsgStateDelivered
	// MsgStateFailed indicates that the message could not be delivered after exceeding the maximum number of retry attempts.
	MsgStateFailed
)

// Envelope serves as an internal wrapper for messages, managing sequencing, timing, and retry logic over a Channel.
type Envelope struct {
	TS       time.Time
	Message  Message
	Raw      []byte
	Packet   *Packet
	Sequence uint16
	Tries    int
}

// Pack serializes the Envelope and its contained Message into a strict binary format suitable for network transmission.
func (env *Envelope) Pack() ([]byte, error) {
	data, err := env.Message.Pack()
	if err != nil {
		return nil, err
	}

	// Wire format: msgtype(2) + sequence(2) + length(2) + data
	raw := make([]byte, 6+len(data))
	binary.BigEndian.PutUint16(raw[0:2], env.Message.GetMsgType())
	binary.BigEndian.PutUint16(raw[2:4], env.Sequence)
	binary.BigEndian.PutUint16(raw[4:6], uint16(len(data)))
	copy(raw[6:], data)
	env.Raw = raw
	return raw, nil
}

// Unpack reconstructs the Envelope and its contained Message from a binary payload using the provided message factories.
func (env *Envelope) Unpack(factories map[uint16]func() Message) error {
	if len(env.Raw) < 6 {
		return errors.New("envelope too short")
	}

	msgType := binary.BigEndian.Uint16(env.Raw[0:2])
	env.Sequence = binary.BigEndian.Uint16(env.Raw[2:4])
	length := binary.BigEndian.Uint16(env.Raw[4:6])

	if len(env.Raw) < 6+int(length) {
		return errors.New("envelope data truncated")
	}

	factory, ok := factories[msgType]
	if !ok {
		return fmt.Errorf("unknown message type %v", msgType)
	}

	msg := factory()
	if err := msg.Unpack(env.Raw[6 : 6+int(length)]); err != nil {
		return err
	}
	env.Message = msg
	return nil
}

// Channel provides a robust, reliable, and sequenced delivery mechanism for discrete messages over a Link.
type Channel struct {
	logger           Logger
	outlet           ChannelOutlet
	mu               sync.RWMutex
	stopCh           chan struct{}
	stopOnce         sync.Once
	startOnce        sync.Once
	txRing           []*Envelope
	rxRing           []*Envelope
	messageHandlers  []messageHandlerEntry
	nextHandlerID    uint64
	nextSequence     uint16
	nextRXSequence   uint16
	messageFactories map[uint16]func() Message
	maxTries         int

	window            int
	windowMax         int
	windowMin         int
	windowFlexibility int
	fastRateRounds    int
	mediumRateRounds  int
}

type messageHandlerEntry struct {
	id      uint64
	handler func(Message) bool
}

const (
	// ChannelWindowDefault specifies the initial transmission window size for a new Channel.
	ChannelWindowDefault = 4
	// ChannelWindowMin defines the absolute minimum allowable transmission window size.
	ChannelWindowMin = 2
	// ChannelWindowMinSlow establishes the minimum transmission window size during slow network conditions.
	ChannelWindowMinSlow = 2
	// ChannelWindowMinMedium establishes the minimum transmission window size during medium-speed network conditions.
	ChannelWindowMinMedium = 5
	// ChannelWindowMinFast establishes the minimum transmission window size during fast network conditions.
	ChannelWindowMinFast = 16
	// ChannelWindowMaxSlow establishes the maximum transmission window size during slow network conditions.
	ChannelWindowMaxSlow = 5
	// ChannelWindowMaxMedium establishes the maximum transmission window size during medium-speed network conditions.
	ChannelWindowMaxMedium = 12
	// ChannelWindowMaxFast establishes the maximum transmission window size during fast network conditions.
	ChannelWindowMaxFast = 48
	// ChannelSeqMax defines the maximum sequence number before wrapping around to zero.
	ChannelSeqMax = 0xFFFF
	// ChannelFastRateRounds specifies the number of consecutive successful rounds required to upgrade the window size tier.
	ChannelFastRateRounds = 10
	// ChannelRTTFast defines the maximum Round Trip Time (in seconds) to be considered a fast connection.
	ChannelRTTFast = 0.18
	// ChannelRTTMedium defines the maximum Round Trip Time (in seconds) to be considered a medium-speed connection.
	ChannelRTTMedium = 0.75
	// ChannelRTTSlow defines the threshold Round Trip Time (in seconds) where the connection is considered slow.
	ChannelRTTSlow = 1.45
)

// NewChannel instantiates a new Channel operating over the provided ChannelOutlet transport mechanism.
func NewChannel(outlet ChannelOutlet) *Channel {
	c := &Channel{
		outlet:            outlet,
		messageFactories:  make(map[uint16]func() Message),
		stopCh:            make(chan struct{}),
		maxTries:          5,
		window:            ChannelWindowDefault,
		windowMax:         ChannelWindowMaxSlow,
		windowMin:         ChannelWindowMin,
		windowFlexibility: 4,
	}

	if outlet.RTT() > ChannelRTTSlow {
		c.window = 1
		c.windowMax = 1
		c.windowMin = 1
		c.windowFlexibility = 1
	}

	return c
}

// Start initiates the internal background processes responsible for packet timeouts, retransmissions, and maintenance.
func (c *Channel) Start() {
	c.startOnce.Do(func() {
		go c.maintenanceLoop()
	})
}

func (c *Channel) maintenanceLoop() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.checkTimeouts()
		case <-c.stopCh:
			return
		}
	}
}

func (c *Channel) checkTimeouts() {
	type timeoutEvent struct {
		receipt *PacketReceipt
	}

	now := float64(time.Now().UnixNano()) / 1e9
	events := make([]timeoutEvent, 0)

	c.mu.RLock()
	for _, env := range c.txRing {
		if env.Packet == nil || env.Packet.Receipt == nil {
			continue
		}
		pr := env.Packet.Receipt
		pr.mu.Lock()
		sentAt := pr.SentAt
		timeout := pr.Timeout
		hasTimeoutCB := pr.timeoutCallback != nil
		pr.mu.Unlock()

		if hasTimeoutCB && sentAt > 0 && timeout > 0 && now >= sentAt+timeout {
			pr.MarkSent(now)
			events = append(events, timeoutEvent{receipt: pr})
		}
	}
	c.mu.RUnlock()

	for _, event := range events {
		event.receipt.TriggerTimeout()
	}
}

// Shutdown halts the channel's background processes and securely clears all pending transmission and reception queues.
func (c *Channel) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.messageHandlers = nil
	c.clearRings()
}

func (c *Channel) clearRings() {
	for _, env := range c.txRing {
		if env.Packet != nil && env.Packet.Receipt != nil {
			env.Packet.Receipt.SetTimeoutCallback(nil)
			env.Packet.Receipt.SetDeliveryCallback(nil)
		}
	}
	c.txRing = nil
	c.rxRing = nil
}

// RegisterMessageType maps a specific 16-bit message type identifier to its corresponding Message factory function.
func (c *Channel) RegisterMessageType(msgType uint16, factory func() Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messageFactories[msgType] = factory
}

// AddMessageHandler registers a callback function to be invoked whenever a valid Message is received and unpacked.
func (c *Channel) AddMessageHandler(handler func(Message) bool) {
	c.addMessageHandler(handler)
}

func (c *Channel) addMessageHandler(handler func(Message) bool) uint64 {
	handlerID := atomic.AddUint64(&c.nextHandlerID, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messageHandlers = append(c.messageHandlers, messageHandlerEntry{
		id:      handlerID,
		handler: handler,
	})
	return handlerID
}

func (c *Channel) removeMessageHandlerByID(handlerID uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, entry := range c.messageHandlers {
		if entry.id == handlerID {
			c.messageHandlers = append(c.messageHandlers[:i], c.messageHandlers[i+1:]...)
			return
		}
	}
}

// Send serializes the provided Message, wraps it in an Envelope, and securely transmits it over the underlying outlet.
func (c *Channel) Send(msg Message) (*Envelope, error) {
	c.mu.Lock()
	if !c.isReadyToSend() {
		c.mu.Unlock()
		return nil, errors.New("channel not ready to send")
	}

	env := &Envelope{
		TS:       time.Now(),
		Message:  msg,
		Sequence: c.nextSequence,
	}
	c.nextSequence++

	if !c.emplaceEnvelope(env, &c.txRing) {
		c.mu.Unlock()
		return nil, errors.New("failed to place envelope in tx ring")
	}
	c.mu.Unlock()

	raw, err := env.Pack()
	if err != nil {
		return nil, err
	}
	c.logger.Extreme("Channel.Send msgType=%v seq=%v rawLen=%v\n", msg.GetMsgType(), env.Sequence, len(raw))
	if len(raw) > c.outlet.MDU() {
		return nil, fmt.Errorf("message too big: %v > %v", len(raw), c.outlet.MDU())
	}

	p, err := c.outlet.Send(raw)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	env.Packet = p
	env.Tries++

	if p.Receipt != nil {
		if len(p.Receipt.Hash) == 0 {
			p.Receipt.Hash = append([]byte(nil), p.PacketHash...)
		}
		if len(p.Receipt.TruncatedHash) == 0 {
			if len(p.Raw) > 0 {
				p.Receipt.TruncatedHash = append([]byte(nil), p.GetTruncatedHash()...)
			} else {
				p.Receipt.TruncatedHash = TruncatedHash(p.PacketHash)
			}
		}
		if p.SentAt > 0 {
			p.Receipt.MarkSent(p.SentAt)
		} else {
			p.Receipt.MarkSent(float64(time.Now().UnixNano()) / 1e9)
		}
		p.Receipt.SetDeliveryCallback(c.packetDelivered)
		p.Receipt.SetTimeoutCallback(c.packetTimeout)
		p.Receipt.SetTimeout(c.getPacketTimeoutSeconds(env.Tries))
		c.updatePacketTimeouts()
	} else {
		// Local delivery, no receipt. Remove from ring immediately.
		for i, ringEnv := range c.txRing {
			if ringEnv == env {
				c.txRing = append(c.txRing[:i], c.txRing[i+1:]...)
				break
			}
		}
	}
	c.mu.Unlock()

	return env, nil
}

func (c *Channel) packetDelivered(pr *PacketReceipt) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, env := range c.txRing {
		if env.Packet != nil && bytes.Equal(env.Packet.PacketHash, pr.Hash) {
			// Remove from ring
			c.txRing = append(c.txRing[:i], c.txRing[i+1:]...)

			// Increase window
			if c.window < c.windowMax {
				c.window++
			}

			rtt := c.outlet.RTT()
			if rtt != 0 {
				if rtt > ChannelRTTFast {
					c.fastRateRounds = 0
					if rtt > ChannelRTTMedium {
						c.mediumRateRounds = 0
					} else {
						c.mediumRateRounds++
						if c.windowMax < ChannelWindowMaxMedium && c.mediumRateRounds >= ChannelFastRateRounds {
							c.windowMax = ChannelWindowMaxMedium
							c.windowMin = ChannelWindowMinMedium
						}
					}
				} else {
					c.fastRateRounds++
					if c.windowMax < ChannelWindowMaxFast && c.fastRateRounds >= ChannelFastRateRounds {
						c.windowMax = ChannelWindowMaxFast
						c.windowMin = ChannelWindowMinFast
					}
				}
			}
			return
		}
	}
}

func (c *Channel) packetTimeout(pr *PacketReceipt) {
	c.mu.Lock()
	envIdx := -1
	for i, env := range c.txRing {
		if env.Packet != nil && bytes.Equal(env.Packet.PacketHash, pr.Hash) {
			envIdx = i
			break
		}
	}

	if envIdx == -1 {
		c.mu.Unlock()
		return
	}

	env := c.txRing[envIdx]
	if env.Tries >= c.maxTries {
		c.logger.Error("Retry count exceeded on %v, shutting down channel.", c)
		c.mu.Unlock()
		c.Shutdown()
		return
	}

	env.Tries++
	if c.window > c.windowMin {
		c.window--
		if c.windowMax > (c.windowMin + c.windowFlexibility) {
			c.windowMax--
		}
	}

	if env.Packet != nil && env.Packet.Receipt != nil {
		env.Packet.Receipt.SetTimeout(c.getPacketTimeoutSeconds(env.Tries))
	}
	c.updatePacketTimeouts()

	packet := env.Packet
	c.mu.Unlock()

	if packet == nil {
		return
	}

	// Resend
	resentPacket, err := c.outlet.Resend(packet)
	if err != nil {
		c.logger.Error("Failed to resend packet: %v", err)
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if resentPacket != nil {
		env.Packet = resentPacket
	}
	if env.Packet != nil && env.Packet.Receipt != nil {
		env.Packet.Receipt.SetDeliveryCallback(c.packetDelivered)
		env.Packet.Receipt.SetTimeoutCallback(c.packetTimeout)
		env.Packet.Receipt.SetTimeout(c.getPacketTimeoutSeconds(env.Tries))
	}
}

func (c *Channel) updatePacketTimeouts() {
	for _, env := range c.txRing {
		if env.Packet == nil || env.Packet.Receipt == nil {
			continue
		}
		updatedTimeout := c.getPacketTimeoutSeconds(env.Tries)
		if updatedTimeout > env.Packet.Receipt.Timeout {
			env.Packet.Receipt.SetTimeout(updatedTimeout)
		}
	}
}

func (c *Channel) getPacketTimeoutSeconds(tries int) float64 {
	return math.Pow(1.5, float64(tries-1)) * math.Max(c.outlet.RTT()*2.5, 0.025) * (float64(len(c.txRing)) + 1.5)
}

func (c *Channel) isReadyToSend() bool {
	if !c.outlet.IsUsable() {
		return false
	}
	// Simplified window check
	return len(c.txRing) < c.window
}

// Receive processes raw byte payloads inbound from the outlet, deserializing envelopes and dispatching validated messages to handlers.
func (c *Channel) Receive(raw []byte) {
	env := &Envelope{
		TS:  time.Now(),
		Raw: raw,
	}

	c.mu.Lock()
	if err := env.Unpack(c.messageFactories); err != nil {
		c.mu.Unlock()
		c.logger.Debug("Failed to unpack channel envelope: %v", err)
		return
	}

	// Duplicate detection and window check
	if env.Sequence < c.nextRXSequence {
		windowOverflow := c.nextRXSequence + uint16(ChannelWindowMaxFast)
		if windowOverflow < c.nextRXSequence {
			if env.Sequence > windowOverflow {
				c.mu.Unlock()
				c.logger.Extreme("Invalid packet sequence %v received on channel", env.Sequence)
				return
			}
		} else {
			c.mu.Unlock()
			c.logger.Extreme("Invalid packet sequence %v received on channel", env.Sequence)
			return
		}
	}

	if !c.emplaceEnvelope(env, &c.rxRing) {
		c.mu.Unlock()
		c.logger.Extreme("Duplicate message %v received on channel", env.Sequence)
		return
	}

	// Process contiguous messages
	var contiguous []*Envelope
	for len(c.rxRing) > 0 && c.rxRing[0].Sequence == c.nextRXSequence {
		e := c.rxRing[0]
		contiguous = append(contiguous, e)
		c.rxRing = c.rxRing[1:]
		c.nextRXSequence++
	}
	c.mu.Unlock()

	for _, e := range contiguous {
		c.logger.Extreme("Channel.Receive msgType=%v seq=%v rawLen=%v\n", e.Message.GetMsgType(), e.Sequence, len(raw))
		c.handleMessage(e.Message)
	}
}

func (c *Channel) emplaceEnvelope(env *Envelope, ring *[]*Envelope) bool {
	for i, existing := range *ring {
		if env.Sequence == existing.Sequence {
			return false
		}
		if env.Sequence < existing.Sequence {
			// Check for wrap-around
			if (c.nextRXSequence - env.Sequence) > (ChannelSeqMax / 2) {
				continue
			}
			// Insert here
			newRing := make([]*Envelope, len(*ring)+1)
			copy(newRing, (*ring)[:i])
			newRing[i] = env
			copy(newRing[i+1:], (*ring)[i:])
			*ring = newRing
			return true
		}
	}
	*ring = append(*ring, env)
	return true
}

func (c *Channel) handleMessage(msg Message) {
	c.mu.RLock()
	handlers := make([]func(Message) bool, 0, len(c.messageHandlers))
	for _, entry := range c.messageHandlers {
		handlers = append(handlers, entry.handler)
	}
	c.mu.RUnlock()

	for _, handler := range handlers {
		if handler(msg) {
			return
		}
	}
}
