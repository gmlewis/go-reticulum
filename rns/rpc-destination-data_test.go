// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// destinationDataConfig is the shared two-instance config template used by the
// destination_data / identity_data RPC tests.
const destinationDataConfig = `[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`

// newDestinationDataInstances builds a shared instance (r1) and a connected
// client (r2) sharing transport ts, returning both plus a cleanup. The caller
// defers the cleanup.
func newDestinationDataInstances(t *testing.T, ts *TransportSystem) (r1, r2 *Reticulum) {
	t.Helper()
	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"
	cfg1 := testutils.TempDir(t, tempDirPrefix)
	cfg2 := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg1, fmt.Sprintf(destinationDataConfig, t.Name()+"-1", sharedPort, rpcPort, rpcKeyHex))
	writeConfig(t, cfg2, fmt.Sprintf(destinationDataConfig, t.Name()+"-2", sharedPort, rpcPort, rpcKeyHex))
	r1 = mustTestNewReticulum(t, ts, cfg1)
	r2 = mustTestNewReticulum(t, ts, cfg2)
	if !r2.isConnectedToSharedInstance {
		t.Fatal("expected second instance to be connected to shared instance")
	}
	return r1, r2
}

// TestDestinationDataRetainLocal covers Phase 13 task 10 local path:
// RetainDestinationData on a non-connected instance sets the known
// destination's retain flag (element 4 == -1) directly on the transport
// (Python Reticulum._retain_destination_data, RNS/Reticulum.py:1314-1326).
func TestDestinationDataRetainLocal(t *testing.T) {
	t.Parallel()
	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"
	cfg := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg, fmt.Sprintf(destinationDataConfig, t.Name(), sharedPort, rpcPort, rpcKeyHex))
	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)

	destHash := []byte("retain-local-dh!!!")
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), nil)

	ok, err := r.RetainDestinationData(destHash)
	if err != nil {
		t.Fatalf("RetainDestinationData error: %v", err)
	}
	if !ok {
		t.Fatal("RetainDestinationData returned false for a known destination")
	}
	got, _ := useTimestampOf(t, ts, destHash)
	if got != -1 {
		t.Fatalf("entry[4] = %v after local retain, want -1 (retained)", got)
	}
}

// TestDestinationDataOperationsViaRPC covers Phase 13 task 10 RPC path: a
// connected client routes destination_data used/retain/unretain to the shared
// instance, mutating the shared transport's known-destination entry
// (Python Reticulum.py:1281-1286, 1300-1340).
func TestDestinationDataOperationsViaRPC(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	r1, r2 := newDestinationDataInstances(t, ts)
	defer closeReticulum(t, r1)
	defer closeReticulum(t, r2)

	destHash := []byte("retain-rpc-dh!!!!")
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), nil)

	// retain via RPC: element 4 becomes -1.
	if ok, err := r2.RetainDestinationData(destHash); err != nil || !ok {
		t.Fatalf("RetainDestinationData via RPC ok=%v err=%v", ok, err)
	}
	if got, _ := useTimestampOf(t, ts, destHash); got != -1 {
		t.Fatalf("entry[4] = %v after RPC retain, want -1 (retained)", got)
	}

	// unretain via RPC: element 4 becomes a positive use timestamp.
	if ok, err := r2.UnretainDestinationData(destHash); err != nil || !ok {
		t.Fatalf("UnretainDestinationData via RPC ok=%v err=%v", ok, err)
	}
	if got, _ := useTimestampOf(t, ts, destHash); got <= 0 {
		t.Fatalf("entry[4] = %v after RPC unretain, want a positive use timestamp", got)
	}

	// used via RPC: element 4 stays a positive use timestamp.
	if ok, err := r2.UsedDestinationData(destHash); err != nil || !ok {
		t.Fatalf("UsedDestinationData via RPC ok=%v err=%v", ok, err)
	}
	if got, _ := useTimestampOf(t, ts, destHash); got <= 0 {
		t.Fatalf("entry[4] = %v after RPC used, want a positive use timestamp", got)
	}
}

// TestIdentityDataRetainViaRPC covers Phase 13 task 10 RPC path: a connected
// client routes identity_data retain to the shared instance, pinning every
// known destination owned by that identity (Python Reticulum.py:1288-1291,
// 1342-1357).
func TestIdentityDataRetainViaRPC(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	r1, r2 := newDestinationDataInstances(t, ts)
	defer closeReticulum(t, r1)
	defer closeReticulum(t, r2)

	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()
	identityHash := TruncatedHash(pubKey)
	destHash := []byte("identity-rpc-dh!!!")
	ts.Remember([]byte("pkt"), destHash, pubKey, nil)

	if ok, err := r2.RetainIdentity(identityHash); err != nil || !ok {
		t.Fatalf("RetainIdentity via RPC ok=%v err=%v", ok, err)
	}
	if got, _ := useTimestampOf(t, ts, destHash); got != -1 {
		t.Fatalf("entry[4] = %v after RPC identity retain, want -1 (retained)", got)
	}
}

// TestRPCDestinationDataAndIdentityDataEndpoints covers Phase 13 task 10 raw
// RPC frames: the shared instance handles {"destination_data": ...} and
// {"identity_data": "retain", ...} frames, returning the boolean result
// (Python Reticulum.py:1281-1291).
func TestRPCDestinationDataAndIdentityDataEndpoints(t *testing.T) {
	t.Parallel()
	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"
	cfg := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg, fmt.Sprintf(destinationDataConfig, t.Name(), sharedPort, rpcPort, rpcKeyHex))
	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)

	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	defer func() { _ = conn.Close() }()
	rpcWriteFrame(t, conn, map[string]any{"auth": rpcKeyHex})
	_ = rpcReadFrame(t, conn)

	destHash := bytes.Repeat([]byte{0x0A}, TruncatedHashLength/8)
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), nil)

	rpcWriteFrame(t, conn, map[string]any{"destination_data": "retain", "destination_hash": destHash})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected destination_data retain=true, got %#v", got)
	}
	if got, _ := useTimestampOf(t, ts, destHash); got != -1 {
		t.Fatalf("entry[4] = %v after RPC retain frame, want -1", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"destination_data": "unretain", "destination_hash": destHash})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected destination_data unretain=true, got %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"destination_data": "used", "destination_hash": destHash})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected destination_data used=true, got %#v", got)
	}

	// identity_data retain: a known destination whose pubkey truncates to the
	// identity hash gets retained.
	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()
	identityHash := TruncatedHash(pubKey)
	idDestHash := []byte("identity-frame-dh")
	ts.Remember([]byte("pkt-id"), idDestHash, pubKey, nil)
	rpcWriteFrame(t, conn, map[string]any{"identity_data": "retain", "identity_hash": identityHash})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected identity_data retain=true, got %#v", got)
	}
	if got, _ := useTimestampOf(t, ts, idDestHash); got != -1 {
		t.Fatalf("entry[4] = %v after RPC identity retain frame, want -1", got)
	}
}
