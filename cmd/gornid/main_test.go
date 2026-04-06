// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestNewRuntime(t *testing.T) {
	t.Parallel()

	rt := newRuntime(nil)
	if rt == nil {
		t.Fatal("newRuntime() returned nil")
	}
	if rt.logger == nil {
		t.Fatal("newRuntime() did not initialize a logger")
	}
	if rt.app == nil {
		t.Fatal("newRuntime() did not initialize the app state")
	}
}

func TestRuntimeLoadIdentityFallsBackToIdentityHash(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem()
	id, err := rns.NewIdentity(true)
	mustTest(t, err)

	destHash := rns.FullHash([]byte("gornid-test-destination"))[:rns.TruncatedHashLength/8]
	ts.Remember([]byte("packet-hash"), destHash, id.GetPublicKey(), nil)

	rt := newRuntime(nil)
	recalled := rt.loadIdentity(ts, id.HexHash, false, 0)
	if recalled == nil {
		t.Fatal("expected identity to be recalled by identity hash")
	}
	if recalled.HexHash != id.HexHash {
		t.Fatalf("recalled identity hash = %v, want %v", recalled.HexHash, id.HexHash)
	}
}
