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

// Errors returned by SendPrivateCommand.
var (
	// ErrPrivateCommandsUnsupported reports that the hub never advertised the
	// CAPPrivateCommand capability, so it would treat a notice addressed to
	// its own identity as an unreachable peer instead of reading it as a
	// command.
	ErrPrivateCommandsUnsupported = errors.New(
		"hub does not advertise the private-command capability (CAP_PRIVATE_COMMAND)")
	// ErrHubIdentityUnknown reports that the hub's identity hash is not known
	// yet, which happens before the WELCOME arrives.
	ErrHubIdentityUnknown = errors.New("hub identity hash is not known yet")
)

// HubIdentityHash returns the hub's identity hash, captured from the source of
// the WELCOME envelope, or nil when no WELCOME has been processed. It is not
// the configured hub hash: that one is the hub's destination hash, from which
// RNS recalls this identity.
func (h *RRCHub) HubIdentityHash() []byte {
	h.lock.Lock()
	defer h.lock.Unlock()
	if len(h.hubIdentity) == 0 {
		return nil
	}
	out := make([]byte, len(h.hubIdentity))
	copy(out, h.hubIdentity)
	return out
}

// adoptHubIdentity records the hub identity hash carried by a WELCOME.
func (h *RRCHub) adoptHubIdentity(src []byte) {
	if len(src) != IdentityHashLen {
		return
	}
	h.lock.Lock()
	h.hubIdentity = make([]byte, len(src))
	copy(h.hubIdentity, src)
	h.lock.Unlock()
}

// SendPrivateCommand sends one command line to a hub that advertises
// CAPPrivateCommand: a NOTICE whose K_DST names the hub's own identity is read
// by that hub as a command and answered on this link, so neither the command
// nor its reply enters a room. It is how a client reaches a participant whose
// full identity hash it has never seen, because the hub resolves the named
// target itself — for example "/dnotice some-nick hello there".
//
// It fails, without putting anything on the wire, when the hub never
// advertised CAPPrivateCommand (ErrPrivateCommandsUnsupported), when the
// WELCOME has not arrived yet (ErrHubIdentityUnknown), or when the encoded
// envelope would exceed one link MDU.
func (h *RRCHub) SendPrivateCommand(text string) error {
	if !h.HasCapability(CapPrivateCommand) {
		return ErrPrivateCommandsUnsupported
	}
	hubHash := h.HubIdentityHash()
	if len(hubHash) != IdentityHashLen {
		return ErrHubIdentityUnknown
	}

	var srcHash []byte
	if h.Manager != nil {
		srcHash = h.Manager.identityHash()
	}
	env := MakeDirectNoticeEnvelope(srcHash, hubHash, nil, text, MsgID(), NowMs())
	if payload := cborEncodedLen(env); payload > rns.MDU {
		return fmt.Errorf("private command is %v bytes encoded, over the %v-byte link MDU; send one line at a time",
			payload, rns.MDU)
	}

	h.sendEnv(env)
	return nil
}
