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
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// sentPacket records one immediate (out-of-queue) payload send.
type sentPacket struct {
	link    *rns.Link
	payload []byte
}

// commandTestEnv is a test hub wiring a CommandHandler to real manager
// instances with recording hooks.
type commandTestEnv struct {
	identity      []byte
	serverOp      bool
	sentPackets   []sentPacket
	reloadCalls   int
	reloadRooms   []*string
	stats         map[string]int
	registryPath  string
	inviteTimeout float64
	nowSec        float64
	rm            *RoomManager
	sm            *SessionManager
	tm            *TrustManager
	mh            *MessageHelper
	chat          *CommandHandler
}

// makeServerOp promotes a peer hash to server operator.
func (e *commandTestEnv) makeServerOp(peer []byte) {
	if err := e.tm.LoadFromConfig([]string{hexKey(peer)}, nil); err != nil {
		panic(err)
	}
}

// newCommandTestEnv builds the test hub.
func newCommandTestEnv(t *testing.T) *commandTestEnv {
	t.Helper()
	env := &commandTestEnv{
		identity: bytesOf(0x21, 32),
		stats:    map[string]int{},
	}
	tm := NewTrustManager(TrustHooks{})
	rm := NewRoomManager(RoomHooks{
		IsServerOp: tm.IsServerOp,
		Now:        func() float64 { return 1730000000.0 },
	})
	sm := NewSessionManager(SessionHooks{
		GetRoomMembers:         rm.GetRoomMembers,
		RemoveMember:           rm.RemoveMember,
		RateLimitMsgsPerMinute: func() int { return 240 },
		IsBanned:               tm.IsBanned,
	})
	env.registryPath = ""
	env.inviteTimeout = 0
	env.nowSec = 1730000000.0
	mh := NewMessageHelper(MessageHooks{
		IdentityHash: func() []byte { return env.identity },
		StatsInc: func(key string, delta int) {
			env.stats[key] += delta
		},
		SendPacket: func(link *rns.Link, payload []byte) error {
			env.sentPackets = append(env.sentPackets, sentPacket{link: link, payload: payload})
			return nil
		},
	})
	rm.hooks.BroadcastNotice = func(outgoing *OutgoingList, l *rns.Link, room, text string) {
		mh.EmitNotice(outgoing, l, &room, text)
	}
	chat := NewCommandHandler(CommandHandlerHooks{
		TrustManager:   func() *TrustManager { return tm },
		SessionManager: func() *SessionManager { return sm },
		RoomManager:    func() *RoomManager { return rm },
		MessageHelper:  func() *MessageHelper { return mh },
		IdentityHash:   func() []byte { return env.identity },
		ParseIdentityHash: func(text string) ([]byte, error) {
			return ParseIdentityHash(text)
		},
		FmtHash: func(hash []byte, prefix int) string {
			if hash == nil {
				return "-"
			}
			text := hex.EncodeToString(hash)
			if prefix <= 0 {
				return text
			}
			if prefix > len(text) {
				prefix = len(text)
			}
			return text[:prefix]
		},
		RegistryPath:       func() string { return env.registryPath },
		RoomInviteTimeoutS: func() float64 { return env.inviteTimeout },
		Now:                func() float64 { return env.nowSec },
		ReloadConfigAndRooms: func(_ *rns.Link, room *string, _ *OutgoingList) {
			env.reloadCalls++
			env.reloadRooms = append(env.reloadRooms, room)
		},
		NormRoom: func(room string) (string, error) {
			nr := strings.ToLower(strings.TrimFunc(room, isUnicodeSpace))
			if nr == "" {
				return "", errors.New("room name must not be empty")
			}
			if len(nr) > 32 {
				return "", fmt.Errorf("room name too long: %v bytes > %v bytes", len(nr), 32)
			}
			return nr, nil
		},
	})
	env.rm, env.sm, env.tm, env.mh, env.chat = rm, sm, tm, mh, chat
	return env
}

// newTestCommandHandler builds the test hub and returns the handler with
// the environment for observation.
func newTestCommandHandler(t *testing.T) (*CommandHandler, *commandTestEnv) {
	t.Helper()
	env := newCommandTestEnv(t)
	return env.chat, env
}

// testEnvelope is one decoded RRC envelope captured from output.
type testEnvelope struct {
	msgType int64
	src     []byte
	room    *string
	body    any
}

// decodeOutgoing decodes every queued payload in order.
func decodeOutgoing(t *testing.T, outgoing *OutgoingList) []testEnvelope {
	t.Helper()
	var out []testEnvelope
	for _, item := range outgoing.Queue {
		decoded, err := cbor.Decode(item.Payload)
		if err != nil {
			t.Fatalf("queued payload does not decode: %v", err)
		}
		m, ok := decoded.(*cbor.Map)
		if !ok {
			t.Fatal("queued payload is not a CBOR map")
		}
		out = append(out, envelopeToTest(m))
	}
	return out
}

// decodeSent decodes every immediately sent payload in order.
func decodeSent(t *testing.T, env *commandTestEnv) []testEnvelope {
	t.Helper()
	var out []testEnvelope
	for _, p := range env.sentPackets {
		decoded, err := cbor.Decode(p.payload)
		if err != nil {
			t.Fatalf("sent payload does not decode: %v", err)
		}
		m, ok := decoded.(*cbor.Map)
		if !ok {
			t.Fatalf("sent payload is not a CBOR map")
		}
		out = append(out, envelopeToTest(m))
	}
	return out
}

func envelopeToTest(m *cbor.Map) testEnvelope {
	e := testEnvelope{}
	if v, ok := m.Get(KT); ok {
		e.msgType, _ = intValue(v)
	}
	if v, ok := m.Get(KSrc); ok {
		e.src, _ = v.([]byte)
	}
	if room, ok := m.Get(KRoom); ok {
		if s, isStr := room.(string); isStr {
			e.room = &s
		}
	}
	e.body, _ = m.Get(KBody)
	return e
}

// G9.1 skeleton: the text must strip to a "/"-prefixed command line whose
// argument list is non-empty, and unimplemented or unknown commands return
// false.
func TestHandleOperatorCommandSkeleton(t *testing.T) {
	t.Parallel()

	chat, _ := newTestCommandHandler(t)
	link := &rns.Link{}

	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "plain chat text", text: "hello world", want: false},
		{name: "slash not first", text: "hi /reload", want: false},
		{name: "empty text", text: "", want: false},
		{name: "whitespace only", text: " \t\r\n\x0b\x0c", want: false},
		{name: "bare slash", text: "/", want: false},
		{name: "slash plus spaces", text: "/   ", want: false},
		{name: "unknown command", text: "/frobnicate a b", want: false},
		{name: "empty command", text: "// a", want: false},
		{name: "unimplemented command stays unknown", text: "/wibble", want: false},
	}

	for _, tt := range tests {
		if got := chat.HandleOperatorCommand(link, []byte("peer"), nil, tt.text, nil); got != tt.want {
			t.Errorf("HandleOperatorCommand(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// G9.4 /reload and /stats mirror the Python reload and stats commands:
// both are server-op-gated with a room-nil `not authorized` ERROR for
// non-ops (skipped entirely when the hub identity is nil), /reload
// delegates to the reload hook with room=nil, and /stats emits the
// formatted stats NOTICE with room=nil.
func TestHandleOperatorCommandReloadStats(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)

	// Non-op /reload: room-nil not-authorized ERROR, no reload call.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/reload", nil); !got {
		t.Error("/reload should be recognized for a non-op")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room != nil {
		t.Fatalf("non-op /reload output = %+v, want one room-nil ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /reload body = %q, want %q", body, "not authorized")
	}
	if env.stats["errors_sent"] != 1 {
		t.Errorf("errors_sent = %v, want 1", env.stats["errors_sent"])
	}
	if env.reloadCalls != 0 {
		t.Errorf("reload calls = %v, want 0", env.reloadCalls)
	}

	// Non-op with no hub identity: handled silently, no output.
	env.sentPackets = nil
	env.stats["errors_sent"] = 0
	env.identity = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/reload", nil); !got {
		t.Error("/reload without hub identity should still be recognized")
	}
	if outs := decodeSent(t, env); len(outs) != 0 {
		t.Errorf("identity-less /reload output = %+v, want none", outs)
	}
	env.identity = bytesOf(0x21, 32)

	// Server-op /reload delegates to the hook with room=nil.
	env.makeServerOp(peer)
	if got := chat.HandleOperatorCommand(link, peer, nil, "/reload", nil); !got {
		t.Error("/reload should be recognized for a server op")
	}
	if env.reloadCalls != 1 || len(env.reloadRooms) != 1 || env.reloadRooms[0] != nil {
		t.Errorf("reload calls = %v rooms = %v, want 1 call with room=nil",
			env.reloadCalls, env.reloadRooms)
	}
	if outs := decodeSent(t, env); len(outs) != 0 {
		t.Errorf("op /reload output = %+v, want none", outs)
	}

	// Uppercase command names are lower-cased.
	env.reloadCalls = 0
	if got := chat.HandleOperatorCommand(link, peer, nil, "/RELOAD", nil); !got {
		t.Error("/RELOAD should be recognized")
	}
	if env.reloadCalls != 1 {
		t.Errorf("reload calls = %v, want 1", env.reloadCalls)
	}

	// Server-op /stats: one room-nil NOTICE with the formatted body.
	env.stats["errors_sent"] = 0
	statsText := "statshubs_total=1"
	chat.hooks.FormatStats = func() string { return statsText }
	if got := chat.HandleOperatorCommand(link, peer, nil, "/stats", nil); !got {
		t.Error("/stats should be recognized for a server op")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("op /stats output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != statsText {
		t.Errorf("op /stats body = %q, want %q", body, statsText)
	}
	if env.reloadCalls != 1 {
		t.Errorf("reload calls = %v, want unchanged 1", env.reloadCalls)
	}

	// Non-op /stats: room-nil not-authorized ERROR, no NOTICE. A fresh
	// peer hash keeps the earlier server-op promotion out of the way.
	env.sentPackets = nil
	nonOp := bytesOf(0xdd, 32)
	if got := chat.HandleOperatorCommand(link, nonOp, nil, "/stats", nil); !got {
		t.Error("/stats should be recognized for a non-op")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room != nil {
		t.Fatalf("non-op /stats output = %+v, want one room-nil ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /stats body = %q, want %q", body, "not authorized")
	}
}

// G9.5 /list mirrors the Python list command: registered public rooms come
// from the live state (registered && !private), registry-only rooms join
// when not private (the registry loop checks only the private flag), a
// room present in both counts once via the state, the empty case emits the
// no-rooms notice, and non-empty output is sorted by room name.
func TestHandleOperatorCommandList(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm

	if got := chat.HandleOperatorCommand(link, peer, nil, "/list", nil); !got {
		t.Error("/list should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("empty /list output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "No public rooms registered" {
		t.Errorf("empty /list body = %q, want %q", body, "No public rooms registered")
	}

	topic := "chat about things"
	secretTopic := "secret topic"
	st1 := rm.EnsureRoomState("general", nil)
	st1.Registered = true
	st1.Topic = &topic
	st2 := rm.EnsureRoomState("lounge", nil)
	st2.Registered = true
	st3 := rm.EnsureRoomState("secret", nil)
	st3.Registered = true
	st3.Private = true
	st3.Topic = &secretTopic
	rm.EnsureRoomState("casual", nil)

	rm.RegistrySet("helpdesk", &RoomState{Registered: true, Topic: &topic})
	rm.RegistrySet("hidden", &RoomState{Registered: true, Private: true, Topic: &secretTopic})
	rm.RegistrySet("general", &RoomState{Registered: true})

	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/list", nil); !got {
		t.Error("/list should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("/list output = %+v, want one room-nil NOTICE", sent)
	}
	want := "Registered public rooms:\n" +
		"  general - chat about things\n" +
		"  helpdesk - chat about things\n" +
		"  lounge"
	if body, _ := sent[0].body.(string); body != want {
		t.Errorf("/list body =\n%q\nwant\n%q", body, want)
	}
}

// G9.6 /who and /names mirror the Python who command: the target defaults
// to the command room, an absent or empty target emits the usage notice,
// a normalization failure emits `bad room: {e}`, a private room hides its
// member list from non-server-ops, and the members line renders
// `nick ({hash12})` or the bare hash entries joined with ", ". Member
// order: identified sessions first by peer hash, unidentified sessions
// after in registration order (documented divergence: Python sorts by
// object id(), which is arbitrary).
func TestHandleOperatorCommandWho(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm

	// No target room: the usage notice.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/who", nil); !got {
		t.Error("/who should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bare /who output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /who [room]" {
		t.Errorf("bare /who body = %q, want usage", body)
	}

	// Empty command room with no argument: usage again.
	env.sentPackets = nil
	emptyRoom := ""
	if got := chat.HandleOperatorCommand(link, peer, &emptyRoom, "/names", nil); !got {
		t.Error("/names should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 {
		t.Fatalf("/names with empty room output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /who [room]" {
		t.Errorf("/names empty-room body = %q, want usage", body)
	}

	// Members rendering: nicks with a 12-char hash prefix, sessions
	// without nicks render the full hash, unidentified peers render ?.
	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	linkD := &rns.Link{}
	identSession(rm, sm, linkA, bytesOf(0x11, 32), "Alice", "general")
	identSession(rm, sm, linkB, bytesOf(0x22, 32), "", "general")
	identSession(rm, sm, linkC, nil, "Carol", "general")
	sm.OnLinkEstablished(linkD)
	linkDSess := sm.GetSession(linkD)
	linkDSess.Rooms["general"] = true
	rm.AddMember("general", linkD, nil)

	hash22 := hexKey(bytesOf(0x22, 32))
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/who general", nil); !got {
		t.Error("/who general should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("/who general output = %+v, want one room-nil NOTICE", sent)
	}
	want := "members in general: Alice (111213141516), " + hash22 + ", Carol (?), ?"
	if body, _ := sent[0].body.(string); body != want {
		t.Errorf("/who general body =\n%q\nwant\n%q", body, want)
	}

	// Empty room: the (none) fallback.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/who emptyroom", nil); !got {
		t.Error("/who emptyroom should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 {
		t.Fatalf("/who emptyroom output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "members in emptyroom: (none)" {
		t.Errorf("/who emptyroom body = %q", body)
	}

	// Private room: hidden from non-ops, visible to server ops.
	st := rm.EnsureRoomState("secret", nil)
	st.Private = true
	identSession(rm, sm, linkD, bytesOf(0x33, 32), "Dave", "secret")

	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/who secret", nil); !got {
		t.Error("/who secret should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 {
		t.Fatalf("/who secret output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "room secret is private" {
		t.Errorf("/who secret body = %q, want private notice", body)
	}

	env.makeServerOp(peer)
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/who secret", nil); !got {
		t.Error("/who secret should be recognized for a server op")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 {
		t.Fatalf("op /who secret output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "members in secret: Dave (333435363738)" {
		t.Errorf("op /who secret body = %q", body)
	}
}

// identSession materializes an identified session, mirroring the session
// state the hub would hold after HELLO and identification: the session is
// created, the peer hash and nick set, and the room memberships registered
// with the room manager.
func identSession(rm *RoomManager, sm *SessionManager, link *rns.Link, peer []byte, nick string, rooms ...string) *Session {
	sm.OnLinkEstablished(link)
	sess := sm.GetSession(link)
	if peer != nil {
		sm.OnRemoteIdentified(link, peer)
	}
	if nick != "" {
		sm.UpdateNickIndex(link, nil, &nick)
		sess.Nick = &nick
	}
	for _, room := range rooms {
		sess.Rooms[room] = true
		rm.AddMember(room, link, nil)
	}
	return sess
}

// G9.2 target resolution mirrors _find_target_links: the hex-candidate
// path takes precedence over the nick-index path, the hex prefix must be
// at least 6 characters, an odd-length candidate falls through, and the
// room filter applies to both paths.
func TestFindTargetLinks(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	sm := env.sm
	rm := env.rm

	peerA := bytesOf(0xaa, 32)
	peerB := bytesOf(0xbb, 32)
	hexA := hexKey(peerA)
	hexB := hexKey(peerB)

	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	identSession(rm, sm, linkA, peerA, "Alice", "general")
	identSession(rm, sm, linkB, peerB, "bob")
	identSession(rm, sm, linkC, nil, "carol", "lounge")

	// Deterministic match order: matches sort by peer-hash hex, with
	// unidentified sessions last (documented divergence: Python's dict
	// order is identification order, Go's map order is random).
	linksEqualUnordered := func(got, want []*rns.Link) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[*rns.Link]bool{}
		for _, l := range want {
			seen[l] = true
		}
		for _, l := range got {
			if !seen[l] {
				return false
			}
		}
		return true
	}

	tests := []struct {
		name  string
		token string
		room  *string
		want  []*rns.Link
	}{
		{name: "empty token", token: "", want: nil},
		{name: "whitespace token", token: "   ", want: nil},
		{name: "full hex hash", token: hexA, want: []*rns.Link{linkA}},
		{name: "0x-prefixed full hash", token: "0x" + hexA, want: []*rns.Link{linkA}},
		{name: "six-character prefix", token: hexA[:6], want: []*rns.Link{linkA}},
		{name: "uppercase prefix", token: strings.ToUpper(hexA[:8]), want: []*rns.Link{linkA}},
		{name: "too-short hex goes to nick path", token: hexA[:4], want: nil},
		{name: "odd-length hex falls through to nick path",
			token: "oddnick", want: nil},
		{name: "non-hex token", token: "zzzzzz", want: nil},
		{name: "nick case-insensitive", token: "ALICE", want: []*rns.Link{linkA}},
		{name: "nick strip", token: "  alice  ", want: []*rns.Link{linkA}},
		{name: "no nick for empty index hit", token: "nobody", want: nil},
	}
	general := "general"
	lounge := "lounge"
	tests = append(tests,
		struct {
			name  string
			token string
			room  *string
			want  []*rns.Link
		}{name: "room filter keeps in-room hex match",
			token: hexA[:8], room: &general, want: []*rns.Link{linkA}},
		struct {
			name  string
			token string
			room  *string
			want  []*rns.Link
		}{name: "room filter drops out-of-room hex match",
			token: hexB[:8], room: &general, want: nil},
		struct {
			name  string
			token string
			room  *string
			want  []*rns.Link
		}{name: "room filter keeps in-room nick match",
			token: "carol", room: &lounge, want: []*rns.Link{linkC}},
		struct {
			name  string
			token string
			room  *string
			want  []*rns.Link
		}{name: "room filter drops out-of-room nick match",
			token: "bob", room: &general, want: nil},
	)

	for _, tt := range tests {
		got := chat.FindTargetLinks(tt.token, tt.room)
		if !linksEqualUnordered(got, tt.want) {
			t.Errorf("FindTargetLinks(%q, room=%v): got %v, want %v",
				tt.token, tt.room, got, tt.want)
		}
	}

	// A nick shared by two links matches both.
	linkD := &rns.Link{}
	peerD := bytesOf(0xcc, 32)
	identSession(rm, sm, linkD, peerD, "multi", "general")
	linkE := &rns.Link{}
	identSession(rm, sm, linkE, nil, "multi")
	if got := chat.FindTargetLinks("multi", nil); len(got) != 2 {
		t.Errorf("ambiguous nick matches = %v, want 2", len(got))
	}

	// Odd-length hex falls through to the nick path when the token is
	// also a nick.
	if got := chat.FindTargetLinks("aaabac1", nil); len(got) != 0 {
		t.Errorf("odd hex fall-through = %v, want none", got)
	}

	// _find_target_link: exactly one match returns the link.
	if got := chat.FindTargetLink(hexA, nil); got != linkA {
		t.Errorf("FindTargetLink full hash = %v, want linkA", got)
	}
	if got := chat.FindTargetLink("multi", nil); got != nil {
		t.Errorf("FindTargetLink ambiguous = %v, want nil", got)
	}
	if got := chat.FindTargetLink("nobody", nil); got != nil {
		t.Errorf("FindTargetLink none = %v, want nil", got)
	}
}

// G9.2 ResolveIdentityHashWithMatches mirrors _resolve_identity_hash_with_matches:
// one online match yields the peer hash, several yield no hash, and an
// offline token falls back to parsing.
func TestResolveIdentityHashWithMatches(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	sm := env.sm
	rm := env.rm

	peerA := bytesOf(0xaa, 32)
	hexA := hexKey(peerA)
	peerB := bytesOf(0xbb, 32)
	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	identSession(rm, sm, linkA, peerA, "Alice", "general")
	identSession(rm, sm, linkB, peerB, "alice")
	identSession(rm, sm, linkC, nil, "carol")

	// One match: the session peer wins.
	h, matches := chat.ResolveIdentityHashWithMatches(hexA, nil)
	if h == nil || string(h) != string(peerA) || len(matches) != 1 || matches[0] != linkA {
		t.Errorf("single match resolve = %v, %v", h, matches)
	}

	// Ambiguous: two online matches, no hash.
	h, matches = chat.ResolveIdentityHashWithMatches("alice", nil)
	if h != nil || len(matches) != 2 {
		t.Errorf("ambiguous resolve = %v, %v", h, matches)
	}

	// No matches, parseable offline hash.
	offline := bytesOf(0x5a, 32)
	h, matches = chat.ResolveIdentityHashWithMatches(hexKey(offline), nil)
	if h == nil || string(h) != string(offline) || matches != nil {
		t.Errorf("offline parse resolve = %v, %v", h, matches)
	}

	// No matches, unparseable token.
	h, matches = chat.ResolveIdentityHashWithMatches("zzzz", nil)
	if h != nil || matches != nil {
		t.Errorf("unparseable resolve = %v, %v", h, matches)
	}

	// A single match whose session is not identified falls through to
	// parsing (which fails for a nick).
	h, matches = chat.ResolveIdentityHashWithMatches("carol", nil)
	if h != nil || matches != nil {
		t.Errorf("unidentified single match resolve = %v, %v", h, matches)
	}

	// Parseable hex resolves through the parser.
	h, matches = chat.ResolveIdentityHashWithMatches("0xabcdef01", nil)
	if h == nil || hexKey(h) != "abcdef01" || matches != nil {
		t.Errorf("parse resolve = %v, %v", h, matches)
	}
}

// G9.3 FormatAmbiguousTargets mirrors _format_ambiguous_targets: the
// not-found text, one "  - {hash16} nick={nick!r}" line per identified
// match, and the disambiguation hint.
func TestFormatAmbiguousTargets(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	sm := env.sm
	rm := env.rm

	peerA := bytesOf(0xaa, 32)
	peerB := bytesOf(0xbb, 32)
	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	identSession(rm, sm, linkA, peerA, "Alice")
	identSession(rm, sm, linkB, peerB, "")
	// linkC is left without a session on purpose.

	hexA := hexKey(peerA)
	hexB := hexKey(peerB)

	tests := []struct {
		name    string
		token   string
		matches []*rns.Link
		want    string
	}{
		{
			name:    "no matches",
			token:   "zz",
			matches: nil,
			want:    "target 'zz' not found",
		},
		{
			name:    "identified match",
			token:   "al",
			matches: []*rns.Link{linkA},
			want: "ambiguous: 'al' matches 1 identities:\n" +
				"  - " + hexA[:16] + " nick='Alice'\n" +
				"Use full or longer identity hash to disambiguate.",
		},
		{
			name:    "empty nick renders as no nick",
			token:   "bb",
			matches: []*rns.Link{linkB},
			want: "ambiguous: 'bb' matches 1 identities:\n" +
				"  - " + hexB[:16] + " (no nick)\n" +
				"Use full or longer identity hash to disambiguate.",
		},
		{
			name:    "session-less matches are skipped",
			token:   "cc",
			matches: []*rns.Link{linkC},
			want:    "target 'cc' not found",
		},
		{
			name:    "multiple matches join with newlines",
			token:   "aa",
			matches: []*rns.Link{linkA, linkB},
			want: "ambiguous: 'aa' matches 2 identities:\n" +
				"  - " + hexA[:16] + " nick='Alice'\n" +
				"  - " + hexB[:16] + " (no nick)\n" +
				"Use full or longer identity hash to disambiguate.",
		},
	}

	for _, tt := range tests {
		if got := chat.FormatAmbiguousTargets(tt.token, tt.matches); got != tt.want {
			t.Errorf("FormatAmbiguousTargets(%q):\n got %q\nwant %q", tt.token, got, tt.want)
		}
	}

	// A nick containing a single quote and no double quote renders with
	// Python repr's double-quote form.
	linkQ := &rns.Link{}
	identSession(rm, sm, linkQ, bytesOf(0x11, 32), "bob's")
	want := "ambiguous: 'q' matches 1 identities:\n" +
		"  - " + hexKey(bytesOf(0x11, 32))[:16] + " nick=\"bob's\"\n" +
		"Use full or longer identity hash to disambiguate."
	if got := chat.FormatAmbiguousTargets("q", []*rns.Link{linkQ}); got != want {
		t.Errorf("quoted nick:\n got %q\nwant %q", got, want)
	}
}

// G9.7 /kick mirrors the Python kick command: usage and bad-room notices,
// the room-op gate, the room-scoped target resolution, the in-room check,
// the force-removal that bypasses remove_member (no PARTED fanout, the
// empty room lingers in the membership map), the `kicked from {r}` ERROR
// to the target, and the confirmation NOTICE.
func TestHandleOperatorCommandKick(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm

	// Fewer than three parts: usage notice with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kick general", nil); !got {
		t.Error("/kick should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("short /kick output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /kick <room> <nick|hashprefix>" {
		t.Errorf("short /kick body = %q", body)
	}

	// Bad target room: `bad room: {e}` with the command room attached.
	env.sentPackets = nil
	cmdRoom := "general"
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/kick badroomnameverylongindeedwayover32bytes ok", nil); !got {
		t.Error("/kick should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "general" {
		t.Fatalf("bad-room /kick output = %+v, want one general-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bad room: room name too long: 39 bytes > 32 bytes" {
		t.Errorf("bad-room /kick body = %q", body)
	}

	// Non-op: `not authorized` ERROR with the target room.
	env.sentPackets = nil
	target := bytesOf(0xbb, 32)
	linkT := &rns.Link{}
	identSession(rm, sm, linkT, target, "bob", "general")
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/kick general bob", nil); !got {
		t.Error("/kick should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "general" {
		t.Fatalf("non-op /kick output = %+v, want one general-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /kick body = %q", body)
	}

	// Op with no matching target: the not-found text with the command
	// room.
	env.makeServerOp(peer)
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/kick general nobody", nil); !got {
		t.Error("/kick should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "general" {
		t.Fatalf("missing-target /kick output = %+v, want one general-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "target 'nobody' not found" {
		t.Errorf("missing-target /kick body = %q", body)
	}

	// Op with a target outside the room: the room-scoped finder does
	// not match it, so the not-found text is emitted.
	env.sentPackets = nil
	linkOut := &rns.Link{}
	identSession(rm, sm, linkOut, bytesOf(0xcc, 32), "carol", "lounge")
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/kick general carol", nil); !got {
		t.Error("/kick should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("outside-room /kick output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "target 'carol' not found" {
		t.Errorf("outside-room /kick body = %q", body)
	}

	// A hash-prefix match whose session is gone (a stale index entry)
	// passes the room filter but fails the in-room check.
	env.sentPackets = nil
	linkStale := &rns.Link{}
	sm.OnLinkEstablished(linkStale)
	sm.OnRemoteIdentified(linkStale, bytesOf(0xdd, 32))
	delete(sm.sessions, linkStale)
	staleHash := hexKey(bytesOf(0xdd, 32))
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/kick general "+staleHash[:8], nil); !got {
		t.Error("/kick should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("stale-index /kick output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "target not in room" {
		t.Errorf("stale-index /kick body = %q", body)
	}

	// Successful kick: session room discarded, membership discarded
	// directly (the empty room lingers), `kicked from {r}` ERROR to the
	// target, `kicked bob from general` NOTICE to the issuer.
	env.sentPackets = nil
	env.stats["errors_sent"] = 0
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/kick general bob", nil); !got {
		t.Error("/kick should be recognized")
	}
	sessT := sm.GetSession(linkT)
	if sessT.Rooms["general"] {
		t.Error("kicked session still lists the room")
	}
	if stats := rm.GetRoomStats(); stats.RoomsTotal != 2 || stats.Memberships != 1 {
		t.Errorf("room stats after kick = %+v, want the empty general room lingering (2 rooms, 1 membership)",
			stats)
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("kick output = %+v, want two envelopes", sent)
	}
	kickErr := sent[0]
	if kickErr.msgType != TError || kickErr.room == nil || *kickErr.room != "general" {
		t.Errorf("kick target envelope = %+v, want ERROR room=general", kickErr)
	}
	if body, _ := kickErr.body.(string); body != "kicked from general" {
		t.Errorf("kick target body = %q, want kicked-from text", body)
	}
	if !sameBytes(kickErr.src, env.identity) {
		t.Errorf("kick target src = %v, want hub hash", kickErr.src)
	}
	kickNote := sent[1]
	if kickNote.msgType != TNotice || kickNote.room == nil || *kickNote.room != "general" {
		t.Errorf("kick issuer envelope = %+v, want NOTICE room=general", kickNote)
	}
	if body, _ := kickNote.body.(string); body != "kicked bob from general" {
		t.Errorf("kick issuer body = %q", body)
	}
	if env.stats["errors_sent"] != 1 {
		t.Errorf("errors_sent = %v, want 1", env.stats["errors_sent"])
	}
}

// G9.8 /kline mirrors the Python kline command: the server-op gate, the
// usage notices, the sorted list rendering, the online/offline/ambiguous
// add paths (an online but unidentified target still tears down and
// reports added without a ban), the parse failure text, and the
// del/not-klined paths.
func TestHandleOperatorCommandKline(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	sm := env.sm
	tm := env.tm

	// Non-op: room-nil not-authorized ERROR, no ban change.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline list", nil); !got {
		t.Error("/kline should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room != nil {
		t.Fatalf("non-op /kline output = %+v, want one room-nil ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /kline body = %q", body)
	}

	env.makeServerOp(peer)

	// Bare /kline: the usage notice.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline", nil); !got {
		t.Error("/kline should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bare /kline output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /kline add|del|list [nick|hashprefix|hash]" {
		t.Errorf("bare /kline body = %q", body)
	}

	// Empty list rendering.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline list", nil); !got {
		t.Error("/kline list should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("empty /kline list output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "klines: (none)" {
		t.Errorf("empty /kline list body = %q", body)
	}

	// Unknown subcommand: usage.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline frob x", nil); !got {
		t.Error("/kline frob should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("unknown-op /kline output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /kline add|del|list [nick|hashprefix|hash]" {
		t.Errorf("unknown-op /kline body = %q", body)
	}

	// add without a target: per-op usage.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline add", nil); !got {
		t.Error("/kline add should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("short /kline add output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /kline add <nick|hashprefix|hash>" {
		t.Errorf("short /kline add body = %q", body)
	}

	// Online target: ban + persist + teardown, notice names the token.
	env.sentPackets = nil
	bobPeer := bytesOf(0xbb, 32)
	linkBob := &rns.Link{}
	identSession(env.rm, sm, linkBob, bobPeer, "bob")
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline add bob", nil); !got {
		t.Error("/kline add bob should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("online /kline add output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "kline added for bob" {
		t.Errorf("online /kline add body = %q", body)
	}
	if !tm.IsBanned(bobPeer) {
		t.Error("online /kline add did not ban the target peer")
	}

	// The ban now shows in the sorted list.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline list", nil); !got {
		t.Error("/kline list should be recognized")
	}
	sent = decodeSent(t, env)
	bobHex := hexKey(bobPeer)
	if body, _ := sent[0].body.(string); body != "klines: "+bobHex {
		t.Errorf("/kline list body = %q, want klines: %v", body, bobHex)
	}

	// Ambiguous token: the ambiguous notice, no extra ban.
	env.sentPackets = nil
	linkA1 := &rns.Link{}
	linkA2 := &rns.Link{}
	identSession(env.rm, sm, linkA1, bytesOf(0x1a, 32), "alice")
	identSession(env.rm, sm, linkA2, bytesOf(0x1b, 32), "alice")
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline add alice", nil); !got {
		t.Error("/kline add alice should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("ambiguous /kline add output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); !strings.HasPrefix(body, "ambiguous: 'alice' matches 2 identities:") {
		t.Errorf("ambiguous /kline add body = %q", body)
	}

	// Offline parseable hash: ban + notice with the full hash.
	env.sentPackets = nil
	offline := bytesOf(0x5a, 32)
	offlineHex := hexKey(offline)
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline add "+offlineHex, nil); !got {
		t.Error("/kline add offline should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("offline /kline add output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "kline added for "+offlineHex {
		t.Errorf("offline /kline add body = %q", body)
	}
	if !tm.IsBanned(offline) {
		t.Error("offline /kline add did not ban the parsed hash")
	}

	// Unparseable token: the bad identity hash text.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline add zzzz!!", nil); !got {
		t.Error("/kline add zzzz!! should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("bad-hash /kline add output = %+v, want one NOTICE", sent)
	}
	wantBad := "bad identity hash: invalid identity hash 'zzzz!!': " +
		"non-hexadecimal number found in fromhex() arg at position 0"
	if body, _ := sent[0].body.(string); body != wantBad {
		t.Errorf("bad-hash /kline add body =\n%q\nwant\n%q", body, wantBad)
	}

	// del of a banned hash: removal + notice.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline del "+offlineHex, nil); !got {
		t.Error("/kline del should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("/kline del output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "kline removed for "+offlineHex {
		t.Errorf("/kline del body = %q", body)
	}
	if tm.IsBanned(offline) {
		t.Error("/kline del did not remove the ban")
	}

	// del of a non-banned hash: the not-klined notice.
	env.sentPackets = nil
	other := bytesOf(0x77, 32)
	if got := chat.HandleOperatorCommand(link, peer, nil, "/kline del "+hexKey(other), nil); !got {
		t.Error("/kline del should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("not-klined output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "not klined: "+hexKey(other) {
		t.Errorf("not-klined body = %q", body)
	}
}

// G9.9 /register mirrors the Python register command: the usage and
// bad-room notices, the presence check against the raw command room (with
// the over-long-room silent abort), the founder-only gate, the missing
// registry-path notice, the state flag updates with the founder op, the
// eight-field registry copy, and the confirmation notice.
func TestHandleOperatorCommandRegister(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm

	// Bare /register: usage with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/register", nil); !got {
		t.Error("/register should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bare /register output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /register <room>" {
		t.Errorf("bare /register body = %q", body)
	}

	// Bad room argument: `bad room: {e}` with room=nil.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/register badroomnameverylongindeedwayover32bytes", nil); !got {
		t.Error("/register should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bad-room /register output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bad room: room name too long: 39 bytes > 32 bytes" {
		t.Errorf("bad-room /register body = %q", body)
	}

	// No command room: the presence error with room=nil (mirrors
	// emitting the command room, which is None).
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/register general", nil); !got {
		t.Error("/register should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("no-room /register output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "must be present in the room to register it" {
		t.Errorf("no-room /register body = %q", body)
	}

	// An over-long raw command room aborts silently with no reply
	// (the normalization failure Python raises out of the dispatch).
	env.sentPackets = nil
	longRoom := "verylongrawroomnamethatiswellbeyond32"
	if got := chat.HandleOperatorCommand(link, peer, &longRoom, "/register general", nil); !got {
		t.Error("/register with an over-long raw room should be handled")
	}
	if outs := decodeSent(t, env); len(outs) != 0 {
		t.Errorf("over-long raw room output = %+v, want none", outs)
	}

	// Command room differing from the target room: the presence error.
	env.sentPackets = nil
	cmdRoom := "lounge"
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/register general", nil); !got {
		t.Error("/register should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("wrong-room /register output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "must be present in the room to register it" {
		t.Errorf("wrong-room /register body = %q", body)
	}

	// Present but not the founder: the founder-only ERROR with room=r.
	env.sentPackets = nil
	identSession(rm, sm, link, peer, "opalice", "lounge")
	rm.EnsureRoomState("lounge", bytesOf(0xbb, 32))
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/register lounge", nil); !got {
		t.Error("/register should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("non-founder /register output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "only the room founder can register" {
		t.Errorf("non-founder /register body = %q", body)
	}

	// Founder but no registry path: the no-registry-path notice with
	// the command room.
	env.sentPackets = nil
	rm.StateDelete("lounge")
	rm.EnsureRoomState("lounge", peer)
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/register lounge", nil); !got {
		t.Error("/register should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("no-registry /register output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "cannot register room: no room_registry_path" {
		t.Errorf("no-registry /register body = %q", body)
	}

	// Founder with a registry path: registration succeeds, the state
	// flags flip, the founder joins ops, the registry copy holds the
	// eight fields, and the notice confirms.
	env.registryPath = "/tmp/fake-rooms.toml"
	env.sentPackets = nil
	topic := "welcome topic"
	st := rm.RoomStateGet("lounge")
	st.Topic = &topic
	st.Moderated = true
	if st.Ops == nil {
		st.Ops = map[string]bool{}
	}
	st.Ops[hexKey(bytesOf(0x99, 32))] = true
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/register lounge", nil); !got {
		t.Error("/register should be recognized")
	}
	if !st.Registered || !st.NoOutsideMsgs || !st.TopicOpsOnly {
		t.Errorf("state flags after register = %v/%v/%v, want all true",
			st.Registered, st.NoOutsideMsgs, st.TopicOpsOnly)
	}
	if !st.Ops[hexKey(peer)] {
		t.Error("founder missing from ops after register")
	}
	reg, ok := rm.RegistryGet("lounge")
	if !ok {
		t.Fatal("registry entry missing after register")
	}
	if !reg.Registered || !sameBytes(reg.Founder, peer) {
		t.Errorf("registry entry = %+v, want registered with the founder hash", reg)
	}
	if reg.Topic == nil || *reg.Topic != topic {
		t.Errorf("registry topic = %v, want %q", reg.Topic, topic)
	}
	if !reg.Moderated || len(reg.Ops) != 2 || !reg.Ops[hexKey(bytesOf(0x99, 32))] || !reg.Ops[hexKey(peer)] {
		t.Errorf("registry ops/moderated = %v %v, want moderated with two ops", reg.Moderated, reg.Ops)
	}
	if reg.InviteOnly || reg.Key != nil || reg.LastUsedTS == nil {
		t.Errorf("registry entry carries forbidden fields: invite_only=%v key=%v last_used_ts=%v",
			reg.InviteOnly, reg.Key, reg.LastUsedTS)
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("success /register output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "registered room lounge" {
		t.Errorf("success /register body = %q", body)
	}
}

// G9.10 /unregister mirrors the Python unregister command: the usage and
// bad-room notices, the presence check with the over-long-room silent
// abort, the founder-only gate, the not-registered notice, and the
// registry/state teardown (the state is popped only when the room has no
// members).
func TestHandleOperatorCommandUnregister(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm

	// Bare /unregister: usage with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/unregister", nil); !got {
		t.Error("/unregister should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bare /unregister output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /unregister <room>" {
		t.Errorf("bare /unregister body = %q", body)
	}

	// Bad room argument: `bad room: {e}` with room=nil.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/unregister badroomnameverylongindeedwayover32bytes", nil); !got {
		t.Error("/unregister should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bad-room /unregister output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bad room: room name too long: 39 bytes > 32 bytes" {
		t.Errorf("bad-room /unregister body = %q", body)
	}

	// No command room: the presence error with room=nil.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/unregister lounge", nil); !got {
		t.Error("/unregister should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("no-room /unregister output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "must be present in the room to unregister it" {
		t.Errorf("no-room /unregister body = %q", body)
	}

	// An over-long raw command room aborts silently.
	env.sentPackets = nil
	longRoom := "verylongrawroomnamethatiswellbeyond32"
	if got := chat.HandleOperatorCommand(link, peer, &longRoom, "/unregister lounge", nil); !got {
		t.Error("/unregister with an over-long raw room should be handled")
	}
	if outs := decodeSent(t, env); len(outs) != 0 {
		t.Errorf("over-long raw room output = %+v, want none", outs)
	}

	// Non-founder: the founder-only ERROR with room=r.
	env.sentPackets = nil
	cmdRoom := "lounge"
	identSession(rm, sm, link, peer, "alice", "lounge")
	rm.EnsureRoomState("lounge", bytesOf(0xbb, 32))
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/unregister lounge", nil); !got {
		t.Error("/unregister should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("non-founder /unregister output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "only the room founder can unregister" {
		t.Errorf("non-founder /unregister body = %q", body)
	}

	// Founder, but the room is not registered: the not-registered
	// notice.
	env.sentPackets = nil
	rm.StateDelete("lounge")
	rm.EnsureRoomState("lounge", peer)
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/unregister lounge", nil); !got {
		t.Error("/unregister should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("unregistered-room /unregister output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "room lounge is not registered" {
		t.Errorf("unregistered-room /unregister body = %q", body)
	}

	// Success with a member still present: the flag flips, the registry
	// entry disappears, the state stays (the room is not empty), and
	// the notice confirms.
	env.sentPackets = nil
	rm.StateDelete("lounge")
	st := rm.EnsureRoomState("lounge", peer)
	st.Registered = true
	st.TopicOpsOnly = true
	rm.RegistrySet("lounge", &RoomState{Registered: true, Founder: peer})
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/unregister lounge", nil); !got {
		t.Error("/unregister should be recognized")
	}
	if st.Registered {
		t.Error("state still registered after unregister")
	}
	if _, stillThere := rm.RegistryGet("lounge"); stillThere {
		t.Error("registry entry survived unregister")
	}
	if rm.RoomStateGet("lounge") == nil {
		t.Error("state was popped although the room still has a member")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("success /unregister output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "unregistered room lounge" {
		t.Errorf("success /unregister body = %q", body)
	}

	// With no members left the state is popped as well.
	env.sentPackets = nil
	rm.StateDelete("temp")
	stTemp := rm.EnsureRoomState("temp", peer)
	stTemp.Registered = true
	rm.RegistrySet("temp", &RoomState{Registered: true, Founder: peer})
	sess := sm.GetSession(link)
	sess.Rooms["temp"] = true
	rm.AddMember("temp", link, peer)
	rm.DiscardMember("temp", link)
	tempRoom := "temp"
	if got := chat.HandleOperatorCommand(link, peer, &tempRoom, "/unregister temp", nil); !got {
		t.Error("/unregister should be recognized")
	}
	if rm.RoomStateGet("temp") != nil {
		t.Error("state survived unregister in an empty room")
	}
	if _, stillThere := rm.RegistryGet("temp"); stillThere {
		t.Error("registry entry survived unregister in an empty room")
	}
}

// G9.11 /topic mirrors the Python topic command: the usage and bad-room
// notices, the permission-less view, the +t gate for non-ops, the
// set/clear behavior, and the member fanout.
func TestHandleOperatorCommandTopic(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm

	// Bare /topic: usage with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/topic", nil); !got {
		t.Error("/topic should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bare /topic output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /topic <room> [topic]" {
		t.Errorf("bare /topic body = %q", body)
	}

	// Bad room argument: `bad room: {e}` with room=nil.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/topic badroomnameverylongindeedwayover32bytes", nil); !got {
		t.Error("/topic should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bad-room /topic output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bad room: room name too long: 39 bytes > 32 bytes" {
		t.Errorf("bad-room /topic body = %q", body)
	}

	// View as a plain member (no permission check on the view path).
	env.sentPackets = nil
	cmdRoom := "lounge"
	identSession(rm, sm, link, peer, "alice", "lounge")
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/topic lounge", nil); !got {
		t.Error("/topic should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("view /topic output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "topic for lounge: (none)" {
		t.Errorf("empty /topic view body = %q", body)
	}

	// Non-op with +t: the not-authorized (+t) ERROR with room=r.
	env.sentPackets = nil
	st := rm.EnsureRoomState("lounge", nil)
	st.TopicOpsOnly = true
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/topic lounge new topic", nil); !got {
		t.Error("/topic should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("+t /topic output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized (+t)" {
		t.Errorf("+t /topic body = %q", body)
	}

	// Op set: the topic sticks and every member receives the fanout.
	env.sentPackets = nil
	rm.StateDelete("lounge")
	rm.EnsureRoomState("lounge", peer)
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/topic lounge welcome to the lounge", nil); !got {
		t.Error("/topic should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if st.Topic == nil || *st.Topic != "welcome to the lounge" {
		t.Errorf("topic after set = %v, want welcome to the lounge", st.Topic)
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("set /topic output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "topic for lounge is now: welcome to the lounge" {
		t.Errorf("set /topic body = %q", body)
	}

	// The view now shows the stored topic.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/topic lounge", nil); !got {
		t.Error("/topic should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("topic view output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "topic for lounge: welcome to the lounge" {
		t.Errorf("topic view body = %q", body)
	}

	// A non-op may set the topic when +t is off (the +t flag is the
	// only permission check on the set path).
	env.sentPackets = nil
	linkOther := &rns.Link{}
	other := bytesOf(0xcc, 32)
	identSession(rm, sm, linkOther, other, "carol", "lounge")
	if got := chat.HandleOperatorCommand(linkOther, other, &cmdRoom, "/topic lounge carol topic", nil); !got {
		t.Error("/topic should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("non-op set /topic output = %+v, want two envelopes (carol + alice)", sent)
	}
	for _, item := range sent {
		if item.msgType != TNotice || item.room == nil || *item.room != "lounge" {
			t.Fatalf("non-op set /topic envelope = %+v, want lounge-room NOTICE", item)
		}
	}
	if body, _ := sent[0].body.(string); body != "topic for lounge is now: carol topic" {
		t.Errorf("non-op set /topic body = %q", body)
	}
}

// G9.12 /op, /deop, /voice, and /devoice mirror the Python commands: the
// usage and bad-room notices, the room-op gate, the room-scoped target
// resolution, the founder deop guard, and the op/voice set updates with
// persistence.
func TestHandleOperatorCommandOpDeopVoice(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm

	// Fewer than three parts: usage with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/op lounge", nil); !got {
		t.Error("/op should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("short /op output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /op <room> <nick|hashprefix|hash>" {
		t.Errorf("short /op body = %q", body)
	}

	// Bad room argument: `bad room: {e}` with room=nil.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/deop badroomnameverylongindeedwayover32bytes x", nil); !got {
		t.Error("/deop should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bad-room /deop output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bad room: room name too long: 39 bytes > 32 bytes" {
		t.Errorf("bad-room /deop body = %q", body)
	}

	// Non-op: `not authorized` ERROR with room=r.
	env.sentPackets = nil
	cmdRoom := "lounge"
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/op lounge carol", nil); !got {
		t.Error("/op should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("non-op /op output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /op body = %q", body)
	}

	// Room op (founder) grants op to carol; the founder must create
	// the room, so the founder membership comes first.
	env.sentPackets = nil
	peerCarol := bytesOf(0xcc, 32)
	linkCarol := &rns.Link{}
	rm.AddMember("lounge", link, peer)
	identSession(rm, sm, link, peer, "opalice", "lounge")
	identSession(rm, sm, linkCarol, peerCarol, "carol", "lounge")
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/op lounge carol", nil); !got {
		t.Error("/op should be recognized")
	}
	st := rm.RoomStateGet("lounge")
	if !st.Ops[hexKey(peerCarol)] {
		t.Errorf("ops after /op = %v, want carol's hash", st.Ops)
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("op /op output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "op granted in lounge" {
		t.Errorf("op /op body = %q", body)
	}

	// Deop of the founder is refused (resolved by full hash).
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/deop lounge "+hexKey(peer), nil); !got {
		t.Error("/deop should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("founder /deop output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "cannot deop founder" {
		t.Errorf("founder /deop body = %q", body)
	}

	// Deop of carol succeeds.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/deop lounge carol", nil); !got {
		t.Error("/deop should be recognized")
	}
	if st.Ops[hexKey(peerCarol)] {
		t.Error("carol still in ops after /deop")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("deop output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "op removed in lounge" {
		t.Errorf("deop body = %q", body)
	}

	// Voice grant and removal.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/voice lounge carol", nil); !got {
		t.Error("/voice should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if !st.Voiced[hexKey(peerCarol)] {
		t.Errorf("voiced after /voice = %v, want carol's hash", st.Voiced)
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("voice output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "voice granted in lounge" {
		t.Errorf("voice body = %q", body)
	}

	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/devoice lounge carol", nil); !got {
		t.Error("/devoice should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if st.Voiced[hexKey(peerCarol)] {
		t.Error("carol still voiced after /devoice")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("devoice output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "voice removed in lounge" {
		t.Errorf("devoice body = %q", body)
	}

	// Ambiguous target: the ambiguous notice with the command room.
	env.sentPackets = nil
	linkA1 := &rns.Link{}
	linkA2 := &rns.Link{}
	identSession(rm, sm, linkA1, bytesOf(0x1a, 32), "alice", "lounge")
	identSession(rm, sm, linkA2, bytesOf(0x1b, 32), "alice", "lounge")
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/op lounge alice", nil); !got {
		t.Error("/op should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("ambiguous /op output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); !strings.HasPrefix(body, "ambiguous: 'alice' matches 2 identities:") {
		t.Errorf("ambiguous /op body = %q", body)
	}
}

// G9.13 /mode mirrors the Python mode command: the usage and bad-room
// notices, the room-op gate, the simple flag toggles with the room-mode
// broadcast, the +k key handling, the +r redirection, the ±o/±v fanout
// with the founder deop guard, and the supported-modes fallback.
func TestHandleOperatorCommandMode(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm
	cmdRoom := "lounge"
	modeUsage := "usage: /mode <room> (+m|-m|+i|-i|+t|-t|+n|-n|+p|-p|+k|-k|+r|-r) [key] | /mode <room> (+o|-o|+v|-v) <nick|hashprefix|hash>"

	// Fewer than three parts: the long usage text with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/mode lounge", nil); !got {
		t.Error("/mode should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("short /mode output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != modeUsage {
		t.Errorf("short /mode body =\n%q\nwant\n%q", body, modeUsage)
	}

	// Non-op: `not authorized` ERROR with room=r.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +m", nil); !got {
		t.Error("/mode should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("non-op /mode output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /mode body = %q", body)
	}

	// Founder setup: the issuer creates the room, carol joins, and the
	// room-manager Notice hook fans out through the message helper.
	rm.AddMember("lounge", link, peer)
	peerCarol := bytesOf(0xcc, 32)
	linkCarol := &rns.Link{}
	identSession(rm, sm, linkCarol, peerCarol, "carol", "lounge")

	// +m sets the flag and broadcasts the new mode to every member.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +m", nil); !got {
		t.Error("/mode should be recognized")
	}
	st := rm.RoomStateGet("lounge")
	if !st.Moderated {
		t.Error("room not moderated after +m")
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("+m /mode output = %+v, want two broadcast envelopes", sent)
	}
	for _, item := range sent {
		if item.msgType != TNotice || item.room == nil || *item.room != "lounge" {
			t.Fatalf("+m /mode envelope = %+v, want lounge-room NOTICE", item)
		}
		if body, _ := item.body.(string); body != "mode for lounge is now: +m" {
			t.Errorf("+m /mode body = %q", body)
		}
	}

	// +k without a key argument: the per-flag usage with the command
	// room, no broadcast.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +k", nil); !got {
		t.Error("/mode should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("short +k /mode output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /mode <room> +k <key>" {
		t.Errorf("short +k /mode body = %q", body)
	}

	// +k with a multi-word key joins the words.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +k sekrit words", nil); !got {
		t.Error("/mode should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if st.Key == nil || *st.Key != "sekrit words" {
		t.Errorf("key after +k = %v, want sekrit words", st.Key)
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("+k /mode output = %+v, want two broadcast envelopes", sent)
	}
	if body, _ := sent[0].body.(string); body != "mode for lounge is now: +km" {
		t.Errorf("+k /mode body = %q, want mode for lounge is now: +km", body)
	}

	// -k clears the key.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge -k", nil); !got {
		t.Error("/mode should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if st.Key != nil {
		t.Errorf("key after -k = %v, want nil", st.Key)
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("-k /mode output = %+v, want two broadcast envelopes", sent)
	}
	if body, _ := sent[0].body.(string); body != "mode for lounge is now: +m" {
		t.Errorf("-k /mode body = %q", body)
	}

	// +r is redirected to /register.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +r", nil); !got {
		t.Error("/mode should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("+r /mode output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "use /register or /unregister to change +r" {
		t.Errorf("+r /mode body = %q", body)
	}

	// +o carol: ops gain carol and every member sees the +o fanout.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +o carol", nil); !got {
		t.Error("/mode should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if !st.Ops[hexKey(peerCarol)] {
		t.Errorf("ops after +o = %v, want carol's hash", st.Ops)
	}
	carolPrefix := hexKey(peerCarol)[:12]
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("+o /mode output = %+v, want two fanout envelopes", sent)
	}
	for _, item := range sent {
		if item.msgType != TNotice || item.room == nil || *item.room != "lounge" {
			t.Fatalf("+o /mode envelope = %+v, want lounge-room NOTICE", item)
		}
		if body, _ := item.body.(string); body != "mode for lounge is now: +o "+carolPrefix {
			t.Errorf("+o /mode body = %q", body)
		}
	}

	// -o of the founder is refused with the command room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge -o "+hexKey(peer), nil); !got {
		t.Error("/mode should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("founder -o /mode output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "cannot deop founder" {
		t.Errorf("founder -o /mode body = %q", body)
	}

	// An unknown flag: the supported-modes list with the command room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +x", nil); !got {
		t.Error("/mode should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("unknown-flag /mode output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "supported modes: +m -m +i -i +k -k +t -t +n -n +p -p +r -r +o -o +v -v" {
		t.Errorf("unknown-flag /mode body = %q", body)
	}
}

// G9.14 /ban mirrors the Python ban command: the usage and bad-room
// notices, the permission-less list, the unknown-op and short-arg usages,
// the room-op gate, the ban set updates, and the force-removal of online
// banned members (direct membership discard, no PARTED).
func TestHandleOperatorCommandBan(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm
	cmdRoom := "lounge"
	banUsage := "usage: /ban <room> add|del|list [nick|hashprefix|hash]"

	// Fewer than three parts: usage with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/ban lounge", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("short /ban output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != banUsage {
		t.Errorf("short /ban body = %q", body)
	}

	// Bad room argument: `bad room: {e}` with room=nil.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, nil, "/ban badroomnameverylongindeedwayover32bytes list", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("bad-room /ban output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bad room: room name too long: 39 bytes > 32 bytes" {
		t.Errorf("bad-room /ban body = %q", body)
	}

	// list with no bans (no permission check): room=cmd-room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge list", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("empty /ban list output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "no bans in lounge" {
		t.Errorf("empty /ban list body = %q", body)
	}

	// Unknown op: usage with the command room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge frob x", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("unknown-op /ban output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != banUsage {
		t.Errorf("unknown-op /ban body = %q", body)
	}

	// add without a target: the per-op usage with the command room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge add", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("short add /ban output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /ban lounge add <nick|hashprefix|hash>" {
		t.Errorf("short add /ban body = %q", body)
	}

	// Non-op add: `not authorized` ERROR with room=r.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge add carol", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("non-op /ban output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /ban body = %q", body)
	}

	// Op add: carol is banned, force-removed from the room (no PARTED,
	// the empty room lingers), receives the banned ERROR, and the
	// issuer gets the confirmation.
	rm.AddMember("lounge", link, peer)
	peerCarol := bytesOf(0xcc, 32)
	linkCarol := &rns.Link{}
	identSession(rm, sm, linkCarol, peerCarol, "carol", "lounge")
	env.sentPackets = nil
	env.stats["errors_sent"] = 0
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge add carol", nil); !got {
		t.Error("/ban should be recognized")
	}
	st := rm.RoomStateGet("lounge")
	if !st.Bans[hexKey(peerCarol)] {
		t.Errorf("bans after add = %v, want carol's hash", st.Bans)
	}
	sessCarol := sm.GetSession(linkCarol)
	if sessCarol.Rooms["lounge"] {
		t.Error("banned session still lists the room")
	}
	// The lingering membership map holds the issuer's entry only.
	if stats := rm.GetRoomStats(); stats.RoomsTotal != 1 || stats.Memberships != 1 {
		t.Errorf("room stats after ban = %+v, want only the lingering lounge room with the issuer", stats)
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("ban add output = %+v, want two envelopes", sent)
	}
	banErr := sent[0]
	if banErr.msgType != TError || banErr.room == nil || *banErr.room != "lounge" {
		t.Errorf("ban target envelope = %+v, want ERROR room=lounge", banErr)
	}
	if body, _ := banErr.body.(string); body != "banned from lounge" {
		t.Errorf("ban target body = %q", body)
	}
	banNote := sent[1]
	if banNote.msgType != TNotice || banNote.room == nil || *banNote.room != "lounge" {
		t.Errorf("ban issuer envelope = %+v, want NOTICE room=lounge", banNote)
	}
	if body, _ := banNote.body.(string); body != "ban added in lounge" {
		t.Errorf("ban issuer body = %q", body)
	}
	if env.stats["errors_sent"] != 1 {
		t.Errorf("errors_sent = %v, want 1", env.stats["errors_sent"])
	}

	// list now shows the ban (sorted hex).
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge list", nil); !got {
		t.Error("/ban should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("ban list output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "bans in lounge: "+hexKey(peerCarol) {
		t.Errorf("ban list body = %q", body)
	}

	// del removes the ban.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/ban lounge del "+hexKey(peerCarol), nil); !got {
		t.Error("/ban should be recognized")
	}
	st = rm.RoomStateGet("lounge")
	if st.Bans[hexKey(peerCarol)] {
		t.Error("ban survived /ban del")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("ban del output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "ban removed in lounge" {
		t.Errorf("ban del body = %q", body)
	}
}

// G9.16 /invite mirrors the Python invite command: the usage and bad-room
// notices, the room-op gate that runs before the subcommand parse, the
// list rendering with expiry seconds, the global (not room-scoped) target
// resolution on add, the keyed vs plain invite texts, the TTL bookkeeping
// with persistence, and the del path with the offline-hash fallback.
func TestHandleOperatorCommandInvite(t *testing.T) {
	t.Parallel()

	chat, env := newTestCommandHandler(t)
	link := &rns.Link{}
	peer := bytesOf(0xaa, 32)
	rm := env.rm
	sm := env.sm
	cmdRoom := "lounge"
	inviteUsage := "usage: /invite <room> add|del|list [nick|hashprefix|hash]"

	// Fewer than three parts: usage with room=nil.
	if got := chat.HandleOperatorCommand(link, peer, nil, "/invite lounge", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent := decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("short /invite output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != inviteUsage {
		t.Errorf("short /invite body = %q", body)
	}

	// Non-op: the room-op gate fires before the subcommand parse even
	// for an unknown subcommand.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge frob", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("non-op /invite output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "not authorized" {
		t.Errorf("non-op /invite body = %q", body)
	}

	// Founder setup with a plain (unkeyed, not +i) room.
	rm.AddMember("lounge", link, peer)
	peerCarol := bytesOf(0xcc, 32)
	linkCarol := &rns.Link{}
	identSession(rm, sm, linkCarol, peerCarol, "carol", "lounge")

	// Empty list rendering with the command room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge list", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("empty /invite list output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "invites in lounge: (none)" {
		t.Errorf("empty /invite list body = %q", body)
	}

	// Unknown subcommand: usage with the command room.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge frob x", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("unknown-op /invite output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != inviteUsage {
		t.Errorf("unknown-op /invite body = %q", body)
	}

	// add without a target: the per-op usage.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge add", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("short add /invite output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "usage: /invite lounge add <nick|hashprefix|hash>" {
		t.Errorf("short add /invite body = %q", body)
	}

	// add with an unknown global target: the invite-failed ERROR names
	// the not-found text and carries room=r.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge add nobody", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("unknown-target /invite output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "invite failed: target 'nobody' not found" {
		t.Errorf("unknown-target /invite body = %q", body)
	}

	// add with an online but unidentified target: the not-identified
	// ERROR.
	env.sentPackets = nil
	linkGhost := &rns.Link{}
	identSession(rm, sm, linkGhost, nil, "ghost", "lounge")
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge add ghost", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TError || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("unidentified /invite output = %+v, want one lounge-room ERROR", sent)
	}
	if body, _ := sent[0].body.(string); body != "invite failed: target not identified" {
		t.Errorf("unidentified /invite body = %q", body)
	}

	// Plain room add: the invite notice reaches carol, the issuer
	// hears `invite sent to {token} for {r}`, and no invite is stored.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge add carol", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("plain add /invite output = %+v, want two envelopes", sent)
	}
	inviteToCarol := sent[0]
	if inviteToCarol.msgType != TNotice || inviteToCarol.room == nil || *inviteToCarol.room != "lounge" {
		t.Errorf("invite notice envelope = %+v, want lounge-room NOTICE", inviteToCarol)
	}
	if body, _ := inviteToCarol.body.(string); body != "You have been invited to join lounge." {
		t.Errorf("invite notice body = %q", body)
	}
	if !sameBytes(inviteToCarol.src, env.identity) {
		t.Errorf("invite notice src = %v, want hub hash", inviteToCarol.src)
	}
	sentNote := sent[1]
	if sentNote.msgType != TNotice || sentNote.room == nil || *sentNote.room != "lounge" {
		t.Errorf("sent notice envelope = %+v, want lounge-room NOTICE", sentNote)
	}
	if body, _ := sentNote.body.(string); body != "invite sent to carol for lounge" {
		t.Errorf("sent notice body = %q", body)
	}
	st := rm.RoomStateGet("lounge")
	if len(st.Invited) != 0 {
		t.Errorf("plain room stored invites = %+v, want none", st.Invited)
	}

	// Keyed room add: the keyed invite text, the stored invite with
	// the TTL, and the expiry notice.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/mode lounge +k sekrit", nil); !got {
		t.Error("/mode +k should be recognized")
	}
	env.sentPackets = nil
	env.inviteTimeout = 60.0
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge add carol", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 2 {
		t.Fatalf("keyed add /invite output = %+v, want two envelopes", sent)
	}
	if body, _ := sent[0].body.(string); body != "You have been invited to join lounge. This invite allows joining without the key (+k)." {
		t.Errorf("keyed invite body = %q", body)
	}
	if body, _ := sent[1].body.(string); body != "invite added in lounge (expires in 60s)" {
		t.Errorf("keyed invite notice body = %q", body)
	}
	st = rm.RoomStateGet("lounge")
	if len(st.Invited) != 1 || !sameBytes(st.Invited[0].Hash, peerCarol) || st.Invited[0].Expires != 1730000060.0 {
		t.Errorf("stored invites = %+v, want carol with expiry now+60", st.Invited)
	}

	// list renders the stored invite with its remaining seconds.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge list", nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice {
		t.Fatalf("invite list output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "invites in lounge: "+hexKey(peerCarol)+" expires_in=60s" {
		t.Errorf("invite list body = %q", body)
	}

	// del by full hash removes the stored invite.
	env.sentPackets = nil
	if got := chat.HandleOperatorCommand(link, peer, &cmdRoom, "/invite lounge del "+hexKey(peerCarol), nil); !got {
		t.Error("/invite should be recognized")
	}
	sent = decodeSent(t, env)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room == nil || *sent[0].room != "lounge" {
		t.Fatalf("del /invite output = %+v, want one lounge-room NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "invite removed in lounge" {
		t.Errorf("del /invite body = %q", body)
	}
	st = rm.RoomStateGet("lounge")
	if len(st.Invited) != 0 {
		t.Errorf("invites after del = %+v, want none", st.Invited)
	}
}
