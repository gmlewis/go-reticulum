// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// MinPeerHashPrefix is the shortest hex prefix accepted as a peer-hash alias.
// Six hex characters narrow a lookup to 24 bits, which is short enough to type
// and long enough that a live room rarely contains two colliding members; a
// shorter prefix resolves ambiguously too often to be useful, so it is rejected
// outright rather than guessed at.
const MinPeerHashPrefix = 6

// Errors returned by ResolvePeerToken. They are sentinels so a caller can tell
// "ask again with a longer token" from "this peer does not exist".
var (
	// ErrPeerTokenEmpty reports a blank peer token.
	ErrPeerTokenEmpty = errors.New("empty peer token")
	// ErrPeerTokenTooShort reports a hex prefix shorter than
	// MinPeerHashPrefix that does match a known peer hash.
	ErrPeerTokenTooShort = errors.New("peer hash prefix is too short")
	// ErrPeerNotFound reports a token that matches no known nick and no known
	// hash prefix.
	ErrPeerNotFound = errors.New("no such peer")
	// ErrPeerAmbiguous reports a token that matches more than one peer. The
	// message lists every candidate with its nick, mirroring the hub's own
	// vocabulary for a destination that cannot be singled out.
	ErrPeerAmbiguous = errors.New("ambiguous peer")
)

// PeerTarget is one resolved RRC peer.
type PeerTarget struct {
	// HashHex is the peer's identity-hash hex exactly as the hub learned it:
	// a full 32-hex hash when the hub knows one, or a truncated prefix when
	// membership arrived only through a /who reply.
	HashHex string
	// Hash is the decoded identity hash, empty when only a prefix is known.
	Hash []byte
	// Nick is the peer's display name, falling back to the hash prefix the
	// hub renders when no nick has been learned.
	Nick string
}

// Resolvable reports whether this peer can be addressed directly, which
// requires a complete identity hash: K_DST carries raw hash bytes, and a
// truncated who-reply prefix cannot be sent to.
func (p PeerTarget) Resolvable() bool {
	return len(p.Hash) == IdentityHashLen
}

// String renders the peer the way the hub renders a member: nick first, with
// the hash prefix in parentheses.
func (p PeerTarget) String() string {
	label := p.Nick
	if label == "" {
		label = p.HashHex
	}
	prefix := p.HashHex
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	if prefix == "" {
		return label
	}
	return fmt.Sprintf("%v (%v)", label, prefix)
}

// ResolvePeerToken resolves a user-supplied peer token to one peer. A token is
// tried as a hex identity-hash prefix first (six characters or more, matched
// case-insensitively), then as a nick (case-insensitive, exact). A token that
// matches several peers fails with ErrPeerAmbiguous and a message listing each
// candidate, because guessing would send a private notice to the wrong person.
//
// The index is the hub's learned membership: every joined room's member set
// plus every nick the hub has observed. It is a client-side convenience built
// on GetRoomMembers and the hub's nick table, so it knows exactly what this
// client has seen.
func (h *RRCHub) ResolvePeerToken(token string) (PeerTarget, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return PeerTarget{}, ErrPeerTokenEmpty
	}
	peers := h.peerIndex()
	lower := strings.ToLower(token)

	if isHashText(lower) {
		matches := matchHashPrefix(peers, lower)
		if len(lower) < MinPeerHashPrefix {
			// A short token that really does prefix a known peer is a
			// deliberate hash lookup, not a nick: tell the caller to be more
			// specific instead of reporting "no such peer". A short non-hex
			// token falls through to the nick lookup below.
			if len(matches) > 0 {
				return PeerTarget{}, fmt.Errorf(
					"%w: %q is %v hex characters, but at least %v are required to identify a peer",
					ErrPeerTokenTooShort, token, len(lower), MinPeerHashPrefix)
			}
		} else {
			switch len(matches) {
			case 1:
				return matches[0], nil
			case 0:
			default:
				return PeerTarget{}, ambiguousPeerError(token, matches)
			}
		}
	}

	var byNick []PeerTarget
	for _, peer := range peers {
		if strings.EqualFold(peer.Nick, token) {
			byNick = append(byNick, peer)
		}
	}
	switch len(byNick) {
	case 1:
		return byNick[0], nil
	case 0:
		return PeerTarget{}, fmt.Errorf("%w: %q", ErrPeerNotFound, token)
	default:
		return PeerTarget{}, ambiguousPeerError(token, byNick)
	}
}

// peerIndex returns every peer this hub has learned, sorted by hash hex so
// resolution — and the candidate list in an ambiguity error — is deterministic.
// Entries that are not hex, or longer than a full identity hash, are ignored.
func (h *RRCHub) peerIndex() []PeerTarget {
	byHash := make(map[string]PeerTarget)

	add := func(hashHex, nick string) {
		hashHex = strings.ToLower(strings.TrimSpace(hashHex))
		if hashHex == "" || !isHashText(hashHex) || len(hashHex) > IdentityHashLen*2 {
			return
		}
		existing, seen := byHash[hashHex]
		if seen && existing.Nick != "" {
			return
		}
		peer := PeerTarget{HashHex: hashHex, Nick: nick}
		if len(hashHex) == IdentityHashLen*2 {
			// A member set always carries valid hex; a decode failure here
			// leaves Hash empty, which Resolvable reports honestly.
			if raw, err := hex.DecodeString(hashHex); err == nil {
				peer.Hash = raw
			}
		}
		byHash[hashHex] = peer
	}

	for _, room := range h.JoinedRoomList() {
		for _, member := range h.GetRoomMembers(room) {
			add(member.HashHex, member.Nick)
		}
	}

	h.lock.Lock()
	nicks := make(map[string]string, len(h.Nicks))
	maps.Copy(nicks, h.Nicks)
	h.lock.Unlock()
	for hashHex, nick := range nicks {
		add(hashHex, nick)
	}

	peers := make([]PeerTarget, 0, len(byHash))
	for _, peer := range byHash {
		peers = append(peers, peer)
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].HashHex < peers[j].HashHex })
	return peers
}

// matchHashPrefix returns the peers whose hash hex starts with the (lowercased)
// prefix, in peerIndex order.
func matchHashPrefix(peers []PeerTarget, prefix string) []PeerTarget {
	var matches []PeerTarget
	for _, peer := range peers {
		if strings.HasPrefix(peer.HashHex, prefix) {
			matches = append(matches, peer)
		}
	}
	return matches
}

// ambiguousPeerError builds the ambiguity failure: the hub's "ambiguous" term
// plus every candidate rendered as nick and hash prefix.
func ambiguousPeerError(token string, candidates []PeerTarget) error {
	rendered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		rendered = append(rendered, candidate.String())
	}
	return fmt.Errorf("%w: %q matches %v peers: %v", ErrPeerAmbiguous, token, len(candidates),
		strings.Join(rendered, ", "))
}
