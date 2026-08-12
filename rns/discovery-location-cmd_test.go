// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// writeLocationScript writes an executable that prints "lat,lon,hgt" to stdout
// and returns its path. It is skipped on Windows (Python skips location_cmd on
// Windows too — RNS/Discovery.py:103).
func writeLocationScript(t *testing.T, contents string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("location_cmd is not evaluated on Windows")
	}
	dir := testutils.TempDir(t, "rns-location-cmd-")
	path := filepath.Join(dir, "location.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+contents+"\n"), 0o755); err != nil {
		t.Fatalf("write location script: %v", err)
	}
	return path
}

// TestParseDiscoveryConfigLocationCmd covers Phase 11 task 4's config side:
// the `location_cmd` key is parsed into DiscoveryConfig.LocationCmd when
// discovery is enabled (RNS/Reticulum.py:887).
func TestParseDiscoveryConfigLocationCmd(t *testing.T) {
	t.Parallel()
	cfg, _ := parseDiscoveryConfig(&ConfigSection{Properties: map[string]string{
		"discoverable": "yes",
		"location_cmd": "/usr/local/bin/where-am-i",
	}}, "TCPServerInterface", interfaces.ModeGateway)
	if !cfg.Discoverable {
		t.Fatalf("expected discoverable config")
	}
	if cfg.LocationCmd != "/usr/local/bin/where-am-i" {
		t.Fatalf("LocationCmd = %q, want %q", cfg.LocationCmd, "/usr/local/bin/where-am-i")
	}
}

// TestInterfaceAnnouncerLocationCmd covers Phase 11 task 4: when
// DiscoveryConfig.LocationCmd points at an executable, the announcer runs it
// at announce time, parses "lat,lon,hgt" from stdout, and injects the parsed
// coordinates into the announce info map — overriding any static config
// (RNS/Discovery.py:103-123).
func TestInterfaceAnnouncerLocationCmd(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("location_cmd is not evaluated on Windows")
	}

	script := writeLocationScript(t, "printf '12.345, -67.890, 1234.5 '")

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcp", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPServerInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Located TCP",
		ReachableOn:       "discovery.example.net",
		LocationCmd:       script,
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

	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldLatitude)); got != 12.345 {
		t.Fatalf("latitude = %v, want 12.345 (from location_cmd)", got)
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldLongitude)); got != -67.890 {
		t.Fatalf("longitude = %v, want -67.890 (from location_cmd)", got)
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldHeight)); got != 1234.5 {
		t.Fatalf("height = %v, want 1234.5 (from location_cmd)", got)
	}
}

// TestInterfaceAnnouncerLocationCmdOverridesStatic covers Phase 11 task 4:
// the executable output overrides any statically-configured latitude/longitude/
// height (Python sets interface.discovery_latitude from the script, ignoring
// the config value when location_cmd is present).
func TestInterfaceAnnouncerLocationCmdOverridesStatic(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("location_cmd is not evaluated on Windows")
	}

	script := writeLocationScript(t, "printf '1.0, 2.0, 3.0'")

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	staticLat, staticLon, staticHgt := 99.0, 88.0, 77.0
	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcp", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPServerInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Override TCP",
		ReachableOn:       "discovery.example.net",
		LocationCmd:       script,
		Latitude:          &staticLat,
		Longitude:         &staticLon,
		Height:            &staticHgt,
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

	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldLatitude)); got != 1.0 {
		t.Fatalf("latitude = %v, want 1.0 (location_cmd overrides static %v)", got, staticLat)
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldLongitude)); got != 2.0 {
		t.Fatalf("longitude = %v, want 2.0 (location_cmd overrides static %v)", got, staticLon)
	}
	if got := asFloat64(lookupDiscoveryValue(info, discoveryFieldHeight)); got != 3.0 {
		t.Fatalf("height = %v, want 3.0 (location_cmd overrides static %v)", got, staticHgt)
	}
}

// TestInterfaceAnnouncerLocationCmdAbortOnBadComponentCount covers Phase 11
// task 4: a location_cmd whose output does not split into exactly three
// comma-separated components aborts the announce — getInterfaceAnnounceData
// returns (nil, nil) just as Python returns None (RNS/Discovery.py:114).
func TestInterfaceAnnouncerLocationCmdAbortOnBadComponentCount(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("location_cmd is not evaluated on Windows")
	}

	script := writeLocationScript(t, "printf '1.0, 2.0'")

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcp", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPServerInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Bad Location",
		LocationCmd:       script,
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v, want nil (abort returns nil,nil)", err)
	}
	if appData != nil {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want nil (abort on bad location)", len(appData))
	}
}

// TestInterfaceAnnouncerLocationCmdAbortOnOutOfRangeLatitude covers Phase 11
// task 4: a latitude outside [-90, 90] aborts the announce
// (RNS/Discovery.py:118).
func TestInterfaceAnnouncerLocationCmdAbortOnOutOfRangeLatitude(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("location_cmd is not evaluated on Windows")
	}

	script := writeLocationScript(t, "printf '91.0, 0.0, 0.0'")

	logger := NewLogger()
	ts := newAnnounceCaptureTransport(logger)
	transportIdentity := mustTestNewIdentity(t, true)
	ts.identity = transportIdentity
	ts.SetEnabled(true)

	r := &Reticulum{
		transport: ts,
		logger:    logger,
	}
	announcer := NewInterfaceAnnouncer(r, logger)

	iface := &announceTestInterface{
		BaseInterface: interfaces.NewBaseInterface("announce-tcp", interfaces.ModeGateway, 1000),
		ifaceType:     "TCPServerInterface",
		bindIP:        "127.0.0.1",
		bindPort:      4242,
	}
	iface.SetDiscoveryConfig(interfaces.DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        6,
		Name:              "Bad Lat",
		LocationCmd:       script,
	})

	appData, err := announcer.getInterfaceAnnounceData(iface)
	if err != nil {
		t.Fatalf("getInterfaceAnnounceData() error = %v, want nil (abort returns nil,nil)", err)
	}
	if appData != nil {
		t.Fatalf("getInterfaceAnnounceData() returned %v bytes, want nil (abort on out-of-range latitude)", len(appData))
	}
}
