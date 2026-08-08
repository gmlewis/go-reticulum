// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// compileStatsTopLevelKeys is the exact set of top-level keys Python
// LXMRouter.compile_stats emits (LXMRouter.py:786-814), captured from the
// Python source as the golden schema.
var compileStatsTopLevelKeys = []string{
	"identity_hash", "destination_hash", "uptime", "delivery_limit",
	"propagation_limit", "sync_limit", "target_stamp_cost",
	"stamp_cost_flexibility", "peering_cost", "max_peering_cost",
	"autopeer_maxdepth", "from_static_only", "messagestore", "clients",
	"unpeered_propagation_incoming", "unpeered_propagation_rx_bytes",
	"static_peers", "discovered_peers", "total_peers", "max_peers", "peers",
}

// compileStatsPeerKeys is the exact set of per-peer keys Python emits
// (LXMRouter.py:756-783).
var compileStatsPeerKeys = []string{
	"type", "state", "alive", "name", "last_heard", "next_sync_attempt",
	"last_sync_attempt", "sync_backoff", "peering_timebase", "ler", "str",
	"transfer_limit", "sync_limit", "target_stamp_cost",
	"stamp_cost_flexibility", "peering_cost", "peering_key",
	"network_distance", "rx_bytes", "tx_bytes", "acceptance_rate", "messages",
}

// compileStatsMessageKeys is the set of keys under per-peer "messages"
// (LXMRouter.py:778-783).
var compileStatsMessageKeys = []string{"offered", "outgoing", "incoming", "unhandled"}

// TestCompileStatsGolden verifies Router.compileStatsLocked (the body of
// statsGetRequest) reproduces the full Python compile_stats schema: every
// top-level key, every per-peer key, every nested messagestore/clients/
// messages key, correct types, and deterministic values for a router
// configured to a known state. It also round-trips the response through
// msgpack to confirm the "peers" sub-map serialises with binary peer-id keys
// (matching Python umsgpack), which a plain Go map cannot express.
func TestCompileStatsGolden(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	// Configure the propagation node to a known state.
	router.propagationEnabled = true
	start := time.Unix(1_700_000_000, 0)
	router.propagationNodeStart = start
	router.now = func() time.Time { return start.Add(30 * time.Second) } // uptime = 30s
	router.propagationCost = 12
	router.propagationCostFlexibility = 3
	router.peeringCost = 8
	router.maxPeeringCost = 64
	router.autopeerMaxdepth = 12
	router.fromStaticOnly = true
	router.deliveryPerTransferLimit = 3
	router.propagationPerTransferLimit = 7
	router.propagationPerSyncLimit = 5
	router.maxPeers = 256
	router.messageStorageLimit = 1_000_000
	router.clientPropagationMessagesReceived = 11
	router.clientPropagationMessagesServed = 7
	router.unpeeredPropagationIncoming = 3
	router.unpeeredPropagationRXBytes = 512

	// A static peer with known sync/transfer state.
	peerHash := make([]byte, 16)
	peerHash[0] = 0xab
	router.staticPeers[string(peerHash)] = struct{}{}
	peer := &Peer{
		destinationHash:                 peerHash,
		state:                           PeerStateResponseReceived,
		alive:                           true,
		lastHeard:                       float64(start.Unix()),
		nextSyncAttempt:                 float64(start.Add(60 * time.Second).Unix()),
		lastSyncAttempt:                 float64(start.Add(-10 * time.Second).Unix()),
		syncBackoff:                     720,
		peeringTimebase:                 float64(start.Unix()),
		linkEstablishmentRate:           2.0,
		syncTransferRate:                4.0,
		offered:                         10,
		outgoing:                        5,
		incoming:                        3,
		rxBytes:                         1024,
		txBytes:                         2048,
		propagationTransferLimit:        new(float64(9)),
		propagationSyncLimit:            new(11),
		propagationStampCost:            new(12),
		propagationStampCostFlexibility: new(3),
		peeringCost:                     new(8),
	}
	peer.peeringKey = []any{[]byte{0x01, 0x02}, 42}
	peer.umCountsSynced = true
	peer.umCount = 2
	router.peers[string(peerHash)] = peer

	// A discovered peer (no static entry) so discovered_peers=1, static_peers=1.
	discHash := make([]byte, 16)
	discHash[0] = 0xcd
	router.peers[string(discHash)] = &Peer{destinationHash: discHash, state: PeerStateIdle, lastHeard: float64(start.Unix())}

	// Allow the calling identity.
	caller := mustTestNewIdentity(t, true)
	router.controlAllowed[string(caller.Hash)] = struct{}{}

	respAny := router.statsGetRequest("", nil, nil, nil, caller, time.Now())
	resp, ok := respAny.(map[string]any)
	if !ok {
		t.Fatalf("compileStats returned %T, want map[string]any", respAny)
	}

	// Top-level key set must match the golden schema exactly.
	assertStringSet(t, "top-level", mapKeysString(resp), compileStatsTopLevelKeys)

	// Deterministic node-level values.
	if got, ok := resp["uptime"].(float64); !ok || got != 30.0 {
		t.Fatalf("uptime=%v want 30.0", resp["uptime"])
	}
	if got := resp["target_stamp_cost"]; got != 12 {
		t.Fatalf("target_stamp_cost=%v want 12", got)
	}
	if got := resp["stamp_cost_flexibility"]; got != 3 {
		t.Fatalf("stamp_cost_flexibility=%v want 3", got)
	}
	if got := resp["peering_cost"]; got != 8 {
		t.Fatalf("peering_cost=%v want 8", got)
	}
	if got := resp["max_peering_cost"]; got != 64 {
		t.Fatalf("max_peering_cost=%v want 64", got)
	}
	if got := resp["autopeer_maxdepth"]; got != 12 {
		t.Fatalf("autopeer_maxdepth=%v want 12", got)
	}
	if got := resp["from_static_only"]; got != true {
		t.Fatalf("from_static_only=%v want true", got)
	}
	if got := asInt(resp["delivery_limit"]); got != 3 {
		t.Fatalf("delivery_limit=%v want 3", resp["delivery_limit"])
	}
	if got := asInt(resp["propagation_limit"]); got != 7 {
		t.Fatalf("propagation_limit=%v want 7", resp["propagation_limit"])
	}
	if got := asInt(resp["sync_limit"]); got != 5 {
		t.Fatalf("sync_limit=%v want 5", resp["sync_limit"])
	}
	if got := resp["max_peers"]; got != 256 {
		t.Fatalf("max_peers=%v want 256", got)
	}
	if got := resp["static_peers"]; got != 1 {
		t.Fatalf("static_peers=%v want 1", got)
	}
	if got := resp["discovered_peers"]; got != 1 {
		t.Fatalf("discovered_peers=%v want 1", got)
	}
	if got := resp["total_peers"]; got != 2 {
		t.Fatalf("total_peers=%v want 2", got)
	}
	if got := resp["unpeered_propagation_incoming"]; got != 3 {
		t.Fatalf("unpeered_propagation_incoming=%v want 3", got)
	}
	if got := resp["unpeered_propagation_rx_bytes"]; got != 512 {
		t.Fatalf("unpeered_propagation_rx_bytes=%v want 512", got)
	}
	if got := resp["identity_hash"]; len(got.([]byte)) == 0 {
		t.Fatalf("identity_hash empty")
	}

	// messagestore sub-map.
	ms, ok := resp["messagestore"].(map[string]any)
	if !ok {
		t.Fatalf("messagestore type=%T want map", resp["messagestore"])
	}
	assertStringSet(t, "messagestore", mapKeysString(ms), []string{"count", "bytes", "limit"})
	if got := asInt(ms["count"]); got != 0 {
		t.Fatalf("messagestore.count=%v want 0", ms["count"])
	}
	if got := asInt(ms["bytes"]); got != 0 {
		t.Fatalf("messagestore.bytes=%v want 0", ms["bytes"])
	}
	if got := asInt(ms["limit"]); got != 1_000_000 {
		t.Fatalf("messagestore.limit=%v want 1000000", ms["limit"])
	}

	// clients sub-map.
	clients, ok := resp["clients"].(map[string]any)
	if !ok {
		t.Fatalf("clients type=%T want map", resp["clients"])
	}
	assertStringSet(t, "clients", mapKeysString(clients), []string{"client_propagation_messages_received", "client_propagation_messages_served"})
	if got := clients["client_propagation_messages_received"]; got != 11 {
		t.Fatalf("clients.received=%v want 11", got)
	}
	if got := clients["client_propagation_messages_served"]; got != 7 {
		t.Fatalf("clients.served=%v want 7", got)
	}

	// The peers value is a peerStatsMsgpack (msgpack.Marshaler). Round-trip
	// the whole response through msgpack and verify the peers sub-map decodes
	// keyed by the binary peer-id, matching Python umsgpack. Default Unpack
	// converts bin keys to string (the raw bytes), so peers keys are strings
	// whose bytes are the peer-id.
	packed, err := msgpack.Pack(resp)
	if err != nil {
		t.Fatalf("Pack response: %v", err)
	}
	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	top, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked top type=%T want map[any]any", unpacked)
	}
	peersMap, ok := top["peers"].(map[any]any)
	if !ok {
		t.Fatalf("unpacked peers type=%T want map[any]any", top["peers"])
	}
	if len(peersMap) != 2 {
		t.Fatalf("unpacked peers count=%d want 2", len(peersMap))
	}

	// Locate the static peer by its binary peer-id key. Default Unpack
	// converts bin keys to string (the raw bytes), so the key is the peer-id
	// bytes as a string.
	staticEntry, ok := peersMap[string(peerHash)].(map[any]any)
	if !ok {
		t.Fatalf("static peer entry missing or wrong type: %T", peersMap[string(peerHash)])
	}
	assertStringSet(t, "per-peer", mapKeysAny(staticEntry), compileStatsPeerKeys)

	if got := staticEntry["type"]; got != "static" {
		t.Fatalf("peer type=%v want static", got)
	}
	if got := asInt(staticEntry["state"]); got != PeerStateResponseReceived {
		t.Fatalf("peer state=%v want %d", staticEntry["state"], PeerStateResponseReceived)
	}
	if got := staticEntry["alive"]; got != true {
		t.Fatalf("peer alive=%v want true", got)
	}
	if got := staticEntry["name"]; got != "" {
		t.Fatalf("peer name=%v want empty", got)
	}
	if got := asInt(staticEntry["last_heard"]); got != int(start.Unix()) {
		t.Fatalf("peer last_heard=%v want %d", staticEntry["last_heard"], start.Unix())
	}
	if got := asInt(staticEntry["ler"]); got != 2 {
		t.Fatalf("peer ler=%v want 2", staticEntry["ler"])
	}
	if got := asInt(staticEntry["str"]); got != 4 {
		t.Fatalf("peer str=%v want 4", staticEntry["str"])
	}
	if got := staticEntry["transfer_limit"]; got != 9.0 {
		t.Fatalf("peer transfer_limit=%v want 9.0", staticEntry["transfer_limit"])
	}
	if got := asInt(staticEntry["sync_limit"]); got != 11 {
		t.Fatalf("peer sync_limit=%v want 11", staticEntry["sync_limit"])
	}
	if got := asInt(staticEntry["target_stamp_cost"]); got != 12 {
		t.Fatalf("peer target_stamp_cost=%v want 12", staticEntry["target_stamp_cost"])
	}
	if got := asInt(staticEntry["stamp_cost_flexibility"]); got != 3 {
		t.Fatalf("peer stamp_cost_flexibility=%v want 3", staticEntry["stamp_cost_flexibility"])
	}
	if got := asInt(staticEntry["peering_cost"]); got != 8 {
		t.Fatalf("peer peering_cost=%v want 8", staticEntry["peering_cost"])
	}
	if got := asInt(staticEntry["peering_key"]); got != 42 {
		t.Fatalf("peer peering_key=%v want 42", staticEntry["peering_key"])
	}
	if got := asInt(staticEntry["network_distance"]); got != rns.PathfinderM {
		t.Fatalf("peer network_distance=%v want %d", staticEntry["network_distance"], rns.PathfinderM)
	}
	if got := asInt(staticEntry["rx_bytes"]); got != 1024 {
		t.Fatalf("peer rx_bytes=%v want 1024", staticEntry["rx_bytes"])
	}
	if got := asInt(staticEntry["tx_bytes"]); got != 2048 {
		t.Fatalf("peer tx_bytes=%v want 2048", staticEntry["tx_bytes"])
	}
	if got := staticEntry["acceptance_rate"]; got != 0.5 {
		t.Fatalf("peer acceptance_rate=%v want 0.5", staticEntry["acceptance_rate"])
	}

	// messages sub-map.
	msgs, ok := staticEntry["messages"].(map[any]any)
	if !ok {
		t.Fatalf("peer messages type=%T want map", staticEntry["messages"])
	}
	assertStringSet(t, "messages", mapKeysAny(msgs), compileStatsMessageKeys)
	if got := asInt(msgs["offered"]); got != 10 {
		t.Fatalf("messages.offered=%v want 10", msgs["offered"])
	}
	if got := asInt(msgs["outgoing"]); got != 5 {
		t.Fatalf("messages.outgoing=%v want 5", msgs["outgoing"])
	}
	if got := asInt(msgs["incoming"]); got != 3 {
		t.Fatalf("messages.incoming=%v want 3", msgs["incoming"])
	}
	if got := asInt(msgs["unhandled"]); got != 2 {
		t.Fatalf("messages.unhandled=%v want 2", msgs["unhandled"])
	}

	// The discovered peer's type must be "discovered".
	discEntry, ok := peersMap[string(discHash)].(map[any]any)
	if !ok {
		t.Fatalf("discovered peer entry missing: %T", peersMap[string(discHash)])
	}
	if got := discEntry["type"]; got != "discovered" {
		t.Fatalf("discovered peer type=%v want discovered", got)
	}
}

// TestCompileStatsDisabledReturnsNil verifies that when propagation is not
// enabled, compileStats returns nil, matching Python's
// `if not self.propagation_node: return None`.
func TestCompileStatsDisabledReturnsNil(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	caller := mustTestNewIdentity(t, true)
	router.controlAllowed[string(caller.Hash)] = struct{}{}

	if got := router.statsGetRequest("", nil, nil, nil, caller, time.Now()); got != nil {
		t.Fatalf("compileStats with propagation disabled = %v, want nil", got)
	}
}

// mapKeysString returns the string keys of a map[string]any.
func mapKeysString(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mapKeysAny returns the string form of the keys of a map[any]any (the shape
// msgpack.Unpack produces for decoded maps), for comparing against a golden
// key set.
func mapKeysAny(m map[any]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, asString(k))
	}
	sort.Strings(keys)
	return keys
}

// assertStringSet fails the test if got does not equal want as a sorted set.
func assertStringSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	wantSorted := append([]string{}, want...)
	sort.Strings(wantSorted)
	if len(got) != len(wantSorted) {
		t.Fatalf("%s keys mismatch:\n got=%v\n want=%v", label, got, wantSorted)
	}
	for i := range got {
		if got[i] != wantSorted[i] {
			t.Fatalf("%s keys mismatch:\n got=%v\n want=%v", label, got, wantSorted)
		}
	}
}

// asInt extracts an int from any msgpack-decoded numeric type.
func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	}
	return -1
}

// asString returns the string form of v (used for map[any]any keys).
func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	}
	if rv := reflect.ValueOf(v); rv.IsValid() && rv.Kind() == reflect.String {
		return rv.String()
	}
	return ""
}
