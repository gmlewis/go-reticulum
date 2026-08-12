// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRPCHandlerRequestRecoversFromPanic covers Phase 12 task 11: the RPC
// request handler recovers from a panic while handling a local-client call,
// logs it, and returns an error response instead of propagating the panic —
// the Go analog of Python wrapping the shared-instance RPC loop body in
// try/except (RNS/Reticulum.py:1240-1296, v1.2.7). A nil transport makes the
// "drop path" branch dereference a nil interface and panic inside the handler.
func TestRPCHandlerRequestRecoversFromPanic(t *testing.T) {
	t.Parallel()
	r := &Reticulum{logger: mustTestLogger(t, LogDebug)} // transport is nil

	// A "drop path" request resolves the destination hash then calls
	// r.transport.InvalidatePath, which panics on the nil transport
	// interface. The handler must recover and return an error response.
	resp := r.handleRPCRequest(map[any]any{
		"drop":             "path",
		"destination_hash": []byte("some-destination-hash"),
	})

	m, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("expected error response map after panic, got %#v", resp)
	}
	if _, hasErr := m["error"]; !hasErr {
		t.Fatalf("expected an error key in the recovered response, got %#v", resp)
	}
}

// TestRPCListenerExitsOnClose covers Phase 12 task 11: stopping the shared
// instance (Close) signals the RPC listener loop to exit, and Close joins the
// loop's done channel so the listener has demonstrably stopped — the Go analog
// of Python's `while RNS.Transport._should_run:` loop guard
// (RNS/Reticulum.py:1241).
func TestRPCListenerExitsOnClose(t *testing.T) {
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
	if !r.isSharedInstance {
		t.Fatalf("expected shared instance")
	}

	r.mu.Lock()
	rpcDone := r.rpcDone
	r.mu.Unlock()
	if rpcDone == nil {
		t.Fatal("rpcDone channel was not created for the shared instance")
	}

	// Confirm the listener is actually serving before shutdown.
	conn := mustDialRPC(t, fmt.Sprintf("127.0.0.1:%v", rpcPort))
	_ = conn.Close()

	// Close must join the RPC listener loop and return within a deadline.
	if !completesWithin(func() {
		if err := r.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}, 3*time.Second) {
		t.Fatal("Close did not return within 3s; RPC listener loop may not exit on shutdown")
	}

	// The RPC listener loop must have exited: rpcDone is closed.
	select {
	case <-rpcDone:
	default:
		t.Fatal("RPC listener loop did not exit after Close (rpcDone still open)")
	}
}
