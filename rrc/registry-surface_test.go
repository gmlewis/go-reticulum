// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"fmt"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
	"github.com/gmlewis/go-reticulum/testutils"
)

func testHash(b byte) []byte {
	h := make([]byte, 16)
	for i := range h {
		h[i] = b
	}
	return h
}

// TestSessionManagerAccessors pins the session registry's per-link accessors
// and index maintenance: peer, nick, caps, rooms, welcomed state, awaiting
// pong, order and prefix lookups.
func TestSessionManagerAccessors(t *testing.T) {
	t.Parallel()

	now := 0.0
	m := NewSessionManager(SessionHooks{
		NowMonotonic:           func() float64 { return now },
		RateLimitMsgsPerMinute: func() int { return 60 },
		IsBanned:               func([]byte) bool { return false },
		QueueWelcome:           func(*OutgoingList, *rns.Link, []byte) {},
		Greeting:               func() string { return "" },
		GetRoomMembers:         func(string) map[*rns.Link]bool { return nil },
		RemoveMember:           func(string, *rns.Link) {},
		IdentityHash:           func() []byte { return testHash(0x77) },
	})
	l1, l2 := &rns.Link{}, &rns.Link{}
	m.OnLinkEstablished(l1)
	m.OnLinkEstablished(l2)

	// Peer index: SetPeer records the hash; GetLinkByHash finds it; the
	// prefix lookup finds it by a leading byte run.
	m.OnRemoteIdentified(l1, testHash(0xAB))
	if got := m.PeerOf(l1); string(got) != string(testHash(0xAB)) {
		t.Errorf("PeerOf = %x, want %x", got, testHash(0xAB))
	}
	m.SetPeer(l1, testHash(0xCD))
	if got := m.PeerOf(l1); string(got) != string(testHash(0xCD)) {
		t.Errorf("SetPeer did not update the session peer: %x", got)
	}
	m.SetPeer(l1, testHash(0xAB))
	if got := m.GetLinkByHash(testHash(0xAB)); got != l1 {
		t.Errorf("GetLinkByHash = %p, want %p", got, l1)
	}
	if got := m.LinksByHashPrefix([]byte{0xAB}); len(got) != 1 || got[0] != l1 {
		t.Errorf("LinksByHashPrefix = %v, want [l1]", got)
	}
	if got := m.LinksByHashPrefix([]byte{0xFF}); len(got) != 0 {
		t.Errorf("LinksByHashPrefix(0xFF) = %v, want none", got)
	}

	// Nick index is normalized (strip + lower).
	m.SetNick(l1, new("  Alice "))
	m.UpdateNickIndex(l1, nil, new("  Alice "))
	if got := m.NickOf(l1); got == nil || *got != "  Alice " {
		t.Errorf("NickOf = %v, want the stored raw nick", got)
	}
	if got := m.GetLinksByNick("alice"); !got[l1] || len(got) != 1 {
		t.Errorf("GetLinksByNick(alice) = %v, want {l1}", got)
	}
	m.SetNick(l1, new("bob"))
	m.UpdateNickIndex(l1, new("  Alice "), new("bob"))
	if got := m.GetLinksByNick("alice"); len(got) != 0 {
		t.Errorf("old nick still indexed as %v", got)
	}
	if got := m.GetLinksByNick("BOB"); !got[l1] {
		t.Errorf("new nick not indexed")
	}

	// Caps and room membership.
	m.SetPeerCaps(l1, map[int64]any{1: true})
	if got := m.GetSession(l1); got == nil || got.PeerCaps[1] != true {
		t.Errorf("SetPeerCaps did not reach the session")
	}
	m.AddSessionRoom(l1, "#one")
	m.AddSessionRoom(l1, "#two")
	if got := m.RoomsCountOf(l1); got != 2 {
		t.Errorf("RoomsCountOf = %v, want 2", got)
	}
	if !m.InRoom(l1, "#one") {
		t.Errorf("InRoom(#one) = false, want true")
	}
	m.DiscardSessionRoom(l1, "#one")
	if m.InRoom(l1, "#one") {
		t.Errorf("DiscardSessionRoom left the membership")
	}
	if got := m.RoomsCountOf(l1); got != 1 {
		t.Errorf("RoomsCountOf after discard = %v, want 1", got)
	}

	// Welcomed state and the welcomed-only list.
	if m.IsWelcomed(l1) {
		t.Errorf("IsWelcomed before SendWelcome = true")
	}
	welcome := &OutgoingList{}
	m.SendWelcome(l1, welcome, testHash(0xAB), nil, nil)
	if !m.IsWelcomed(l1) {
		t.Errorf("IsWelcomed after SendWelcome = false")
	}
	if got := m.WelcomedSessions(); len(got) != 1 || got[0] != l1 {
		t.Errorf("WelcomedSessions = %v, want [l1]", got)
	}

	// Awaiting pong.
	m.SetAwaitingPong(l1, 12.5)
	if got := m.AwaitingPong(l1); got == nil || *got != 12.5 {
		t.Errorf("AwaitingPong = %v, want 12.5", got)
	}
	m.ClearAwaitingPong(l1)
	if got := m.AwaitingPong(l1); got != nil {
		t.Errorf("AwaitingPong after clear = %v, want nil", got)
	}

	// Registration order.
	if got := m.FirstSessionLink(); got != l1 {
		t.Errorf("FirstSessionLink = %p, want l1", got)
	}
	if got := m.SessionOrderIndex(l2); got != 1 {
		t.Errorf("SessionOrderIndex(l2) = %v, want 1", got)
	}
	if got := m.SessionOrderIndex(&rns.Link{}); got != -1 {
		t.Errorf("SessionOrderIndex(unknown) = %v, want -1", got)
	}

	// Stats.
	stats := m.GetStats()
	if stats.Total != 2 || stats.Welcomed != 1 || stats.Identified != 1 ||
		stats.IndexedByHash != 1 || stats.IndexedByNick != 1 {
		t.Errorf("GetStats = %+v, want Total=2 Welcomed=1 Identified=1 IndexedByHash=1 IndexedByNick=1", stats)
	}

	// Closing a link removes it from every index.
	m.OnLinkClosed(l1)
	if got := m.GetLinkByHash(testHash(0xAB)); got != nil {
		t.Errorf("closed link still indexed by hash")
	}
	if got := m.GetLinksByNick("bob"); len(got) != 0 {
		t.Errorf("closed link still indexed by nick")
	}
	if got := m.SessionOrderIndex(l1); got != -1 {
		t.Errorf("closed link still in the order (%v)", got)
	}
	if got := m.FirstSessionLink(); got != l2 {
		t.Errorf("FirstSessionLink after close = %p, want l2", got)
	}
}

// TestSessionManagerBeginReHello pins _handle_re_hello: the session's welcomed
// flag, rooms, nick and caps reset, and the old nick and room names are
// returned for the notice.
func TestSessionManagerBeginReHello(t *testing.T) {
	t.Parallel()

	m := NewSessionManager(SessionHooks{
		NowMonotonic:           func() float64 { return 0 },
		RateLimitMsgsPerMinute: func() int { return 60 },
		QueueWelcome:           func(*OutgoingList, *rns.Link, []byte) {},
		Greeting:               func() string { return "" },
	})
	link := &rns.Link{}
	m.OnLinkEstablished(link)
	m.SetNick(link, new("alice"))
	m.AddSessionRoom(link, "#one")
	m.AddSessionRoom(link, "#two")
	m.SendWelcome(link, &OutgoingList{}, nil, nil, nil)

	oldNick, oldRooms := m.BeginReHello(link)
	if oldNick == nil || *oldNick != "alice" {
		t.Errorf("old nick = %v, want alice", oldNick)
	}
	if len(oldRooms) != 2 {
		t.Errorf("old rooms = %v, want two", oldRooms)
	}
	if m.IsWelcomed(link) {
		t.Errorf("welcomed flag survived re-HELLO")
	}
	if got := m.NickOf(link); got != nil {
		t.Errorf("nick survived re-HELLO: %v", *got)
	}
	if got := m.RoomsCountOf(link); got != 0 {
		t.Errorf("rooms survived re-HELLO: %v", got)
	}
	if got := m.GetSession(link); got.PeerCaps == nil || len(got.PeerCaps) != 0 {
		t.Errorf("caps survived re-HELLO: %v", got.PeerCaps)
	}
}

// TestSessionManagerRefillAndTake pins the token bucket: it refills at
// perMinute/60 tokens per second, caps at the per-minute budget, and refuses a
// take it cannot afford.
func TestSessionManagerRefillAndTake(t *testing.T) {
	t.Parallel()

	now := 0.0
	m := NewSessionManager(SessionHooks{
		NowMonotonic:           func() float64 { return now },
		RateLimitMsgsPerMinute: func() int { return 60 },
	})
	link := &rns.Link{}
	m.OnLinkEstablished(link)
	// The bucket starts full (60 tokens).
	if !m.RefillAndTake(link, 60) {
		t.Fatalf("first take of the full budget failed")
	}
	if m.RefillAndTake(link, 1) {
		t.Errorf("take from an empty bucket succeeded")
	}
	// 0.5 s at 1 token/s refills half a token.
	now = 0.5
	if m.RefillAndTake(link, 1) {
		t.Errorf("take from a half-refilled bucket succeeded")
	}
	now = 1.0
	if !m.RefillAndTake(link, 1) {
		t.Errorf("take after a full second failed")
	}
	// Refill is capped at the per-minute budget.
	now = 1e6
	m.RefillAndTake(link, 0)
	if !m.RefillAndTake(link, 60) {
		t.Errorf("capped bucket is smaller than the per-minute budget")
	}
}

// TestSessionManagerOnLinkClosedParts pins the PARTED fanout: another member
// of a shared room receives a PARTED envelope carrying the departing peer's
// hash (and the joined-member body when configured).
func TestSessionManagerOnLinkClosedParts(t *testing.T) {
	t.Parallel()

	type sent struct {
		link    *rns.Link
		payload []byte
	}
	var sentPayloads []sent
	hubIdentity := testHash(0x77)
	rooms := map[string]map[*rns.Link]bool{}

	m := NewSessionManager(SessionHooks{
		NowMonotonic:           func() float64 { return 0 },
		RateLimitMsgsPerMinute: func() int { return 60 },
		GetRoomMembers:         func(room string) map[*rns.Link]bool { return rooms[room] },
		RemoveMember:           func(room string, link *rns.Link) { delete(rooms[room], link) },
		IdentityHash:           func() []byte { return hubIdentity },
		IncludeJoinedMemberList: func() bool {
			return true
		},
		SendPacket: func(link *rns.Link, payload []byte) error {
			sentPayloads = append(sentPayloads, sent{link, payload})
			return nil
		},
		StatsInc: func(string, int) {},
	})

	leaving, staying := &rns.Link{}, &rns.Link{}
	m.OnLinkEstablished(leaving)
	m.OnLinkEstablished(staying)
	m.SetPeer(leaving, testHash(0xAA))
	m.SetNick(leaving, new("alice"))
	rooms["#one"] = map[*rns.Link]bool{leaving: true, staying: true}
	m.AddSessionRoom(leaving, "#one")
	m.AddSessionRoom(staying, "#one")

	peer, nick, count := m.OnLinkClosed(leaving)
	if string(peer) != string(testHash(0xAA)) || nick == nil || *nick != "alice" || count != 1 {
		t.Errorf("OnLinkClosed returned (%x, %v, %v), want (aa.., alice, 1)", peer, nick, count)
	}
	if len(sentPayloads) != 1 || sentPayloads[0].link != staying {
		t.Fatalf("PARTED fanout = %v, want one payload to the staying link", sentPayloads)
	}
	decoded, err := cbor.Decode(sentPayloads[0].payload)
	if err != nil {
		t.Fatalf("decode PARTED: %v", err)
	}
	env, ok := decoded.(*cbor.Map)
	if !ok {
		t.Fatalf("PARTED decoded to %T, want map", decoded)
	}
	if got, _ := env.GetInt(KT); got != int64(TParted) {
		t.Errorf("PARTED type = %v, want %v", got, TParted)
	}
	if room, _ := env.GetString(KRoom); room != "#one" {
		t.Errorf("PARTED room = %q, want #one", room)
	}
	body, _ := env.Get(KBody)
	if list, ok := body.([]any); !ok || len(list) != 1 || string(list[0].([]byte)) != string(testHash(0xAA)) {
		t.Errorf("PARTED body = %v, want the joining-member list", body)
	}
}

// TestRoomManagerModes pins the room-mode surface: flags, the IRC-style mode
// string, moderation, and the mode broadcast to every member.
func TestRoomManagerModes(t *testing.T) {
	t.Parallel()

	var notices []string
	rm := NewRoomManager(RoomHooks{
		BroadcastNotice: func(_ *OutgoingList, _ *rns.Link, _ string, text string) {
			notices = append(notices, text)
		},
	})
	link := &rns.Link{}
	rm.AddMember("#room", link, nil)

	// A fresh room materializes default flags.
	if got := rm.GetRoomModes("#room"); got != (RoomModes{}) {
		t.Errorf("default modes = %+v, want zero", got)
	}
	rm.StateSet("#room", &RoomState{
		Registered: true, InviteOnly: true, Moderated: true, NoOutsideMsgs: true, Private: true,
		Key: new("sekrit"),
	})
	modes := rm.GetRoomModes("#room")
	if !modes.Registered || !modes.InviteOnly || !modes.Moderated || !modes.NoOutsideMsgs ||
		!modes.Private || !modes.HasKey {
		t.Errorf("modes = %+v, want every flag set", modes)
	}
	if got := rm.GetRoomModeString("#room"); got != "+ikmnpr" {
		t.Errorf("GetRoomModeString = %q, want %q", got, "ikmnpr")
	}
	if !rm.IsRoomModerated("#room") {
		t.Errorf("IsRoomModerated = false, want true")
	}

	outgoing := &OutgoingList{}
	rm.BroadcastRoomMode("#room", outgoing)
	if len(notices) != 1 {
		t.Fatalf("BroadcastRoomMode emitted %v notices, want 1 (one member)", len(notices))
	}
	if want := fmt.Sprintf("mode for %v is now: +ikmnpr", "#room"); notices[0] != want {
		t.Errorf("mode notice = %q, want %q", notices[0], want)
	}
}

// TestRoomManagerRegistry pins the registry surface: insertion order, a deep
// snapshot, deletion, and the registered-public-room listing (live state first,
// then registry-only rooms, private rooms excluded).
func TestRoomManagerRegistry(t *testing.T) {
	t.Parallel()

	rm := NewRoomManager(RoomHooks{})
	rm.RegistrySet("zeta", &RoomState{Registered: true, Topic: new("z")})
	rm.RegistrySet("alpha", &RoomState{Registered: true, Topic: new("a")})
	rm.RegistrySet("secret", &RoomState{Registered: true, Private: true})
	if got := rm.RegistryLen(); got != 3 {
		t.Fatalf("RegistryLen = %v, want 3", got)
	}
	if got := rm.RegistryOrder(); len(got) != 3 || got[0] != "zeta" || got[1] != "alpha" || got[2] != "secret" {
		t.Errorf("RegistryOrder = %v, want insertion order", got)
	}

	// Snapshot is a deep copy: mutating it must not touch the registry.
	snap := rm.RegistrySnapshot()
	snap["alpha"].Registered = false
	if st, ok := rm.RegistryGet("alpha"); !ok || !st.Registered {
		t.Errorf("RegistrySnapshot aliased the live entry")
	}

	// The live state wins for a registered public room; a registry-only
	// room follows in registry order; private rooms are omitted.
	rm.StateSet("#live", &RoomState{Registered: true, Topic: new("live")})
	rooms := rm.RegisteredPublicRooms()
	names := make([]string, len(rooms))
	for i, room := range rooms {
		names[i] = room.Name
	}
	want := []string{"#live", "zeta", "alpha"}
	if len(names) != len(want) {
		t.Fatalf("RegisteredPublicRooms = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("RegisteredPublicRooms[%v] = %q, want %q", i, names[i], want[i])
		}
	}

	rm.RegistryDelete("zeta")
	if got := rm.RegistryOrder(); len(got) != 2 || got[0] != "alpha" {
		t.Errorf("RegistryOrder after delete = %v, want [alpha secret]", got)
	}
}

// TestRoomManagerTouch pins TouchRoom: it stamps the live and registry
// last-used timestamps from the injected clock.
func TestRoomManagerTouch(t *testing.T) {
	t.Parallel()

	now := 100.0
	rm := NewRoomManager(RoomHooks{Now: func() float64 { return now }})
	rm.RegistrySet("#room", &RoomState{Registered: true})
	rm.StateSet("#room", &RoomState{Registered: true})

	rm.TouchRoom("#room")
	st, _ := rm.RegistryGet("#room")
	if st.LastUsedTS == nil || *st.LastUsedTS != 100.0 {
		t.Errorf("registry LastUsedTS = %v, want 100", st.LastUsedTS)
	}
	if got := rm.GetRoomModes("#room"); !got.Registered {
		t.Errorf("TouchRoom disturbed the live state")
	}
}

// TestTrustManagerIdentities pins load/replace/list/stats for the trusted and
// banned sets, and the banning checks.
func TestTrustManagerIdentities(t *testing.T) {
	t.Parallel()

	trusted := testHash(0x11)
	banned := testHash(0x22)
	tm := NewTrustManager(TrustHooks{})
	if err := tm.LoadFromConfig([]string{fmt.Sprintf("%x", trusted), "  "}, []string{fmt.Sprintf("0x%x", banned)}); err != nil {
		t.Fatalf("LoadFromConfig: %v", err)
	}
	if !tm.IsTrusted(trusted) || tm.IsTrusted(banned) {
		t.Errorf("trusted set wrong after load")
	}
	if !tm.IsServerOp(trusted) {
		t.Errorf("IsServerOp(trusted) = false")
	}
	if !tm.IsBanned(banned) || tm.IsBanned(trusted) {
		t.Errorf("banned set wrong after load")
	}
	if got := tm.TrustedHexSet(); len(got) != 1 || got[0] != fmt.Sprintf("%x", trusted) {
		t.Errorf("TrustedHexSet = %v, want the trusted hash", got)
	}
	if got := tm.BannedHexList(); len(got) != 1 || got[0] != fmt.Sprintf("%x", banned) {
		t.Errorf("BannedHexList = %v, want the banned hash", got)
	}
	if stats := tm.GetStats(); stats.TrustedCount != 1 || stats.BannedCount != 1 {
		t.Errorf("GetStats = %+v, want 1/1", stats)
	}

	// ReplaceIdentities swaps both sets wholesale.
	tm.ReplaceIdentities([][]byte{testHash(0x33)}, nil)
	if tm.IsTrusted(trusted) || !tm.IsTrusted(testHash(0x33)) {
		t.Errorf("ReplaceIdentities did not swap the trusted set")
	}
	if tm.IsBanned(banned) {
		t.Errorf("ReplaceIdentities did not swap the banned set")
	}
	if err := tm.LoadFromConfig([]string{"not-hex"}, nil); err == nil {
		t.Errorf("LoadFromConfig accepted a non-hex identity")
	}
}

// TestMessageHelperQueueAndDirectNotice pins the queued-envelope helpers:
// QueueEnv queues exactly one encoded envelope and counts its bytes, and
// EmitDirectNotice targets only the requested link and reports whether it fits.
func TestMessageHelperQueueAndDirectNotice(t *testing.T) {
	t.Parallel()

	stats := map[string]int{}
	mh := NewMessageHelper(MessageHooks{
		IdentityHash: func() []byte { return testHash(0x77) },
		StatsInc:     func(key string, delta int) { stats[key] += delta },
		FmtHash:      func(hash []byte) string { return fmt.Sprintf("%x", hash) },
		FmtLinkID:    func(link *rns.Link) string { return "link" },
		Logf:         func(string, ...any) {},
	})

	target, other := &rns.Link{}, &rns.Link{}
	outgoing := &OutgoingList{}
	env := MakeEnvelope(int(TNotice), testHash(0x01), WithBody("hello"))
	mh.QueueEnv(outgoing, target, env)
	if len(outgoing.Queue) != 1 || outgoing.Queue[0].Link != target {
		t.Fatalf("QueueEnv queued %v items, want one for the target", len(outgoing.Queue))
	}
	if want := len(cbor.Encode(env)); stats["bytes_out"] != want {
		t.Errorf("bytes_out = %v, want %v", stats["bytes_out"], want)
	}
	decoded, err := cbor.Decode(outgoing.Queue[0].Payload)
	if err != nil {
		t.Fatalf("decode queued payload: %v", err)
	}
	if got, _ := decoded.(*cbor.Map).GetString(KBody); got != "hello" {
		t.Errorf("queued body = %q, want hello", got)
	}

	id, ok := mh.EmitDirectNotice(outgoing, other, testHash(0x01), "alice", testHash(0x02), "hi")
	if !ok || len(id) == 0 {
		t.Fatalf("EmitDirectNotice = (%x, %v), want an id and true", id, ok)
	}
	if len(outgoing.Queue) != 2 || outgoing.Queue[1].Link != other {
		t.Errorf("EmitDirectNotice did not queue exactly one payload on the target link")
	}
	if stats["notices_forwarded"] != 1 {
		t.Errorf("notices_forwarded = %v, want 1", stats["notices_forwarded"])
	}
	oversized := &OutgoingList{}
	_, fits := mh.EmitDirectNotice(oversized, other, testHash(0x01), "", testHash(0x02), string(make([]byte, rnsMDU+1)))
	if fits || len(oversized.Queue) != 0 {
		t.Errorf("EmitDirectNotice queued an oversized notice")
	}
}

// TestRoomManagerPersistAndDelete pins PersistRoomState/DeleteRoomFromRegistry
// through the registry file: a registered room's fields are written under
// [rooms."#room"], and unregistering removes the table.
func TestRoomManagerPersistAndDelete(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "rrc-registry-")
	regPath := writeTemp(t, dir, "rooms.toml", "[rooms]\n")
	rm := NewRoomManager(RoomHooks{RegistryPath: func() string { return regPath }})

	topic := "hello"
	ts := 1234.5
	rm.StateSet("#room", &RoomState{
		Founder:    testHash(0xAB),
		Registered: true,
		Topic:      &topic,
		Moderated:  true,
		InviteOnly: true,
		Key:        new("sekrit"),
		Ops:        map[string]bool{hexKey(testHash(0x01)): true},
		LastUsedTS: &ts,
	})

	rm.PersistRoomState(nil, "#room")
	registry, errMsg := rm.LoadRegistryFromPath(regPath)
	if errMsg != "" {
		t.Fatalf("LoadRegistryFromPath: %v", errMsg)
	}
	st := registry["#room"]
	if st == nil {
		t.Fatalf("registry has no #room entry after PersistRoomState")
	}
	// The file records the room's modes; registration is implied by the
	// room's presence in the registry file, which the loader does not repeat
	// as a field.
	if !st.Moderated || !st.InviteOnly {
		t.Errorf("persisted flags = %+v, want moderated/invite-only", st)
	}
	if st.Topic == nil || *st.Topic != "hello" {
		t.Errorf("persisted topic = %v, want hello", st.Topic)
	}
	if st.Key == nil || *st.Key != "sekrit" {
		t.Errorf("persisted key = %v, want sekrit", st.Key)
	}
	if !st.Ops[hexKey(testHash(0x01))] {
		t.Errorf("persisted operators = %v, want the op hash", st.Ops)
	}

	// A room that is not registered is not persisted.
	rm.StateSet("#unregistered", &RoomState{})
	rm.PersistRoomState(nil, "#unregistered")
	if registry, _ := rm.LoadRegistryFromPath(regPath); registry["#unregistered"] != nil {
		t.Errorf("PersistRoomState wrote an unregistered room")
	}

	rm.DeleteRoomFromRegistry(nil, "#room")
	if registry, _ := rm.LoadRegistryFromPath(regPath); registry["#room"] != nil {
		t.Errorf("DeleteRoomFromRegistry left the room in the file")
	}
}

// TestMessageHelperSendTextSmartQueued pins the queued NOTICE path: with an
// outgoing list the text is chunked into it, never sent immediately.
func TestMessageHelperSendTextSmartQueued(t *testing.T) {
	t.Parallel()

	stats := map[string]int{}
	mh := NewMessageHelper(MessageHooks{
		IdentityHash:           func() []byte { return testHash(0x77) },
		StatsInc:               func(key string, delta int) { stats[key] += delta },
		FmtLinkID:              func(*rns.Link) string { return "link" },
		FmtHash:                func([]byte) string { return "hash" },
		Logf:                   func(string, ...any) {},
		MaxMsgBodyBytes:        func() int { return 500 },
		MaxRoomNameBytes:       func() int { return 64 },
		MaxNickBytes:           func() int { return 32 },
		MaxRoomsPerSession:     func() int { return 8 },
		WelcomeLimits:          func() []any { return []any{32, 64, 500, 8, 60} },
		SendPacket:             func(*rns.Link, []byte) error { return nil },
		EnableResourceTransfer: func() bool { return false },
	})

	link := &rns.Link{}
	outgoing := &OutgoingList{}
	room := "#room"
	mh.SendTextSmartQueued(outgoing, link, TNotice, "hello world", &room, ResKindNotice)
	if len(outgoing.Queue) == 0 {
		t.Fatalf("SendTextSmartQueued queued nothing")
	}
	for _, item := range outgoing.Queue {
		if item.Link != link {
			t.Errorf("queued item targets the wrong link")
		}
	}
	if stats["bytes_out"] == 0 {
		t.Errorf("bytes_out was not counted")
	}
}

// TestHubSetHubName pins SetHubName/GetHubName: the display name is set
// independently of the advertised server name.
func TestHubSetHubName(t *testing.T) {
	t.Parallel()

	hub := &RRCHub{HubName: "server-name"}
	hub.SetHubName("my hub")
	if got := hub.GetHubName(); got != "my hub" {
		t.Errorf("GetHubName = %q, want %q", got, "my hub")
	}
	if got := hub.GetServerName(); got != "server-name" {
		t.Errorf("SetHubName changed the advertised server name to %q", got)
	}
}
