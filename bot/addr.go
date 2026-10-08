// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the addr command: every address a peer's identity publishes,
// printed in the form a reader can act on.
//
// Those forms are the client's, not this command's invention. An RRC client
// linkifies exactly three shapes in a chat body (Channels.py's _LINK_RE), so the
// reply uses them and prints nothing that pretends to be one:
//
//   - "@<identity hash>" is the address a hub knows a client by. The "@" is not
//     decoration: the client's node-link pattern refuses to match a hash that
//     follows an "@", so an identity hash printed this way stays plain text
//     instead of turning into a link to a node that does not exist.
//   - "lxmf@<destination hash>" is an LXMF address, which the client turns into a
//     link that opens a conversation with that peer.
//   - a bare "<destination hash>" is a NomadNet node, which the client turns into
//     a link that opens the node browser.
//
// The full destination specifier is deliberately absent here. It is the tool form
// — what `gornid -H` prints and what a configuration file stores — and a chat body
// containing one makes the client underline it as a node link with a nonsense page
// path, which is a worse answer than not printing it at all. The qr command prints
// the specifier under the picture, where the reader is already copying text out.
package bot

import (
	"fmt"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	// addrUsage is the usage line for the addr command.
	addrUsage = "addr [<nick|hash|me>]"
	// addrIdentityNote explains what the identity hash is not, because that is the
	// confusion the command exists to remove: an identity is not a destination and
	// its hash is not one either.
	addrIdentityNote = "the RNS identity, not a destination"
	// addrUnknownPurpose is the wording for a destination with no purpose of its
	// own, which keeps the reply honest if the destination list ever grows.
	addrUnknownPurpose = "an RNS destination"
)

// addrPurpose says what each destination is for, in a reader's terms: a list of
// names is not an answer to "which of these do I want?".
var addrPurpose = map[string]string{
	"lxmf.delivery":     "where LXMF messages go",
	"nomadnetwork.node": "the node to browse",
	"rrc.hub":           "the hub itself",
}

// addrLine renders one destination the way a reader can use it: the address in the
// form the client linkifies, then the destination name and what it is for.
func addrLine(dest peerDestination, identityHash []byte) string {
	destHex := hexString(rns.CalculateHash(&rns.Identity{Hash: identityHash}, dest.app, dest.aspects...))
	address := destHex
	if dest.app == "lxmf" {
		address = "lxmf@" + destHex
	}
	purpose := addrPurpose[dest.name]
	if purpose == "" {
		purpose = addrUnknownPurpose
	}
	return fmt.Sprintf("%v (%v, %v)", address, dest.name, purpose)
}

// runAddr reports every address the named peer's identity publishes: the identity
// hash a hub addresses them by, and each destination hash under its own name.
func (c *commandContext) runAddr() []string {
	if len(strings.Fields(c.Args)) > 1 {
		return []string{"Usage: " + addrUsage}
	}
	token := strings.TrimSpace(c.Args)
	if token == "" {
		token = "me"
	}
	subject, rejected := c.pathSubject(token)
	if rejected != nil {
		return rejected
	}
	destinations := c.pathDestinations(subject.hash)
	label := safeEcho(subject.label, maxEchoNickBytes)
	lines := make([]string, 0, len(destinations)+1)
	lines = append(lines, fmt.Sprintf("addr %v: identity @%v (%v)",
		label, hexString(subject.hash), addrIdentityNote))
	for _, dest := range destinations {
		lines = append(lines, addrLine(dest, subject.hash))
	}
	return lines
}
