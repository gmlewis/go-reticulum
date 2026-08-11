// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// Cross-implementation (Go <-> Python LXMF) round-trip tests for every on-disk
// LXMF storage format under ~/.reticulum/storage/lxmf/ that both
// implementations write. These lock in the msgpack contract — in particular
// the bin (raw-bytes) vs str map-key distinction that Python's umsgpack
// requires (Go writing str keys for destination_hash/transient_id/ticket made
// Python choke, mirroring the RNS known_destinations regression). See the
// rns/storage_interop_test.go header and the OrderedMap bin-key fix in
// router.go's Save* functions.
//
// Tests run only when `python3` + RNS are importable (SkipIfNoPythonRNS).

// newInteropRouter builds a router whose storage lives in a fresh /tmp dir and
// whose job loop is already stopped (safe for synchronous storage round-trips).
func newInteropRouter(t *testing.T) *Router {
	t.Helper()
	return mustTestNewRouter(t, rns.NewTransportSystem(nil), nil, testutils.TempDir(t, tempDirPrefix))
}

// rawStr returns a Go string holding n copies of byte b, the in-memory key
// representation LXMF uses for raw hash bytes (the save functions convert these
// to bin map keys on disk via orderedBinMap).
func rawStr(b byte, n int) string { return string(bytes.Repeat([]byte{b}, n)) }

// hexStr returns the hex of n copies of byte b, for building Python scripts.
func hexStr(b byte, n int) string { return fmt.Sprintf("%x", bytes.Repeat([]byte{b}, n)) }

// keyBytes returns n copies of byte b as a []byte (the raw key Python writes).
func keyBytes(b byte, n int) []byte { return bytes.Repeat([]byte{b}, n) }

func okFromPython(t *testing.T, out string, what string) {
	t.Helper()
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python failed to read Go-written %s:\n%s", what, out)
	}
}

// TestStorageInteropLocalDeliveries verifies lxmf/local_deliveries: bin
// transient_id map keys, float received timestamps. Both directions through the
// real Save/Load path.
func TestStorageInteropLocalDeliveries(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	r := newInteropRouter(t)
	r.mu.Lock()
	r.locallyDeliveredIDs[rawStr(0x1d, 16)] = time.Now()
	r.mu.Unlock()
	if err := r.SaveLocalTransientIDCaches(); err != nil {
		t.Fatal(err)
	}
	okFromPython(t, testutils.RunPython(t, transientReadScript, r.localDeliveriesPath()), "local_deliveries")

	// Python writes (bin keys), Go loads.
	r2 := newInteropRouter(t)
	testutils.RunPython(t, fmt.Sprintf(transientWriteScript, hexStr(0x2d, 16)), r2.localDeliveriesPath())
	if err := r2.LoadLocalTransientIDCaches(); err != nil {
		t.Fatal(err)
	}
	r2.mu.Lock()
	_, ok := r2.locallyDeliveredIDs[string(keyBytes(0x2d, 16))]
	r2.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written bin-keyed local_deliveries")
	}
}

// TestStorageInteropLocallyProcessed verifies lxmf/locally_processed: bin
// transient_id map keys, float processed timestamps. Both directions.
func TestStorageInteropLocallyProcessed(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	r := newInteropRouter(t)
	r.mu.Lock()
	r.locallyProcessedIDs[rawStr(0x1e, 16)] = time.Now()
	r.mu.Unlock()
	if err := r.SaveLocalTransientIDCaches(); err != nil {
		t.Fatal(err)
	}
	okFromPython(t, testutils.RunPython(t, transientReadScript, r.locallyProcessedPath()), "locally_processed")

	// Python writes (bin keys), Go loads.
	r2 := newInteropRouter(t)
	testutils.RunPython(t, fmt.Sprintf(transientWriteScript, hexStr(0x2e, 16)), r2.locallyProcessedPath())
	if err := r2.LoadLocalTransientIDCaches(); err != nil {
		t.Fatal(err)
	}
	r2.mu.Lock()
	_, ok := r2.locallyProcessedIDs[string(keyBytes(0x2e, 16))]
	r2.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written bin-keyed locally_processed")
	}
}

// TestStorageInteropOutboundStampCosts verifies lxmf/outbound_stamp_costs: bin
// destination_hash map keys, [float timestamp, stampCost] values. Both
// directions.
func TestStorageInteropOutboundStampCosts(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	r := newInteropRouter(t)
	r.mu.Lock()
	r.outboundStampCosts[rawStr(0x3d, 16)] = outboundStampCostEntry{updatedAt: time.Now(), stampCost: 1}
	r.mu.Unlock()
	if err := r.SaveOutboundStampCosts(); err != nil {
		t.Fatal(err)
	}
	okFromPython(t, testutils.RunPython(t, stampCostReadScript, r.outboundStampCostsPath()), "outbound_stamp_costs")

	// Python writes (bin keys), Go loads.
	r2 := newInteropRouter(t)
	testutils.RunPython(t, fmt.Sprintf(stampCostWriteScript, hexStr(0x4d, 16)), r2.outboundStampCostsPath())
	if err := r2.LoadOutboundStampCosts(); err != nil {
		t.Fatal(err)
	}
	r2.mu.Lock()
	_, ok := r2.outboundStampCosts[string(keyBytes(0x4d, 16))]
	r2.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written bin-keyed outbound_stamp_costs")
	}
}

// TestStorageInteropAvailableTickets verifies lxmf/available_tickets: str
// top-level keys (outbound/inbound/last_deliveries) with bin destination_hash
// and bin ticket nested keys. Both directions.
func TestStorageInteropAvailableTickets(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	r := newInteropRouter(t)
	dest := rawStr(0x5d, 16)
	ticket := keyBytes(0x6d, 16)
	r.ticketStore.mu.Lock()
	r.ticketStore.outbound[dest] = TicketEntry{Expires: float64(time.Now().Unix()) + 3600, Ticket: ticket}
	r.ticketStore.inbound[dest] = map[string]TicketEntry{string(ticket): {Expires: float64(time.Now().Unix()) + 3600}}
	r.ticketStore.lastDeliveries[dest] = float64(time.Now().Unix())
	r.ticketStore.mu.Unlock()
	if err := r.SaveAvailableTickets(); err != nil {
		t.Fatal(err)
	}
	okFromPython(t, testutils.RunPython(t, ticketsReadScript, r.availableTicketsPath()), "available_tickets")

	// Python writes (str top, bin nested), Go loads.
	r2 := newInteropRouter(t)
	testutils.RunPython(t, fmt.Sprintf(ticketsWriteScript, hexStr(0x7d, 16), hexStr(0x8d, 16)), r2.availableTicketsPath())
	if err := r2.LoadAvailableTickets(); err != nil {
		t.Fatal(err)
	}
	r2.ticketStore.mu.Lock()
	_, outOK := r2.ticketStore.outbound[string(keyBytes(0x7d, 16))]
	_, inOK := r2.ticketStore.inbound[string(keyBytes(0x7d, 16))]
	_, ldOK := r2.ticketStore.lastDeliveries[string(keyBytes(0x7d, 16))]
	r2.ticketStore.mu.Unlock()
	if !outOK || !inOK || !ldOK {
		t.Fatalf("Go failed to load Python-written available_tickets (out=%v in=%v ld=%v)", outOK, inOK, ldOK)
	}
}

// TestStorageInteropNodeStats verifies lxmf/node_stats: str literal keys, int
// values. Both directions (regression guard — already matches Python).
func TestStorageInteropNodeStats(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	r := newInteropRouter(t)
	r.mu.Lock()
	r.clientPropagationMessagesReceived = 42
	r.clientPropagationMessagesServed = 7
	r.unpeeredPropagationIncoming = 3
	r.unpeeredPropagationRXBytes = 1234
	r.mu.Unlock()
	if err := r.SaveNodeStats(); err != nil {
		t.Fatal(err)
	}
	okFromPython(t, testutils.RunPython(t, nodeStatsReadScript, r.nodeStatsPath()), "node_stats")

	// Python writes (str keys, int values), Go loads.
	r2 := newInteropRouter(t)
	testutils.RunPython(t, nodeStatsWriteScript, r2.nodeStatsPath())
	if err := r2.LoadNodeStats(); err != nil {
		t.Fatal(err)
	}
	r2.mu.Lock()
	got := r2.clientPropagationMessagesReceived
	r2.mu.Unlock()
	if got != 99 {
		t.Fatalf("Go loaded client_propagation_messages_received = %v, want 99 (Python-written node_stats)", got)
	}
}

// TestStorageInteropPeers verifies lxmf/peers: an array of bin blobs, each a
// per-peer dict with str keys and a bin destination_hash value. Both
// directions (regression guard — already matches Python).
func TestStorageInteropPeers(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	r := newInteropRouter(t)
	dest := keyBytes(0x9d, 16)
	peer := NewPeer(r, dest)
	r.mu.Lock()
	r.propagationEnabled = true
	r.peers[string(dest)] = peer
	r.mu.Unlock()
	if err := r.SavePeers(); err != nil {
		t.Fatal(err)
	}
	okFromPython(t, testutils.RunPython(t, peersReadScript, r.peersPath()), "peers")

	// Python writes (array of bin blobs, str-keyed dicts), Go loads.
	r2 := newInteropRouter(t)
	// LoadPeers skips peers whose identity cannot be recalled, so register a
	// real identity for the peer's destination_hash in the transport cache
	// first. This is a semantic gate, not a format requirement.
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ts2, ok := r2.transport.(*rns.TransportSystem); ok {
		ts2.Remember(keyBytes(0xad, 16), keyBytes(0xad, 16), id.GetPublicKey(), nil)
	}
	pyDest := hexStr(0xad, 16)
	testutils.RunPython(t, fmt.Sprintf(peersWriteScript, pyDest), r2.peersPath())
	r2.mu.Lock()
	r2.propagationEnabled = true
	r2.mu.Unlock()
	if err := r2.LoadPeers(); err != nil {
		t.Fatal(err)
	}
	r2.mu.Lock()
	_, ok := r2.peers[string(keyBytes(0xad, 16))]
	r2.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written peers entry")
	}
}

// --- Python scripts (Go-written -> Python-read assertions) ---

const transientReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.unpackb(f.read())
assert isinstance(d, dict) and len(d) == 1, "want 1-entry dict, got %r" % (d,)
for k, v in d.items():
    assert isinstance(k, bytes), "transient_id key %r is %r not bytes (str-vs-bin bug)" % (k, type(k))
    assert len(k) == 16, "key len %d want 16" % len(k)
    assert isinstance(v, float), "value not float: %r" % type(v)
print("OK")
`

const transientWriteScript = `import sys, time
from RNS.vendor import umsgpack
tid = bytes.fromhex("%s")
d = {tid: time.time()}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb(d))
print("OK")
`

const stampCostReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.unpackb(f.read())
assert isinstance(d, dict) and len(d) == 1, "want 1-entry dict, got %r" % (d,)
for k, v in d.items():
    assert isinstance(k, bytes), "dest key %r is %r not bytes" % (k, type(k))
    assert len(k) == 16, "key len %d want 16" % len(k)
    assert isinstance(v, list) and len(v) == 2, "value not 2-list: %r" % (v,)
    assert isinstance(v[0], float), "timestamp not float: %r" % type(v[0])
print("OK")
`

const stampCostWriteScript = `import sys, time
from RNS.vendor import umsgpack
dest = bytes.fromhex("%s")
d = {dest: [time.time(), 1]}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb(d))
print("OK")
`

const ticketsReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.unpackb(f.read())
assert isinstance(d, dict), "top not dict: %r" % type(d)
assert set(d.keys()) == {"outbound", "inbound", "last_deliveries"}, "top keys %r" % list(d.keys())
for k in d:
    assert isinstance(k, str), "top key %r is %r not str" % (k, type(k))
for k, v in d["outbound"].items():
    assert isinstance(k, bytes), "outbound dest key %r not bytes" % type(k)
    assert isinstance(v, list) and len(v) == 2 and isinstance(v[1], bytes), "outbound value %r" % (v,)
for k, v in d["inbound"].items():
    assert isinstance(k, bytes), "inbound dest key %r not bytes" % type(k)
    assert isinstance(v, dict), "inbound value not dict"
    for tk, tv in v.items():
        assert isinstance(tk, bytes), "inbound ticket key %r not bytes" % type(tk)
        assert isinstance(tv, list) and len(tv) == 1, "inbound ticket value %r" % (tv,)
for k, v in d["last_deliveries"].items():
    assert isinstance(k, bytes), "last_deliveries dest key %r not bytes" % type(k)
    assert isinstance(v, float), "last_deliveries value not float"
print("OK")
`

// ticketsWriteScript: dest hex then ticket hex. Python writes str top-level,
// bin nested keys (matching Python's real format) so Go's bin-tolerant reader
// loads it.
const ticketsWriteScript = `import sys, time
from RNS.vendor import umsgpack
dest = bytes.fromhex("%s")
ticket = bytes.fromhex("%s")
d = {
    "outbound": {dest: [time.time() + 3600, ticket]},
    "inbound": {dest: {ticket: [time.time() + 3600]}},
    "last_deliveries": {dest: time.time()},
}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb(d))
print("OK")
`

const nodeStatsReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.unpackb(f.read())
assert isinstance(d, dict), "not dict: %r" % type(d)
expected = {"client_propagation_messages_received", "client_propagation_messages_served", "unpeered_propagation_incoming", "unpeered_propagation_rx_bytes"}
assert set(d.keys()) == expected, "keys %r" % list(d.keys())
for k, v in d.items():
    assert isinstance(k, str), "key %r not str" % type(k)
    assert isinstance(v, int), "value %r not int" % type(v)
assert d["client_propagation_messages_received"] == 42, "rx %r" % d["client_propagation_messages_received"]
print("OK")
`

const nodeStatsWriteScript = `import sys
from RNS.vendor import umsgpack
d = {
    "client_propagation_messages_received": 99,
    "client_propagation_messages_served": 0,
    "unpeered_propagation_incoming": 0,
    "unpeered_propagation_rx_bytes": 0,
}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb(d))
print("OK")
`

const peersReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    lst = umsgpack.unpackb(f.read())
assert isinstance(lst, list) and len(lst) == 1, "want 1-element list, got %r" % type(lst)
blob = lst[0]
assert isinstance(blob, bytes), "peer blob not bytes: %r" % type(blob)
d = umsgpack.unpackb(blob)
assert isinstance(d, dict), "peer dict not dict: %r" % type(d)
assert "destination_hash" in d, "missing destination_hash"
assert isinstance(d["destination_hash"], bytes) and len(d["destination_hash"]) == 16, "destination_hash not 16-byte bin"
for k in d:
    assert isinstance(k, str), "peer key %r not str" % type(k)
print("OK")
`

const peersWriteScript = `import sys, time
from RNS.vendor import umsgpack
dest = bytes.fromhex("%s")
peer = {
    "peering_timebase": 0.0, "alive": False, "metadata": None, "last_heard": 0.0,
    "sync_strategy": "periodic", "peering_key": None, "destination_hash": dest,
    "link_establishment_rate": 0, "sync_transfer_rate": 0,
    "propagation_transfer_limit": None, "propagation_sync_limit": None,
    "propagation_stamp_cost": None, "propagation_stamp_cost_flexibility": None,
    "peering_cost": None, "last_sync_attempt": 0.0, "offered": [], "outgoing": [],
    "incoming": [], "rx_bytes": 0, "tx_bytes": 0, "handled_ids": [], "unhandled_ids": [],
}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb([umsgpack.packb(peer)]))
print("OK")
`
