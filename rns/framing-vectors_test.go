// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// Byte-level framing golden vectors, derived fresh from the Python Reticulum
// reference on every run — the pattern nomadnet/micron's
// TestRenderParityFixtures and lxmf's Python-signed message test established.
// Nothing here is a checked-in golden file: the expected bytes come from the
// installed Python RNS each time, so upstream drift or a Go change to the wire
// format surfaces immediately instead of silently. Every case cites the Python
// source line that defines it, and every reviewed divergence is listed with its
// reason, so an unexplained difference cannot pass.
//
// Three suites:
//
//   - TestFramingVectorsPackMatrix pins Packet.Pack for every header type,
//     packet type, context, context flag and destination type Python allows.
//   - TestFramingVectorsOutboundFraming pins Outbound's transmit/no-transmit
//     decision and the exact frame it emits for a known path, a shared-instance
//     one-hop path and no path at all. This is the decision the "two transport
//     ids in one frame" bug lived in.
//   - TestFramingVectorsAnnounceExclusions pins announceTransmitDecision
//     against Python's announce-broadcast elif chain.
//
// All three exec Python, so they skip (never fail) on a machine without Python
// RNS, via testutils.SkipIfNoPythonRNS. They run in the full gate and in CI,
// where Python RNS is installed; the -short integration pass skips them.

// The fixed inputs every framing vector uses: a 16-byte destination hash, two
// distinct 16-byte transport ids and a short payload. Small fixed values keep a
// mismatch readable in the failure output.
var (
	framingVectorDestHash      = sequentialBytes(0x00, 16)
	framingVectorTransportID   = sequentialBytes(0x10, 16)
	framingVectorRepackNextHop = sequentialBytes(0x20, 16)
)

// framingVectorPayload is the plaintext every vector packs.
const framingVectorPayload = "hello world"

// sequentialBytes returns n bytes starting at start and incrementing by one.
func sequentialBytes(start byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = start + byte(i)
	}
	return out
}

// identityEncryptDestination is a PacketDestination whose Encrypt/Decrypt are
// the identity function. With encryption out of the way a packed packet's bytes
// are fully deterministic, so they can be compared byte-for-byte against a
// Python Packet built on the matching Python-side mock. Real encryption is not
// left uncovered: the LXMF message and storage interop suites exercise it.
type identityEncryptDestination struct {
	hash []byte
	typ  int
}

func (d *identityEncryptDestination) GetHash() []byte                  { return d.hash }
func (d *identityEncryptDestination) GetType() int                     { return d.typ }
func (d *identityEncryptDestination) GetTransport() Transport          { return nil }
func (d *identityEncryptDestination) Encrypt(p []byte) ([]byte, error) { return p, nil }
func (d *identityEncryptDestination) Decrypt(c []byte) ([]byte, error) { return c, nil }
func (d *identityEncryptDestination) Sign([]byte) ([]byte, error) {
	return nil, fmt.Errorf("identity-encrypt destination cannot sign")
}
func (d *identityEncryptDestination) Verify([]byte, []byte) bool { return false }

// framingVectorInterface is a stub interface that records every frame it is
// asked to send. Its mode and announce options are settable so the announce
// matrix can drive them. It embeds dummyInterface for the rest of the
// interfaces.Interface method set.
type framingVectorInterface struct {
	dummyInterface
	mode    int
	isOut   bool
	fromInt bool
	toInt   *bool
	mu      sync.Mutex
	sent    [][]byte
}

func newFramingVectorInterface(name string, mode int) *framingVectorInterface {
	return &framingVectorInterface{dummyInterface: dummyInterface{name: name}, mode: mode, isOut: true}
}

func (f *framingVectorInterface) IsOut() bool                 { return f.isOut }
func (f *framingVectorInterface) Mode() int                   { return f.mode }
func (f *framingVectorInterface) AnnouncesFromInternal() bool { return f.fromInt }
func (f *framingVectorInterface) AnnouncesToInternal() *bool  { return f.toInt }

// SetAnnouncesToInternal sets the announce-to-internal option.
func (f *framingVectorInterface) SetAnnouncesToInternal(v bool) {
	if v {
		yes := true
		f.toInt = &yes
		return
	}
	f.toInt = nil
}

func (f *framingVectorInterface) Send(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, append([]byte(nil), data...))
	return nil
}

// SentFrames returns a copy of every frame the interface was asked to send.
func (f *framingVectorInterface) SentFrames() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.sent))
	copy(out, f.sent)
	return out
}

// runFramingVectorPython marshals req to a temp file, runs the given Python
// script against it, and decodes its stdout into out. The script reads its
// cases from the file named by sys.argv[1], so a large case table needs no
// shell quoting.
func runFramingVectorPython(t *testing.T, script string, req any, out any) {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)

	dir := testutils.TempDir(t, tempDirPrefix)
	path := filepath.Join(dir, "framing-cases.json")
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal framing cases: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write framing cases: %v", err)
	}

	stdout := testutils.RunPython(t, script, path)
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), out); err != nil {
		t.Fatalf("decode python framing vectors: %v\nraw stdout:\n%v", err, stdout)
	}
}

// mustDecodeHex decodes a hex string the Python script produced.
func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode python hex %q: %v", s, err)
	}
	return b
}

// ---------------------------------------------------------------------------
// Packet.Pack matrix
// ---------------------------------------------------------------------------

// packVectorRequest is one Packet construction handed to Python.
type packVectorRequest struct {
	DestHash      string `json:"dest_hash"`
	DestType      int    `json:"dest_type"`
	PacketType    int    `json:"packet_type"`
	Context       int    `json:"context"`
	ContextFlag   int    `json:"context_flag"`
	TransportType int    `json:"transport_type"`
	HeaderType    int    `json:"header_type"`
	TransportID   string `json:"transport_id,omitempty"`
	Hops          int    `json:"hops"`
	Plaintext     string `json:"plaintext"`
}

// packVectorResult is Python's answer for one case.
type packVectorResult struct {
	Raw   string `json:"raw"`
	Flags int    `json:"flags"`
	Err   string `json:"err"`
}

// packMatrixCase is one cell of the coverage matrix, with the Python citation
// that defines its expected bytes.
type packMatrixCase struct {
	name        string
	headerType  int
	packetType  int
	context     int
	contextFlag int
	destType    int
	transportTy int
	hops        int
	transportID bool
	citation    string
}

// pythonPackVectorScript builds a Python Packet on a mock destination whose
// encrypt is the identity function, then packs it. RNS/Packet.py:122-176 is the
// constructor and get_packed_flags, :177-239 is pack. The LRPROOF header
// substitution is at :178-180: the header carries the destination's link_id,
// which for a real Link equals its hash (RNS/Link.py:346), so the mock sets
// link_id = hash and the substitution is equivalent to the port's use of the
// destination hash.
const pythonPackVectorScript = `
import json, sys
import RNS
from RNS.Packet import Packet as PacketCls

class MockDest:
    def __init__(self, h, dtype, mtu=500):
        self.hash = h
        self.link_id = h
        self.type = dtype
        self.mtu = mtu
    def encrypt(self, data): return bytes(data)
    def decrypt(self, data): return bytes(data)

cases = json.load(open(sys.argv[1]))
out = []
for c in cases:
    dest = MockDest(bytes.fromhex(c["dest_hash"]), c["dest_type"])
    p = PacketCls(dest, bytes.fromhex(c["plaintext"]),
                  packet_type=c["packet_type"], context=c["context"],
                  transport_type=c["transport_type"], header_type=c["header_type"],
                  transport_id=bytes.fromhex(c["transport_id"]) if c.get("transport_id") else None,
                  context_flag=c["context_flag"])
    p.hops = c.get("hops", 0)
    rec = {"raw": "", "flags": -1, "err": ""}
    try:
        p.pack()
        rec["raw"] = p.raw.hex()
        rec["flags"] = p.flags
    except Exception as e:
        rec["err"] = "%s: %s" % (type(e).__name__, e)
    out.append(rec)
print(json.dumps(out))
`

// framingPackCases is the coverage matrix.
func framingPackCases() []packMatrixCase {
	const packCite = "RNS/Packet.py:177-239 (pack) + :122-176 (__init__/get_packed_flags)"

	contexts := []struct {
		name string
		val  int
	}{
		{"none", ContextNone},
		{"resource", ContextResource},
		{"resource_adv", ContextResourceAdv},
		{"cache_request", ContextCacheRequest},
		{"request", ContextRequest},
		{"path_response", ContextPathResponse},
		{"channel", ContextChannel},
		{"keepalive", ContextKeepalive},
		{"link_identify", ContextLinkIdentify},
		{"link_close", ContextLinkClose},
		{"link_proof", ContextLinkProof},
		{"lrrtt", ContextLrrtt},
		{"lrproof", ContextLrproof},
	}
	packetTypes := []struct {
		name string
		val  int
	}{
		{"data", PacketData},
		{"announce", PacketAnnounce},
		{"linkrequest", PacketLinkRequest},
		{"proof", PacketProof},
	}
	destTypes := []struct {
		name string
		val  int
	}{
		{"single", DestinationSingle},
		{"group", DestinationGroup},
		{"plain", DestinationPlain},
		{"link", DestinationLink},
	}

	var cases []packMatrixCase
	// Header1 cross-product: every packet type × context × destination type.
	for _, pt := range packetTypes {
		for _, cx := range contexts {
			for _, dt := range destTypes {
				cases = append(cases, packMatrixCase{
					name:       fmt.Sprintf("header1/%v/%v/%v", pt.name, cx.name, dt.name),
					headerType: Header1,
					packetType: pt.val,
					context:    cx.val,
					destType:   dt.val,
					citation:   packCite,
				})
			}
		}
	}

	cases = append(cases,
		packMatrixCase{
			name: "header1/context_flag_set", headerType: Header1, packetType: PacketData,
			context: ContextNone, contextFlag: FlagSet, destType: DestinationSingle, citation: packCite,
		},
		packMatrixCase{
			name: "header2/announce/context_flag_set", headerType: Header2, packetType: PacketAnnounce,
			context: ContextNone, contextFlag: FlagSet, destType: DestinationSingle, transportID: true, citation: packCite,
		},
		packMatrixCase{
			name: "header1/nonzero_hops", headerType: Header1, packetType: PacketData,
			context: ContextNone, destType: DestinationSingle, hops: 7, citation: packCite,
		},
	)

	// Header2 packed outright. Python's HEADER_2 branch assigns a ciphertext
	// only for ANNOUNCE (RNS/Packet.py:226-234), so ANNOUNCE is the only packet
	// type that can be constructed as HEADER_2; every other HEADER_2 frame on
	// the wire is produced by a relay's raw byte splice, which the Outbound
	// suite covers. The TRANSPORT transport type is the relay case — Python
	// builds exactly this packet for an announce it is forwarding
	// (RNS/Transport.py:2007-2020) — and BROADCAST is a locally-built one.
	transportTypes := []struct {
		name string
		val  int
	}{
		{"broadcast", TransportBroadcast},
		{"transport", TransportForward},
	}
	for _, tt := range transportTypes {
		for _, dt := range destTypes {
			cases = append(cases, packMatrixCase{
				name:        fmt.Sprintf("header2/announce/%v/%v", tt.name, dt.name),
				headerType:  Header2,
				packetType:  PacketAnnounce,
				context:     ContextNone,
				destType:    dt.val,
				transportTy: tt.val,
				transportID: true,
				citation:    "RNS/Packet.py:226-234 (HEADER_2 ciphertext for ANNOUNCE) + :146-157 (flags)",
			})
		}
	}

	// LRPROOF: Python puts the destination's link_id in the header instead of its
	// hash (:178-180) and forces the LINK destination type in the flags (:155-157).
	for _, dt := range destTypes {
		cases = append(cases, packMatrixCase{
			name: fmt.Sprintf("lrproof/%v", dt.name), headerType: Header1, packetType: PacketProof,
			context: ContextLrproof, destType: dt.val, citation: "RNS/Packet.py:155-157,178-180",
		})
	}
	return cases
}

// TestFramingVectorsPackMatrix asserts that Packet.Pack emits byte-identical
// frames to the Python reference across the whole construction matrix.
func TestFramingVectorsPackMatrix(t *testing.T) {
	t.Parallel()

	cases := framingPackCases()
	req := make([]packVectorRequest, 0, len(cases))
	for _, c := range cases {
		r := packVectorRequest{
			DestHash:      hex.EncodeToString(framingVectorDestHash),
			DestType:      c.destType,
			PacketType:    c.packetType,
			Context:       c.context,
			ContextFlag:   c.contextFlag,
			TransportType: c.transportTy,
			HeaderType:    c.headerType,
			Hops:          c.hops,
			Plaintext:     hex.EncodeToString([]byte(framingVectorPayload)),
		}
		if c.transportID {
			r.TransportID = hex.EncodeToString(framingVectorTransportID)
		}
		req = append(req, r)
	}

	var results []packVectorResult
	runFramingVectorPython(t, pythonPackVectorScript, req, &results)
	if len(results) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(results), len(cases))
	}

	for i, c := range cases {
		res := results[i]
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			gotRaw, gotFlags, err := packVectorGo(c)
			pythonFailed := res.Err != ""

			// An error on both sides is agreement about rejecting the input.
			if pythonFailed && err != nil {
				return
			}
			if pythonFailed != (err != nil) {
				t.Fatalf("error disagreement: go err=%v, python err=%q (Python: %v)", err, res.Err, c.citation)
			}
			if want := mustDecodeHex(t, res.Raw); !bytes.Equal(gotRaw, want) {
				t.Errorf("packed frame differs from Python (%v):\n  go     = %x\n  python = %x",
					c.citation, gotRaw, want)
			}
			if gotFlags != res.Flags {
				t.Errorf("flags byte differs from Python (%v): go=%#02x python=%#02x", c.citation, gotFlags, res.Flags)
			}
		})
	}
}

// packVectorGo builds the Go packet for one matrix case and returns its raw
// bytes and flags byte.
func packVectorGo(c packMatrixCase) ([]byte, int, error) {
	dest := &identityEncryptDestination{hash: framingVectorDestHash, typ: c.destType}
	p := NewPacketWithTransport(nil, dest, []byte(framingVectorPayload))
	p.PacketType = c.packetType
	p.Context = c.context
	p.ContextFlag = c.contextFlag
	p.HeaderType = c.headerType
	p.TransportType = c.transportTy
	p.Hops = c.hops
	if c.transportID {
		p.TransportID = framingVectorTransportID
	}
	if err := p.Pack(); err != nil {
		return nil, 0, err
	}
	return p.Raw, int(p.Flags), nil
}

// ---------------------------------------------------------------------------
// Outbound framing decisions
// ---------------------------------------------------------------------------

// outboundVectorRequest describes one Outbound scenario.
type outboundVectorRequest struct {
	SharedInstance    bool   `json:"shared_instance"`
	WithInterface     bool   `json:"with_interface"`
	InterfaceIsOut    bool   `json:"interface_is_out"`
	AttachedInterface bool   `json:"attached_interface"`
	AttachedIsOut     bool   `json:"attached_is_out"`
	LocalDestination  bool   `json:"local_destination"`
	DestHash          string `json:"dest_hash"`
	DestType          int    `json:"dest_type"`
	PacketType        int    `json:"packet_type"`
	Context           int    `json:"context"`
	ContextFlag       int    `json:"context_flag"`
	TransportType     int    `json:"transport_type"`
	HeaderType        int    `json:"header_type"`
	TransportID       string `json:"transport_id,omitempty"`
	RepackTransportID string `json:"repack_transport_id,omitempty"`
	Hops              int    `json:"hops"`
	Plaintext         string `json:"plaintext"`
	PathHops          *int   `json:"path_hops"`
	NextHop           string `json:"next_hop,omitempty"`
}

// outboundVectorResult is Python's answer for one scenario.
type outboundVectorResult struct {
	Sent   bool     `json:"sent"`
	Frames []string `json:"frames"`
	Err    string   `json:"err"`
}

// pythonOutboundVectorScript runs the real Python Transport.outbound against a
// stub owner and a recording transmit, so the transmit/no-transmit decision and
// the exact frame bytes come from the reference implementation.
// RNS/Transport.py:1109-1355 is the whole function: the known-path branch with
// its two header-insert rules is :1144-1193, and the broadcast/attached fallback
// is :1196-1355.
const pythonOutboundVectorScript = `
import json, sys, threading
import RNS
from RNS import Transport
from RNS.Packet import Packet as PacketCls

class Owner:
    def __init__(self, shared):
        self.is_connected_to_shared_instance = shared

class StubInterface:
    """Mirrors the Go vector's stub: full mode, with the case's OUT flag."""
    def __init__(self, name, out=True):
        self.name = name
        self.OUT = out
        self.IN = True
        self.bitrate = 1000000
        self.mode = RNS.Interfaces.Interface.Interface.MODE_FULL
        self.announces_from_internal = False
        self.announces_to_internal = False
        self.announce_cap = RNS.Reticulum.ANNOUNCE_CAP
        self.announce_allowed_at = 0
        self.announce_queue = []
        self.HW_MTU = 500
        self.announce_sent = 0
    # size= is accepted because RNS 1.5.0 added it to these interface
    # callbacks (interface.sent_announce(size=len(raw))); 1.4.2 calls them
    # with no argument. Tolerating both keeps the vectors version-agnostic.
    def sent_announce(self, size=None): self.announce_sent += 1
    def sent_path_request(self, size=None): pass

class MockDest:
    def __init__(self, h, dtype, mtu=500):
        self.hash = h
        self.link_id = h
        self.type = dtype
        self.mtu = mtu
        # A LINK destination carries its own state and its own attached
        # interface; the broadcast branch reads both (Transport.py:1401-1403).
        self.status = None
        self.attached_interface = None
    def encrypt(self, data): return bytes(data)
    def decrypt(self, data): return bytes(data)

cases = json.load(open(sys.argv[1]))
out = []
for c in cases:
    Transport.path_table = {}
    Transport.path_table_lock = threading.RLock()
    Transport.owner = Owner(c["shared_instance"])
    Transport.identity = RNS.Identity.from_bytes(bytes(range(64)))
    Transport.receipts = []
    Transport.receipts_lock = threading.RLock()
    Transport.packet_hashlist = set()
    Transport.destinations_map = {}
    Transport.destinations_map_lock = threading.RLock()
    Transport.held_announces = {}

    iface = StubInterface("out", c.get("interface_is_out", True))
    Transport.interfaces = [iface] if c.get("with_interface") else []

    attached = None
    if c.get("attached_interface"):
        attached = StubInterface("attached", c.get("attached_is_out", True))
        Transport.interfaces.append(attached)

    dest_hash = bytes.fromhex(c["dest_hash"])
    if c.get("local_destination"):
        Transport.destinations_map[dest_hash] = object()
        # next_hop_interface falls back to the shared instance's interface for a
        # locally-registered destination (Transport.py:2750-2758). There is no
        # shared instance in this scenario, so it returns None -- which is what
        # the real function returns here too; the stub avoids needing a live
        # Reticulum singleton.
        Transport.next_hop_interface = staticmethod(lambda dh: None)
    if c.get("path_hops") is not None:
        Transport.path_table[dest_hash] = [0.0, bytes.fromhex(c.get("next_hop") or ("00"*16)),
                                           c["path_hops"], 0.0, [], iface, None]

    recorded = []
    Transport.transmit = lambda interface, raw: recorded.append(interface.name + ":" + raw.hex())

    dest = MockDest(dest_hash, c["dest_type"])
    if c["dest_type"] == RNS.Destination.LINK:
        dest.status = RNS.Link.ACTIVE
        dest.attached_interface = attached
    p = PacketCls(dest, bytes.fromhex(c["plaintext"]),
                  packet_type=c["packet_type"], context=c["context"],
                  transport_type=c["transport_type"], header_type=c["header_type"],
                  transport_id=bytes.fromhex(c["transport_id"]) if c.get("transport_id") else None,
                  attached_interface=attached,
                  create_receipt=False)
    p.hops = c.get("hops", 0)
    rec = {"sent": False, "frames": [], "err": ""}
    try:
        p.pack()
        if c.get("repack_transport_id"):
            p.header_type = PacketCls.HEADER_2
            p.transport_type = RNS.Transport.TRANSPORT
            p.transport_id = bytes.fromhex(c["repack_transport_id"])
            p.pack()
        rec["sent"] = bool(Transport.outbound(p))
        rec["frames"] = recorded
    except Exception as e:
        rec["err"] = "%s: %s" % (type(e).__name__, e)
    out.append(rec)
print(json.dumps(out))
`

// outboundVectorCase is one Outbound scenario in the Go table.
type outboundVectorCase struct {
	name           string
	pathHops       *int
	sharedInstance bool
	withInterface  bool
	destType       int
	packetType     int
	context        int
	// header2Announce builds the packet outright as HEADER_2 with the
	// TRANSPORT transport type, the shape a relay gives an announce it is
	// forwarding (RNS/Transport.py:2007-2020). It is the only packet type
	// Python can frame as HEADER_2 (RNS/Packet.py:226-234).
	header2Announce bool
	// interfaceInOnly registers an interface that cannot transmit. Python's
	// broadcast loop is gated on `if interface.OUT:` (Transport.py:1197), so
	// nothing leaves by it, whatever the packet type.
	interfaceInOnly bool
	// localDestination registers the destination locally, which is the state a
	// node announcing its own destination is in.
	localDestination bool
	// attachedInOnly stamps an attached interface that cannot transmit, which a
	// link, a proof or a synthesized tunnel can be attached to.
	attachedInOnly bool
	citation       string
}

// acceptedOutboundDiffs lists reviewed scenarios where Go and Python
// deliberately differ. It is empty: the two entries it held were fixed rather
// than tolerated. Outbound now leaves the sent flag alone when no interface
// accepted the packet (matching Packet.py:299-303), and its broadcast fallback
// runs the announce exclusion chain (matching Transport.py:1196-1345). A future
// entry needs a reason on its line, so an unexplained difference cannot pass.
var acceptedOutboundDiffs = map[string]string{}

// framingOutboundCases returns the Outbound scenarios.
func framingOutboundCases() []outboundVectorCase {
	hops1, hops2, hops3 := 1, 2, 3
	return []outboundVectorCase{
		{
			name: "no_path/data/single", withInterface: true, destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1387-1394 (broadcast fallback for an unknown destination)",
		},
		{
			name: "no_path/no_interface/data/single", destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1387-1394 (no OUT interface: nothing transmitted)",
		},
		{
			name: "path_hops2/data/single", pathHops: &hops2, withInterface: true, destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1149-1162 (insert a transport header when hops > 1)",
		},
		{
			name: "path_hops3/data/single", pathHops: &hops3, withInterface: true, destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1149-1162 (insert a transport header when hops > 1)",
		},
		{
			name: "path_hops1/data/single", pathHops: &hops1, withInterface: true, destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1184-1193 (one hop, no shared instance: transmit unchanged)",
		},
		{
			name: "path_hops1/shared_instance/data/single", pathHops: &hops1, sharedInstance: true, withInterface: true,
			destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1172-1183 (one hop behind a shared instance: insert a transport header)",
		},
		{
			name: "path_hops2/shared_instance/data/single", pathHops: &hops2, sharedInstance: true, withInterface: true,
			destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1149-1162 (hops > 1 inserts whether or not a shared instance is present)",
		},
		{
			name: "header2_announce/transport/hops2", pathHops: &hops2, withInterface: true, destType: DestinationSingle,
			packetType: PacketAnnounce, header2Announce: true,
			citation: "RNS/Transport.py:2007-2020 (an announce forwarded as HEADER_2 with TRANSPORT) + :1387-1394 (broadcast fallback)",
		},
		{
			name: "header2_announce/transport/no_path", withInterface: true, destType: DestinationSingle,
			packetType: PacketAnnounce, header2Announce: true,
			citation: "RNS/Transport.py:2007-2020 (an announce forwarded as HEADER_2 with TRANSPORT) + :1387-1394 (broadcast fallback)",
		},
		{
			name: "path_hops2/plain_destination", pathHops: &hops2, withInterface: true, destType: DestinationPlain,
			packetType: PacketData,
			citation:   "RNS/Transport.py:1144 (PLAIN destinations skip the known-path branch)",
		},
		{
			name: "path_hops2/group_destination", pathHops: &hops2, withInterface: true, destType: DestinationGroup,
			packetType: PacketData,
			citation:   "RNS/Transport.py:1144 (GROUP destinations skip the known-path branch)",
		},
		{
			name: "path_hops2/announce", pathHops: &hops2, withInterface: true, destType: DestinationSingle,
			packetType: PacketAnnounce,
			citation:   "RNS/Transport.py:1144 (ANNOUNCE skips the known-path branch; TestFramingVectorsAnnounceExclusions covers the exclusion chain)",
		},
		{
			name: "path_hops2/keepalive", pathHops: &hops2, withInterface: true, destType: DestinationSingle,
			packetType: PacketData, context: ContextKeepalive,
			citation: "RNS/Transport.py:1149-1162 (the context does not change the insert rule)",
		},
		{
			name: "no_path/data/in_only_interface", withInterface: true, interfaceInOnly: true,
			destType: DestinationSingle, packetType: PacketData,
			citation: "RNS/Transport.py:1197 (the broadcast loop only uses interfaces with OUT set)",
		},
		{
			name: "no_path/announce/in_only_interface", withInterface: true, interfaceInOnly: true,
			destType: DestinationSingle, packetType: PacketAnnounce,
			citation: "RNS/Transport.py:1197 (the OUT gate applies to announces too)",
		},
		{
			name: "no_path/announce/local_destination", withInterface: true, localDestination: true,
			destType: DestinationSingle, packetType: PacketAnnounce,
			citation: "RNS/Transport.py:1287-1294 (a locally-originated announce is always transmitted, hops 0, no cap)",
		},
		{
			name: "no_path/announce/unknown_destination", withInterface: true,
			destType: DestinationSingle, packetType: PacketAnnounce,
			citation: "RNS/Transport.py:1220 (announce for an unknown, non-local destination with no next hop is blocked)",
		},
		{
			name: "attached/in_only_interface", withInterface: true, attachedInOnly: true,
			destType: DestinationLink, packetType: PacketData, context: ContextKeepalive,
			citation: "RNS/Transport.py:1197,1403 (the OUT gate also filters the attached interface)",
		},
		{
			name: "attached/out_interface", withInterface: true,
			destType: DestinationLink, packetType: PacketData, context: ContextKeepalive,
			citation: "RNS/Transport.py:1403 (a link packet leaves by its attached interface and no other)",
		},
	}
}

// TestFramingVectorsOutboundFraming pins Outbound's transmit decision and the
// exact frame it emits against the Python reference, for both header-insert
// rules and the broadcast fallback. The pre-stamped HEADER_2 cases are the
// regression pin for the "two transport ids in one frame" bug, now derived from
// Python rather than from a hand-written expectation.
func TestFramingVectorsOutboundFraming(t *testing.T) {
	t.Parallel()

	cases := framingOutboundCases()
	req := make([]outboundVectorRequest, 0, len(cases))
	for _, c := range cases {
		r := outboundVectorRequest{
			SharedInstance:    c.sharedInstance,
			WithInterface:     c.withInterface,
			InterfaceIsOut:    !c.interfaceInOnly,
			AttachedInterface: c.attachedInOnly || strings.Contains(c.name, "attached/"),
			AttachedIsOut:     !c.attachedInOnly,
			LocalDestination:  c.localDestination,
			DestHash:          hex.EncodeToString(framingVectorDestHash),
			DestType:          c.destType,
			PacketType:        c.packetType,
			Context:           c.context,
			TransportType:     TransportBroadcast,
			HeaderType:        Header1,
			Plaintext:         hex.EncodeToString([]byte(framingVectorPayload)),
			PathHops:          c.pathHops,
		}
		if c.pathHops != nil {
			r.NextHop = hex.EncodeToString(framingVectorTransportID)
		}
		if c.header2Announce {
			r.HeaderType = Header2
			r.TransportType = TransportForward
			r.TransportID = hex.EncodeToString(framingVectorTransportID)
		}
		req = append(req, r)
	}

	var results []outboundVectorResult
	runFramingVectorPython(t, pythonOutboundVectorScript, req, &results)
	if len(results) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(results), len(cases))
	}

	for i, c := range cases {
		res := results[i]
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if reason, accepted := acceptedOutboundDiffs[c.name]; accepted {
				t.Logf("accepted divergence from Python: %v", reason)
				return
			}

			goSent, goFrames, goFrameNames, err := outboundVectorGo(t, c)
			if res.Err != "" {
				t.Fatalf("python failed for this case: %v", res.Err)
			}
			if err != nil {
				t.Fatalf("go Outbound: %v", err)
			}

			if len(goFrames) != len(res.Frames) {
				t.Fatalf("transmitted %v frame(s), python transmitted %v (%v):\n  go     = %x\n  python = %v",
					len(goFrames), len(res.Frames), c.citation, goFrames, res.Frames)
			}
			for j, want := range res.Frames {
				name, hexPart, ok := strings.Cut(want, ":")
				if !ok {
					t.Fatalf("python frame %q is not name:hex", want)
				}
				gotName := goFrameNames[j]
				if gotName != name {
					t.Errorf("frame %v left by interface %v, python sent it by %v (%v)", j, gotName, name, c.citation)
					continue
				}
				wantBytes := mustDecodeHex(t, hexPart)
				if !bytes.Equal(goFrames[j], wantBytes) {
					t.Errorf("frame %v differs from Python (%v):\n  go     = %x\n  python = %x",
						j, c.citation, goFrames[j], wantBytes)
				}
			}
			if res.Sent != goSent {
				t.Errorf("sent flag differs from Python (%v): go=%v python=%v", c.citation, goSent, res.Sent)
			}
		})
	}
}

// outboundVectorGo drives the Go Outbound for one scenario with a real
// TransportSystem, a path-table entry and a capturing interface, and returns
// every frame the interface received together with the packet's sent flag.
func outboundVectorGo(t *testing.T, c outboundVectorCase) (bool, [][]byte, []string, error) {
	t.Helper()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)
	ts.SetConnectedToSharedInstance(c.sharedInstance)

	iface := newFramingVectorInterface("out", interfaces.ModeFull)
	iface.isOut = !c.interfaceInOnly
	if c.withInterface {
		ts.RegisterInterface(iface)
	}
	var attached *framingVectorInterface
	if c.attachedInOnly || strings.Contains(c.name, "attached/") {
		attached = newFramingVectorInterface("attached", interfaces.ModeFull)
		attached.isOut = !c.attachedInOnly
		ts.RegisterInterface(attached)
	}
	if c.localDestination {
		// Python models "the destination is registered locally" by re-pointing a
		// destination's hash at the announcement's hash (Transport.py:1998 does
		// exactly that when it rebuilds an announce). The same re-pointing is
		// needed here so both sides see the vector's destination as local.
		d := mustTestNewDestination(t, ts, ts.identity, DestinationIn, DestinationSingle, "framing", "local")
		ts.mu.Lock()
		delete(ts.destinationsMap, string(d.Hash))
		d.Hash = append([]byte(nil), framingVectorDestHash...)
		ts.mu.Unlock()
		ts.RegisterDestination(d)
	}
	if c.pathHops != nil {
		now := time.Now()
		ts.mu.Lock()
		ts.pathTable[string(framingVectorDestHash)] = &PathEntry{
			Interface: iface,
			Hops:      *c.pathHops,
			Timestamp: now,
			Expires:   now.Add(time.Hour),
			NextHop:   append([]byte(nil), framingVectorTransportID...),
		}
		ts.mu.Unlock()
	}

	dest := &identityEncryptDestination{hash: framingVectorDestHash, typ: c.destType}
	p := NewPacketWithTransport(ts, dest, []byte(framingVectorPayload))
	p.PacketType = c.packetType
	p.Context = c.context
	p.HeaderType = Header1
	if attached != nil {
		p.AttachedInterface = attached
	}
	if c.header2Announce {
		p.HeaderType = Header2
		p.TransportType = TransportForward
		p.TransportID = framingVectorTransportID
	}
	if err := p.Pack(); err != nil {
		return false, nil, nil, err
	}

	if err := ts.Outbound(p); err != nil {
		return false, nil, nil, err
	}

	// Frames from the outbound interface come first, then the attached one; only
	// one of the two can receive a packet.
	var frames [][]byte
	var names []string
	for _, fi := range []*framingVectorInterface{iface, attached} {
		if fi == nil {
			continue
		}
		for _, f := range fi.SentFrames() {
			frames = append(frames, f)
			names = append(names, fi.Name())
		}
	}
	return p.Sent, frames, names, nil
}

// ---------------------------------------------------------------------------
// Announce broadcast exclusions
// ---------------------------------------------------------------------------

// announceExclusionRequest is one cell of the announce-exclusion matrix.
type announceExclusionRequest struct {
	OutMode                  int  `json:"out_mode"`
	FromMode                 *int `json:"from_mode"`
	LocalDestination         bool `json:"local_destination"`
	Hops                     int  `json:"hops"`
	OutAnnouncesFromInternal bool `json:"out_announces_from_internal"`
	FromAnnouncesToInternal  bool `json:"from_announces_to_internal"`
}

// announceExclusionCase pairs a matrix cell with its name.
type announceExclusionCase struct {
	name string
	c    announceExclusionRequest
}

// pythonAnnounceExclusionScript asks the reference implementation whether an
// ANNOUNCE would be transmitted on the given outbound interface, given the
// next-hop interface the announce arrived from. It runs the real
// Transport.outbound exclusion chain (RNS/Transport.py:1216-1345) with a
// recording transmit and a stubbed next_hop_interface.
const pythonAnnounceExclusionScript = `
import json, sys, threading
import RNS
from RNS import Transport
from RNS.Packet import Packet as PacketCls

class Owner:
    def __init__(self): self.is_connected_to_shared_instance = False

class StubInterface:
    def __init__(self, name, mode, afi, ati):
        self.name = name
        self.OUT = True
        self.IN = True
        self.bitrate = 1000000
        self.mode = mode
        self.announces_from_internal = afi
        self.announces_to_internal = ati
        self.announce_cap = RNS.Reticulum.ANNOUNCE_CAP
        self.announce_allowed_at = 0
        self.announce_queue = []
        self.HW_MTU = 500
    # size= is accepted because RNS 1.5.0 passes it; 1.4.2 passes nothing.
    def sent_announce(self, size=None): pass
    def sent_path_request(self, size=None): pass

class MockDest:
    def __init__(self, h):
        self.hash = h
        self.link_id = h
        self.type = RNS.Destination.SINGLE
        self.mtu = 500
    def encrypt(self, data): return bytes(data)
    def decrypt(self, data): return bytes(data)

cases = json.load(open(sys.argv[1]))
out = []
for c in cases:
    Transport.path_table = {}
    Transport.path_table_lock = threading.RLock()
    Transport.owner = Owner()
    Transport.identity = RNS.Identity.from_bytes(bytes(range(64)))
    Transport.packet_hashlist = set()
    Transport.destinations_map = {}
    Transport.destinations_map_lock = threading.RLock()
    Transport.held_announces = {}

    dest_hash = bytes(range(16))
    out_iface = StubInterface("out", c["out_mode"], c["out_announces_from_internal"], False)
    from_iface = None
    if c["from_mode"] is not None:
        from_iface = StubInterface("from", c["from_mode"], False, c["from_announces_to_internal"])
    if c["local_destination"]:
        Transport.destinations_map[dest_hash] = object()
    Transport.next_hop_interface = staticmethod(lambda dh: from_iface)
    Transport.interfaces = [out_iface]

    recorded = []
    Transport.transmit = lambda interface, raw: recorded.append(raw.hex())

    p = PacketCls(MockDest(dest_hash), b"announce-payload", packet_type=PacketCls.ANNOUNCE,
                  header_type=PacketCls.HEADER_1, create_receipt=False)
    p.hops = c["hops"]
    rec = {"transmitted": False, "err": ""}
    try:
        p.pack()
        Transport.outbound(p)
        rec["transmitted"] = len(recorded) > 0
    except Exception as e:
        rec["err"] = "%s: %s" % (type(e).__name__, e)
    out.append(rec)
print(json.dumps(out))
`

// acceptedAnnounceDiffs lists reviewed announce-exclusion divergences, keyed by
// case name. It is empty: the one entry it held (Python reading the next hop's
// announces_to_internal while the port read the outbound interface's) was fixed
// in shouldTransmitAnnounce branch B4b, and the table now matches Python for
// every cell in the matrix. A future entry needs a reason on its line, so an
// unexplained difference cannot pass.
var acceptedAnnounceDiffs = map[string]string{}

// announceExclusionCases enumerates the elif chain's inputs: outbound mode ×
// next-hop mode (or absent) × local/remote destination × received hops, plus
// the cells that separate the announces_from_internal and
// announces_to_internal options.
func announceExclusionCases() []announceExclusionCase {
	modes := []struct {
		name string
		val  int
	}{
		{"full", interfaces.ModeFull},
		{"p2p", interfaces.ModePointToPoint},
		{"ap", interfaces.ModeAccessPoint},
		{"roaming", interfaces.ModeRoaming},
		{"boundary", interfaces.ModeBoundary},
		{"gateway", interfaces.ModeGateway},
		{"internal", interfaces.ModeInternal},
	}

	var out []announceExclusionCase
	for _, om := range modes {
		for _, fm := range modes {
			for _, local := range []bool{false, true} {
				for _, hops := range []int{0, 1} {
					from := fm.val
					out = append(out, announceExclusionCase{
						name: fmt.Sprintf("out=%v/from=%v/local=%v/hops=%v", om.name, fm.name, local, hops),
						c: announceExclusionRequest{
							OutMode: om.val, FromMode: &from, LocalDestination: local, Hops: hops,
						},
					})
				}
			}
		}
		for _, local := range []bool{false, true} {
			for _, hops := range []int{0, 1} {
				out = append(out, announceExclusionCase{
					name: fmt.Sprintf("out=%v/from=none/local=%v/hops=%v", om.name, local, hops),
					c: announceExclusionRequest{
						OutMode: om.val, FromMode: nil, LocalDestination: local, Hops: hops,
					},
				})
			}
		}
	}

	internal := interfaces.ModeInternal
	boundary := interfaces.ModeBoundary
	// announces_from_internal on the OUTBOUND interface, with an internal next
	// hop (RNS/Transport.py:1222).
	out = append(out, announceExclusionCase{
		name: "internal_out/internal_from/afi_out",
		c: announceExclusionRequest{
			OutMode: internal, FromMode: &internal, Hops: 1, OutAnnouncesFromInternal: true,
		},
	})
	// The boundary next hop: Python reads the NEXT HOP's announces_to_internal
	// (RNS/Transport.py:1237), so these cells separate the two readings.
	out = append(out,
		announceExclusionCase{
			name: "internal_out/boundary_from/from_ati",
			c: announceExclusionRequest{
				OutMode: internal, FromMode: &boundary, Hops: 1, FromAnnouncesToInternal: true,
			},
		},
		announceExclusionCase{
			name: "internal_out/boundary_from/from_ati_clear",
			c: announceExclusionRequest{
				OutMode: internal, FromMode: &boundary, Hops: 1,
			},
		},
	)
	return out
}

// TestFramingVectorsAnnounceExclusions pins announceTransmitDecision against
// Python's announce-broadcast elif chain (RNS/Transport.py:1216-1345). Python's
// announceCapped is compared as "transmits now": the vector's stub interface is
// fresh (announce_allowed_at = 0, empty queue), which always admits the first
// announce, and that is exactly the state the Python side is driven in.
func TestFramingVectorsAnnounceExclusions(t *testing.T) {
	t.Parallel()

	cases := announceExclusionCases()
	req := make([]announceExclusionRequest, 0, len(cases))
	for _, c := range cases {
		req = append(req, c.c)
	}

	var results []struct {
		Transmitted bool   `json:"transmitted"`
		Err         string `json:"err"`
	}
	runFramingVectorPython(t, pythonAnnounceExclusionScript, req, &results)
	if len(results) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(results), len(cases))
	}

	for i, c := range cases {
		res := results[i]
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if res.Err != "" {
				t.Fatalf("python failed for this case: %v", res.Err)
			}

			outIface := newFramingVectorInterface("out", c.c.OutMode)
			outIface.fromInt = c.c.OutAnnouncesFromInternal
			var fromIface interfaces.Interface
			if c.c.FromMode != nil {
				fi := newFramingVectorInterface("from", *c.c.FromMode)
				fi.SetAnnouncesToInternal(c.c.FromAnnouncesToInternal)
				fromIface = fi
			}

			ts := NewTransportSystem(nil)
			decision := ts.shouldTransmitAnnounce(outIface, fromIface, c.c.LocalDestination, c.c.Hops)
			goTransmitted := decision != announceBlock

			if res.Transmitted != goTransmitted {
				if reason, ok := acceptedAnnounceDiffs[c.name]; ok {
					t.Logf("accepted divergence from Python: %v", reason)
					return
				}
				t.Errorf("announce transmit decision differs from Python (RNS/Transport.py:1216-1345):\n"+
					"  go     = %v (decision %v)\n  python = %v", goTransmitted, decision, res.Transmitted)
			}
		})
	}
}

// TestFramingVectorsPrestampedHeader2NotRestamped is the regression pin for the
// "two transport ids in one frame" bug, driven from a genuine received frame
// rather than from a hand-built packet. A frame that arrives as HEADER_2
// already carries the transport id of the hop it was addressed to; Python
// inserts a transport header only into a HEADER_1 packet, and only when the
// packet is a HEADER_1 one is anything transmitted, so a HEADER_2 packet gains
// no second id and nothing goes out (RNS/Transport.py:1149 and :1174 both gate
// their insert on `if packet.header_type == RNS.Packet.HEADER_1`).
//
// Python's own outbound cannot be driven for this case: its pack() assigns a
// ciphertext only for a HEADER_2 ANNOUNCE (RNS/Packet.py:226-234), and a
// locally-generated packet always has a Destination, so a HEADER_2 non-announce
// packet only ever reaches Python's Transport.inbound, which performs the same
// HEADER_1-gated insert. The decision is therefore pinned here with the Python
// citation in the failure messages; TestFramingVectorsOutboundFraming covers
// the frames Python can produce.
func TestFramingVectorsPrestampedHeader2NotRestamped(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		hops       int
		sharedInst bool
	}{
		{name: "hops2", hops: 2},
		{name: "hops3", hops: 3},
		{name: "hops1_shared_instance", hops: 1, sharedInst: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			// Build a genuine HEADER_2 DATA frame, as a relay emits it and as
			// the next hop receives it.
			dest := &identityEncryptDestination{hash: framingVectorDestHash, typ: DestinationSingle}
			built := NewPacketWithTransport(nil, dest, []byte(framingVectorPayload))
			built.PacketType = PacketData
			built.HeaderType = Header2
			built.TransportType = TransportForward
			built.TransportID = framingVectorTransportID
			built.Hops = 4
			if err := built.Pack(); err != nil {
				t.Fatalf("build HEADER_2 frame: %v", err)
			}

			ts := NewTransportSystem(nil)
			ts.identity = mustTestNewIdentity(t, true)
			ts.SetEnabled(true)
			ts.SetConnectedToSharedInstance(c.sharedInst)
			iface := newFramingVectorInterface("prestamped-out", interfaces.ModeFull)
			ts.RegisterInterface(iface)
			now := time.Now()
			ts.mu.Lock()
			ts.pathTable[string(framingVectorDestHash)] = &PathEntry{
				Interface: iface,
				Hops:      c.hops,
				Timestamp: now,
				Expires:   now.Add(time.Hour),
				NextHop:   append([]byte(nil), framingVectorRepackNextHop...),
			}
			ts.mu.Unlock()

			p := NewPacketFromRaw(append([]byte(nil), built.Raw...))
			if err := p.Unpack(); err != nil {
				t.Fatalf("unpack HEADER_2 frame: %v", err)
			}
			if p.HeaderType != Header2 {
				t.Fatalf("unpacked header type = %v, want HEADER_2", p.HeaderType)
			}
			// The frame is already framed, exactly as a just-received packet is.
			p.Packed = true

			if err := ts.Outbound(p); err != nil {
				t.Fatalf("Outbound: %v", err)
			}

			if frames := iface.SentFrames(); len(frames) != 0 {
				t.Fatalf("transmitted %v frame(s) for a packet that already carries a transport id (RNS/Transport.py:1149,1174 "+
					"insert only into a HEADER_1 packet):\n  %x\nA frame with two transport ids makes the next hop read the second "+
					"id as the destination and drop the packet", len(frames), frames)
			}
			if p.Sent {
				t.Errorf("packet marked sent although nothing was transmitted")
			}
		})
	}
}
