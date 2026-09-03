// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"encoding/hex"
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
	identity    []byte
	serverOp    bool
	sentPackets []sentPacket
	reloadCalls int
	reloadRooms []*string
	stats       map[string]int
	rm          *RoomManager
	sm          *SessionManager
	tm          *TrustManager
	mh          *MessageHelper
	chat        *CommandHandler
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
	rm := NewRoomManager(RoomHooks{
		IsServerOp: func([]byte) bool { return false },
		Now:        func() float64 { return 1730000000.0 },
	})
	sm := NewSessionManager(SessionHooks{
		GetRoomMembers:         rm.GetRoomMembers,
		RemoveMember:           rm.RemoveMember,
		RateLimitMsgsPerMinute: func() int { return 240 },
		IsBanned:               func([]byte) bool { return false },
	})
	tm := NewTrustManager(TrustHooks{})
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
	chat := NewCommandHandler(CommandHandlerHooks{
		TrustManager:   func() *TrustManager { return tm },
		SessionManager: func() *SessionManager { return sm },
		RoomManager:    func() *RoomManager { return rm },
		MessageHelper:  func() *MessageHelper { return mh },
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
		ReloadConfigAndRooms: func(_ *rns.Link, room *string, _ *OutgoingList) {
			env.reloadCalls++
			env.reloadRooms = append(env.reloadRooms, room)
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

// identSession materializes an identified session, mirroring the session
// state the hub would hold after HELLO and identification.
func identSession(sm *SessionManager, link *rns.Link, peer []byte, nick string, rooms ...string) *Session {
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

	peerA := bytesOf(0xaa, 32)
	peerB := bytesOf(0xbb, 32)
	hexA := hexKey(peerA)
	hexB := hexKey(peerB)

	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	identSession(sm, linkA, peerA, "Alice", "general")
	identSession(sm, linkB, peerB, "bob")
	identSession(sm, linkC, nil, "carol", "lounge")

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
	identSession(sm, linkD, peerD, "multi", "general")
	linkE := &rns.Link{}
	identSession(sm, linkE, nil, "multi")
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

	peerA := bytesOf(0xaa, 32)
	hexA := hexKey(peerA)
	peerB := bytesOf(0xbb, 32)
	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	identSession(sm, linkA, peerA, "Alice", "general")
	identSession(sm, linkB, peerB, "alice")
	identSession(sm, linkC, nil, "carol")

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

	peerA := bytesOf(0xaa, 32)
	peerB := bytesOf(0xbb, 32)
	linkA := &rns.Link{}
	linkB := &rns.Link{}
	linkC := &rns.Link{}
	identSession(sm, linkA, peerA, "Alice")
	identSession(sm, linkB, peerB, "")
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
	identSession(sm, linkQ, bytesOf(0x11, 32), "bob's")
	want := "ambiguous: 'q' matches 1 identities:\n" +
		"  - " + hexKey(bytesOf(0x11, 32))[:16] + " nick=\"bob's\"\n" +
		"Use full or longer identity hash to disambiguate."
	if got := chat.FormatAmbiguousTargets("q", []*rns.Link{linkQ}); got != want {
		t.Errorf("quoted nick:\n got %q\nwant %q", got, want)
	}
}
