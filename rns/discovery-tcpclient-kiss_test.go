// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// TestInterfaceAnnouncerTCPClientWithoutKISSAborts verifies that a
// TCPClientInterface that is not KISS-framed aborts the discovery announce
// (RNS/Discovery.py:139-141 — "Invalid interface discovery configuration ...
// aborting discovery announce", return None). getInterfaceAnnounceData must
// return a nil payload and no error.
func TestInterfaceAnnouncerTCPClientWithoutKISSAborts(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	r := &Reticulum{transport: ts, logger: logger}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcpclient", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPClientInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
		kiss:          false,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Plain TCP Client",
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v, want nil", err)
	}
	if appData != nil {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want nil (non-KISS TCPClient aborts)", len(appData))
	}
}

// TestInterfaceAnnouncerTCPClientWithKISSAnnounces verifies that a
// KISS-framed TCPClientInterface does NOT abort — it advertises as
// KISSInterface (RNS/Discovery.py:186-187 rewrites INTERFACE_TYPE to
// KISSInterface). getInterfaceAnnounceData returns a real announce whose
// interface_type field is "KISSInterface".
func TestInterfaceAnnouncerTCPClientWithKISSAnnounces(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	r := &Reticulum{transport: ts, logger: logger}
	announcer := NewInterfaceAnnouncer(r, logger)

	freq, bw := 868000000, 12500
	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcpclient-kiss", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPClientInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
		kiss:          true,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "KISS TCP Client",
		Frequency:         &freq,
		Bandwidth:         &bw,
		Modulation:        "LoRa",
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v", err)
	}
	if len(appData) <= 1+discoveryStampSize {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want > %v", len(appData), 1+discoveryStampSize)
	}

	payload := appData[1:]
	packed := payload[:len(payload)-discoveryStampSize]
	unpacked, err := msgpack.Unpack(packed)
	if err != nil {
		t.Fatalf("msgpack.Unpack() error = %v", err)
	}
	info := asAnyMap(unpacked)
	if info == nil {
		t.Fatalf("unexpected announce payload type %T", unpacked)
	}
	if got := lookupDiscoveryValue(info, discoveryFieldInterfaceType); got != "KISSInterface" {
		t.Fatalf("interface_type = %v, want %q (KISS-framed TCPClient advertises as KISSInterface)", got, "KISSInterface")
	}
}
