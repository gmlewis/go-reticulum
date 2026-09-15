// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// The fixture's fixed actors. msgAsker is the peer that types the commands, and
// msgPeer is the peer the messages are addressed to; both have to be distinct
// from fakeHubOne/fakeHubTwo, which the fixture uses for the hub destination and
// the bot's own identity.
var (
	// msgAskerHash is the identity hash of the peer asking for a delivery.
	msgAskerHash = peerHashFor(0x11)
	// msgPeerHash is the identity hash of the peer a message is addressed to.
	msgPeerHash = peerHashFor(0x41)
)

const (
	// msgAsker is the nick the fake hub knows the asker by.
	msgAsker = testAskerNick
	// msgPeer is the nick the fake hub knows the target by.
	msgPeer = "Bob"
	// msgNodeHex is the propagation node hash the fixture's sender is
	// configured with. It is a real 16-byte destination hash in shape.
	msgNodeHex = "1234c1a68c735693aa8e6b8193ed44b2"
)

// fakeLXMF is a scriptable lxmfSender. It records every message the command
// layer queued, chooses the method the live sender would have chosen from the
// transport state a test declares, and can be made to fail. Nothing here touches
// Reticulum or the network.
type fakeLXMF struct {
	mu sync.Mutex
	// sends is every queued message, in order, and methods the method each was
	// queued with. The method comes from the same decision function the live
	// sender uses, so the confirmation line is exercised for real.
	sends   []lxmfSend
	methods []lxmfMethod
	// err, when set, is returned instead of queueing.
	err error
	// hasPath, hasRatchet and hasPropagationNode are the transport conditions
	// the method choice is made from.
	hasPath            bool
	hasRatchet         bool
	hasPropagationNode bool
	// nodeHex is the propagation node the outcomes name.
	nodeHex string
	// closed counts Close calls and closeErr is returned from each.
	closed   int
	closeErr error
	// closeBlocks, when set, makes Close wait until it is closed, standing in for
	// a router whose queued deliveries are still waiting on a link.
	closeBlocks chan struct{}
}

// newFakeLXMF builds an idle sender whose default state is "no path, no node",
// which is the fallback the bot must still attempt.
func newFakeLXMF() *fakeLXMF {
	return &fakeLXMF{nodeHex: msgNodeHex}
}

// Send implements lxmfSender.
func (f *fakeLXMF) Send(req lxmfSend) (lxmfMethod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return lxmfNone, f.err
	}
	f.sends = append(f.sends, req)
	method := lxmfMethodFor(f.hasPath, f.hasRatchet, f.hasPropagationNode)
	f.methods = append(f.methods, method)
	return method, nil
}

// Close implements lxmfSender.
func (f *fakeLXMF) Close() error {
	f.mu.Lock()
	blocks := f.closeBlocks
	f.closed++
	err := f.closeErr
	f.mu.Unlock()
	if blocks != nil {
		<-blocks
	}
	return err
}

// setTransport declares what the transport knows about the peer, which is the
// whole input to the delivery-method choice.
func (f *fakeLXMF) setTransport(hasPath, hasRatchet, hasPropagationNode bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hasPath, f.hasRatchet, f.hasPropagationNode = hasPath, hasRatchet, hasPropagationNode
}

// setErr makes every later send fail with err.
func (f *fakeLXMF) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// texts returns the payloads that reached the sender, in order.
func (f *fakeLXMF) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sends))
	for _, req := range f.sends {
		out = append(out, req.Text)
	}
	return out
}

// sendCount reports how many messages were queued.
func (f *fakeLXMF) sendCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sends)
}

// last returns the most recently queued message.
func (f *fakeLXMF) last() lxmfSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		return lxmfSend{}
	}
	return f.sends[len(f.sends)-1]
}

// closeCount reports how many times the sender was closed.
func (f *fakeLXMF) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// deliver hands one outcome to the last asker, exactly as the live sender does
// when the router reports what became of the message.
func (f *fakeLXMF) deliver(o lxmfOutcome) {
	f.mu.Lock()
	var onResult func(lxmfOutcome)
	if n := len(f.sends); n > 0 {
		onResult = f.sends[n-1].OnResult
	}
	f.mu.Unlock()
	if onResult != nil {
		onResult(o)
	}
}

// msgFixture is a bot with an LXMF sender, a stopped budget clock, and one
// reachable asker and one reachable target in #general.
type msgFixture struct {
	reg     *registry
	session *hubSession
	fake    *fakeHub
	lxmf    *fakeLXMF
	clock   time.Time
}

// newMsgFixture wires LXMF the way startup does: the sender is installed on the
// registry and on the bot only when the configuration enables it, which is what
// makes "no router when it is off" observable.
func newMsgFixture(t *testing.T, enabled bool) *msgFixture {
	t.Helper()
	cfg := defaultTestConfig()
	cfg.LXMFEnabled = enabled
	reg, session, fake := commandFixture(t, cfg)
	f := &msgFixture{reg: reg, session: session, fake: fake, lxmf: newFakeLXMF(), clock: catchupBase}
	if enabled {
		reg.lxmf = f.lxmf
		session.bot.lxmf = f.lxmf
	}
	// newReplySession builds a session without going through addHub, so the bot
	// must be told about it: an outcome is delivered by looking the session up
	// by hub.
	session.bot.sessions = append(session.bot.sessions, session)
	f.fake.setCapability(rrc.CapDirectNotice, true)
	f.fake.setKnownPeer(hexString(msgAskerHash), msgAsker)
	f.fake.setKnownPeer(hexString(msgPeerHash), msgPeer)
	return f
}

// line runs one command line as the fixture's asker, at the stopped clock.
func (f *msgFixture) line(t *testing.T, line string) []string {
	t.Helper()
	return f.run(t, msgAskerHash, msgAsker, line)
}

// run runs one command line as some peer.
func (f *msgFixture) run(t *testing.T, asker []byte, nick, line string) []string {
	t.Helper()
	f.fake.setKnownPeer(hexString(asker), nick)
	return f.reg.Run(&commandRequest{
		Session: f.session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, asker),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     f.clock,
	})
}

// advance moves the stopped clock forward.
func (f *msgFixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

// notices returns the direct notices the fake hub was asked to send.
func (f *msgFixture) notices() []string { return f.fake.directList() }

// queuedLine is the confirmation the fixture expects for a message to msgPeer.
func queuedLine(method lxmfMethod) string {
	return "queued for LXMF delivery to " + msgPeer + " (" + shortHash(hexString(msgPeerHash)) +
		"…) via " + method.label() + " delivery"
}

// TestMsgIsNotConfiguredAndBuildsNoRouter asserts the whole LXMF path is absent
// until an operator opts in: the command answers with the line that says how to
// turn it on, no sender is installed, and nothing is ever queued.
func TestMsgIsNotConfiguredAndBuildsNoRouter(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, false)
	for _, line := range []string{"msg", "msg Bob hi", "msg " + hexString(msgPeerHash) + " hi", "lxmf Bob hi"} {
		assertLines(t, f.line(t, line), []string{lxmfNotConfiguredLine})
	}
	if f.reg.lxmf != nil {
		t.Error("a bot with lxmf_enabled = false carries an LXMF sender")
	}
	if f.session.bot.lxmf != nil {
		t.Error("a bot with lxmf_enabled = false carries an LXMF sender on the engine")
	}
	if got := f.lxmf.sendCount(); got != 0 {
		t.Errorf("messages queued with LXMF off = %v, want 0", got)
	}
}

// TestOpenLXMFDeliveryBuildsNoRouterWhenDisabled is the wiring rule itself: with
// lxmf_enabled = false the opener is never called, so no LXMF router, no job
// loop and no state under the storage directory can come into existence.
func TestOpenLXMFDeliveryBuildsNoRouterWhenDisabled(t *testing.T) {
	t.Parallel()

	logger := rns.NewLogger()
	ts := rns.NewTransportSystem(logger)
	identity := &rns.Identity{Hash: mustHex(fakeHubTwo)}

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		calls := 0
		sender, err := openLXMFDelivery(cfg, ts, identity, func(rns.Transport, *rns.Identity, *BotConfig) (lxmfSender, error) {
			calls++
			return newFakeLXMF(), nil
		})
		if err != nil {
			t.Fatalf("openLXMFDelivery: %v", err)
		}
		if sender != nil {
			t.Error("a disabled LXMF configuration produced a sender")
		}
		if calls != 0 {
			t.Errorf("the opener was called %v time(s) with LXMF off, want 0 routers built", calls)
		}
	})

	t.Run("enabled and wired", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.LXMFEnabled = true
		cfg.StorageDir = tempDir(t)
		want := newFakeLXMF()
		var gotCfg *BotConfig
		sender, err := openLXMFDelivery(cfg, ts, identity, func(ts rns.Transport, id *rns.Identity, got *BotConfig) (lxmfSender, error) {
			if ts == nil || id == nil {
				t.Error("the opener was handed a nil transport or identity")
			}
			gotCfg = got
			return want, nil
		})
		if err != nil {
			t.Fatalf("openLXMFDelivery: %v", err)
		}
		if sender != want {
			t.Error("openLXMFDelivery did not return the sender the opener built")
		}
		if gotCfg != cfg {
			t.Error("the opener was handed a different configuration")
		}
	})

	t.Run("enabled without a transport", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.LXMFEnabled = true
		sender, err := openLXMFDelivery(cfg, nil, identity, nil)
		if !errors.Is(err, errLXMFNoTransport) {
			t.Errorf("openLXMFDelivery without a transport = %v, want %v", err, errLXMFNoTransport)
		}
		if sender != nil {
			t.Error("a sender was built without a transport")
		}
	})

	t.Run("enabled without a storage directory", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.LXMFEnabled = true
		cfg.StorageDir = ""
		sender, err := openLXMFDelivery(cfg, ts, identity, nil)
		if !errors.Is(err, errLXMFNoStorage) {
			t.Errorf("openLXMFDelivery without storage = %v, want %v", err, errLXMFNoStorage)
		}
		if sender != nil {
			t.Error("a sender was built without a storage directory")
		}
	})
}

// TestMsgQueuesAndReportsTheMethod asserts the confirmation line is exactly the
// documented one and that the method it names is the method the transport's own
// state implies: a path and a ratchet is opportunistic, a path alone is direct,
// no path with a propagation node is propagated, and no path without one falls
// back to direct, which fails visibly rather than silently.
func TestMsgQueuesAndReportsTheMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		hasPath    bool
		hasRatchet bool
		hasNode    bool
		want       lxmfMethod
	}{
		{name: "a path and a ratchet is opportunistic", hasPath: true, hasRatchet: true, want: lxmfOpportunistic},
		{name: "a path alone is direct", hasPath: true, want: lxmfDirect},
		{name: "no path with a propagation node is propagated", hasNode: true, want: lxmfPropagated},
		{name: "no path and no node is direct", want: lxmfDirect},
		{name: "a ratchet without a path is direct", hasRatchet: true, want: lxmfDirect},
		{name: "a path and a ratchet with a node prefers opportunistic", hasPath: true, hasRatchet: true,
			hasNode: true, want: lxmfOpportunistic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newMsgFixture(t, true)
			f.lxmf.setTransport(tt.hasPath, tt.hasRatchet, tt.hasNode)

			assertLines(t, f.line(t, "msg Bob hello there"), []string{queuedLine(tt.want)})

			if got := f.lxmf.sendCount(); got != 1 {
				t.Fatalf("messages queued = %v, want 1", got)
			}
			sent := f.lxmf.last()
			if got := hexString(sent.PeerHash); got != hexString(msgPeerHash) {
				t.Errorf("the peer the message was queued for = %v, want %v", got, hexString(msgPeerHash))
			}
			if sent.Text != "hello there" {
				t.Errorf("the payload = %q, want %q", sent.Text, "hello there")
			}
			if sent.Ask.PeerName != msgPeer {
				t.Errorf("the outcome would be reported as %q, want %q", sent.Ask.PeerName, msgPeer)
			}
			if got := hexString(sent.Ask.AskerHash); got != hexString(msgAskerHash) {
				t.Errorf("the asker recorded = %v, want %v", got, hexString(msgAskerHash))
			}
			if sent.Ask.HubHex != fakeHubOne {
				t.Errorf("the hub recorded = %v, want %v", sent.Ask.HubHex, fakeHubOne)
			}
			if sent.OnResult == nil {
				t.Error("no outcome callback was handed to the sender")
			}
		})
	}
}

// TestLXMFMethodChoice pins the decision function on its own, including the
// case a live transport reaches when a path has been dropped but a ratchet is
// still remembered.
func TestLXMFMethodChoice(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name                         string
		hasPath, hasRatchet, hasNode bool
		want                         lxmfMethod
	}{
		{name: "path and ratchet", hasPath: true, hasRatchet: true, want: lxmfOpportunistic},
		{name: "path only", hasPath: true, want: lxmfDirect},
		{name: "ratchet only", hasRatchet: true, want: lxmfDirect},
		{name: "node only", hasNode: true, want: lxmfPropagated},
		{name: "nothing known", want: lxmfDirect},
	} {
		if got := lxmfMethodFor(tt.hasPath, tt.hasRatchet, tt.hasNode); got != tt.want {
			t.Errorf("%v: lxmfMethodFor(%v, %v, %v) = %v, want %v",
				tt.name, tt.hasPath, tt.hasRatchet, tt.hasNode, got.label(), tt.want.label())
		}
	}
	// The values are LXMF's own, so the bot and the router cannot disagree
	// about what a method number means.
	for _, tt := range []struct {
		method lxmfMethod
		want   int
	}{
		{lxmfOpportunistic, lxmf.MethodOpportunistic},
		{lxmfDirect, lxmf.MethodDirect},
		{lxmfPropagated, lxmf.MethodPropagated},
	} {
		if int(tt.method) != tt.want {
			t.Errorf("method %v = %v, want LXMF's %v", tt.method.label(), int(tt.method), tt.want)
		}
	}
}

// TestMsgAnswersHonestlyAboutPeers asserts the three different ways a message
// can fail to be queued are reported differently: an unknown token, a peer whose
// key can never be recalled because they have never announced, and a router that
// refuses the message. None of them may be reported as queued.
func TestMsgAnswersHonestlyAboutPeers(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)

	assertLines(t, f.line(t, "msg nobody hi"), []string{"no such peer"})

	// A peer the hub knows but whose identity the transport cannot recall: LXMF
	// encrypts to a public key learned from an announce, so there is nothing to
	// encrypt to.
	f.lxmf.setErr(errLXMFPeerNeverAnnounced)
	assertLines(t, f.line(t, "msg Bob hi"), []string{lxmfNoAnnounceLine})

	// Any other refusal is reported without quoting the router's own error,
	// which is operator-facing.
	f.lxmf.setErr(errors.New("the router exploded"))
	assertLines(t, f.line(t, "msg Bob hi"), []string{lxmfQueueFailedLine})

	if got := f.lxmf.sendCount(); got != 0 {
		t.Errorf("messages queued while every send failed = %v, want 0", got)
	}
}

// TestMsgAnswersHonestlyForAShortHashPrefix asserts a peer the hub only knows by
// a partial hash is refused rather than addressed by guesswork, because a
// shortened hash is not enough to derive an lxmf.delivery destination.
func TestMsgAnswersHonestlyForAShortHashPrefix(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)
	f.fake.setKnownPeer("abcd12", "Shorty")

	lines := f.line(t, "msg Shorty hi")
	if len(lines) != 1 || !strings.Contains(lines[0], "full 32-character hash") {
		t.Errorf("msg to a short-hash peer = %q, want one line asking for the full hash", lines)
	}
	if got := f.lxmf.sendCount(); got != 0 {
		t.Errorf("messages queued for a short-hash peer = %v, want 0", got)
	}
}

// TestMsgNamesAPeerWithNoNickByHashPrefix asserts every line names somebody: a
// peer the hub knows no nick for is reported by the hash prefix alone, both in
// the confirmation and in the outcome that follows later.
func TestMsgNamesAPeerWithNoNickByHashPrefix(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)
	f.fake.setKnownPeer(hexString(msgPeerHash), "")
	prefix := shortHash(hexString(msgPeerHash))

	assertLines(t, f.line(t, "msg "+hexString(msgPeerHash)+" hello"),
		[]string{"queued for LXMF delivery to " + prefix + "… via direct delivery"})

	f.lxmf.deliver(lxmfOutcome{State: lxmf.StateDelivered, Method: int(lxmfDirect), Attempts: 1})
	assertLines(t, f.notices(), []string{"lxmf: delivered to " + prefix})
}

// TestMsgRejectsUnusableText asserts the inbox rule: text is required, a text
// with nothing readable left after sanitizing is refused instead of being sent
// as an empty message, and text over the cap is refused rather than silently
// shortened. Only the acceptable message reaches the sender.
func TestMsgRejectsUnusableText(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)

	assertLines(t, f.line(t, "msg"), []string{"Usage: " + msgUsage})
	assertLines(t, f.line(t, "msg Bob"), []string{"Usage: " + msgUsage})
	assertLines(t, f.line(t, "msg Bob    "), []string{"Usage: " + msgUsage})
	assertLines(t, f.line(t, "msg Bob \x1b[31m\x1b[0m"), []string{lxmfEmptyTextLine})
	assertLines(t, f.line(t, "msg Bob "+strings.Repeat("x", maxLXMFTextBytes+1)),
		[]string{fmt.Sprintf(lxmfTextTooLongLine, maxLXMFTextBytes)})
	if got := f.lxmf.sendCount(); got != 0 {
		t.Errorf("messages queued from unusable text = %v, want 0", got)
	}

	// A text exactly at the cap is accepted, and its payload is unchanged.
	atCap := strings.Repeat("y", maxLXMFTextBytes)
	assertLines(t, f.line(t, "msg Bob "+atCap), []string{queuedLine(lxmfDirect)})
	if got := f.lxmf.texts(); len(got) != 1 || got[0] != atCap {
		t.Errorf("payloads = %v, want the one message exactly at the cap", got)
	}
}

// TestMsgStripsControlCharacters asserts the payload this bot writes into
// somebody else's inbox carries no terminal escapes and no control characters:
// a DM is rendered by a client this bot does not control, exactly like a NOTICE.
func TestMsgStripsControlCharacters(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)
	f.line(t, "msg Bob hel\x1b[31mlo\x07 there\x00")

	got := f.lxmf.texts()
	if len(got) != 1 {
		t.Fatalf("payloads = %v, want exactly one message", got)
	}
	if got[0] != "hello there" {
		t.Errorf("payload = %q, want %q", got[0], "hello there")
	}
	for _, r := range got[0] {
		if r < 0x20 || r == 0x7f {
			t.Errorf("payload %q still carries the control character %q", got[0], r)
		}
	}
}

// TestMsgNeverRelaysRoomHistoryIntoADM asserts the payload is exactly what the
// asker typed. A DM is addressed to somebody who is not in the room, so quoting
// anything the bot heard in a room would hand a third party's words to them.
func TestMsgNeverRelaysRoomHistoryIntoADM(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)
	f.fake.messages["general"] = []*rrc.RRCMessage{
		{Kind: "msg", Room: "general", Src: msgAskerHash, Nick: msgAsker,
			Text: "the vault code is hunter2", Ts: catchupBase.UnixMilli()},
		{Kind: "notice", Room: "general", Src: msgPeerHash, Nick: msgPeer,
			Text: "a private remark", Ts: catchupBase.UnixMilli()},
	}

	assertLines(t, f.line(t, "msg Bob just this"), []string{queuedLine(lxmfDirect)})
	got := f.lxmf.texts()
	if len(got) != 1 || got[0] != "just this" {
		t.Fatalf("payload = %v, want exactly the text the asker typed", got)
	}
	for _, secret := range []string{"hunter2", "private remark", msgPeer} {
		if strings.Contains(got[0], secret) {
			t.Errorf("payload = %q, which quotes room history (%q)", got[0], secret)
		}
	}
}

// TestMsgIsRateLimitedPerAsker asserts the per-asker budget: this command writes
// into somebody else's inbox, so one client cannot use it to flood a peer. The
// window slides, so the asker can try again a minute later, and one asker's
// budget never blocks another's.
func TestMsgIsRateLimitedPerAsker(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)
	for i := range maxLXMFAskerPerMinute {
		assertLines(t, f.line(t, "msg Bob message "+strconv.Itoa(i)), []string{queuedLine(lxmfDirect)})
	}
	assertLines(t, f.line(t, "msg Bob one too many"), []string{errLXMFAskerBudget.Error()})

	// Another asker has a budget of their own.
	assertLines(t, f.run(t, peerHashFor(0x22), "Carol", "msg Bob hello from Carol"),
		[]string{queuedLine(lxmfDirect)})

	// The window slides: a minute later the same asker is admitted again.
	f.advance(lxmfBudgetWindow)
	assertLines(t, f.line(t, "msg Bob after the window"), []string{queuedLine(lxmfDirect)})

	want := maxLXMFAskerPerMinute + 2
	if got := f.lxmf.sendCount(); got != want {
		t.Errorf("messages queued = %v, want %v", got, want)
	}
}

// TestMsgIsRateLimitedForTheWholeBot asserts the second budget: however many
// askers there are, the bot as a whole stops writing into inboxes at a fixed
// rate, which is the bound that matters when a room is busy.
func TestMsgIsRateLimitedForTheWholeBot(t *testing.T) {
	t.Parallel()

	f := newMsgFixture(t, true)
	sent := 0
	for i := 0; sent < maxLXMFBotPerMinute; i++ {
		asker := peerHashFor(byte(0x50 + i))
		for range maxLXMFAskerPerMinute {
			lines := f.run(t, asker, "asker"+strconv.Itoa(i), "msg Bob hello")
			if len(lines) != 1 {
				t.Fatalf("reply = %q, want one line", lines)
			}
			if lines[0] != queuedLine(lxmfDirect) {
				t.Fatalf("message %v was answered %q, want the bot budget to allow exactly %v of them",
					sent+1, lines[0], maxLXMFBotPerMinute)
			}
			sent++
			if sent == maxLXMFBotPerMinute {
				break
			}
		}
	}
	// One more asker, one more message, and the bot's own budget refuses it.
	assertLines(t, f.run(t, peerHashFor(0xF0), "fresh", "msg Bob hello"), []string{errLXMFBotBudget.Error()})
	if got := f.lxmf.sendCount(); got != maxLXMFBotPerMinute {
		t.Errorf("messages queued = %v, want %v", got, maxLXMFBotPerMinute)
	}
}

// TestLXMFOutcomeReachesTheAskerAsADirectNotice asserts the asynchronous half:
// the outcome of a queued message goes back to the asker as a direct NOTICE,
// through the hub their ask arrived on. A propagated message is reported as
// accepted for store-and-forward, never as delivered, because StateSent proves
// only that a node took it.
func TestLXMFOutcomeReachesTheAskerAsADirectNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome lxmfOutcome
		want    string
	}{
		{
			name:    "delivered",
			outcome: lxmfOutcome{State: lxmf.StateDelivered, Method: int(lxmfDirect), Attempts: 1},
			want:    "lxmf: delivered to " + msgPeer,
		},
		{
			name: "propagated is accepted, not delivered",
			outcome: lxmfOutcome{State: lxmf.StateSent, Method: int(lxmfPropagated),
				Attempts: 1, PropagationNodeHex: msgNodeHex},
			want: "lxmf: accepted by propagation node " + shortHash(msgNodeHex) + "… (store-and-forward)",
		},
		{
			name:    "failed after five attempts",
			outcome: lxmfOutcome{State: lxmf.StateFailed, Method: int(lxmfDirect), Attempts: 5},
			want:    "lxmf: failed after 5 attempts",
		},
		{
			name:    "one failed attempt reads in the singular",
			outcome: lxmfOutcome{State: lxmf.StateFailed, Method: int(lxmfDirect), Attempts: 1},
			want:    "lxmf: failed after 1 attempt",
		},
		{
			name:    "a non-propagated sent state claims no delivery",
			outcome: lxmfOutcome{State: lxmf.StateSent, Method: int(lxmfDirect), Attempts: 1},
			want:    "lxmf: sent to " + msgPeer + "; no delivery proof yet",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newMsgFixture(t, true)
			assertLines(t, f.line(t, "msg Bob hello"), []string{queuedLine(lxmfDirect)})

			f.lxmf.deliver(tt.outcome)
			assertLines(t, f.notices(), []string{tt.want})
		})
	}
}

// TestLXMFOutcomeForAnUnreachableAskerIsLoggedNotFaked asserts the honest
// failure: when the asker cannot be reached, the outcome is logged and nothing
// is sent. Faking a notice would be worse than silence, because the bot would
// then claim a delivery it never reported.
func TestLXMFOutcomeForAnUnreachableAskerIsLoggedNotFaked(t *testing.T) {
	t.Parallel()

	t.Run("the asker is not a member any more", func(t *testing.T) {
		t.Parallel()
		f := newMsgFixture(t, true)
		f.line(t, "msg Bob hello")
		f.fake.mu.Lock()
		delete(f.fake.knownPeers, hexString(msgAskerHash))
		f.fake.mu.Unlock()

		f.lxmf.deliver(lxmfOutcome{State: lxmf.StateDelivered, Method: int(lxmfDirect), Attempts: 1})
		if got := f.notices(); len(got) != 0 {
			t.Errorf("direct notices = %q, want none for an unreachable asker", got)
		}
	})

	t.Run("the hub is no longer a session", func(t *testing.T) {
		t.Parallel()
		f := newMsgFixture(t, true)
		f.line(t, "msg Bob hello")
		f.session.bot.sessions = nil

		f.lxmf.deliver(lxmfOutcome{State: lxmf.StateFailed, Method: int(lxmfDirect), Attempts: 5})
		if got := f.notices(); len(got) != 0 {
			t.Errorf("direct notices = %q, want none without a session for the hub", got)
		}
	})
}

// TestLXMFClosesOnShutdown asserts the router is released when the bot stops,
// exactly once, and that a Close failure is reported in the log rather than
// propagated out of Run: the bot has already stopped by then.
func TestLXMFClosesOnShutdown(t *testing.T) {
	t.Parallel()

	t.Run("closed once", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.LXMFEnabled = true
		sender := newFakeLXMF()
		f := newEngineFixtureWith(t, cfg, func(b *bot) { b.lxmf = sender })
		// Run returns without shutting down when its context is already
		// cancelled, so the bot has to be running before it is stopped.
		waitFor(t, "the hub to connect", func() bool { return f.dialer.hub(t, "One").connectCount() == 1 })

		if err := f.stop(t); err != nil {
			t.Fatalf("Run: %v", err)
		}
		// A second stop must not close the router again.
		if err := f.stop(t); err != nil {
			t.Fatalf("second stop: %v", err)
		}
		if got := sender.closeCount(); got != 1 {
			t.Errorf("the LXMF router was closed %v times, want 1", got)
		}
	})

	t.Run("a close failure is not fatal", func(t *testing.T) {
		t.Parallel()
		cfg := defaultTestConfig()
		cfg.LXMFEnabled = true
		sender := newFakeLXMF()
		sender.closeErr = errors.New("the router refused to close")
		f := newEngineFixtureWith(t, cfg, func(b *bot) { b.lxmf = sender })
		waitFor(t, "the hub to connect", func() bool { return f.dialer.hub(t, "One").connectCount() == 1 })

		if err := f.stop(t); err != nil {
			t.Errorf("Run = %v, want nil: a failure to close LXMF must not fail the run", err)
		}
		if got := sender.closeCount(); got != 1 {
			t.Errorf("the LXMF router was closed %v times, want 1", got)
		}
	})
}

// TestMsgCommandsAreRegistered asserts the command and its alias both exist, are
// documented, and share one handler, so the generated help lists them.
func TestMsgCommandsAreRegistered(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, name := range []string{"msg", "lxmf"} {
		cmd, ok := reg.byName[name]
		if !ok {
			t.Fatalf("the command registry has no %q command", name)
		}
		if cmd.usage != msgUsage {
			t.Errorf("%v usage = %q, want %q", name, cmd.usage, msgUsage)
		}
		if strings.TrimSpace(cmd.summary) == "" {
			t.Errorf("%v has no summary", name)
		}
	}
	if got := reg.aliases["lxmf"]; got != "msg" {
		t.Errorf("aliases[lxmf] = %q, want %q", got, "msg")
	}
	lines := runLines(t, reg, session, "help msg")
	if len(lines) == 0 || !strings.Contains(lines[0], msgUsage) {
		t.Errorf("help msg = %q, want the command's usage line", lines)
	}
	msgCmd, _ := reg.byName["msg"]
	if want := helpLineCount(msgCmd, reg.config()); len(lines) != want {
		t.Errorf("help msg returned %v lines, want the summary line plus %v detail lines",
			len(lines), len(msgCmd.detail))
	}
	// The listing is alphabetical, so msg and its lxmf alias are not adjacent
	// once the field-assistant commands are registered; what matters is that
	// both are listed, in order.
	got := runLines(t, reg, session, "help")[0]
	lxmfAt := strings.Index(got, "lxmf")
	msgAt := strings.Index(got, "msg")
	if lxmfAt < 0 || msgAt < 0 || lxmfAt > msgAt {
		t.Errorf("help = %q, want both lxmf and msg listed with lxmf first", got)
	}
}

// TestLXMFEnabledWithNoSenderSaysSo asserts the seam's absence is handled
// honestly: a bot that was told to do LXMF but has no router answers with that,
// instead of the misleading "not configured" line.
func TestLXMFEnabledWithNoSenderSaysSo(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.LXMFEnabled = true
	reg, session, _ := commandFixture(t, cfg)
	lines := runLines(t, reg, session, "msg Bob hello")
	assertLines(t, lines, []string{lxmfUnavailableLine})
}

// TestDefaultConfigTemplateCarriesTheLXMFKeys asserts the generated config.toml
// documents the three new keys with their defaults, since that file is the only
// documentation an operator sees before anything connects.
func TestDefaultConfigTemplateCarriesTheLXMFKeys(t *testing.T) {
	t.Parallel()

	content := defaultConfigContent(BotPaths{
		Home:         "/tmp/gorrcbot-lxmf",
		ConfigPath:   "/tmp/gorrcbot-lxmf/config.toml",
		IdentityPath: "/tmp/gorrcbot-lxmf/bot_identity",
		StorageDir:   "/tmp/gorrcbot-lxmf/storage",
	})
	for _, want := range []string{
		"lxmf_enabled = false",
		"lxmf_propagation_node = \"\"",
		"lxmf_announce_minutes = " + strconv.Itoa(DefaultLXMFAnnounceMinutes),
	} {
		if !strings.Contains(content, want) {
			t.Errorf("the generated config does not contain %q", want)
		}
	}
	cfg, warnings, err := DecodeBotConfig("config.toml", content)
	if err != nil {
		t.Fatalf("the generated template does not parse: %v", err)
	}
	for _, warning := range warnings {
		if strings.Contains(warning, "lxmf") {
			t.Errorf("the generated template warns about an LXMF key: %v", warning)
		}
	}
	if cfg.LXMFEnabled {
		t.Error("the generated template enables LXMF, want it off by default")
	}
	if cfg.LXMFAnnounceMinutes != DefaultLXMFAnnounceMinutes {
		t.Errorf("LXMFAnnounceMinutes = %v, want %v",
			cfg.LXMFAnnounceMinutes, DefaultLXMFAnnounceMinutes)
	}
}

// TestSanitizeLXMFTextKeepsOneLine asserts the payload rule on its own: escapes
// go, control and format characters go, whitespace runs collapse so the message
// is one line however it was typed, and text with nothing readable left is
// reported rather than sent empty.
func TestSanitizeLXMFTextKeepsOneLine(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		text string
		want string
	}{
		{name: "plain", text: "hello there", want: "hello there"},
		{name: "trimmed", text: "  hello  ", want: "hello"},
		{name: "newlines collapse", text: "hello\nthere\r\nfriend", want: "hello there friend"},
		{name: "csi removed", text: "he\x1b[31mllo", want: "hello"},
		{name: "osc removed", text: "he\x1b]0;pwned\x07llo", want: "hello"},
		{name: "bell and nul dropped", text: "he\x07l\x00lo", want: "hello"},
		{name: "bidi override dropped", text: "he\u202ello", want: "hello"},
		{name: "tab collapses", text: "hello\tthere", want: "hello there"},
		{name: "utf8 survives", text: "Zürich, São Paulo, 京都", want: "Zürich, São Paulo, 京都"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := sanitizeLXMFText(tt.text)
			if err != nil {
				t.Fatalf("sanitizeLXMFText(%q): %v", tt.text, err)
			}
			if got != tt.want {
				t.Errorf("sanitizeLXMFText(%q) = %q, want %q", tt.text, got, tt.want)
			}
			if strings.ContainsAny(got, "\n\r") {
				t.Errorf("sanitizeLXMFText(%q) = %q, want a single line", tt.text, got)
			}
		})
	}

	for _, text := range []string{"", "   ", "\x1b[2J", "\x00\x07\x1b"} {
		if got, err := sanitizeLXMFText(text); !errors.Is(err, errLXMFEmptyText) {
			t.Errorf("sanitizeLXMFText(%q) = %q, %v; want %v", text, got, err, errLXMFEmptyText)
		}
	}
}

// TestLXMFCloseIsBoundedByTheShutdownGrace asserts a router that will not close —
// a queued delivery waiting on a link that never comes up — cannot hold shutdown
// open. Every other shutdown step is bounded the same way.
func TestLXMFCloseIsBoundedByTheShutdownGrace(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	sender := newFakeLXMF()
	sender.closeBlocks = make(chan struct{})
	reg.lxmf = sender
	reg.bot.lxmf = sender
	reg.bot.shutdownGrace = 50 * time.Millisecond

	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.bot.closeLXMF()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(sender.closeBlocks)
		t.Fatal("closeLXMF waited for a router that will not close")
	}
	close(sender.closeBlocks)
	_ = session
}
