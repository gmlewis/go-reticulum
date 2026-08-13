// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"bytes"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// lxmfOutboundSyncGoldenPy drives the REAL Python LXMPeer outbound sync
// state machine to capture golden offer + resource bytes. It builds a
// Python LXMRouter with four controlled propagation entries, a peer in the
// LINK_READY state with a mock link, calls peer.sync() (the LINK_READY
// offer-construction branch — LXMPeer.py:327-389) to capture the offer
// [peering_key[0], [transient_id,...]], then calls peer.offer_response(True)
// (LXMPeer.py:429-468) to capture the resource data msgpack.packb([time,
// [lxm_data,...]]). time.time is pinned so the resource timestamp and the
// weight age term are deterministic. The Go port replicates the identical
// scenario and must produce byte-identical offer + resource bytes.
const lxmfOutboundSyncGoldenPy = `import os, sys, time
import RNS
import LXMF
import LXMF.LXMPeer as LXMPeerMod
from RNS.vendor import umsgpack as msgpack

if len(sys.argv) != 4:
    print("ERROR: missing args", file=sys.stderr); sys.exit(1)

store_dir, offer_out, resource_out = sys.argv[1], sys.argv[2], sys.argv[3]

fixed_time = 2000000.0
config_dir = os.path.join(store_dir, "rnsconfig")
os.makedirs(config_dir, exist_ok=True)
with open(os.path.join(config_dir, "config"), "w") as f:
    f.write("[reticulum]\nshare_instance = No\nenable_transport = False\n")
RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_CRITICAL)
router = LXMF.LXMRouter(storagepath=store_dir)

# Pin time AFTER RNS/LXMF init so the resource timestamp and the weight
# age term are deterministic. All modules share the time module object.
time.time = lambda: fixed_time
RNS.Transport.has_path = lambda h: True
RNS.Transport.request_path = lambda h: None

stamp = b"\xaa"*32
dest_hash = b"\x5e"*16

def make_entry(tid_hex, lxm_data, msg_size, stamp_value):
    tid = bytes.fromhex(tid_hex)
    path = os.path.join(store_dir, tid_hex + ".msg")
    with open(path, "wb") as f:
        f.write(lxm_data + stamp)
    router.propagation_entries[tid] = [dest_hash, path, fixed_time, msg_size, [], [], stamp_value]

# Four entries exercising the offer-preparation branches: tidC is dropped
# by the low-stamp-value filter, tidA exceeds the sync limit, tidB and tidD
# survive (offered smallest-weight-first).
make_entry("b0"*32, b"\x0b"*18, 50, 10)
make_entry("d0"*32, b"\x0d"*48, 80, 10)
make_entry("a0"*32, b"\x0a"*768, 800, 10)
make_entry("c0"*32, b"\x0c"*168, 200, 0)

P = LXMPeerMod.LXMPeer
receiver_hash = b"\x99"*16
peer = LXMPeerMod.LXMPeer(router, receiver_hash, sync_strategy=P.STRATEGY_PERSISTENT)
peer.identity = RNS.Identity()
peer.destination = RNS.Destination(peer.identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "propagation")
peer.propagation_stamp_cost = 5
peer.propagation_stamp_cost_flexibility = 2
peer.peering_cost = 1
peer.propagation_transfer_limit = 1000.0
peer.propagation_sync_limit = 1
peer.peering_key = [b"\x01"*32, 1]
peer.state = P.LINK_READY
peer.currently_transferring_messages = None
for tid_hex in ["b0"*32, "d0"*32, "a0"*32, "c0"*32]:
    peer.add_unhandled_message(bytes.fromhex(tid_hex))

captured = {}
class MockLink:
    def request(self, path, data, response_callback=None, failed_callback=None, progress_callback=None, timeout=None, **kw):
        captured["offer"] = data
        return None
    def identify(self, identity): return None
    def teardown(self): pass
peer.link = MockLink()

peer.sync()

with open(offer_out, "wb") as f:
    f.write(msgpack.packb(captured.get("offer")))

class MockReceipt:
    response = True
class MockResource:
    def __init__(self, data, link, callback=None, **kw):
        captured["resource"] = data
RNS.Resource = MockResource

peer.offer_response(MockReceipt())

with open(resource_out, "wb") as f:
    f.write(captured.get("resource") or b"")
`

// TestIntegrationOutboundPeerSyncGoToPython proves outbound peer-sync
// parity: the Go Peer.Sync LINK_READY offer-construction branch and the
// OfferResponse(True) resource-transfer branch produce byte-identical offer
// and resource bytes to the real Python LXMPeer state machine driven over
// the same controlled propagation entries, stamp costs, and size limits.
// This is the 24.B.12 capstone of the outbound peer-sync subsystem.
func TestIntegrationOutboundPeerSyncGoToPython(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir := testutils.TempDir(t, "lxmf-int-outbound-sync-*")

	// Run the Python golden-capture script.
	scriptPath := filepath.Join(tmpDir, "outbound_sync_golden.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfOutboundSyncGoldenPy), 0o600); err != nil {
		t.Fatalf("write python script: %v", err)
	}
	pyStore := filepath.Join(tmpDir, "py_store")
	if err := os.MkdirAll(pyStore, 0o700); err != nil {
		t.Fatalf("mkdir py store: %v", err)
	}
	offerGoldenPath := filepath.Join(tmpDir, "offer_golden.bin")
	resourceGoldenPath := filepath.Join(tmpDir, "resource_golden.bin")
	cmd := exec.Command("python3", scriptPath, pyStore, offerGoldenPath, resourceGoldenPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python golden-capture failed: %v\noutput=%s", err, string(out))
	}
	offerGolden, err := os.ReadFile(offerGoldenPath)
	if err != nil {
		t.Fatalf("read offer golden: %v", err)
	}
	resourceGolden, err := os.ReadFile(resourceGoldenPath)
	if err != nil {
		t.Fatalf("read resource golden: %v", err)
	}

	// Replicate the identical scenario on the Go side.
	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, "lxmf-int-outbound-sync-go-*"))

	fixedNow := time.Unix(2_000_000, 0)
	router.now = func() time.Time { return fixedNow }

	peeringKey0 := bytes.Repeat([]byte{0x01}, 32)
	stamp := bytes.Repeat([]byte{0xaa}, 32)
	destHash := bytes.Repeat([]byte{0x5e}, 16)
	receiverHash := bytes.Repeat([]byte{0x99}, 16)

	type entrySpec struct {
		tid      []byte
		lxmData  []byte
		size     int
		stampVal int
	}
	specs := []entrySpec{
		{bytes.Repeat([]byte{0xb0}, 32), bytes.Repeat([]byte{0x0b}, 18), 50, 10},
		{bytes.Repeat([]byte{0xd0}, 32), bytes.Repeat([]byte{0x0d}, 48), 80, 10},
		{bytes.Repeat([]byte{0xa0}, 32), bytes.Repeat([]byte{0x0a}, 768), 800, 10},
		{bytes.Repeat([]byte{0xc0}, 32), bytes.Repeat([]byte{0x0c}, 168), 200, 0},
	}
	for _, s := range specs {
		payload := append(append([]byte{}, s.lxmData...), stamp...)
		router.propagationEntries[string(s.tid)] = &propagationEntry{
			destinationHash: destHash,
			payload:         payload,
			receivedAt:      fixedNow,
			size:            s.size,
			stampValue:      s.stampVal,
			handledBy:       [][]byte{},
			unhandledBy:     [][]byte{receiverHash},
		}
	}

	peer := NewPeer(router, receiverHash)
	peer.now = func() time.Time { return fixedNow }
	stampCost, stampFlex, peeringCost := 5, 2, 1
	peer.propagationStampCost = &stampCost
	peer.propagationStampCostFlexibility = &stampFlex
	peer.peeringCost = &peeringCost
	transferLimit := 1000.0
	syncLimit := 1
	peer.propagationTransferLimit = &transferLimit
	peer.propagationSyncLimit = &syncLimit
	peer.peeringKey = []any{append([]byte{}, peeringKey0...), 1}
	peer.state = PeerStateLinkReady
	peer.hasPathFn = func([]byte) bool { return true }
	peer.identity = mustTestNewIdentity(t, true)
	peer.destination = mustTestNewDestination(
		t, ts, peer.identity, rns.DestinationOut, rns.DestinationSingle, AppName, "propagation")

	// Capture the offer via the requestLink seam.
	var capturedOffer any
	peer.requestLinkFn = func(_ *rns.Link, _ string, data any,
		_, _, _ func(*rns.RequestReceipt),
		_ time.Duration) (*rns.RequestReceipt, error) {
		capturedOffer = data
		return nil, nil
	}
	peer.link = &rns.Link{}

	peer.Sync()

	if capturedOffer == nil {
		t.Fatal("Sync did not send an offer (requestLink seam captured nothing)")
	}
	offerBytes, err := msgpack.Pack(capturedOffer)
	if err != nil {
		t.Fatalf("pack Go offer: %v", err)
	}
	if !bytes.Equal(offerBytes, offerGolden) {
		t.Errorf("Go offer bytes != Python golden offer:\n  Go:      %x\n  Python:  %x",
			offerBytes, offerGolden)
	}

	// Capture the resource data via the newResource seam.
	var capturedResource []byte
	router.newResource = func(data []byte, link *rns.Link) (*rns.Resource, error) {
		capturedResource = append([]byte{}, data...)
		return &rns.Resource{}, nil
	}

	receipt := &rns.RequestReceipt{}
	receipt.Response = true
	peer.OfferResponse(receipt)

	if capturedResource == nil {
		t.Fatal("OfferResponse(True) did not produce a resource (newResource seam captured nothing)")
	}
	if !bytes.Equal(capturedResource, resourceGolden) {
		t.Errorf("Go resource bytes != Python golden resource:\n  Go:      %x\n  Python:  %x",
			capturedResource, resourceGolden)
	}

	// Sanity: the offered list is exactly tidB then tidD (tidC dropped by
	// low stamp value, tidA skipped by the sync limit, smallest-weight-first).
	wantOfferHex := "92c420" + hex.EncodeToString(peeringKey0) +
		"92c420" + hex.EncodeToString(bytes.Repeat([]byte{0xb0}, 32)) +
		"c420" + hex.EncodeToString(bytes.Repeat([]byte{0xd0}, 32))
	if hex.EncodeToString(offerBytes) != wantOfferHex {
		t.Errorf("offer hex = %x, want %s", offerBytes, wantOfferHex)
	}
}
