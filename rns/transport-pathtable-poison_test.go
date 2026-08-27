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

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// poisonedBlob returns a 10-byte random blob whose embedded uint40 emission
// timebase is garbage. Blobs like these were written into persisted
// destination_table files by pre-fix binaries that misparsed truncated
// announces: the values (10^10 .. 10^12) are far beyond any unix timestamp
// (real emissions in 2026 are ~0x6A9xxxxx ≈ 1.79e9, always starting with a
// 0x00 byte).
func poisonedBlob(v uint64) []byte {
	b := make([]byte, 10)
	b[5] = byte(v >> 32)
	b[6] = byte(v >> 24)
	b[7] = byte(v >> 16)
	b[8] = byte(v >> 8)
	b[9] = byte(v)
	return b
}

// validBlob returns a 10-byte random blob with a plausible emission timebase
// (offset seconds before now, matching Python's get_random_hash()[0:5] +
// int(time.time()).to_bytes(5, "big") layout).
func validBlob(secondsAgo uint64) []byte {
	tb := uint64(time.Now().Unix()) - secondsAgo
	b := make([]byte, 10)
	b[5] = byte(tb >> 32)
	b[6] = byte(tb >> 24)
	b[7] = byte(tb >> 16)
	b[8] = byte(tb >> 8)
	b[9] = byte(tb)
	return b
}

// TestPlausibleAnnounceTimebase pins the plausibility predicate used to
// neutralize poisoned random blobs: real emission timestamps sit near local
// time (and are always < 2^32 until 2106), while the observed poisoned blobs
// carry values from ~6e10 to ~1.1e12.
func TestPlausibleAnnounceTimebase(t *testing.T) {
	t.Parallel()
	now := time.Now()

	valid := []uint64{
		uint64(now.Unix()),                   // now
		uint64(now.Unix()) - 86400,           // a day ago
		0x6A906D80,                           // a real 2026 emission from the fleet logs
		uint64(now.Unix()) + 3600,            // one hour of clock skew is tolerated
		uint64(minPlausibleAnnounceTimebase), // exact floor
	}
	for _, tb := range valid {
		if !plausibleAnnounceTimebase(tb, now) {
			t.Errorf("plausibleAnnounceTimebase(%#x) = false, want true", tb)
		}
	}

	poisoned := []uint64{
		0xB7F1963C4E,                      // nano's stored garbage (~789e9)
		0xFFFBA686BE,                      // the Mac hub's stored garbage (~1.1e12)
		0x11A79C9FAE,                      // penguin's stored garbage (~7.6e10)
		uint64(now.Unix()) + 86400 + 3600, // future beyond the skew window
		1,                                 // pre-RNS epoch
		0,
	}
	for _, tb := range poisoned {
		if plausibleAnnounceTimebase(tb, now) {
			t.Errorf("plausibleAnnounceTimebase(%#x) = true, want false", tb)
		}
	}
}

// TestTimebaseFromRandomBlobsIgnoresPoisonedBlobs verifies that a poisoned
// blob no longer blocks path replacement: the timebase comparison must see
// only plausible emissions, so an entry whose stored blobs are all garbage
// behaves like an entry with no blobs (timebase 0) and the next real announce
// replaces it. This is the self-healing half of the nano fleet bug, where
// poisoned timebases silently blocked every announce handler from firing.
func TestTimebaseFromRandomBlobsIgnoresPoisonedBlobs(t *testing.T) {
	t.Parallel()

	allPoison := timebaseFromRandomBlobs([][]byte{
		poisonedBlob(0xB7F1963C4E),
		poisonedBlob(0xFFFBA686BE),
	})
	if allPoison != 0 {
		t.Errorf("timebaseFromRandomBlobs(all poisoned) = %#x, want 0", allPoison)
	}

	mixed := timebaseFromRandomBlobs([][]byte{
		poisonedBlob(0xB7F1963C4E),
		validBlob(60),
	})
	if want := uint64(time.Now().Unix()) - 60; mixed < want-5 || mixed > want {
		t.Errorf("timebaseFromRandomBlobs(mixed) = %#x, want ~%#x", mixed, want)
	}

	// Short blobs carry no timebase and stay neutral.
	if got := timebaseFromRandomBlobs([][]byte{{0xAA}}); got != 0 {
		t.Errorf("timebaseFromRandomBlobs(short blob) = %#x, want 0", got)
	}
}

// writeTableFile writes a Python-layout destination_table file (8 fields per
// entry: [destHash, timestamp, nextHop, hops, expires, blobs, ifaceHash,
// packetHash]) for the load tests.
func writeTableFile(t *testing.T, entries []([]any)) string {
	t.Helper()
	dir := t.TempDir()
	payload := make([]any, 0, len(entries))
	for _, e := range entries {
		payload = append(payload, e)
	}
	encoded, err := msgpack.Pack(payload)
	if err != nil {
		t.Fatalf("msgpack.Pack: %v", err)
	}
	path := filepath.Join(dir, "destination_table")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return dir
}

// TestLoadPathTableSanitizesPoisonedBlobs verifies self-healing of the
// persisted destination_table: entries written by pre-fix binaries with
// poisoned random blobs load with the poisoned blobs removed and plausible
// blobs kept, so the next periodic persistPathTable writes the cleaned state
// back to disk and the table heals on the first start with fixed code.
func TestLoadPathTableSanitizesPoisonedBlobs(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.storagePath = writeTableFile(t, []([]any){
		{
			[]byte{0xBC, 0x37, 0x34, 0x8E, 0xC2, 0x7F, 0xAF, 0xAD, 0x10, 0xF3, 0xFD, 0x2E, 0x92, 0xEC, 0xF5, 0xF5},
			float64(time.Now().Add(-26 * time.Hour).Unix()),
			[]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10},
			12, // poisoned hop count from a public-relay-only route
			float64(time.Now().Add(5 * 24 * time.Hour).Unix()),
			[]any{
				poisonedBlob(0xB7F1963C4E),
				validBlob(3600),
				poisonedBlob(0xFFFBA686BE),
			},
			[]byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20},
			[]byte{0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2A, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F, 0x30},
		},
	})

	ts.mu.Lock()
	ts.loadPathTableLocked()
	entry, ok := ts.pathTable[string([]byte{0xBC, 0x37, 0x34, 0x8E, 0xC2, 0x7F, 0xAF, 0xAD, 0x10, 0xF3, 0xFD, 0x2E, 0x92, 0xEC, 0xF5, 0xF5})]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("poisoned entry was dropped; want it retained with sanitized blobs")
	}
	if entry.Hops != 12 {
		t.Errorf("entry.Hops = %v, want 12 (entry retained)", entry.Hops)
	}
	if got := len(entry.RandomBlobs); got != 1 {
		t.Fatalf("entry.RandomBlobs = %v blobs, want 1 (poisoned blobs dropped)", got)
	}
	if tb := timebaseFromRandomBlobs(entry.RandomBlobs); tb == 0 {
		t.Error("remaining blob lost its timebase; want the plausible one kept")
	}
}

// TestAnnounceReplacesPoisonedPathAndFiresHandler is the fleet-symptom
// regression test: nano's poisoned destination_table entries (garbage blob
// timebases + a high hop count from a relay-only route) silently blocked
// every later announce — ValidateAnnounce passed, but the path table never
// replaced the entry and the announce handler never fired, so the node never
// saw its peers. With poisoned blobs neutralized, a valid lower-hop announce
// must replace the entry AND fire the handler.
func TestAnnounceReplacesPoisonedPathAndFiresHandler(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "poison-heal")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	handlerFired := make(chan []byte, 1)
	ts.RegisterAnnounceHandler(&AnnounceHandler{
		// Empty AspectFilter matches every announce (the gate under test is
		// the shouldForwardToLocalClients firing gate, not the filter).
		AspectFilter: "",
		ReceivedAnnounce: func(destHash []byte, announcedIdentity *Identity, appData []byte) {
			handlerFired <- destHash
		},
	})

	iface := &capturingInterface{name: "rx-poison", gravity: 0}

	// Poisoned state as found on the fleet: a 40-hop route learned via public
	// relays, with a garbage timebase that blocks every replacement.
	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Timestamp:   time.Now().Add(-150 * time.Minute),
		NextHop:     []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		Hops:        40,
		Expires:     time.Now().Add(7 * 24 * time.Hour),
		RandomBlobs: [][]byte{poisonedBlob(0xFFFBA686BE)},
	}
	ts.mu.Unlock()

	// A valid announce emitted now, arriving over the direct hub connection
	// with far fewer hops.
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix()))
	p.Hops = 1
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	ts.Inbound(append([]byte(nil), p.Raw...), iface)

	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("path table entry disappeared")
	}
	if entry.Hops != 2 {
		t.Errorf("entry.Hops = %v, want 2 (poisoned 40-hop path replaced by the 2-hop announce)", entry.Hops)
	}

	select {
	case <-handlerFired:
	default:
		t.Error("announce handler did not fire; poisoned entry still blocks announce delivery")
	}
}

// TestAnnounceWithImplausibleEmissionDoesNotPoison verifies the forward
// direction: an announce signed by a node with a badly skewed clock (its
// emission timebase far in the future) must not store its poisoned blob,
// so later announces from correctly-clocked peers keep replacing the path.
func TestAnnounceWithImplausibleEmissionDoesNotPoison(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "future-clock")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	iface := &capturingInterface{name: "rx-future", gravity: 0}

	// First announce: legitimate emission, installs the path.
	p1 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix()))
	p1.Hops = 1
	if err := p1.Pack(); err != nil {
		t.Fatalf("Pack p1: %v", err)
	}
	ts.Inbound(append([]byte(nil), p1.Raw...), iface)

	// Second announce: emission ~400 days in the future (bad clock), more
	// hops. Its emission beats the stored timebase, so it replaces — but its
	// blob must not be stored, or every future announce would be blocked.
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix())+400*86400)
	p2.Hops = 3
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack p2: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), iface)

	ts.mu.Lock()
	entry := ts.pathTable[string(dest.Hash)]
	blobs := append([][]byte(nil), entry.RandomBlobs...)
	ts.mu.Unlock()
	for _, b := range blobs {
		if tb := timebaseFromRandomBlobs([][]byte{b}); !plausibleAnnounceTimebase(tb, time.Now()) {
			t.Errorf("stored blob has implausible timebase %#x; poisoned blob was persisted", tb)
		}
	}

	// Third announce: normal clock, fewer hops than the future-clock one.
	// It must still replace the path (the future blob was not stored, so the
	// comparison sees only the first announce's plausible timebase).
	p3 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix())+1)
	p3.Hops = 1
	if err := p3.Pack(); err != nil {
		t.Fatalf("Pack p3: %v", err)
	}
	ts.Inbound(append([]byte(nil), p3.Raw...), iface)

	ts.mu.Lock()
	hops := ts.pathTable[string(dest.Hash)].Hops
	ts.mu.Unlock()
	if hops != 2 {
		t.Errorf("entry.Hops = %v, want 2 (normal-clock announce replaced the future-clock path)", hops)
	}
}
