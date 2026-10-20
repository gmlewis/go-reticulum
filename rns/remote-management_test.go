// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

// TestRemoteStatusHandler pins Python Transport.remote_status_handler
// (Reticulum.py:3253-3268): an unauthenticated request gets nothing; the
// response is [interface_stats] plus link_count when the first element is true.
func TestRemoteStatusHandler(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	r := &Reticulum{transport: ts}
	remote := mustTestNewIdentity(t, true)

	if got := r.remoteStatusHandler([]any{true}, nil); got != nil {
		t.Errorf("unauthenticated request = %v, want nil", got)
	}
	if got := r.remoteStatusHandler("not-a-list", remote); got != nil {
		t.Errorf("non-list request = %v, want nil", got)
	}

	got, ok := r.remoteStatusHandler([]any{false}, remote).([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("response = %v, want [interface_stats]", r.remoteStatusHandler([]any{false}, remote))
	}
	if _, ok := got[0].(map[string]any); !ok {
		t.Errorf("response[0] = %T, want the interface stats map", got[0])
	}

	got, ok = r.remoteStatusHandler([]any{true}, remote).([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("response = %v, want [interface_stats, link_count]", got)
	}
	if _, ok := got[1].(int); !ok {
		t.Errorf("response[1] = %T, want the link count", got[1])
	}
}

// TestRemotePathHandler pins Python Transport.remote_path_handler
// (Transport.py:3271-3301): "table" returns the path table (optionally filtered
// to one destination hash), "rates" the rate table, and an unknown command
// returns nothing.
func TestRemotePathHandler(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	r := &Reticulum{transport: ts}
	remote := mustTestNewIdentity(t, true)

	dest := bytes.Repeat([]byte{0x11}, TruncatedHashLength/8)
	other := bytes.Repeat([]byte{0x22}, TruncatedHashLength/8)
	ts.mu.Lock()
	ts.pathTable[string(dest)] = &PathEntry{Hops: 2, Expires: time.Now().Add(time.Hour)}
	ts.pathTable[string(other)] = &PathEntry{Hops: 3, Expires: time.Now().Add(time.Hour)}
	ts.mu.Unlock()

	all, ok := r.remotePathHandler([]any{"table"}, remote).([]any)
	if !ok || len(all) != 2 {
		t.Fatalf("table response = %v, want 2 entries", r.remotePathHandler([]any{"table"}, remote))
	}
	filtered, ok := r.remotePathHandler([]any{"table", dest}, remote).([]any)
	if !ok || len(filtered) != 1 {
		t.Fatalf("filtered table = %v, want 1 entry", r.remotePathHandler([]any{"table", dest}, remote))
	}
	row, _ := filtered[0].(map[string]any)
	if hash, _ := row["hash"].([]byte); !bytes.Equal(hash, dest) {
		t.Errorf("filtered hash = %x, want %x", hash, dest)
	}

	if got := r.remotePathHandler([]any{"bogus"}, remote); got != nil {
		t.Errorf("unknown command = %v, want nil", got)
	}
	if got := r.remotePathHandler([]any{"table"}, nil); got != nil {
		t.Errorf("unauthenticated request = %v, want nil", got)
	}
}

// TestRegisterRemoteManagementDestination pins the registration half
// (Transport.py:356-362): the destination exists and serves /status and /path
// under an allow-list ACL.
func TestRegisterRemoteManagementDestination(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	allowed := bytes.Repeat([]byte{0x33}, TruncatedHashLength/8)
	ts.SetRemoteManagement(true, [][]byte{allowed})

	if err := ts.registerRemoteManagementDestination(); err != nil {
		t.Fatalf("registerRemoteManagementDestination: %v", err)
	}
	if ts.remoteManagementDestination == nil {
		t.Fatal("remote management destination not recorded")
	}
	if !ts.remoteManagementDestination.HasRequestHandler("/status") {
		t.Error("destination has no /status handler")
	}
	if !ts.remoteManagementDestination.HasRequestHandler("/path") {
		t.Error("destination has no /path handler")
	}
}
