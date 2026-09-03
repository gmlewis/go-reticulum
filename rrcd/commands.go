// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements the RRC hub operator (slash) command handler,
// mirroring Python's CommandHandler.

package rrcd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

// CommandHandlerHooks wires a CommandHandler to the hub services it needs.
// The accessors re-read the live managers on every call, matching Python's
// fresh self.hub attribute reads.
type CommandHandlerHooks struct {
	// TrustManager returns the trust manager (server ops, bans, and their
	// persistence).
	TrustManager func() *TrustManager
	// SessionManager returns the session manager (target resolution and
	// session membership).
	SessionManager func() *SessionManager
	// RoomManager returns the room manager.
	RoomManager func() *RoomManager
	// MessageHelper returns the message helper (notices and errors).
	MessageHelper func() *MessageHelper
	// IdentityHash returns the hub identity hash, or nil when the hub
	// identity is unavailable.
	IdentityHash func() []byte
	// NormRoom normalizes a room name, mirroring _norm_room.
	NormRoom func(room string) (string, error)
	// ParseIdentityHash parses an identity hash token, mirroring
	// _parse_identity_hash.
	ParseIdentityHash func(text string) ([]byte, error)
	// FmtHash renders a hash with at most prefix hex characters (0 renders
	// all of it), mirroring _fmt_hash; "-" for non-byte values.
	FmtHash func(hash []byte, prefix int) string
	// ReloadConfigAndRooms performs the /reload operation.
	ReloadConfigAndRooms func(link *rns.Link, room *string, outgoing *OutgoingList)
	// FormatStats renders the /stats reply body.
	FormatStats func() string
	// RegistryPath resolves the room registry file path for writes (""
	// when unset).
	RegistryPath func() string
	// RoomInviteTimeoutS re-reads the invite TTL setting.
	RoomInviteTimeoutS func() float64
	// Now returns the current wall time in seconds (injectable in tests).
	Now func() float64
}

// CommandHandler handles operator commands for the RRC hub, mirroring
// Python's CommandHandler.
type CommandHandler struct {
	hooks CommandHandlerHooks
}

// NewCommandHandler creates a command handler wired to the given hooks.
func NewCommandHandler(hooks CommandHandlerHooks) *CommandHandler {
	return &CommandHandler{hooks: hooks}
}

// HandleOperatorCommand handles one operator command line and reports
// whether it was recognized and handled; unknown commands return false.
// It must be called with the hub state lock held.
func (c *CommandHandler) HandleOperatorCommand(link *rns.Link, peerHash []byte, room *string, text string, outgoing *OutgoingList) bool {
	cmdline := strings.TrimFunc(text, isUnicodeSpace)
	if !strings.HasPrefix(cmdline, "/") {
		return false
	}
	parts := strings.Fields(cmdline[1:])
	if len(parts) == 0 {
		return false
	}
	return false
}

// FindTargetLink finds a link by nick or identity-hash prefix, returning
// it only when exactly one session matches, mirroring _find_target_link.
func (c *CommandHandler) FindTargetLink(token string, room *string) *rns.Link {
	matches := c.FindTargetLinks(token, room)
	if len(matches) == 1 {
		return matches[0]
	}
	return nil
}

// FindTargetLinks finds every link matching a nick or identity-hash
// prefix, mirroring _find_target_links: the hex-candidate path (a token
// of at least six hex characters after an optional 0x prefix) takes
// precedence, and an odd-length candidate falls through to the nick
// index. Matches are ordered by peer-hash hex with unidentified sessions
// last (Python's match order comes from dict/set iteration order).
func (c *CommandHandler) FindTargetLinks(token string, room *string) []*rns.Link {
	t := strings.ToLower(strings.TrimFunc(token, isUnicodeSpace))
	if t == "" {
		return nil
	}
	sm := c.hooks.SessionManager()
	hexCandidate := strings.TrimPrefix(t, "0x")
	if isLowerHex(hexCandidate) && len(hexCandidate) >= 6 {
		if prefix, err := fromHexPython(hexCandidate); err == nil {
			var matches []*rns.Link
			for _, link := range sm.LinksByHashPrefix(prefix) {
				if room != nil {
					// Python skips a candidate only when its session
					// exists and lacks the room.
					if sess := sm.GetSession(link); sess != nil && !sess.Rooms[*room] {
						continue
					}
				}
				matches = append(matches, link)
			}
			c.sortMatches(matches)
			return matches
		}
	}
	var matches []*rns.Link
	for link := range sm.GetLinksByNick(t) {
		if room != nil {
			if sess := sm.GetSession(link); sess == nil || !sess.Rooms[*room] {
				continue
			}
		}
		matches = append(matches, link)
	}
	c.sortMatches(matches)
	return matches
}

// ResolveIdentityHashWithMatches resolves a token to an identity hash and
// also returns all matching links, mirroring
// _resolve_identity_hash_with_matches: one online match yields the
// session peer hash, several yield no hash, and an offline or
// unidentified token falls back to parsing.
func (c *CommandHandler) ResolveIdentityHashWithMatches(token string, room *string) ([]byte, []*rns.Link) {
	matches := c.FindTargetLinks(token, room)
	sm := c.hooks.SessionManager()
	if len(matches) == 1 {
		if sess := sm.GetSession(matches[0]); sess != nil && sess.Peer != nil {
			return sess.Peer, matches
		}
	} else if len(matches) > 1 {
		return nil, matches
	}
	h, err := c.hooks.ParseIdentityHash(token)
	if err != nil {
		return nil, nil
	}
	return h, nil
}

// isLowerHex reports whether every byte of s is a lowercase hex digit.
func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sortMatches orders target matches by peer-hash hex, unidentified
// sessions last, for determinism (Python's match order comes from
// dict/set iteration order, which Go maps do not preserve).
func (c *CommandHandler) sortMatches(matches []*rns.Link) {
	sm := c.hooks.SessionManager()
	sort.SliceStable(matches, func(i, j int) bool {
		return matchSortKey(sm, matches[i]) < matchSortKey(sm, matches[j])
	})
}

// matchSortKey renders one match's sort key: the peer hash hex, or the
// highest byte value when the session is unidentified.
func matchSortKey(sm *SessionManager, link *rns.Link) string {
	if sess := sm.GetSession(link); sess != nil && sess.Peer != nil {
		return hexKey(sess.Peer)
	}
	return "\xff"
}

// FormatAmbiguousTargets renders the message shown when a target lookup
// is ambiguous, mirroring _format_ambiguous_targets: one
// "  - {hash16} nick={nick!r}" line per identified match (Python repr
// quotes the nick), and the not-found text when nothing surfaces.
func (c *CommandHandler) FormatAmbiguousTargets(token string, matches []*rns.Link) string {
	if len(matches) == 0 {
		return fmt.Sprintf("target '%v' not found", token)
	}
	sm := c.hooks.SessionManager()
	var items []string
	for _, matchLink := range matches {
		sess := sm.GetSession(matchLink)
		if sess == nil {
			continue
		}
		hashStr := "?"
		if sess.Peer != nil {
			hashStr = c.hooks.FmtHash(sess.Peer, 16)
		}
		nickStr := "(no nick)"
		if sess.Nick != nil && *sess.Nick != "" {
			nickStr = fmt.Sprintf("nick=%v", pythonQuote(*sess.Nick))
		}
		items = append(items, fmt.Sprintf("%v %v", hashStr, nickStr))
	}
	if len(items) == 0 {
		return fmt.Sprintf("target '%v' not found", token)
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "  - "+item)
	}
	return fmt.Sprintf("ambiguous: '%v' matches %v identities:\n", token, len(items)) +
		strings.Join(lines, "\n") +
		"\nUse full or longer identity hash to disambiguate."
}
