// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// fakeHub is a scriptable hubConn. It records every call so a test can assert
// ordering, not just occurrence. Every field is read back through an accessor
// that takes the mutex, because the engine drives a fake hub from its own
// goroutines.
type fakeHub struct {
	mu       sync.Mutex
	cfg      *HubConfig
	status   int
	hook     func(*rrc.RRCMessage)
	linkHook func()
	calls    []string

	nickOverride  string
	nickSet       bool
	autoReconnect bool
	autoList      bool
	autoWho       bool
	connectAsync  int
	disconnects   int
	joins         []string
	greetings     []string
	caps          map[int]bool
	rooms         map[string]bool
	serverName    string
	hubIdentity   []byte
	hubVersion    string
	motd          string
	members       map[string][]string
	roomMembers   map[string][]rrc.RoomMemberInfo
	messages      map[string][]*rrc.RRCMessage
	sendNoticeErr error
	// knownPeers maps an identity hash to the nick the hub knows it by. It is
	// what makes a direct NOTICE deliverable in the fake.
	knownPeers map[string]string
	// directErr, when set, makes SendDirectNotice fail even for a reachable
	// peer, modelling a path that vanished between the check and the send.
	directErr error
	// notices and direct record what the bot asked the hub to send.
	notices []sentNotice
	direct  []string
}

// sentNotice is one NOTICE the bot asked the hub to send.
type sentNotice struct {
	Room string
	Text string
}

// newFakeHub builds a fake hub in the disconnected state.
func newFakeHub(cfg *HubConfig) *fakeHub {
	return &fakeHub{
		cfg:         cfg,
		status:      rrc.StatusDisconnected,
		caps:        map[int]bool{},
		rooms:       map[string]bool{},
		members:     map[string][]string{},
		roomMembers: map[string][]rrc.RoomMemberInfo{},
		messages:    map[string][]*rrc.RRCMessage{},
		knownPeers:  map[string]string{},
	}
}

// setCapability records one hub capability, as WELCOME does.
func (f *fakeHub) setCapability(capability int, value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caps[capability] = value
}

// setKnownPeer makes a peer reachable, so a direct NOTICE to it can succeed.
func (f *fakeHub) setKnownPeer(hashHex, nick string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.knownPeers[hashHex] = nick
}

// setDirectErr makes the next direct NOTICEs fail.
func (f *fakeHub) setDirectErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.directErr = err
}

// noticeList returns the NOTICEs the bot sent, in order.
func (f *fakeHub) noticeList() []sentNotice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentNotice(nil), f.notices...)
}

// directList returns the bodies of the direct NOTICEs the bot sent, in order.
func (f *fakeHub) directList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.direct...)
}

// record appends one entry to the ordered call log. The caller holds the lock.
func (f *fakeHub) record(entry string) {
	f.calls = append(f.calls, entry)
}

// callLog returns a copy of the ordered call log.
func (f *fakeHub) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// sawCall reports whether any call log entry has the given prefix.
func (f *fakeHub) sawCall(prefix string) bool {
	for _, entry := range f.callLog() {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeHub) SetOnMessage(fn func(*rrc.RRCMessage)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetOnMessage")
	f.hook = fn
}

func (f *fakeHub) SetOnLinkEstablished(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetOnLinkEstablished")
	f.linkHook = fn
}

// established fires the link-established callback, as the client does when a
// link comes up. A reconnect is exactly this event happening again.
func (f *fakeHub) established() {
	f.mu.Lock()
	hook := f.linkHook
	f.mu.Unlock()
	if hook == nil {
		panic("no link-established callback is registered")
	}
	hook()
}

func (f *fakeHub) SetAutoReconnect(enabled, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetAutoReconnect")
	f.autoReconnect = enabled
}

func (f *fakeHub) SetAutoList(enabled, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetAutoList")
	f.autoList = enabled
}

func (f *fakeHub) SetAutoWho(enabled, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetAutoWho")
	f.autoWho = enabled
}

func (f *fakeHub) SetNickOverride(nick string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetNickOverride:" + nick)
	f.nickOverride = nick
	f.nickSet = true
}

func (f *fakeHub) ConnectAsync() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ConnectAsync")
	f.connectAsync++
}

func (f *fakeHub) Disconnect() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("Disconnect")
	f.disconnects++
	f.status = rrc.StatusDisconnected
}

func (f *fakeHub) GetHubStatus() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

// setStatus changes the connection status, as the client does on WELCOME.
func (f *fakeHub) setStatus(status int) {
	f.mu.Lock()
	f.status = status
	f.mu.Unlock()
}

// connectCount reports how many times the hub was asked to connect.
func (f *fakeHub) connectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectAsync
}

// disconnectCount reports how many times the hub was disconnected.
func (f *fakeHub) disconnectCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disconnects
}

// joinList reports the rooms joined, in call order.
func (f *fakeHub) joinList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.joins...)
}

// greetingList reports the notices this hub was asked to send.
func (f *fakeHub) greetingList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.greetings...)
}

// autoFlags reports the automatic-sweep settings.
func (f *fakeHub) autoFlags() (reconnect, list, who bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.autoReconnect, f.autoList, f.autoWho
}

// nick reports the nick override and whether SetNickOverride was called at all.
func (f *fakeHub) nick() (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nickOverride, f.nickSet
}

// inboundHook returns the registered hook.
func (f *fakeHub) inboundHook() func(*rrc.RRCMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hook
}

func (f *fakeHub) GetServerName() string { return f.serverName }
func (f *fakeHub) GetHubVersion() string { return f.hubVersion }
func (f *fakeHub) HubAddressHex() string { return f.cfg.Destination }

// HubIdentityHash reports the identity hash a test gave the fake hub, standing in
// for the one a real hub sends in its WELCOME.
func (f *fakeHub) HubIdentityHash() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hubIdentity
}

// setHubIdentity gives the fake hub an identity hash.
func (f *fakeHub) setHubIdentity(hash []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hubIdentity = hash
}

func (f *fakeHub) GetEffectiveNick() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.nickSet && f.nickOverride != "" {
		return f.nickOverride
	}
	return "gorrcbot"
}

func (f *fakeHub) GetMOTD() string { return f.motd }

func (f *fakeHub) HasCapability(capability int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.caps[capability]
}

func (f *fakeHub) JoinRoom(room string, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("JoinRoom:" + room)
	f.joins = append(f.joins, room)
	f.rooms[room] = true
}

func (f *fakeHub) JoinRoomWithKey(room string, _ bool, key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("JoinRoomWithKey:" + room + ":" + key)
	f.joins = append(f.joins, room)
	f.rooms[room] = true
}

func (f *fakeHub) HasRoom(room string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rooms[room]
}

func (f *fakeHub) JoinedRoomList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.rooms))
	for room := range f.rooms {
		out = append(out, room)
	}
	return out
}

func (f *fakeHub) GetMembers(room string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.members[room]...)
}

func (f *fakeHub) GetRoomMembers(room string) []rrc.RoomMemberInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]rrc.RoomMemberInfo(nil), f.roomMembers[room]...)
	// Every reachable peer is a member of every joined room.
	for hashHex, nick := range f.knownPeers {
		out = append(out, rrc.RoomMemberInfo{HashHex: hashHex, Nick: nick})
	}
	return out
}

func (f *fakeHub) GetMessages(room string) []*rrc.RRCMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*rrc.RRCMessage(nil), f.messages[room]...)
}

func (f *fakeHub) DisplayNameFor(peer []byte) string {
	hexed := hex.EncodeToString(peer)
	f.mu.Lock()
	defer f.mu.Unlock()
	if nick := f.knownPeers[hexed]; nick != "" {
		return nick
	}
	for _, infos := range f.roomMembers {
		for _, info := range infos {
			if info.HashHex == hexed && info.Nick != "" {
				return info.Nick
			}
		}
	}
	return ""
}

func (f *fakeHub) SendNotice(room, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendNoticeErr != nil {
		return "", f.sendNoticeErr
	}
	f.record("SendNotice:" + room)
	f.greetings = append(f.greetings, room+"|"+text)
	f.notices = append(f.notices, sentNotice{Room: room, Text: text})
	return "notice-id", nil
}

func (f *fakeHub) SendDirectNotice(peerHash []byte, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.directErr != nil {
		return f.directErr
	}
	if !f.caps[rrc.CapDirectNotice] {
		return rrc.ErrDirectNoticesUnsupported
	}
	if _, ok := f.knownPeers[hex.EncodeToString(peerHash)]; !ok {
		return rrc.ErrDestinationNotConnected
	}
	f.record("SendDirectNotice:" + hex.EncodeToString(peerHash))
	f.direct = append(f.direct, text)
	return nil
}

// ResolvePeerToken resolves a nick, a hex prefix, or a full hash from the peers
// this fake knows, mirroring the client's resolution order.
func (f *fakeHub) ResolvePeerToken(token string) (rrc.PeerTarget, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return rrc.PeerTarget{}, rrc.ErrPeerTokenEmpty
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	lower := strings.ToLower(token)
	if isHexString(lower) {
		if len(lower) < rrc.MinPeerHashPrefix {
			return rrc.PeerTarget{}, rrc.ErrPeerTokenTooShort
		}
		var matches []rrc.PeerTarget
		for hashHex, nick := range f.knownPeers {
			if strings.HasPrefix(hashHex, lower) {
				target := rrc.PeerTarget{HashHex: hashHex, Nick: nick}
				if len(hashHex) == rrc.IdentityHashLen*2 {
					target.Hash = mustHex(hashHex)
				}
				matches = append(matches, target)
			}
		}
		switch len(matches) {
		case 0:
			return rrc.PeerTarget{}, rrc.ErrPeerNotFound
		case 1:
			return matches[0], nil
		default:
			return rrc.PeerTarget{}, rrc.ErrPeerAmbiguous
		}
	}
	for hashHex, nick := range f.knownPeers {
		if strings.EqualFold(nick, lower) {
			target := rrc.PeerTarget{HashHex: hashHex, Nick: nick}
			if len(hashHex) == rrc.IdentityHashLen*2 {
				target.Hash = mustHex(hashHex)
			}
			return target, nil
		}
	}
	return rrc.PeerTarget{}, rrc.ErrPeerNotFound
}

// deliver pushes one inbound message through the registered hook, exactly as the
// hub's link goroutine does.
func (f *fakeHub) deliver(msg *rrc.RRCMessage) {
	hook := f.inboundHook()
	if hook == nil {
		return
	}
	f.mu.Lock()
	f.messages[msg.Room] = append(f.messages[msg.Room], msg)
	f.mu.Unlock()
	hook(msg)
}

// confirmedJoined delivers the hub's JOINED confirmation for a room.
func (f *fakeHub) confirmedJoined(room string) {
	f.mu.Lock()
	f.rooms[room] = true
	f.mu.Unlock()
	f.deliver(&rrc.RRCMessage{Kind: "system", Room: room, Text: "You joined #" + room})
}

// fakeDialer hands out fake hubs and records teardown.
type fakeDialer struct {
	mu      sync.Mutex
	created []*fakeHub
	byName  map[string]*fakeHub
	dialErr error
	dials   int
	closed  int
}

// newFakeDialer builds a dialer that creates one fake hub per configuration.
func newFakeDialer() *fakeDialer {
	return &fakeDialer{byName: map[string]*fakeHub{}}
}

func (d *fakeDialer) Dial(cfg *HubConfig) (hubConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dialErr != nil {
		return nil, d.dialErr
	}
	d.dials++
	hub := newFakeHub(cfg)
	d.created = append(d.created, hub)
	d.byName[cfg.Name] = hub
	return hub, nil
}

func (d *fakeDialer) Close() {
	// Mirror RRCManager.Shutdown: every hub is disconnected.
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed++
	for _, hub := range d.created {
		hub.Disconnect()
	}
}

// hub returns the fake hub created for a configured hub name, waiting for the
// engine to dial it. Run dials from its own goroutine, so a test cannot assume
// the dial has already happened.
func (d *fakeDialer) hub(t *testing.T, name string) *fakeHub {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		hub, ok := d.byName[name]
		d.mu.Unlock()
		if ok {
			return hub
		}
		time.Sleep(time.Millisecond)
	}
	// A dial error is the usual reason a configured hub never appears; report
	// it rather than a bare timeout.
	d.mu.Lock()
	err := d.dialErr
	d.mu.Unlock()
	if err != nil {
		t.Fatalf("no fake hub was dialed for %q: dial error %v", name, err)
	}
	t.Fatalf("no fake hub was dialed for %q", name)
	return nil
}

// dialCount reports how many hubs were dialed.
func (d *fakeDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// closeCount reports how many times the dialer was closed.
func (d *fakeDialer) closeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// recordingHooks captures every greeting request and inbound message.
type recordingHooks struct {
	mu        sync.Mutex
	greetings []string
	inbound   []*rrc.RRCMessage
	greetText string
	onInbound func(*hubSession, *rrc.RRCMessage)
}

// hooks adapts the recorder to the engine's policy seam.
func (h *recordingHooks) hooks() botHooks {
	return botHooks{
		Greeting: func(_ *hubSession, room string) string {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.greetings = append(h.greetings, room)
			return h.greetText
		},
		Inbound: func(s *hubSession, msg *rrc.RRCMessage) {
			h.mu.Lock()
			h.inbound = append(h.inbound, msg)
			onInbound := h.onInbound
			h.mu.Unlock()
			if onInbound != nil {
				onInbound(s, msg)
			}
		},
	}
}

// greetingCount reports how many times the greeting hook was consulted.
func (h *recordingHooks) greetingCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.greetings)
}

// inboundCount reports how many messages the command layer has handled.
func (h *recordingHooks) inboundCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.inbound)
}

// inboundTexts reports the texts handed to the command layer, in order.
func (h *recordingHooks) inboundTexts() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.inbound))
	for _, msg := range h.inbound {
		out = append(out, msg.Text)
	}
	return out
}

const (
	fakeHubOne = "a012129c10205c0b9441fcd2b755b2a7"
	fakeHubTwo = "28c7c1a68c735693aa8e6b8193ed44b2"
)

// peerHashFor returns a deterministic 16-byte identity hash.
func peerHashFor(seed byte) []byte {
	hash := make([]byte, rrc.IdentityHashLen)
	for i := range hash {
		hash[i] = seed + byte(i)
	}
	return hash
}

// addressedMessage builds a room message from a fixed requester.
func addressedMessage(room, text string) *rrc.RRCMessage {
	return addressedMessageFrom(room, text, peerHashFor(0x21))
}

// messageSeq makes every addressedMessage carry a distinct message id, so the
// duplicate guard only fires for a deliberately repeated envelope.
var messageSeq atomic.Int64

// addressedMessageFrom builds a room message from the given requester.
func addressedMessageFrom(room, text string, src []byte) *rrc.RRCMessage {
	seq := messageSeq.Add(1)
	return &rrc.RRCMessage{
		Kind: "msg",
		Room: room,
		Src:  src,
		Nick: "Alice",
		Text: text,
		Ts:   time.Now().UnixMilli(),
		ID:   fmt.Sprintf("%016x", seq),
	}
}

// directMessageFrom builds a direct NOTICE (K_DST) from the given requester. A
// direct notice carries no room and needs no address: the envelope is the
// address, so its whole body is the command line.
func directMessageFrom(text string, src []byte) *rrc.RRCMessage {
	seq := messageSeq.Add(1)
	return &rrc.RRCMessage{
		Kind:   "notice",
		Src:    src,
		Nick:   "Alice",
		Text:   text,
		Ts:     time.Now().UnixMilli(),
		ID:     fmt.Sprintf("%016x", seq),
		Direct: true,
	}
}

// engineFixture wires a bot around a fake dialer and millisecond timings.
type engineFixture struct {
	dialer *fakeDialer
	hooks  *recordingHooks
	bot    *bot
	cancel func()

	// finished is closed once Run has returned; err holds its result.
	finished chan struct{}
	errMu    sync.Mutex
	err      error
}

// stop cancels the bot and waits for Run to return, then reports its error.
// It is safe to call more than once: the fixture's cleanup does the same thing.
func (f *engineFixture) stop(t *testing.T) error {
	t.Helper()
	f.cancel()
	select {
	case <-f.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("bot.Run did not return within 5s of cancellation")
	}
	f.errMu.Lock()
	defer f.errMu.Unlock()
	return f.err
}

// defaultTestConfig is the one-hub configuration most engine tests use.
func defaultTestConfig() *BotConfig {
	return &BotConfig{
		Nick:           "gorrcbot",
		Reply:          ReplyAuto,
		CooldownSecs:   DefaultCooldownSecs,
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{{
			Name:        "One",
			Destination: fakeHubOne,
			Rooms:       []RoomConfig{{Name: "general"}},
		}},
	}
}

// newEngineFixture starts a bot in the background with simulated timings.
func newEngineFixture(t *testing.T, cfg *BotConfig) *engineFixture {
	t.Helper()
	return newEngineFixtureWith(t, cfg, nil)
}

// newEngineFixtureWith is newEngineFixture with the bot's timing knobs adjusted
// before Run starts. The knobs are read by the supervisor goroutines, so they
// have to be set here rather than on a running bot.
func newEngineFixtureWith(t *testing.T, cfg *BotConfig, tune func(*bot)) *engineFixture {
	t.Helper()
	if cfg == nil {
		cfg = defaultTestConfig()
	}
	ownHash, err := hex.DecodeString(fakeHubTwo)
	if err != nil {
		t.Fatalf("decode own hash: %v", err)
	}
	dialer := newFakeDialer()
	recorder := &recordingHooks{greetText: "hello, I am gorrrcbot; ask me for help"}
	b := newBot(cfg, BotPaths{Home: tempDir(t)}, nil, ownHash, dialer, recorder.hooks())
	b.statusPoll = time.Millisecond
	b.shutdownGrace = time.Second
	if tune != nil {
		tune(b)
	}

	ctx, cancel := context.WithCancel(context.Background())
	f := &engineFixture{dialer: dialer, hooks: recorder, bot: b, cancel: cancel, finished: make(chan struct{})}
	go func() {
		err := b.Run(ctx)
		f.errMu.Lock()
		f.err = err
		f.errMu.Unlock()
		close(f.finished)
	}()
	t.Cleanup(func() { _ = f.stop(t) })
	return f
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %v", what)
}

// TestEngineDialsEveryConfiguredHub asserts every configured hub is dialed,
// configured for unattended operation, and asked to connect exactly once.
func TestEngineDialsEveryConfiguredHub(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		Reply:          ReplyAuto,
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{
			{Name: "One", Destination: fakeHubOne, Rooms: []RoomConfig{{Name: "general"}}},
			{Name: "Two", Destination: fakeHubTwo, Nick: "otherbot",
				Rooms: []RoomConfig{{Name: "general"}, {Name: "ops", Key: "s3cret"}}},
		},
	}
	f := newEngineFixture(t, cfg)

	one := f.dialer.hub(t, "One")
	two := f.dialer.hub(t, "Two")
	waitFor(t, "both hubs to be asked to connect", func() bool {
		return one.connectCount() == 1 && two.connectCount() == 1
	})
	if got := f.dialer.dialCount(); got != 2 {
		t.Errorf("dialed %v hubs, want 2", got)
	}

	for name, hub := range map[string]*fakeHub{"One": one, "Two": two} {
		reconnect, list, who := hub.autoFlags()
		if !reconnect {
			t.Errorf("hub %v: auto-reconnect is off, want on for an always-on bot", name)
		}
		if list || who {
			t.Errorf("hub %v: auto list/who = %v/%v, want both off", name, list, who)
		}
	}
	if _, set := one.nick(); set {
		t.Error("hub One has no nick override configured, want SetNickOverride not called")
	}
	if nick, set := two.nick(); !set || nick != "otherbot" {
		t.Errorf("hub Two nick override = %q (set=%v), want %q", nick, set, "otherbot")
	}
}

// TestEngineRegistersInboundHookBeforeConnecting asserts no message can be
// missed: every hub has its hook registered before it reaches the network.
func TestEngineRegistersInboundHookBeforeConnecting(t *testing.T) {
	f := newEngineFixture(t, nil)
	hub := f.dialer.hub(t, "One")
	waitFor(t, "the hub to be asked to connect", func() bool { return hub.connectCount() == 1 })

	log := hub.callLog()
	hookAt, connectAt := -1, -1
	for i, entry := range log {
		if entry == "SetOnMessage" && hookAt < 0 {
			hookAt = i
		}
		if entry == "ConnectAsync" && connectAt < 0 {
			connectAt = i
		}
	}
	if hookAt < 0 || connectAt < 0 {
		t.Fatalf("call log %v is missing the hook or the connect call", log)
	}
	if hookAt > connectAt {
		t.Errorf("call log %v registers the inbound hook after connecting", log)
	}
}

// TestEngineJoinsOnlyAfterTheWelcome asserts the link-established callback does
// not itself join. The callback fires before the hub has welcomed the session,
// and the hub answers a JOIN in that window with "send HELLO first" and drops
// it: the client then keeps the room in no confirmed state, the bot never
// greets, and a bot that joined there came up connected and permanently silent.
// The join belongs to the first CONNECTED sync.
func TestEngineJoinsOnlyAfterTheWelcome(t *testing.T) {
	f := newEngineFixture(t, nil)
	hub := f.dialer.hub(t, "One")

	// The link is up and the client is in its HELLO-to-WELCOME window.
	hub.setStatus(rrc.StatusConnecting)
	hub.established()
	time.Sleep(20 * time.Millisecond)
	if got := hub.joinList(); len(got) != 0 {
		t.Fatalf("joins = %v in the CONNECTING window, want none", got)
	}

	hub.setStatus(rrc.StatusConnected)
	waitFor(t, "the join after the WELCOME", func() bool { return len(hub.joinList()) == 1 })
}

// TestEngineRepeatsAJoinTheHubNeverConfirms asserts a room the hub never
// confirms is asked for again, a bounded number of times. The client records a
// JOINED it treats as a silent self-join without handing it to the inbound
// hook, so a lost confirmation must not leave the bot out of the room for the
// rest of the session — and a hub that keeps refusing the room must not be
// asked forever.
func TestEngineRepeatsAJoinTheHubNeverConfirms(t *testing.T) {
	f := newEngineFixtureWith(t, nil, func(b *bot) { b.joinRetry = 10 * time.Millisecond })
	hub := f.dialer.hub(t, "One")
	hub.setStatus(rrc.StatusConnected)

	waitFor(t, "the first join", func() bool { return len(hub.joinList()) >= 1 })
	waitFor(t, "the join to be repeated", func() bool { return len(hub.joinList()) >= 2 })

	// The repeats are capped, so a room the hub never confirms stops being
	// asked for.
	time.Sleep(60 * time.Millisecond)
	if got := len(hub.joinList()); got != maxJoinAttempts {
		t.Errorf("joins = %v with no confirmation, want exactly %v", got, maxJoinAttempts)
	}

	// A confirmation arriving late stops the repeats and greets the room.
	hub.confirmedJoined("general")
	waitFor(t, "the greeting", func() bool { return len(hub.greetingList()) == 1 })
}

// TestEngineJoinsConfiguredRoomsOnlyWhenConnected asserts the bot waits for the
// hub to reach Connected before joining, and then joins every room in
// configuration order with its key.
func TestEngineJoinsConfiguredRoomsOnlyWhenConnected(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{{
			Name:        "One",
			Destination: fakeHubOne,
			Rooms:       []RoomConfig{{Name: "general"}, {Name: "ops", Key: "s3cret"}},
		}},
	}
	f := newEngineFixture(t, cfg)
	hub := f.dialer.hub(t, "One")

	// Several poll cycles in the Connecting state must not produce a JOIN.
	hub.setStatus(rrc.StatusConnecting)
	time.Sleep(20 * time.Millisecond)
	if got := hub.joinList(); len(got) != 0 {
		t.Fatalf("joins = %v while still connecting, want none", got)
	}

	hub.setStatus(rrc.StatusConnected)
	waitFor(t, "both rooms to be joined", func() bool { return len(hub.joinList()) == 2 })

	if got := hub.joinList(); got[0] != "general" || got[1] != "ops" {
		t.Errorf("joins = %v, want [general ops] in configuration order", got)
	}
	if !hub.sawCall("JoinRoomWithKey:ops:s3cret") {
		t.Errorf("call log %v is missing the keyed join for ops", hub.callLog())
	}
}

// TestEngineRejoinsAfterReconnect asserts a dropped connection is re-joined:
// the hub forgets the session, so the JOIN has to be sent again.
func TestEngineRejoinsAfterReconnect(t *testing.T) {
	f := newEngineFixture(t, nil)
	hub := f.dialer.hub(t, "One")

	hub.setStatus(rrc.StatusConnected)
	waitFor(t, "the first join", func() bool { return len(hub.joinList()) >= 1 })
	// Let the session settle so the status poll cannot add another join and
	// make the count ambiguous.
	time.Sleep(20 * time.Millisecond)
	firstJoins := len(hub.joinList())

	// A reconnect is a new link coming up: the hub forgot the session, so the
	// JOIN has to be sent again.
	hub.established()
	waitFor(t, "the rejoin", func() bool { return len(hub.joinList()) > firstJoins })
}

// TestEngineGreetsOncePerRoomPerSession asserts exactly one self-introduction
// NOTICE per room per session, sent only after the hub confirms the JOIN.
func TestEngineGreetsOncePerRoomPerSession(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{{
			Name:        "One",
			Destination: fakeHubOne,
			Rooms:       []RoomConfig{{Name: "general"}, {Name: "ops"}},
		}},
	}
	f := newEngineFixture(t, cfg)
	hub := f.dialer.hub(t, "One")
	hub.setStatus(rrc.StatusConnected)
	waitFor(t, "both rooms to be joined", func() bool { return len(hub.joinList()) == 2 })

	// Joining alone must not greet: the hub rejects room traffic before JOINED.
	if got := f.hooks.greetingCount(); got != 0 {
		t.Fatalf("greetings = %v before any JOINED confirmation, want 0", got)
	}

	hub.confirmedJoined("general")
	waitFor(t, "the general greeting", func() bool { return len(hub.greetingList()) == 1 })
	hub.confirmedJoined("ops")
	waitFor(t, "the ops greeting", func() bool { return len(hub.greetingList()) == 2 })

	// Further poll cycles and a repeated JOINED must not greet again.
	hub.confirmedJoined("general")
	time.Sleep(20 * time.Millisecond)
	greetings := hub.greetingList()
	if len(greetings) != 2 {
		t.Fatalf("greetings = %v, want exactly 2 for this session", greetings)
	}
	for _, greeting := range greetings {
		if !strings.Contains(greeting, "hello, I am gorrrcbot") {
			t.Errorf("greeting = %q, want the hook's text", greeting)
		}
	}
}

// TestEngineGreetsAgainAfterReconnect asserts a new session greets again, as the
// RRC bot contract requires one introduction per room per session.
func TestEngineGreetsAgainAfterReconnect(t *testing.T) {
	f := newEngineFixture(t, nil)
	hub := f.dialer.hub(t, "One")
	hub.setStatus(rrc.StatusConnected)
	// The hub only confirms a JOIN it received, so the join comes first.
	waitFor(t, "the room to be joined", func() bool { return len(hub.joinList()) >= 1 })
	hub.confirmedJoined("general")
	waitFor(t, "the first greeting", func() bool { return len(hub.greetingList()) >= 1 })
	time.Sleep(20 * time.Millisecond)
	firstGreetings := len(hub.greetingList())

	// A reconnect is a new link coming up, and a new session greets again.
	hub.established()
	hub.confirmedJoined("general")
	waitFor(t, "the greeting after reconnecting", func() bool {
		return len(hub.greetingList()) > firstGreetings
	})
}

// TestEngineSkipsGreetingWhenDisabled asserts announce_on_join = false keeps the
// bot completely silent on join.
func TestEngineSkipsGreetingWhenDisabled(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		AnnounceOnJoin: false,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{{
			Name:        "One",
			Destination: fakeHubOne,
			Rooms:       []RoomConfig{{Name: "general"}},
		}},
	}
	f := newEngineFixture(t, cfg)
	hub := f.dialer.hub(t, "One")
	hub.setStatus(rrc.StatusConnected)
	hub.confirmedJoined("general")

	time.Sleep(20 * time.Millisecond)
	if got := hub.greetingList(); len(got) != 0 {
		t.Errorf("greetings = %v with announce_on_join off, want 0", got)
	}
	if got := f.hooks.greetingCount(); got != 0 {
		t.Errorf("the greeting hook was consulted %v times, want 0", got)
	}
}

// TestEngineDispatchesInboundToHandler asserts every inbound message reaches the
// command layer exactly once, tagged with the hub it arrived on.
func TestEngineDispatchesInboundToHandler(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{
			{Name: "One", Destination: fakeHubOne, Rooms: []RoomConfig{{Name: "general"}}},
			{Name: "Two", Destination: fakeHubTwo, Rooms: []RoomConfig{{Name: "general"}}},
		},
	}
	f := newEngineFixture(t, cfg)
	one := f.dialer.hub(t, "One")
	two := f.dialer.hub(t, "Two")
	waitFor(t, "both hubs to connect", func() bool {
		return one.connectCount() == 1 && two.connectCount() == 1
	})

	one.deliver(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "hello from one"})
	two.deliver(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "hello from two"})

	waitFor(t, "both messages to be dispatched", func() bool { return f.hooks.inboundCount() == 2 })
	texts := f.hooks.inboundTexts()
	if texts[0] != "hello from one" || texts[1] != "hello from two" {
		t.Errorf("dispatch order = %v, want arrival order", texts)
	}
}

// TestEngineShutdownDisconnectsEveryHubAndReturns asserts a cancelled context
// tears the connections down and Run returns promptly.
func TestEngineShutdownDisconnectsEveryHubAndReturns(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{
			{Name: "One", Destination: fakeHubOne, Rooms: []RoomConfig{{Name: "general"}}},
			{Name: "Two", Destination: fakeHubTwo, Rooms: []RoomConfig{{Name: "general"}}},
		},
	}
	f := newEngineFixture(t, cfg)
	one := f.dialer.hub(t, "One")
	two := f.dialer.hub(t, "Two")
	waitFor(t, "both hubs to connect", func() bool {
		return one.connectCount() == 1 && two.connectCount() == 1
	})

	if err := f.stop(t); err != nil {
		t.Fatalf("Run returned %v, want nil on a cancelled context", err)
	}

	for name, hub := range map[string]*fakeHub{"One": one, "Two": two} {
		if hub.disconnectCount() == 0 {
			t.Errorf("hub %v was never disconnected", name)
		}
	}
	if got := f.dialer.closeCount(); got != 1 {
		t.Errorf("dialer Close called %v times, want 1", got)
	}
}

// TestEngineShutdownFlushesQueuedInbound asserts a message that arrived before
// the signal still gets its reply: the dispatcher finishes what it holds and
// drains what is already queued.
func TestEngineShutdownFlushesQueuedInbound(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var handled []string
	f := newEngineFixture(t, nil)
	f.hooks.mu.Lock()
	f.hooks.onInbound = func(_ *hubSession, msg *rrc.RRCMessage) {
		if msg.Text == "block" {
			close(started)
			<-release
		}
		f.hooks.mu.Lock()
		handled = append(handled, msg.Text)
		f.hooks.mu.Unlock()
	}
	f.hooks.mu.Unlock()

	hub := f.dialer.hub(t, "One")
	waitFor(t, "the hub to connect", func() bool { return hub.connectCount() == 1 })
	hub.deliver(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "block"})
	<-started
	for i := range 3 {
		hub.deliver(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "queued" + string(rune('a'+i))})
	}

	close(release)
	if err := f.stop(t); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	f.hooks.mu.Lock()
	defer f.hooks.mu.Unlock()
	if len(handled) != 4 {
		t.Errorf("handled %v messages (%v), want all 4 delivered before shutdown", len(handled), handled)
	}
}

// TestEngineInboundAfterShutdownDoesNotBlock asserts a late message from a hub's
// link goroutine is dropped instead of blocking or panicking.
func TestEngineInboundAfterShutdownDoesNotBlock(t *testing.T) {
	f := newEngineFixture(t, nil)
	hub := f.dialer.hub(t, "One")
	waitFor(t, "the hub to connect", func() bool { return hub.connectCount() == 1 })

	// The hub's link goroutine may already hold a copy of the hook when the
	// bot stops, so the captured closure is the one that has to stay safe.
	hook := hub.inboundHook()
	if hook == nil {
		t.Fatal("no inbound hook was registered")
	}
	if err := f.stop(t); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if hub.inboundHook() != nil {
		t.Error("shutdown left the inbound hook attached, want it detached")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		hook(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "too late"})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("delivering a message after shutdown blocked the link goroutine")
	}
}

// TestEngineRunReturnsDialError asserts a hub that cannot be created is
// reported instead of being silently skipped.
func TestEngineRunReturnsDialError(t *testing.T) {
	cfg := &BotConfig{
		Nick:           "gorrcbot",
		AnnounceOnJoin: true,
		MaxReplyLines:  DefaultMaxReplyLines,
		Hubs: []HubConfig{
			{Name: "One", Destination: fakeHubOne, Rooms: []RoomConfig{{Name: "general"}}},
			{Name: "Two", Destination: fakeHubTwo, Rooms: []RoomConfig{{Name: "general"}}},
		},
	}
	dialer := newFakeDialer()
	dialer.dialErr = errors.New("no transport")
	b := newBot(cfg, BotPaths{}, nil, nil, dialer, botHooks{})
	b.statusPoll = time.Millisecond
	b.shutdownGrace = time.Second

	err := b.Run(context.Background())
	if err == nil {
		t.Fatal("Run = nil error when a hub cannot be created")
	}
	if !strings.Contains(err.Error(), "One") {
		t.Errorf("error = %q, want it to name the hub that failed", err)
	}
	if got := dialer.closeCount(); got != 1 {
		t.Errorf("dialer Close called %v times, want 1 even when start-up fails", got)
	}
}

// TestEngineRunOnCancelledContextIsANoOp asserts a bot started with a dead
// context dials nothing.
func TestEngineRunOnCancelledContextIsANoOp(t *testing.T) {
	dialer := newFakeDialer()
	b := newBot(defaultTestConfig(), BotPaths{}, nil, nil, dialer, botHooks{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := b.Run(ctx); err != nil {
		t.Fatalf("Run on a cancelled context = %v, want nil", err)
	}
	if got := dialer.dialCount(); got != 0 {
		t.Errorf("dialed %v hubs on a cancelled context, want 0", got)
	}
}
