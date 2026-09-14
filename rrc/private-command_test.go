// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// privateCommandFixture registers a sender and two participants and returns
// the handler with everything the assertions need. The room names are left to
// the caller: a private command passes a nil room, exactly as the hub's
// private command channel does.
func privateCommandFixture(t *testing.T) (*CommandHandler, *commandTestEnv, *rns.Link, *rns.Link) {
	t.Helper()
	chat, env := newTestCommandHandler(t)

	senderLink := &rns.Link{}
	targetLink := &rns.Link{}
	identSession(env.rm, env.sm, senderLink, bytesOf(0x11, IdentityHashLen), "alice", "general")
	identSession(env.rm, env.sm, targetLink, bytesOf(0x22, IdentityHashLen), "minipc", "general")
	return chat, env, senderLink, targetLink
}

// queuesTo returns the decoded envelopes queued to one link.
func queuesTo(t *testing.T, outgoing *OutgoingList, link *rns.Link) []testEnvelope {
	t.Helper()
	var mine []OutgoingItem
	for _, item := range outgoing.Queue {
		if item.Link == link {
			mine = append(mine, item)
		}
	}
	saved := outgoing.Queue
	outgoing.Queue = mine
	defer func() { outgoing.Queue = saved }()
	return decodeOutgoing(t, outgoing)
}

// TestPrivateCommandDeliversToAResolvedNick asserts the whole private path:
// the body reaches the named participant's link as a direct NOTICE carrying
// the sender's identity and nick, the confirmation names the recipient's full
// hash, and nothing at all is queued to any other link.
func TestPrivateCommandDeliversToAResolvedNick(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, targetLink := privateCommandFixture(t)
	outgoing := &OutgoingList{}

	if !chat.HandleOperatorCommand(senderLink, env.sm.GetSession(senderLink).Peer, nil,
		"/dnotice minipc hello there", outgoing) {
		t.Fatal("HandleOperatorCommand reported the command unrecognized")
	}

	// The target receives exactly one NOTICE with the sender's identity.
	delivered := queuesTo(t, outgoing, targetLink)
	if len(delivered) != 1 {
		t.Fatalf("target received %v envelopes, want 1", len(delivered))
	}
	got := delivered[0]
	if got.msgType != TNotice {
		t.Errorf("delivered msgType = %v, want %v", got.msgType, int64(TNotice))
	}
	if got.room != nil {
		t.Errorf("delivered room = %q, want no room on a private notice", *got.room)
	}
	targetHash := env.sm.GetSession(targetLink).Peer
	if !bytes.Equal(got.dst, targetHash) {
		t.Errorf("delivered dst = %v, want the target's full hash %v", hexKey(got.dst), hexKey(targetHash))
	}
	if bytes.Equal(got.src, targetHash) {
		t.Error("delivered src is still the target's own hash; the hub must credit the sender")
	}

	// The sender receives the confirmation, and no other link sees anything.
	confirmed := queuesTo(t, outgoing, senderLink)
	if len(confirmed) != 1 {
		t.Fatalf("sender received %v envelopes, want 1 confirmation", len(confirmed))
	}
	confirmation, _ := bodyText(confirmed[0].body)
	if !strings.HasPrefix(confirmation, "Direct NOTICE sent to "+hexKey(targetHash)) {
		t.Errorf("confirmation = %q, want it to name %q", confirmation, hexKey(targetHash))
	}
	for _, item := range outgoing.Queue {
		if item.Link != senderLink && item.Link != targetLink {
			t.Errorf("a private message reached a third link")
		}
	}
}

// TestPrivateCommandDeliversTheSenderIdentityAndNick pins the rewrites the
// recipient's client relies on: the source hash is the sender's, the nick is
// the sender's, and the body is exactly the text after the target.
func TestPrivateCommandDeliversTheSenderIdentityAndNick(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, targetLink := privateCommandFixture(t)
	outgoing := &OutgoingList{}
	senderHash := env.sm.GetSession(senderLink).Peer

	if !chat.HandleOperatorCommand(senderLink, senderHash, nil, "/dnotice minipc yo dude, whazzup?", outgoing) {
		t.Fatal("command unrecognized")
	}
	delivered := queuesTo(t, outgoing, targetLink)
	if len(delivered) != 1 {
		t.Fatalf("target received %v envelopes, want 1", len(delivered))
	}
	if !bytes.Equal(delivered[0].src, senderHash) {
		t.Errorf("delivered src = %v, want the sender %v", hexKey(delivered[0].src), hexKey(senderHash))
	}
	body, _ := bodyText(delivered[0].body)
	if body != "yo dude, whazzup?" {
		t.Errorf("delivered body = %q, want %q", body, "yo dude, whazzup?")
	}
}

// TestPrivateCommandQuotedTargetWithSpaces asserts the quoting a user types is
// honoured, which is the only way to name a participant whose nick contains
// spaces.
func TestPrivateCommandQuotedTargetWithSpaces(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, _ := privateCommandFixture(t)
	targetLink := &rns.Link{}
	identSession(env.rm, env.sm, targetLink, bytesOf(0x44, IdentityHashLen), "gonomadnet on MiniPC", "general")

	for _, line := range []string{
		"/dnotice 'gonomadnet on MiniPC' yo dude",
		`/dnotice "gonomadnet on MiniPC" yo dude`,
		"/msg 'gonomadnet on MiniPC' yo dude",
		"/dn 'gonomadnet on MiniPC' yo dude",
	} {
		outgoing := &OutgoingList{}
		if !chat.HandleOperatorCommand(senderLink, env.sm.GetSession(senderLink).Peer, nil, line, outgoing) {
			t.Fatalf("%q was reported unrecognized", line)
		}
		delivered := queuesTo(t, outgoing, targetLink)
		if len(delivered) != 1 {
			t.Fatalf("%q delivered %v envelopes, want 1", line, len(delivered))
		}
		body, _ := bodyText(delivered[0].body)
		if body != "yo dude" {
			t.Errorf("%q body = %q, want %q", line, body, "yo dude")
		}
	}
}

// TestPrivateCommandAmbiguousNickDeliversNothing is the leak guard: a token
// that matches more than one participant is reported and nothing is sent, so a
// private message can never reach somebody the sender did not name.
func TestPrivateCommandAmbiguousNickDeliversNothing(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, _ := privateCommandFixture(t)
	twinLink := &rns.Link{}
	twinTwo := &rns.Link{}
	identSession(env.rm, env.sm, twinLink, bytesOf(0x33, IdentityHashLen), "twin", "general")
	identSession(env.rm, env.sm, twinTwo, bytesOf(0x34, IdentityHashLen), "twin", "general")

	outgoing := &OutgoingList{}
	if !chat.HandleOperatorCommand(senderLink, env.sm.GetSession(senderLink).Peer, nil,
		"/dnotice twin hello", outgoing) {
		t.Fatal("command unrecognized")
	}

	if len(queuesTo(t, outgoing, twinLink)) != 0 || len(queuesTo(t, outgoing, twinTwo)) != 0 {
		t.Error("an ambiguous target still received the message")
	}
	report := queuesTo(t, outgoing, senderLink)
	if len(report) != 1 {
		t.Fatalf("sender received %v envelopes, want 1 ambiguity report", len(report))
	}
	text, _ := bodyText(report[0].body)
	if !strings.Contains(text, "ambiguous") {
		t.Errorf("report = %q, want it to say the target is ambiguous", text)
	}
}

// TestPrivateCommandUnknownTargetReportsAndSendsNothing covers a nick nobody
// holds and a hash prefix nobody answers to.
func TestPrivateCommandUnknownTargetReportsAndSendsNothing(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, targetLink := privateCommandFixture(t)

	for _, line := range []string{
		"/dnotice nobody hello",
		"/dnotice deadbeef hello",
	} {
		outgoing := &OutgoingList{}
		if !chat.HandleOperatorCommand(senderLink, env.sm.GetSession(senderLink).Peer, nil, line, outgoing) {
			t.Fatalf("%q was reported unrecognized", line)
		}
		if len(queuesTo(t, outgoing, targetLink)) != 0 {
			t.Errorf("%q delivered a message to an unrelated participant", line)
		}
		report := queuesTo(t, outgoing, senderLink)
		if len(report) != 1 {
			t.Fatalf("%q produced %v replies, want 1", line, len(report))
		}
		text, _ := bodyText(report[0].body)
		if !strings.Contains(text, "not found") {
			t.Errorf("%q report = %q, want a not-found report", line, text)
		}
	}
}

// TestPrivateCommandMeDeliversToTheSender covers the self-test target.
func TestPrivateCommandMeDeliversToTheSender(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, _ := privateCommandFixture(t)
	outgoing := &OutgoingList{}
	senderHash := env.sm.GetSession(senderLink).Peer

	if !chat.HandleOperatorCommand(senderLink, senderHash, nil, "/dnoticeme hello me", outgoing) {
		t.Fatal("command unrecognized")
	}
	delivered := queuesTo(t, outgoing, senderLink)
	if len(delivered) != 2 {
		t.Fatalf("sender received %v envelopes, want the notice plus its confirmation", len(delivered))
	}
	body, _ := bodyText(delivered[0].body)
	if body != "hello me" {
		t.Errorf("delivered body = %q, want %q", body, "hello me")
	}
	confirmation, _ := bodyText(delivered[1].body)
	if !strings.Contains(confirmation, "Direct NOTICE sent to self") {
		t.Errorf("confirmation = %q, want the official self wording", confirmation)
	}
}

// TestPrivateCommandUsageAndCapabilityErrors pins the two short command forms.
func TestPrivateCommandUsageAndCapabilityErrors(t *testing.T) {
	t.Parallel()

	chat, env, senderLink, _ := privateCommandFixture(t)
	senderHash := env.sm.GetSession(senderLink).Peer

	outgoing := &OutgoingList{}
	if !chat.HandleOperatorCommand(senderLink, senderHash, nil, "/dnotice alice", outgoing) {
		t.Fatal("command unrecognized")
	}
	if len(outgoing.Queue) != 1 {
		t.Fatalf("usage error queued %v envelopes, want 1", len(outgoing.Queue))
	}
	if text, _ := bodyText(decodeOutgoing(t, outgoing)[0].body); text != dnoticeUsage {
		t.Errorf("usage reply = %q, want %q", text, dnoticeUsage)
	}

	outgoing = &OutgoingList{}
	if !chat.HandleOperatorCommand(senderLink, senderHash, nil, "/dnoticecap", outgoing) {
		t.Fatal("command unrecognized")
	}
	if text, _ := bodyText(decodeOutgoing(t, outgoing)[0].body); text != "Direct NOTICE supported: True" {
		t.Errorf("capability reply = %q, want the official wording", text)
	}
}

// TestWelcomeAdvertisesPrivateCommandsOnlyWhenEnabled pins the capability flag
// itself: a hub that has the extension advertises key 3, and a hub that does
// not keeps Python's exact three caps.
func TestWelcomeAdvertisesPrivateCommandsOnlyWhenEnabled(t *testing.T) {
	t.Parallel()

	capsFor := func(enable *bool) map[int64]any {
		t.Helper()
		hooks := MessageHooks{
			IdentityHash:           func() []byte { return bytesOf(0x21, 32) },
			StatsInc:               func(string, int) {},
			SendPacket:             func(*rns.Link, []byte) error { return nil },
			EnableResourceTransfer: func() bool { return false },
			HubName:                func() string { return "TestHub" },
			WelcomeLimits:          func() []any { return []any{32, 64, 350, 32, 240} },
			FmtHash:                func(hash []byte) string { return hex.EncodeToString(hash) },
			FmtLinkID:              func(*rns.Link) string { return "-" },
		}
		if enable != nil {
			hooks.EnablePrivateCommands = func() bool { return *enable }
		}
		mh := NewMessageHelper(hooks)
		outgoing := &OutgoingList{}
		mh.QueueWelcome(outgoing, &rns.Link{}, bytesOf(0xaa, 32))
		if len(outgoing.Queue) != 1 {
			t.Fatalf("queued %v payloads, want 1", len(outgoing.Queue))
		}
		decoded, err := cbor.Decode(outgoing.Queue[0].Payload)
		if err != nil {
			t.Fatalf("WELCOME does not decode: %v", err)
		}
		env, ok := decoded.(*cbor.Map)
		if !ok {
			t.Fatal("WELCOME is not a CBOR map")
		}
		body, _ := env.Get(KBody)
		bodyMap, ok := body.(*cbor.Map)
		if !ok {
			t.Fatal("WELCOME body is not a CBOR map")
		}
		raw, _ := bodyMap.Get(BWelcomeCaps)
		caps, ok := raw.(*cbor.Map)
		if !ok {
			t.Fatal("WELCOME caps is not a CBOR map")
		}
		out := map[int64]any{}
		for _, pair := range caps.Pairs() {
			key, _ := intValue(pair.Key)
			out[key] = pair.Val
		}
		return out
	}

	enabled, disabled := true, false
	got := capsFor(&enabled)
	if got[CAPPrivateCommand] != true {
		t.Errorf("enabled hub caps = %v, want CAPPrivateCommand present", got)
	}
	for _, want := range []int64{CAPAction, CAPDirectNotice} {
		if got[want] != true {
			t.Errorf("enabled hub caps = %v, want key %v present", got, want)
		}
	}

	got = capsFor(&disabled)
	if _, ok := got[CAPPrivateCommand]; ok {
		t.Errorf("disabled hub caps = %v, want no CAPPrivateCommand", got)
	}
	got = capsFor(nil)
	if len(got) != 2 {
		t.Errorf("unwired hub caps = %v, want Python's two non-resource caps", got)
	}
}

// privateRouterRecorder captures what the router did with one notice.
type privateRouterRecorder struct {
	commands []string
	roomSeen []*string
	errors   []string
	relayed  []OutgoingItem
}

// newPrivateRouterFixture builds a router over the command test hub, with one
// identified peer, so a notice can be addressed either to that peer or to the
// hub itself.
func newPrivateRouterFixture(t *testing.T, enabled bool) (*Router, *privateRouterRecorder, *commandTestEnv, *rns.Link, []byte) {
	t.Helper()
	env := newCommandTestEnv(t)
	rec := &privateRouterRecorder{}
	peerHash := bytesOf(0x22, IdentityHashLen)
	peerLink := &rns.Link{}
	identSession(env.rm, env.sm, peerLink, peerHash, "minipc", "general")

	hooks := RouterHooks{
		Sessions:     func() *SessionManager { return env.sm },
		RoomManager:  func() *RoomManager { return env.rm },
		StatsInc:     func(string, int) {},
		IdentityHash: func() []byte { return env.identity },
		DebugEnabled: func() bool { return false },
		MaxNickBytes: func() int { return 32 },
		HandleOperatorCommand: func(_ *rns.Link, _ []byte, room *string, text string, _ *OutgoingList) bool {
			rec.commands = append(rec.commands, text)
			rec.roomSeen = append(rec.roomSeen, room)
			return true
		},
		EmitError: func(_ *OutgoingList, _ *rns.Link, _ []byte, text string, _ *string) {
			rec.errors = append(rec.errors, text)
		},
		QueuePayload: func(outgoing *OutgoingList, link *rns.Link, payload []byte) {
			outgoing.Queue = append(outgoing.Queue, OutgoingItem{Link: link, Payload: payload})
		},
	}
	if enabled {
		hooks.EnablePrivateCommands = func() bool { return true }
	}
	return NewRouter(hooks), rec, env, peerLink, peerHash
}

// TestPrivateCommandNoticeReachesTheCommandHandler asserts the hub side of the
// extension: a notice addressed to the hub's own identity is read as a command
// and answered on the sender's link, never relayed, and an extension-free hub
// keeps the standard "destination not connected" reply.
func TestPrivateCommandNoticeReachesTheCommandHandler(t *testing.T) {
	t.Parallel()

	senderLink := &rns.Link{}

	for _, tc := range []struct {
		name     string
		enabled  bool
		toHub    bool
		text     string
		wantCmd  bool
		wantErrs int
	}{
		{name: "command to the hub", enabled: true, toHub: true, text: "/dnotice minipc hi", wantCmd: true},
		{name: "command with a leading space", enabled: true, toHub: true, text: "  /dnoticeme hi", wantCmd: true},
		{name: "plain text to the hub is not a command", enabled: true, toHub: true, text: "hello", wantErrs: 1},
		{name: "extension off keeps the standard refusal", enabled: false, toHub: true, text: "/dnotice minipc hi", wantErrs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, rec, env, peerLink, peerHash := newPrivateRouterFixture(t, tc.enabled)
			dst := peerHash
			if tc.toHub {
				dst = env.identity
			}
			notice := MakeEnvelope(int(TNotice), peerHash, WithDst(dst), WithBody(tc.text))
			outgoing := &OutgoingList{}
			router.handleDirectNotice(senderLink, env.sm.GetSession(peerLink), peerHash, notice, outgoing)

			if got := len(rec.commands) > 0; got != tc.wantCmd {
				t.Errorf("command handled = %v, want %v (commands %v)", got, tc.wantCmd, rec.commands)
			}
			for _, room := range rec.roomSeen {
				if room != nil {
					t.Errorf("a private command carried room %q; it must carry none", *room)
				}
			}
			if len(rec.errors) != tc.wantErrs {
				t.Errorf("errors = %v, want %v", rec.errors, tc.wantErrs)
			}
			if len(outgoing.Queue) != 0 {
				t.Errorf("queued %v payloads, want 0: a private command is answered by the handler", len(outgoing.Queue))
			}
		})
	}
}

// TestPeerNoticeStillRelaysToThePeer guards the standard path next to the new
// branch: a notice addressed to another participant is relayed to that peer's
// link with the sender's source hash and no room.
func TestPeerNoticeStillRelaysToThePeer(t *testing.T) {
	t.Parallel()

	router, rec, testEnv, peerLink, peerHash := newPrivateRouterFixture(t, true)
	senderHash := bytesOf(0x11, IdentityHashLen)
	senderLink := &rns.Link{}
	identSession(testEnv.rm, testEnv.sm, senderLink, senderHash, "alice", "general")

	notice := MakeEnvelope(int(TNotice), senderHash, WithDst(peerHash), WithBody("hi"))
	outgoing := &OutgoingList{}
	router.handleDirectNotice(senderLink, testEnv.sm.GetSession(senderLink), senderHash, notice, outgoing)

	if len(rec.commands) != 0 {
		t.Errorf("a peer notice was read as a command: %v", rec.commands)
	}
	if len(rec.errors) != 0 {
		t.Errorf("peer notice produced errors: %v", rec.errors)
	}
	if len(outgoing.Queue) != 1 {
		t.Fatalf("queued %v payloads, want the relayed notice", len(outgoing.Queue))
	}
	if outgoing.Queue[0].Link != peerLink {
		t.Error("the relayed notice did not go to the target's link")
	}
	decoded, err := cbor.Decode(outgoing.Queue[0].Payload)
	if err != nil {
		t.Fatalf("relayed notice does not decode: %v", err)
	}
	m, ok := decoded.(*cbor.Map)
	if !ok {
		t.Fatal("relayed notice is not a CBOR map")
	}
	got := envelopeToTest(m)
	if !bytes.Equal(got.src, senderHash) {
		t.Errorf("relayed src = %v, want the sender %v", hexKey(got.src), hexKey(senderHash))
	}
	if got.room != nil {
		t.Errorf("relayed notice carries room %q, want none", *got.room)
	}
}
