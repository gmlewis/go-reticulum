// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file is the end-to-end proof for gorrcbot: real RRC hub services (the rrcd
// hub role, in process), a real Reticulum stack on every node, real second
// clients in the rooms, and the real bot engine with its real reply policy and
// command registry.
//
// Nothing here is faked. The bot dials its hubs over loopback TCP, learns their
// paths from their announces, joins their rooms, posts one self-introduction per
// room, and then answers exactly the clients that addressed it.

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// integrationWait is how long the end-to-end tests wait for one step. The whole
// rig is in-process over loopback, so a step that takes this long is a failure,
// not slowness.
const integrationWait = 15 * time.Second

// integrationGreetWait is the budget for the bot's first self-introduction. A
// greeting has to survive a link handshake, the WELCOME, the JOIN, and the
// hub's JOINED fanout, so it gets a longer budget than a reply to a command on
// an already-established link.
const integrationGreetWait = 30 * time.Second

// inboundCollector records every message one asker receives, in arrival order.
// Reading the bot's answers this way sees both the room route and the direct
// route through the same lens.
type inboundCollector struct {
	mu   sync.Mutex
	msgs []*rrc.RRCMessage
}

// add appends one inbound message. Only the leaf fields are copied, because the
// client reuses nothing but the caller must not hold live state.
func (c *inboundCollector) add(msg *rrc.RRCMessage) {
	if msg == nil {
		return
	}
	copied := &rrc.RRCMessage{
		Kind:   msg.Kind,
		Room:   msg.Room,
		Src:    append([]byte(nil), msg.Src...),
		Nick:   msg.Nick,
		Text:   msg.Text,
		Ts:     msg.Ts,
		Direct: msg.Direct,
		ID:     msg.ID,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, copied)
}

// all returns every collected message, for an assertion that does not care
// which route a reply took.
func (c *inboundCollector) all() []*rrc.RRCMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*rrc.RRCMessage(nil), c.msgs...)
}

// texts returns the collected message texts, for failure diagnostics.
func (c *inboundCollector) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.msgs))
	for _, msg := range c.msgs {
		out = append(out, msg.Text)
	}
	return out
}

// fromDirect returns the direct-notice texts the given source sent.
func (c *inboundCollector) fromDirect(srcHex string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, msg := range c.msgs {
		if msg.Direct && hexString(msg.Src) == srcHex {
			out = append(out, msg.Text)
		}
	}
	return out
}

// integrationHub is one live hub plus the live client that talks to the bot on
// it.
type integrationHub struct {
	name    string
	service *rrc.HubService
	hash    string
	port    int
	asker   *rrc.RRCHub
	// inbound records everything the asker received, across both routes.
	inbound *inboundCollector
}

// integrationRig is the bot and every hub it is configured for.
type integrationRig struct {
	hubs    []*integrationHub
	botHash string
	home    string
	// botLog holds the bot's own log lines, for failure diagnostics.
	botLog *capturedLog
}

// failf reports a failed assertion together with the bot's own log, so the
// failure explains itself.
func (r *integrationRig) failf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Fatalf(format+"\n--- bot log ---\n%v", append(args, r.botLog.String())...)
}

// capturedLog collects the bot's own log lines, so a failed end-to-end
// assertion can be read together with what the bot thought it was doing.
type capturedLog struct {
	mu    sync.Mutex
	lines []string
}

func (c *capturedLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for line := range strings.SplitSeq(strings.TrimRight(string(p), "\n"), "\n") {
		// Only the bot's own lines: the stack's debug stream is noise here and
		// would bury the story a failure needs to tell.
		if strings.Contains(line, "gorrcbot: ") {
			c.lines = append(c.lines, line)
		}
	}
	return len(p), nil
}

func (c *capturedLog) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// silentRNSLogger returns a logger that emits nothing, so the integration tests
// show only their own failures and not the transport chatter of four RNS nodes.
func silentRNSLogger() *rns.Logger {
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	return logger
}

// writeRNSConfig writes a standalone RNS config with the given interface text.
func writeRNSConfig(t *testing.T, dir, interfacesText string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%v): %v", dir, err)
	}
	content := "[reticulum]\nshare_instance = No\n\n[logging]\nloglevel = 4\n\n[interfaces]\n" +
		interfacesText
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
}

// freeLoopbackPort reserves and releases a loopback port.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return port
}

// waitForCondition polls cond until it holds or the timeout expires.
func waitForCondition(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}

// newIntegrationHub brings up one real hub service listening on loopback, with
// its own identity and its own Reticulum stack.
func newIntegrationHub(t *testing.T, root, name string) *integrationHub {
	t.Helper()
	port := freeLoopbackPort(t)
	hubDir := filepath.Join(root, name+"-hub-rns")
	writeRNSConfig(t, hubDir, fmt.Sprintf(`
  [[%v Listen]]
    type = TCPServerInterface
    enabled = yes
    listen_ip = 127.0.0.1
    listen_port = %v
`, name, port))

	identityPath := filepath.Join(root, name+"-hub_identity")
	identity, err := rns.NewIdentity(true, silentRNSLogger())
	if err != nil {
		t.Fatalf("%v NewIdentity: %v", name, err)
	}
	if err := identity.ToFile(identityPath); err != nil {
		t.Fatalf("%v ToFile: %v", name, err)
	}

	cfg := rrc.DefaultHubConfig()
	cfg.Configdir = &hubDir
	cfg.IdentityPath = &identityPath
	cfg.AnnounceOnStart = true
	cfg.AnnouncePeriodS = 0.0
	cfg.HubName = name
	greeting := "Welcome to the " + name + " hub. Type /help for commands."
	cfg.Greeting = &greeting

	hub := rrc.NewHubService(cfg)
	if err := hub.Start(); err != nil {
		t.Fatalf("%v hub.Start: %v", name, err)
	}
	t.Cleanup(hub.Stop)
	collector := &inboundCollector{}

	return &integrationHub{
		name:    name,
		service: hub,
		hash:    hexString(hub.DestinationHash()),
		port:    port,
		inbound: collector,
		asker:   newIntegrationAsker(t, root, name, port, hub.DestinationHash(), collector),
	}
}

// newIntegrationAsker starts the client that talks to the bot: a real RRC client
// with its own Reticulum stack, joined to the room.
func newIntegrationAsker(t *testing.T, root, name string, hubPort int, hubHash []byte,
	collector *inboundCollector) *rrc.RRCHub {
	t.Helper()
	dir := filepath.Join(root, name+"-asker-rns")
	writeRNSConfig(t, dir, fmt.Sprintf(`
  [[%v Asker Uplink]]
    type = TCPClientInterface
    enabled = yes
    target_host = 127.0.0.1
    target_port = %v
`, name, hubPort))

	ts := rns.NewTransportSystem(silentRNSLogger())
	ret, err := rns.NewReticulum(ts, dir)
	if err != nil {
		t.Fatalf("%v asker NewReticulum: %v", name, err)
	}
	t.Cleanup(func() {
		if err := ret.Close(); err != nil {
			t.Logf("closing the %v asker's Reticulum: %v", name, err)
		}
	})

	identity, err := rns.NewIdentity(true, silentRNSLogger())
	if err != nil {
		t.Fatalf("%v asker NewIdentity: %v", name, err)
	}
	mgr := rrc.NewManager(filepath.Join(root, name+"-asker-storage"), func() []byte {
		return identity.Hash
	})
	mgr.SetIdentity(identity)
	mgr.SetNickname("Asker on " + name)
	mgr.SetTransport(ts)
	t.Cleanup(mgr.Shutdown)

	hub := mgr.AddHub(hubHash, rrc.HubDestName, name)
	if hub == nil {
		t.Fatalf("%v: could not create the asker's hub connection", name)
	}
	hub.AddRoom("general")
	// Every message the asker sees is collected, so an assertion can look at
	// both routes without caring which one the bot chose.
	hub.SetOnMessage(collector.add)
	hub.SetAutoReconnect(false, false)
	hub.SetAutoList(false, false)
	hub.SetAutoWho(false, false)
	t.Cleanup(hub.Disconnect)
	hub.ConnectAsync()

	if !waitForCondition(integrationWait, func() bool {
		return hub.GetHubStatus() == rrc.StatusConnected && hub.HasRoom("general")
	}) {
		t.Fatalf("%v: the asker never connected (status %v, room %v)",
			name, hub.GetHubStatus(), hub.HasRoom("general"))
	}
	return hub
}

// newIntegrationRig brings up hubCount live hubs, each with an asker already in
// the room, and then the bot configured for all of them. replyMode selects the
// route the bot answers on, so both routes can be driven over the real wire.
func newIntegrationRig(t *testing.T, hubCount int, replyMode string) *integrationRig {
	t.Helper()
	return newIntegrationRigWith(t, hubCount, replyMode, nil)
}

// newIntegrationRigWith is newIntegrationRig with one adjustment applied to the
// bot's configuration before it starts.
func newIntegrationRigWith(t *testing.T, hubCount int, replyMode string,
	adjust func(*BotConfig)) *integrationRig {
	t.Helper()
	root := tempDir(t)
	names := []string{"alpha", "beta"}
	if hubCount > len(names) {
		t.Fatalf("the rig supports at most %v hubs", len(names))
	}

	var hubs []*integrationHub
	hubConfigs := make([]HubConfig, 0, hubCount)
	for i := range hubCount {
		hub := newIntegrationHub(t, root, names[i])
		hubs = append(hubs, hub)
		hubConfigs = append(hubConfigs, HubConfig{
			Name:        names[i],
			Destination: hub.hash,
			DestHash:    mustHex(hub.hash),
			Rooms:       []RoomConfig{{Name: "general"}},
		})
	}

	home := filepath.Join(root, "gorrcbot-home")
	paths := BotPaths{
		Home:         home,
		ConfigPath:   filepath.Join(home, defaultConfigFileName),
		IdentityPath: filepath.Join(home, defaultIdentityFileName),
		StorageDir:   filepath.Join(home, defaultStorageDirName),
	}
	if _, err := EnsureFirstRun(paths); err != nil {
		t.Fatalf("EnsureFirstRun: %v", err)
	}
	identity, _, err := LoadBotIdentity(paths.IdentityPath)
	if err != nil {
		t.Fatalf("LoadBotIdentity: %v", err)
	}

	botDir := filepath.Join(root, "bot-rns")
	var uplinks strings.Builder
	for _, hub := range hubs {
		port := hub.port
		fmt.Fprintf(&uplinks, `
  [[%v Bot Uplink]]
    type = TCPClientInterface
    enabled = yes
    target_host = 127.0.0.1
    target_port = %v
`, hub.name, port)
	}
	writeRNSConfig(t, botDir, uplinks.String())

	ts := rns.NewTransportSystem(silentRNSLogger())
	ret, err := rns.NewReticulum(ts, botDir)
	if err != nil {
		t.Fatalf("bot NewReticulum: %v", err)
	}

	cfg := &BotConfig{
		Nick:           DefaultNick,
		Reply:          replyMode,
		MaxReplyLines:  DefaultMaxReplyLines,
		AnnounceOnJoin: true,
		Hubs:           hubConfigs,
	}
	if adjust != nil {
		adjust(cfg)
	}

	dialer := newManagerDialer(identity, cfg.Nick, paths.StorageDir, ret)
	b := newBot(cfg, paths, silentRNSLogger(), identity.Hash, dialer, botHooks{Greeting: greeting})
	reg := newRegistry(b)
	b.hooks.Inbound = newResponder(cfg, identity.Hash, reg.Run).handle

	// Route the bot's own log lines into the failure output: an always-on
	// service that cannot join a room has to be diagnosable from its log.
	botLog := &capturedLog{}
	prevOutput := log.Writer()
	log.SetOutput(botLog)
	t.Cleanup(func() { log.SetOutput(prevOutput) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("b.Run returned %v, want nil after a cancelled context", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("b.Run did not return after the context was cancelled")
		}
	})

	return &integrationRig{hubs: hubs, botHash: hexString(identity.Hash), home: home, botLog: botLog}
}

// notices returns the room NOTICEs the bot sent to one asker.
func (r *integrationRig) notices(t *testing.T, index int) []string {
	t.Helper()
	var out []string
	for _, msg := range r.hubs[index].asker.GetMessages("general") {
		if msg.Kind == "notice" && hexString(msg.Src) == r.botHash {
			out = append(out, msg.Text)
		}
	}
	return out
}

// greetings counts the bot's self-introductions in one asker's room. The hub's
// own MOTD notice is not attributed to the bot, so it is never counted.
func (r *integrationRig) greetings(t *testing.T, index int) int {
	t.Helper()
	count := 0
	for _, notice := range r.notices(t, index) {
		if strings.Contains(notice, "RRC bot") {
			count++
		}
	}
	return count
}

// TestIntegrationBotAnswersAnAddressedCommand is the end-to-end proof: over a
// real hub link the bot joins, introduces itself exactly once, stays silent for
// room chatter, and answers an addressed command with one NOTICE per line.
//
// The bot is configured to answer in the room, so every reply is visible in the
// room buffer and can be checked against what a human would see.
func TestIntegrationBotAnswersAnAddressedCommand(t *testing.T) {
	rig := newIntegrationRig(t, 1, ReplyRoom)
	asker := rig.hubs[0].asker

	// 1. The bot joins the room and introduces itself exactly once.
	if !waitForCondition(integrationGreetWait, func() bool { return rig.greetings(t, 0) >= 1 }) {
		rig.failf(t, "the bot never introduced itself; notices so far: %v", rig.notices(t, 0))
	}
	time.Sleep(500 * time.Millisecond)
	if got := rig.greetings(t, 0); got != 1 {
		t.Errorf("the bot introduced itself %v times, want exactly 1", got)
	}
	for _, notice := range rig.notices(t, 0) {
		if !strings.Contains(notice, "@"+DefaultNick+" help") {
			t.Errorf("the greeting %q does not say how to address the bot", notice)
		}
	}

	// 2. Ordinary room chatter is ignored completely.
	asker.SendMessage("general", "just talking to myself here")
	time.Sleep(500 * time.Millisecond)
	if got := len(rig.notices(t, 0)); got != 1 {
		t.Errorf("the bot replied to unaddressed chatter: %v notices, want 1", got)
	}

	// 3. An addressed command is answered with the generated listing.
	asker.SendMessage("general", "@"+DefaultNick+" help")
	if !waitForCondition(integrationWait, func() bool {
		for _, notice := range rig.notices(t, 0) {
			if strings.HasPrefix(notice, "Commands: ") {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("the bot never answered @%v help; notices so far: %v",
			DefaultNick, rig.notices(t, 0))
	}

	// 4. The listing names every registered command.
	reg := newRegistry(newBot(defaultTestConfig(), BotPaths{}, nil, mustHex(rig.botHash), nil, botHooks{}))
	var help string
	for _, notice := range rig.notices(t, 0) {
		if strings.HasPrefix(notice, "Commands: ") {
			help = notice
		}
	}
	for _, name := range reg.names() {
		if !strings.Contains(help, name) {
			t.Errorf("help = %q, want it to list %q", help, name)
		}
	}

	// 5. An unknown command gets exactly one short line.
	before := len(rig.notices(t, 0))
	asker.SendMessage("general", "@"+DefaultNick+" bogus")
	if !waitForCondition(integrationWait, func() bool {
		return len(rig.notices(t, 0)) > before
	}) {
		t.Fatalf("the bot never answered an unknown command; notices: %v", rig.notices(t, 0))
	}
	time.Sleep(500 * time.Millisecond)
	notices := rig.notices(t, 0)
	if len(notices) != before+1 {
		t.Fatalf("an unknown command produced %v notices, want exactly 1: %v",
			len(notices)-before, notices[before:])
	}
	if last := notices[len(notices)-1]; !strings.Contains(last, "unknown command") {
		t.Errorf("unknown-command reply = %q, want the one short line", last)
	}

	// 6. Addressing the bot by identity-hash prefix works too.
	asker.SendMessage("general", "@"+rig.botHash[:6]+" ping")
	if !waitForCondition(integrationWait, func() bool {
		return slices.Contains(rig.notices(t, 0), "pong")
	}) {
		t.Fatalf("the bot never answered @%v ping; notices so far: %v",
			rig.botHash[:6], rig.notices(t, 0))
	}

	// 7. A multi-line command is answered one NOTICE per line.
	asker.SendMessage("general", "@"+DefaultNick+" uptime")
	if !waitForCondition(integrationWait, func() bool {
		for _, notice := range rig.notices(t, 0) {
			if strings.HasPrefix(notice, "Uptime: runtime=") {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("the bot never answered uptime; notices so far: %v", rig.notices(t, 0))
	}
	for _, notice := range rig.notices(t, 0) {
		if strings.HasPrefix(notice, "Uptime: ") {
			if !strings.Contains(notice, "hub-connection=") {
				t.Errorf("uptime = %q, want the connection age too", notice)
			}
			if strings.Contains(notice, "\n") {
				t.Errorf("uptime = %q, want one line per envelope", notice)
			}
		}
	}
}

// TestIntegrationBotSilencesEveryOtherFormOfAddress asserts the negative half of
// the contract over the wire: a mid-sentence mention, a longer nick, a shorter
// one, and the other bot's '!' prefix must all stay unanswered.
func TestIntegrationBotSilencesEveryOtherFormOfAddress(t *testing.T) {
	rig := newIntegrationRig(t, 1, ReplyRoom)
	asker := rig.hubs[0].asker

	if !waitForCondition(integrationGreetWait, func() bool { return rig.greetings(t, 0) >= 1 }) {
		t.Fatalf("the bot never introduced itself; notices: %v", rig.notices(t, 0))
	}
	after := len(rig.notices(t, 0))

	for _, text := range []string{
		"what does " + DefaultNick + " do?",
		"@" + DefaultNick + "x help",
		"@" + DefaultNick[:4] + " help",
		"!help",
		DefaultNick + " help",
		"@" + DefaultNick + "bogus",
	} {
		asker.SendMessage("general", text)
	}
	time.Sleep(2 * time.Second)

	if got := len(rig.notices(t, 0)); got != after {
		t.Errorf("the bot answered something it was not addressed by: %v notices, want %v",
			got, after)
	}
}

// TestIntegrationBotRepliesDirectlyWhenTheHubSupportsIt asserts auto mode uses
// the K_DST direct-notice route when the hub advertises it, so a private answer
// never lands in the room.
func TestIntegrationBotRepliesDirectlyWhenTheHubSupportsIt(t *testing.T) {
	rig := newIntegrationRig(t, 1, ReplyAuto)
	asker := rig.hubs[0].asker

	if !waitForCondition(integrationGreetWait, func() bool { return rig.greetings(t, 0) >= 1 }) {
		t.Fatalf("the bot never introduced itself; notices: %v", rig.notices(t, 0))
	}
	if !waitForCondition(integrationWait, func() bool {
		return asker.HasCapability(rrc.CapDirectNotice)
	}) {
		t.Skip("this hub does not advertise the direct-notice capability")
	}

	asker.SendMessage("general", "@"+DefaultNick+" help")
	if !waitForCondition(integrationWait, func() bool {
		for _, text := range rig.hubs[0].inbound.fromDirect(rig.botHash) {
			if strings.HasPrefix(text, "Commands: ") {
				return true
			}
		}
		return false
	}) {
		t.Fatalf("the bot did not answer directly; direct replies: %v, room notices: %v",
			rig.hubs[0].inbound.fromDirect(rig.botHash), rig.notices(t, 0))
	}

	// The private answer must not have landed in the room: the only thing the
	// room may hold from the bot is its self-introduction.
	for _, notice := range rig.notices(t, 0) {
		if strings.HasPrefix(notice, "Commands: ") {
			t.Errorf("a direct answer also landed in the room: %q", notice)
		}
	}
}

// TestIntegrationBotServesEveryHubWithOneIdentity asserts the multi-hub
// contract: the bot dials every configured hub at once, joins each one's rooms,
// greets each, answers on each, and presents the SAME identity hash everywhere,
// which is what lets a peer address it by hash prefix without knowing the hub.
func TestIntegrationBotServesEveryHubWithOneIdentity(t *testing.T) {
	rig := newIntegrationRig(t, 2, ReplyRoom)
	var reported []string

	for i, hub := range rig.hubs {
		index := i
		if !waitForCondition(integrationGreetWait, func() bool { return rig.greetings(t, index) >= 1 }) {
			rig.failf(t, "the bot never introduced itself on %v; notices: %v",
				hub.name, rig.notices(t, index))
		}

		// The bot must answer on this hub too, and it must report the one
		// identity hash it owns. This is the property that lets a peer
		// address it by hash prefix without knowing which hub it is on.
		hub.asker.SendMessage("general", "@"+DefaultNick+" id")
		if !waitForCondition(integrationWait, func() bool {
			for _, notice := range rig.notices(t, index) {
				if strings.HasPrefix(notice, "identity=") {
					return true
				}
			}
			return false
		}) {
			t.Fatalf("the bot never answered on %v; notices: %v",
				hub.name, rig.notices(t, index))
		}
		reported = append(reported, identityFromID(t, rig.notices(t, index)))
	}

	// The two hubs are genuinely different destinations, or the test proves
	// nothing about multi-hub support.
	if rig.hubs[0].hash == rig.hubs[1].hash {
		t.Error("both hubs have the same destination hash")
	}

	// One identity everywhere: the hash the bot reports on each hub is the
	// hash the greeting on that hub was sent from, and both are the same.
	for i, got := range reported {
		if got != rig.botHash {
			t.Errorf("hub %v reports identity %v, want %v",
				rig.hubs[i].name, got, rig.botHash)
		}
	}
	if reported[0] != reported[1] {
		t.Errorf("the bot presents %v on %v and %v on %v, want one identity",
			reported[0], rig.hubs[0].name, reported[1], rig.hubs[1].name)
	}
}

// identityFromID extracts the identity hash from the bot's id reply.
func identityFromID(t *testing.T, notices []string) string {
	t.Helper()
	for _, notice := range notices {
		if !strings.HasPrefix(notice, "identity=") {
			continue
		}
		field, _, _ := strings.Cut(strings.TrimPrefix(notice, "identity="), ";")
		if !isHexString(field) {
			t.Fatalf("id reply %q does not start with a hash", notice)
		}
		return field
	}
	t.Fatalf("no id reply among %v", notices)
	return ""
}

// TestIntegrationCooldownSuppressesARepeatedRequest asserts the anti-spam rule
// over the wire: a client that repeats the same request inside the cooldown
// window gets one answer, not two, and a different client is still served.
func TestIntegrationCooldownSuppressesARepeatedRequest(t *testing.T) {
	rig := newIntegrationRigWith(t, 1, ReplyRoom, func(cfg *BotConfig) {
		cfg.CooldownSecs = 30
	})
	asker := rig.hubs[0].asker

	if !waitForCondition(integrationGreetWait, func() bool { return rig.greetings(t, 0) >= 1 }) {
		rig.failf(t, "the bot never introduced itself; notices so far: %v", rig.notices(t, 0))
	}
	before := len(rig.notices(t, 0))

	// Two requests from the same identity, back to back.
	asker.SendMessage("general", "@"+DefaultNick+" ping")
	if !waitForCondition(integrationWait, func() bool {
		return slices.Contains(rig.notices(t, 0), "pong")
	}) {
		rig.failf(t, "the bot never answered the first request; notices: %v", rig.notices(t, 0))
	}
	asker.SendMessage("general", "@"+DefaultNick+" ping")
	time.Sleep(2 * time.Second)

	pongs := 0
	for _, notice := range rig.notices(t, 0)[before:] {
		if notice == "pong" {
			pongs++
		}
	}
	if pongs != 1 {
		t.Errorf("a repeated request inside the cooldown produced %v answers, want 1: %v",
			pongs, rig.notices(t, 0)[before:])
	}
}

// liveHubEnv names the hub a live run should use. The live test is skipped
// unless it is set, so the suite stays green with no network at all.
const liveHubEnv = "GORRCBOT_LIVE_HUB"

// TestLiveHubRoundTrip is the optional live check: with GORRCBOT_LIVE_HUB set to
// a real hub's destination hash, the bot is brought up against that hub and a
// second client verifies a genuine addressed-command round trip.
//
// It is skipped by default. Nothing here is public unless an operator names a
// public hub, and no test in this file ever contacts a hub that the operator did
// not choose.
func TestLiveHubRoundTrip(t *testing.T) {
	hubHex := strings.TrimSpace(os.Getenv(liveHubEnv))
	if hubHex == "" {
		t.Skipf("set %v=<hub destination hash> to run the live round trip", liveHubEnv)
	}
	if !isHexString(hubHex) || len(hubHex) != rrc.IdentityHashLen*2 {
		t.Fatalf("%v=%q is not a 32-character hub destination hash", liveHubEnv, hubHex)
	}
	room := os.Getenv("GORRCBOT_LIVE_ROOM")
	if room == "" {
		room = "general"
	}

	root := tempDir(t)
	// The live run attaches to the operator's real Reticulum configuration, so
	// it learns the hub's path the same way the bot does in production.
	live := &inboundCollector{}
	asker := newLiveAsker(t, root, hubHex, room, live)

	home := filepath.Join(root, "gorrcbot-home")
	paths := BotPaths{
		Home:         home,
		ConfigPath:   filepath.Join(home, defaultConfigFileName),
		IdentityPath: filepath.Join(home, defaultIdentityFileName),
		StorageDir:   filepath.Join(home, defaultStorageDirName),
	}
	if _, err := EnsureFirstRun(paths); err != nil {
		t.Fatalf("EnsureFirstRun: %v", err)
	}
	identity, _, err := LoadBotIdentity(paths.IdentityPath)
	if err != nil {
		t.Fatalf("LoadBotIdentity: %v", err)
	}

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulum(ts, "")
	if err != nil {
		t.Fatalf("NewReticulum: %v", err)
	}
	t.Cleanup(func() {
		if err := ret.Close(); err != nil {
			t.Logf("closing the live Reticulum: %v", err)
		}
	})

	cfg := &BotConfig{
		Nick:           DefaultNick,
		Reply:          ReplyAuto,
		MaxReplyLines:  DefaultMaxReplyLines,
		AnnounceOnJoin: true,
		Hubs: []HubConfig{{
			Name:        "Live hub",
			Destination: hubHex,
			DestHash:    mustHex(hubHex),
			Rooms:       []RoomConfig{{Name: room}},
		}},
	}
	dialer := newManagerDialer(identity, cfg.Nick, paths.StorageDir, ret)
	b := newBot(cfg, paths, logger, identity.Hash, dialer, botHooks{Greeting: greeting})
	reg := newRegistry(b)
	b.hooks.Inbound = newResponder(cfg, identity.Hash, reg.Run).handle

	botLog := &capturedLog{}
	prevOutput := log.Writer()
	log.SetOutput(botLog)
	t.Cleanup(func() { log.SetOutput(prevOutput) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("b.Run returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("b.Run did not return after the context was cancelled")
		}
	})

	botHash := hexString(identity.Hash)
	t.Logf("live bot identity %v on hub %v room %v", botHash, hubHex, room)

	// The bot must appear in the room and answer an addressed command.
	sawJoin := waitForCondition(integrationGreetWait, func() bool {
		for _, member := range asker.GetRoomMembers(room) {
			if member.HashHex == botHash {
				return true
			}
		}
		return false
	})
	if !sawJoin {
		t.Errorf("the hub never reported the bot as a member of %v; bot log:\n%v",
			room, botLog.String())
	}

	// A live link can flap right after it comes up, and a request sent into
	// that window is simply lost: the hub fans it out to a link that is no
	// longer there. Wait for a session that has held for a moment before
	// asking, then repeat the request until it is answered. The bot is
	// silent, so an unanswered request cannot be confused with a refusal.
	settled := waitForCondition(integrationGreetWait, func() bool {
		if len(b.sessions) != 1 {
			return false
		}
		sess := b.sessions[0]
		return sess.isJoined(room) && sess.connectionAge(time.Now()) > 3*time.Second
	})
	if !settled {
		t.Errorf("the bot's session never settled in %v; bot log:\n%v", room, botLog.String())
	}

	// The answer may arrive in the room or as a direct NOTICE, since the
	// production default is "auto": the collector sees both routes.
	answered := func() bool {
		return slices.ContainsFunc(live.all(), func(msg *rrc.RRCMessage) bool {
			return strings.HasPrefix(msg.Text, "Commands: ")
		})
	}
	if !waitForCondition(integrationWait, func() bool {
		asker.SendMessage(room, "@"+DefaultNick+" help")
		return waitForCondition(4*time.Second, answered)
	}) {
		var roster []string
		for _, member := range asker.GetRoomMembers(room) {
			roster = append(roster, fmt.Sprintf("%v(%v)", member.HashHex, member.Nick))
		}
		t.Errorf("no live help round trip in %v; received %v; roster %v; bot log:\n%v",
			room, live.texts(), roster, botLog.String())
	}
}

// newLiveAsker starts a client against a real hub. An operator running the bot
// against their own hub usually wants it to share that hub's Reticulum
// instance, so the config directory comes from GORRCBOT_LIVE_CONFIG and falls
// back to the standard configuration.
func newLiveAsker(t *testing.T, root, hubHex, room string,
	collector *inboundCollector) *rrc.RRCHub {
	t.Helper()
	configDir := strings.TrimSpace(os.Getenv("GORRCBOT_LIVE_CONFIG"))

	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	ts := rns.NewTransportSystem(logger)
	ret, err := rns.NewReticulum(ts, configDir)
	if err != nil {
		t.Fatalf("asker NewReticulum: %v", err)
	}
	t.Cleanup(func() {
		if err := ret.Close(); err != nil {
			t.Logf("closing the live asker's Reticulum: %v", err)
		}
	})

	identity, err := rns.NewIdentity(true, logger)
	if err != nil {
		t.Fatalf("asker NewIdentity: %v", err)
	}
	mgr := rrc.NewManager(filepath.Join(root, "live-asker-storage"), func() []byte {
		return identity.Hash
	})
	mgr.SetIdentity(identity)
	mgr.SetNickname("gorrcbot-live-check")
	t.Logf("live asker identity %v nick %v", hexString(identity.Hash), "gorrcbot-live-check")
	mgr.SetTransport(ts)
	t.Cleanup(mgr.Shutdown)

	hub := mgr.AddHub(mustHex(hubHex), rrc.HubDestName, "Live hub")
	if hub == nil {
		t.Fatal("could not create the live asker's hub connection")
	}
	hub.AddRoom(room)
	hub.SetOnMessage(collector.add)
	hub.SetAutoReconnect(false, false)
	hub.SetAutoList(false, false)
	hub.SetAutoWho(false, false)
	t.Cleanup(hub.Disconnect)
	hub.ConnectAsync()

	if !waitForCondition(integrationGreetWait, func() bool {
		return hub.GetHubStatus() == rrc.StatusConnected && hub.HasRoom(room)
	}) {
		t.Fatalf("the live asker never connected (status %v, room %v)",
			hub.GetHubStatus(), hub.HasRoom(room))
	}
	return hub
}

// TestIntegrationClientStorageIsADirectory asserts the one path the bot hands
// the RRC client is created as a DIRECTORY. It used to be called state.toml, so
// the client made a directory wearing a file's name.
func TestIntegrationClientStorageIsADirectory(t *testing.T) {
	rig := newIntegrationRig(t, 1, ReplyRoom)

	if !waitForCondition(integrationGreetWait, func() bool { return rig.greetings(t, 0) >= 1 }) {
		rig.failf(t, "the bot never introduced itself; notices so far: %v", rig.notices(t, 0))
	}

	storageDir := filepath.Join(rig.home, defaultStorageDirName)
	info, err := os.Stat(storageDir)
	if err != nil {
		t.Fatalf("the client never created %v: %v", storageDir, err)
	}
	if !info.IsDir() {
		t.Errorf("%v is not a directory", storageDir)
	}
	if filepath.Ext(storageDir) != "" {
		t.Errorf("storage path %q looks like a file name", storageDir)
	}
}
