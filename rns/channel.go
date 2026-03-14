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

// SystemMessageTypes defines system-reserved message type codes.
const (
	SMTStreamData = 0xff00
)

// Message is the interface for any message sent over a Channel.
type Message interface {
	GetMsgType() uint16
	Pack() ([]byte, error)
	Unpack(data []byte) error
}

// ChannelOutlet defines the transport layer interface used by Channel.
type ChannelOutlet interface {
	Send(raw []byte) (*Packet, error)
	Resend(p *Packet) (*Packet, error)
	MDU() int
	RTT() float64
	IsUsable() bool
}

// MessageState represents the possible states of a message.
type MessageState int

const (
	MsgStateNew MessageState = iota
	MsgStateSent
	MsgStateDelivered
	MsgStateFailed
)

// Envelope is an internal wrapper for messages sent over a channel.
type Envelope struct {
	TS       time.Time
	Message  Message
	Raw      []byte
	Packet   *Packet
	Sequence uint16
	Tries    int
}

// Pack returns the binary representation of the envelope.
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

// Unpack populates the envelope from binary representation.
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

// Channel provides reliable delivery of messages over a link.
type Channel struct {
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
	ChannelWindowDefault   = 2
	ChannelWindowMin       = 2
	ChannelWindowMinSlow   = 2
	ChannelWindowMinMedium = 5
	ChannelWindowMinFast   = 16
	ChannelWindowMaxSlow   = 5
	ChannelWindowMaxMedium = 12
	ChannelWindowMaxFast   = 48
	ChannelSeqMax          = 0xFFFF
	ChannelFastRateRounds  = 10
	ChannelRTTFast         = 0.18
	ChannelRTTMedium       = 0.75
	ChannelRTTSlow         = 1.45
)

// NewChannel creates a new Channel over the given outlet.
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

// Start starts the channel's background processes.
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

// Shutdown stops the channel and clears resources.
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

// RegisterMessageType registers a message factory for a given type.
func (c *Channel) RegisterMessageType(msgType uint16, factory func() Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messageFactories[msgType] = factory
}

// AddMessageHandler adds a callback for incoming messages.
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

// Send packs and sends a message over the channel.
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
		Logf("Retry count exceeded on %v, shutting down channel.", LogError, false, c)
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
		Logf("Failed to resend packet: %v", LogError, false, err)
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

// Receive handles raw data received from the outlet.
func (c *Channel) Receive(raw []byte) {
	env := &Envelope{
		TS:  time.Now(),
		Raw: raw,
	}

	c.mu.Lock()
	if err := env.Unpack(c.messageFactories); err != nil {
		c.mu.Unlock()
		Logf("Failed to unpack channel envelope: %v", LogDebug, false, err)
		return
	}

	// Duplicate detection and window check
	if env.Sequence < c.nextRXSequence {
		windowOverflow := c.nextRXSequence + uint16(ChannelWindowMaxFast)
		if windowOverflow < c.nextRXSequence {
			if env.Sequence > windowOverflow {
				c.mu.Unlock()
				Logf("Invalid packet sequence %v received on channel", LogExtreme, false, env.Sequence)
				return
			}
		} else {
			c.mu.Unlock()
			Logf("Invalid packet sequence %v received on channel", LogExtreme, false, env.Sequence)
			return
		}
	}

	if !c.emplaceEnvelope(env, &c.rxRing) {
		c.mu.Unlock()
		Logf("Duplicate message %v received on channel", LogExtreme, false, env.Sequence)
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
			break
		}
	}
}
