// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// RoomState holds one room's moderation state, mirroring the Python room
// state dicts. Hashes are kept as raw bytes; the ops/voiced/bans sets are
// keyed by lowercase hex for deterministic membership and rendering.
type RoomState struct {
	Founder       []byte
	Registered    bool
	Topic         *string
	Moderated     bool
	InviteOnly    bool
	TopicOpsOnly  bool
	NoOutsideMsgs bool
	Private       bool
	Key           *string
	Ops           map[string]bool
	Voiced        map[string]bool
	Bans          map[string]bool
	Invited       []Invite
	LastUsedTS    *float64
}

// Invite is one keyed-room invite: an identity hash and its expiry as a
// Unix timestamp in seconds.
type Invite struct {
	Hash    []byte
	Expires float64
}

// hexKey renders a hash the way the Python code stores set members.
func hexKey(b []byte) string { return strings.ToLower(fmt.Sprintf("%x", b)) }

// RoomManager manages room memberships, state, permissions, and registry
// persistence, mirroring Python's RoomManager.
// RoomHooks wires a RoomManager to the hub services it needs, mirroring the
// hub reference Python's RoomManager holds.
type RoomHooks struct {
	// IsServerOp reports whether a hash is a server operator (from the
	// trust manager).
	IsServerOp func([]byte) bool
	// RegistryPath resolves the room registry file for writes; nil when
	// unset (persist then no-ops).
	RegistryPath func() string
	// Notice emits a NOTICE to a link on persistence failures.
	Notice func(link *rns.Link, room, text string)
	// BroadcastNotice emits a room-mode NOTICE to a link, carrying the
	// outgoing queue like the message helper.
	BroadcastNotice func(outgoing *OutgoingList, link *rns.Link, room, text string)
	// Now returns the current wall time as seconds (injectable in tests).
	Now func() float64
}

type RoomManager struct {
	hooks     RoomHooks
	mu        sync.Mutex
	rooms     map[string]map[*rns.Link]bool
	roomState map[string]*RoomState
	// roomStateOrder tracks the live state's insertion order for the
	// deterministic iteration Python dicts provide.
	roomStateOrder []string

	registryWriteMu sync.Mutex

	roomRegistry      map[string]*RoomState
	roomRegistryOrder []string
}

// NewRoomManager creates a room manager wired to the given hooks.
func NewRoomManager(hooks RoomHooks) *RoomManager {
	m := &RoomManager{
		hooks:        hooks,
		rooms:        map[string]map[*rns.Link]bool{},
		roomState:    map[string]*RoomState{},
		roomRegistry: map[string]*RoomState{},
	}
	if m.hooks.Now == nil {
		m.hooks.Now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	return m
}

// ClearAll clears all room state, called during hub shutdown.
func (m *RoomManager) ClearAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rooms = map[string]map[*rns.Link]bool{}
	m.roomState = map[string]*RoomState{}
	m.roomStateOrder = nil
	m.roomRegistry = map[string]*RoomState{}
	m.roomRegistryOrder = nil
}

// GetRoomMembers returns the set of links currently in a room.
func (m *RoomManager) GetRoomMembers(room string) map[*rns.Link]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roomMembersLocked(room)
}

func (m *RoomManager) roomMembersLocked(room string) map[*rns.Link]bool {
	out := map[*rns.Link]bool{}
	for link := range m.rooms[room] {
		out[link] = true
	}
	return out
}

// AddMember adds a link to a room, creating the room if needed.
func (m *RoomManager) AddMember(room string, link *rns.Link, founder []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rooms[room]; !ok {
		m.rooms[room] = map[*rns.Link]bool{}
		m.ensureRoomStateLocked(room, founder)
	}
	m.rooms[room][link] = true
}

// RemoveMember removes a link from a room, cleaning up empty rooms.
func (m *RoomManager) RemoveMember(room string, link *rns.Link) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeMemberLocked(room, link)
}

func (m *RoomManager) removeMemberLocked(room string, link *rns.Link) {
	if _, ok := m.rooms[room]; !ok {
		return
	}
	delete(m.rooms[room], link)
	if len(m.rooms[room]) == 0 {
		delete(m.rooms, room)
		if st := m.roomStateGetLocked(room); st != nil && !st.Registered {
			m.removeOrderEntry(&m.roomStateOrder, room)
			delete(m.roomState, room)
		}
	}
}

// RemoveMemberFromAll removes a link from all rooms and returns the number
// of rooms it was removed from.
func (m *RoomManager) RemoveMemberFromAll(link *rns.Link) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var affected []string
	for room, links := range m.rooms {
		if links[link] {
			affected = append(affected, room)
		}
	}
	sortStrings(affected)
	for _, room := range affected {
		m.removeMemberLocked(room, link)
	}
	return len(affected)
}

// GetMemberRooms returns the list of rooms a link is currently in.
func (m *RoomManager) GetMemberRooms(link *rns.Link) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for room, links := range m.rooms {
		if links[link] {
			out = append(out, room)
		}
	}
	sortStrings(out)
	return out
}

// RoomCount pairs a room name with its member count for stats.
type RoomCount struct {
	Room  string
	Count int
}

// RoomStats holds the room statistics for the hub stats.
type RoomStats struct {
	RoomsTotal  int
	Memberships int
	TopRooms    []RoomCount
}

// DiscardMember removes a link from a room without the empty-room
// cleanup, mirroring the direct rooms[room].discard(link) calls in the
// kick and ban force-removal paths (empty rooms linger in the map).
func (m *RoomManager) DiscardMember(room string, link *rns.Link) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if members, ok := m.rooms[room]; ok {
		delete(members, link)
	}
}

// GetRoomStats returns room statistics for the hub stats.
func (m *RoomManager) GetRoomStats() RoomStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	stats := RoomStats{RoomsTotal: len(m.rooms)}
	type counted struct {
		room  string
		count int
	}
	all := make([]counted, 0, len(m.rooms))
	for room, links := range m.rooms {
		all = append(all, counted{room, len(links)})
		stats.Memberships += len(links)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].count != all[j].count {
			return all[i].count > all[j].count
		}
		return all[i].room < all[j].room
	})
	for i, c := range all {
		if i >= 5 {
			break
		}
		stats.TopRooms = append(stats.TopRooms, RoomCount{Room: c.room, Count: c.count})
	}
	return stats
}

// roomStateGetLocked returns the room state if it exists.
func (m *RoomManager) roomStateGetLocked(room string) *RoomState {
	return m.roomState[room]
}

// RoomStateGet returns the room state if it exists.
func (m *RoomManager) RoomStateGet(room string) *RoomState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.roomStateGetLocked(room)
}

// EnsureRoomState ensures room state exists, creating it from the registry
// or defaults, mirroring _room_state_ensure.
func (m *RoomManager) EnsureRoomState(room string, founder []byte) *RoomState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureRoomStateLocked(room, founder)
}

func (m *RoomManager) ensureRoomStateLocked(room string, founder []byte) *RoomState {
	if st := m.roomState[room]; st != nil {
		if st.Founder == nil && founder != nil {
			st.Founder = founder
			if st.Ops == nil {
				st.Ops = map[string]bool{}
			}
			st.Ops[hexKey(founder)] = true
		}
		return st
	}

	if base := m.roomRegistry[room]; base != nil {
		st := &RoomState{
			Founder:       base.Founder,
			Registered:    true,
			Topic:         base.Topic,
			Moderated:     base.Moderated,
			InviteOnly:    base.InviteOnly,
			TopicOpsOnly:  base.TopicOpsOnly,
			NoOutsideMsgs: base.NoOutsideMsgs,
			Private:       base.Private,
			Key:           base.Key,
			Ops:           copyHashSet(base.Ops),
			Voiced:        copyHashSet(base.Voiced),
			Bans:          copyHashSet(base.Bans),
			Invited:       appendInvites(nil, base.Invited),
			LastUsedTS:    base.LastUsedTS,
		}
		m.stateSetLocked(room, st)
		return st
	}

	st := &RoomState{Founder: founder}
	if founder != nil {
		st.Ops = map[string]bool{hexKey(founder): true}
	} else {
		st.Ops = map[string]bool{}
	}
	st.Voiced = map[string]bool{}
	st.Bans = map[string]bool{}
	m.stateSetLocked(room, st)
	return st
}

// TouchRoom updates last_used_ts for a room in state and registry.
func (m *RoomManager) TouchRoom(room string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	ts := m.hooks.Now()
	st.LastUsedTS = &ts
	if reg := m.roomRegistry[room]; reg != nil {
		reg.LastUsedTS = &ts
	}
}

// RoomModes is the dict of room mode flags for a room.
type RoomModes struct {
	Registered    bool
	Moderated     bool
	InviteOnly    bool
	TopicOpsOnly  bool
	NoOutsideMsgs bool
	Private       bool
	HasKey        bool
}

// GetRoomModes returns the room mode flags, materializing state for unknown
// rooms like the Python side does.
func (m *RoomManager) GetRoomModes(room string) RoomModes {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	modes := RoomModes{
		Registered:    st.Registered,
		Moderated:     st.Moderated,
		InviteOnly:    st.InviteOnly,
		TopicOpsOnly:  st.TopicOpsOnly,
		NoOutsideMsgs: st.NoOutsideMsgs,
		Private:       st.Private,
	}
	if st.Key != nil && *st.Key != "" {
		modes.HasKey = true
	}
	return modes
}

// GetRoomModeString returns the IRC-style mode string for a room with flags
// appended in the order i, k, m, n, p, r, t.
func (m *RoomManager) GetRoomModeString(room string) string {
	modes := m.GetRoomModes(room)
	var flags strings.Builder
	if modes.InviteOnly {
		flags.WriteString("i")
	}
	if modes.HasKey {
		flags.WriteString("k")
	}
	if modes.Moderated {
		flags.WriteString("m")
	}
	if modes.NoOutsideMsgs {
		flags.WriteString("n")
	}
	if modes.Private {
		flags.WriteString("p")
	}
	if modes.Registered {
		flags.WriteString("r")
	}
	if modes.TopicOpsOnly {
		flags.WriteString("t")
	}
	if flags.Len() == 0 {
		return "(none)"
	}
	return "+" + flags.String()
}

// BroadcastRoomMode sends the current room mode to every member,
// mirroring broadcast_room_mode.
func (m *RoomManager) BroadcastRoomMode(room string, outgoing *OutgoingList) {
	modeText := m.GetRoomModeString(room)
	for other := range m.GetRoomMembers(room) {
		if m.hooks.BroadcastNotice != nil {
			m.hooks.BroadcastNotice(outgoing, other, room, fmt.Sprintf("mode for %v is now: %v", room, modeText))
		}
	}
}

// IsRoomModerated reports whether the room is moderated.
func (m *RoomManager) IsRoomModerated(room string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	return st.Moderated
}

// IsRoomOp reports whether the peer is a room operator (server op → founder
// → ops member).
func (m *RoomManager) IsRoomOp(room string, peerHash []byte) bool {
	if peerHash == nil {
		return false
	}
	if m.hooks.IsServerOp != nil && m.hooks.IsServerOp(peerHash) {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	if st.Founder != nil && string(st.Founder) == string(peerHash) {
		return true
	}
	return st.Ops[hexKey(peerHash)]
}

// IsRoomVoiced reports whether the peer has voice in the room (op → voiced).
func (m *RoomManager) IsRoomVoiced(room string, peerHash []byte) bool {
	if peerHash == nil {
		return false
	}
	if m.hooks.IsServerOp != nil && m.hooks.IsServerOp(peerHash) {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	if st.Founder != nil && string(st.Founder) == string(peerHash) {
		return true
	}
	if st.Ops[hexKey(peerHash)] {
		return true
	}
	return st.Voiced[hexKey(peerHash)]
}

// IsRoomBanned reports whether the peer is banned from the room.
func (m *RoomManager) IsRoomBanned(room string, peerHash []byte) bool {
	if peerHash == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	return st.Bans[hexKey(peerHash)]
}

// IsInvited reports whether the peer has a valid (non-expired) invite,
// lazily popping expired entries.
func (m *RoomManager) IsInvited(room string, peerHash []byte) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	if len(st.Invited) == 0 {
		return false
	}
	now := m.hooks.Now()
	for i, inv := range st.Invited {
		if string(inv.Hash) != string(peerHash) {
			continue
		}
		if inv.Expires <= now {
			st.Invited = append(st.Invited[:i], st.Invited[i+1:]...)
			return false
		}
		return true
	}
	return false
}

// PruneExpiredInvites removes expired invites from a room and reports
// whether anything was removed.
func (m *RoomManager) PruneExpiredInvites(room string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.ensureRoomStateLocked(room, nil)
	if len(st.Invited) == 0 {
		return false
	}
	now := m.hooks.Now()
	removed := false
	kept := st.Invited[:0]
	for _, inv := range st.Invited {
		if inv.Expires <= now {
			removed = true
			continue
		}
		kept = append(kept, inv)
	}
	st.Invited = kept
	return removed
}

// copyHashSet copies a hex-keyed set.
func copyHashSet(src map[string]bool) map[string]bool {
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// appendInvites copies an invite list.
func appendInvites(dst, src []Invite) []Invite {
	if len(src) == 0 {
		return dst
	}
	return append(dst, src...)
}

// copyRoomState copies a room state value.
func copyRoomState(st *RoomState) *RoomState {
	if st == nil {
		return nil
	}
	out := *st
	out.Ops = copyHashSet(st.Ops)
	out.Voiced = copyHashSet(st.Voiced)
	out.Bans = copyHashSet(st.Bans)
	out.Invited = appendInvites(nil, st.Invited)
	return &out
}

// RegistrySnapshot returns a copy of the registry map for diffing.
func (m *RoomManager) RegistrySnapshot() map[string]*RoomState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*RoomState, len(m.roomRegistry))
	for name, st := range m.roomRegistry {
		out[name] = copyRoomState(st)
	}
	return out
}

// ReplaceRegistry swaps the live registry, appending the new rooms in
// sorted-name order (Python's reload assigns the freshly loaded dict;
// the Go map carries no order, so the sorted order is documented).
func (m *RoomManager) ReplaceRegistry(registry map[string]*RoomState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roomRegistry = map[string]*RoomState{}
	m.roomRegistryOrder = nil
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m.roomRegistry[name] = copyRoomState(registry[name])
		m.roomRegistryOrder = append(m.roomRegistryOrder, name)
	}
}

// RegistryLen returns the number of registered rooms.
func (m *RoomManager) RegistryLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.roomRegistry)
}

// RegistryOrder returns the registry room names in insertion order.
func (m *RoomManager) RegistryOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.roomRegistryOrder...)
}

// RegistryGet returns the registry entry for a room.
func (m *RoomManager) RegistryGet(room string) (*RoomState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.roomRegistry[room]
	return st, ok
}

// RegisteredPublicRoom is one registered public room listing entry: the
// room name and its topic (nil when unset).
type RegisteredPublicRoom struct {
	Name  string
	Topic *string
}

// RegisteredPublicRooms collects the registered non-private rooms from the
// live state, then registry-only rooms whose registry entry is not
// private, mirroring the /list collection (the registry loop checks only
// the private flag).
func (m *RoomManager) RegisteredPublicRooms() []RegisteredPublicRoom {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RegisteredPublicRoom
	for _, name := range m.roomStateOrder {
		st := m.roomState[name]
		if st == nil || !st.Registered || st.Private {
			continue
		}
		out = append(out, RegisteredPublicRoom{Name: name, Topic: st.Topic})
	}
	for _, name := range m.roomRegistryOrder {
		if _, inState := m.roomState[name]; inState {
			continue
		}
		reg := m.roomRegistry[name]
		if reg == nil || reg.Private {
			continue
		}
		out = append(out, RegisteredPublicRoom{Name: name, Topic: reg.Topic})
	}
	return out
}

// registrySetLocked stores a registry entry, appending to the insertion
// order.
func (m *RoomManager) registrySetLocked(room string, st *RoomState) {
	if _, ok := m.roomRegistry[room]; !ok {
		m.roomRegistryOrder = append(m.roomRegistryOrder, room)
	}
	m.roomRegistry[room] = st
}

// RegistrySet stores a registry entry, appending to the insertion order.
func (m *RoomManager) RegistrySet(room string, st *RoomState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registrySetLocked(room, st)
}

// RegistryDelete removes a registry entry.
func (m *RoomManager) RegistryDelete(room string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.roomRegistry, room)
	m.removeOrderEntry(&m.roomRegistryOrder, room)
}

// StateSet stores a room's live state, appending to the insertion order.
func (m *RoomManager) StateSet(room string, st *RoomState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateSetLocked(room, st)
}

func (m *RoomManager) stateSetLocked(room string, st *RoomState) {
	if _, ok := m.roomState[room]; !ok {
		m.roomStateOrder = append(m.roomStateOrder, room)
	}
	m.roomState[room] = st
}

// StateDelete removes a room's live state.
func (m *RoomManager) StateDelete(room string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateDeleteLocked(room)
}

func (m *RoomManager) stateDeleteLocked(room string) {
	delete(m.roomState, room)
	m.removeOrderEntry(&m.roomStateOrder, room)
}

func (m *RoomManager) removeOrderEntry(order *[]string, room string) {
	for i, name := range *order {
		if name == room {
			*order = append((*order)[:i], (*order)[i+1:]...)
			return
		}
	}
}

// truthy evaluates a TOML value the way Python's bool() does.
func truthy(v toml.Value) bool {
	switch v.Kind {
	case toml.KindString:
		return v.Str != ""
	case toml.KindBool:
		return v.Bool
	case toml.KindInt:
		return v.Int != 0
	case toml.KindFloat:
		return v.Flt != 0
	case toml.KindArray:
		return len(v.Arr) > 0
	case toml.KindInlineTable:
		return len(v.Tbl) > 0
	}
	return false
}

// hexDecodeTOML decodes a hex identity string the way the Python loader
// does: strip, lower, optional 0x prefix, then bytes.fromhex (which skips
// ASCII whitespace between pairs).
func hexDecodeTOML(s string) ([]byte, error) {
	t := strings.ToLower(strings.TrimSpace(s))
	t = strings.TrimPrefix(t, "0x")
	return fromHexPython(t)
}

// findRoomsTable locates the [rooms] table without creating it.
func findRoomsTable(root *toml.Table) *toml.Table {
	for _, sub := range root.Tables {
		if len(sub.Path) > 0 && sub.Path[len(sub.Path)-1] == "rooms" {
			return sub
		}
	}
	return nil
}

// LoadRegistryFromPath loads the room registry from a TOML file, returning
// (registry, errorMessage) exactly like load_registry_from_path: a missing
// file is empty with no error, parse errors report "parse error: {e}", and
// per-room entries coerce values with Python truthiness.
func (m *RoomManager) LoadRegistryFromPath(path string) (map[string]*RoomState, string) {
	empty := map[string]*RoomState{}
	if path == "" {
		return empty, ""
	}
	if _, err := os.Stat(path); err != nil {
		return empty, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Sprintf("parse error: %v", err)
	}
	doc, err := toml.Parse(string(data))
	if err != nil {
		return nil, fmt.Sprintf("parse error: %v", err)
	}
	rooms := findRoomsTable(doc.Root())
	if rooms == nil {
		return empty, ""
	}
	now := m.hooks.Now()
	registry := map[string]*RoomState{}

	// Room entries appear as sub-tables and as inline-table values, in
	// document order (mirroring rooms_section.items()). Nested
	// sub-tables (e.g. [rooms."x".invited]) contribute their entries as
	// the nested dict's items.
	for _, sub := range rooms.Tables {
		roomName := sub.Path[len(sub.Path)-1]
		keys := append([]toml.KeyVal{}, sub.Keys...)
		for _, nested := range sub.Tables {
			nestedName := nested.Path[len(nested.Path)-1]
			for i := range nested.Keys {
				kv := &nested.Keys[i]
				if kv.IsRaw {
					continue
				}
				keys = append(keys, toml.KeyVal{
					Key:   nestedName + "\x00" + kv.Key,
					Value: kv.Value,
				})
			}
		}
		registry[roomName] = registryRoomFromEntries(roomName, keys, now)
	}
	return registry, ""
}

// registryRoomFromEntries builds one registry entry from a room's entries.
func registryRoomFromEntries(roomName string, keys []toml.KeyVal, now float64) *RoomState {
	get := func(key string) (toml.Value, bool) {
		for i := range keys {
			if !keys[i].IsRaw && keys[i].Key == key {
				return keys[i].Value, true
			}
		}
		return toml.Value{}, false
	}
	st := &RoomState{Ops: map[string]bool{}, Voiced: map[string]bool{}, Bans: map[string]bool{}}

	if founder, ok := get("founder"); ok && founder.Kind == toml.KindString {
		if b, err := hexDecodeTOML(founder.Str); err == nil {
			st.Founder = b
		}
	}
	if topic, ok := get("topic"); ok && topic.Kind == toml.KindString {
		st.Topic = strPtr(topic.Str)
	}
	if v, ok := get("moderated"); ok {
		st.Moderated = truthy(v)
	}
	if v, ok := get("invite_only"); ok {
		st.InviteOnly = truthy(v)
	}
	if v, ok := get("topic_ops_only"); ok {
		st.TopicOpsOnly = truthy(v)
	}
	if v, ok := get("no_outside_msgs"); ok {
		st.NoOutsideMsgs = truthy(v)
	}
	if v, ok := get("private"); ok {
		st.Private = truthy(v)
	}
	if key, ok := get("key"); ok && key.Kind == toml.KindString {
		st.Key = strPtr(key.Str)
	}
	hexSet := func(key string, dst map[string]bool) {
		v, ok := get(key)
		if !ok || v.Kind != toml.KindArray {
			return
		}
		for _, item := range v.Arr {
			if item.Kind != toml.KindString {
				continue
			}
			if b, err := hexDecodeTOML(item.Str); err == nil {
				dst[hexKey(b)] = true
			}
		}
	}
	hexSet("operators", st.Ops)
	hexSet("voiced", st.Voiced)
	hexSet("bans", st.Bans)

	if v, ok := get("invited"); ok && v.Kind == toml.KindInlineTable {
		for i := range v.Tbl {
			entry := &v.Tbl[i]
			if entry.IsRaw {
				continue
			}
			if b, err := hexDecodeTOML(entry.Key); err == nil && entry.Value.Kind == toml.KindFloat {
				if entry.Value.Flt > now {
					st.Invited = append(st.Invited, Invite{Hash: b, Expires: entry.Value.Flt})
				}
			}
		}
	}
	// Nested invited sub-tables ([rooms."x".invited]) arrive as keys
	// "invited\x00<hash>" from the flattened load.
	const invitedPrefix = "invited" + "\x00"
	for i := range keys {
		entry := &keys[i]
		if entry.IsRaw || entry.Value.Kind != toml.KindFloat ||
			!strings.HasPrefix(entry.Key, invitedPrefix) {
			continue
		}
		if b, err := hexDecodeTOML(entry.Key[len(invitedPrefix):]); err == nil {
			if entry.Value.Flt > now {
				st.Invited = append(st.Invited, Invite{Hash: b, Expires: entry.Value.Flt})
			}
		}
	}
	if v, ok := get("last_used_ts"); ok {
		switch v.Kind {
		case toml.KindFloat:
			ts := v.Flt
			st.LastUsedTS = &ts
		case toml.KindInt:
			ts := float64(v.Int)
			st.LastUsedTS = &ts
		}
	}
	_ = roomName
	return st
}

// DiffRegistrySummary generates a human-readable summary of registry
// changes mirroring diff_registry_summary.
func DiffRegistrySummary(old, new map[string]*RoomState) []string {
	oldRooms := make([]string, 0, len(old))
	newRooms := make([]string, 0, len(new))
	for name := range old {
		oldRooms = append(oldRooms, name)
	}
	for name := range new {
		newRooms = append(newRooms, name)
	}
	sort.Strings(oldRooms)
	sort.Strings(newRooms)

	oldSet := map[string]bool{}
	for _, name := range oldRooms {
		oldSet[name] = true
	}
	newSet := map[string]bool{}
	for _, name := range newRooms {
		newSet[name] = true
	}

	lines := []string{}
	var added, removed []string
	for _, name := range newRooms {
		if !oldSet[name] {
			added = append(added, name)
		}
	}
	for _, name := range oldRooms {
		if !newSet[name] {
			removed = append(removed, name)
		}
	}
	if len(added) > 0 {
		preview, suffix := previewList(added)
		lines = append(lines, fmt.Sprintf("rooms_added=%v: %v%v", len(added), preview, suffix))
	}
	if len(removed) > 0 {
		preview, suffix := previewList(removed)
		lines = append(lines, fmt.Sprintf("rooms_removed=%v: %v%v", len(removed), preview, suffix))
	}
	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("rooms_changed=0 (registered_rooms=%v)", len(newSet)))
	}
	return lines
}

// previewList renders the first 10 names with a suffix for the rest.
func previewList(names []string) (string, string) {
	n := len(names)
	if n > 10 {
		n = 10
	}
	preview := strings.Join(names[:n], ", ")
	if len(names) <= 10 {
		return preview, ""
	}
	return preview, fmt.Sprintf(" (+%v more)", len(names)-10)
}

// PruneUnusedRegisteredRooms prunes registered rooms that have no
// connected members and were last used longer than pruneAfterS ago,
// returning the pruned room names.
func (m *RoomManager) PruneUnusedRegisteredRooms(pruneAfterS, startedWallTime float64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.hooks.Now()
	var pruned []string
	for _, room := range append([]string{}, m.roomRegistryOrder...) {
		if members, ok := m.rooms[room]; ok && len(members) > 0 {
			continue
		}
		reg := m.roomRegistry[room]
		lastUsed := startedWallTime
		if reg.LastUsedTS != nil {
			lastUsed = *reg.LastUsedTS
		}
		if now-lastUsed < pruneAfterS {
			continue
		}
		delete(m.roomRegistry, room)
		m.removeOrderEntry(&m.roomRegistryOrder, room)
		delete(m.roomState, room)
		m.removeOrderEntry(&m.roomStateOrder, room)
		pruned = append(pruned, room)
	}
	return pruned
}

// MergeRegistryIntoState updates live room state with registry data,
// mirroring merge_registry_into_state.
func (m *RoomManager) MergeRegistryIntoState(registry map[string]*RoomState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, room := range append([]string{}, m.roomStateOrder...) {
		st := m.roomState[room]
		if st == nil {
			continue
		}
		reg := registry[room]
		if reg == nil {
			if st.Registered {
				st.Registered = false
			}
			continue
		}

		st.Registered = true
		if reg.Founder != nil {
			st.Founder = reg.Founder
		}
		if reg.Topic != nil {
			st.Topic = strPtr(*reg.Topic)
		}
		st.Moderated = reg.Moderated
		st.InviteOnly = reg.InviteOnly
		st.TopicOpsOnly = reg.TopicOpsOnly
		st.NoOutsideMsgs = reg.NoOutsideMsgs
		st.Private = reg.Private
		if reg.Key != nil {
			st.Key = strPtr(*reg.Key)
		}
		st.Ops = copyHashSet(reg.Ops)
		st.Voiced = copyHashSet(reg.Voiced)
		st.Bans = copyHashSet(reg.Bans)
		st.Invited = appendInvites(nil, reg.Invited)
		if reg.LastUsedTS != nil {
			ts := *reg.LastUsedTS
			st.LastUsedTS = &ts
		}
	}
}

// sortedHexKeys renders a hex-keyed set as a sorted hex list.
func sortedHexKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PersistRoomState persists a registered room's state to the registry TOML
// file in the exact field order persist_room_state writes. It no-ops when
// the room is empty, no registry path is set, the state is missing, or the
// room is unregistered; a missing registry file produces the
// "room config persist failed: {e}" NOTICE without creating the file.
func (m *RoomManager) PersistRoomState(link *rns.Link, room string) {
	if room == "" {
		return
	}
	if m.hooks.RegistryPath == nil {
		return
	}
	regPath := m.hooks.RegistryPath()
	if regPath == "" {
		return
	}

	m.mu.Lock()
	st := m.roomState[room]
	if st == nil || !st.Registered {
		m.mu.Unlock()
		return
	}
	// Copy the state fields out under the state lock.
	stateCopy := *st
	stateCopy.Ops = copyHashSet(st.Ops)
	stateCopy.Voiced = copyHashSet(st.Voiced)
	stateCopy.Bans = copyHashSet(st.Bans)
	stateCopy.Invited = appendInvites(nil, st.Invited)
	m.mu.Unlock()

	m.registryWriteMu.Lock()
	defer m.registryWriteMu.Unlock()
	if err := persistRoomStateFile(regPath, room, &stateCopy); err != nil {
		if m.hooks.Notice != nil {
			m.hooks.Notice(link, room, fmt.Sprintf("room config persist failed: %v", err))
		}
	}
}

// persistRoomStateFile performs the file read-modify-write of one room's
// registry entry: stat for the mode restore, parse, table creation only
// when missing, the exact field write order, an in-place truncate+rewrite,
// and the best-effort chmod restore.
func persistRoomStateFile(regPath, room string, st *RoomState) error {
	var fileMode os.FileMode
	if info, statErr := os.Stat(regPath); statErr == nil {
		fileMode = info.Mode()
	} else {
		// The registry file must exist; never create it here.
		return statErr
	}

	data, err := os.ReadFile(regPath)
	if err != nil {
		return err
	}
	doc, err := toml.Parse(string(data))
	if err != nil {
		return err
	}

	roomTbl := doc.TablePath("rooms", room)

	if st.Founder != nil {
		roomTbl.Set("founder", toml.StringValue(hexKey(st.Founder)))
	}
	if st.Topic != nil && strings.TrimSpace(*st.Topic) != "" {
		roomTbl.Set("topic", toml.StringValue(*st.Topic))
	} else {
		roomTbl.Delete("topic")
	}
	roomTbl.Set("moderated", toml.BoolValue(st.Moderated))
	roomTbl.Set("invite_only", toml.BoolValue(st.InviteOnly))
	roomTbl.Set("topic_ops_only", toml.BoolValue(st.TopicOpsOnly))
	roomTbl.Set("no_outside_msgs", toml.BoolValue(st.NoOutsideMsgs))

	if st.Key != nil && *st.Key != "" {
		roomTbl.Set("key", toml.StringValue(*st.Key))
	} else {
		roomTbl.Delete("key")
	}

	lastUsed := float64(time.Now().UnixNano()) / 1e9
	if st.LastUsedTS != nil {
		lastUsed = *st.LastUsedTS
	}
	roomTbl.Set("last_used_ts", toml.FloatValue(lastUsed))

	roomTbl.Set("operators", toml.StringArrayValue(sortedHexKeys(st.Ops)))
	roomTbl.Set("voiced", toml.StringArrayValue(sortedHexKeys(st.Voiced)))
	roomTbl.Set("bans", toml.StringArrayValue(sortedHexKeys(st.Bans)))

	// Assigning a dict converts to a sub-table and replaces the previous
	// content, keeping only unexpired invites.
	now := float64(time.Now().UnixNano()) / 1e9
	invitedTbl := roomTbl.SetTable("invited")
	for _, inv := range st.Invited {
		if inv.Expires > now {
			invitedTbl.Set(hexKey(inv.Hash), toml.FloatValue(inv.Expires))
		}
	}

	newText := doc.Dump()
	if err := os.WriteFile(regPath, []byte(newText), fileMode); err != nil {
		return err
	}
	_ = os.Chmod(regPath, fileMode)
	return nil
}

// DeleteRoomFromRegistry removes a room's table from the registry TOML
// file, mirroring delete_room_from_registry, reporting failures with the
// "room unregister persist failed: {e}" NOTICE.
func (m *RoomManager) DeleteRoomFromRegistry(link *rns.Link, room string) {
	if m.hooks.RegistryPath == nil {
		return
	}
	regPath := m.hooks.RegistryPath()
	if regPath == "" {
		return
	}
	if err := deleteRoomFromFile(regPath, room); err != nil {
		if m.hooks.Notice != nil {
			m.hooks.Notice(link, room, fmt.Sprintf("room unregister persist failed: %v", err))
		}
	}
}

// deleteRoomFromFile parses the registry, deletes the room's table when
// present, and rewrites the file in place with the mode restored.
func deleteRoomFromFile(regPath, room string) error {
	var fileMode os.FileMode
	if info, statErr := os.Stat(regPath); statErr == nil {
		fileMode = info.Mode()
	} else {
		return statErr
	}
	data, err := os.ReadFile(regPath)
	if err != nil {
		return err
	}
	doc, err := toml.Parse(string(data))
	if err != nil {
		return err
	}
	if rooms := doc.LookupTable("rooms"); rooms != nil {
		rooms.DeleteTable(room)
	}
	if err := os.WriteFile(regPath, []byte(doc.Dump()), fileMode); err != nil {
		return err
	}
	_ = os.Chmod(regPath, fileMode)
	return nil
}
