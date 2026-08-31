// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestReannouncePerInterfaceNotGloballySuppressed verifies that each
// configured TCP client interface gets its own local-destination re-announce
// when it registers/connects.
//
// Background: on a fresh boot every configured client connects within a few
// seconds of START_ANNOUNCE_DELAY, so the app's first announce races out over
// whichever interfaces happen to be established at that instant (often none of
// the slow dials). The v0.53 onConnect re-announce compensates for exactly
// that race — but with ONE shared lastReannounce timestamp the first client's
// re-announce suppressed every sibling within minReannounceInterval, so the
// Local TCP Hub client (and any later dial) stayed silent and remote peers saw
// nothing until the next 6-hour periodic announce. Rate limiting must be
// PER INTERFACE.
func TestReannouncePerInterfaceNotGloballySuppressed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: mustHexDecode(t, "11223344556677889900112233445566")}

	announced := make(chan string, 8)
	ts.reannounceImpl = func(iface interfaces.Interface) {
		announced <- iface.Name()
	}

	mkClient := func(name string) *interfaces.TCPClientInterface {
		bi := interfaces.NewBaseInterface(name, interfaces.ModeFull, 1000)
		return &interfaces.TCPClientInterface{BaseInterface: bi}
	}

	a := mkClient("client-a")
	b := mkClient("client-b")
	ts.RegisterInterface(a)
	ts.RegisterInterface(b)

	// Both hooks dispatch on their own goroutine, so collect without order
	// assumptions and assert every interface got exactly its own re-announce.
	want := map[string]bool{"client-a": false, "client-b": false}
	deadline := time.After(2 * time.Second)
	for done := false; !done; {
		select {
		case got := <-announced:
			if _, known := want[got]; !known {
				t.Fatalf("unexpected re-announce source %q", got)
			}
			if want[got] {
				t.Fatalf("duplicate re-announce from %v", got)
			}
			want[got] = true
			done = want["client-a"] && want["client-b"]
		case <-deadline:
			for name, seen := range want {
				if !seen {
					t.Fatalf("re-announce for %v never fired: back-to-back interface registrations must not suppress each other", name)
				}
			}
			done = true
		}
	}
}

// TestReannounceSameInterfaceRateLimited verifies one interface still cannot
// spam re-announces faster than reconnectAnnounceMinInterval through its hook.
func TestReannounceSameInterfaceRateLimited(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())

	keyA := "TCPInterface[a/127.0.0.1:1]"
	now := time.Now()
	if !ts.reannounceDue(keyA, now) {
		t.Fatal("first re-announce for an interface must be allowed")
	}
	if ts.reannounceDue(keyA, now.Add(time.Minute)) {
		t.Fatal("second re-announce inside reconnectAnnounceMinInterval must be denied")
	}
	if !ts.reannounceDue("TCPInterface[b/127.0.0.1:2]", now.Add(time.Minute)) {
		t.Fatal("a DIFFERENT interface must not inherit another interface's throttle")
	}
	if !ts.reannounceDue(keyA, now.Add(reconnectAnnounceMinInterval+time.Second)) {
		t.Fatal("re-announce after reconnectAnnounceMinInterval must be allowed again")
	}
}

// TestReconnectAnnounceRequiresGenuineOutage pins the announce-parity
// behavior: an interface reconnecting after a BRIEF blip (down under
// reconnectAnnounceMinDown) must stay silent — path tables expire by age,
// not link state, python never re-announces on reconnect, and public
// transport operators police high-rate announcers (the fleet measured a
// flapping relay link driving node announces every ~4-5 min for 7 hours
// straight). A GENUINE outage (down >= reconnectAnnounceMinDown) still
// re-announces on recovery, and a first-time connect (no down record — the
// slow-dial boot race) always announces.
func TestReconnectAnnounceRequiresGenuineOutage(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())

	mkClient := func(name string) *interfaces.TCPClientInterface {
		bi := interfaces.NewBaseInterface(name, interfaces.ModeFull, 1000)
		return &interfaces.TCPClientInterface{BaseInterface: bi}
	}
	blip := mkClient("client-blip")
	outage := mkClient("client-outage")

	now := time.Now()
	// Both interfaces went down at some earlier point.
	ts.stampIfaceDown(blip.HashString(), now.Add(-4*time.Minute))
	ts.stampIfaceDown(outage.HashString(), now.Add(-15*time.Minute))

	if ts.reconnectAnnounceAllowed(blip, now) {
		t.Fatal("reconnect after a 4-minute blip must NOT re-announce (announce-parity): python stays silent for sub-threshold blips")
	}
	if !ts.reconnectAnnounceAllowed(outage, now) {
		t.Fatal("reconnect after a genuine outage must re-announce on recovery")
	}

	// A first-time connect with no down record (slow dial at boot) must
	// announce — the v0.53 boot-race compensation is preserved.
	first := mkClient("client-first")
	if !ts.reconnectAnnounceAllowed(first, now) {
		t.Fatal("first connect without any down record must re-announce (boot-race compensation)")
	}
}
