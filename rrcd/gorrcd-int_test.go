// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

// This file holds the cross-implementation integration harness: the real
// gorrcd binary runs with an RNS config whose only interface is a
// PipeInterface spawning the Python driver, which bridges HDLC-framed RNS
// frames to a real RNS.Reticulum and drives the real nomadnet.RRC client
// against the hub. Observability is a JSONL events file (the pipe's
// stdin/stdout carry the HDLC data channel, and the Go
// PipeSubprocessInterface sends the child's stderr to /dev/null).

package rrcd

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// pythonDriverScript is the Python driver: it bridges the child side of the
// HDLC pipe into a real RNS.Reticulum (a custom stdio Interface registered
// with RNS.Transport.add_interface, framing identical to
// RNS/Interfaces/PipeInterface.py: FLAG 0x7E, ESC 0x7D, ESC_MASK 0x20,
// HW_MTU 1064) and drives the real nomadnet.RRC client. Every
// oracle-observable state change is appended to the JSONL events file; all
// RNS logging goes to a file because stdout/stdin ARE the data channel and
// the Go PipeSubprocessInterface sends the child's stderr to /dev/null.
// On stdin EOF the driver exits hard so killing gorrcd tears the helper
// down without triggering the respawn loop.
const pythonDriverScript = `
import json, os, sys, threading, time
import RNS
from RNS.Interfaces.Interface import Interface
from nomadnet.vendor import cbor
import nomadnet.RRC as rrc

rns_configdir = sys.argv[1]
events_path = sys.argv[2]
command_file = sys.argv[3]
storagepath = sys.argv[4]

os.makedirs(storagepath, exist_ok=True)

# stdout/stdin are the HDLC data channel: send RNS logging to a file before
# bringing up Reticulum, never print(), and hard-exit on stdin EOF.
RNS.logdest = RNS.LOG_FILE
RNS.logfile = os.path.join(rns_configdir, "rns.log")
reticulum = RNS.Reticulum(rns_configdir)

events_lock = threading.Lock()

def emit(event):
    with events_lock:
        with open(events_path, "a") as f:
            f.write(json.dumps(event) + "\n")

def jsonable(value):
    if isinstance(value, (bytes, bytearray)):
        return {"__bytes__": bytes(value).hex()}
    if isinstance(value, dict):
        return {str(k): jsonable(v) for k, v in value.items()}
    if isinstance(value, (list, tuple)):
        return [jsonable(v) for v in value]
    if isinstance(value, (str, int, float, bool)) or value is None:
        return value
    return str(value)

class HDLC:
    FLAG = 0x7E
    ESC = 0x7D
    ESC_MASK = 0x20

    @staticmethod
    def escape(data):
        data = data.replace(bytes([HDLC.ESC]), bytes([HDLC.ESC, HDLC.ESC ^ HDLC.ESC_MASK]))
        data = data.replace(bytes([HDLC.FLAG]), bytes([HDLC.ESC, HDLC.FLAG ^ HDLC.ESC_MASK]))
        return data

class StdioInterface(Interface):
    DEFAULT_IFAC_SIZE = 8
    BITRATE_GUESS = 1000000

    def __init__(self):
        super().__init__()
        self.name = "StdioBridge"
        self.online = True
        self.bitrate = StdioInterface.BITRATE_GUESS
        self.HW_MTU = 1064
        self.ifac_size = self.DEFAULT_IFAC_SIZE
        self.ifac_identity = None
        self.ifac_key = None
        self.announce_rate_target = None
        self.announce_rate_grace = 0
        self.announce_rate_penalty = 0
        self.egress_control = None
        self.discovery_height = None
        self.discovery_frequency = None
        self.discovery_bandwidth = None
        self.discovery_modulation = None
        self.IN = True
        self.OUT = True
        threading.Thread(target=self.read_loop, daemon=True).start()
        threading.Thread(target=self.read_loop, daemon=True).start()

    def process_incoming(self, data):
        self.rxb += len(data)
        RNS.Transport.inbound(data, self)

    def process_outgoing(self, data):
        frame = bytes([HDLC.FLAG]) + HDLC.escape(data) + bytes([HDLC.FLAG])
        sys.stdout.buffer.write(frame)
        sys.stdout.buffer.flush()

    def read_loop(self):
        in_frame = False
        escape = False
        buf = b""
        while True:
            chunk = sys.stdin.buffer.read(1)
            if len(chunk) == 0:
                sys.stdout.buffer.flush()
                os._exit(0)
            byte = chunk[0]
            if in_frame and byte == HDLC.FLAG:
                in_frame = False
                self.process_incoming(buf)
            elif byte == HDLC.FLAG:
                in_frame = True
                buf = b""
            elif in_frame and len(buf) < self.HW_MTU:
                if byte == HDLC.ESC:
                    escape = True
                else:
                    if escape:
                        if byte == HDLC.FLAG ^ HDLC.ESC_MASK:
                            byte = HDLC.FLAG
                        if byte == HDLC.ESC ^ HDLC.ESC_MASK:
                            byte = HDLC.ESC
                        escape = False
                    buf = buf + bytes([byte])

iface = StdioInterface()
RNS.Transport.add_interface(iface)

class FakeApp:
    def __init__(self, ident, nick, storage):
        self.identity = ident
        self.peer_settings = {"display_name": nick}
        self.storagepath = storage

class HubAnnounceHandler:
    aspect_filter = "rrc.hub"

    def received_announce(self, destination_hash, announced_identity, app_data):
        emit({
            "event": "hub-hash",
            "hash": destination_hash.hex(),
            "app_data_hex": app_data.hex() if app_data else "",
        })

handler = HubAnnounceHandler()
RNS.Transport.register_announce_handler(handler)

# The hash-file fallback mirrors the rrc-xprocess harness: the Go test may
# publish the hub hash directly.
hub_hash = None
hash_file = os.path.join(os.path.dirname(events_path), "hubhash")
deadline = time.monotonic() + 30.0
while time.monotonic() < deadline and hub_hash is None:
    try:
        with open(hash_file, "r") as f:
            h = f.read().strip()
        if h:
            hub_hash = bytes.fromhex(h)
            break
    except Exception:
        pass
    try:
        with open(events_path, "r") as f:
            for line in f:
                event = json.loads(line)
                if event.get("event") == "hub-hash":
                    hub_hash = bytes.fromhex(event["hash"])
                    break
    except Exception:
        pass
    time.sleep(0.2)

if hub_hash is None:
    emit({"event": "hash-timeout"})
    sys.exit(1)

identity = RNS.Identity()
own_hash = bytes(identity.hash)

class Client:
    def __init__(self, index, nick):
        self.index = index
        self.nick = nick
        self.app = FakeApp(identity, nick, storagepath)
        self.manager = rrc.RRCManager(self.app)
        self.hub = self.manager.add_hub(hub_hash, dest_name="rrc.hub", name="hub" + str(index))
        # Observe every received envelope by wrapping the hub's packet
        # callback (late-bound at call time, so instance patching works).
        orig = self.hub._on_packet

        def wrapped(data, _orig=orig):
            try:
                env = cbor.decode(data)
                emit({"event": "envelope", "hub_index": self.index, "env": jsonable(env)})
            except Exception as e:
                emit({"event": "envelope-decode-failed", "hub_index": self.index, "error": str(e)})
            return _orig(data)

        self.hub._on_packet = wrapped

    def emit_welcome(self):
        hub = self.hub
        caps = {}
        for k, v in dict(hub.hub_caps).items():
            caps[str(k)] = bool(v)
        limits = {
            "max_nick_bytes": hub.max_nick_bytes,
            "max_room_name_bytes": hub.max_room_name_bytes,
            "max_msg_body_bytes": hub.max_msg_body_bytes,
            "max_rooms_per_session": hub.max_rooms_per_session,
            "rate_limit_msgs_per_minute": hub.rate_limit_msgs_per_minute,
        }
        emit({"event": "welcome", "hub_index": self.index, "hub_name": hub.hub_name,
              "version": hub.hub_version, "caps": caps, "limits": limits})

clients = []

def wait_welcomed(client, timeout=30.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if client.hub.welcomed:
            client.emit_welcome()
            return True
        time.sleep(0.1)
    emit({"event": "welcome-timeout", "hub_index": client.index})
    return False

def run_commands():
    with open(command_file, "r") as f:
        lines = [line.strip() for line in f if line.strip() and not line.startswith("#")]
    for line in lines:
        try:
            fields = line.split(" ")
            op = fields[0]
            if op == "connect":
                client = Client(len(clients), fields[1])
                clients.append(client)
                client.hub.connect()
                if wait_welcomed(client):
                    emit({"event": "connected", "hub_index": client.index, "nick": fields[1]})
            elif op == "join":
                index = int(fields[1])
                room = fields[2]
                key = fields[3] if len(fields) > 3 else None
                clients[index].hub.join_room(room, key=key)
                emit({"event": "join-sent", "hub_index": index, "room": room})
            elif op == "msg":
                index = int(fields[1])
                room = fields[2]
                text = line[len("msg ") + len(fields[1]) + 1 + len(room) + 1:]
                clients[index].hub.send_message(room, text)
                emit({"event": "msg-sent", "hub_index": index, "room": room})
            elif op == "part":
                index = int(fields[1])
                room = fields[2]
                clients[index].hub.part_room(room)
                emit({"event": "part-sent", "hub_index": index, "room": room})
            elif op == "cmd":
                index = int(fields[1])
                room = None if fields[2] == "-" else fields[2]
                prefix_len = len("cmd") + 1 + len(fields[1]) + 1 + len(fields[2]) + 1
                text = line[prefix_len:]
                clients[index].hub.send_command(text, room=room)
                emit({"event": "cmd-sent", "hub_index": index, "room": room, "text": text})
            elif op == "sleep":
                seconds = float(fields[1])
                time.sleep(seconds)
                emit({"event": "slept", "seconds": seconds})
            elif op == "wait":
                emit({"event": "marker", "name": fields[1]})
            else:
                emit({"event": "unknown-op", "op": op})
        except Exception as e:
            emit({"event": "op-error", "line": line, "error": str(e)})

emit({"event": "ready"})
run_commands()

# Drain for a moment so trailing hub traffic lands in the events file.
time.sleep(float(os.environ.get("GORRCD_DRIVER_DRAIN", "2")))
emit({"event": "driver-done"})

# Keep the process alive until stdin closes so gorrcd's respawn does not
# spawn a fresh driver mid-test.
while True:
    time.sleep(1)
`

// testEvent is one decoded JSONL event from the driver.
type testEvent map[string]any

// eventReader polls the driver's JSONL events file.
type eventReader struct {
	mu   sync.Mutex
	path string
	n    int
}

func newEventReader(path string) *eventReader {
	return &eventReader{path: path}
}

// poll re-reads the events file, tracking the line count.
func (er *eventReader) poll() {
	data, err := os.ReadFile(er.path)
	if err != nil {
		return
	}
	er.mu.Lock()
	defer er.mu.Unlock()
	er.n = strings.Count(string(data), "\n")
}

// lines returns every non-empty event line decoded so far.
func (er *eventReader) lines() []string {
	er.poll()
	data, err := os.ReadFile(er.path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// all returns every event decoded so far.
func (er *eventReader) all() []testEvent {
	var out []testEvent
	for _, line := range er.lines() {
		var ev testEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// find returns the first event with the given event name (and, when
// hubIndex >= 0, matching hub_index).
func (er *eventReader) find(name string, hubIndex int) (testEvent, bool) {
	for _, ev := range er.all() {
		if ev["event"] != name {
			continue
		}
		if hubIndex >= 0 {
			idx, ok := ev["hub_index"].(float64)
			if !ok || int(idx) != hubIndex {
				continue
			}
		}
		return ev, true
	}
	return nil, false
}

// waitFor polls until an event matching name/hubIndex appears or the
// deadline passes.
func (er *eventReader) waitFor(t *testing.T, name string, hubIndex int, timeout time.Duration) testEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ev, ok := er.find(name, hubIndex); ok {
			return ev
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event %q; events so far:\n%v", name, er.dump())
	return nil
}

// envelopes returns all received envelope events for a hub.
func (er *eventReader) envelopes(hubIndex int) []testEvent {
	var out []testEvent
	for _, ev := range er.all() {
		if ev["event"] != "envelope" {
			continue
		}
		if idx, ok := ev["hub_index"].(float64); !ok || int(idx) != hubIndex {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// dump renders the events read so far for failure reports.
func (er *eventReader) dump() string {
	data, err := os.ReadFile(er.path)
	if err != nil {
		return "(no events file)"
	}
	return string(data)
}

// findRNSPython returns a Python interpreter that can import RNS and
// nomadnet.RRC (python3.14 first on this host); the test skips cleanly
// when none is available.
func findRNSPython(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"python3.14", "python3"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		check := exec.Command(path, "-c", "import RNS, nomadnet.RRC")
		if err := check.Run(); err == nil {
			return path
		}
	}
	t.Skip("no python interpreter with RNS + nomadnet.RRC available; skipping cross-implementation test")
	return ""
}

// buildGorrcdBinary compiles the gorrcd binary into a temp dir.
func buildGorrcdBinary(t *testing.T) string {
	t.Helper()
	dir := testutils.TempDir(t, "gorrcd-bin")
	bin := filepath.Join(dir, "gorrcd")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../cmd/gorrcd"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build gorrcd failed: %v\n%v", err, out)
	}
	return bin
}

// writeDriverRNSConfig writes the Python driver's RNS config: standalone
// with no interfaces (the stdio bridge is registered manually).
func writeDriverRNSConfig(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "[reticulum]\nshare_instance = No\n\n[logging]\nloglevel = 4\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gorrcdHub bundles one running gorrcd hub with its helper.
type gorrcdHub struct {
	binary    string
	homeDir   string
	rnsDir    string
	identity  *rns.Identity
	events    *eventReader
	cmd       *exec.Cmd
	eventPath string
	hubIndex  int
}

// hubTestConfig holds the hub config knobs a test wants to override.
type hubTestConfig struct {
	rateLimitMsgsPerMinute *int
	maxMsgBodyBytes        *int
	enableResourceTransfer *bool
	greeting               *string
	bannedIdentities       []string
	trustedIdentities      []string
	announcePeriodS        float64
}

// startGorrcdHub builds the binary, writes the RNS + rrcd configs, and
// starts the hub with the PipeInterface spawning the Python driver.
func startGorrcdHub(t *testing.T, pyPath string, cfg hubTestConfig) *gorrcdHub {
	t.Helper()

	binary := buildGorrcdBinary(t)
	homeDir := testutils.TempDir(t, "gorrcd-home")
	if debugHome := os.Getenv("GORRCD_INT_DEBUG_HOME"); debugHome != "" {
		homeDir = debugHome
		_ = os.RemoveAll(debugHome)
		if err := os.MkdirAll(debugHome, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	rnsDir := filepath.Join(homeDir, "rns")
	if err := os.MkdirAll(rnsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	driverPath := filepath.Join(homeDir, "gorrcd_driver.py")
	if err := os.WriteFile(driverPath, []byte(pythonDriverScript), 0o644); err != nil {
		t.Fatal(err)
	}
	// The driver's RNS config must NOT contain the pipe interface (it
	// registers its stdio bridge manually); a shared config would make
	// the driver spawn PipeInterfaces recursively.
	driverRnsDir := filepath.Join(homeDir, "driver-rns")
	writeDriverRNSConfig(t, driverRnsDir)
	eventPath := filepath.Join(homeDir, "events.jsonl")
	cmdPath := filepath.Join(homeDir, "commands.txt")
	storagePath := filepath.Join(homeDir, "storage")
	if err := os.WriteFile(cmdPath, []byte("# no commands yet\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := fmt.Sprintf("%v %v %v %v %v %v",
		pyPath, driverPath, driverRnsDir, eventPath, cmdPath, storagePath)
	rnsConfig := "[reticulum]\nshare_instance = No\n\n[interfaces]\n\n  [[python client]]\n" +
		"    type = PipeInterface\n" +
		"    interface_enabled = True\n" +
		"    command = " + command + "\n" +
		"    respawn_delay = 1\n"
	if err := os.WriteFile(filepath.Join(rnsDir, "config"), []byte(rnsConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	identity, err := rns.NewIdentity(true, silentRNSLogger())
	if err != nil {
		t.Fatal(err)
	}
	identityPath := filepath.Join(homeDir, "hub_identity")
	if err := identity.ToFile(identityPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(identityPath, 0o600); err != nil {
		t.Fatal(err)
	}

	hub := &gorrcdHub{
		binary:    binary,
		homeDir:   homeDir,
		rnsDir:    rnsDir,
		identity:  identity,
		eventPath: eventPath,
	}
	hub.events = newEventReader(eventPath)

	hub.writeHubConfig(t, cfg, rnsDir, identityPath)
	hub.start(t)
	t.Cleanup(hub.stop)
	return hub
}

// writeHubConfig writes the hub's rrcd.toml with the test's knobs.
func (g *gorrcdHub) writeHubConfig(t *testing.T, cfg hubTestConfig, rnsDir, identityPath string) {
	t.Helper()
	config := strings.Join([]string{
		"[hub]",
		"configdir = '" + rnsDir + "'",
		"identity_path = '" + identityPath + "'",
		"room_registry_path = '" + filepath.Join(g.homeDir, "rooms.toml") + "'",
		"announce_on_start = true",
		fmt.Sprintf("announce_period_s = %v", cfg.announcePeriodS),
		"hub_name = 'TestHub'",
		"room_registry_prune_after_s = 2592000",
		"room_registry_prune_interval_s = 3600.0",
		"room_invite_timeout_s = 900.0",
		"include_joined_member_list = false",
		"max_nick_bytes = 32",
		"max_room_name_bytes = 64",
		"max_msg_body_bytes = 350",
		"max_rooms_per_session = 32",
		"rate_limit_msgs_per_minute = 240",
		"ping_interval_s = 0.0",
		"ping_timeout_s = 0.0",
		"enable_resource_transfer = true",
		"max_resource_bytes = 262144",
		"max_pending_resource_expectations = 8",
		"resource_expectation_ttl_s = 30.0",
		"",
		"[logging]",
		"level = 'ERROR'",
		"console = true",
		"",
	}, "\n")
	if cfg.rateLimitMsgsPerMinute != nil {
		config = strings.Replace(config, "rate_limit_msgs_per_minute = 240",
			fmt.Sprintf("rate_limit_msgs_per_minute = %v", *cfg.rateLimitMsgsPerMinute), 1)
	}
	if cfg.maxMsgBodyBytes != nil {
		config = strings.Replace(config, "max_msg_body_bytes = 350",
			fmt.Sprintf("max_msg_body_bytes = %v", *cfg.maxMsgBodyBytes), 1)
	}
	if cfg.enableResourceTransfer != nil {
		config = strings.Replace(config, "enable_resource_transfer = true",
			fmt.Sprintf("enable_resource_transfer = %v", *cfg.enableResourceTransfer), 1)
	}
	if cfg.greeting != nil {
		config = strings.Replace(config, "hub_name = 'TestHub'",
			"hub_name = 'TestHub'\ngreeting = "+pythonReprString(*cfg.greeting), 1)
	}
	if len(cfg.trustedIdentities) > 0 {
		quoted := make([]string, 0, len(cfg.trustedIdentities))
		for _, id := range cfg.trustedIdentities {
			quoted = append(quoted, "'"+id+"'")
		}
		config = strings.Replace(config, "trusted_identities = []",
			"trusted_identities = ["+strings.Join(quoted, ", ")+"]", 1)
	}
	if len(cfg.bannedIdentities) > 0 {
		quoted := make([]string, 0, len(cfg.bannedIdentities))
		for _, id := range cfg.bannedIdentities {
			quoted = append(quoted, "'"+id+"'")
		}
		config = strings.Replace(config, "banned_identities = []",
			"banned_identities = ["+strings.Join(quoted, ", ")+"]", 1)
	}
	configPath := filepath.Join(g.homeDir, "rrcd.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

// start launches the gorrcd binary with the hub config.
func (g *gorrcdHub) start(t *testing.T) {
	t.Helper()
	cmd := exec.Command(g.binary, "--config", filepath.Join(g.homeDir, "rrcd.toml"))
	cmd.Env = append(os.Environ(), "RRCD_HOME="+g.homeDir)
	g.cmd = cmd
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gorrcd: %v", err)
	}
	// Capture the hub's stdout into a file for failure diagnostics.
	logPath := filepath.Join(g.homeDir, "gorrcd.log")
	go func() {
		f, err := os.Create(logPath)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			_, _ = f.WriteString(scanner.Text() + "\n")
		}
	}()
}

// stop kills the hub process; its PipeInterface child dies with it.
func (g *gorrcdHub) stop() {
	if g.cmd == nil || g.cmd.Process == nil {
		return
	}
	_ = g.cmd.Process.Kill()
	_, _ = g.cmd.Process.Wait()
}

// writeCommands replaces the driver's command file.
func (g *gorrcdHub) writeCommands(lines ...string) {
	path := filepath.Join(g.homeDir, "commands.txt")
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// silentRNSLogger builds a silent RNS logger.
func silentRNSLogger() *rns.Logger {
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	return logger
}

// eventBytes converts an event field carrying {"__bytes__": hex} to raw
// bytes.
func eventBytes(t *testing.T, ev testEvent, key string) []byte {
	t.Helper()
	raw, ok := ev[key].(map[string]any)
	if !ok {
		t.Fatalf("event field %q is not a bytes object: %v", key, ev)
	}
	hexStr, _ := raw["__bytes__"].(string)
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("event field %q is not hex: %v", key, err)
	}
	return data
}

// G13.2 The gorrcd binary builds; a --help run needs no Python.
func TestIntegrationGorrcdBinaryBuilds(t *testing.T) {
	binary := buildGorrcdBinary(t)
	if info, err := os.Stat(binary); err != nil || info.Size() == 0 {
		t.Fatalf("gorrcd binary missing: %v %v", info, err)
	}
}

// G13.1 + G13.3 The real Python client connects through the PipeInterface,
// sends HELLO, and parses the hub's WELCOME: the hub name, version string,
// caps map, and limits map all match the expected values.
func TestIntegrationHelloWelcomeOverPipe(t *testing.T) {
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{announcePeriodS: 1.0})
	defer hub.stop()

	// The driver learns the hub hash from the hub's announce arriving
	// through the pipe.
	ev := hub.events.waitFor(t, "welcome", 0, 90*time.Second)
	hubName, _ := ev["hub_name"].(string)
	version, _ := ev["version"].(string)
	if hubName != "TestHub" {
		t.Errorf("parsed WELCOME hub_name = %q, want %q", hubName, "TestHub")
	}
	if version != "0.1.0" {
		t.Errorf("parsed WELCOME version = %q, want %q (the Go hub version constant)", version, "0.1.0")
	}
	caps, _ := ev["caps"].(map[string]any)
	if caps["1"] != true || caps["2"] != true || caps["0"] != true {
		t.Errorf("parsed WELCOME caps = %v, want {1:true, 2:true, 0:true}", caps)
	}
	limits, _ := ev["limits"].(map[string]any)
	wantLimits := map[string]float64{
		"max_nick_bytes": 32, "max_room_name_bytes": 64, "max_msg_body_bytes": 350,
		"max_rooms_per_session": 32, "rate_limit_msgs_per_minute": 240,
	}
	for key, want := range wantLimits {
		if got, _ := limits[key].(float64); got != want {
			t.Errorf("parsed WELCOME limits[%v] = %v, want %v", key, got, want)
		}
	}

	// The announce app_data carries the proto/v/hub keys in the Python
	// order with the hub name.
	hashEv, ok := hub.events.find("hub-hash", -1)
	if !ok {
		t.Fatalf("no hub-hash event; events:\n%v", hub.events.dump())
	}
	appData := eventBytes(t, hashEv, "app_data_hex")
	// The golden map prefix is proto, v, hub with the lowercased hub
	// name ("testhub"); the full golden hex for hub "testhub" was
	// captured from python-cbor2.
	if got := hex.EncodeToString(appData); got != "a36570726f746f63727263617601636875626374657374487562" {
		t.Errorf("announce app_data = %v, want the proto/v/hub map for testhub", got)
	}
}

// pythonReprString renders a string the way Python's repr would quote it
// inside the config template.
func pythonReprString(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = "\""
	}
	var sb strings.Builder
	sb.WriteString(quote)
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteString(quote)
	return sb.String()
}
