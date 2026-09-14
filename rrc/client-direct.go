// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"errors"
	"fmt"

	"github.com/gmlewis/go-reticulum/rns"
)

// IdentityHashLen is the length in bytes of an RNS identity hash. RRC keys
// every per-identity structure (member sets, nick tables, K_SRC, K_DST) by this
// value, so a direct-notice destination must be exactly this many bytes.
const IdentityHashLen = 16

// Errors returned by SendDirectNotice. They are sentinels so a caller can map
// them onto a user-facing message without matching on text.
var (
	// ErrDirectNoticesUnsupported reports that the hub never advertised the
	// CAP_DIRECT_NOTICE capability, so K_DST forwarding is not available on
	// this hub.
	ErrDirectNoticesUnsupported = errors.New(
		"hub does not advertise the direct-notice capability (CAP_DIRECT_NOTICE)")
	// ErrDestinationNotConnected reports a target hash the hub does not know.
	// The wording echoes the hub's own rejection for an unreachable target
	// ("destination not connected", handleDirectNotice), because that is the
	// error this client would otherwise receive one round trip later.
	ErrDestinationNotConnected = errors.New("destination not connected")
)

// HasCapability reports whether the hub advertised the given capability in its
// WELCOME. CBOR integer map keys and values may decode as int, int64, or
// uint64, and a hub may advertise a capability as a boolean or as a number, so
// every representation is accepted; an unknown, absent, false, or zero entry
// reports false.
func (h *RRCHub) HasCapability(capability int) bool {
	h.lock.Lock()
	caps := h.HubCaps
	h.lock.Unlock()
	return capabilityAdvertised(caps, capability)
}

// capabilityAdvertised is the representation-tolerant capability lookup.
func capabilityAdvertised(caps map[any]any, capability int) bool {
	if len(caps) == 0 {
		return false
	}
	switch v := envVal(caps, capability).(type) {
	case bool:
		return v
	case int:
		return v != 0
	case int64:
		return v != 0
	case uint64:
		return v != 0
	}
	return false
}

// knowsPeer reports whether the hub has learned the given identity-hash hex from
// a joined room's membership or from an observed nick, which is the client's
// only evidence that the hub can address a link for that identity.
func (h *RRCHub) knowsPeer(hashHex string) bool {
	if hashHex == "" {
		return false
	}
	h.lock.Lock()
	defer h.lock.Unlock()
	if _, ok := h.Nicks[hashHex]; ok {
		return true
	}
	for _, members := range h.Members {
		if members[hashHex] {
			return true
		}
	}
	return false
}

// SendDirectNotice sends one private NOTICE to peerHash using the RRC K_DST
// extension: the hub forwards it to that peer's link alone, with K_SRC rewritten
// to this client's identity hash and no room attached. Replies are typically
// addressed the same way and arrive as messages with Direct set.
//
// It fails, without putting anything on the wire, when the target is not a full
// identity hash, when the hub never advertised CAP_DIRECT_NOTICE, when the peer
// is unknown to the hub (ErrDestinationNotConnected), or when the encoded
// envelope would exceed one link MDU — the link silently drops an oversized
// packet, so the caller must split long text into lines itself.
func (h *RRCHub) SendDirectNotice(peerHash []byte, text string) error {
	if len(peerHash) != IdentityHashLen {
		return fmt.Errorf("direct notice destination must be a %v-byte identity hash, got %v bytes",
			IdentityHashLen, len(peerHash))
	}
	if !h.HasCapability(CapDirectNotice) {
		return ErrDirectNoticesUnsupported
	}
	if !h.knowsPeer(hexString(peerHash)) {
		return ErrDestinationNotConnected
	}

	var srcHash []byte
	if h.Manager != nil {
		srcHash = h.Manager.identityHash()
	}
	env := MakeDirectNoticeEnvelope(srcHash, peerHash, nil, text, MsgID(), NowMs())
	if payload := cborEncodedLen(env); payload > rns.MDU {
		return fmt.Errorf("direct notice is %v bytes encoded, over the %v-byte link MDU; send one line at a time",
			payload, rns.MDU)
	}

	h.sendEnv(env)
	return nil
}

// cborEncodedLen returns the encoded envelope length without building a packet.
func cborEncodedLen(env map[any]any) int {
	data, err := EncodeEnvelope(env)
	if err != nil {
		return 0
	}
	return len(data)
}
