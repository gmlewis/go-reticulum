// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// H1 — the differential harness for the transport tables.
//
// The framing-fuzz harness compares the parser and the packet filter. This one
// goes further: the same frames are driven through a full inbound pass on both
// implementations, and the resulting path-table entries are compared field by
// field (destination hash, next hop, hops, expiry class, receiving interface).
//
// The Python side runs a real Reticulum instance in a throwaway config
// directory so Transport.start has done its normal setup, then replaces its
// tables per case and calls Transport.inbound. Python builds each announce
// itself (a real identity, a real destination, a real signature), so the
// expectation is the reference implementation's behaviour rather than a
// paraphrase of it.

// h1AnnounceCase is one announce scenario.
type h1AnnounceCase struct {
	Name        string `json:"name"`
	AppData     string `json:"app_data"`
	WireHops    int    `json:"wire_hops"`
	TransportID string `json:"transport_id"` // empty for a HEADER_1 announce
}

// h1AnnounceResult is what the reference's tables hold after inbound.
type h1AnnounceResult struct {
	Dest      string  `json:"dest"`
	Raw       string  `json:"raw"`
	Found     bool    `json:"found"`
	NextHop   string  `json:"next_hop"`
	Hops      int     `json:"hops"`
	Expiry    float64 `json:"expiry"`
	Interface string  `json:"iface"`
}

const h1AnnounceScript = `
import json, sys
import RNS
from RNS import Transport
from RNS.Transport import IDX_PT_TIMESTAMP, IDX_PT_NEXT_HOP, IDX_PT_HOPS, IDX_PT_EXPIRES, IDX_PT_RVCD_IF
from RNS.Interfaces.Interface import Interface

# The stub is deliberately not the real Interface: its ingress-limit burst
# state machine trips after a few announces in one process, which would make
# the table comparison depend on case order. The reference implementation's
# path-table logic is what is under test, not its burst timer.
class Stub(Interface):
    def __init__(self):
        Interface.__init__(self)
        self.name = "rx"
        self.online = True
        self.announce_rate_target = None
        self.announce_rate_grace = None
        self.announce_rate_penalty = None
        self.ia_freq_deque = []
        self.op_freq_deque = []
        self.held_announces = {}
    def should_ingress_limit(self): return False
    def should_ingress_limit_pr(self): return False

r = RNS.Reticulum(sys.argv[2])

iface = Stub()

def reset():
    Transport.path_table = {}
    Transport.announce_table = {}
    Transport.announce_rate_table = {}
    Transport.destinations_map = {}
    Transport.destinations = []
    Transport.packet_hashlist = set()
    Transport.packet_hashlist_prev = set()
    Transport.held_announces = {}
    Transport.path_requests = {}
    Transport.discovery_path_requests = {}
    Transport.discovery_pr_tags = set()
    Transport.discovery_pr_tags_prev = set()
    Transport.inflight_path_requests = {}

results = []
for case in json.loads(sys.argv[1]):
    reset()
    idn = RNS.Identity()
    dest = RNS.Destination(idn, RNS.Destination.IN, RNS.Destination.SINGLE, "h1test", "aspect")
    base = dest.announce(app_data=bytes.fromhex(case["app_data"]), send=False)
    if case["transport_id"]:
        # A HEADER_2 announce must be built with its transport header from the
        # start: the announce signature covers the hashable part, so mutating
        # header_type afterwards invalidates it.
        p = RNS.Packet(dest, base.data, RNS.Packet.ANNOUNCE,
                       header_type=RNS.Packet.HEADER_2,
                       transport_type=Transport.TRANSPORT,
                       transport_id=bytes.fromhex(case["transport_id"]))
    else:
        p = base
    p.hops = case["wire_hops"]
    p.pack()
    # The destination registered itself with the transport above; a receiving
    # transport does not know it, so clear the local destination table before
    # the inbound pass.
    Transport.destinations_map = {}
    Transport.destinations = []
    Transport.inbound(p.raw, iface)
    entry = Transport.path_table.get(dest.hash)
    rec = {"dest": dest.hash.hex(), "raw": p.raw.hex(), "found": entry is not None,
           "next_hop": "", "hops": 0, "expiry": 0, "iface": ""}
    if entry is not None:
        rec["next_hop"] = entry[IDX_PT_NEXT_HOP].hex() if entry[IDX_PT_NEXT_HOP] else ""
        rec["hops"] = entry[IDX_PT_HOPS]
        rec["expiry"] = entry[IDX_PT_EXPIRES] - entry[IDX_PT_TIMESTAMP]
        rec["iface"] = str(entry[IDX_PT_RVCD_IF].name)
    results.append(rec)
json.dump(results, sys.stdout)
`

// TestTransportTableDifferentialAnnounce drives a battery of announces through
// a Python transport and the Go transport and compares the resulting
// path-table entries field by field.
func TestTransportTableDifferentialAnnounce(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	cases := []h1AnnounceCase{
		{Name: "direct announce", AppData: "68656c6c6f", WireHops: 0},
		{Name: "announce two hops out", AppData: "6d756c7469686f70", WireHops: 2},
		{Name: "short app data", AppData: "00", WireHops: 0},
		{Name: "transported announce", AppData: "7472616e73706f7274", WireHops: 1, TransportID: "0102030405060708090a0b0c0d0e0f10"},
	}
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	var want []h1AnnounceResult
	if err := json.Unmarshal([]byte(runPythonTransportScript(t, h1AnnounceScript, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(want), len(cases))
	}

	for i, tc := range cases {
		raw, err := hex.DecodeString(want[i].Raw)
		if err != nil {
			t.Fatalf("%v: decode raw frame: %v", tc.Name, err)
		}
		ts := NewTransportSystem(nil)
		ts.identity = mustTestNewIdentity(t, true)
		iface := &dummyInterface{name: "rx"}
		ts.Inbound(raw, iface)

		dest, err := hex.DecodeString(want[i].Dest)
		if err != nil {
			t.Fatalf("%v: decode destination: %v", tc.Name, err)
		}
		ts.mu.Lock()
		entry := ts.pathTable[string(dest)]
		ts.mu.Unlock()

		if !want[i].Found {
			if entry != nil {
				t.Errorf("%v: Go installed a path Python did not", tc.Name)
			}
			continue
		}
		if entry == nil {
			t.Errorf("%v: Go did not install the path Python did", tc.Name)
			continue
		}
		if got := hex.EncodeToString(entry.NextHop); got != want[i].NextHop {
			t.Errorf("%v: next hop = %v, want %v (Python)", tc.Name, got, want[i].NextHop)
		}
		if entry.Hops != want[i].Hops {
			t.Errorf("%v: hops = %v, want %v (Python)", tc.Name, entry.Hops, want[i].Hops)
		}
		if got := entry.Expires.Sub(entry.Timestamp).Seconds(); math.Abs(got-want[i].Expiry) > 1 {
			t.Errorf("%v: expiry = %v s, want %v s (Python)", tc.Name, got, want[i].Expiry)
		}
		if entry.Interface == nil || entry.Interface.Name() != want[i].Interface {
			t.Errorf("%v: interface = %v, want %q (Python)", tc.Name, entry.Interface, want[i].Interface)
		}
	}
}

// h1PathRequestResult is the decision both transports report for a path
// request: whether this node answered it from its own destination table.
type h1PathRequestResult struct {
	Matched bool `json:"matched"`
}

const h1PathRequestScript = `
import json, sys
import RNS
from RNS import Transport
from RNS.Interfaces.Interface import Interface

class Capture(Interface):
    def __init__(self):
        Interface.__init__(self)
        self.name = "rx"
        self.online = True
        self.OUT = True
        self.sent = []
        self.announce_rate_target = None
        self.announce_rate_grace = None
        self.announce_rate_penalty = None
        self.ia_freq_deque = []
        self.op_freq_deque = []
        self.held_announces = {}
    def process_outgoing(self, raw):
        self.sent.append(raw)
    def should_ingress_limit(self): return False
    def should_ingress_limit_pr(self): return False

r = RNS.Reticulum(sys.argv[2])

results = []
for case in json.loads(sys.argv[1]):
    Transport.path_table = {}
    Transport.announce_table = {}
    Transport.destinations_map = {}
    Transport.destinations = []
    Transport.packet_hashlist = set()
    Transport.packet_hashlist_prev = set()
    Transport.held_announces = {}
    Transport.path_requests = {}
    Transport.discovery_path_requests = {}
    Transport.discovery_pr_tags = []
    Transport.discovery_pr_tags_prev = set()
    Transport.inflight_path_requests = {}

    iface = Capture()
    Transport.interfaces = [iface]

    idn = RNS.Identity()
    dest = RNS.Destination(idn, RNS.Destination.IN, RNS.Destination.SINGLE, "h1test", "aspect")
    # The transport's own control destination handles path requests; clearing
    # the maps above removed it, so put it back.
    Transport.destinations_map[Transport.path_request_destination.hash] = Transport.path_request_destination
    pr_dest = RNS.Destination(None, RNS.Destination.OUT, RNS.Destination.PLAIN,
                              "rnstransport", "path", "request")
    data = dest.hash + idn.hash + bytes.fromhex(case["tag"])
    p = RNS.Packet(pr_dest, data, RNS.Packet.DATA, transport_type=Transport.BROADCAST)
    p.pack()
    Transport.inbound(p.raw, iface)
    answered = any(len(raw) > 0 and (raw[0] >> 4) == RNS.Packet.HEADER_1 and
                   (raw[0] & 0x0f) == RNS.Packet.ANNOUNCE for raw in iface.sent)
    results.append({"matched": answered})
json.dump(results, sys.stdout)
`

// TestTransportTableDifferentialPathRequest drives a path request for a
// locally registered destination through both transports and compares the
// decision both tables imply: this node answers it itself rather than
// relaying it onward. Each side builds the request for its own local
// destination, because the destination hash is the input that makes the
// request answerable; the comparison is the decision, not the identity.
func TestTransportTableDifferentialPathRequest(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	type requestCase struct {
		Tag string `json:"tag"`
	}
	cases := []requestCase{{Tag: "00112233445566778899aabbccddeeff"}}
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	var want []h1PathRequestResult
	if err := json.Unmarshal([]byte(runPythonTransportScript(t, h1PathRequestScript, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(want), len(cases))
	}

	for i := range want {
		// A positive assertion: the reference must answer its own local
		// destination, or the comparison below is vacuous.
		if !want[i].Matched {
			t.Fatalf("case %v: the reference did not answer its own local destination", i)
		}
		ts := NewTransportSystem(nil)
		ts.identity = mustTestNewIdentity(t, true)
		local := mustTestNewDestination(t, ts, mustTestNewIdentity(t, true), DestinationIn, DestinationSingle, "h1test", "aspect")
		ts.RegisterDestination(local)

		prDest, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
		if err != nil {
			t.Fatalf("path request destination: %v", err)
		}
		ts.mu.Lock()
		ts.pathRequestHash = copyBytes(prDest.Hash)
		ts.mu.Unlock()

		tag, err := hex.DecodeString(cases[i].Tag)
		if err != nil {
			t.Fatalf("decode tag: %v", err)
		}
		data := append(copyBytes(local.Hash), ts.identity.Hash...)
		data = append(data, tag...)
		p := NewPacket(prDest, data)
		p.TransportType = TransportBroadcast
		if err := p.Pack(); err != nil {
			t.Fatalf("pack path request: %v", err)
		}

		iface := &captureSendInterface{dummyInterface: dummyInterface{name: "rx"}}
		ts.Inbound(p.Raw, iface)

		answered := false
		for _, raw := range iface.sent {
			// The port answers with a transported (HEADER_2) announce; the
			// decision under test is that an announce answered the request.
			if len(raw) > 0 && (raw[0]&0x0f) == PacketAnnounce {
				answered = true
				break
			}
		}
		if answered != want[i].Matched {
			t.Errorf("case %v: Go answered=%v, want %v (Python)", i, answered, want[i].Matched)
		}
	}
}

// captureSendInterface records everything the transport sends on it.
type captureSendInterface struct {
	dummyInterface
	sent [][]byte
}

func (c *captureSendInterface) Send(data []byte) error {
	c.sent = append(c.sent, append([]byte(nil), data...))
	return nil
}

// runPythonTransportScript runs script with the repo-standard interpreter and a
// throwaway Reticulum config directory, returning stdout.
func runPythonTransportScript(t *testing.T, script, arg string) string {
	t.Helper()
	dir := testutils.TempDir(t, tempDirPrefix)
	config := "[reticulum]\nshare_instance = No\nenable_transport = No\n\n[logging]\nloglevel = 0\n\n[interfaces]\n"
	if err := os.WriteFile(dir+"/config", []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return testutils.RunPython(t, script, arg, dir)
}

// h1EmittedFraming is the framing both implementations put on the wire for a
// locally answered path request.
type h1EmittedFraming struct {
	HeaderType      int    `json:"header_type"`
	PacketType      int    `json:"packet_type"`
	Context         int    `json:"context"`
	TransportType   int    `json:"transport_type"`
	TransportID     string `json:"transport_id"`
	Hops            int    `json:"hops"`
	DestinationHash string `json:"destination_hash"`
	FrameLen        int    `json:"frame_len"`
}

const h1EmittedScript = `
import json, sys
import RNS
from RNS import Transport
from RNS.Interfaces.Interface import Interface

class Capture(Interface):
    def __init__(self):
        Interface.__init__(self)
        self.name = "rx"
        self.online = True
        self.OUT = True
        self.sent = []
        self.announce_rate_target = None
        self.announce_rate_grace = None
        self.announce_rate_penalty = None
        self.ia_freq_deque = []
        self.op_freq_deque = []
        self.held_announces = {}
    def process_outgoing(self, raw):
        self.sent.append(raw)
    def should_ingress_limit(self): return False
    def should_ingress_limit_pr(self): return False

r = RNS.Reticulum(sys.argv[2])
iface = Capture()
Transport.interfaces = [iface]

idn = RNS.Identity()
dest = RNS.Destination(idn, RNS.Destination.IN, RNS.Destination.SINGLE, "h1test", "aspect")
Transport.destinations_map[Transport.path_request_destination.hash] = Transport.path_request_destination
pr_dest = RNS.Destination(None, RNS.Destination.OUT, RNS.Destination.PLAIN,
                          "rnstransport", "path", "request")
data = dest.hash + idn.hash + bytes.fromhex("00112233445566778899aabbccddeeff")
p = RNS.Packet(pr_dest, data, RNS.Packet.DATA, transport_type=Transport.BROADCAST)
p.pack()
Transport.inbound(p.raw, iface)
framing = {}
if iface.sent:
    q = RNS.Packet(None, iface.sent[0])
    q.unpack()
    framing = {
        "header_type": q.header_type,
        "packet_type": q.packet_type,
        "context": q.context,
        "transport_type": q.transport_type,
        "transport_id": q.transport_id.hex() if q.transport_id is not None else "",
        "hops": q.hops,
        "destination_hash": q.destination_hash.hex(),
        "frame_len": len(iface.sent[0]),
    }
json.dump(framing, sys.stdout)
`

// TestOutboundFramingDifferentialPathResponse compares the framing of the
// announce a transport emits when it answers a path request for its own local
// destination against the reference's captured frame. The payload bytes cannot
// be compared directly (each side signs with its own identity and stamps the
// current time into the random blob), but every framing decision can be.
func TestOutboundFramingDifferentialPathResponse(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	var want h1EmittedFraming
	if err := json.Unmarshal([]byte(runPythonTransportScript(t, h1EmittedScript, "{}")), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	if want.FrameLen == 0 {
		t.Fatal("the reference emitted no path response to compare")
	}

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	local := mustTestNewDestination(t, ts, mustTestNewIdentity(t, true), DestinationIn, DestinationSingle, "h1test", "aspect")
	ts.RegisterDestination(local)
	iface := &captureSendInterface{dummyInterface: dummyInterface{name: "rx"}}
	ts.mu.Lock()
	ts.pathRequestHash = nil
	ts.mu.Unlock()

	tag := make([]byte, TruncatedHashLength/8)
	data := append(copyBytes(local.Hash), ts.identity.Hash...)
	data = append(data, tag...)
	ts.handlePathRequest(data, &Packet{ReceivingInterface: iface})
	if len(iface.sent) != 1 {
		t.Fatalf("Go emitted %v frames, want 1", len(iface.sent))
	}
	frame := iface.sent[0]
	got := NewPacketFromRaw(frame)
	if err := got.Unpack(); err != nil {
		t.Fatalf("unpack emitted frame: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"header type", got.HeaderType, want.HeaderType},
		{"packet type", got.PacketType, want.PacketType},
		{"context", got.Context, want.Context},
		{"transport type", got.TransportType, want.TransportType},
		{"hops", got.Hops, want.Hops},
		{"destination hash length", len(got.DestinationHash), len(want.DestinationHash) / 2},
		{"frame length", len(frame), want.FrameLen},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%v = %v, want %v (Python)", check.name, check.got, check.want)
		}
	}
	if len(got.TransportID) != len(want.TransportID)/2 {
		t.Errorf("transport id length = %v, want %v (Python)", len(got.TransportID), len(want.TransportID)/2)
	}
}
