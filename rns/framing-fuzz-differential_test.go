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
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// Differential fuzzing at the packet boundary: the same corpus is parsed by the
// Python reference and by the port, and the parsed state is compared field by
// field. Golden vectors only cover cases someone thought of; this covers the
// cases nobody did.
//
// The corpus is deterministic (a fixed PRNG seed, plus structured near-miss
// frames and mutations of well-formed frames), so a failure reproduces exactly.
// The whole corpus goes to Python in ONE invocation and the comparison happens
// in Go, which keeps the runtime bounded and CI-friendly.
//
// On a difference the harness minimises the input before reporting it: bytes are
// dropped and zeroed while the difference persists, so the failure names the
// smallest frame that still diverges.

// fuzzCorpusSize bounds the corpus so the harness stays fast.
const fuzzCorpusSize = 400

// parserDifferentialRequest is the corpus handed to Python.
type parserDifferentialRequest struct {
	Frames []string `json:"frames"`
}

// parserState is the parsed packet state both sides must agree on. Pointers mark
// fields that only exist for one header type.
type parserState struct {
	Accepted        bool    `json:"accepted"`
	Flags           int     `json:"flags"`
	Hops            int     `json:"hops"`
	HeaderType      int     `json:"header_type"`
	PacketType      int     `json:"packet_type"`
	Context         int     `json:"context"`
	TransportType   int     `json:"transport_type"`
	DestinationType int     `json:"destination_type"`
	TransportID     *string `json:"transport_id"`
	DestinationHash string  `json:"destination_hash"`
	Data            string  `json:"data"`
	PacketHash      string  `json:"packet_hash"`
}

// pythonParserDifferentialScript parses each frame with the reference Packet
// class (RNS/Packet.py:242-290) and reports the resulting state, or
// accepted=false when unpack() returns False.
const pythonParserDifferentialScript = `
import json, sys
import RNS
from RNS.Packet import Packet as PacketCls

frames = json.load(open(sys.argv[1]))["frames"]
out = []
for raw_hex in frames:
    raw = bytes.fromhex(raw_hex)
    rec = {"accepted": False, "flags": 0, "hops": 0, "header_type": 0,
           "packet_type": 0, "context": 0, "transport_type": 0,
           "destination_type": 0, "transport_id": None,
           "destination_hash": "", "data": "", "packet_hash": ""}
    try:
        p = PacketCls(None, raw)
        if p.unpack():
            rec["accepted"] = True
            rec["flags"] = p.flags
            rec["hops"] = p.hops
            rec["header_type"] = p.header_type
            rec["packet_type"] = p.packet_type
            rec["context"] = p.context
            rec["transport_type"] = p.transport_type
            rec["destination_type"] = p.destination_type
            rec["transport_id"] = p.transport_id.hex() if p.transport_id is not None else None
            rec["destination_hash"] = p.destination_hash.hex()
            rec["data"] = p.data.hex()
            rec["packet_hash"] = p.packet_hash.hex()
    except Exception:
        pass
    out.append(rec)
print(json.dumps(out))
`

// TestFramingDifferentialParserFuzz feeds a deterministic corpus of frames to
// both parsers and requires identical parsed state, minimising any difference
// before it is reported.
func TestFramingDifferentialParserFuzz(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	corpus := fuzzCorpus(t)

	req := parserDifferentialRequest{Frames: make([]string, len(corpus))}
	for i, frame := range corpus {
		req.Frames[i] = hex.EncodeToString(frame)
	}
	pyStates := runPythonParser(t, pythonParserDifferentialScript, req)
	if len(pyStates) != len(corpus) {
		t.Fatalf("python returned %v states for %v frames", len(pyStates), len(corpus))
	}

	accepted := 0
	for i, frame := range corpus {
		goState := goParseState(frame)
		if !statesEqual(goState, pyStates[i]) {
			small := minimiseDivergentFrame(t, frame, goState, pyStates[i])
			t.Errorf("parser divergence on frame %x:\n  go     = %+v\n  python = %+v\n"+
				"minimised frame (%v bytes): %x\n"+
				"Turn this into a Pillar 1 vector in framing-vectors_test.go: add the frame to the case table "+
				"with the Python source line it violates, then fix the port.",
				frame, goState, pyStates[i], len(small), small)
		}
		if pyStates[i].Accepted {
			accepted++
		}
	}
	t.Logf("fuzz corpus: %v frames, %v accepted by both parsers", len(corpus), accepted)
}

// fuzzCorpus builds the deterministic corpus: random bytes, structured near-miss
// frames, and single-byte mutations of well-formed frames of every header type.
func fuzzCorpus(t *testing.T) [][]byte {
	t.Helper()

	var corpus [][]byte
	rng := rand.New(rand.NewSource(20261017))

	// Random bytes across the interesting length range.
	for i := range 120 {
		n := rng.Intn(60)
		if i%7 == 0 {
			n = rng.Intn(400)
		}
		frame := make([]byte, n)
		for j := range frame {
			frame[j] = byte(rng.Intn(256))
		}
		corpus = append(corpus, frame)
	}

	// Structured near-miss frames: a well-formed skeleton with each field
	// varied, so every parse branch is entered with plausible bytes.
	dst := sequentialBytes(0x00, 16)
	tid := sequentialBytes(0x10, 16)
	payload := []byte("fuzz-payload-0123456789")
	for _, headerType := range []int{Header1, Header2} {
		for _, hops := range []int{0, 1, 127, 128, 200, 255} {
			for _, flagsLow := range []int{0x00, 0x01, 0x02, 0x03, 0x0C, 0x0D, 0x0E, 0x0F} {
				flags := byte((headerType << 6) | flagsLow)
				frame := []byte{flags, byte(hops)}
				if headerType == Header2 {
					frame = append(frame, tid...)
				}
				frame = append(frame, dst...)
				frame = append(frame, 0x00)
				frame = append(frame, payload...)
				corpus = append(corpus, frame)

				// Truncations of the same frame, which is where the length
				// checks differ.
				for _, cut := range []int{0, 1, 2, 15, 17, 18, 19, 34, 35} {
					if cut < len(frame) {
						corpus = append(corpus, append([]byte(nil), frame[:len(frame)-cut]...))
					}
				}
			}
		}
	}

	// Single-byte mutations of a small set of well-formed frames, so the corpus
	// contains frames that are one bit away from legal.
	base := [][]byte{
		append(append(append([]byte{0x00, 0x00}, dst...), 0x00), payload...),
		append(append(append(append([]byte{0x50, 0x01}, tid...), dst...), 0x0b), payload...),
		append(append(append(append([]byte{0x41, 0x02}, tid...), dst...), 0x00), payload...),
		append(append(append([]byte{0x0f, 0x00}, dst...), 0xff), payload...),
	}
	for _, frame := range base {
		for pos := 0; pos < len(frame) && len(corpus) < fuzzCorpusSize; pos++ {
			for _, mutation := range []byte{0x00, 0x01, 0x7F, 0x80, 0xFF} {
				mutated := append([]byte(nil), frame...)
				mutated[pos] = mutation
				corpus = append(corpus, mutated)
			}
		}
	}

	if len(corpus) > fuzzCorpusSize {
		corpus = corpus[:fuzzCorpusSize]
	}
	return corpus
}

// goParseState parses a frame with the port's own Unpack.
func goParseState(frame []byte) parserState {
	state := parserState{}
	if len(frame) == 0 {
		return state
	}
	p := NewPacketFromRaw(frame)
	if err := p.Unpack(); err != nil {
		return state
	}
	state.Accepted = true
	state.Flags = int(p.Flags)
	state.Hops = p.Hops
	state.HeaderType = p.HeaderType
	state.PacketType = p.PacketType
	state.Context = p.Context
	state.TransportType = p.TransportType
	state.DestinationType = p.DestinationType
	if p.TransportID != nil {
		tid := hex.EncodeToString(p.TransportID)
		state.TransportID = &tid
	}
	state.DestinationHash = hex.EncodeToString(p.DestinationHash)
	state.Data = hex.EncodeToString(p.Data)
	state.PacketHash = hex.EncodeToString(p.PacketHash)
	return state
}

// statesEqual compares every field of two parsed states.
func statesEqual(a, b parserState) bool {
	if a.Accepted != b.Accepted {
		return false
	}
	if !a.Accepted {
		return true
	}
	return a.Flags == b.Flags &&
		a.Hops == b.Hops &&
		a.HeaderType == b.HeaderType &&
		a.PacketType == b.PacketType &&
		a.Context == b.Context &&
		a.TransportType == b.TransportType &&
		a.DestinationType == b.DestinationType &&
		pointerEqual(a.TransportID, b.TransportID) &&
		a.DestinationHash == b.DestinationHash &&
		a.Data == b.Data &&
		a.PacketHash == b.PacketHash
}

func pointerEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// minimiseDivergentFrame shrinks a divergent frame: bytes are dropped and zeroed
// while the divergence persists. The candidates for one round are batched into a
// single Python invocation, so a minimised report costs a handful of processes
// rather than one per candidate.
func minimiseDivergentFrame(t *testing.T, frame []byte, goState, pyState parserState) []byte {
	t.Helper()

	current := append([]byte(nil), frame...)
	for range 12 {
		type candidate struct {
			frame []byte
		}
		var candidates []candidate

		// Drop each byte, longest drop first so the shrink is aggressive.
		for i := range current {
			reduced := make([]byte, 0, len(current)-1)
			reduced = append(reduced, current[:i]...)
			reduced = append(reduced, current[i+1:]...)
			candidates = append(candidates, candidate{frame: reduced})
		}
		// Zero each byte.
		for i := range current {
			zeroed := append([]byte(nil), current...)
			zeroed[i] = 0
			candidates = append(candidates, candidate{frame: zeroed})
		}
		if len(candidates) == 0 {
			break
		}

		req := parserDifferentialRequest{Frames: make([]string, len(candidates))}
		for i, c := range candidates {
			req.Frames[i] = hex.EncodeToString(c.frame)
		}
		states := runPythonParser(t, pythonParserDifferentialScript, req)

		improved := false
		for i, c := range candidates {
			if len(c.frame) > len(current) {
				continue
			}
			if !statesEqual(goParseState(c.frame), states[i]) {
				// Prefer any strictly smaller frame.
				if len(c.frame) < len(current) || !bytes.Equal(c.frame, current) {
					current = c.frame
					improved = true
					break
				}
			}
		}
		if !improved {
			break
		}
	}
	return current
}

// runPythonParser runs the parser script over a corpus and decodes the states.
func runPythonParser(t *testing.T, script string, req parserDifferentialRequest) []parserState {
	t.Helper()

	dir := testutils.TempDir(t, tempDirPrefix)
	path := filepath.Join(dir, "fuzz-frames.json")
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal fuzz frames: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fuzz frames: %v", err)
	}

	stdout := testutils.RunPython(t, script, path)
	var states []parserState
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &states); err != nil {
		t.Fatalf("decode python parser states: %v\nraw stdout:\n%s", err, truncateForLog(stdout))
	}
	return states
}

// truncateForLog bounds a stdout dump in a failure message.
func truncateForLog(s string) string {
	const limit = 2000
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("... (%v bytes total)", len(s))
}

// ---------------------------------------------------------------------------
// Differential filter fuzzing
//
// The parser is only half of the transport boundary: a frame that parses can
// still be refused, and refusing the wrong one costs a path, a link or a
// message. This harness feeds the same corpus to Python's Transport.packet_filter
// and to the port's, in three states — a first sight, a hash already seen, and a
// client of a shared instance — and requires the same verdict from each.
//
// The remaining halves of the boundary — the resulting path and destination
// tables, and the bytes each side emits in response — need a full-transport
// differential. The filter decides whether a frame reaches that machinery at
// all, so it is covered first.

// filterFuzzState is one (frame, state) pair's index into the corpus.
type filterFuzzState struct {
	Frame  int  `json:"frame"`
	Seen   bool `json:"seen"`
	Shared bool `json:"shared"`
}

// pythonFilterFuzzScript evaluates the reference filter over the corpus.
const pythonFilterFuzzScript = `
import json, sys, types
import RNS
from RNS import Transport
from RNS.Packet import Packet as PacketCls

req = json.load(open(sys.argv[1]))
Transport.identity = RNS.Identity.from_bytes(bytes(range(64)))
Transport.local_hops_delta = 0
Transport.local_client_interfaces = []

out = []
for st in req["states"]:
    frame = bytes.fromhex(req["frames"][st["frame"]])
    Transport.owner = types.SimpleNamespace(is_connected_to_shared_instance=st["shared"])
    p = PacketCls(None, frame)
    if not p.unpack():
        out.append({"accepted": None, "err": "unparseable"})
        continue
    Transport.packet_hashlist = set()
    Transport.packet_hashlist_prev = set()
    if st["seen"]:
        Transport.packet_hashlist.add(p.packet_hash)
    try:
        out.append({"accepted": bool(Transport.packet_filter(p)), "err": ""})
    except Exception as e:
        out.append({"accepted": None, "err": "%s: %s" % (type(e).__name__, e)})
print(json.dumps(out))
`

// TestFramingDifferentialFilterFuzz requires the port's filter to reach the same
// verdict as the reference over the whole fuzz corpus, in three states.
func TestFramingDifferentialFilterFuzz(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	corpus := fuzzCorpus(t)
	frames := make([]string, len(corpus))
	for i, frame := range corpus {
		frames[i] = hex.EncodeToString(frame)
	}

	var states []filterFuzzState
	for _, seen := range []bool{false, true} {
		for _, shared := range []bool{false, true} {
			for i := range corpus {
				states = append(states, filterFuzzState{Frame: i, Seen: seen, Shared: shared})
			}
		}
	}

	reqData := struct {
		Frames []string          `json:"frames"`
		States []filterFuzzState `json:"states"`
	}{Frames: frames, States: states}
	var pyStates []struct {
		Accepted *bool  `json:"accepted"`
		Err      string `json:"err"`
	}
	runPythonFilterFuzz(t, pythonFilterFuzzScript, reqData, &pyStates)
	if len(pyStates) != len(states) {
		t.Fatalf("python returned %v verdicts for %v states", len(pyStates), len(states))
	}

	compared, refused := 0, 0
	for i, st := range states {
		if pyStates[i].Accepted == nil {
			// The frame does not parse at all, which the parser harness above
			// already compares field by field; nothing reaches the filter.
			if pyStates[i].Err != "unparseable" {
				t.Fatalf("python failed on corpus frame %v: %v", st.Frame, pyStates[i].Err)
			}
			continue
		}
		ts := NewTransportSystem(nil)
		ts.identity = mustTestNewIdentity(t, true)
		ts.identity.Hash = filterFuzzIdentityHash
		ts.SetConnectedToSharedInstance(st.Shared)
		packet := NewPacketFromRaw(append([]byte(nil), corpus[st.Frame]...))
		if err := packet.Unpack(); err != nil {
			continue
		}
		ts.mu.Lock()
		ts.ensureStateLocked()
		if st.Seen {
			ts.packetHashes[string(packet.PacketHash)] = time.Now()
		}
		got := ts.packetFilter(packet)
		ts.mu.Unlock()

		if got != *pyStates[i].Accepted {
			t.Errorf("filter verdict differs for corpus frame %v (seen=%v shared=%v):\n  go     = %v\n  python = %v\n  frame  = %x",
				st.Frame, st.Seen, st.Shared, got, *pyStates[i].Accepted, corpus[st.Frame])
			continue
		}
		compared++
		if !got {
			refused++
		}
	}
	t.Logf("filter fuzz: %v verdicts compared (%v refusals) across a %v-frame corpus in 4 states",
		compared, refused, len(corpus))
}

// filterFuzzIdentityHash is the identity whose hash the Python script uses, so
// that "the transport id is ours" means the same thing on both sides.
var filterFuzzIdentityHash = func() []byte {
	h, err := hex.DecodeString("8f40c5adb68f25624ae5b214ea767a6ec94d829d3d7b5e1ad1ba6f3e2138285f")
	if err != nil {
		panic(err)
	}
	return h
}()

// runPythonFilterFuzz runs the filter script over a corpus and decodes its verdicts.
func runPythonFilterFuzz(t *testing.T, script string, req any, out any) {
	t.Helper()
	dir := testutils.TempDir(t, tempDirPrefix)
	path := filepath.Join(dir, "filter-fuzz.json")
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal filter fuzz request: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write filter fuzz request: %v", err)
	}
	stdout := testutils.RunPython(t, script, path)
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), out); err != nil {
		t.Fatalf("decode python filter verdicts: %v\nraw stdout:\n%s", err, truncateForLog(stdout))
	}
}
