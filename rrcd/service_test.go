// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// hubTestEnv is a HubService with stubbed seams for loop and drain tests.
type hubTestEnv struct {
	hub        *HubService
	sends      []sentPacket
	announces  [][]byte
	sleepCalls []float64
	sleepOK    bool
	nowWall    float64
	nowMono    float64
}

// newHubTestEnv builds a HubService with test seams and the standard
// default config.
func newHubTestEnv(t *testing.T) *hubTestEnv {
	t.Helper()
	env := &hubTestEnv{
		sleepOK: true,
		nowWall: 1730000000.0,
		nowMono: 100.0,
	}
	hub := NewHubService(DefaultHubConfig())
	hub.sendPacket = func(link *rns.Link, payload []byte) error {
		env.sends = append(env.sends, sentPacket{link: link, payload: payload})
		return nil
	}
	hub.announce = func(appData []byte) error {
		env.announces = append(env.announces, appData)
		return nil
	}
	hub.sleep = func(d float64) bool {
		env.sleepCalls = append(env.sleepCalls, d)
		return env.sleepOK
	}
	hub.nowWall = func() float64 { return env.nowWall }
	hub.nowMono = func() float64 { return env.nowMono }
	hub.logf = func(string, ...any) {}
	env.hub = hub
	return env
}

// setDestination injects a real destination (and its identity) so the
// announce and notice paths run without a live network.
func (env *hubTestEnv) setDestination(t *testing.T) {
	t.Helper()
	identity := mustTestIdentity(t)
	ts := rns.NewTransportSystem(testSilentRNSLogger())
	dest, err := rns.NewDestination(ts, identity, rns.DestinationIn, rns.DestinationSingle, "rrc", "hub")
	if err != nil {
		t.Fatalf("NewDestination error: %v", err)
	}
	env.hub.destination = dest
	env.hub.setIdentityForTest(identity)
}

// G11.1 NewHubService wires the managers, forces the destination name,
// and opens the shutdown channel.
func TestNewHubService(t *testing.T) {
	t.Parallel()

	hub := NewHubService(DefaultHubConfig())
	if hub.Config.DestName != HubDestName {
		t.Errorf("DestName = %q, want %q", hub.Config.DestName, HubDestName)
	}
	hub2 := NewHubService(func() HubConfig {
		cfg := DefaultHubConfig()
		cfg.DestName = "other.hub"
		return cfg
	}())
	if hub2.Config.DestName != HubDestName {
		t.Errorf("forced DestName = %q, want %q", hub2.Config.DestName, HubDestName)
	}
	if hub.Router == nil || hub.SessionManager == nil || hub.CommandHandler == nil ||
		hub.ResourceManager == nil || hub.RoomManager == nil || hub.StatsManager == nil ||
		hub.TrustManager == nil || hub.MessageHelper == nil {
		t.Fatal("hub managers are not fully wired")
	}
	select {
	case <-hub.Shutdown:
		t.Error("shutdown channel is already closed")
	default:
	}
}

// G11.3 AnnounceOnce sends the exact app_data CBOR bytes and counts the
// announce; a missing destination skips the announce entirely.
func TestAnnounceOnce(t *testing.T) {
	t.Parallel()

	// The golden bytes captured from python-cbor2 with the Python key
	// order proto, v, hub.
	got := hex.EncodeToString(buildAnnounceAppData("rrc"))
	want := "a36570726f746f637272636176016368756263727263"
	if got != want {
		t.Errorf("announce app_data = %v, want %v", got, want)
	}
	got = hex.EncodeToString(buildAnnounceAppData("TestHub"))
	want = "a36570726f746f63727263617601636875626754657374487562"
	if got != want {
		t.Errorf("named hub app_data = %v, want %v", got, want)
	}

	env := newHubTestEnv(t)
	env.hub.AnnounceOnce()
	if len(env.announces) != 0 || env.hub.StatsManager.Counter("announces") != 0 {
		t.Errorf("announce without a destination ran: %v", env.announces)
	}
}

// G11.3 AnnounceLoop sleeps first, announces each cycle, and exits when
// the sleep reports the shutdown; a zero period idles on the 1-second
// poll without announcing.
func TestAnnounceLoop(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)
	env.hub.Config.AnnouncePeriodS = 0.5
	cycles := 0
	done := make(chan struct{})
	go func() {
		env.hub.sleep = func(d float64) bool {
			cycles++
			if cycles >= 3 {
				env.hub.ShutdownOnce.Do(func() { close(env.hub.Shutdown) })
				return false
			}
			return true
		}
		env.hub.AnnounceLoop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("announce loop did not exit on shutdown")
	}
	if len(env.announces) != 2 {
		t.Errorf("announces = %v, want 2", len(env.announces))
	}

	// A zero period idles on the 1-second poll.
	env2 := newHubTestEnv(t)
	env2.hub.Config.AnnouncePeriodS = 0
	env2.hub.sleep = func(d float64) bool {
		env2.sleepCalls = append(env2.sleepCalls, d)
		env2.hub.ShutdownOnce.Do(func() { close(env2.hub.Shutdown) })
		return false
	}
	stop := make(chan struct{})
	go func() { env2.hub.AnnounceLoop(); close(stop) }()
	select {
	case <-stop:
	case <-time.After(2 * time.Second):
		t.Fatal("zero-period announce loop did not exit")
	}
	if len(env2.sleepCalls) == 0 || env2.sleepCalls[0] != 1.0 {
		t.Errorf("zero-period sleep calls = %v, want a 1.0 idle poll first", env2.sleepCalls)
	}
	if len(env2.announces) != 0 {
		t.Errorf("zero-period announces = %v, want none", env2.announces)
	}
}

// G11.10 NormRoom mirrors _norm_room: Python's Unicode strip and lower,
// the empty check, and the UTF-8 byte-length check (U+0130 lowercases to
// "i" + U+0307 exactly like Python, three UTF-8 bytes).
func TestNormRoom(t *testing.T) {
	t.Parallel()

	hub := NewHubService(DefaultHubConfig())

	norm, err := hub.NormRoom("  General Room ")
	if err != nil || norm != "general room" {
		t.Errorf("NormRoom trimmed = %q, %v, want %q", norm, err, "general room")
	}
	if _, err := hub.NormRoom("   "); err == nil || err.Error() != "room name must not be empty" {
		t.Errorf("NormRoom whitespace error = %v, want the empty-name error", err)
	}
	// The exact byte count renders in the error.
	long := strings.Repeat("x", 65)
	_, err = hub.NormRoom(long)
	if err == nil || err.Error() != "room name too long: 65 bytes > 64 bytes" {
		t.Errorf("NormRoom(65 bytes) error = %v, want 65 > 64", err)
	}

	norm, err = hub.NormRoom("İstanbul")
	if err != nil {
		t.Fatalf("NormRoom(İstanbul) error = %v", err)
	}
	if norm != "i̇stanbul" {
		t.Errorf("NormRoom(İstanbul) = %q, want %q (i + U+0307)", norm, "i̇stanbul")
	}
	if len(norm) != len("i\xcc\x87stanbul") {
		t.Errorf("NormRoom(İstanbul) byte length = %v, want %v", len(norm), len("i\xcc\x87stanbul"))
	}
}

// G11.11 ResolveIdentityHash resolves an online match first and falls
// back to parsing offline tokens.
func TestResolveIdentityHash(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	linkA := &rns.Link{}
	peerA := bytesOf(0xaa, 32)
	hub := env.hub
	hub.SessionManager.OnLinkEstablished(linkA)
	hub.SessionManager.OnRemoteIdentified(linkA, peerA)

	if got := hub.ResolveIdentityHash(hexKey(peerA), nil); !sameBytes(got, peerA) {
		t.Errorf("online hash resolve = %v, want %v", got, peerA)
	}
	offline := bytesOf(0x5a, 32)
	if got := hub.ResolveIdentityHash("0x"+hexKey(offline), nil); !sameBytes(got, offline) {
		t.Errorf("offline hash resolve = %v, want %v", got, offline)
	}
	if got := hub.ResolveIdentityHash("zzzz", nil); got != nil {
		t.Errorf("unparseable resolve = %v, want nil", got)
	}
}

// G11.5 OnPacket routes one packet and drains the outgoing queue with
// per-item bytes_out counting and the post-send callbacks.
func TestOnPacketDrain(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	hub := env.hub
	link := &rns.Link{}

	outgoing := &OutgoingList{}
	payload := []byte{0x01, 0x02}
	hub.MessageHelper.QueuePayload(outgoing, link, payload)
	called := 0
	outgoing.PostSendCallbacks = append(outgoing.PostSendCallbacks, func() { called++ })

	hub.drainOutgoing(outgoing)
	if len(env.sends) != 1 || !sameBytes(env.sends[0].payload, payload) {
		t.Errorf("drain sends = %+v, want one payload", env.sends)
	}
	// bytes_out is double-counted for queued payloads (queue_payload
	// counts once, the drain loop again) — the documented quirk.
	if hub.StatsManager.Counter("bytes_out") != 2*len(payload) {
		t.Errorf("bytes_out = %v, want %v (double-counted)",
			hub.StatsManager.Counter("bytes_out"), 2*len(payload))
	}
	if called != 1 {
		t.Errorf("post-send callback calls = %v, want 1", called)
	}
}

// G11.6 PingLoop pings welcomed sessions, records the pending pong, and
// tears down timed-out sessions.
func TestPingLoop(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	hub := env.hub
	hub.Config.PingIntervalS = 1.0
	hub.Config.PingTimeoutS = 5.0
	link := &rns.Link{}
	hub.SessionManager.OnLinkEstablished(link)
	sess := hub.SessionManager.GetSession(link)
	sess.Welcomed = true
	peer := bytesOf(0xaa, 32)
	hub.SessionManager.OnRemoteIdentified(link, peer)
	hub.setIdentityForTest(mustTestIdentity(t))
	env.nowMono = 100.0

	done := make(chan struct{})
	pingSleeps := 0
	go func() {
		hub.sleep = func(d float64) bool {
			pingSleeps++
			env.sleepCalls = append(env.sleepCalls, d)
			if pingSleeps >= 2 {
				hub.ShutdownOnce.Do(func() { close(hub.Shutdown) })
				return false
			}
			return true
		}
		hub.PingLoop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ping loop did not exit on shutdown")
	}

	if len(env.sleepCalls) == 0 || env.sleepCalls[0] != 1.0 {
		t.Errorf("ping sleep calls = %v, want the interval-first sleep", env.sleepCalls)
	}
	if len(env.sends) != 1 {
		t.Fatalf("ping sends = %+v, want one PING", env.sends)
	}
	env2, err := decodeEnvelope(env.sends[0].payload)
	if err != nil {
		t.Fatalf("ping payload does not decode: %v", err)
	}
	if v, _ := env2.Get(KT); !int64Equal(v, int64(TPing)) {
		t.Errorf("ping type = %v, want PING", v)
	}
	body, _ := env2.Get(KBody)
	if _, isFloat := body.(float64); !isFloat {
		t.Errorf("ping body = %T, want a float64 monotonic reading", body)
	}
	if hub.StatsManager.Counter("pings_out") != 1 {
		t.Errorf("pings_out = %v, want 1", hub.StatsManager.Counter("pings_out"))
	}
	if await := hub.SessionManager.AwaitingPong(link); await == nil || *await != 100.0 {
		t.Errorf("awaiting_pong = %v, want 100.0", await)
	}
}

// G11.7 PruneLoop only deletes registry entries when at least one session
// exists (the dummy-link guard) and prunes expired registered rooms.
func TestPruneLoop(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	hub := env.hub
	hub.Config.RoomRegistryPruneIntervalS = 1.0
	hub.Config.RoomRegistryPruneAfterS = 10.0
	hub.StatsManager.SetStartTime()

	regPath := filepath.Join(testutils.TempDir(t, "prune"), "rooms.toml")
	staleTS := env.nowWall - 100.0
	freshTS := env.nowWall - 1.0
	roomFile := "[rooms]\n\n[rooms.stale]\nfounder = \"" + hexKey(bytesOf(0xaa, 32)) +
		"\"\nregistered = true\nlast_used_ts = " + fmtPythonFloat(staleTS) +
		"\n\n[rooms.fresh]\nfounder = \"" + hexKey(bytesOf(0xaa, 32)) +
		"\"\nregistered = true\nlast_used_ts = " + fmtPythonFloat(freshTS) + "\n"
	if err := os.WriteFile(regPath, []byte(roomFile), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.Config.RoomRegistryPath = &regPath
	loaded, loadErr := hub.RoomManager.LoadRegistryFromPath(regPath)
	if loadErr != "" {
		t.Fatalf("LoadRegistryFromPath error: %v", loadErr)
	}
	hub.RoomManager.ReplaceRegistry(loaded)
	if reg, ok := hub.RoomManager.RegistryGet("stale"); !ok || reg.LastUsedTS == nil || *reg.LastUsedTS != staleTS {
		t.Fatalf("stale room registry entry = %+v, want last_used_ts %v", reg, staleTS)
	}

	// No sessions: the room leaves the live registry but the file keeps
	// it (the dummy-link guard skips the file deletion).
	hub.pruneOnce()
	if _, stillThere := hub.RoomManager.RegistryGet("stale"); stillThere {
		t.Error("the stale room stayed in the live registry after the prune pass")
	}
	fileData, err := os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fileData), "stale") {
		t.Error("the registry file lost the stale room although no session exists (dummy-link guard)")
	}

	// With a session present the file deletion runs too.
	link := &rns.Link{}
	hub.SessionManager.OnLinkEstablished(link)
	hub.RoomManager.RegistrySet("stale", &RoomState{Registered: true, Founder: bytesOf(0xaa, 32), LastUsedTS: &staleTS})
	hub.pruneOnce()
	fileData, err = os.ReadFile(regPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(fileData), "stale") {
		t.Error("the registry file kept the stale room after the prune pass with a session present")
	}
	if _, stillThere := hub.RoomManager.RegistryGet("fresh"); !stillThere {
		t.Error("the fresh room was pruned although it was recently used")
	}
}

// fmtPythonFloat renders a float the way Python repr does (integral
// floats keep the trailing .0).
func fmtPythonFloat(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10) + ".0"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// G11.8 Stop clears the manager state, closes the shutdown channel, and
// tears the links down.
func TestStop(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	hub := env.hub
	link := &rns.Link{}
	hub.SessionManager.OnLinkEstablished(link)
	peer := bytesOf(0xbb, 32)
	hub.SessionManager.OnRemoteIdentified(link, peer)
	hub.RoomManager.EnsureRoomState("general", peer)

	hub.Stop()
	select {
	case <-hub.Shutdown:
	default:
		t.Error("shutdown channel is still open after Stop")
	}
	if got := hub.SessionManager.GetSession(link); got != nil {
		t.Error("session survived Stop")
	}
}

// mustTestIdentity creates a fresh identity for tests.
func mustTestIdentity(t *testing.T) *rns.Identity {
	t.Helper()
	ident, err := rns.NewIdentity(true, testSilentRNSLogger())
	if err != nil {
		t.Fatalf("NewIdentity error: %v", err)
	}
	return ident
}

// testSilentRNSLogger builds a silent RNS logger for tests.
func testSilentRNSLogger() *rns.Logger {
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	return logger
}

// G11.2 + G11.12 Start brings the stack up standalone with an isolated
// RNS config dir and a generated identity, announces on start, and never
// attaches to a shared instance.
func TestHubServiceStartStandalone(t *testing.T) {
	t.Parallel()

	rnsDir := testutils.TempDir(t, "gorrcd-rns")
	rnsConfig := "[reticulum]\nshare_instance = No\n"
	if err := os.MkdirAll(rnsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rnsDir, "config"), []byte(rnsConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	identity, err := rns.NewIdentity(true, testSilentRNSLogger())
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(rnsDir, "hub_identity")
	if err := identity.ToFile(identityPath); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultHubConfig()
	cfg.Configdir = &rnsDir
	cfg.IdentityPath = &identityPath
	cfg.AnnounceOnStart = true

	hub := NewHubService(cfg)
	hub.SetLogger(testSilentRNSLogger())
	hub.logf = func(string, ...any) {}
	if err := hub.Start(); err != nil {
		t.Fatalf("Start error: %v", err)
	}
	defer hub.Stop()

	if hub.DestinationHash() == nil {
		t.Error("destination hash is nil after Start")
	}
	if hub.IdentityHash() == nil {
		t.Error("identity hash is nil after Start")
	}
	if hub.IsConnectedToSharedInstance() {
		t.Error("the hub attached to a shared instance; standalone operation is required")
	}
	// The announce-on-start announce ran (it fails without a network but
	// the attempt is observable through the stats).
	if hub.StatsManager.Counter("announces") != 1 {
		t.Errorf("announces = %v, want 1", hub.StatsManager.Counter("announces"))
	}
}

// G11.2 Start requires an identity path and an existing identity file.
func TestHubServiceStartIdentityErrors(t *testing.T) {
	t.Parallel()

	rnsDir := testutils.TempDir(t, "gorrcd-rns")
	rnsConfig := "[reticulum]\nshare_instance = No\n"
	if err := os.MkdirAll(rnsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rnsDir, "config"), []byte(rnsConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	// No identity path at all.
	cfg := DefaultHubConfig()
	cfg.Configdir = &rnsDir
	hub := NewHubService(cfg)
	hub.SetLogger(testSilentRNSLogger())
	hub.logf = func(string, ...any) {}
	err := hub.Start()
	if err == nil || err.Error() != "identity_path is not set" {
		t.Errorf("Start without an identity path = %v, want identity_path is not set", err)
	}

	// A missing identity file: the Identity not found error. A second
	// RNS dir keeps the two bring-ups independent.
	rnsDir2 := testutils.TempDir(t, "gorrcd-rns2")
	if err := os.MkdirAll(rnsDir2, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rnsDir2, "config"), []byte(rnsConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg2 := DefaultHubConfig()
	cfg2.Configdir = &rnsDir2
	missing := filepath.Join(rnsDir2, "missing_identity")
	cfg2.IdentityPath = &missing
	hub2 := NewHubService(cfg2)
	hub2.SetLogger(testSilentRNSLogger())
	hub2.logf = func(string, ...any) {}
	err = hub2.Start()
	if err == nil || err.Error() != "Identity not found at "+missing {
		t.Errorf("Start with a missing identity = %v, want Identity not found", err)
	}
}

// G11.9 ReloadConfigAndRooms emits the exact failure notices for the
// missing path, the parse error, and the identity-list parse error.
func TestReloadConfigFailureNotices(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)
	hub := env.hub
	link := &rns.Link{}

	// Missing config_path: the exact notice.
	outgoing := &OutgoingList{}
	hub.ReloadConfigAndRooms(link, nil, outgoing)
	sent := decodeOutgoing(t, outgoing)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("missing-path reload output = %+v, want one room-nil NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "reload failed: config_path not set or missing" {
		t.Errorf("missing-path reload body = %q", body)
	}

	// A config path that does not exist: the same notice.
	missing := "/tmp/definitely-missing-rrcd.toml"
	hub.Config.ConfigPath = &missing
	outgoing = &OutgoingList{}
	hub.ReloadConfigAndRooms(link, nil, outgoing)
	sent = decodeOutgoing(t, outgoing)
	if len(sent) != 1 {
		t.Fatalf("missing-file reload output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); body != "reload failed: config_path not set or missing" {
		t.Errorf("missing-file reload body = %q", body)
	}

	// A parse error: the parse-error notice.
	badPath := filepath.Join(testutils.TempDir(t, "badcfg"), "rrcd.toml")
	if err := os.WriteFile(badPath, []byte("[hub\nbroken"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.Config.ConfigPath = &badPath
	outgoing = &OutgoingList{}
	hub.ReloadConfigAndRooms(link, nil, outgoing)
	sent = decodeOutgoing(t, outgoing)
	if len(sent) != 1 {
		t.Fatalf("parse-error reload output = %+v, want one NOTICE", sent)
	}
	if body, _ := sent[0].body.(string); !strings.HasPrefix(body, "reload failed: config parse error: ") {
		t.Errorf("parse-error reload body = %q", body)
	}
}

// G11.9 ReloadConfigAndRooms applies a valid config swap, merges the
// registry, and emits the success summary with counts and diffs.
func TestReloadConfigSuccess(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)
	hub := env.hub
	link := &rns.Link{}
	hub.StatsManager.SetStartTime()

	dir := testutils.TempDir(t, "reload")
	cfgPath := filepath.Join(dir, "rrcd.toml")
	regPath := filepath.Join(dir, "rooms.toml")
	configText := "[hub]\nhub_name = \"ReloadedHub\"\nmax_nick_bytes = 40\n"
	if err := os.WriteFile(cfgPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	roomsText := "[rooms]\n\n[rooms.extra]\nfounder = \"" + hexKey(bytesOf(0xaa, 32)) + "\"\nregistered = true\n"
	if err := os.WriteFile(regPath, []byte(roomsText), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.Config.ConfigPath = &cfgPath
	hub.Config.RoomRegistryPath = &regPath
	hub.RoomManager.RegistrySet("oldroom", &RoomState{Registered: true})

	outgoing := &OutgoingList{}
	hub.ReloadConfigAndRooms(link, nil, outgoing)
	sent := decodeOutgoing(t, outgoing)
	if len(sent) != 1 || sent[0].msgType != TNotice || sent[0].room != nil {
		t.Fatalf("success reload output = %+v, want one room-nil NOTICE", sent)
	}
	body, _ := sent[0].body.(string)
	wantHeader := "reloaded: trusted=0->0 banned=0->0 registered_rooms=1->1"
	if !strings.HasPrefix(body, wantHeader) {
		t.Errorf("success reload body header = %q, want prefix %q", body, wantHeader)
	}
	if !strings.Contains(body, "policy: max_nick_bytes=40") {
		t.Errorf("success reload body lacks the policy line: %q", body)
	}
	if !strings.Contains(body, "config_changes:") || !strings.Contains(body, "hub_name: rrc -> ReloadedHub") {
		t.Errorf("success reload body lacks the config changes: %q", body)
	}
	if !strings.Contains(body, "rooms_added=1: extra") {
		t.Errorf("success reload body lacks the room addition: %q", body)
	}
	if hub.Config.HubName != "ReloadedHub" || hub.Config.MaxNickBytes != 40 {
		t.Errorf("config after reload = %q/%v, want ReloadedHub/40", hub.Config.HubName, hub.Config.MaxNickBytes)
	}
	if _, ok := hub.RoomManager.RegistryGet("extra"); !ok {
		t.Error("the registry reload did not pick up the new room")
	}
}

// G11.4 OnLink wires the packet/closed/remote-identified callbacks and
// initializes the session and resource state; OnRemoteIdentified
// disconnects banned peers with the `banned` ERROR and a teardown; OnClose
// cleans up the session.
func TestLinkCallbacks(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)
	hub := env.hub
	link := &rns.Link{}

	// OnLink initializes the session and resource state; the callback
	// wiring itself is exercised by the cross-implementation suite.
	hub.OnLink(link)
	if hub.SessionManager.GetSession(link) == nil {
		t.Error("OnLink did not create the session")
	}

	// A banned peer is disconnected with the `banned` ERROR.
	peer := bytesOf(0xbb, 32)
	if err := hub.TrustManager.LoadFromConfig(nil, []string{hexKey(peer)}); err != nil {
		t.Fatal(err)
	}
	link2 := &rns.Link{}
	hub.OnLink(link2)
	hub.OnRemoteIdentified(link2, mustTestIdentified(t, peer))
	if sent := env.sends; len(sent) == 0 {
		t.Error("the banned peer received no ERROR envelope")
	} else {
		env2, err := decodeEnvelope(sent[0].payload)
		if err != nil {
			t.Fatalf("banned ERROR payload does not decode: %v", err)
		}
		if v, _ := env2.Get(KT); !int64Equal(v, int64(TError)) {
			t.Errorf("banned peer envelope type = %v, want ERROR", v)
		}
		if body, _ := env2.Get(KBody); body != "banned" {
			t.Errorf("banned peer body = %v, want banned", body)
		}
	}

	// OnClose cleans the session up.
	hub.OnClose(link2)
	if hub.SessionManager.GetSession(link2) != nil {
		t.Error("the closed link's session survived OnClose")
	}
}

// mustTestIdentified wraps a peer hash in an identity for the
// remote-identified callback.
func mustTestIdentified(t *testing.T, peer []byte) *rns.Identity {
	t.Helper()
	ident := mustTestIdentity(t)
	ident.Hash = peer
	return ident
}
