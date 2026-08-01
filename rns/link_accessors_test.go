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

// establishLoopbackLink wires two TransportSystems together with a pipe,
// registers an IN destination on the receiver, and establishes a link from
// the initiator to it. It returns the active initiator link and the receiver
// destination (so tests can register request handlers on it).
func establishLoopbackLink(t *testing.T) (initiator *Link, receiverDest *Destination) {
	t.Helper()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest = mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	initiator = mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(initiator.Teardown)

	establishedInitiator := make(chan bool, 1)
	initiator.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := initiator.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}
	select {
	case l := <-establishedReceiver:
		t.Cleanup(l.Teardown)
		if l.status != LinkActive {
			t.Fatalf("receiver link not active: %v", l.status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}
	if initiator.status != LinkActive {
		t.Fatalf("initiator link not active: %v", initiator.status)
	}
	return initiator, receiverDest
}

// TestLinkCapabilityAccessors covers Phase B.1: GetMTU/GetMDU/GetMode against
// an established link, with golden values captured from a matching Python
// link (rns/Link.py get_mtu/get_mdu/get_mode; RNS.Reticulum.MTU=500, computed
// MDU=431, default MODE_AES256_CBC=1). MTU/MDU return nil when not active.
func TestLinkCapabilityAccessors(t *testing.T) {
	t.Parallel()

	initiator, _ := establishLoopbackLink(t)

	// Golden values captured from Python (RNS.Reticulum.MTU=500,
	// MDU = floor((500-1-19-48)/16)*16 - 1 = 431, MODE_AES256_CBC=1).
	if got := initiator.GetMTU(); got == nil || *got != MTU {
		t.Fatalf("GetMTU() = %v, want %v", got, MTU)
	}
	if got := initiator.GetMDU(); got == nil || *got != MDU {
		t.Fatalf("GetMDU() = %v, want %v", got, MDU)
	}
	if got := initiator.GetMode(); got != LinkModeAES256CBC {
		t.Fatalf("GetMode() = %v, want %v", got, LinkModeAES256CBC)
	}

	// A fresh, non-active link returns nil MTU/MDU (Python returns None when
	// status != ACTIVE) but still reports its mode.
	inactive := mustTestNewLink(t, newTestTransportSystem(t), mustTestNewDestination(t, newTestTransportSystem(t), mustTestNewIdentity(t, true), DestinationIn, DestinationSingle, "d"))
	if got := inactive.GetMTU(); got != nil {
		t.Fatalf("inactive GetMTU() = %v, want nil", *got)
	}
	if got := inactive.GetMDU(); got != nil {
		t.Fatalf("inactive GetMDU() = %v, want nil", *got)
	}
	if got := inactive.GetMode(); got != LinkModeAES256CBC {
		t.Fatalf("inactive GetMode() = %v, want %v", got, LinkModeAES256CBC)
	}
}

// TestLinkAgeAccessor covers Phase B.2: GetAge returns nil before activation
// (Python get_age returns None when activated_at is unset) and a non-negative
// seconds-since-activation afterwards.
func TestLinkAgeAccessor(t *testing.T) {
	t.Parallel()

	// Not yet activated -> nil.
	plain := &Link{}
	if got := plain.GetAge(); got != nil {
		t.Fatalf("GetAge() before activation = %v, want nil", *got)
	}

	// Activated with an injected clock -> now - activatedAt.
	now := time.Unix(1700000000, 0)
	link := &Link{activatedAt: now.Add(-10 * time.Second), now: func() time.Time { return now }}
	got := link.GetAge()
	if got == nil {
		t.Fatal("GetAge() after activation = nil, want 10s")
	}
	if *got < 9.9 || *got > 10.1 {
		t.Fatalf("GetAge() = %v, want ~10s", *got)
	}
}

// TestLinkRateAccessors covers Phase B.3: GetEstablishmentRate returns
// establishment_rate*8 (bps) or nil, and GetExpectedRate returns the measured
// rate*8 (bps) or nil. The formulae are captured from Python Link.py:600-636
// (get_establishment_rate = establishment_rate*8) and Link.py:1290
// (expected_rate = (size*8)/transfer_time).
func TestLinkRateAccessors(t *testing.T) {
	t.Parallel()

	// establishmentRate = establishmentCost/rtt = 1000 bytes / 0.5 s = 2000 B/s.
	// get_establishment_rate returns establishment_rate*8 = 16000 bps.
	link := &Link{establishmentRate: 1000.0 / 0.5}
	if got := link.GetEstablishmentRate(); got == nil || *got != 16000 {
		t.Fatalf("GetEstablishmentRate() = %v, want 16000", got)
	}

	// Before measurement -> nil (Python None).
	zero := &Link{}
	if got := zero.GetEstablishmentRate(); got != nil {
		t.Fatalf("GetEstablishmentRate() unset = %v, want nil", *got)
	}
	if got := zero.GetExpectedRate(); got != nil {
		t.Fatalf("GetExpectedRate() unset = %v, want nil", *got)
	}

	// expectedRate stored as bytes/sec (size*8/transfer_seconds); accessor
	// returns it in bps, i.e. *8. A 1000-byte resource over 0.5 s stores
	// 1000*8/0.5 = 16000 B/s, accessor returns 128000 bps.
	link2 := &Link{expectedRate: 1000.0 * 8 / 0.5}
	if got := link2.GetExpectedRate(); got == nil || *got != 128000 {
		t.Fatalf("GetExpectedRate() = %v, want 128000", got)
	}
}

// TestLinkEstablishmentRateWiredEndToEnd confirms the establishment-rate wiring
// fires during a real handshake: after loopback establishment, the initiator
// link (which receives the proof and computes establishment_cost/rtt in
// ValidateProof) exposes a non-nil, positive establishment rate.
func TestLinkEstablishmentRateWiredEndToEnd(t *testing.T) {
	t.Parallel()

	initiator, _ := establishLoopbackLink(t)
	if got := initiator.GetEstablishmentRate(); got == nil || *got <= 0 {
		t.Fatalf("GetEstablishmentRate() = %v, want a positive rate after establishment", got)
	}
}

// TestLinkSaltContextAccessors covers Phase B.4: GetSalt returns the link_id
// (Python get_salt returns self.link_id) and GetContext returns nil (Python
// get_context always returns None).
func TestLinkSaltContextAccessors(t *testing.T) {
	t.Parallel()

	link := &Link{linkID: []byte{0x01, 0x02, 0x03}}
	if got := link.GetSalt(); !bytes.Equal(got, []byte{0x01, 0x02, 0x03}) {
		t.Fatalf("GetSalt() = %x, want 010203", got)
	}
	// GetSalt must return a copy.
	link.GetSalt()[0] = 0xff
	if link.linkID[0] != 0x01 {
		t.Fatal("GetSalt() did not return a defensive copy")
	}
	if got := link.GetContext(); got != nil {
		t.Fatalf("GetContext() = %v, want nil", got)
	}
}

// TestLinkPHYStatsToggle pins Phase B.5: TrackPHYStats toggles whether
// GetRSSI/GetSNR/GetQ return the tracked values (Python track_phy_stats /
// get_rssi / get_snr / get_q). With tracking disabled (the default) all three
// return nil; once enabled they return the recorded physical-layer values.
func TestLinkPHYStatsToggle(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(ts, id, DestinationIn, DestinationSingle, "test", "app")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	link, err := NewLink(ts, dest)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	// Default: tracking disabled -> nil.
	if got := link.GetRSSI(); got != nil {
		t.Fatalf("GetRSSI() before tracking = %v, want nil", *got)
	}
	if got := link.GetSNR(); got != nil {
		t.Fatalf("GetSNR() before tracking = %v, want nil", *got)
	}
	if got := link.GetQ(); got != nil {
		t.Fatalf("GetQ() before tracking = %v, want nil", *got)
	}

	// Enable tracking and record values.
	link.TrackPHYStats(true)
	link.mu.Lock()
	link.rssi = -72.5
	link.snr = 12.25
	link.q = 0.87
	link.mu.Unlock()

	if got := link.GetRSSI(); got == nil || *got != -72.5 {
		t.Fatalf("GetRSSI() = %v, want -72.5", got)
	}
	if got := link.GetSNR(); got == nil || *got != 12.25 {
		t.Fatalf("GetSNR() = %v, want 12.25", got)
	}
	if got := link.GetQ(); got == nil || *got != 0.87 {
		t.Fatalf("GetQ() = %v, want 0.87", got)
	}

	// Disabling again hides the values.
	link.TrackPHYStats(false)
	if got := link.GetRSSI(); got != nil {
		t.Fatalf("GetRSSI() after disable = %v, want nil", *got)
	}
}

// TestLinkPing covers Phase B.6: Ping sends an RTT probe over an established
// link and returns a non-negative round-trip time. The probe is a normal RNS
// request to LinkPingPath; the remote end must register a handler for it.
func TestLinkPing(t *testing.T) {
	t.Parallel()

	initiator, receiverDest := establishLoopbackLink(t)

	// Register a ping echo handler on the receiver destination so the probe
	// gets a response. Any non-nil response suffices.
	receiverDest.RegisterRequestHandler(
		LinkPingPath,
		func(path string, _ []byte, _ []byte, _ []byte, _ *Identity, _ time.Time) any {
			_ = path
			return []byte("pong")
		},
		AllowAll, nil, false,
	)

	rtt, err := initiator.Ping()
	if err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if rtt < 0 {
		t.Fatalf("Ping() RTT = %v, want non-negative", rtt)
	}

	// Ping on a non-active link fails immediately.
	inactive := &Link{}
	if _, err := inactive.Ping(); err == nil {
		t.Fatal("Ping() on inactive link succeeded, want error")
	}
}

// TestLinkPingNoHandlerTimesOut confirms Ping returns an error (rather than
// hanging) when the remote end has no registered ping handler.
func TestLinkPingNoHandlerTimesOut(t *testing.T) {
	t.Parallel()

	initiator, _ := establishLoopbackLink(t)
	// No ping handler registered on the receiver.

	_, err := initiator.Ping()
	if err == nil {
		t.Fatal("Ping() without a remote handler succeeded, want timeout/error")
	}
}
