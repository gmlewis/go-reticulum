// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// OutgoingItem is one queued (link, payload) pair.
type OutgoingItem struct {
	Link    *rns.Link
	Payload []byte
}

// OutgoingList collects outgoing payloads in order plus post-send
// callbacks, mirroring Python's outgoing list with the
// _post_send_callbacks attribute the MOTD delivery hooks onto.
type OutgoingList struct {
	Queue             []OutgoingItem
	PostSendCallbacks []func()
}

// Session holds one link's RRC session state.
type Session struct {
	Welcomed     bool
	Rooms        map[string]bool
	Peer         []byte
	Nick         *string
	PeerCaps     map[int64]any
	AwaitingPong *float64
}

// RateState is the token bucket state for rate limiting.
type RateState struct {
	Tokens     float64
	LastRefill float64
}

// SessionHooks wires a SessionManager to the hub services it needs.
type SessionHooks struct {
	// IsBanned reports whether a hash is banned (from the trust manager).
	IsBanned func([]byte) bool
	// GetRoomMembers returns the member links of a room (room manager).
	GetRoomMembers func(room string) map[*rns.Link]bool
	// RemoveMember removes a link from a room (room manager).
	RemoveMember func(room string, link *rns.Link)
	// RateLimitMsgsPerMinute re-reads the configured rate limit.
	RateLimitMsgsPerMinute func() int
	// IncludeJoinedMemberList reports the config's
	// include_joined_member_list setting.
	IncludeJoinedMemberList func() bool
	// IdentityHash returns the hub identity hash, or nil when the hub
	// identity is unavailable.
	IdentityHash func() []byte
	// NowMonotonic returns a monotonic clock reading in seconds.
	NowMonotonic func() float64
	// QueueWelcome queues the WELCOME message (message helper).
	QueueWelcome func(outgoing *OutgoingList, link *rns.Link, peerHash []byte)
	// Greeting returns the configured MOTD greeting (nil/empty when unset).
	Greeting func() string
	// SendTextSmart sends text via resource or chunks (message helper).
	SendTextSmart func(link *rns.Link, msgType int64, text string, room *string, kind string)
	// SendPacket sends a payload to a link immediately; failures are
	// swallowed by the caller.
	SendPacket func(link *rns.Link, payload []byte) error
	// FmtLinkID formats a link id for logs.
	FmtLinkID func(link *rns.Link) string
	// FmtHash formats a hash for logs.
	FmtHash func(hash []byte) string
	// Logf logs a hub message.
	Logf func(format string, args ...any)
	// StatsInc increments a stats counter (bytes_out for immediate
	// sends).
	StatsInc func(key string, delta int)
}

// SessionManager manages session lifecycle for RRC hub connections,
// mirroring Python's SessionManager. All exported methods take the state
// lock; callers that already hold it use the Unlocked variants.
type SessionManager struct {
	hooks SessionHooks

	mu            sync.Mutex
	sessions      map[*rns.Link]*Session
	rate          map[*rns.Link]*RateState
	indexByHash   map[string]*rns.Link
	indexByNick   map[string]map[*rns.Link]bool
	sessionsOrder []*rns.Link
}

// NewSessionManager creates a session manager wired to the given hooks.
func NewSessionManager(hooks SessionHooks) *SessionManager {
	m := &SessionManager{
		hooks:         hooks,
		sessions:      map[*rns.Link]*Session{},
		rate:          map[*rns.Link]*RateState{},
		indexByHash:   map[string]*rns.Link{},
		indexByNick:   map[string]map[*rns.Link]bool{},
		sessionsOrder: []*rns.Link{},
	}
	if m.hooks.NowMonotonic == nil {
		start := time.Now()
		m.hooks.NowMonotonic = func() float64 { return float64(time.Since(start).Nanoseconds()) / 1e9 }
	}
	return m
}

// OnLinkEstablished creates session and rate-limit state for a new link,
// mirroring on_link_established.
func (m *SessionManager) OnLinkEstablished(link *rns.Link) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onLinkEstablishedLocked(link)
}

func (m *SessionManager) onLinkEstablishedLocked(link *rns.Link) {
	m.sessions[link] = &Session{
		Rooms:    map[string]bool{},
		PeerCaps: map[int64]any{},
	}
	perMin := float64(maxInt(1, m.hooks.RateLimitMsgsPerMinute()))
	m.rate[link] = &RateState{Tokens: perMin, LastRefill: m.hooks.NowMonotonic()}
	if m.hooks.Logf != nil {
		m.hooks.Logf("Session created link_id=%v", m.hooks.FmtLinkID(link))
	}
}

// OnRemoteIdentified handles a remote identity being established and
// returns (banned, peerHash), mirroring on_remote_identified.
func (m *SessionManager) OnRemoteIdentified(link *rns.Link, peerHash []byte) (bool, []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onRemoteIdentifiedLocked(link, peerHash)
}

func (m *SessionManager) onRemoteIdentifiedLocked(link *rns.Link, peerHash []byte) (bool, []byte) {
	sess := m.sessions[link]
	if sess == nil {
		return false, nil
	}

	if peerHash != nil {
		sess.Peer = peerHash
		m.indexByHash[string(peerHash)] = link
		banned := m.hooks.IsBanned(peerHash)
		if !banned && m.hooks.Logf != nil {
			m.hooks.Logf("Remote identified peer=%v link_id=%v",
				m.hooks.FmtHash(peerHash), m.hooks.FmtLinkID(link))
		}
		return banned, peerHash
	}
	return false, nil
}

// OnLinkClosed handles link closure and cleanup, returning
// (peerHash, nick, roomsCount) for logging, mirroring on_link_closed.
// PARTED notifications fan out to remaining room members immediately.
func (m *RoomManager) noop() {}

func (m *SessionManager) OnLinkClosed(link *rns.Link) ([]byte, *string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onLinkClosedLocked(link)
}

func (m *SessionManager) onLinkClosedLocked(link *rns.Link) ([]byte, *string, int) {
	sess := m.sessions[link]
	delete(m.sessions, link)
	delete(m.rate, link)
	m.removeSessionOrderEntry(link)

	if sess == nil {
		return nil, nil, 0
	}

	peer := sess.Peer
	nick := sess.Nick
	roomsCount := len(sess.Rooms)

	if peer != nil {
		if m.indexByHash[string(peer)] == link {
			delete(m.indexByHash, string(peer))
		}
	}

	if nick != nil {
		m.updateNickIndexLocked(link, nick, nil)
	}

	for room := range sess.Rooms {
		members := m.hooks.GetRoomMembers(room)
		var remaining []*rns.Link
		for member := range members {
			if member != link {
				remaining = append(remaining, member)
			}
		}

		m.hooks.RemoveMember(room, link)

		peerStillInRoom := false
		if peer != nil {
			for _, memberLink := range remaining {
				other := m.sessions[memberLink]
				if other != nil && string(other.Peer) == string(peer) {
					peerStillInRoom = true
					break
				}
			}
		}

		if len(remaining) > 0 && peer != nil && m.hooks.IdentityHash() != nil && !peerStillInRoom {
			var body any
			if m.hooks.IncludeJoinedMemberList() {
				body = []any{peer}
			}
			notification := MakeEnvelope(int(TParted), m.hooks.IdentityHash(), WithRoom(room), WithBody(body), WithNick(nickDeref(nick)))
			payload := encodePayload(notification)
			for _, memberLink := range remaining {
				if err := m.hooks.SendPacket(memberLink, payload); err == nil {
					m.statsInc("bytes_out", len(payload))
				}
			}
		}
	}

	return peer, nick, roomsCount
}

func nickStr(n *string) *string { return n }

// UpdateNickIndex updates the nickname index when a nick changes,
// mirroring update_nick_index.
func (m *SessionManager) UpdateNickIndex(link *rns.Link, oldNick, newNick *string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateNickIndexLocked(link, oldNick, newNick)
}

func (m *SessionManager) updateNickIndexLocked(link *rns.Link, oldNick, newNick *string) {
	if oldNick != nil && *oldNick != "" {
		oldKey := nickIndexKey(*oldNick)
		if _, ok := m.indexByNick[oldKey]; ok {
			delete(m.indexByNick[oldKey], link)
			if len(m.indexByNick[oldKey]) == 0 {
				delete(m.indexByNick, oldKey)
			}
		}
	}
	if newNick != nil && *newNick != "" {
		newKey := nickIndexKey(*newNick)
		if _, ok := m.indexByNick[newKey]; !ok {
			m.indexByNick[newKey] = map[*rns.Link]bool{}
		}
		m.indexByNick[newKey][link] = true
	}
}

// nickIndexKey normalizes a nick for the index (strip().lower()).
func nickIndexKey(nick string) string {
	return strings.ToLower(strings.TrimSpace(nick))
}

func (m *SessionManager) statsInc(key string, delta int) {
	// Stats are managed by the StatsManager; the session manager only
	// counts bytes_out through this passthrough hook.
	if m.hooks.StatsInc != nil {
		m.hooks.StatsInc(key, delta)
	}
}

// RefillAndTake refills the token bucket and attempts to take cost tokens,
// mirroring refill_and_take. It returns false when rate limited.
func (m *SessionManager) RefillAndTake(link *rns.Link, cost float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refillAndTakeLocked(link, cost)
}

func (m *SessionManager) refillAndTakeLocked(link *rns.Link, cost float64) bool {
	state := m.rate[link]
	if state == nil {
		return true
	}
	now := m.hooks.NowMonotonic()
	perMin := float64(maxInt(1, m.hooks.RateLimitMsgsPerMinute()))
	ratePerS := perMin / 60.0
	elapsed := maxFloat(0.0, now-state.LastRefill)
	state.Tokens = minFloat(perMin, state.Tokens+elapsed*ratePerS)
	state.LastRefill = now
	if state.Tokens < cost {
		return false
	}
	state.Tokens -= cost
	return true
}

// GetSession returns the session state for a link.
func (m *SessionManager) GetSession(link *rns.Link) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[link]
}

// GetLinkByHash looks up a link by peer identity hash.
func (m *SessionManager) GetLinkByHash(peerHash []byte) *rns.Link {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.indexByHash[string(peerHash)]
}

// GetLinksByNick looks up links by normalized nickname, returning a copy.
func (m *SessionManager) GetLinksByNick(nick string) map[*rns.Link]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := nickIndexKey(nick)
	out := map[*rns.Link]bool{}
	for link := range m.indexByNick[key] {
		out[link] = true
	}
	return out
}

// LinksByHashPrefix returns every link whose indexed peer identity hash
// starts with the given byte prefix, mirroring the hash-prefix scan of
// _find_target_links (map order; callers order the matches).
func (m *SessionManager) LinksByHashPrefix(prefix []byte) []*rns.Link {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*rns.Link
	for hashStr, link := range m.indexByHash {
		if strings.HasPrefix(hashStr, string(prefix)) {
			out = append(out, link)
		}
	}
	return out
}

// ClearAll clears all sessions and returns the links for teardown.
func (m *SessionManager) ClearAll() []*rns.Link {
	m.mu.Lock()
	defer m.mu.Unlock()
	links := append([]*rns.Link{}, m.sessionsOrder...)
	m.sessions = map[*rns.Link]*Session{}
	m.rate = map[*rns.Link]*RateState{}
	m.indexByHash = map[string]*rns.Link{}
	m.indexByNick = map[string]map[*rns.Link]bool{}
	m.sessionsOrder = nil
	return links
}

// SessionStats holds the session statistics for the hub stats.
type SessionStats struct {
	Total         int
	Welcomed      int
	Identified    int
	IndexedByHash int
	IndexedByNick int
}

// GetStats returns session statistics for monitoring.
func (m *SessionManager) GetStats() SessionStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := SessionStats{Total: len(m.sessions)}
	for _, sess := range m.sessions {
		if sess.Welcomed {
			stats.Welcomed++
		}
		if sess.Peer != nil {
			stats.Identified++
		}
	}
	stats.IndexedByHash = len(m.indexByHash)
	stats.IndexedByNick = len(m.indexByNick)
	return stats
}

// SendWelcome sends a WELCOME message to a client and optionally queues the
// MOTD delivery, mirroring send_welcome.
func (m *SessionManager) SendWelcome(link *rns.Link, outgoing *OutgoingList, peerHash []byte, oldNick, newNick *string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := m.sessions[link]
	if sess == nil {
		return
	}

	if !nickPtrEqual(oldNick, newNick) {
		m.updateNickIndexLocked(link, oldNick, newNick)
	}

	sess.Welcomed = true

	m.hooks.QueueWelcome(outgoing, link, peerHash)

	// A non-empty greeting queues the MOTD delivery as a post-send
	// callback (mirroring the _post_send_callbacks attribute).
	if greeting := m.hooks.Greeting(); greeting != "" {
		outgoing.PostSendCallbacks = append(outgoing.PostSendCallbacks, func() {
			m.hooks.SendTextSmart(link, TNotice, greeting, nil, ResKindMOTD)
		})
	}
}

func nickPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// encodePayload encodes an envelope to CBOR bytes the way Python's codec
// module does.
func encodePayload(env *cbor.Map) []byte {
	return cbor.Encode(env)
}

// nickDeref returns the string value of a nick pointer, or empty for nil.
func nickDeref(n *string) string {
	if n == nil {
		return ""
	}
	return *n
}

// removeSessionOrderEntry removes one link from the session order slice.
func (m *SessionManager) removeSessionOrderEntry(link *rns.Link) {
	for i, l := range m.sessionsOrder {
		if l == link {
			m.sessionsOrder = append(m.sessionsOrder[:i], m.sessionsOrder[i+1:]...)
			return
		}
	}
}

// SessionsForTest returns the session manager itself for hook wiring in
// tests and the router (satisfies the RouterHooks.Sessions pattern).
func (m *SessionManager) SessionsForTest() *SessionManager {
	return m
}
