// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// pyRenderBlockCase is the JSON-encoded description of one
// TestRenderInterfacePythonParity case, consumed by the Python capture
// script. Optional fields use pointers so nil values are omitted from the
// JSON (omitempty on a *T omits only nil), mirroring the "key in ifstat"
// guards in the Python rnstatus.py display block. rxb/txb/rxs/txs are
// always present (matching the real get_interface_stats output and the
// Go renderInterface, which always emits the speed column).
type pyRenderBlockCase struct {
	Name    string   `json:"name"`
	Mode    int      `json:"mode"`
	Status  bool     `json:"status"`
	Clients *int     `json:"clients,omitempty"`
	Peers   *int     `json:"peers,omitempty"`
	InAnn   *float64 `json:"incoming_announce_frequency,omitempty"`
	OutAnn  *float64 `json:"outgoing_announce_frequency,omitempty"`
	InPr    *float64 `json:"incoming_pr_frequency,omitempty"`
	OutPr   *float64 `json:"outgoing_pr_frequency,omitempty"`
	Art     *int     `json:"announce_rate_target,omitempty"`
	Arp     *int     `json:"announce_rate_penalty,omitempty"`
	Arg     *int     `json:"announce_rate_grace,omitempty"`
	RXB     float64  `json:"rxb"`
	TXB     float64  `json:"txb"`
	RXS     float64  `json:"rxs"`
	TXS     float64  `json:"txs"`
	Astats  bool     `json:"astats"`
	Pstats  bool     `json:"pstats"`
}

// pythonRenderBlocks runs the verbatim Python rnstatus.py display block
// (RNS/Utilities/rnstatus.py:564-644 — the Announces / Path Rqs. / Traffic
// section) against each supplied case, capturing the output live from the
// installed RNS. The block itself is copied line-for-line from the Python
// source so it exercises the real RNS.prettyfrequency / prettysize /
// prettyspeed / prettytime formatters. It is gated on SkipIfNoPythonRNS so
// it runs (and diffs) whenever Python+RNS are available and skips otherwise.
func pythonRenderBlocks(t *testing.T, cases []pyRenderBlockCase) []string {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)

	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatalf("marshal render-block cases: %v", err)
	}
	dir, err := os.MkdirTemp("/tmp", "rnstatus-parity-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	jsonPath := filepath.Join(dir, "cases.json")
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		t.Fatalf("write cases.json: %v", err)
	}

	script := `
import sys, io, time, json
import RNS
from RNS.Interfaces.Interface import Interface as I

# Verbatim copy of the rnstatus.py display block (RNS/Utilities/rnstatus.py
# lines 564-644) that emits the Announces / Path Rqs. / Traffic lines.
def render_block(ifstat, name, clients, astats, pstats):
    buf = io.StringIO()
    def print(*a, **k):
        buf.write(" ".join(str(x) for x in a) + "\n")

    art = None; arp = None; arg = None
    if astats and "announce_rate_target" in ifstat: art = ifstat["announce_rate_target"]
    if astats and "announce_rate_penalty" in ifstat: arp = ifstat["announce_rate_penalty"]
    if astats and "announce_rate_grace" in ifstat: arg = ifstat["announce_rate_grace"]
    if art and arp != None and arg: art_str = f"(t:{RNS.prettytime(art)}/p:{RNS.prettytime(arp)}/g:{arg})"
    elif art and arp != None:       art_str = f"(t:{RNS.prettytime(art)}/p:{RNS.prettytime(arp)})"
    elif art:                       art_str = f"(t:{RNS.prettytime(art)})"
    else:                           art_str = ""

    burst_str = ""
    if "burst_active" in ifstat and ifstat["burst_active"]:
        for_str = RNS.prettytime(time.time()-ifstat["burst_activated"])
        burst_str = f" burst for {for_str}"

    pburst_str = ""
    if "pr_burst_active" in ifstat and ifstat["pr_burst_active"]:
        for_str = RNS.prettytime(time.time()-ifstat["pr_burst_activated"])
        pburst_str = f"burst for {for_str}"

    rxb_str = "↓"+RNS.prettysize(ifstat["rxb"])
    txb_str = "↑"+RNS.prettysize(ifstat["txb"])

    asr = False
    if astats and "incoming_announce_frequency" in ifstat and ifstat["incoming_announce_frequency"] != None:
        oan = ifstat["outgoing_announce_frequency"]
        ian = ifstat["incoming_announce_frequency"]
        if name.startswith("Shared Instance[") and clients and clients > 0: oan = oan-(oan/clients) # Sub rnstatus own part
        oaf = RNS.prettyfrequency(oan, d=1, lpf=True)
        iaf = RNS.prettyfrequency(ian, d=1, lpf=True)

        cspec = "c"
        if clients == None and "peers" in ifstat and ifstat["peers"]: clients = ifstat["peers"]; cspec = "p"
        if clients != None and clients > 0: pc_str = f"{RNS.prettyfrequency(ifstat['outgoing_announce_frequency']/clients, d=1, lpf=True)}/{cspec}"
        else:                               pc_str = ""
        asr = True

    psr = False
    if pstats and "incoming_pr_frequency" in ifstat and ifstat["incoming_pr_frequency"] != None:
        opn = ifstat["outgoing_pr_frequency"]
        ipn = ifstat["incoming_pr_frequency"]
        if name.startswith("Shared Instance[") and clients and clients > 0: opn = opn-(opn/clients) # Sub rnstatus own part
        if astats:
            opf = "↑"+RNS.prettyfrequency(opn, d=1, lpf=True)
            ipf = "↓"+RNS.prettyfrequency(ipn, d=1, lpf=True)
        else:
            opf = RNS.prettyfrequency(opn,d=1, lpf=True)+"↑"
            ipf = RNS.prettyfrequency(ipn,d=1, lpf=True)+"↓"
        cspec = "c"
        if clients == None and "peers" in ifstat and ifstat["peers"]: clients = ifstat["peers"]; cspec = "p"
        if clients != None and clients > 0: rpc_str = f"{RNS.prettyfrequency(ifstat['outgoing_pr_frequency']/clients, d=1, lpf=True)}/{cspec}"
        else:                               rpc_str = ""
        psr = True

    if not asr: iaf = ""; oaf = ""
    if not psr: ipf = ""; opf = ""
    amlen    = max(len(iaf), len(oaf))
    iaf     += (amlen-len(iaf))*" "+"↓"
    oaf     += (amlen-len(oaf))*" "+"↑"
    mlen     = max(max(len(iaf), len(oaf), len(rxb_str), len(txb_str), len(ipf), len(opf)), 10)
    iaf     += (mlen-len(iaf))*" "
    oaf     += (mlen-len(oaf))*" "
    ipf     += (mlen-len(ipf))*" "
    opf     += (mlen-len(opf))*" "
    rxb_str += (mlen-len(rxb_str))*" "
    txb_str += (mlen-len(txb_str))*" "

    if psr:
        print(f"    Path Rqs. : {opf}  {rpc_str}")
        print(f"                {ipf}  {pburst_str}")

    if asr:
        print(f"    Announces : {oaf}  {pc_str}")
        print(f"                {iaf} {art_str}{burst_str}")

    rxstat = rxb_str
    txstat = txb_str
    if "rxs" in ifstat and "txs" in ifstat:
        rxstat += "  "+RNS.prettyspeed(ifstat["rxs"])
        txstat += "  "+RNS.prettyspeed(ifstat["txs"])

    print(f"    Traffic   : {txstat}\n                {rxstat}")
    return buf.getvalue()

with open(sys.argv[1]) as f:
    cases = json.load(f)

for i, c in enumerate(cases):
    ifstat = {"name": c["name"], "status": c["status"], "mode": c["mode"],
              "rxb": c["rxb"], "txb": c["txb"], "rxs": c["rxs"], "txs": c["txs"]}
    for k in ("clients", "peers", "announce_rate_target",
              "announce_rate_penalty", "announce_rate_grace"):
        if k in c:
            ifstat[k] = c[k]
    # Frequency fields are floats in the real Python runtime
    # (get_interface_stats); force float so prettyfrequency renders the
    # d=1 decimal (e.g. "2.0 Hz") regardless of how Go's encoding/json
    # serialised the value ("2" parses back to a Python int, which would
    # format as "2 Hz").
    for k in ("incoming_announce_frequency", "outgoing_announce_frequency",
              "incoming_pr_frequency", "outgoing_pr_frequency"):
        if k in c:
            ifstat[k] = float(c[k])
    clients = c["clients"] if "clients" in c else None
    sys.stdout.write("===CASE %d===\n" % i)
    sys.stdout.write(render_block(ifstat, c["name"], clients, c["astats"], c["pstats"]))
`
	out := testutils.RunPython(t, script, jsonPath)
	parts := strings.Split(out, "===CASE ")
	results := make([]string, len(cases))
	for _, p := range parts[1:] {
		idxStr, body, ok := strings.Cut(p, "===\n")
		if !ok {
			t.Fatalf("malformed python case output: %q", p)
		}
		var idx int
		if err := json.Unmarshal([]byte(idxStr), &idx); err != nil {
			t.Fatalf("parse case index %q: %v", idxStr, err)
		}
		if idx < 0 || idx >= len(cases) {
			t.Fatalf("case index %d out of range (have %d cases)", idx, len(cases))
		}
		results[idx] = body
	}
	for i, r := range results {
		if r == "" {
			t.Fatalf("python capture missing case %d output:\n%s", i, out)
		}
	}
	return results
}
