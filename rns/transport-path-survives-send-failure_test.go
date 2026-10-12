// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestFailedSendKeepsPathAndIdentity pins Python's path-lifetime model: a
// transmit that fails on an interface never removes path-table entries or
// known-destination (identity) entries. Python's Transport has no such
// behaviour — every path_table mutation there is one of disk load, the
// age-based cull, a timestamp refresh, an announce add/update, expire_path or
// a blackholed identity (RNS/Transport.py) — so a failed write is a dropped
// packet and nothing more. A configured TCP client that goes down keeps its
// registration in Transport.interfaces (TCPInterface.py:452-453 removes only
// non-initiator interfaces), so its paths stay until the age-based cull.
//
// The Go port instead purged the whole path table (and every identity behind
// it) when one send failed on an interface that reported itself up: on
// 2026-10-12 a single failed transmit at 08:36:34 took the local node's path
// table from 8707 entries to 0 and its known-destination store from 8708 to
// 1, leaving it blind for the rest of a six-hour hub outage. These cases lock
// in the Python behaviour for every send path that used to purge.
func TestFailedSendKeepsPathAndIdentity(t *testing.T) {
	t.Parallel()

	// keepHash is installed via the failing interface in every case; the
	// per-case survivors list names the hashes that must still be present.
	const keepHash = "keep-via-half-open-interface"

	cases := []struct {
		name string
		// send performs the failing transmit and returns the additional
		// path hashes (besides keepHash) that must survive it.
		send func(t *testing.T, ts *TransportSystem, dead *stormIface) []string
	}{
		{
			name: "control frame",
			send: func(t *testing.T, ts *TransportSystem, dead *stormIface) []string {
				t.Helper()
				_ = ts.sendControlFrame(dead, []byte{0xC0, 0x02, 0x01}, "forwarding path request")
				return nil
			},
		},
		{
			name: "rebroadcast announce",
			send: func(t *testing.T, ts *TransportSystem, dead *stormIface) []string {
				t.Helper()
				ts.sendRebroadcast(dead, []byte{0x60, 0x00, 0x01, 0x02})
				return nil
			},
		},
		{
			name: "outbound along a known path",
			send: func(t *testing.T, ts *TransportSystem, dead *stormIface) []string {
				t.Helper()
				dest := mustTestNewDestination(t, ts, ts.identity, DestinationOut, DestinationSingle, "testapp", "pathsend")
				hash := string(dest.Hash)
				ts.mu.Lock()
				ts.pathTable[hash] = &PathEntry{
					Interface: dead, Hops: 1, Timestamp: time.Now(),
					Expires: time.Now().Add(time.Hour),
				}
				ts.knownDestinations[hash] = []any{1}
				ts.mu.Unlock()

				p := NewPacketWithTransport(ts, dest, []byte("hello"))
				if err := p.Pack(); err != nil {
					t.Fatalf("pack failed: %v", err)
				}
				if err := ts.Outbound(p); err != nil {
					t.Fatalf("outbound failed: %v", err)
				}
				return []string{hash}
			},
		},
		{
			name: "outbound broadcast with no path",
			send: func(t *testing.T, ts *TransportSystem, dead *stormIface) []string {
				t.Helper()
				dest := mustTestNewDestination(t, ts, ts.identity, DestinationOut, DestinationSingle, "testapp", "nosuchpath")
				p := NewPacketWithTransport(ts, dest, []byte("hello"))
				if err := p.Pack(); err != nil {
					t.Fatalf("pack failed: %v", err)
				}
				if err := ts.Outbound(p); err != nil {
					t.Fatalf("outbound failed: %v", err)
				}
				return nil
			},
		},
		{
			name: "inbound path-table forward",
			send: func(t *testing.T, ts *TransportSystem, dead *stormIface) []string {
				t.Helper()
				dest := bytes.Repeat([]byte{0x22}, 16)
				hash := string(dest)
				ts.mu.Lock()
				ts.pathTable[hash] = &PathEntry{
					Interface: dead, Hops: 1, Timestamp: time.Now(),
					Expires: time.Now().Add(time.Hour),
				}
				ts.knownDestinations[hash] = []any{1}
				transportID := copyBytes(ts.identity.Hash)
				ts.mu.Unlock()

				ts.Inbound(transportDataFrame(transportID, dest, 1), dead)
				return []string{hash}
			},
		},
		{
			name: "inbound link-transport forward",
			send: func(t *testing.T, ts *TransportSystem, dead *stormIface) []string {
				t.Helper()
				linkID := bytes.Repeat([]byte{0x5a}, 16)
				ts.mu.Lock()
				ts.linkTable[string(linkID)] = &LinkEntry{
					Timestamp:         time.Now(),
					Hops:              1,
					RemainingHops:     1,
					OutboundInterface: dead,
					ReceivedInterface: dead,
					DestinationHash:   bytes.Repeat([]byte{0x11}, 16),
				}
				ts.mu.Unlock()

				ts.Inbound(linkDataFrame(linkID, 1), dead)
				return nil
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ts := NewTransportSystem(nil)
			ts.identity = mustTestNewIdentity(t, true)
			ts.SetEnabled(true)

			// A half-open peer: it reports itself up while every write fails.
			// That is the transition the old code treated as a reason to purge
			// the path table.
			dead := newStormIface("half-open")
			dead.setUp(true)
			ts.interfaces = append(ts.interfaces, dead)

			ts.mu.Lock()
			ts.pathTable[keepHash] = &PathEntry{
				Interface: dead, Hops: 2, Timestamp: time.Now(),
				Expires: time.Now().Add(time.Hour),
			}
			ts.knownDestinations[keepHash] = []any{1}
			ts.mu.Unlock()

			survivors := append([]string{keepHash}, tc.send(t, ts, dead)...)
			ts.WaitOutboundSends()

			for _, hash := range survivors {
				if !ts.HasPath([]byte(hash)) {
					t.Errorf("path %q was removed by a failed send; Python keeps it until the age-based cull", hash)
				}
				ts.mu.Lock()
				_, identityKept := ts.knownDestinations[hash]
				ts.mu.Unlock()
				if !identityKept {
					t.Errorf("known-destination entry for %q was removed by a failed send; Python never removes these on a failed write", hash)
				}
			}
		})
	}
}

// TestCullExpiredPathsRemovesOnlyVanishedInterfaces pins Python's one rule for
// purging a path because of its interface: the age cull drops entries whose
// attached interface no longer exists (Transport.py:798-801, "Path to … was
// removed since the attached interface no longer exists"). An interface that is
// merely down keeps its paths — which is what lets a configured TCP client ride
// out an outage with its path table intact — and so does a live spawned
// interface that never enters the interface registry, such as the
// per-connection local client a shared instance serves.
func TestCullExpiredPathsRemovesOnlyVanishedInterfaces(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)

	up := &dummyInterface{name: "up"}
	down := newStormIface("down")
	down.setUp(false)
	spawned := newDetachedInterface("spawned-alive")
	vanished := newDetachedInterface("vanished")
	vanished.setDetached(true)

	// Neither the spawned client nor the torn-down interface is in the
	// registry: a shared instance keeps its per-connection clients on the
	// parent server interface.
	ts.interfaces = append(ts.interfaces, up, down)

	future := time.Now().Add(time.Hour)
	for hash, iface := range map[string]interfaces.Interface{
		"via-up":      up,
		"via-down":    down,
		"via-spawned": spawned,
		"via-gone":    vanished,
	} {
		ts.pathTable[hash] = &PathEntry{Interface: iface, Hops: 1, Timestamp: time.Now(), Expires: future}
		ts.announceTable[hash] = &AnnounceEntry{}
		ts.pathRequests[hash] = time.Now()
	}

	ts.cullExpiredPaths(time.Now())

	if !ts.HasPath([]byte("via-up")) {
		t.Error("path via a registered, up interface was culled")
	}
	if !ts.HasPath([]byte("via-down")) {
		t.Error("path via a registered but DOWN interface was culled; Python keeps the path until it ages out")
	}
	if !ts.HasPath([]byte("via-spawned")) {
		t.Error("path via a live spawned interface was culled; only a torn-down interface may drop its paths")
	}
	if ts.HasPath([]byte("via-gone")) {
		t.Error("path via a torn-down interface survived the cull")
	}
}

// detachedInterface is a plain interface whose teardown state a test controls.
type detachedInterface struct {
	dummyInterface
	detached atomic.Bool
}

func newDetachedInterface(name string) *detachedInterface {
	d := &detachedInterface{}
	d.name = name
	return d
}

func (d *detachedInterface) setDetached(detached bool) { d.detached.Store(detached) }

func (d *detachedInterface) IsDetached() bool { return d.detached.Load() }
