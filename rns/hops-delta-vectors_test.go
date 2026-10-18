// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// Golden vectors for the local_hops_delta hop-count disguise, derived fresh from
// the Python reference on every run.
//
// Python turns the option on from [reticulum] local_hops_delta
// (RNS/Reticulum.py:256,510-512), draws the per-transport delta in
// Transport.start (Transport.py:240), and then uses two functions plus two
// inline expressions:
//
//	should_apply_delta(packet, interface)      Transport.py:1361-1365
//	mangle_hops(raw, hops, transport_insert)   Transport.py:1367-1369
//	the forwarding ternaries                   Transport.py:1736, :2258, :2343
//	the post-forward mangle                    Transport.py:1692
//
// The script evaluates Python's own code and its own expressions, so a vector's
// expectation is the reference arithmetic rather than a paraphrase of it.

// hopsDeltaRequest is one disguise scenario.
type hopsDeltaRequest struct {
	OwnerShared         bool   `json:"owner_shared"`
	PacketHops          int    `json:"packet_hops"`
	Delta               int    `json:"delta"`
	DestType            int    `json:"dest_type"`
	IfaceLocalClient    bool   `json:"iface_local_client"`
	IfaceSharedInstance bool   `json:"iface_shared_instance"`
	Raw                 string `json:"raw"`
	MangleHops          int    `json:"mangle_hops"`
	FromLocalClient     bool   `json:"from_local_client"`
	LocalCase           bool   `json:"local_case"`
	ToLocalClient       bool   `json:"to_local_client"`
}

// hopsDeltaResult is Python's answer for one scenario.
type hopsDeltaResult struct {
	ShouldApply      bool   `json:"should_apply"`
	ManglePlain      string `json:"mangle_plain"`
	MangleInsert     string `json:"mangle_insert"`
	ForwardedHop     int    `json:"forwarded_hop"`
	ForwardedMangled string `json:"forwarded_mangled"`
	IdentityHash     string `json:"identity_hash"`
}

// pythonHopsDeltaScript evaluates the reference implementation. The two inline
// expressions are copied verbatim from the source, each with its line number, so
// what is compared is Python's own arithmetic.
const pythonHopsDeltaScript = `
import json, sys, types
import RNS
from RNS import Transport

class Dest:
    def __init__(self, t): self.type = t

class Packet:
    def __init__(self, hops, dtype):
        self.hops = hops
        self.destination = Dest(dtype)

class Iface:
    def __init__(self, shared_instance=False):
        if shared_instance: self.is_local_shared_instance = True

cases = json.load(open(sys.argv[1]))

# A deterministic transport identity, so the transport id mangle_hops inserts is
# reproducible on the Go side too.
Transport.identity = RNS.Identity.from_bytes(bytes(range(64)))

out = []
for c in cases:
    Transport.owner = types.SimpleNamespace(is_connected_to_shared_instance=c["owner_shared"])
    Transport.local_hops_delta = c["delta"]
    iface = Iface(c["iface_shared_instance"])
    Transport.local_client_interfaces = [iface] if c["iface_local_client"] else []

    p = Packet(c["packet_hops"], c["dest_type"])
    raw = bytes.fromhex(c["raw"])
    rec = {"should_apply": bool(Transport.should_apply_delta(p, iface)),
           "mangle_plain": Transport.mangle_hops(raw, c["mangle_hops"], False).hex(),
           "mangle_insert": Transport.mangle_hops(raw, c["mangle_hops"], True).hex(),
           "identity_hash": Transport.identity.hash.hex()}

    # Transport.py:1736, :2258, :2343, verbatim:
    #   packet.hops if not from_local_client or <local_case> or local_hops_delta == 0
    #   else local_hops_delta
    rec["forwarded_hop"] = (c["packet_hops"]
                            if not c["from_local_client"] or c["local_case"] or Transport.local_hops_delta == 0
                            else Transport.local_hops_delta)

    # Transport.py:1692, verbatim:
    #   if local_hops_delta != 0 and from_local_client and not to_local_client:
    #       new_raw = Transport.mangle_hops(new_raw, Transport.local_hops_delta)
    if Transport.local_hops_delta != 0 and c["from_local_client"] and not c["to_local_client"]:
        rec["forwarded_mangled"] = Transport.mangle_hops(raw, Transport.local_hops_delta).hex()
    else:
        rec["forwarded_mangled"] = raw.hex()
    out.append(rec)
print(json.dumps(out))
`

// hopsDeltaRaw is the frame the transform vectors rewrite: HEADER_2, TRANSPORT,
// a hop byte and a body.
var hopsDeltaRaw = []byte{0x40, 0x02, 0xAA, 0xBB, 0xCC, 0xDD}

// hopsDeltaCases enumerates the gate's inputs, then the forwarding expressions'
// own inputs.
func hopsDeltaCases() []hopsDeltaRequest {
	hexRaw := hex.EncodeToString(hopsDeltaRaw)
	var cases []hopsDeltaRequest

	for _, shared := range []bool{false, true} {
		for _, hops := range []int{0, 1, 3} {
			for _, delta := range []int{0, 2, 7} {
				for _, dt := range []int{DestinationSingle, DestinationGroup, DestinationPlain, DestinationLink} {
					for _, kind := range []struct{ localClient, sharedInstance bool }{
						{false, false},
						{true, false},
						{false, true},
					} {
						cases = append(cases, hopsDeltaRequest{
							OwnerShared:         shared,
							PacketHops:          hops,
							Delta:               delta,
							DestType:            dt,
							IfaceLocalClient:    kind.localClient,
							IfaceSharedInstance: kind.sharedInstance,
							Raw:                 hexRaw,
							MangleHops:          5,
						})
					}
				}
			}
		}
	}

	for _, hops := range []int{0, 1, 3, 9} {
		for _, delta := range []int{0, 4} {
			for _, from := range []bool{false, true} {
				for _, localCase := range []bool{false, true} {
					for _, to := range []bool{false, true} {
						cases = append(cases, hopsDeltaRequest{
							PacketHops: hops, Delta: delta, DestType: DestinationSingle,
							Raw: hexRaw, MangleHops: 3,
							FromLocalClient: from, LocalCase: localCase, ToLocalClient: to,
						})
					}
				}
			}
		}
	}
	return cases
}

// TestHopsDeltaVectorsAgainstPython pins the disguise's gate, its transform and
// its two forwarding expressions to the Python reference.
//
// The two interface kinds that exempt a packet from the disguise are skipped
// here because neither can be built as a plain stub — one is a spawned socket
// client and the other the instance's own listener — and are asserted in
// TestHopsDeltaInterfaceExemptions instead.
func TestHopsDeltaVectorsAgainstPython(t *testing.T) {
	t.Parallel()

	cases := hopsDeltaCases()
	var results []hopsDeltaResult
	runFramingVectorPython(t, pythonHopsDeltaScript, cases, &results)
	if len(results) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(results), len(cases))
	}

	for i, c := range cases {
		res := results[i]
		if c.IfaceLocalClient || c.IfaceSharedInstance {
			continue
		}
		t.Run("case", func(t *testing.T) {
			t.Parallel()

			ts := NewTransportSystem(nil)
			// The transport id mangle_hops inserts has to be the one Python used,
			// so the identity's hash is set to the reference value.
			ts.identity = mustTestNewIdentity(t, true)
			ts.identity.Hash = mustDecodeHex(t, res.IdentityHash)
			ts.SetConnectedToSharedInstance(c.OwnerShared)
			ts.SetLocalHopsDeltaEnabled(true)
			ts.mu.Lock()
			ts.localHopsDelta = c.Delta
			ts.mu.Unlock()

			raw := mustDecodeHex(t, c.Raw)
			packet := NewPacketFromRaw(raw)
			packet.Hops = c.PacketHops
			packet.DestinationType = c.DestType
			iface := newFramingVectorInterface("iface", interfaces.ModeFull)

			if got, want := ts.shouldApplyDelta(packet, iface), res.ShouldApply; got != want {
				t.Errorf("shouldApplyDelta(hops=%v, delta=%v, destType=%v, ownerShared=%v) = %v, want %v (RNS/Transport.py:1361-1365)",
					c.PacketHops, c.Delta, c.DestType, c.OwnerShared, got, want)
			}
			if got, want := ts.mangleHops(raw, c.MangleHops, false), mustDecodeHex(t, res.ManglePlain); !bytes.Equal(got, want) {
				t.Errorf("mangleHops(hops=%v, insert=false) = %x, want %x (RNS/Transport.py:1368)", c.MangleHops, got, want)
			}
			if got, want := ts.mangleHops(raw, c.MangleHops, true), mustDecodeHex(t, res.MangleInsert); !bytes.Equal(got, want) {
				t.Errorf("mangleHops(hops=%v, insert=true) = %x, want %x (RNS/Transport.py:1369)", c.MangleHops, got, want)
			}
			if got, want := ts.forwardedHopByteLocked(packet, c.FromLocalClient, c.LocalCase), byte(res.ForwardedHop); got != want {
				t.Errorf("forwarded hop byte (hops=%v, fromLocalClient=%v, localCase=%v, delta=%v) = %v, want %v (RNS/Transport.py:1736,2258,2343)",
					c.PacketHops, c.FromLocalClient, c.LocalCase, c.Delta, got, want)
			}
			if got, want := ts.mangleForwardedRaw(raw, c.FromLocalClient, c.ToLocalClient), mustDecodeHex(t, res.ForwardedMangled); !bytes.Equal(got, want) {
				t.Errorf("mangleForwardedRaw (fromLocalClient=%v, toLocalClient=%v, delta=%v) = %x, want %x (RNS/Transport.py:1692)",
					c.FromLocalClient, c.ToLocalClient, c.Delta, got, want)
			}
		})
	}
}

// TestHopsDeltaInterfaceExemptions pins the two interface kinds Python exempts
// from the disguise. A plain stub cannot stand in for either: Python's
// `is_shared_instance(interface)` looks for the attribute the instance's own
// listener carries (Transport.py:1364, :3162-3164; set at
// RNS/Interfaces/LocalInterface.py:403,412), and `interface in
// Transport.local_client_interfaces` for one of its spawned clients, which the
// shared-instance link tests exercise end to end.
func TestHopsDeltaInterfaceExemptions(t *testing.T) {
	testutils.SkipShortIntegration(t)

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetLocalHopsDeltaEnabled(true)
	ts.mu.Lock()
	ts.localHopsDelta = 5
	ts.mu.Unlock()

	packet := NewPacketFromRaw([]byte{0x00, 0x00, 0x01})
	packet.Hops = 0
	packet.DestinationType = DestinationSingle

	if !ts.shouldApplyDelta(packet, newFramingVectorInterface("plain", interfaces.ModeFull)) {
		t.Errorf("a plain interface must not exempt a packet from the disguise")
	}
	if ts.shouldApplyDelta(packet, nil) {
		t.Errorf("a nil interface must not apply the disguise")
	}

	port := reserveTCPPort(t)
	server, err := interfaces.NewLocalServerInterface("shared", "", port, func([]byte, interfaces.Interface) {})
	if err != nil {
		t.Fatalf("NewLocalServerInterface: %v", err)
	}
	t.Cleanup(func() { _ = server.Detach() })
	if ts.shouldApplyDelta(packet, server) {
		t.Errorf("the shared instance's own listener must be exempt from the disguise (RNS/Transport.py:1364)")
	}
}
