// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// validValidationAnnounceAppData builds an app-data announce with a valid
// 16-byte transport_id and the given field overrides, at stamp cost 2.
func validValidationAnnounceAppData(t *testing.T, overrides map[any]any) []byte {
	t.Helper()
	payload := map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   make([]byte, 16),
		discoveryFieldName:          "Valid",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
		discoveryFieldLatitude:      nil,
		discoveryFieldLongitude:     nil,
		discoveryFieldHeight:        nil,
	}
	for k, v := range overrides {
		payload[k] = v
	}
	return mustDiscoveryAnnounceAppData(t, payload, 2)
}

// validationHandler builds a standalone InterfaceAnnounceHandler whose
// callback records whether it fired.
func validationHandler(t *testing.T, requiredValue int) (*InterfaceAnnounceHandler, *bool) {
	t.Helper()
	tmpDir := testutils.TempDir(t, "rns-discovery-validation-")
	ts := NewTransportSystem(nil)
	r := &Reticulum{configDir: tmpDir, transport: ts, logger: NewLogger()}
	called := false
	h := NewInterfaceAnnounceHandler(r, requiredValue, func(info map[string]any) {
		called = true
	})
	return h, &called
}

// TestDiscoveryRejectsNonBoolTransport covers Phase 11 task 5: the transport
// field must be exactly a bool (RNS/Discovery.py:305). An int transport is
// rejected — the callback must not fire.
func TestDiscoveryRejectsNonBoolTransport(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldTransport: 1,
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if *called {
		t.Fatal("expected non-bool transport announce to be rejected")
	}
}

// TestDiscoveryRejectsIntLatitude covers Phase 11 task 5: latitude must be
// nil or float, not int (RNS/Discovery.py:306).
func TestDiscoveryRejectsIntLatitude(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldLatitude: 12,
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if *called {
		t.Fatal("expected int latitude announce to be rejected")
	}
}

// TestDiscoveryRejectsBoolLongitude covers Phase 11 task 5: longitude must be
// nil or float, not bool (RNS/Discovery.py:307).
func TestDiscoveryRejectsBoolLongitude(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldLongitude: true,
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if *called {
		t.Fatal("expected bool longitude announce to be rejected")
	}
}

// TestDiscoveryRejectsShortTransportID covers Phase 11 task 5: transport_id
// must be exactly TRUNCATED_HASHLENGTH/8 (16) bytes (RNS/Discovery.py:309).
func TestDiscoveryRejectsShortTransportID(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldTransportID: []byte{0xde, 0xad, 0xbe, 0xef},
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if *called {
		t.Fatal("expected short transport_id announce to be rejected")
	}
}

// TestDiscoveryRejectsIntTransportID covers Phase 11 task 5: transport_id
// must be a byte string; an int has no len() in Python and raises, aborting
// the announce (RNS/Discovery.py:309).
func TestDiscoveryRejectsIntTransportID(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldTransportID: 123,
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if *called {
		t.Fatal("expected int transport_id announce to be rejected")
	}
}

// TestDiscoveryRejectsUnknownInterfaceType covers Phase 11 task 5: an
// interface_type not in DISCOVERABLE_INTERFACE_TYPES is rejected
// (RNS/Discovery.py:310-312).
func TestDiscoveryRejectsUnknownInterfaceType(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "BogusInterface",
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if *called {
		t.Fatal("expected unknown interface_type announce to be rejected")
	}
}

// TestDiscoveryCoercesIFACNetnameToString covers Phase 11 task 5:
// ifac_netname is coerced via str() (RNS/Discovery.py:330). A bytes value
// arrives as the Python bytes repr "b'mesh'".
func TestDiscoveryCoercesIFACNetnameToString(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-ifac-coerce-")
	ts := NewTransportSystem(nil)
	r := &Reticulum{configDir: tmpDir, transport: ts, logger: NewLogger()}
	var info map[string]any
	h := NewInterfaceAnnounceHandler(r, 2, func(got map[string]any) {
		info = cloneStringAnyMap(got)
	})
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldIFACNetname: []byte("mesh"),
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if info == nil {
		t.Fatal("expected callback to fire for valid announce with bytes ifac_netname")
	}
	if got, ok := info["ifac_netname"]; !ok || got != "b'mesh'" {
		t.Fatalf("ifac_netname = %#v (ok=%v), want %q (Python str(b'mesh'))", got, ok, "b'mesh'")
	}
}

// TestDiscoveryAcceptsFloatLatitude covers Phase 11 task 5: a float latitude
// is accepted (the callback fires), confirming the nil-or-float check does
// not over-reject valid floats.
func TestDiscoveryAcceptsFloatLatitude(t *testing.T) {
	t.Parallel()
	h, called := validationHandler(t, 2)
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldLatitude: 12.5,
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if !*called {
		t.Fatal("expected float latitude announce to be accepted")
	}
}
