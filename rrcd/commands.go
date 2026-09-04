// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file implements the RRC hub operator (slash) command handler,
// mirroring Python's CommandHandler.

package rrcd

import (
	"fmt"
	"maps"
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
	// Logf logs a hub message.
	Logf func(format string, args ...any)
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
	switch strings.ToLower(parts[0]) {
	case "reload":
		c.handleReload(link, peerHash, outgoing)
		return true
	case "stats":
		c.handleStats(link, peerHash, outgoing)
		return true
	case "list":
		c.handleList(link, outgoing)
		return true
	case "who", "names":
		c.handleWho(link, peerHash, parts, room, outgoing)
		return true
	case "kick":
		c.handleKick(link, peerHash, parts, room, outgoing)
		return true
	case "kline":
		c.handleKline(link, peerHash, parts, outgoing)
		return true
	case "register":
		c.handleRegister(link, peerHash, parts, room, outgoing)
		return true
	case "unregister":
		c.handleUnregister(link, peerHash, parts, room, outgoing)
		return true
	case "topic":
		c.handleTopic(link, peerHash, parts, room, outgoing)
		return true
	case "op", "deop", "voice", "devoice":
		c.handleOpVoice(link, peerHash, parts, room, outgoing)
		return true
	case "mode":
		c.handleMode(link, peerHash, parts, room, outgoing)
		return true
	case "ban":
		c.handleBan(link, peerHash, parts, room, outgoing)
		return true
	case "invite":
		c.handleInvite(link, peerHash, parts, room, outgoing)
		return true
	}
	return false
}

// handleInvite mirrors the Python /invite command: the room-op gate runs
// before the subcommand parse, the list renders sorted expiry lines, the
// add path resolves targets globally (not room-scoped), and the keyed or
// +i rooms store expiring invites.
func (c *CommandHandler) handleInvite(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 3 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil,
			"usage: /invite <room> add|del|list [nick|hashprefix|hash]")
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	if !rm.IsRoomOp(r, peerHash) {
		c.emitCommandError(outgoing, link, "not authorized", &r)
		return
	}
	op := strings.ToLower(strings.TrimFunc(parts[2], isUnicodeSpace))
	st := rm.EnsureRoomState(r, nil)
	pruned := rm.PruneExpiredInvites(r)
	invites := func() []Invite {
		if st.Invited == nil {
			st.Invited = []Invite{}
		}
		return st.Invited
	}

	if op == "list" {
		now := c.hooks.Now()
		var items []string
		for _, inv := range invites() {
			if inv.Expires <= now {
				continue
			}
			items = append(items, fmt.Sprintf("%v expires_in=%vs", hexKey(inv.Hash), int(inv.Expires-now)))
		}
		sort.Strings(items)
		if pruned {
			rm.TouchRoom(r)
			rm.PersistRoomState(link, r)
		}
		joined := "(none)"
		if len(items) > 0 {
			joined = strings.Join(items, ", ")
		}
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("invites in %v: %v", r, joined))
		return
	}
	if op != "add" && op != "del" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
			"usage: /invite <room> add|del|list [nick|hashprefix|hash]")
		return
	}
	if len(parts) < 4 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
			fmt.Sprintf("usage: /invite %v %v <nick|hashprefix|hash>", r, op))
		return
	}

	if op == "add" {
		token := parts[3]
		targetLink := c.FindTargetLink(token, nil)
		if targetLink == nil {
			allMatches := c.FindTargetLinks(token, nil)
			c.emitCommandError(outgoing, link,
				fmt.Sprintf("invite failed: %v", c.FormatAmbiguousTargets(token, allMatches)), &r)
			return
		}
		sess := c.hooks.SessionManager().GetSession(targetLink)
		if sess == nil || sess.Peer == nil {
			c.emitCommandError(outgoing, link, "invite failed: target not identified", &r)
			return
		}
		targetHash := sess.Peer
		isKeyed := st.Key != nil && *st.Key != ""
		isInviteOnly := st.InviteOnly
		if isKeyed {
			c.hooks.MessageHelper().EmitNotice(outgoing, targetLink, &r,
				fmt.Sprintf("You have been invited to join %v. This invite allows joining without the key (+k).", r))
		} else {
			c.hooks.MessageHelper().EmitNotice(outgoing, targetLink, &r, fmt.Sprintf("You have been invited to join %v.", r))
		}
		if isKeyed || isInviteOnly {
			ttl := 0.0
			if timeout := c.hooks.RoomInviteTimeoutS(); timeout != 0 {
				ttl = timeout
			}
			if ttl <= 0 {
				ttl = 900.0
			}
			exp := c.hooks.Now() + ttl
			st.Invited = append(st.Invited, Invite{Hash: targetHash, Expires: exp})
			rm.TouchRoom(r)
			rm.PersistRoomState(link, r)
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
				fmt.Sprintf("invite added in %v (expires in %vs)", r, int(ttl)))
		} else {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("invite sent to %v for %v", token, r))
		}
		return
	}

	targetHash, allMatches := c.ResolveIdentityHashWithMatches(parts[3], nil)
	if targetHash == nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, c.FormatAmbiguousTargets(parts[3], allMatches))
		return
	}
	for i, inv := range st.Invited {
		if sameBytes(inv.Hash, targetHash) {
			st.Invited = append(st.Invited[:i], st.Invited[i+1:]...)
			break
		}
	}
	rm.TouchRoom(r)
	rm.PersistRoomState(link, r)
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("invite removed in %v", r))
}

// handleBan mirrors the Python /ban command: the permission-less list,
// the room-op gate, and the force-removal of online banned members.
func (c *CommandHandler) handleBan(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 3 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil,
			"usage: /ban <room> add|del|list [nick|hashprefix|hash]")
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	op := strings.ToLower(strings.TrimFunc(parts[2], isUnicodeSpace))
	if op == "list" {
		st := rm.EnsureRoomState(r, nil)
		if len(st.Bans) == 0 {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("no bans in %v", r))
			return
		}
		items := sortedHexKeys(st.Bans)
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("bans in %v: %v", r, strings.Join(items, ", ")))
		return
	}
	if op != "add" && op != "del" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
			"usage: /ban <room> add|del|list [nick|hashprefix|hash]")
		return
	}
	if len(parts) < 4 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
			fmt.Sprintf("usage: /ban %v %v <nick|hashprefix|hash>", r, op))
		return
	}
	if !rm.IsRoomOp(r, peerHash) {
		c.emitCommandError(outgoing, link, "not authorized", &r)
		return
	}
	targetHash, allMatches := c.ResolveIdentityHashWithMatches(parts[3], &r)
	if targetHash == nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, c.FormatAmbiguousTargets(parts[3], allMatches))
		return
	}
	st := rm.EnsureRoomState(r, nil)
	if st.Bans == nil {
		st.Bans = map[string]bool{}
	}
	if op == "add" {
		st.Bans[hexKey(targetHash)] = true
		rm.TouchRoom(r)
		rm.PersistRoomState(link, r)
		sm := c.hooks.SessionManager()
		for other := range rm.GetRoomMembers(r) {
			sess := sm.GetSession(other)
			if sess == nil || sess.Peer == nil || !sameBytes(sess.Peer, targetHash) {
				continue
			}
			delete(sess.Rooms, r)
			rm.DiscardMember(r, other)
			c.emitCommandError(outgoing, other, fmt.Sprintf("banned from %v", r), &r)
		}
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("ban added in %v", r))
		return
	}
	delete(st.Bans, hexKey(targetHash))
	rm.TouchRoom(r)
	rm.PersistRoomState(link, r)
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("ban removed in %v", r))
}

// handleOpVoice mirrors the Python /op, /deop, /voice, and /devoice
// commands: the room-op gate, the room-scoped target resolution, the
// founder deop guard, and the ops/voiced set updates.
func (c *CommandHandler) handleOpVoice(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	cmd := strings.ToLower(parts[0])
	if len(parts) < 3 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil,
			fmt.Sprintf("usage: /%v <room> <nick|hashprefix|hash>", cmd))
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	if !rm.IsRoomOp(r, peerHash) {
		c.emitCommandError(outgoing, link, "not authorized", &r)
		return
	}
	targetHash, allMatches := c.ResolveIdentityHashWithMatches(parts[2], &r)
	if targetHash == nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, c.FormatAmbiguousTargets(parts[2], allMatches))
		return
	}
	st := rm.EnsureRoomState(r, nil)
	touch := func() {
		rm.TouchRoom(r)
		rm.PersistRoomState(link, r)
	}
	if cmd == "op" || cmd == "deop" {
		if st.Ops == nil {
			st.Ops = map[string]bool{}
		}
		if cmd == "op" {
			st.Ops[hexKey(targetHash)] = true
			touch()
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("op granted in %v", r))
			return
		}
		if st.Founder != nil && sameBytes(st.Founder, targetHash) {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "cannot deop founder")
			return
		}
		delete(st.Ops, hexKey(targetHash))
		touch()
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("op removed in %v", r))
		return
	}
	if st.Voiced == nil {
		st.Voiced = map[string]bool{}
	}
	if cmd == "voice" {
		st.Voiced[hexKey(targetHash)] = true
		touch()
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("voice granted in %v", r))
		return
	}
	delete(st.Voiced, hexKey(targetHash))
	touch()
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("voice removed in %v", r))
}

// modeUsageText is the /mode usage line.
const modeUsageText = "usage: /mode <room> (+m|-m|+i|-i|+t|-t|+n|-n|+p|-p|+k|-k|+r|-r) [key] | /mode <room> (+o|-o|+v|-v) <nick|hashprefix|hash>"

// handleMode mirrors the Python /mode command: the simple flag toggles
// with the room-mode broadcast, the +k key handling, the +r redirection,
// and the ±o/±v member fanout.
func (c *CommandHandler) handleMode(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 3 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, modeUsageText)
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	if !rm.IsRoomOp(r, peerHash) {
		c.emitCommandError(outgoing, link, "not authorized", &r)
		return
	}
	flag := strings.ToLower(strings.TrimFunc(parts[2], isUnicodeSpace))
	st := rm.EnsureRoomState(r, nil)
	touch := func() {
		rm.TouchRoom(r)
		rm.PersistRoomState(link, r)
	}

	switch flag {
	case "+m", "-m":
		st.Moderated = flag == "+m"
		touch()
		rm.BroadcastRoomMode(r, outgoing)
		return
	case "+i", "-i":
		st.InviteOnly = flag == "+i"
		touch()
		rm.BroadcastRoomMode(r, outgoing)
		return
	case "+t", "-t":
		st.TopicOpsOnly = flag == "+t"
		touch()
		rm.BroadcastRoomMode(r, outgoing)
		return
	case "+n", "-n":
		st.NoOutsideMsgs = flag == "+n"
		touch()
		rm.BroadcastRoomMode(r, outgoing)
		return
	case "+p", "-p":
		st.Private = flag == "+p"
		touch()
		rm.BroadcastRoomMode(r, outgoing)
		return
	case "+k", "-k":
		if flag == "+k" {
			if len(parts) < 4 {
				c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "usage: /mode <room> +k <key>")
				return
			}
			key := strings.TrimSpace(strings.Join(parts[3:], " "))
			if key == "" {
				c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "key must not be empty")
				return
			}
			st.Key = &key
		} else {
			st.Key = nil
		}
		touch()
		rm.BroadcastRoomMode(r, outgoing)
		return
	case "+r", "-r":
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "use /register or /unregister to change +r")
		return
	case "+o", "-o", "+v", "-v":
		if len(parts) < 4 {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
				"usage: /mode <room> (+o|-o|+v|-v) <nick|hashprefix|hash>")
			return
		}
		targetHash, allMatches := c.ResolveIdentityHashWithMatches(parts[3], &r)
		if targetHash == nil {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, room, c.FormatAmbiguousTargets(parts[3], allMatches))
			return
		}
		if flag == "+o" || flag == "-o" {
			if st.Ops == nil {
				st.Ops = map[string]bool{}
			}
			if flag == "+o" {
				st.Ops[hexKey(targetHash)] = true
			} else {
				if st.Founder != nil && sameBytes(st.Founder, targetHash) {
					c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "cannot deop founder")
					return
				}
				delete(st.Ops, hexKey(targetHash))
			}
		} else {
			if st.Voiced == nil {
				st.Voiced = map[string]bool{}
			}
			if flag == "+v" {
				st.Voiced[hexKey(targetHash)] = true
			} else {
				delete(st.Voiced, hexKey(targetHash))
			}
		}
		touch()
		memberNotice := fmt.Sprintf("mode for %v is now: %v %v", r, flag, hexKey(targetHash)[:12])
		for other := range rm.GetRoomMembers(r) {
			c.hooks.MessageHelper().EmitNotice(outgoing, other, &r, memberNotice)
		}
		return
	}
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room,
		"supported modes: +m -m +i -i +k -k +t -t +n -n +p -p +r -r +o -o +v -v")
}

// handleTopic mirrors the Python /topic command: the permission-less
// view, the +t gate for non-ops, and the member fanout on set.
func (c *CommandHandler) handleTopic(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 2 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "usage: /topic <room> [topic]")
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	st := rm.EnsureRoomState(r, nil)
	if len(parts) == 2 {
		topicText := "(none)"
		if st.Topic != nil && *st.Topic != "" {
			topicText = *st.Topic
		}
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("topic for %v: %v", r, topicText))
		return
	}
	if !rm.IsRoomOp(r, peerHash) {
		st = rm.EnsureRoomState(r, nil)
		if st.TopicOpsOnly {
			c.emitCommandError(outgoing, link, "not authorized (+t)", &r)
			return
		}
	}
	topic := strings.TrimSpace(strings.Join(parts[2:], " "))
	if topic != "" {
		st.Topic = &topic
	} else {
		st.Topic = nil
	}
	rm.TouchRoom(r)
	rm.PersistRoomState(link, r)
	noticeText := fmt.Sprintf("topic for %v is now: %v", r, topic)
	if topic == "" {
		noticeText = fmt.Sprintf("topic for %v is now: (cleared)", r)
	}
	for other := range rm.GetRoomMembers(r) {
		c.hooks.MessageHelper().EmitNotice(outgoing, other, &r, noticeText)
	}
}

// handleUnregister mirrors the Python /unregister command: the presence
// check against the raw command room, the founder-only gate, and the
// registry/state teardown (the state is popped only when the room has no
// members).
func (c *CommandHandler) handleUnregister(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 2 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "usage: /unregister <room>")
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	sm := c.hooks.SessionManager()
	// The presence check normalizes the raw command room; a normalization
	// failure (e.g. an over-long raw room) aborts packet processing
	// silently, mirroring the exception Python raises out of the dispatch.
	if room == nil || *room == "" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "must be present in the room to unregister it")
		return
	}
	normCmdRoom, err := c.hooks.NormRoom(*room)
	if err != nil {
		c.logf("Unregister presence check failed on raw room: %v", err)
		return
	}
	sess := sm.GetSession(link)
	if normCmdRoom != r || sess == nil || !sess.Rooms[r] {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "must be present in the room to unregister it")
		return
	}

	st := rm.EnsureRoomState(r, nil)
	if st.Founder == nil || !sameBytes(st.Founder, peerHash) {
		c.emitCommandError(outgoing, link, "only the room founder can unregister", &r)
		return
	}
	if !st.Registered {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("room %v is not registered", r))
		return
	}
	st.Registered = false
	rm.RegistryDelete(r)
	rm.DeleteRoomFromRegistry(link, r)
	if len(rm.GetRoomMembers(r)) == 0 {
		rm.StateDelete(r)
	}
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("unregistered room %v", r))
}

// emitCommandError emits an ERROR with the hub identity as src, skipping
// the send when the hub identity is unavailable, mirroring the repeated
// `if self.hub.identity is not None: self._emit_error(...)` guards.
func (c *CommandHandler) emitCommandError(outgoing *OutgoingList, link *rns.Link, text string, room *string) {
	if idHash := c.hooks.IdentityHash(); idHash != nil {
		c.hooks.MessageHelper().EmitError(outgoing, link, idHash, text, room)
	}
}

// logf logs through the hooks when wired.
func (c *CommandHandler) logf(format string, args ...any) {
	if c.hooks.Logf != nil {
		c.hooks.Logf(format, args...)
	}
}

// handleKline mirrors the Python /kline command: the server-op gate, the
// list rendering, and the add/del ban management with the kline-union
// persistence.
func (c *CommandHandler) handleKline(link *rns.Link, peerHash []byte, parts []string, outgoing *OutgoingList) {
	if !c.isServerOp(peerHash) {
		c.emitNotAuthorized(outgoing, link, nil)
		return
	}
	tm := c.hooks.TrustManager()
	usage := "usage: /kline add|del|list [nick|hashprefix|hash]"
	if len(parts) < 2 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, usage)
		return
	}
	op := strings.ToLower(strings.TrimFunc(parts[1], isUnicodeSpace))
	if op == "list" {
		items := append([]string{}, tm.BannedHexList()...)
		sort.Strings(items)
		joined := "(none)"
		if len(items) > 0 {
			joined = strings.Join(items, ", ")
		}
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "klines: "+joined)
		return
	}
	if op != "add" && op != "del" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, usage)
		return
	}
	if len(parts) < 3 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil,
			fmt.Sprintf("usage: /kline %v <nick|hashprefix|hash>", op))
		return
	}
	target := parts[2]
	if op == "add" {
		targetLink := c.FindTargetLink(target, nil)
		if targetLink != nil {
			sess := c.hooks.SessionManager().GetSession(targetLink)
			if sess != nil && sess.Peer != nil {
				tm.AddBan(sess.Peer)
				tm.PersistBannedIdentitiesToConfig(link, "")
			}
			// Teardown failures are ignored, mirroring the bare
			// try/except in Python.
			targetLink.Teardown()
			c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("kline added for %v", target))
			return
		}
		allMatches := c.FindTargetLinks(target, nil)
		if len(allMatches) > 0 {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, c.FormatAmbiguousTargets(target, allMatches))
			return
		}
		h, err := c.hooks.ParseIdentityHash(target)
		if err != nil {
			c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad identity hash: %v", err))
			return
		}
		tm.AddBan(h)
		tm.PersistBannedIdentitiesToConfig(link, "")
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("kline added for %v", hexKey(h)))
		return
	}
	h, err := c.hooks.ParseIdentityHash(target)
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad identity hash: %v", err))
		return
	}
	if tm.IsBanned(h) {
		tm.RemoveBan(h)
		tm.PersistBannedIdentitiesToConfig(link, "")
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("kline removed for %v", hexKey(h)))
	} else {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("not klined: %v", hexKey(h)))
	}
}

// handleRegister mirrors the Python /register command: the presence
// check against the raw command room, the founder-only gate, and the
// eight-field registry copy.
func (c *CommandHandler) handleRegister(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 2 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "usage: /register <room>")
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	sm := c.hooks.SessionManager()
	// The presence check normalizes the raw command room; a normalization
	// failure (e.g. an over-long raw room) aborts packet processing
	// silently, mirroring the exception Python raises out of the dispatch.
	if room == nil || *room == "" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "must be present in the room to register it")
		return
	}
	normCmdRoom, err := c.hooks.NormRoom(*room)
	if err != nil {
		c.logf("Register presence check failed on raw room: %v", err)
		return
	}
	sess := sm.GetSession(link)
	if normCmdRoom != r || sess == nil || !sess.Rooms[r] {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "must be present in the room to register it")
		return
	}

	st := rm.EnsureRoomState(r, nil)
	if rm.PruneExpiredInvites(r) && st.Registered {
		rm.PersistRoomState(link, r)
	}
	if st.Founder == nil || !sameBytes(st.Founder, peerHash) {
		c.emitCommandError(outgoing, link, "only the room founder can register", &r)
		return
	}
	if c.hooks.RegistryPath() == "" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "cannot register room: no room_registry_path")
		return
	}
	st.Registered = true
	st.NoOutsideMsgs = true
	st.TopicOpsOnly = true
	if st.Founder != nil {
		if st.Ops == nil {
			st.Ops = map[string]bool{}
		}
		st.Ops[hexKey(st.Founder)] = true
	}
	rm.TouchRoom(r)
	rm.RegistrySet(r, &RoomState{
		Founder:    st.Founder,
		Registered: true,
		Topic:      st.Topic,
		Moderated:  st.Moderated,
		Ops:        copyHexSet(st.Ops),
		Voiced:     copyHexSet(st.Voiced),
		Bans:       copyHexSet(st.Bans),
		LastUsedTS: st.LastUsedTS,
	})
	rm.PersistRoomState(link, r)
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("registered room %v", r))
}

// copyHexSet copies a hex-keyed set, mirroring Python's set() copies.
func copyHexSet(set map[string]bool) map[string]bool {
	out := make(map[string]bool, len(set))
	maps.Copy(out, set)
	return out
}

// handleKick mirrors the Python /kick command: the room-op gate, the
// room-scoped target resolution, the in-room check, and the force-removal
// that bypasses remove_member.
func (c *CommandHandler) handleKick(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	if len(parts) < 3 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "usage: /kick <room> <nick|hashprefix>")
		return
	}
	r, err := c.hooks.NormRoom(parts[1])
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("bad room: %v", err))
		return
	}
	rm := c.hooks.RoomManager()
	if !rm.IsRoomOp(r, peerHash) {
		c.emitNotAuthorized(outgoing, link, &r)
		return
	}
	target := parts[2]
	targetLink := c.FindTargetLink(target, &r)
	if targetLink == nil {
		allMatches := c.FindTargetLinks(target, &r)
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, c.FormatAmbiguousTargets(target, allMatches))
		return
	}
	sm := c.hooks.SessionManager()
	sess := sm.GetSession(targetLink)
	if sess == nil || !sess.Rooms[r] {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, room, "target not in room")
		return
	}
	delete(sess.Rooms, r)
	if len(rm.GetRoomMembers(r)) > 0 {
		rm.DiscardMember(r, targetLink)
	}
	if idHash := c.hooks.IdentityHash(); idHash != nil {
		c.hooks.MessageHelper().EmitError(outgoing, targetLink, idHash, fmt.Sprintf("kicked from %v", r), &r)
	}
	c.hooks.MessageHelper().EmitNotice(outgoing, link, room, fmt.Sprintf("kicked %v from %v", target, r))
}

// isServerOp reports the server-op gate result; callers emit their own
// not-authorized errors.
func (c *CommandHandler) isServerOp(peerHash []byte) bool {
	return c.hooks.TrustManager().IsServerOp(peerHash)
}

// emitNotAuthorized emits the room-nil or room-scoped not-authorized
// ERROR when the hub identity is available, mirroring the repeated
// `if self.hub.identity is not None` guards in Python.
func (c *CommandHandler) emitNotAuthorized(outgoing *OutgoingList, link *rns.Link, room *string) {
	if idHash := c.hooks.IdentityHash(); idHash != nil {
		c.hooks.MessageHelper().EmitError(outgoing, link, idHash, "not authorized", room)
	}
}

// handleReload mirrors the Python /reload command: the server-op gate
// emits a room-nil `not authorized` ERROR, otherwise the reload hook runs
// with room=nil.
func (c *CommandHandler) handleReload(link *rns.Link, peerHash []byte, outgoing *OutgoingList) {
	if !c.isServerOp(peerHash) {
		c.emitNotAuthorized(outgoing, link, nil)
		return
	}
	c.hooks.ReloadConfigAndRooms(link, nil, outgoing)
}

// handleStats mirrors the Python /stats command: the server-op gate emits
// a room-nil `not authorized` ERROR, otherwise the formatted stats body is
// emitted as a room-nil NOTICE.
func (c *CommandHandler) handleStats(link *rns.Link, peerHash []byte, outgoing *OutgoingList) {
	if !c.isServerOp(peerHash) {
		c.emitNotAuthorized(outgoing, link, nil)
		return
	}
	c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, c.hooks.FormatStats())
}

// handleList mirrors the Python /list command: registered public rooms
// collected from the live state plus registry-only rooms, sorted by name,
// rendered as one NOTICE, with the no-rooms fallback.
func (c *CommandHandler) handleList(link *rns.Link, outgoing *OutgoingList) {
	rooms := c.hooks.RoomManager().RegisteredPublicRooms()
	if len(rooms) == 0 {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "No public rooms registered")
		return
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].Name < rooms[j].Name })
	lines := make([]string, 0, len(rooms)+1)
	lines = append(lines, "Registered public rooms:")
	for _, room := range rooms {
		if room.Topic != nil && *room.Topic != "" {
			lines = append(lines, fmt.Sprintf("  %v - %v", room.Name, *room.Topic))
		} else {
			lines = append(lines, "  "+room.Name)
		}
	}
	c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, strings.Join(lines, "\n"))
}

// handleWho mirrors the Python /who (and /names) command: the target room
// defaults to the command room, the private-room check hides members from
// non-ops, and the member line renders `nick ({hash12})` or bare hash
// entries.
func (c *CommandHandler) handleWho(link *rns.Link, peerHash []byte, parts []string, room *string, outgoing *OutgoingList) {
	targetRoom := room
	if len(parts) >= 2 {
		targetRoom = &parts[1]
	}
	if targetRoom == nil || *targetRoom == "" {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, "usage: /who [room]")
		return
	}
	r, err := c.hooks.NormRoom(*targetRoom)
	if err != nil {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("bad room: %v", err))
		return
	}
	st := c.hooks.RoomManager().RoomStateGet(r)
	if st != nil && st.Private && !c.isServerOp(peerHash) {
		c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("room %v is private", r))
		return
	}

	rm := c.hooks.RoomManager()
	sm := c.hooks.SessionManager()
	memberLinks := mapKeys(rm.GetRoomMembers(r))
	sort.SliceStable(memberLinks, func(i, j int) bool {
		return whoMemberKey(sm, memberLinks[i]) < whoMemberKey(sm, memberLinks[j])
	})
	members := make([]string, 0, len(memberLinks))
	for _, other := range memberLinks {
		sess := sm.GetSession(other)
		if sess == nil {
			continue
		}
		ident := "?"
		if sess.Peer != nil {
			ident = hexKey(sess.Peer)
		}
		if sess.Nick != nil && *sess.Nick != "" {
			prefix := ident
			if len(prefix) > 12 {
				prefix = prefix[:12]
			}
			members = append(members, fmt.Sprintf("%v (%v)", *sess.Nick, prefix))
		} else {
			members = append(members, ident)
		}
	}
	joined := "(none)"
	if len(members) > 0 {
		joined = strings.Join(members, ", ")
	}
	c.hooks.MessageHelper().EmitNotice(outgoing, link, nil, fmt.Sprintf("members in %v: %v", r, joined))
}

// whoMemberKey orders /who members: identified sessions first by peer
// hash, unidentified sessions after in registration order. Python sorts
// by object id(), which is arbitrary; this key is deterministic.
func whoMemberKey(sm *SessionManager, link *rns.Link) string {
	if sess := sm.GetSession(link); sess != nil && sess.Peer != nil {
		return "0" + hexKey(sess.Peer)
	}
	return "1" + fmt.Sprintf("%012x", sm.SessionOrderIndex(link))
}

// mapKeys converts a set map to a slice.
func mapKeys[T comparable](set map[T]bool) []T {
	out := make([]T, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	return out
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
