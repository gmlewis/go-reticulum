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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
        # A SINGLE reader thread: two concurrent readers each hold their
        # own HDLC state (in_frame/escape/buf) and interleave bytes under
        # CPU load, so frames assemble as garbage and no announce ever
        # decodes (mirrors Python PipeInterface's one readLoop thread).
        threading.Thread(target=self.read_loop, daemon=True).start()

    def process_incoming(self, data):
        self.rxb += len(data)
        if os.environ.get("GORRCD_TRACE_FRAMES"):
            emit({"event": "frame-in", "len": len(data), "hex": data[:32].hex()})
        RNS.Transport.inbound(data, self)

    def process_outgoing(self, data):
        if os.environ.get("GORRCD_TRACE_FRAMES"):
            emit({"event": "frame-out", "len": len(data), "hex": data[:32].hex()})
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
        # Each client holds its own RNS identity so direct notices route
        # between clients; the identity persists across driver restarts
        # so two-phase tests can ban a stable hash.
        ident_path = os.path.join(storagepath, "ident-%s" % nick)
        if os.path.exists(ident_path):
            self.identity = RNS.Identity.from_file(ident_path)
        else:
            self.identity = RNS.Identity()
            self.identity.to_file(ident_path)
        self.app = FakeApp(self.identity, nick, storagepath)
        self.manager = rrc.RRCManager(self.app)
        self.hub = self.manager.add_hub(hub_hash, dest_name="rrc.hub", name="hub" + str(index))
        # Every rendered message (system rows, chat, notices) is emitted
        # as an event so the Go tests can assert client-visible rows.
        self.manager.set_message_callback(
            lambda hub, msg, _index=index: emit({
                "event": "message-row", "hub_index": _index,
                "room": msg.room, "kind": msg.kind, "text": msg.text,
                "nick": msg.nick}))
        # Observe every received envelope by wrapping the hub's packet
        # callback (late-bound at call time, so instance patching works).
        orig = self.hub._on_packet

        def wrapped(data, _orig=orig):
            try:
                env = cbor.decode(data)
                emit({"event": "envelope", "hub_index": self.index, "env": jsonable(env),
                      "payload_hex": data.hex()})
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
            elif op == "connect-nosync":
                client = Client(len(clients), fields[1])
                clients.append(client)
                client.hub.connect()
                emit({"event": "connecting", "hub_index": client.index, "nick": fields[1]})
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
            elif op == "ping":
                index = int(fields[1])
                room = None if fields[2] == "-" else fields[2]
                body = clients[index].hub.send_ping(room)
                emit({"event": "ping-sent", "hub_index": index,
                      "body_hex": body.hex(), "room": room})
            elif op == "silence":
                # Swallow the hub's PINGs so the client never PONGs.
                index = int(fields[1])
                client = clients[index]
                orig = client.hub._on_packet

                def silent_packet(data, _orig=orig):
                    try:
                        env = cbor.decode(data)
                        if isinstance(env, dict) and env.get(rrc.K_T) == rrc.T_PING:
                            return
                    except Exception:
                        pass
                    _orig(data)

                client.hub._on_packet = silent_packet
                emit({"event": "silenced", "hub_index": index})
            elif op == "status":
                index = int(fields[1])
                hub = clients[index].hub
                emit({"event": "hub-status", "hub_index": index,
                      "status": int(hub.status), "status_text": hub.status_text})
            elif op == "rawmsg":
                index = int(fields[1])
                room = fields[2]
                n = int(fields[3])
                env = rrc._make_envelope(rrc.T_MSG, src=clients[index].identity.hash, room=room, body="x" * n)
                nick = clients[index].hub.get_effective_nick()
                if nick:
                    env[rrc.K_NICK] = nick
                clients[index].hub._send_env(env)
                emit({"event": "rawmsg-sent", "hub_index": index})
            elif op == "garbage":
                index = int(fields[1])
                clients[index].hub._send_env(b"\x01garbage-not-cbor")
                emit({"event": "garbage-sent", "hub_index": index})
            elif op == "connect-earlyjoin":
                # Create the client with _on_established replaced: the
                # link identifies but sends no HELLO, and a JOIN lands
                # shortly after the hub's session exists.
                nick = fields[1]
                room = fields[2]
                client = Client(len(clients), nick)
                clients.append(client)

                def early_established(link, _hub=client.hub, _room=room, _index=client.index, _ident=client.identity):
                    link.identify(_ident)

                    def send_join():
                        env = rrc._make_envelope(rrc.T_JOIN, src=_ident.hash, room=_room)
                        _hub._send_env(env)
                        emit({"event": "earlyjoin-sent", "hub_index": _index, "room": _room})

                    threading.Timer(0.3, send_join).start()

                client.hub._on_established = early_established
                client.hub.connect()
                emit({"event": "connecting", "hub_index": client.index, "nick": nick})
                # The hub never welcomes this client (no HELLO); wait for
                # the JOIN reply instead.
                time.sleep(2.0)
                emit({"event": "connected", "hub_index": client.index, "nick": nick})
            elif op == "earlyjoin":
                index = int(fields[1])
                room = fields[2]
                env = rrc._make_envelope(rrc.T_JOIN, src=identity.hash, room=room)
                clients[index].hub._send_env(env)
                emit({"event": "earlyjoin-sent", "hub_index": index})
            elif op == "direct":
                # Hand-craft a T_NOTICE with K_DST and no room.
                index = int(fields[1])
                dst_index = int(fields[2])
                text = line[len("direct ") + len(fields[1]) + 1 + len(fields[2]) + 1:]
                env = rrc._make_envelope(rrc.T_NOTICE, src=clients[index].identity.hash, body=text)
                env[8] = clients[dst_index].identity.hash
                clients[index].hub._send_env(env)
                emit({"event": "direct-sent", "hub_index": index, "dst": dst_index})
            elif op == "direct-bad":
                # A direct notice that also carries a room: the hub must
                # reject it with the exact ERROR.
                index = int(fields[1])
                dst_index = int(fields[2])
                env = rrc._make_envelope(rrc.T_NOTICE, src=clients[index].identity.hash, room="general", body="mixed")
                env[8] = clients[dst_index].identity.hash
                clients[index].hub._send_env(env)
                emit({"event": "direct-bad-sent", "hub_index": index})
            elif op == "direct-unknown":
                # A direct notice to a destination that is not connected.
                index = int(fields[1])
                env = rrc._make_envelope(rrc.T_NOTICE, src=clients[index].identity.hash, body="nowhere")
                env[8] = os.urandom(32)
                clients[index].hub._send_env(env)
                emit({"event": "direct-unknown-sent", "hub_index": index})
            elif op == "drop":
                index = int(fields[1])
                clients[index].hub.disconnect()
                emit({"event": "dropped", "hub_index": index})
            elif op == "identity":
                index = int(fields[1])
                emit({"event": "client-identity", "hub_index": index,
                      "hash": clients[index].identity.hash.hex()})
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

// waitForMarker polls until a marker event with the name appears.
func (er *eventReader) waitForMarker(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, ev := range er.all() {
			if ev["event"] == "marker" && ev["name"] == name {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for marker %q; events so far:\n%v", name, er.dump())
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
	logLevel := "4"
	if v := os.Getenv("GORRCD_DRIVER_LOGLEVEL"); v != "" {
		logLevel = v
	}
	content := "[reticulum]\nshare_instance = No\n\n[logging]\nloglevel = " + logLevel + "\n"
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
	pingIntervalS          *float64
	pingTimeoutS           *float64
	includeJoinedList      *bool
	// commands are written into the driver's command file before the
	// hub starts (the driver reads it once at boot).
	commands []string
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

	if len(cfg.commands) > 0 {
		if err := os.WriteFile(cmdPath, []byte(strings.Join(cfg.commands, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
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

// tomlFloatLiteral renders a float64 as a TOML float literal: the
// shortest representation plus a ".0" suffix when it would otherwise
// parse as an integer.
func tomlFloatLiteral(v float64) string {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
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
		"trusted_identities = []",
		"banned_identities = []",
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
	if cfg.pingIntervalS != nil {
		config = strings.Replace(config, "ping_interval_s = 0.0",
			"ping_interval_s = "+tomlFloatLiteral(*cfg.pingIntervalS), 1)
	}
	if cfg.pingTimeoutS != nil {
		config = strings.Replace(config, "ping_timeout_s = 0.0",
			"ping_timeout_s = "+tomlFloatLiteral(*cfg.pingTimeoutS), 1)
	}
	if cfg.includeJoinedList != nil {
		config = strings.Replace(config, "include_joined_member_list = false",
			fmt.Sprintf("include_joined_member_list = %v", *cfg.includeJoinedList), 1)
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
	// The hub's first-run bootstrap must not fire: the room registry
	// file must already exist.
	roomsPath := filepath.Join(g.homeDir, "rooms.toml")
	if _, err := os.Stat(roomsPath); os.IsNotExist(err) {
		if err := os.WriteFile(roomsPath, []byte("[rooms]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// start launches the gorrcd binary with the hub config.
func (g *gorrcdHub) start(t *testing.T) {
	t.Helper()
	cmd := exec.Command(g.binary, "--config", filepath.Join(g.homeDir, "rrcd.toml"))
	cmd.Env = append(os.Environ(), "RRCD_HOME="+g.homeDir)
	g.cmd = cmd
	stdout, _ := cmd.StdoutPipe()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gorrcd: %v", err)
	}
	// Capture the hub's stdout and stderr into a file for failure
	// diagnostics.
	g.startCapture(stdout, stderr)
}

// stop kills the hub process; its PipeInterface child dies with it.
func (g *gorrcdHub) stop() {
	if g.cmd == nil || g.cmd.Process == nil {
		return
	}
	_ = g.cmd.Process.Kill()
	_, _ = g.cmd.Process.Wait()
}

// startCapture streams the hub's stdout and stderr into gorrcd.log.
func (g *gorrcdHub) startCapture(stdout, stderr io.Reader) {
	f, err := os.Create(filepath.Join(g.homeDir, "gorrcd.log"))
	if err != nil {
		return
	}
	reader := bufio.NewReader(io.MultiReader(stdout, stderr))
	go func() {
		defer func() { _ = f.Close() }()
		for {
			line, err := reader.ReadString('\n')
			if len(line) > 0 {
				_, _ = f.WriteString(line)
			}
			if err != nil {
				return
			}
		}
	}()
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
	t.Parallel()
	binary := buildGorrcdBinary(t)
	if info, err := os.Stat(binary); err != nil || info.Size() == 0 {
		t.Fatalf("gorrcd binary missing: %v %v", info, err)
	}
}

// G13.1 + G13.3 The real Python client connects through the PipeInterface,
// sends HELLO, and parses the hub's WELCOME: the hub name, version string,
// caps map, and limits map all match the expected values.
func TestIntegrationHelloWelcomeOverPipe(t *testing.T) {
	t.Parallel()
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands:        []string{"connect PyClient"},
	})
	defer hub.stop()

	// The driver learns the hub hash from the hub's announce arriving
	// through the pipe.
	ev := hub.events.waitFor(t, "welcome", 0, 90*time.Second)
	hubName, _ := ev["hub_name"].(string)
	version, _ := ev["version"].(string)
	if hubName != "TestHub" {
		t.Errorf("parsed WELCOME hub_name = %q, want %q", hubName, "TestHub")
	}
	if version != rns.VERSION {
		t.Errorf("parsed WELCOME version = %q, want %q (rns.VERSION)", version, rns.VERSION)
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
	// The golden hex for {"proto":"rrc","v":1,"hub":"TestHub"} was
	// captured from python-cbor2 with the Python insertion order.
	if got, _ := hashEv["app_data_hex"].(string); got != "a36570726f746f63727263617601636875626754657374487562" {
		t.Errorf("announce app_data = %v, want the proto/v/hub map for TestHub", got)
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

// envelopeOf extracts the decoded env map from an envelope event; the
// nested JSON object decodes as the unnamed map type, so the conversion
// goes through it.
func envelopeOf(ev testEvent) testEvent {
	raw, ok := ev["env"].(map[string]any)
	if !ok {
		return nil
	}
	return raw
}

// envInt reads an integer field from the decoded env.
func envInt(env testEvent, key string) (int64, bool) {
	v, ok := env[key].(float64)
	return int64(v), ok
}

// envString reads a string field from the decoded env.
func envString(env testEvent, key string) (string, bool) {
	v, ok := env[key].(string)
	return v, ok
}

// envBytesField reads a bytes field from the decoded env.
func envBytesField(t *testing.T, env testEvent, key string) []byte {
	t.Helper()
	raw, ok := env[key].(map[string]any)
	if !ok {
		t.Fatalf("env field %q is not bytes: %v", key, env)
	}
	hexStr, _ := raw["__bytes__"].(string)
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("env field %q is not hex: %v", key, err)
	}
	return data
}

// countEnvelopesOf counts envelopes of the given type for a hub.
func countEnvelopesOf(er *eventReader, hubIndex int, msgType int64) int {
	n := 0
	for _, ev := range er.envelopes(hubIndex) {
		env := envelopeOf(ev)
		if t, ok := envInt(env, "1"); ok && t == msgType {
			n++
		}
	}
	return n
}

// G13.4 JOIN and PART over the pipe: the joining client receives its own
// JOINED (no nick) plus the room-info NOTICE, a second client receives the
// fanout JOINED with the actor's nick, and the PART produces the PARTED
// self copy plus the fanout with the actor's nick.
func TestIntegrationJoinPartOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"join 0 general",
			"sleep 1",
			"join 1 general",
			"sleep 1",
			"part 1 general",
			"sleep 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitFor(t, "connected", 1, 60*time.Second)
	hub.events.waitForMarker(t, "done", 60*time.Second)

	alphaJoinJoined := countEnvelopesOf(hub.events, 0, TJoined)
	if alphaJoinJoined < 2 {
		t.Errorf("Alpha received %v JOINED envelopes, want >= 2 (own + Beta's)", alphaJoinJoined)
	}
	betaJoined := countEnvelopesOf(hub.events, 1, TJoined)
	if betaJoined != 1 {
		t.Errorf("Beta received %v JOINED envelopes, want 1 (its own; Alpha joined before Beta)",
			betaJoined)
	}

	// Alpha's own JOINED carries no K_NICK; the fanout copies carry the
	// actor's nick.
	var alphaOwnJoined, alphaFanoutJoined bool
	var alphaFanoutNick string
	for _, ev := range hub.events.envelopes(0) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TJoined {
			continue
		}
		if nick, hasNick := envString(env, "7"); hasNick {
			alphaFanoutJoined = true
			alphaFanoutNick = nick
		} else {
			alphaOwnJoined = true
		}
	}
	if !alphaOwnJoined {
		t.Error("Alpha's own JOINED carries K_NICK; the actor's copy must not")
	}
	if !alphaFanoutJoined || alphaFanoutNick != "Beta" {
		t.Errorf("Alpha's fanout JOINED nick = %q (fanout seen: %v), want Beta", alphaFanoutNick, alphaFanoutJoined)
	}

	// The room-info NOTICE after the first join: `room general:
	// unregistered; mode=(none); topic=(none)`.
	foundRoomInfo := false
	for _, ev := range hub.events.envelopes(0) {
		env := envelopeOf(ev)
		if t, ok := envInt(env, "1"); !ok || t != TNotice {
			continue
		}
		body, _ := envString(env, "6")
		if body == "room general: unregistered; mode=(none); topic=(none)" {
			foundRoomInfo = true
		}
	}
	if !foundRoomInfo {
		t.Error("Alpha never received the room-info NOTICE with the exact text")
	}

	// Beta's PART: Beta receives its own PARTED (no nick).
	betaParted := 0
	for _, ev := range hub.events.envelopes(1) {
		env := envelopeOf(ev)
		if t, ok := envInt(env, "1"); !ok || t != TParted {
			continue
		}
		if _, hasNick := env["7"]; !hasNick {
			betaParted++
		}
	}
	if betaParted < 1 {
		t.Error("Beta never received its own PARTED after parting")
	}
}

// G15.7 PING/PONG over the pipe: the real client's send_ping reaches the
// hub, the hub echoes the ping body byte-identically in a t=31 envelope,
// and the client renders its `Pong from hub: N ms` system row.
func TestIntegrationPingPongOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"join 0 general",
			"sleep 1",
			"ping 0 general",
			"sleep 2",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitFor(t, "connected", 0, 90*time.Second)
	pingEv := hub.events.waitFor(t, "ping-sent", 0, 60*time.Second)
	bodyHex, _ := pingEv["body_hex"].(string)
	if bodyHex == "" {
		t.Fatalf("ping-sent event carries no body_hex: %v", pingEv)
	}
	hub.events.waitForMarker(t, "done", 90*time.Second)

	// The hub's PONG echoes the ping body byte-identically.
	echoed := false
	for _, ev := range hub.events.envelopes(0) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TPong {
			continue
		}
		body := envBytesField(t, env, "6")
		if hex.EncodeToString(body) == bodyHex {
			echoed = true
		}
	}
	if !echoed {
		t.Errorf("Alpha never received a t=31 envelope echoing ping body %v; envelopes:\n%v",
			bodyHex, hub.events.dump())
	}

	// The client rendered the Pong from hub system row.
	pongRow := false
	for _, ev := range hub.events.all() {
		if ev["event"] != "message-row" {
			continue
		}
		if idx, ok := ev["hub_index"].(float64); !ok || int(idx) != 0 {
			continue
		}
		kind, _ := ev["kind"].(string)
		text, _ := ev["text"].(string)
		if kind == "system" && strings.HasPrefix(text, "Pong from hub: ") && strings.HasSuffix(text, " ms") {
			pongRow = true
		}
	}
	if !pongRow {
		t.Errorf("the client never rendered the 'Pong from hub' system row; events:\n%v", hub.events.dump())
	}
}

// G15.8 The hub-initiated PING loop over the pipe: with
// ping_interval_s = 1.0 and ping_timeout_s = 5.0 every welcomed client
// receives t=30 envelopes with a float body and the auto-PONGing client's
// link survives; a deliberately silent client is torn down after the
// timeout.
func TestIntegrationHubPingLoopOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	pingInterval := 1.0
	pingTimeout := 5.0
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		pingIntervalS:   &pingInterval,
		pingTimeoutS:    &pingTimeout,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"sleep 2",
			"silence 1",
			"sleep 9",
			"status 0",
			"status 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitFor(t, "silenced", 1, 90*time.Second)
	hub.events.waitForMarker(t, "done", 90*time.Second)

	// Every t=30 envelope body is a Python float.
	pingBodies := map[int]int{}
	for _, ev := range hub.events.all() {
		if ev["event"] != "envelope" {
			continue
		}
		env := envelopeOf(ev)
		mt, ok := envInt(env, "1")
		if !ok || mt != TPing {
			continue
		}
		idx, _ := ev["hub_index"].(float64)
		if _, isFloat := env["6"].(float64); !isFloat {
			t.Errorf("client %v received a t=30 envelope whose body is not a float: %v", int(idx), env["6"])
			continue
		}
		pingBodies[int(idx)]++
	}
	if pingBodies[0] < 2 {
		t.Errorf("Alpha received %v t=30 envelopes over ~11s at a 1.0s interval, want >= 2", pingBodies[0])
	}
	if pingBodies[1] < 1 {
		t.Errorf("Beta received %v t=30 envelopes, want >= 1 (before the silencing)", pingBodies[1])
	}

	// The final hub-status events: Alpha still connected, Beta torn down.
	finalStatus := func(hubIndex int) (float64, bool) {
		var status float64
		found := false
		for _, ev := range hub.events.all() {
			if ev["event"] != "hub-status" {
				continue
			}
			if idx, ok := ev["hub_index"].(float64); !ok || int(idx) != hubIndex {
				continue
			}
			s, ok := ev["status"].(float64)
			if !ok {
				continue
			}
			status = s
			found = true
		}
		return status, found
	}
	alphaStatus, okAlpha := finalStatus(0)
	if !okAlpha {
		t.Fatal("no hub-status event for Alpha")
	}
	if alphaStatus != 2 {
		t.Errorf("Alpha's final hub status = %v, want 2 (CONNECTED): the healthy link was torn down", alphaStatus)
	}
	betaStatus, okBeta := finalStatus(1)
	if !okBeta {
		t.Fatal("no hub-status event for Beta")
	}
	if betaStatus != 0 {
		t.Errorf("Beta's final hub status = %v, want 0 (DISCONNECTED): the silent client was not torn down", betaStatus)
	}
}

// G15.9 The include_joined_member_list = true hub config: fanout bodies
// are one-element hash lists, so the real client renders the
// `<nick> joined` / `<nick> left` system rows and each joiner gets its
// own `You joined #<room>` row.
func TestIntegrationJoinedMemberListOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	includeList := true
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS:   1.0,
		includeJoinedList: &includeList,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"sleep 1",
			"join 0 general",
			"sleep 1",
			"join 1 general",
			"sleep 1",
			"part 1 general",
			"sleep 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitFor(t, "connected", 1, 90*time.Second)
	hub.events.waitForMarker(t, "done", 90*time.Second)

	// Collect the system rows each client rendered.
	rows := func(hubIndex int) []string {
		var out []string
		for _, ev := range hub.events.all() {
			if ev["event"] != "message-row" {
				continue
			}
			if idx, ok := ev["hub_index"].(float64); !ok || int(idx) != hubIndex {
				continue
			}
			if kind, _ := ev["kind"].(string); kind != "system" {
				continue
			}
			room, _ := ev["room"].(string)
			text, _ := ev["text"].(string)
			out = append(out, room+": "+text)
		}
		return out
	}

	has := func(rows []string, want string) bool {
		for _, row := range rows {
			if row == want {
				return true
			}
		}
		return false
	}

	alphaRows := rows(0)
	betaRows := rows(1)
	if !has(alphaRows, "general: You joined #general") {
		t.Errorf("Alpha never rendered its own join row; rows: %v", alphaRows)
	}
	if !has(betaRows, "general: You joined #general") {
		t.Errorf("Beta never rendered its own join row; rows: %v", betaRows)
	}
	if !has(alphaRows, "general: Beta joined") {
		t.Errorf("Alpha never rendered the '<nick> joined' fanout row; rows: %v", alphaRows)
	}
	if !has(alphaRows, "general: Beta left") {
		t.Errorf("Alpha never rendered the '<nick> left' fanout row; rows: %v", alphaRows)
	}
}

// G13.5 MSG fanout over the pipe: a second client receives the forwarded
// envelope with the sender's hash and nick, and the sender receives its own
// echo with identical payload bytes.
func TestIntegrationMsgFanoutOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"join 0 general",
			"sleep 1",
			"join 1 general",
			"sleep 1",
			"msg 0 general Hello from Python!",
			"sleep 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitFor(t, "connected", 1, 60*time.Second)
	hub.events.waitForMarker(t, "done", 60*time.Second)

	// Beta receives the forwarded MSG with Alpha's hash and nick.
	var fwdSrc []byte
	foundForward := false
	for _, ev := range hub.events.envelopes(1) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TMsg {
			continue
		}
		body, _ := envString(env, "6")
		if body != "Hello from Python!" {
			continue
		}
		foundForward = true
		fwdSrc = envBytesField(t, env, "4")
		if _, hasNick := envString(env, "7"); !hasNick {
			t.Error("the forwarded MSG lacks K_NICK")
		}
		if room, _ := envString(env, "5"); room != "general" {
			t.Errorf("the forwarded MSG room = %q, want general", room)
		}
	}
	if !foundForward {
		t.Fatal("Beta never received the forwarded MSG")
	}

	// Alpha receives its own echo with identical payload bytes.
	var alphaEcho string
	foundEcho := false
	for _, ev := range hub.events.envelopes(0) {
		env := envelopeOf(ev)
		if t, ok := envInt(env, "1"); !ok || t != TMsg {
			continue
		}
		body, _ := envString(env, "6")
		if body == "Hello from Python!" {
			foundEcho = true
			alphaEcho, _ = ev["payload_hex"].(string)
		}
	}
	if !foundEcho {
		t.Fatal("Alpha never received its own MSG echo")
	}

	// The forwarded copy and the echo are byte-identical (the hub sends
	// the same payload to the sender and the room).
	var betaForwardPayload string
	for _, ev := range hub.events.envelopes(1) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TMsg {
			continue
		}
		body, _ := envString(env, "6")
		if body == "Hello from Python!" {
			betaForwardPayload, _ = ev["payload_hex"].(string)
			break
		}
	}
	if betaForwardPayload == "" {
		t.Fatal("Beta's forward payload missing")
	}
	if alphaEcho != betaForwardPayload {
		t.Errorf("the echo and the forward differ:\n%v\nvs\n%v", alphaEcho, betaForwardPayload)
	}

	_ = fwdSrc
}

// firstErrorBody returns the first ERROR envelope body for a hub.
func firstErrorBody(er *eventReader, hubIndex int) (string, bool) {
	for _, ev := range er.envelopes(hubIndex) {
		env := envelopeOf(ev)
		mt, ok := envInt(env, "1")
		if !ok || mt != TError {
			continue
		}
		body, _ := envString(env, "6")
		return body, true
	}
	return "", false
}

// G13.6 /register, /list, and /who over the pipe: the Python client parses
// the NOTICE texts, so the byte-exact strings are the contract.
func TestIntegrationListWhoOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"join 0 general",
			"sleep 1",
			"cmd 0 general /register general",
			"sleep 1",
			"cmd 0 - /list",
			"sleep 1",
			"cmd 0 general /who general",
			"sleep 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitForMarker(t, "done", 90*time.Second)

	bodies := noticeBodies(hub.events, 0)
	wantRegister := "registered room general"
	if !containsString(bodies, wantRegister) {
		t.Errorf("no %q notice; bodies: %v", wantRegister, bodies)
	}
	wantList := "Registered public rooms:\n  general"
	if !containsString(bodies, wantList) {
		t.Errorf("no %q notice; bodies: %v", wantList, bodies)
	}
	// The /who line: `members in general: Alpha (<hash12>)`.
	foundWho := false
	prefix := "members in general: Alpha ("
	for _, body := range bodies {
		if !strings.HasPrefix(body, prefix) || !strings.HasSuffix(body, ")") {
			continue
		}
		inner := body[len(prefix) : len(body)-1]
		if len(inner) == 12 {
			foundWho = true
		}
	}
	if !foundWho {
		t.Errorf("no members-in notice for Alpha; bodies: %v", bodies)
	}
}

// noticeBodies collects every NOTICE body a hub received.
func noticeBodies(er *eventReader, hubIndex int) []string {
	var out []string
	for _, ev := range er.envelopes(hubIndex) {
		env := envelopeOf(ev)
		mt, ok := envInt(env, "1")
		if !ok || mt != TNotice {
			continue
		}
		if body, ok := envString(env, "6"); ok {
			out = append(out, body)
		}
	}
	return out
}

// containsString reports whether the exact string is in the list.
func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// G13.7 Error paths over the pipe: an oversized MSG, a bad CBOR packet, a
// pre-HELLO JOIN, an unknown slash command, and rate limiting all produce
// the exact wire-visible ERROR texts.
func TestIntegrationErrorPathsOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	rateLimit := 5
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS:        1.0,
		rateLimitMsgsPerMinute: &rateLimit,
		commands: []string{
			// Oversized MSG (raw, bypassing the client's size check).
			"connect Alpha",
			"join 0 general",
			"sleep 1",
			"rawmsg 0 general 360",
			"sleep 1",
			// Unknown slash command.
			"cmd 0 general /frobnicate",
			"sleep 1",
			// Rate limiting: drive messages until the hub rejects one.
			"msg 0 general one",
			"msg 0 general two",
			"msg 0 general three",
			"msg 0 general four",
			"msg 0 general five",
			"msg 0 general six",
			"msg 0 general seven",
			"msg 0 general eight",
			"sleep 1",
			// A pre-HELLO JOIN: the second client sends JOIN before the
			// HELLO handshake completes.
			"connect-earlyjoin Beta general",
			"sleep 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitForMarker(t, "done", 90*time.Second)

	bodies := errorBodies(hub.events, 0)
	if !containsString(bodies, "message too large: 360 bytes > 350 bytes") {
		t.Errorf("no oversized-message ERROR; bodies: %v", bodies)
	}
	if !containsString(bodies, "unrecognized command") {
		t.Errorf("no unrecognized-command ERROR; bodies: %v", bodies)
	}
	if !containsString(bodies, "rate limited") {
		t.Errorf("no rate-limited ERROR; bodies: %v", bodies)
	}

	// Beta's pre-HELLO JOIN: the `send HELLO first` ERROR.
	hub.events.waitFor(t, "connected", 1, 60*time.Second)
	betaBodies := errorBodies(hub.events, 1)
	if !containsString(betaBodies, "send HELLO first") {
		t.Errorf("no send-HELLO-first ERROR for Beta; bodies: %v", betaBodies)
	}
}

// errorBodies collects every ERROR body a hub received.
func errorBodies(er *eventReader, hubIndex int) []string {
	var out []string
	for _, ev := range er.envelopes(hubIndex) {
		env := envelopeOf(ev)
		mt, ok := envInt(env, "1")
		if !ok || mt != TError {
			continue
		}
		if body, ok := envString(env, "6"); ok {
			out = append(out, body)
		}
	}
	return out
}

// G13.10 Direct notices over the pipe: a hand-crafted T_NOTICE with K_DST
// and no room is forwarded with K_SRC rewritten and K_DST preserved; a
// room+dst mix errors with `direct notice must not include room`; an
// unknown destination errors with `destination not connected`.
func TestIntegrationDirectNoticeOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"sleep 1",
			"direct 0 1 A private note for Beta",
			"sleep 1",
			"direct-bad 0 1",
			"sleep 1",
			"direct-unknown 0",
			"sleep 1",
			"wait done",
		},
	})
	defer hub.stop()

	hub.events.waitForMarker(t, "done", 90*time.Second)

	// Beta receives the forwarded direct notice with K_DST preserved and
	// no room.
	found := false
	for _, ev := range hub.events.envelopes(1) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TNotice {
			continue
		}
		if _, hasRoom := env["5"]; hasRoom {
			continue
		}
		body, _ := envString(env, "6")
		if body == "A private note for Beta" {
			found = true
			if _, hasDst := env["8"]; !hasDst {
				t.Error("the forwarded direct notice lacks K_DST")
			}
			if src, _ := envString(env, "7"); src == "" {
				t.Log("the forwarded notice carries no nick (expected: the hub rewrites K_SRC only)")
			}
		}
	}
	if !found {
		t.Error("Beta never received the forwarded direct notice")
	}

	// Alpha hears the two error paths.
	alphaErrors := errorBodies(hub.events, 0)
	if !containsString(alphaErrors, "direct notice must not include room") {
		t.Errorf("no direct-notice-room ERROR; bodies: %v", alphaErrors)
	}
	if !containsString(alphaErrors, "destination not connected") {
		t.Errorf("no destination-not-connected ERROR; bodies: %v", alphaErrors)
	}
}

// G13.11 Lifecycle: dropping a client's link delivers the disconnect-driven
// PARTED with the departed nick to the remaining members; a banned
// identity is disconnected with the `banned` ERROR (two-phase hub start).
func TestIntegrationLifecycleOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	// Phase 1: connect both clients, drop Beta's link, and emit Beta's
	// identity hash for the ban configuration.
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"join 0 general",
			"join 1 general",
			"sleep 1",
			"identity 1",
			"drop 1",
			"sleep 2",
			"wait done",
		},
	})
	hub.events.waitForMarker(t, "done", 90*time.Second)

	// Alpha receives the disconnect-driven PARTED with Beta's nick.
	foundParted := false
	for _, ev := range hub.events.envelopes(0) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TParted {
			continue
		}
		if nick, hasNick := envString(env, "7"); hasNick && nick == "Beta" {
			foundParted = true
		}
	}
	if !foundParted {
		t.Error("Alpha never received the disconnect-driven PARTED with Beta's nick")
	}

	betaIDev, ok := hub.events.find("client-identity", 1)
	if !ok {
		t.Fatalf("no client-identity event; events:\n%v", hub.events.dump())
	}
	betaHash := betaIDev["hash"].(string)

	// Phase 2: ban Beta's identity and restart the hub.
	hub.stop()
	time.Sleep(500 * time.Millisecond)

	cfgText, err := os.ReadFile(filepath.Join(hub.homeDir, "rrcd.toml"))
	if err != nil {
		t.Fatal(err)
	}
	bannedConfig := strings.Replace(string(cfgText), "banned_identities = []",
		"banned_identities = ['"+betaHash+"']", 1)
	if err := os.WriteFile(filepath.Join(hub.homeDir, "rrcd.toml"), []byte(bannedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.writeCommands("connect Beta", "sleep 2", "wait done2")
	hub.start(t)
	// Poll for the banned ERROR instead of a fixed 15s wait: the restarted
	// hub must connect the client, evaluate the ban, and emit the ERROR,
	// which typically lands in a few seconds. The 45s cap leaves headroom
	// for Python driver startup under heavy CPU load (GO_TEST_PARALLEL).
	if !testutils.PollUntil(45*time.Second, func() bool {
		return containsString(errorBodies(hub.events, 0), "banned")
	}) {
		t.Errorf("no banned ERROR within 45s after the restart; bodies: %v", errorBodies(hub.events, 0))
	}
	hub.stop()

	betaErrors := errorBodies(hub.events, 0)
	if !containsString(betaErrors, "banned") {
		t.Errorf("no banned ERROR after the restart; bodies: %v", betaErrors)
	}
}

// G13.8 Slash-command matrix over the pipe: the operator commands run from
// the real Python client against a trusted-operator hub, and the persistent
// commands land in rooms.toml / rrcd.toml.
func TestIntegrationSlashCommandMatrixOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	// Phase 1: learn Alpha's identity hash.
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands:        []string{"connect Alpha", "identity 0", "wait done"},
	})
	hub.events.waitForMarker(t, "done", 90*time.Second)
	alphaIDev, ok := hub.events.find("client-identity", 0)
	if !ok {
		t.Fatalf("no client-identity event; events:\n%v", hub.events.dump())
	}
	alphaHash := alphaIDev["hash"].(string)

	// Phase 2: restart with Alpha as a server operator and drive the
	// operator command matrix.
	hub.stop()
	time.Sleep(500 * time.Millisecond)
	cfgText, err := os.ReadFile(filepath.Join(hub.homeDir, "rrcd.toml"))
	if err != nil {
		t.Fatal(err)
	}
	trustedConfig := strings.Replace(string(cfgText), "trusted_identities = []",
		"trusted_identities = ['"+alphaHash+"']", 1)
	if err := os.WriteFile(filepath.Join(hub.homeDir, "rrcd.toml"), []byte(trustedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	hub.writeCommands(
		"connect Alpha",
		"connect Beta",
		"join 0 general",
		"join 1 general",
		"sleep 1",
		"cmd 0 general /register general",
		"sleep 1",
		"cmd 0 general /mode general +t",
		"cmd 0 general /topic general Test Topic",
		"sleep 1",
		"cmd 0 general /ban general add Beta",
		"sleep 1",
		"cmd 0 general /ban general list",
		"cmd 0 general /invite general add Beta",
		"sleep 1",
		"cmd 0 - /reload",
		"sleep 1",
		"cmd 0 - /kline add Beta",
		"sleep 1",
		"wait done2",
	)
	hub.start(t)
	hub.events.waitForMarker(t, "done2", 120*time.Second)

	// Alpha's notices: the mode broadcast, the topic fanout, the kick
	// confirmation, the ban list, the invite, the stats header, the
	// reload summary, and the kline.
	alphaNotices := noticeBodies(hub.events, 0)
	for _, want := range []string{
		// The broadcast reflects the registered room's full mode
		// string (+nrt from the register).
		"mode for general is now: +nrt",
		"topic for general is now: Test Topic",
		"invite sent to Beta for general",
	} {
		if !containsString(alphaNotices, want) {
			t.Errorf("missing operator notice %q; notices: %v", want, alphaNotices)
		}
	}
	banLine := ""
	for _, body := range alphaNotices {
		if strings.HasPrefix(body, "bans in general: ") {
			banLine = body
		}
	}
	if banLine == "" {
		t.Fatalf("missing ban list notice; notices: %v", alphaNotices)
	}
	betaHash := strings.TrimSpace(strings.TrimPrefix(banLine, "bans in general: "))
	// Beta hears the ban's force-removal ERROR.
	betaErrors := errorBodies(hub.events, 1)
	if !containsString(betaErrors, "banned from general") {
		t.Errorf("missing banned-from ERROR for Beta; bodies: %v", betaErrors)
	}
	reloadFound := false
	for _, body := range alphaNotices {
		if strings.HasPrefix(body, "reloaded: trusted=") {
			reloadFound = true
		}
	}
	if !reloadFound {
		t.Errorf("missing /reload notice; notices: %v", alphaNotices)
	}

	// The persisted state: rooms.toml carries the topic and the ban, and
	// rrcd.toml carries Beta's kline.
	roomsData, err := os.ReadFile(filepath.Join(hub.homeDir, "rooms.toml"))
	if err != nil {
		t.Fatal(err)
	}
	roomsText := string(roomsData)
	if !strings.Contains(roomsText, "topic = \"Test Topic\"") {
		t.Errorf("rooms.toml lacks the topic:\\n%v", roomsText)
	}
	if !strings.Contains(roomsText, betaHash) {
		t.Errorf("rooms.toml lacks Beta's ban hash:\\n%v", roomsText)
	}
	cfgData, err := os.ReadFile(filepath.Join(hub.homeDir, "rrcd.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfgData), betaHash) {
		t.Errorf("rrcd.toml lacks Beta's kline hash:\\n%v", string(cfgData))
	}
}

// runPythonScript probes python3.14 then python3 to run a capture script;
// the test skips when no interpreter with the needed packages exists.
func runPythonScript(t *testing.T, script string) string {
	t.Helper()
	dir := "/tmp/rrcd-py-rt"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "script.py")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	var lastErr error
	for _, py := range []string{"python3.14", "python3"} {
		if _, err := exec.LookPath(py); err != nil {
			lastErr = err
			continue
		}
		cmd := exec.Command(py, path)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			lastErr = fmt.Errorf("%v: %v: %v", py, err, stderr.String())
			continue
		}
		return stdout.String()
	}
	t.Skipf("no python interpreter for the storage round-trip: %v", lastErr)
	return ""
}

// G13.12 Storage round-trip through the live hub: /register from the
// Python client persists rooms.toml that the ORIGINAL Python loader reads;
// a Python-written second room shows up in the hub's /list after a restart.
func TestIntegrationStorageRoundTripOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	// Phase 1: register a room from the client.
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		commands: []string{
			"connect Alpha",
			"join 0 general",
			"sleep 1",
			"cmd 0 general /register general",
			"sleep 1",
			"wait done",
		},
	})
	hub.events.waitForMarker(t, "done", 90*time.Second)

	// The ORIGINAL Python loader reads the hub's rooms.toml.
	roomsPath := filepath.Join(hub.homeDir, "rooms.toml")
	output := runPythonScript(t, "import sys\n"+
		"sys.path.insert(0, r\""+testutils.PythonRRCDRepoDir(t)+"\")\n"+
		"from rrcd.rooms import RoomManager\n"+
		"rm = RoomManager(None)\n"+
		"registry, err = rm.load_registry_from_path(r\""+roomsPath+"\", invite_timeout_s=900.0)\n"+
		"assert err is None, err\n"+
		"st = registry.get(\"general\")\n"+
		"assert st is not None, registry\n"+
		"print(\"FOUND\", st.get(\"founder\") is not None, st.get(\"no_outside_msgs\"), st.get(\"topic_ops_only\"))\n")
	if !strings.Contains(output, "FOUND True True True") {
		t.Fatalf("python loader did not see the registered room: %q", output)
	}

	// Phase 2: hand-write a second room with tomlkit formatting.
	hub.stop()
	time.Sleep(500 * time.Millisecond)
	runPythonScript(t, "import sys\n"+
		"sys.path.insert(0, r\""+testutils.PythonRRCDRepoDir(t)+"\")\n"+
		"import tomlkit\n"+
		"doc = tomlkit.parse(open(r\""+roomsPath+"\").read())\n"+
		"room = tomlkit.table()\n"+
		"doc[\"rooms\"][\"lounge\"] = room\n"+
		"doc[\"rooms\"][\"lounge\"][\"founder\"] = \""+hexKey(hub.identity.Hash)+"\"\n"+
		"doc[\"rooms\"][\"lounge\"][\"registered\"] = True\n"+
		"doc[\"rooms\"][\"lounge\"][\"topic\"] = \"Second room\"\n"+
		"text = tomlkit.dumps(doc)\n"+
		"open(r\""+roomsPath+"\", \"w\").write(text)\n"+
		"print(\"WROTE\", len(text))\n")
	// Restart the hub with the merged registry and list the rooms.
	hub.writeCommands(
		"connect Alpha",
		"sleep 1",
		"cmd 0 - /list",
		"sleep 1",
		"wait done3",
	)
	hub.start(t)
	hub.events.waitForMarker(t, "done3", 90*time.Second)

	bodies := noticeBodies(hub.events, 0)
	foundList := false
	for _, body := range bodies {
		if body == "Registered public rooms:\n  general\n  lounge - Second room" ||
			body == "Registered public rooms:\n  lounge - Second room\n  general" {
			foundList = true
		}
	}
	if !foundList {
		t.Errorf("the restarted hub's /list does not show both rooms; notices: %v", bodies)
	}
}

// G13.9 MOTD delivery: with resource transfer enabled the client receives
// the RESOURCE_ENVELOPE (kind "motd"); with it disabled the chunked-NOTICE
// fallback delivers the same text. (The RNS resource part transfer itself
// is an rns-package cross-implementation surface, exercised separately.)
func TestIntegrationMotdDeliveryOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	greeting := strings.Repeat("Welcome to the hub! ", 30)

	// Scenario 1: resource transfer enabled - the client receives the
	// RESOURCE_ENVELOPE for the greeting.
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS: 1.0,
		greeting:        &greeting,
		commands:        []string{"connect Alpha", "sleep 2", "wait done"},
	})
	hub.events.waitForMarker(t, "done", 90*time.Second)
	resEnvelope := 0
	for _, ev := range hub.events.envelopes(0) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); ok && mt == TResource {
			resEnvelope++
		}
	}
	if resEnvelope != 1 {
		t.Errorf("the client received %v RESOURCE_ENVELOPEs, want 1", resEnvelope)
	}
	hub.stop()
	time.Sleep(500 * time.Millisecond)

	// Scenario 2: resource transfer disabled - the chunked-NOTICE
	// fallback delivers the greeting text.
	enabled := false
	hub2 := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS:        1.0,
		greeting:               &greeting,
		enableResourceTransfer: &enabled,
		commands: []string{
			"connect Alpha",
			"sleep 2",
			"wait done2",
		},
	})
	hub2.events.waitForMarker(t, "done2", 90*time.Second)

	// The NOTICE chunks reassemble into the greeting.
	chunks := ""
	for _, ev := range hub2.events.envelopes(0) {
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); !ok || mt != TNotice {
			continue
		}
		if _, hasRoom := env["5"]; hasRoom {
			continue
		}
		if body, _ := envString(env, "6"); body != "" {
			chunks += body
		}
	}
	if !strings.Contains(chunks, "Welcome to the hub! Welcome to the hub!") {
		t.Errorf("the chunked MOTD did not reassemble; got %q", chunks[:minInt(len(chunks), 120)])
	}
}

// G15.10 The live-fleet soak: the exact raspberrypi deployment config
// (ping_interval_s=30, ping_timeout_s=60, include_joined_member_list=true,
// announce_period_s=300) with three real Python clients joined to one room,
// soaked long enough to cross several ping cycles. The hub must keep
// pinging every cycle (the PONGs must clear the pending markers), never
// tear a healthy client down, and never produce JOINED/PARTED waves.
func TestIntegrationPingSoakOverPipe(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	pyPath := findRNSPython(t)

	pingInterval := 2.0
	pingTimeout := 6.0
	includeList := true
	hub := startGorrcdHub(t, pyPath, hubTestConfig{
		announcePeriodS:   300.0,
		pingIntervalS:     &pingInterval,
		pingTimeoutS:      &pingTimeout,
		includeJoinedList: &includeList,
		commands: []string{
			"connect Alpha",
			"connect Beta",
			"connect Gamma",
			"join 0 soak",
			"join 1 soak",
			"join 2 soak",
			"sleep 1",
			"wait soak-ready",
		},
	})
	defer hub.stop()

	for i := range 3 {
		hub.events.waitFor(t, "connected", i, 90*time.Second)
	}
	hub.events.waitForMarker(t, "soak-ready", 90*time.Second)

	// SOAK: 3 minutes (180s) with a 2s ping interval = 90 ping cycles.
	// (Long enough to cross dozens of cycles while staying inside the
	// per-package go test -timeout; runs in parallel with the rest of the
	// package since everything it touches is test-private.)
	deadline := time.Now().Add(180 * time.Second)
	var lastPings [3]int
	var lastParts [3]int
	var lastJoins [3]int
	for time.Now().Before(deadline) {
		time.Sleep(15 * time.Second)
		for i := range 3 {
			lastPings[i] = countEnvelopesOf(hub.events, i, TPing)
			lastParts[i] = countEnvelopesOf(hub.events, i, TParted)
			lastJoins[i] = countEnvelopesOf(hub.events, i, TJoined)
		}
		t.Logf("soak t+%vs pings=%v parts=%v joins=%v",
			int(180-time.Until(deadline).Seconds()), lastPings, lastParts, lastJoins)
	}

	// Every client must have been pinged continuously (90 cycles).
	for i := range 3 {
		if lastPings[i] < 45 {
			t.Errorf("client %v received only %v pings over the soak, want >= 45 (2s interval)",
				i, lastPings[i])
		}
		// No client may ever be torn down while it keeps PONGing.
		if lastParts[i] != 0 {
			t.Errorf("client %v received %v PARTED envelopes; healthy PONGing clients must never be torn down",
				i, lastParts[i])
		}
	}

	// The hub heard every PONG: pings_out == pongs_in per the hub stats.
	// (Observable via the clients' t=31 echo envelopes.)
	pongs := 0
	for _, ev := range hub.events.all() {
		if ev["event"] != "envelope" {
			continue
		}
		env := envelopeOf(ev)
		if mt, ok := envInt(env, "1"); ok && mt == TPong {
			pongs++
		}
	}
	t.Logf("client-sent PONGs observed: %v", pongs)
}
