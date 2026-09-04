// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newRouterTestEnv builds a HubService test environment with an injected
// identity and the include_joined_member_list flag fixed.
func newRouterTestEnv(t *testing.T, includeList bool) *hubTestEnv {
	t.Helper()
	env := newHubTestEnv(t)
	env.setDestination(t)
	env.hub.Config.IncludeJoinedMemberList = includeList
	return env
}

// identifyLink registers a link with the hub the way the live callbacks
// do: the session is created and the peer hash is adopted.
func (env *hubTestEnv) identifyLink(t *testing.T, link *rns.Link, peerHash []byte) {
	t.Helper()
	env.hub.OnLink(link)
	env.hub.OnRemoteIdentified(link, mustTestIdentified(t, peerHash))
}

// sendAs routes one envelope from a link through the hub and records the
// resulting sends, starting from an empty send log.
func (env *hubTestEnv) sendAs(link *rns.Link, envMap *cbor.Map) {
	env.sends = nil
	env.hub.OnPacket(link, cbor.Encode(envMap))
}

// helloFrom sends a HELLO envelope from a link and discards the WELCOME
// reply, leaving the session welcomed.
func (env *hubTestEnv) helloFrom(link *rns.Link, peerHash []byte, nick string) {
	env.sendAs(link, MakeEnvelope(int(THello), peerHash, WithNick(nick)))
}

// joinFrom sends a JOIN envelope from a link.
func (env *hubTestEnv) joinFrom(link *rns.Link, peerHash []byte, room string) {
	env.sendAs(link, MakeEnvelope(int(TJoin), peerHash, WithRoom(room)))
}

// partFrom sends a PART envelope from a link.
func (env *hubTestEnv) partFrom(link *rns.Link, peerHash []byte, room string) {
	env.sendAs(link, MakeEnvelope(int(TPart), peerHash, WithRoom(room)))
}

// decodedSends decodes every recorded send payload in order into raw CBOR
// maps so tests can assert optional-key presence.
func (env *hubTestEnv) decodedSends(t *testing.T) []*cbor.Map {
	t.Helper()
	var out []*cbor.Map
	for _, s := range env.sends {
		decoded, err := cbor.Decode(s.payload)
		if err != nil {
			t.Fatalf("sent payload does not decode: %v", err)
		}
		m, ok := decoded.(*cbor.Map)
		if !ok {
			t.Fatal("sent payload is not a CBOR map")
		}
		out = append(out, m)
	}
	return out
}

// sendLinks returns the link of every recorded send in order.
func (env *hubTestEnv) sendLinks() []*rns.Link {
	var out []*rns.Link
	for _, s := range env.sends {
		out = append(out, s.link)
	}
	return out
}

// envType returns the envelope's message type.
func envType(t *testing.T, m *cbor.Map) int64 {
	t.Helper()
	v, _ := m.Get(KT)
	n, _ := intValue(v)
	return n
}

// envNickString returns the envelope's nick and whether the key is present.
func envNickString(m *cbor.Map) (string, bool) {
	v, ok := m.Get(KNick)
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

// envRoomString returns the envelope's room text and whether the key is
// present.
func envRoomString(m *cbor.Map) (string, bool) {
	v, ok := m.Get(KRoom)
	if !ok {
		return "", false
	}
	s, isStr := v.(string)
	return s, isStr
}

// envHashList converts a decoded CBOR list-of-byte-strings body to hashes,
// reporting false when the body is absent or not a hash list.
func envHashList(body any, ok bool) ([][]byte, bool) {
	if !ok || body == nil {
		return nil, false
	}
	list, isList := body.([]any)
	if !isList {
		return nil, false
	}
	out := make([][]byte, 0, len(list))
	for _, item := range list {
		b, isBytes := item.([]byte)
		if !isBytes {
			return nil, false
		}
		out = append(out, b)
	}
	return out, true
}

// sortHashes sorts hashes by their bytes for order-insensitive comparison.
func sortHashes(hashes [][]byte) {
	sort.Slice(hashes, func(i, j int) bool {
		return string(hashes[i]) < string(hashes[j])
	})
}

// wantHashList is the expected member-hash set for the two-member room.
func wantHashList(hashes ...[]byte) [][]byte {
	out := make([][]byte, 0, len(hashes))
	out = append(out, hashes...)
	sortHashes(out)
	return out
}

// assertEnvelope checks one decoded envelope against its expected shape:
// the message type, the room value, the optional body, and the optional
// nick. A nil body means K_BODY must be absent.
func assertEnvelope(t *testing.T, name string, m *cbor.Map, wantType int64, wantRoom *string, wantBody any, wantNick *string) {
	t.Helper()
	if got := envType(t, m); got != wantType {
		t.Fatalf("%v: type = %v, want %v", name, got, wantType)
	}
	room, hasRoom := envRoomString(m)
	if wantRoom == nil {
		if hasRoom {
			t.Fatalf("%v: room = %q, want no K_ROOM", name, room)
		}
	} else if !hasRoom || room != *wantRoom {
		t.Fatalf("%v: room = %v, want %q", name, room, *wantRoom)
	}
	body, hasBody := m.Get(KBody)
	if wantBody == nil {
		if hasBody {
			t.Fatalf("%v: body = %v, want no K_BODY", name, pythonRepr(body))
		}
	} else if !hasBody {
		t.Fatalf("%v: K_BODY missing, want %v", name, pythonRepr(wantBody))
	} else if wantList, isList := wantBody.([][]byte); isList {
		gotList, ok := envHashList(body, true)
		if !ok {
			t.Fatalf("%v: body = %v, want a hash list", name, pythonRepr(body))
		}
		sortHashes(gotList)
		if len(gotList) != len(wantList) {
			t.Fatalf("%v: body = %v, want %v hashes", name, pythonRepr(gotList), len(wantList))
		}
		for i := range gotList {
			if string(gotList[i]) != string(wantList[i]) {
				t.Fatalf("%v: body = %v, want %v", name,
					hexKey(gotList[i]), hexKey(wantList[i]))
			}
		}
	} else if got, isStr := body.(string); !isStr || got != wantBody {
		t.Fatalf("%v: body = %v, want %v", name, pythonRepr(body), pythonRepr(wantBody))
	}
	nick, hasNick := envNickString(m)
	if wantNick == nil {
		if hasNick {
			t.Fatalf("%v: nick = %q, want no K_NICK", name, nick)
		}
	} else if !hasNick || nick != *wantNick {
		t.Fatalf("%v: nick = %v, want %q", name, nick, *wantNick)
	}
}

// G15.1 The router's JOIN fanout shapes: the JOINED fanout to existing
// members carries the actor hash body only when
// include_joined_member_list is set plus a nick; the joiner's own copy
// carries the full member list (when configured) but no nick; the room
// info NOTICE text is exact; the invite is consumed.
func TestRouterJoinFanoutShapes(t *testing.T) {
	t.Parallel()

	for _, includeList := range []bool{false, true} {
		env := newRouterTestEnv(t, includeList)

		joinerHash := bytesOf(0xaa, 32)
		memberHash := bytesOf(0xbb, 32)
		joinerLink := &rns.Link{}
		memberLink := &rns.Link{}
		env.identifyLink(t, memberLink, memberHash)
		env.identifyLink(t, joinerLink, joinerHash)
		env.helloFrom(memberLink, memberHash, "member")
		env.helloFrom(joinerLink, joinerHash, "joiner")

		// The first member joins: no existing members means no fanout,
		// only the self JOINED and the room info NOTICE.
		env.joinFrom(memberLink, memberHash, "general")
		maps := env.decodedSends(t)
		if len(maps) != 2 {
			t.Fatalf("first JOIN (includeList=%v): %v envelopes, want 2 (self JOINED + NOTICE): %v",
				includeList, len(maps), env.sendLinks())
		}
		firstSelf := maps[0]
		if got := env.sendLinks()[0]; got != memberLink {
			t.Fatalf("first JOIN: first send went to the wrong link")
		}
		wantSelfBody := any(nil)
		if includeList {
			wantSelfBody = wantHashList(memberHash)
		}
		assertEnvelope(t, "first-join self JOINED", firstSelf, TJoined, new("general"),
			wantSelfBody, nil)

		// The second member joins: the JOINED fanout to the existing
		// member, then the self copy, then the room info NOTICE.
		env.joinFrom(joinerLink, joinerHash, "general")
		maps = env.decodedSends(t)
		links := env.sendLinks()
		if len(maps) != 3 {
			t.Fatalf("second JOIN (includeList=%v): %v envelopes, want 3: %v",
				includeList, len(maps), links)
		}

		// Envelope 1: the fanout copy to the existing member. The body is
		// the one-element actor list only when the flag is set, and the
		// nick rides along (quirk 10: fanout copies carry K_NICK).
		wantFanoutBody := any(nil)
		if includeList {
			wantFanoutBody = wantHashList(joinerHash)
		}
		if links[0] != memberLink {
			t.Fatalf("second JOIN: first send went to the wrong link")
		}
		assertEnvelope(t, "fanout JOINED", maps[0], TJoined, new("general"),
			wantFanoutBody, new("joiner"))

		// Envelope 2: the joiner's own copy. The body is the full member
		// list only when the flag is set, and it never carries a nick.
		wantSelfBody = any(nil)
		if includeList {
			wantSelfBody = wantHashList(joinerHash, memberHash)
		}
		if links[1] != joinerLink {
			t.Fatalf("second JOIN: second send went to the wrong link")
		}
		assertEnvelope(t, "self JOINED", maps[1], TJoined, new("general"),
			wantSelfBody, nil)

		// Envelope 3: the exact room info NOTICE.
		if links[2] != joinerLink {
			t.Fatalf("second JOIN: third send went to the wrong link")
		}
		assertEnvelope(t, "room info NOTICE", maps[2], TNotice, new("general"),
			any("room general: unregistered; mode=(none); topic=(none)"), nil)
	}
}

// G15.1 (invite consumption) A JOIN consumes the joiner's invite from the
// room state; only the matching peer's invite is removed.
func TestRouterJoinConsumesInvite(t *testing.T) {
	t.Parallel()

	env := newRouterTestEnv(t, false)
	hub := env.hub

	joinerHash := bytesOf(0xaa, 32)
	otherHash := bytesOf(0xcc, 32)
	joinerLink := &rns.Link{}
	env.identifyLink(t, joinerLink, joinerHash)
	env.helloFrom(joinerLink, joinerHash, "guest")

	// The registered room carries two outstanding invites.
	hub.RoomManager.RegistrySet("general", &RoomState{Registered: true})
	st := hub.RoomManager.EnsureRoomState("general", nil)
	st.Invited = []Invite{
		{Hash: otherHash, Expires: 2e9},
		{Hash: joinerHash, Expires: 2e9},
	}

	env.joinFrom(joinerLink, joinerHash, "general")

	// The joiner's invite is consumed; the other peer's invite survives.
	invited := hub.RoomManager.RoomStateGet("general").Invited
	if len(invited) != 1 || string(invited[0].Hash) != string(otherHash) {
		t.Errorf("invited after JOIN = %+v, want only the other peer's invite", invited)
	}
}

// G15.2 The router's PART fanout shapes: the remaining members receive
// PARTED with the actor nick (suppressed while the same peer remains via
// another link), the parting client receives the self PARTED with the
// body iff include_joined_member_list, and room cleanup follows the
// registered/unregistered split.
func TestRouterPartFanoutShapes(t *testing.T) {
	t.Parallel()

	for _, includeList := range []bool{false, true} {
		// Case 1: two different peers in a room; one parts.
		env := newRouterTestEnv(t, includeList)

		partingHash := bytesOf(0xaa, 32)
		stayingHash := bytesOf(0xbb, 32)
		partingLink := &rns.Link{}
		stayingLink := &rns.Link{}
		env.identifyLink(t, stayingLink, stayingHash)
		env.identifyLink(t, partingLink, partingHash)
		env.helloFrom(stayingLink, stayingHash, "stayer")
		env.helloFrom(partingLink, partingHash, "leaver")
		env.joinFrom(stayingLink, stayingHash, "general")
		env.joinFrom(partingLink, partingHash, "general")

		env.partFrom(partingLink, partingHash, "general")
		maps := env.decodedSends(t)
		links := env.sendLinks()
		if len(maps) != 2 {
			t.Fatalf("PART (includeList=%v): %v envelopes, want 2: %v",
				includeList, len(maps), links)
		}

		// Envelope 1: the fanout copy to the remaining member, with the
		// parting peer's nick.
		wantFanoutBody := any(nil)
		if includeList {
			wantFanoutBody = wantHashList(partingHash)
		}
		if links[0] != stayingLink {
			t.Fatalf("PART: first send went to the wrong link")
		}
		assertEnvelope(t, "fanout PARTED", maps[0], TParted, new("general"),
			wantFanoutBody, new("leaver"))

		// Envelope 2: the parting client's self copy, never with a nick.
		wantSelfBody := any(nil)
		if includeList {
			wantSelfBody = wantHashList(partingHash)
		}
		if links[1] != partingLink {
			t.Fatalf("PART: second send went to the wrong link")
		}
		assertEnvelope(t, "self PARTED", maps[1], TParted, new("general"),
			wantSelfBody, nil)

		// Case 2: the same peer on a second link stays in the room; the
		// fanout to the remaining members is suppressed but the self
		// PARTED is still sent.
		env2 := newRouterTestEnv(t, includeList)
		peerHash := bytesOf(0xdd, 32)
		link1 := &rns.Link{}
		link2 := &rns.Link{}
		env2.identifyLink(t, link1, peerHash)
		env2.identifyLink(t, link2, peerHash)
		env2.helloFrom(link1, peerHash, "twin1")
		env2.helloFrom(link2, peerHash, "twin2")
		env2.joinFrom(link1, peerHash, "general")
		env2.joinFrom(link2, peerHash, "general")

		env2.partFrom(link1, peerHash, "general")
		maps = env2.decodedSends(t)
		links = env2.sendLinks()
		if len(maps) != 1 {
			t.Fatalf("same-peer PART (includeList=%v): %v envelopes, want 1 (self only): %v",
				includeList, len(maps), links)
		}
		wantSelfBody = any(nil)
		if includeList {
			wantSelfBody = wantHashList(peerHash)
		}
		if links[0] != link1 {
			t.Fatalf("same-peer PART: the send went to the wrong link")
		}
		assertEnvelope(t, "suppressed self PARTED", maps[0], TParted, new("general"),
			wantSelfBody, nil)
	}
}

// G15.2 A non-member PART still fans PARTED out to the room's members and
// returns the self PARTED to the parting client (Python ground truth:
// both happen even though the client was never in the room).
func TestRouterPartNonMemberStillFansOut(t *testing.T) {
	t.Parallel()

	env := newRouterTestEnv(t, false)
	hub := env.hub

	memberHash := bytesOf(0xbb, 32)
	strangerHash := bytesOf(0xee, 32)
	memberLink := &rns.Link{}
	strangerLink := &rns.Link{}
	env.identifyLink(t, memberLink, memberHash)
	env.identifyLink(t, strangerLink, strangerHash)
	env.helloFrom(memberLink, memberHash, "member")
	env.helloFrom(strangerLink, strangerHash, "guest")
	env.joinFrom(memberLink, memberHash, "general")

	// A welcomed client that never joined PARTs the room.
	env.partFrom(strangerLink, strangerHash, "general")
	maps := env.decodedSends(t)
	links := env.sendLinks()
	if len(maps) != 2 {
		t.Fatalf("non-member PART: %v envelopes, want 2: %v", len(maps), links)
	}
	if links[0] != memberLink || links[1] != strangerLink {
		t.Fatalf("non-member PART sends went to %v, want member then stranger", links)
	}
	assertEnvelope(t, "fanout PARTED", maps[0], TParted, new("general"),
		nil, new("guest"))
	// The stranger has no nick: the fanout nick is the session nick, which
	// is unset, so make_envelope drops it.
	assertEnvelope(t, "self PARTED", maps[1], TParted, new("general"), nil, nil)

	// The member remains in the room.
	if got := hub.RoomManager.GetRoomMembers("general"); len(got) != 1 || !got[memberLink] {
		t.Errorf("room members after a non-member PART = %v", got)
	}
}

// G15.2 The last member's PART cleans up an unregistered room's state but
// keeps a registered room's state and persists it.
func TestRouterPartRoomCleanup(t *testing.T) {
	t.Parallel()

	// Unregistered room: the state is deleted with the last member.
	env := newRouterTestEnv(t, false)
	hub := env.hub
	soloHash := bytesOf(0xaa, 32)
	soloLink := &rns.Link{}
	env.identifyLink(t, soloLink, soloHash)
	env.helloFrom(soloLink, soloHash, "solo")
	env.joinFrom(soloLink, soloHash, "general")
	if hub.RoomManager.RoomStateGet("general") == nil {
		t.Fatal("the unregistered room state is missing after JOIN")
	}

	env.partFrom(soloLink, soloHash, "general")
	if st := hub.RoomManager.RoomStateGet("general"); st != nil {
		t.Errorf("the unregistered room state survived the last PART: %+v", st)
	}
	if got := hub.RoomManager.GetRoomMembers("general"); len(got) != 0 {
		t.Errorf("room members after the last PART = %v, want none", got)
	}

	// Registered room: the state survives and the registry file is
	// rewritten with the fresh last_used_ts.
	regDir := testutils.TempDir(t, "router-part-registry")
	regPath := writeTemp(t, regDir, "rooms.toml", `[rooms]

[rooms.general]
founder = "`+hexKey(soloHash)+`"
registered = true
`)
	env2 := newRouterTestEnv(t, false)
	hub2 := env2.hub
	roomPath := regPath
	hub2.Config.RoomRegistryPath = &roomPath
	registry, errMsg := hub2.RoomManager.LoadRegistryFromPath(regPath)
	if errMsg != "" {
		t.Fatalf("LoadRegistryFromPath error: %v", errMsg)
	}
	hub2.RoomManager.ReplaceRegistry(registry)

	regHash := bytesOf(0xcc, 32)
	regLink := &rns.Link{}
	env2.identifyLink(t, regLink, regHash)
	env2.helloFrom(regLink, regHash, "founder")
	env2.joinFrom(regLink, regHash, "general")
	if st := hub2.RoomManager.RoomStateGet("general"); st == nil || !st.Registered {
		t.Fatalf("the registered room state is wrong after JOIN: %+v", st)
	}

	env2.partFrom(regLink, regHash, "general")
	if st := hub2.RoomManager.RoomStateGet("general"); st == nil || !st.Registered {
		t.Errorf("the registered room state was deleted by the last PART: %+v", st)
	}
	data, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "last_used_ts") {
		t.Errorf("the registered room was not persisted: %q", string(data))
	}
}

// G15.3 The PING→PONG echo: every PONG echoes the PING body verbatim —
// 8-byte bytes, the empty byte string, text, and float bodies — while an
// absent or CBOR-null body produces a PONG without K_BODY. The
// pings_in/pongs_out counters advance once per PING.
func TestHandlePingEchoesBodyVerbatim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body any
	}{
		{name: "8-byte bytes body", body: []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x23, 0x45, 0x67}},
		{name: "empty bytes body", body: []byte{}},
		{name: "text body", body: "hello"},
		{name: "float body", body: 1.5},
		{name: "list body", body: []any{int64(1), "two"}},
	}

	for _, tt := range tests {
		env := newRouterTestEnv(t, false)
		peerHash := bytesOf(0xaa, 32)
		link := &rns.Link{}
		env.identifyLink(t, link, peerHash)
		env.helloFrom(link, peerHash, "pinger")

		env.sendAs(link, MakeEnvelope(int(TPing), peerHash, WithBody(tt.body)))

		maps := env.decodedSends(t)
		if len(maps) != 1 {
			t.Fatalf("%v: %v envelopes, want 1 PONG", tt.name, len(maps))
		}
		pong := maps[0]
		if got := envType(t, pong); got != TPong {
			t.Fatalf("%v: PONG type = %v, want %v", tt.name, got, TPong)
		}
		gotBody, hasBody := pong.Get(KBody)
		switch want := tt.body.(type) {
		case []byte:
			if !hasBody {
				t.Fatalf("%v: PONG has no K_BODY", tt.name)
			}
			got, ok := gotBody.([]byte)
			if !ok || string(got) != string(want) {
				t.Errorf("%v: PONG body = %v, want the same bytes %x", tt.name, pythonRepr(gotBody), want)
			}
		case string:
			if !hasBody || gotBody != any(want) {
				t.Errorf("%v: PONG body = %v, want %q", tt.name, pythonRepr(gotBody), want)
			}
		case float64:
			if !hasBody {
				t.Fatalf("%v: PONG has no K_BODY", tt.name)
			}
			got, ok := gotBody.(float64)
			if !ok || got != want {
				t.Errorf("%v: PONG body = %v, want %v", tt.name, pythonRepr(gotBody), want)
			}
		case []any:
			if !hasBody {
				t.Fatalf("%v: PONG has no K_BODY", tt.name)
			}
			got, ok := gotBody.([]any)
			if !ok || len(got) != len(want) {
				t.Errorf("%v: PONG body = %v, want a %v-element list", tt.name, pythonRepr(gotBody), len(want))
			}
		}

		// Counters advance exactly once per PING.
		if got := env.hub.StatsManager.Counter("pings_in"); got != 1 {
			t.Errorf("%v: pings_in = %v, want 1", tt.name, got)
		}
		if got := env.hub.StatsManager.Counter("pongs_out"); got != 1 {
			t.Errorf("%v: pongs_out = %v, want 1", tt.name, got)
		}
	}
}

// G15.3 A PING without K_BODY and a PING whose body is CBOR null both
// produce a PONG that omits K_BODY, and the empty byte string echo is the
// two-byte CBOR 0x40 on the wire.
func TestHandlePingBodyEdgeCases(t *testing.T) {
	t.Parallel()

	// Absent K_BODY: no body key in the PONG.
	env := newRouterTestEnv(t, false)
	peerHash := bytesOf(0xaa, 32)
	link := &rns.Link{}
	env.identifyLink(t, link, peerHash)
	env.helloFrom(link, peerHash, "pinger")
	env.sendAs(link, MakeEnvelope(int(TPing), peerHash))
	maps := env.decodedSends(t)
	if len(maps) != 1 {
		t.Fatalf("bodyless PING: %v envelopes, want 1", len(maps))
	}
	if _, hasBody := maps[0].Get(KBody); hasBody {
		t.Error("a bodyless PING produced a PONG with K_BODY")
	}

	// CBOR-null body: decoded as nil, the PONG omits K_BODY too.
	env2 := newRouterTestEnv(t, false)
	link2 := &rns.Link{}
	env2.identifyLink(t, link2, peerHash)
	env2.helloFrom(link2, peerHash, "pinger")
	env2.sendAs(link2, MakeEnvelope(int(TPing), peerHash, WithBody(nil)))
	maps = env2.decodedSends(t)
	if len(maps) != 1 {
		t.Fatalf("null-body PING: %v envelopes, want 1", len(maps))
	}
	if _, hasBody := maps[0].Get(KBody); hasBody {
		t.Error("a null-body PING produced a PONG with K_BODY")
	}

	// The empty byte string echo is exactly CBOR 0x40 on the wire.
	env3 := newRouterTestEnv(t, false)
	link3 := &rns.Link{}
	env3.identifyLink(t, link3, peerHash)
	env3.helloFrom(link3, peerHash, "pinger")
	env3.sendAs(link3, MakeEnvelope(int(TPing), peerHash, WithBody([]byte{})))
	if len(env3.sends) != 1 {
		t.Fatalf("empty-bytes PING: %v envelopes, want 1", len(env3.sends))
	}
	payload := env3.sends[0].payload
	if !containsHexSuffix(payload, "40") {
		t.Errorf("the empty byte string echo is not 0x40: %x", payload)
	}
}

// containsHexSuffix reports whether the payload ends with the given hex.
func containsHexSuffix(payload []byte, hexTail string) bool {
	text := hexKey(payload)
	return strings.HasSuffix(text, hexTail)
}

// G15.4 A PONG clears the session's pending-ping marker and increments
// pongs_in; the link is then eligible for a fresh PING.
func TestHandlePongClearsAwaitingPong(t *testing.T) {
	t.Parallel()

	env := newRouterTestEnv(t, false)
	peerHash := bytesOf(0xaa, 32)
	link := &rns.Link{}
	env.identifyLink(t, link, peerHash)
	env.helloFrom(link, peerHash, "ponger")
	env.hub.SessionManager.SetAwaitingPong(link, 123.0)
	if env.hub.SessionManager.AwaitingPong(link) == nil {
		t.Fatal("the pending PING marker was not recorded")
	}

	env.sendAs(link, MakeEnvelope(int(TPong), peerHash))

	if got := env.hub.SessionManager.AwaitingPong(link); got != nil {
		t.Errorf("AwaitingPong after PONG = %v, want nil", got)
	}
	if got := env.hub.StatsManager.Counter("pongs_in"); got != 1 {
		t.Errorf("pongs_in = %v, want 1", got)
	}
	// A PONG carries no reply: the send log stays empty.
	if len(env.sends) != 0 {
		t.Errorf("a PONG produced replies: %v", env.sends)
	}
}
