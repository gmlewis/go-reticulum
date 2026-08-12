// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
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

	ts := rns.NewTransportSystem(nil)
	id, err := rns.NewIdentity(true, nil)
	mustTest(t, err)

	destHash := rns.FullHash([]byte("gornid-test-destination"))[:rns.TruncatedHashLength/8]
	ts.Remember([]byte("packet-hash"), destHash, id.GetPublicKey(), nil)

	rt := newRuntime(nil)
	recalled, _ := rt.loadIdentity(ts, id.HexHash, false, false, 0)
	if recalled == nil {
		t.Fatal("expected identity to be recalled by identity hash")
	}
	if recalled.HexHash != id.HexHash {
		t.Fatalf("recalled identity hash = %v, want %v", recalled.HexHash, id.HexHash)
	}
}

func TestLoadIdentityNoCacheSkipsRecall(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	id, err := rns.NewIdentity(true, nil)
	mustTest(t, err)

	// Remember the identity so recall would succeed without noCache.
	ts.Remember([]byte("packet-hash"), id.Hash, id.GetPublicKey(), nil)

	rt := newRuntime(nil)
	// With noCache=true, loadIdentity must skip the recall and return nil
	// for a hex hash path (not a file), matching Python's no_cache branch.
	recalled, exitCode := rt.loadIdentity(ts, id.HexHash, false, true, 0)
	if recalled != nil {
		t.Fatalf("noCache expected nil identity, got %v", recalled.HexHash)
	}
	if exitCode != 0 {
		t.Errorf("noCache exitCode = %v, want 0", exitCode)
	}
}

func TestNoCacheFlagParsed(t *testing.T) {
	t.Parallel()
	app, err := parseFlags([]string{"-N"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags failed: %v", err)
	}
	if !app.noCache {
		t.Fatal("noCache flag not set")
	}
}
