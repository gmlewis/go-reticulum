// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rrc

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// The hub-liveness watchdog: the hub pings every ~30 s, so total inbound
// silence means the hub instance is gone even though this process's link
// object still looks active. The RNS link watchdog cannot be relied on for
// this — its stale window is 2*clamp(rtt*(KEEPALIVE_MAX/KEEPALIVE_MAX_RTT),
// 5s, 360s) with the rtt measured once at establishment, so a link
// established during a load spike (rtt ~15 s observed live on the
// raspberrypi) stays "active" for up to 12 minutes after the hub died: the
// TUI shows Connected while the user's joins and messages vanish.

func TestHubLivenessWatchdogTearsDownSilentHub(t *testing.T) {
	mgr, hub, sent := pingFixture(t)
	_ = mgr
	hub.link = &rns.Link{}
	hub.Welcomed = true
	hub.Status = StatusConnected
	hub.hubLiveness = 60 * time.Millisecond
	torn := make(chan struct{})
	hub.livenessTeardownFn = func() { close(torn) }
	hub.lastHubTraffic.Store(time.Now().UnixNano())
	hub.startHubLivenessLoop()

	// This hub never pings (rrcd ships ping_interval_s = 0.0) and never
	// answers, so the watchdog must ask it directly with an RRC PING and only
	// declare it dead when that answer never comes.
	select {
	case <-torn:
	case <-time.After(3 * time.Second):
		t.Fatal("the liveness watchdog did not tear down a hub that stopped answering")
	}
	if !envelopeHasType(*sent, TypePing) {
		t.Error("the watchdog declared the hub dead without probing it first")
	}
}

// TestHubLivenessWatchdogRidesOutTransientSilence pins the 2026-09-13
// glenn-kamrui incident: the client sat on the RNS Community hub
// (rrc.hub.62b73cc9ecd8d9eceb66ce539b1c0060) from 08:26, heard its last
// envelope at 08:40:36, and stayed disconnected for three hours. The hub was
// healthy — the Beleth TCP interface's egress path had stalled (a 10 s write
// deadline expired at 08:44:44 and the interface reconnected at 08:44:49), so
// for ~4 minutes the hub looked exactly like a dead one from the client. A
// single unanswered probe torn the link down there (auto-reconnect is off by
// default, Python RRC.py:238), while Python's watchdog-less client rides the
// same outage out on the RNS link stale window. The watchdog must therefore
// re-probe a silent hub before it blames it — and still tear down a hub that
// never comes back.
func TestHubLivenessWatchdogRidesOutTransientSilence(t *testing.T) {
	mgr, hub, _ := pingFixture(t)
	_ = mgr
	hub.link = &rns.Link{}
	hub.Welcomed = true
	hub.Status = StatusConnected
	hub.hubLiveness = 120 * time.Millisecond

	torn := make(chan struct{})
	hub.livenessTeardownFn = func() { close(torn) }
	var mu sync.Mutex
	probes := 0
	hub.onSend = func(env map[any]any) {
		if intVal(env, KeyType) != TypePing {
			return
		}
		mu.Lock()
		probes++
		mu.Unlock()
	}
	probeCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return probes
	}

	hub.lastHubTraffic.Store(time.Now().UnixNano())
	hub.startHubLivenessLoop()

	// A silent hub is probed again instead of being torn down on the first
	// unanswered probe (the pre-fix behavior tore down here).
	deadline := time.Now().Add(3 * time.Second)
	for probeCount() < 2 && time.Now().Before(deadline) {
		select {
		case <-torn:
			t.Fatal("the watchdog tore the link down on the first unanswered probe")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := probeCount(); got < 2 {
		t.Fatalf("the watchdog sent %v liveness probes to a silent hub, want it to re-probe before tearing down", got)
	}

	// The outage heals and the hub answers: the escalation restarts and the
	// session survives a silence that already outlived the first probe grace.
	hub.HandleData(pingEnvelope(t, []byte("pingbody"), "healed"))
	healed := time.Now()
	for time.Since(healed) < 500*time.Millisecond {
		select {
		case <-torn:
			t.Fatal("the watchdog tore the link down after the hub started answering again")
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}

	// A hub that stays silent for the whole budget is still declared gone.
	select {
	case <-torn:
	case <-time.After(5 * time.Second):
		t.Fatalf("the watchdog never tore down a hub that stayed silent; probes sent: %v", probeCount())
	}
}

// envelopeHasType reports whether any captured envelope carries the given type.
func envelopeHasType(envelopes []map[any]any, want int) bool {
	for _, env := range envelopes {
		if intVal(env, KeyType) == want {
			return true
		}
	}
	return false
}

func TestHubLivenessWatchdogResetByTraffic(t *testing.T) {
	mgr, hub, _ := pingFixture(t)
	_ = mgr
	hub.link = &rns.Link{}
	hub.hubLiveness = 120 * time.Millisecond
	torn := make(chan struct{})
	hub.livenessTeardownFn = func() { close(torn) }
	hub.lastHubTraffic.Store(time.Now().UnixNano())
	hub.startHubLivenessLoop()

	// Steady hub traffic (the ~30 s pings, compressed here) must keep the
	// watchdog quiet for the whole window.
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		hub.HandleData(pingEnvelope(t, []byte("pingbody"), "mid"))
		select {
		case <-torn:
			t.Fatal("the watchdog tore down a link that was receiving hub traffic")
		default:
		}
		time.Sleep(40 * time.Millisecond)
	}
}

// TestHubLivenessWatchdogKeepsLinkToHubWithoutPings covers the hub
// configuration rrcd documents as its default: ping_interval_s = 0.0 disables
// hub pings (rrcd/config.py:35-36, EX1-RRCD.md:479-482 "Default: Disabled
// (because Reticulum already has link-level keepalives)"). A healthy hub that
// does not ping therefore sends an idle room nothing at all for minutes at a
// time, and the client must keep the link: tearing it down reconnects to a hub
// that was never gone, which the hub's owner sees as a flapping connection.
func TestHubLivenessWatchdogKeepsLinkToHubWithoutPings(t *testing.T) {
	t.Parallel()
	rig := newSharedHubRig(t, 0, 0)

	rig.hub.AnnounceOnce()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !rig.clientTS.HasPath(rig.hub.DestinationHash()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !rig.clientTS.HasPath(rig.hub.DestinationHash()) {
		t.Fatalf("client never learned the hub path; hub log:\n%v", rig.logs.String())
	}

	client := rig.manager.AddHub(rig.hub.DestinationHash(), "rrc.hub", "Public Hub")
	client.AddRoom("general")
	client.SetAutoReconnect(false, false)
	// The watchdog window must be in place before the link is established:
	// the loop snapshots it once per establishment.
	client.hubLiveness = 250 * time.Millisecond
	rig.client = client
	t.Cleanup(client.Disconnect)
	client.ConnectAsync()

	if !rig.waitWelcomed(10 * time.Second) {
		t.Fatalf("client never received WELCOME; hub log:\n%v", rig.logs.String())
	}

	// Well past the watchdog window, with the hub healthy but silent.
	time.Sleep(1500 * time.Millisecond)

	logs := rig.logs.String()
	client.lock.Lock()
	status, welcomed := client.Status, client.Welcomed
	client.lock.Unlock()
	if status != StatusConnected || !welcomed {
		t.Errorf("client status=%v welcomed=%v after 1.5s of silence from a non-pinging hub, want StatusConnected/true; hub log:\n%v",
			status, welcomed, logs)
	}
	if got := strings.Count(logs, "Link closed"); got != 0 {
		t.Errorf("hub logged %v link closes, want 0; hub log:\n%v", got, logs)
	}
}

// TestHubLivenessWatchdogDetectsWedgedHubAndReconnects covers the failure the
// watchdog exists for: a hub that still holds the link open but has stopped
// answering. RNS keeps such a link looking ACTIVE for up to ~12 minutes because
// its stale window comes from the RTT measured once at establishment, so the
// client would sit there "Connected" while its joins and messages vanish. With
// hub pings disabled (rrcd's own default) there is no hub traffic to notice the
// wedge, so the client must probe, give the probe time, tear the wedged link
// down, and let the normal reconnect path restore the session.
func TestHubLivenessWatchdogDetectsWedgedHubAndReconnects(t *testing.T) {
	t.Parallel()
	rig := newSharedHubRig(t, 0, 0)

	rig.hub.AnnounceOnce()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !rig.clientTS.HasPath(rig.hub.DestinationHash()) {
		time.Sleep(10 * time.Millisecond)
	}
	if !rig.clientTS.HasPath(rig.hub.DestinationHash()) {
		t.Fatalf("client never learned the hub path; hub log:\n%v", rig.logs.String())
	}

	client := rig.manager.AddHub(rig.hub.DestinationHash(), "rrc.hub", "Public Hub")
	client.AddRoom("general")
	client.SetAutoReconnect(true, false)
	client.hubLiveness = 400 * time.Millisecond
	rig.client = client
	t.Cleanup(client.Disconnect)
	client.ConnectAsync()
	if !rig.waitWelcomed(10 * time.Second) {
		t.Fatalf("client never received WELCOME; hub log:\n%v", rig.logs.String())
	}

	client.lock.Lock()
	wedged := client.link
	client.lock.Unlock()
	if wedged == nil {
		t.Fatal("the welcomed client has no link to wedge")
	}

	// The hub goes deaf while holding its side of the link open.
	rig.blockClientInbound.Store(true)
	if !waitLinkClosed(wedged, 5*time.Second) {
		t.Fatalf("the watchdog never tore down a wedged hub; hub log:\n%v", rig.logs.String())
	}

	// The hub answers again: the reconnect path must restore the session.
	//
	// Order matters here. rns.Link dispatches its link-closed callback on its
	// own goroutine after the status already reads LinkClosed, so the client
	// still reports the torn-down session's WELCOME for a moment after
	// waitLinkClosed returns — and reading that stale flag as "reconnected"
	// is exactly what made this test flake. So first wait for the close
	// handling to clear it (which also proves the reconnect it schedules is
	// armed), and only then open the inbound path, keeping the reconnect from
	// firing into a still-wedged hub.
	if !rig.waitNotWelcomed(5 * time.Second) {
		t.Fatalf("the client never dropped its WELCOME after the wedged link was torn down; hub log:\n%v", rig.logs.String())
	}
	rig.blockClientInbound.Store(false)
	if !rig.waitWelcomeCount(2, 15*time.Second) {
		t.Fatalf("the client did not reconnect to the recovered hub; hub log:\n%v", rig.logs.String())
	}
	if !rig.waitWelcomed(5 * time.Second) {
		t.Fatalf("the hub re-welcomed the reconnected client but the client never saw it; hub log:\n%v", rig.logs.String())
	}
}

// waitLinkClosed reports whether the given link reaches the closed state.
func waitLinkClosed(link *rns.Link, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if link.GetStatus() == rns.LinkClosed {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
