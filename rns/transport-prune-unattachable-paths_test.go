// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPrunePathEntriesWithoutInterface pins the Python load-time guarantee that
// a path entry is only kept when its receiving interface still exists.
//
// Python builds a path-table entry only when find_interface_from_hash() finds
// the interface (RNS/Transport.py:329, `if announce_packet != None and
// receiving_interface != None`), and drops the entry otherwise. The Go port
// loads the table before its interfaces exist and reattaches by interface hash
// afterwards, which leaves entries whose interface hash matches nothing behind
// with a nil Interface. Those entries are harmful: an outbound packet for such
// a destination cannot use the path branch and falls through to the broadcast
// fallback (which reports the packet sent), so on a node behind a shared
// instance — or any node whose stale entry is the only one — the packet is
// silently dropped instead of the sender being told there is no usable path and
// requesting a fresh one. Pruning restores Python's invariant so the caller
// sees HasPath == false and rediscovers the path.
func TestPrunePathEntriesWithoutInterface(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(testSilentLogger())
	dir := testutils.TempDir(t, "rns-prune-paths")
	if err := ts.Start(dir); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// One live interface, whose hash a persisted entry can legitimately carry.
	pipe := interfaces.NewPipeInterface("live", func([]byte, interfaces.Interface) {})
	ts.RegisterInterface(pipe)
	t.Cleanup(func() { _ = pipe.Detach() })

	liveHash := interfaceHash(pipe)
	if liveHash == nil {
		t.Fatal("interfaceHash(live) = nil")
	}
	deadHash := make([]byte, len(liveHash))
	copy(deadHash, liveHash)
	deadHash[0] ^= 0xFF // an interface that no longer exists

	liveDest := mustTruncHash(t, 0xA1)
	deadDest := mustTruncHash(t, 0xB2)

	now := time.Now()
	entry := func(dest, ifaceHash []byte) []any {
		return []any{
			dest,
			float64(now.UnixNano()) / 1e9,
			mustTruncHash(t, 0x11),
			1,
			float64(now.Add(7*24*time.Hour).UnixNano()) / 1e9,
			[]any{},
			ifaceHash,
			mustTruncHash(t, byte(dest[0])),
		}
	}
	packed, err := msgpack.Pack([]any{entry(liveDest, liveHash), entry(deadDest, deadHash)})
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(pathTableFile(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathTableFile(dir), packed, 0o600); err != nil {
		t.Fatal(err)
	}

	ts.LoadPathTable()

	if !ts.HasPath(liveDest) {
		t.Fatal("entry whose interface exists was pruned")
	}
	if ts.HasPath(deadDest) {
		t.Fatal("entry whose interface no longer exists survived the prune: outbound packets for it fall through to the broadcast fallback and are silently lost")
	}
}

func mustTruncHash(t *testing.T, seed byte) []byte {
	t.Helper()
	h := make([]byte, TruncatedHashLength/8)
	for i := range h {
		h[i] = seed + byte(i)
	}
	return h
}
