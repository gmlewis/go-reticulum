// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// RouterHooks wires a Router to the hub services it needs. The config
// accessors re-read the live configuration on every call, matching
// Python's fresh self.hub.config reads.
type RouterHooks struct {
	// Sessions returns the session manager.
	Sessions func() *SessionManager
	// RoomManager returns the room manager.
	RoomManager func() *RoomManager
	// TrustManager returns the trust manager.
	TrustManager func() *TrustManager
	// StatsInc increments a stats counter.
	StatsInc func(key string, delta int)
	// IdentityHash returns the hub identity hash, or nil when the hub
	// identity is unavailable.
	IdentityHash func() []byte
	// EnableResourceTransfer reports the resource-transfer setting.
	EnableResourceTransfer func() bool
	// MaxResourceBytes returns the configured resource size limit.
	MaxResourceBytes func() int
	// MaxNickBytes returns the configured nick limit.
	MaxNickBytes func() int
	// MaxRoomNameBytes returns the configured room-name byte limit.
	MaxRoomNameBytes func() int
	// MaxRoomsPerSession returns the per-session room cap.
	MaxRoomsPerSession func() int
	// MaxMsgBodyBytes returns the message body byte limit.
	MaxMsgBodyBytes func() int
	// IncludeJoinedMemberList reports the include_joined_member_list
	// setting.
	IncludeJoinedMemberList func() bool
	// FmtHash formats a hash for logs.
	FmtHash func(hash []byte) string
	// FmtLinkID formats a link id for logs.
	FmtLinkID func(link *rns.Link) string
	// DebugEnabled reports whether debug logging is enabled.
	DebugEnabled func() bool
	// Debugf logs a debug message.
	Debugf func(format string, args ...any)
	// Infof logs an informational message.
	Infof func(format string, args ...any)
	// SendPacket sends a payload to a link immediately.
	SendPacket func(link *rns.Link, payload []byte) error
	// PersistRoomState persists a registered room's state.
	PersistRoomState func(link *rns.Link, room string)
	// QueuePayload queues a raw payload for a link.
	QueuePayload func(outgoing *OutgoingList, link *rns.Link, payload []byte)
	// QueueEnv encodes an envelope and queues it.
	QueueEnv func(outgoing *OutgoingList, link *rns.Link, env *cbor.Map)
	// EmitNotice emits a notice message (queued or immediate).
	EmitNotice func(outgoing *OutgoingList, link *rns.Link, room *string, text string)
	// EmitError emits an error message (queued or immediate).
	EmitError func(outgoing *OutgoingList, link *rns.Link, src []byte, text string, room *string)
	// AddResourceExpectation registers a pending resource transfer and
	// reports whether capacity remained.
	AddResourceExpectation func(link *rns.Link, rid []byte, kind string, size int, sha256 []byte, encoding string, room *string) bool
	// HandleOperatorCommand runs a slash command and reports whether it
	// was recognized and handled.
	HandleOperatorCommand func(link *rns.Link, peerHash []byte, room *string, text string, outgoing *OutgoingList) bool
	// SendWelcome queues the WELCOME message and the optional MOTD
	// delivery.
	SendWelcome func(link *rns.Link, outgoing *OutgoingList, peerHash []byte, oldNick, newNick *string)
}

// Router decodes, validates, and dispatches incoming RRC packets for the
// hub, mirroring Python's MessageRouter.
type Router struct {
	hooks RouterHooks
}

// NewRouter creates a router wired to the given hooks.
func NewRouter(hooks RouterHooks) *Router {
	return &Router{hooks: hooks}
}

// RoutePacket is the main entry point for routing an incoming packet:
// it decodes and validates the RRC envelope and dispatches it by message
// type. It must be called with the hub state lock held.
func (r *Router) RoutePacket(link *rns.Link, data []byte, outgoing *OutgoingList) {
	sess := r.hooks.Sessions().GetSession(link)
	if sess == nil {
		return
	}

	r.hooks.StatsInc("pkts_in", 1)
	r.hooks.StatsInc("bytes_in", len(data))

	peerHash := sess.Peer
	if peerHash == nil {
		ri := link.GetRemoteIdentity()
		if ri == nil {
			return
		}
		peerHash = ri.Hash
		sess.Peer = peerHash
	}

	if !r.hooks.Sessions().RefillAndTake(link, 1.0) {
		r.hooks.StatsInc("rate_limited", 1)
		if r.hooks.DebugEnabled() {
			r.hooks.Debugf("Rate limited peer=%v link_id=%v",
				r.hooks.FmtHash(peerHash), r.hooks.FmtLinkID(link))
		}
		if idHash := r.hooks.IdentityHash(); idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "rate limited", nil)
		}
		return
	}

	env, err := decodeEnvelope(data)
	if err != nil {
		r.hooks.StatsInc("pkts_bad", 1)
		if r.hooks.DebugEnabled() {
			r.hooks.Debugf("Bad packet peer=%v link_id=%v bytes=%v err=%v",
				r.hooks.FmtHash(peerHash), r.hooks.FmtLinkID(link), len(data), err)
		}
		if idHash := r.hooks.IdentityHash(); idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, fmt.Sprintf("bad message: %v", err), nil)
		}
		return
	}

	tAny, _ := env.Get(KT)
	t, tIsInt := intValue(tAny)
	roomVal, _ := env.Get(KRoom)
	body, _ := env.Get(KBody)

	if r.hooks.DebugEnabled() {
		r.hooks.Debugf("RX peer=%v link_id=%v t=%v room=%v bytes=%v body_type=%v body_len=%v",
			r.hooks.FmtHash(peerHash), r.hooks.FmtLinkID(link), pythonRepr(tAny),
			roomRepr(roomVal), len(data), pythonTypeName(body), bodyLenRepr(body))
	}

	switch {
	case tIsInt && t == TPong:
		r.handlePong(sess)
	case tIsInt && t == TResource:
		r.handleResourceEnvelope(link, sess, env, outgoing)
	case !sess.Welcomed:
		r.handlePreWelcome(link, sess, peerHash, env, outgoing)
	case tIsInt && t == THello:
		r.handleReHello(link, sess, peerHash, env, outgoing)
	case tIsInt && t == TJoin:
		r.handleJoin(link, sess, peerHash, env, outgoing)
	case tIsInt && t == TPart:
		r.handlePart(link, sess, peerHash, env, outgoing)
	case tIsInt && (t == TMsg || t == TNotice || t == TAction):
		r.handleMessage(link, sess, peerHash, env, outgoing)
	case tIsInt && t == TPing:
		r.handlePing(link, env, outgoing)
	}
}

// handlePong mirrors _handle_pong: the pending-pong marker is cleared
// and no reply is produced.
func (r *Router) handlePong(sess *Session) {
	r.hooks.StatsInc("pongs_in", 1)
	sess.AwaitingPong = nil
}

// handleResourceEnvelope mirrors _handle_resource_envelope: the
// resource-transfer gate, the step-by-step body validation with its
// verbatim error texts, and finally the pending-expectation
// registration whose room carries the envelope's raw K_ROOM value.
func (r *Router) handleResourceEnvelope(link *rns.Link, sess *Session, env *cbor.Map, outgoing *OutgoingList) {
	room := roomPtrOf(mustEnvGet(env, KRoom))
	body := mustEnvGet(env, KBody)
	idHash := r.hooks.IdentityHash()

	if !r.hooks.EnableResourceTransfer() {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "resource transfer disabled", room)
		}
		return
	}

	bodyMap, isMap := body.(*cbor.Map)
	if !isMap {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "invalid resource envelope body", room)
		}
		return
	}

	ridVal, _ := bodyMap.Get(BResID)
	rid, ridIsBytes := ridVal.([]byte)
	if !ridIsBytes {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "resource envelope missing id", room)
		}
		return
	}

	kindVal, _ := bodyMap.Get(BResKind)
	kind, kindIsStr := kindVal.(string)
	if !kindIsStr || kind == "" {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "resource envelope missing kind", room)
		}
		return
	}

	sizeVal, _ := bodyMap.Get(BResSize)
	size, sizeIsInt := intValue(sizeVal)
	if !sizeIsInt {
		// Python ints are unbounded; a CBOR uint64 beyond the int64
		// range is still an int there and can only fail the size cap.
		if _, isWide := sizeVal.(uint64); isWide {
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash,
					fmt.Sprintf("resource too large: %v > %v", pythonRepr(sizeVal), r.hooks.MaxResourceBytes()), room)
			}
			return
		}
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "resource envelope invalid size", room)
		}
		return
	}
	if size < 0 {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "resource envelope invalid size", room)
		}
		return
	}

	maxResource := r.hooks.MaxResourceBytes()
	if size > int64(maxResource) {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash,
				fmt.Sprintf("resource too large: %v > %v", pythonRepr(sizeVal), maxResource), room)
		}
		return
	}

	var sha256 []byte
	if shaVal, ok := bodyMap.Get(BResSHA256); ok && shaVal != nil {
		shaBytes, shaIsBytes := shaVal.([]byte)
		if !shaIsBytes {
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash, "resource envelope invalid sha256", room)
			}
			return
		}
		sha256 = shaBytes
	}

	// A present but non-string encoding is silently dropped, mirroring
	// Python's reassignment to None.
	var encoding string
	if encVal, ok := bodyMap.Get(BResEncoding); ok {
		if encStr, encIsStr := encVal.(string); encIsStr {
			encoding = encStr
		}
	}

	var shaArg []byte
	if len(sha256) > 0 {
		// Python passes bytes(sha256) if sha256 else None: an empty
		// byte string is falsy there and becomes None.
		shaArg = sha256
	}
	if !r.hooks.AddResourceExpectation(link, rid, kind, int(size), shaArg, encoding, room) {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "too many pending resource expectations", room)
		}
	}
}

// handlePreWelcome mirrors _handle_pre_welcome: only HELLO is allowed
// before the WELCOME; the nick and capabilities are learned and the
// WELCOME is sent.
func (r *Router) handlePreWelcome(link *rns.Link, sess *Session, peerHash []byte, env *cbor.Map, outgoing *OutgoingList) {
	tAny, _ := env.Get(KT)
	t, tIsInt := intValue(tAny)
	idHash := r.hooks.IdentityHash()

	if !tIsInt || t != THello {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "send HELLO first", nil)
		}
		return
	}

	oldNick := sess.Nick
	newNick := r.learnHelloIdentity(sess, env)

	r.hooks.Infof("HELLO peer=%v nick=%v link_id=%v",
		r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), r.hooks.FmtLinkID(link))

	r.hooks.SendWelcome(link, outgoing, peerHash, oldNick, newNick)
}

// handleReHello mirrors _handle_re_hello: a HELLO on an already welcomed
// link resets the session state, leaves every old room without PARTED
// notifications, re-learns the nick and capabilities, and re-sends the
// WELCOME.
func (r *Router) handleReHello(link *rns.Link, sess *Session, peerHash []byte, env *cbor.Map, outgoing *OutgoingList) {
	idHash := r.hooks.IdentityHash()
	if idHash == nil {
		return
	}

	oldNick := sess.Nick
	oldRooms := make([]string, 0, len(sess.Rooms))
	for room := range sess.Rooms {
		oldRooms = append(oldRooms, room)
	}
	sess.Welcomed = false
	sess.Rooms = map[string]bool{}
	sess.Nick = nil
	sess.PeerCaps = map[int64]any{}

	for _, room := range oldRooms {
		r.hooks.RoomManager().RemoveMember(room, link)
	}

	newNick := r.learnHelloIdentity(sess, env)

	r.hooks.Infof("Re-HELLO peer=%v nick=%v link_id=%v",
		r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), r.hooks.FmtLinkID(link))

	r.hooks.SendWelcome(link, outgoing, peerHash, oldNick, newNick)
}

// learnHelloIdentity applies the shared HELLO nick-and-caps learning
// block of the two HELLO handlers: an envelope nick (falling back to the
// legacy body key only when the envelope nick did not yield a valid
// nick) is normalized into the session, and the capability map is
// extracted into the session. It returns the newly adopted nick, or nil
// when none was adopted.
func (r *Router) learnHelloIdentity(sess *Session, env *cbor.Map) *string {
	var newNick *string
	if nick, ok := EnvGetString(env, KNick); ok {
		if n := NormalizeNick(nick, r.hooks.MaxNickBytes()); n != "" {
			newNick = &n
			sess.Nick = &n
		}
	}
	if body, ok := EnvGetMap(env, KBody); ok {
		sess.PeerCaps = r.extractCaps(body)
		if newNick == nil {
			if legacy, ok := body.Get(BHelloNickLegacy); ok {
				if legacyStr, isStr := legacy.(string); isStr {
					if n := NormalizeNick(legacyStr, r.hooks.MaxNickBytes()); n != "" {
						newNick = &n
						sess.Nick = &n
					}
				}
			}
		}
	}
	return newNick
}

// handleJoin mirrors _handle_join: the room-name and rooms-cap gates,
// room normalization, the invite-only, key, and ban gates, the founder
// bootstrap, the JOINED fan out to existing members and to the joiner,
// invite consumption, and the room info notice.
func (r *Router) handleJoin(link *rns.Link, sess *Session, peerHash []byte, env *cbor.Map, outgoing *OutgoingList) {
	rm := r.hooks.RoomManager()
	idHash := r.hooks.IdentityHash()
	roomVal, _ := env.Get(KRoom)
	body, _ := env.Get(KBody)

	r.hooks.StatsInc("joins", 1)

	room, roomIsStr := "", false
	if roomStr, isStr := roomVal.(string); isStr {
		room, roomIsStr = roomStr, true
	}
	if !roomIsStr || room == "" {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "JOIN requires room name", nil)
		}
		return
	}

	if len(sess.Rooms) >= r.hooks.MaxRoomsPerSession() {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "too many rooms", nil)
		}
		return
	}

	nr, err := r.normRoom(room)
	if err != nil {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, err.Error(), nil)
		}
		return
	}

	if _, ok := rm.RegistryGet(nr); ok {
		rm.EnsureRoomState(nr, nil)
	}
	st := rm.EnsureRoomState(nr, nil)

	if st.InviteOnly {
		isInvited := rm.IsInvited(nr, peerHash)
		if !rm.IsRoomOp(nr, peerHash) && !isInvited {
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash, "invite-only (+i)", &nr)
			}
			return
		}
	}

	if st.Key != nil && *st.Key != "" {
		isInvited := rm.IsInvited(nr, peerHash)
		if !rm.IsRoomOp(nr, peerHash) && !isInvited {
			provided, hasProvided := "", false
			if bodyStr, isStr := body.(string); isStr {
				provided, hasProvided = bodyStr, true
			}
			if !hasProvided || provided != *st.Key {
				if idHash != nil {
					r.hooks.EmitError(outgoing, link, idHash, "bad key (+k)", &nr)
				}
				return
			}
		}
	}

	if rm.IsRoomBanned(nr, peerHash) {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "banned from room", &nr)
		}
		return
	}

	if len(rm.GetRoomMembers(nr)) == 0 {
		rm.EnsureRoomState(nr, peerHash)
	}

	sess.Rooms[nr] = true
	rm.AddMember(nr, link, nil)

	r.hooks.Infof("JOIN peer=%v nick=%v room=%v link_id=%v",
		r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), nr, r.hooks.FmtLinkID(link))

	rm.TouchRoom(nr)

	var existingMembers []*rns.Link
	for memberLink := range rm.GetRoomMembers(nr) {
		if memberLink != link {
			existingMembers = append(existingMembers, memberLink)
		}
	}
	if len(existingMembers) > 0 && idHash != nil {
		var notificationBody any
		if r.hooks.IncludeJoinedMemberList() {
			notificationBody = []any{peerHash}
		}
		memberNotification := MakeEnvelope(int(TJoined), idHash,
			WithRoom(nr), WithBody(notificationBody), WithNick(nickDeref(sess.Nick)))
		memberNotificationPayload := cbor.Encode(memberNotification)
		for _, memberLink := range existingMembers {
			r.hooks.QueuePayload(outgoing, memberLink, memberNotificationPayload)
		}
	}

	var joinedBody any
	if r.hooks.IncludeJoinedMemberList() {
		members := []any{}
		for memberLink := range rm.GetRoomMembers(nr) {
			if memberSession := r.hooks.Sessions().GetSession(memberLink); memberSession != nil && memberSession.Peer != nil {
				members = append(members, memberSession.Peer)
			}
		}
		joinedBody = members
	}
	if idHash != nil {
		joined := MakeEnvelope(int(TJoined), idHash, WithRoom(nr), WithBody(joinedBody))
		r.hooks.QueueEnv(outgoing, link, joined)
	}

	for i, invite := range st.Invited {
		if string(invite.Hash) == string(peerHash) {
			st.Invited = append(st.Invited[:i], st.Invited[i+1:]...)
			if st.Registered {
				r.hooks.PersistRoomState(link, nr)
			}
			break
		}
	}

	regTxt := "unregistered"
	if st.Registered {
		regTxt = "registered"
	}
	topicTxt := "(none)"
	if st.Topic != nil && *st.Topic != "" {
		topicTxt = *st.Topic
	}
	r.hooks.EmitNotice(outgoing, link, &nr, fmt.Sprintf("room %v: %v; mode=%v; topic=%v",
		nr, regTxt, rm.GetRoomModeString(nr), topicTxt))
}

// handlePart mirrors _handle_part: the room is left unconditionally,
// empty unregistered rooms are cleaned up, PARTED is fanned out to the
// remaining members (suppressed when the same peer remains in the room
// via another link), and PARTED is queued to the parting client.
func (r *Router) handlePart(link *rns.Link, sess *Session, peerHash []byte, env *cbor.Map, outgoing *OutgoingList) {
	rm := r.hooks.RoomManager()
	idHash := r.hooks.IdentityHash()
	roomVal, _ := env.Get(KRoom)

	r.hooks.StatsInc("parts", 1)

	room, roomIsStr := "", false
	if roomStr, isStr := roomVal.(string); isStr {
		room, roomIsStr = roomStr, true
	}
	if !roomIsStr || room == "" {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "PART requires room name", nil)
		}
		return
	}

	nr, err := r.normRoom(room)
	if err != nil {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, err.Error(), nil)
		}
		return
	}

	delete(sess.Rooms, nr)

	var remainingMembers []*rns.Link
	for memberLink := range rm.GetRoomMembers(nr) {
		if memberLink != link {
			remainingMembers = append(remainingMembers, memberLink)
		}
	}

	if len(rm.GetRoomMembers(nr)) > 0 {
		rm.RemoveMember(nr, link)
		if len(rm.GetRoomMembers(nr)) == 0 {
			rm.RemoveMember(nr, link)
			st := rm.RoomStateGet(nr)
			if st != nil {
				rm.TouchRoom(nr)
				if st.Registered {
					rm.PersistRoomState(link, nr)
				}
			}
			if st != nil && !st.Registered {
				rm.StateDelete(nr)
			}
		}
	}

	peerStillInRoom := false
	if peerHash != nil {
		for _, memberLink := range remainingMembers {
			if other := r.hooks.Sessions().GetSession(memberLink); other != nil && string(other.Peer) == string(peerHash) {
				peerStillInRoom = true
				break
			}
		}
	}

	if len(remainingMembers) > 0 && idHash != nil && !peerStillInRoom {
		var notificationBody any
		if r.hooks.IncludeJoinedMemberList() {
			notificationBody = []any{peerHash}
		}
		memberNotification := MakeEnvelope(int(TParted), idHash,
			WithRoom(nr), WithBody(notificationBody), WithNick(nickDeref(sess.Nick)))
		memberNotificationPayload := cbor.Encode(memberNotification)
		for _, memberLink := range remainingMembers {
			r.hooks.QueuePayload(outgoing, memberLink, memberNotificationPayload)
		}
	}

	var partedBody any
	if r.hooks.IncludeJoinedMemberList() {
		partedBody = []any{peerHash}
	}
	if idHash != nil {
		parted := MakeEnvelope(int(TParted), idHash, WithRoom(nr), WithBody(partedBody))
		r.hooks.QueueEnv(outgoing, link, parted)
	}

	r.hooks.Infof("PART peer=%v nick=%v room=%v link_id=%v",
		r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), nr, r.hooks.FmtLinkID(link))
}

// handleMessage mirrors _handle_message: the slash-command interception
// for MSG and NOTICE, the room and size gates, the direct-notice path,
// room existence and moderation checks, the source/room/nick rewrites,
// and the single-encode fan out to every room member.
func (r *Router) handleMessage(link *rns.Link, sess *Session, peerHash []byte, env *cbor.Map, outgoing *OutgoingList) {
	rm := r.hooks.RoomManager()
	idHash := r.hooks.IdentityHash()
	tAny, _ := env.Get(KT)
	t, _ := intValue(tAny)
	roomVal, _ := env.Get(KRoom)
	dstVal, _ := env.Get(KDst)
	body, _ := env.Get(KBody)

	bodyStr, bodyIsStr := "", false
	if s, isStr := body.(string); isStr {
		bodyStr, bodyIsStr = s, true
	}

	if (t == TMsg || t == TNotice) && bodyIsStr {
		cmdline := strings.TrimFunc(bodyStr, isUnicodeSpace)
		if strings.HasPrefix(cmdline, "/") {
			if r.hooks.DebugEnabled() {
				r.hooks.Debugf("Slash command peer=%v link_id=%v cmd=%v room=%v",
					r.hooks.FmtHash(peerHash), r.hooks.FmtLinkID(link), pythonQuote(cmdline), roomRepr(roomVal))
			}
			handled := r.hooks.HandleOperatorCommand(link, peerHash, roomPtrOf(roomVal), bodyStr, outgoing)
			if handled {
				if r.hooks.DebugEnabled() {
					r.hooks.Debugf("Slash command handled, queued=%v responses", len(outgoing.Queue))
				}
				return
			}
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash, "unrecognized command", roomPtrOf(roomVal))
			}
			return
		}
	}

	if t == TMsg || t == TAction {
		room, roomIsStr := "", false
		if roomStr, isStr := roomVal.(string); isStr {
			room, roomIsStr = roomStr, true
		}
		if !roomIsStr || room == "" {
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash, "message requires room name", nil)
			}
			return
		}
		if bodyIsStr {
			bodyBytes := len(bodyStr)
			if limit := r.hooks.MaxMsgBodyBytes(); bodyBytes > limit {
				if idHash != nil {
					r.hooks.EmitError(outgoing, link, idHash,
						fmt.Sprintf("message too large: %v bytes > %v bytes", bodyBytes, limit), nil)
				}
				r.hooks.Infof("Rejected oversized message peer=%v nick=%v body_bytes=%v limit=%v",
					r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), bodyBytes, limit)
				return
			}
		}
	} else if t == TNotice {
		if dstVal != nil {
			r.handleDirectNotice(link, sess, peerHash, env, outgoing)
			return
		}
		room, roomIsStr := "", false
		if roomStr, isStr := roomVal.(string); isStr {
			room, roomIsStr = roomStr, true
		}
		if !roomIsStr || room == "" {
			return
		}
	}

	nr := ""
	if roomVal != nil {
		if room, isStr := roomVal.(string); isStr && room != "" {
			normalized, err := r.normRoom(room)
			if err != nil {
				if idHash != nil {
					r.hooks.EmitError(outgoing, link, idHash, err.Error(), nil)
				}
				return
			}
			nr = normalized
		}
	}

	if _, inRooms := sess.Rooms[nr]; !inRooms {
		var st *RoomState
		if _, ok := rm.RegistryGet(nr); ok {
			st = rm.EnsureRoomState(nr, nil)
		} else if len(rm.GetRoomMembers(nr)) > 0 {
			st = rm.EnsureRoomState(nr, nil)
		}
		if st == nil {
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash, "no such room", &nr)
			}
			return
		}
		if st.NoOutsideMsgs {
			if idHash != nil {
				r.hooks.EmitError(outgoing, link, idHash, "no outside messages (+n)", &nr)
			}
			return
		}
	}

	if rm.IsRoomBanned(nr, peerHash) {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "banned from room", &nr)
		}
		return
	}
	if rm.IsRoomModerated(nr) && !rm.IsRoomVoiced(nr, peerHash) {
		if idHash != nil {
			r.hooks.EmitError(outgoing, link, idHash, "room is moderated (+m)", &nr)
		}
		return
	}

	if peerHash != nil {
		env.Set(KSrc, peerHash)
	}
	env.Set(KRoom, nr)

	r.adoptNick(link, sess, env)

	payload := cbor.Encode(env)
	members := rm.GetRoomMembers(nr)
	for memberLink := range members {
		r.hooks.QueuePayload(outgoing, memberLink, payload)
	}

	if r.hooks.DebugEnabled() {
		r.hooks.Debugf("Forwarded t=%v peer=%v nick=%v room=%v recipients=%v body_type=%v",
			pythonRepr(tAny), r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), nr,
			len(members), pythonTypeName(body))
	}

	if t == TMsg {
		r.hooks.StatsInc("msgs_forwarded", 1)
	} else if t == TAction {
		r.hooks.StatsInc("actions_forwarded", 1)
	} else {
		r.hooks.StatsInc("notices_forwarded", 1)
	}
}

// handleDirectNotice mirrors _handle_direct_notice: a client-to-client
// NOTICE delivered by destination identity, with the source, nick, and
// destination rewrites applied before the single encode and queue to
// the target link only.
func (r *Router) handleDirectNotice(link *rns.Link, sess *Session, peerHash []byte, env *cbor.Map, outgoing *OutgoingList) {
	idHash := r.hooks.IdentityHash()
	if idHash == nil {
		return
	}

	if roomVal, ok := env.Get(KRoom); ok && roomVal != nil {
		r.hooks.EmitError(outgoing, link, idHash, "direct notice must not include room", nil)
		return
	}

	dst, dstOK := EnvGetBytes(env, KDst)
	if !dstOK {
		r.hooks.EmitError(outgoing, link, idHash, "direct notice requires destination identity", nil)
		return
	}

	targetLink := r.hooks.Sessions().GetLinkByHash(dst)
	if targetLink == nil {
		r.hooks.EmitError(outgoing, link, idHash, "destination not connected", nil)
		return
	}

	env.Set(KSrc, peerHash)
	r.adoptNick(link, sess, env)
	env.Set(KDst, dst)

	payload := cbor.Encode(env)
	r.hooks.QueuePayload(outgoing, targetLink, payload)

	if r.hooks.DebugEnabled() {
		body, _ := env.Get(KBody)
		r.hooks.Debugf("Forwarded direct NOTICE peer=%v nick=%v dst=%v body_type=%v",
			r.hooks.FmtHash(peerHash), nickRepr(sess.Nick), hex.EncodeToString(dst), pythonTypeName(body))
	}

	r.hooks.StatsInc("notices_forwarded", 1)
}

// adoptNick mirrors the shared nick-handling block of _handle_message
// and _handle_direct_notice: a present envelope nick is normalized and
// adopted into the session (updating the nick index when it changed) or
// popped when invalid, while a missing envelope nick falls back to the
// session nick.
func (r *Router) adoptNick(link *rns.Link, sess *Session, env *cbor.Map) {
	if nickVal, ok := env.Get(KNick); ok && nickVal != nil {
		nick, isStr := nickVal.(string)
		if !isStr {
			// normalize_nick returns None for non-string input, which
			// pops the invalid nick from the envelope.
			env.Pop(KNick)
			return
		}
		n := NormalizeNick(nick, r.hooks.MaxNickBytes())
		if n == "" {
			env.Pop(KNick)
			return
		}
		oldSessionNick := sess.Nick
		if oldSessionNick == nil || *oldSessionNick != n {
			sess.Nick = &n
			r.hooks.Sessions().UpdateNickIndex(link, oldSessionNick, &n)
		}
		env.Set(KNick, n)
	} else if sess.Nick != nil {
		if n := NormalizeNick(*sess.Nick, r.hooks.MaxNickBytes()); n != "" {
			env.Set(KNick, n)
		}
	}
}

// handlePing mirrors _handle_ping: the ping counter increments and a
// PONG echoing the body verbatim is queued when the hub identity is
// available.
func (r *Router) handlePing(link *rns.Link, env *cbor.Map, outgoing *OutgoingList) {
	body, hasBody := env.Get(KBody)

	r.hooks.StatsInc("pings_in", 1)
	if idHash := r.hooks.IdentityHash(); idHash != nil {
		var opts []EnvelopeOpt
		if hasBody {
			opts = append(opts, WithBody(body))
		}
		pong := MakeEnvelope(int(TPong), idHash, opts...)
		r.hooks.StatsInc("pongs_out", 1)
		r.hooks.QueueEnv(outgoing, link, pong)
	}
}

// extractCaps mirrors _extract_caps: the HELLO body's capability map is
// returned keyed by integer (bool keys collapse to 1/0 per Python's
// bool-int equivalence), with anything else yielding an empty map.
func (r *Router) extractCaps(body *cbor.Map) map[int64]any {
	out := map[int64]any{}
	if body == nil {
		return out
	}
	capsVal, ok := body.Get(BHelloCaps)
	if !ok {
		return out
	}
	caps, ok := capsVal.(*cbor.Map)
	if !ok {
		return out
	}
	for _, pair := range caps.Pairs() {
		if key, isInt := intValue(pair.Key); isInt {
			out[key] = pair.Val
		}
	}
	return out
}

// normRoom mirrors _norm_room: a Unicode-trimmed, lowercased room name
// with a configured UTF-8 byte-length cap.
func (r *Router) normRoom(room string) (string, error) {
	nr := strings.ToLower(strings.TrimFunc(room, isUnicodeSpace))
	if nr == "" {
		return "", errors.New("room name must not be empty")
	}
	if limit := r.hooks.MaxRoomNameBytes(); len(nr) > limit {
		return "", fmt.Errorf("room name too long: %v bytes > %v bytes", len(nr), limit)
	}
	return nr, nil
}

// decodeEnvelope decodes an RRC envelope from its CBOR payload and
// validates it, mirroring Python's decode + validate_envelope pair.
func decodeEnvelope(data []byte) (*cbor.Map, error) {
	decoded, err := cbor.Decode(data)
	if err != nil {
		return nil, err
	}
	env, ok := decoded.(*cbor.Map)
	if !ok {
		return nil, fmt.Errorf("envelope must be a CBOR map (dict)")
	}
	if err := ValidateEnvelope(env); err != nil {
		return nil, err
	}
	return env, nil
}

// mustEnvGet returns the envelope's value for a key, or nil when the
// key is absent (mirroring Python's dict.get).
func mustEnvGet(env *cbor.Map, key int64) any {
	if v, ok := env.Get(key); ok {
		return v
	}
	return nil
}

// roomPtrOf converts a decoded K_ROOM value to its optional-string
// form: nil when the value is missing or not a string, a pointer to the
// text otherwise.
func roomPtrOf(v any) *string {
	if s, isStr := v.(string); isStr {
		return &s
	}
	return nil
}

// nickRepr renders the session nick the way Python's %r renders
// get("nick"): None when unset, repr-quoted text otherwise.
func nickRepr(n *string) string {
	if n == nil {
		return "None"
	}
	return pythonQuote(*n)
}

// roomRepr renders a decoded K_ROOM value the way Python's %r does for
// the debug lines: None for a missing room, repr-quoted text otherwise.
func roomRepr(v any) string {
	if v == nil {
		return "None"
	}
	if s, isStr := v.(string); isStr {
		return pythonQuote(s)
	}
	return pythonRepr(v)
}

// bodyLenRepr renders the body_len field of the RX debug line: the byte
// length for byte-string bodies, the character count for text bodies,
// and None otherwise.
func bodyLenRepr(body any) string {
	switch b := body.(type) {
	case []byte:
		return fmt.Sprintf("%v", len(b))
	case string:
		return fmt.Sprintf("%v", utf8.RuneCountInString(b))
	}
	return "None"
}

// pythonTypeName mirrors Python's type(body).__name__ for the decoded
// CBOR value classes that can appear in envelopes.
func pythonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "NoneType"
	case []byte:
		return "bytes"
	case string:
		return "str"
	case *cbor.Map:
		return "dict"
	case []any:
		return "list"
	case bool:
		return "bool"
	case int64, uint64:
		return "int"
	case float64:
		return "float"
	}
	return fmt.Sprintf("%T", v)
}
