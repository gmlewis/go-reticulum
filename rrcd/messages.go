// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements the message helper: sending and queueing utilities
// for the RRC hub, mirroring Python's MessageHelper.

package rrcd

import (
	"strings"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// MessageHooks wires a MessageHelper to the hub services it needs. The
// config accessors re-read the live configuration on every call, matching
// Python's fresh self.hub.config reads.
type MessageHooks struct {
	IdentityHash           func() []byte
	StatsInc               func(key string, delta int)
	SendPacket             func(link *rns.Link, payload []byte) error
	EnableResourceTransfer func() bool
	SendViaResource        func(link *rns.Link, kind string, payload []byte, room *string, encoding string) bool
	HubName                func() string
	Greeting               func() string
	MaxNickBytes           func() int
	MaxRoomNameBytes       func() int
	MaxMsgBodyBytes        func() int
	MaxRoomsPerSession     func() int
	RateLimitMsgsPerMinute func() int
	FmtLinkID              func(link *rns.Link) string
	FmtHash                func(hash []byte) string
	Logf                   func(format string, args ...any)
}

// MessageHelper provides message sending and queueing utilities for the RRC
// hub, mirroring Python's MessageHelper.
type MessageHelper struct {
	hooks MessageHooks
}

// Hooks returns the message helper's hook set for rewiring in tests.
func (m *MessageHelper) Hooks() *MessageHooks { return &m.hooks }

// NewMessageHelper creates a message helper wired to the given hooks.
func NewMessageHelper(hooks MessageHooks) *MessageHelper {
	return &MessageHelper{hooks: hooks}
}

// rnsMDU mirrors Python's Link.MDU class constant: the block-aligned MDU at
// the default MTU of 500.
const rnsMDU = ((500 - rns.IFACMinSize - rns.HeaderMinSize - rns.TokenOverhead) / rns.AES128BlockSize * rns.AES128BlockSize) - 1

// packetWouldFit checks whether a payload fits within the link MDU without
// creating or packing packets, mirroring packet_would_fit. Python compares
// against link.MDU, the class constant (431 at the default MTU 500), not the
// negotiated per-link MDU.
func (m *MessageHelper) packetWouldFit(link *rns.Link, payload []byte) bool {
	return len(payload) <= rnsMDU
}

// QueuePayload adds a raw payload to the outgoing queue, counting
// bytes_out, mirroring queue_payload.
func (m *MessageHelper) QueuePayload(outgoing *OutgoingList, link *rns.Link, payload []byte) {
	m.hooks.StatsInc("bytes_out", len(payload))
	outgoing.Queue = append(outgoing.Queue, OutgoingItem{Link: link, Payload: payload})
}

// QueueEnv encodes an envelope and queues it, mirroring queue_env.
func (m *MessageHelper) QueueEnv(outgoing *OutgoingList, link *rns.Link, env *cbor.Map) {
	m.QueuePayload(outgoing, link, cbor.Encode(env))
}

// splitLinesPython splits text the way Python's str.splitlines() does,
// handling \n, \r\n, and the remaining Python break characters.
func splitLinesPython(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == '\n' {
			line := text[start:i]
			line = strings.TrimSuffix(line, "\r")
			lines = append(lines, line)
			i += size
			start = i
			continue
		}
		if isPythonSplitline(r) {
			lines = append(lines, text[start:i])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}

// isPythonSplitline reports whether r is a Python splitlines break that is
// not \n (handled separately for \r\n pairing).
func isPythonSplitline(r rune) bool {
	switch r {
	case '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}

// QueueNoticeChunks splits text into lines and queues NOTICE envelopes for
// each MTU-sized chunk, mirroring queue_notice_chunks.
func (m *MessageHelper) QueueNoticeChunks(outgoing *OutgoingList, link *rns.Link, room *string, text string) {
	if m.hooks.IdentityHash() == nil {
		return
	}
	if text == "" {
		return
	}
	for _, line := range splitLinesPython(text) {
		runes := []rune(line)
		if len(runes) == 0 {
			continue
		}
		maxChars := minInt(utf8.RuneCountInString(line), MaxNoticeChunkChars)
		for len(runes) > 0 {
			take := minInt(len(runes), maxChars)
			chunk := string(runes[:take])
			env := MakeEnvelope(int(TNotice), m.hooks.IdentityHash(), WithRoomPtr(room), WithBody(chunk))
			payload := cbor.Encode(env)
			if m.packetWouldFit(link, payload) {
				m.QueuePayload(outgoing, link, payload)
				runes = runes[take:]
				maxChars = minInt(maxChars, MaxNoticeChunkChars)
				continue
			}
			if maxChars <= 1 {
				m.logf("NOTICE chunk would not fit MTU; dropping remainder (%v chars)", len(runes))
				break
			}
			maxChars = maxInt(1, maxChars/2)
		}
	}
}

// QueueWelcome queues a WELCOME message for a newly connected peer,
// mirroring queue_welcome. The caps map carries the Python insertion order
// (1, 2, then 0) and the limits map the 0,1,2,3,4 order.
func (m *MessageHelper) QueueWelcome(outgoing *OutgoingList, link *rns.Link, peerHash []byte) {
	if m.hooks.IdentityHash() == nil {
		return
	}
	limits := cbor.NewMap()
	limits.Set(BLimitMaxNickBytes, int64(m.hooks.MaxNickBytes()))
	limits.Set(BLimitMaxRoomNameBytes, int64(m.hooks.MaxRoomNameBytes()))
	limits.Set(BLimitMaxMsgBodyBytes, int64(m.hooks.MaxMsgBodyBytes()))
	limits.Set(BLimitMaxRoomsPerSession, int64(m.hooks.MaxRoomsPerSession()))
	limits.Set(BLimitRateMsgsPerMinute, int64(m.hooks.RateLimitMsgsPerMinute()))
	caps := cbor.NewMap()
	caps.Set(CAPAction, true)
	caps.Set(CAPDirectNotice, true)
	if m.hooks.EnableResourceTransfer() {
		caps.Set(CAPResourceEnvelope, true)
	}
	body := cbor.NewMap()
	body.Set(BWelcomeHub, m.hooks.HubName())
	body.Set(BWelcomeVer, rns.VERSION)
	body.Set(BWelcomeCaps, caps)
	body.Set(BWelcomeLimits, limits)
	welcome := MakeEnvelope(int(TWelcome), m.hooks.IdentityHash(), WithBody(body))
	welcomePayload := cbor.Encode(welcome)

	if !m.packetWouldFit(link, welcomePayload) {
		m.logf("WELCOME would not fit MTU; cannot welcome peer=%v link_id=%v",
			m.hooks.FmtHash(peerHash), m.hooks.FmtLinkID(link))
		return
	}
	m.QueuePayload(outgoing, link, welcomePayload)
	m.logf("Queued WELCOME peer=%v link_id=%v",
		m.hooks.FmtHash(peerHash), m.hooks.FmtLinkID(link))
}

// SendTextSmart sends a text message using the most efficient method
// (resource transfer for large immediate messages, chunks otherwise),
// mirroring send_text_smart with the default encoding.
func (m *MessageHelper) SendTextSmart(link *rns.Link, msgType int64, text string, room *string, kind string) {
	m.sendTextSmart(nil, link, msgType, text, room, kind, "utf-8")
}

// SendTextSmartQueued is the queued variant used when the outgoing list is
// provided.
func (m *MessageHelper) SendTextSmartQueued(outgoing *OutgoingList, link *rns.Link, msgType int64, text string, room *string, kind string) {
	m.sendTextSmart(outgoing, link, msgType, text, room, kind, "utf-8")
}

func (m *MessageHelper) sendTextSmart(outgoing *OutgoingList, link *rns.Link, msgType int64, text string, room *string, kind, encoding string) {
	resourceKind := kind
	if resourceKind == "" {
		if msgType == TNotice && room == nil {
			resourceKind = ResKindMOTD
		} else {
			resourceKind = ResKindNotice
		}
	}

	if m.hooks.EnableResourceTransfer() && outgoing == nil &&
		len([]byte(text)) > 512 {
		m.logf("Attempting resource transfer link_id=%v kind=%v chars=%v",
			m.hooks.FmtLinkID(link), resourceKind, utf8.RuneCountInString(text))
		if m.hooks.SendViaResource(link, resourceKind, []byte(text), room, encoding) {
			m.logf("Sent large text via resource link_id=%v kind=%v chars=%v",
				m.hooks.FmtLinkID(link), resourceKind, utf8.RuneCountInString(text))
			return
		}
		m.logf("Resource send failed, falling back to chunks link_id=%v",
			m.hooks.FmtLinkID(link))
	}

	if msgType == TNotice {
		m.logf("Falling back to chunking link_id=%v outgoing_is_none=%v",
			m.hooks.FmtLinkID(link), outgoing == nil)
		if outgoing == nil {
			var chunks OutgoingList
			m.QueueNoticeChunks(&chunks, link, room, text)
			for _, item := range chunks.Queue {
				m.hooks.StatsInc("bytes_out", len(item.Payload))
				if err := m.hooks.SendPacket(item.Link, item.Payload); err != nil {
					m.logf("Failed to send chunk link_id=%v: %v",
						m.hooks.FmtLinkID(item.Link), err)
				}
			}
		} else {
			m.QueueNoticeChunks(outgoing, link, room, text)
		}
	} else {
		m.logf("Message too large and not NOTICE link_id=%v type=%v",
			m.hooks.FmtLinkID(link), msgType)
	}
}

// EmitNotice emits a notice message, queued or immediate, mirroring
// emit_notice.
func (m *MessageHelper) EmitNotice(outgoing *OutgoingList, link *rns.Link, room *string, text string) {
	if m.hooks.IdentityHash() == nil {
		return
	}
	env := MakeEnvelope(int(TNotice), m.hooks.IdentityHash(), WithRoomPtr(room), WithBody(text))
	if outgoing == nil {
		m.Send(link, env)
	} else {
		m.QueueEnv(outgoing, link, env)
	}
}

// EmitError emits an error message, queued or immediate, mirroring
// emit_error. The errors_sent counter increments before any send.
func (m *MessageHelper) EmitError(outgoing *OutgoingList, link *rns.Link, src []byte, text string, room *string) {
	m.hooks.StatsInc("errors_sent", 1)
	env := MakeEnvelope(int(TError), src, WithRoomPtr(room), WithBody(text))
	if outgoing == nil {
		m.Send(link, env)
	} else {
		m.QueueEnv(outgoing, link, env)
	}
}

// NoticeTo sends a notice message immediately, mirroring notice_to.
func (m *MessageHelper) NoticeTo(link *rns.Link, room *string, text string) {
	if m.hooks.IdentityHash() == nil {
		return
	}
	env := MakeEnvelope(int(TNotice), m.hooks.IdentityHash(), WithRoomPtr(room), WithBody(text))
	m.Send(link, env)
}

// Error sends an error message immediately, mirroring error.
func (m *MessageHelper) Error(link *rns.Link, src []byte, text string, room *string) {
	m.EmitError(nil, link, src, text, room)
}

// Send sends an envelope immediately (not queued), mirroring send.
func (m *MessageHelper) Send(link *rns.Link, env *cbor.Map) {
	payload := cbor.Encode(env)
	m.hooks.StatsInc("bytes_out", len(payload))
	if err := m.hooks.SendPacket(link, payload); err != nil {
		m.logf("Send failed link_id=%v bytes=%v err=%v",
			m.hooks.FmtLinkID(link), len(payload), err)
	}
}

// logf logs a hub message through the hooks.
func (m *MessageHelper) logf(format string, args ...any) {
	if m.hooks.Logf != nil {
		m.hooks.Logf(format, args...)
	}
}

// minInt returns the smaller of two ints.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
