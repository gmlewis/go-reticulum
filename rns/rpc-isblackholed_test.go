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

// TestIsBlackholedLocal verifies that the public IsBlackholed API
// queries the local transport's blackhole registry when the instance is not
// connected to a shared instance (Python Reticulum.is_blackholed,
// RNS/Reticulum.py:1705-1717, the `else` branch).
func TestIsBlackholedLocal(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	cfg := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`, t.Name(), sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)
	if r.isConnectedToSharedInstance {
		t.Fatal("shared instance reports connectedToSharedInstance; expected local path")
	}

	hash := bytes.Repeat([]byte{0x42}, TruncatedHashLength/8)
	if ok, err := r.BlackholeIdentity(hash, nil, "test"); err != nil || !ok {
		t.Fatalf("BlackholeIdentity ok=%v err=%v", ok, err)
	}

	got, err := r.IsBlackholed(hash)
	if err != nil {
		t.Fatalf("IsBlackholed error: %v", err)
	}
	if !got {
		t.Fatal("IsBlackholed returned false for a blackholed identity, want true")
	}

	unknown := bytes.Repeat([]byte{0x99}, TruncatedHashLength/8)
	gotUnknown, err := r.IsBlackholed(unknown)
	if err != nil {
		t.Fatalf("IsBlackholed(unknown) error: %v", err)
	}
	if gotUnknown {
		t.Fatal("IsBlackholed returned true for an unknown identity, want false")
	}
}

// TestIsBlackholedViaRPC verifies that a client connected to a
// shared instance routes IsBlackholed to the shared instance via RPC
// (Python Reticulum.is_blackholed, RNS/Reticulum.py:1711-1715).
func TestIsBlackholedViaRPC(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	configTemplate := `[reticulum]
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
	cfg1 := testutils.TempDir(t, tempDirPrefix)
	cfg2 := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg1, fmt.Sprintf(configTemplate, t.Name()+"-1", sharedPort, rpcPort, rpcKeyHex))
	writeConfig(t, cfg2, fmt.Sprintf(configTemplate, t.Name()+"-2", sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r1 := mustTestNewReticulum(t, ts, cfg1)
	defer closeReticulum(t, r1)
	r2 := mustTestNewReticulum(t, ts, cfg2)
	defer closeReticulum(t, r2)
	if !r2.isConnectedToSharedInstance {
		t.Fatal("expected second instance to be connected to shared instance")
	}

	hash := bytes.Repeat([]byte{0x55}, TruncatedHashLength/8)
	// BlackholeIdentity on the connected client routes to the shared instance
	// via RPC, installing the entry on r1's transport.
	if ok, err := r2.BlackholeIdentity(hash, nil, "test"); err != nil || !ok {
		t.Fatalf("BlackholeIdentity ok=%v err=%v", ok, err)
	}

	got, err := r2.IsBlackholed(hash)
	if err != nil {
		t.Fatalf("IsBlackholed via RPC error: %v", err)
	}
	if !got {
		t.Fatal("IsBlackholed via RPC returned false for a blackholed identity, want true")
	}

	unknown := bytes.Repeat([]byte{0x77}, TruncatedHashLength/8)
	gotUnknown, err := r2.IsBlackholed(unknown)
	if err != nil {
		t.Fatalf("IsBlackholed(unknown) via RPC error: %v", err)
	}
	if gotUnknown {
		t.Fatal("IsBlackholed via RPC returned true for an unknown identity, want false")
	}
}

// TestRPCIsBlackholedEndpoint verifies that the shared instance
// handles the raw {"get": "is_blackholed", "identity_hash": ...} RPC frame,
// returning the boolean blackhole membership (Python Reticulum.py:1263).
func TestRPCIsBlackholedEndpoint(t *testing.T) {
	t.Parallel()

	sharedPort := reserveTCPPort(t)
	rpcPort := reserveTCPPort(t)
	rpcKeyHex := "00112233445566778899aabbccddeeff"

	cfg := testutils.TempDir(t, tempDirPrefix)
	writeConfig(t, cfg, fmt.Sprintf(`[reticulum]
instance_name = %v
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v
rpc_key = %v

[logging]
loglevel = 4

[interfaces]
`, t.Name(), sharedPort, rpcPort, rpcKeyHex))

	ts := NewTransportSystem(nil)
	r := mustTestNewReticulum(t, ts, cfg)
	defer closeReticulum(t, r)

	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	defer func() { _ = conn.Close() }()
	rpcWriteFrame(t, conn, map[string]any{"auth": rpcKeyHex})
	_ = rpcReadFrame(t, conn)

	hash := bytes.Repeat([]byte{0x33}, TruncatedHashLength/8)
	rpcWriteFrame(t, conn, map[string]any{"blackhole_identity": hash, "until": int64(0), "reason": "test"})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected blackhole_identity=true, got %#v", got)
	}

	rpcWriteFrame(t, conn, map[string]any{"get": "is_blackholed", "identity_hash": hash})
	if got := rpcReadFrame(t, conn); !asBool(got) {
		t.Fatalf("expected is_blackholed=true for blackholed identity, got %#v", got)
	}

	unknown := bytes.Repeat([]byte{0x88}, TruncatedHashLength/8)
	rpcWriteFrame(t, conn, map[string]any{"get": "is_blackholed", "identity_hash": unknown})
	if got := rpcReadFrame(t, conn); asBool(got) {
		t.Fatalf("expected is_blackholed=false for unknown identity, got %#v", got)
	}
}
