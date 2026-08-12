// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestDetermineCompressionSupportFromAppData covers Phase 14 task 3:
// DetermineCompressionSupport reconciles m.AutoCompress with the destination's
// recalled announce app-data, mirroring Python's
// LXMessage.determine_compression_support (LXMF/LXMessage.py:510-513, v1.1.0):
// empty app-data defaults to supported, a v0.5.0+ functionality list containing
// SF_COMPRESSION yields supported, and a list omitting it yields unsupported.
func TestDetermineCompressionSupportFromAppData(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		hex      string
		compress bool
	}{
		// No recalled announce: default to compression supported.
		{"empty_app_data_defaults_supported", "", true},
		// [b"Alice", 100, [0]] — functionality list contains SF_COMPRESSION.
		{"three_with_sf_compression", "93c405416c696365649100", true},
		// [b"Alice", 100, []] — empty functionality list omits SF_COMPRESSION.
		{"three_empty_functionality_list", "93c405416c6963656490", false},
		// [b"Alice", 100, [1]] — other functionality only.
		{"three_other_functionality_only", "93c405416c696365649101", false},
		// Original raw-string format: compression supported.
		{"original_string_format", "a5416c696365", true},
		// Two-element list (no functionality list): default supported.
		{"two_element_no_functionality_list", "92c405416c69636564", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Message{AutoCompress: false}
			var appData []byte
			if tc.hex != "" {
				appData = mustHexDecode(t, tc.hex)
			}
			if err := m.DetermineCompressionSupport(appData); err != nil {
				t.Fatalf("DetermineCompressionSupport(%q): unexpected error %v", tc.hex, err)
			}
			if m.AutoCompress != tc.compress {
				t.Errorf("AutoCompress = %v, want %v", m.AutoCompress, tc.compress)
			}
		})
	}
}

// TestDetermineCompressionSupportMalformed covers Phase 14 task 3: a malformed
// v0.5.0+-prefixed app-data payload yields a non-nil error rather than a
// silent default, mirroring the umsgpack exception that propagates from
// Python's compression_support_from_app_data.
func TestDetermineCompressionSupportMalformed(t *testing.T) {
	t.Parallel()
	m := &Message{AutoCompress: false}
	malformed := mustHexDecode(t, "93c405416c")
	if err := m.DetermineCompressionSupport(malformed); err == nil {
		t.Fatal("expected error for malformed app data, got nil")
	}
}

// TestNewMessageDefaultsAutoCompressTrue covers Phase 14 task 3: a freshly
// constructed outbound message defaults AutoCompress to true, matching
// Python's LXMessage.__init__ (LXMF/LXMessage.py:146, v1.1.0).
func TestNewMessageDefaultsAutoCompressTrue(t *testing.T) {
	t.Parallel()
	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	m, err := NewMessage(destination, source, "hello", "", nil)
	if err != nil {
		t.Fatalf("NewMessage: %v", err)
	}
	if !m.AutoCompress {
		t.Fatal("new message AutoCompress = false, want true (Python default)")
	}
}

// TestAsResourceDirectPassesAutoCompress covers Phase 14 task 3: the
// DIRECT-delivery resource path passes m.AutoCompress into the resource
// options, mirroring Python's LXMessage.__as_resource
// (LXMF/LXMessage.py:654, v1.1.0). A highly compressible packed payload is
// compressed when AutoCompress is true and left uncompressed when false.
func TestAsResourceDirectPassesAutoCompress(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	// A highly compressible packed payload large enough that bzip2 shrinks it.
	compressible := bytes.Repeat([]byte("AAAAAAAAAAAAAAAA"), 1024)

	link, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateLink(t, link)

	// AutoCompress true: the resource is compressed.
	mCompress := mustTestNewMessage(t, destination, source, "x", "", nil)
	mCompress.Method = MethodDirect
	mCompress.Packed = compressible
	mCompress.AutoCompress = true
	mCompress.setDeliveryDestination(link)
	rCompress, err := mCompress.asResource()
	if err != nil {
		t.Fatalf("asResource (compress): %v", err)
	}
	if !rCompress.IsCompressed() {
		t.Fatal("expected compressed resource when AutoCompress=true")
	}

	// AutoCompress false: the same payload is left uncompressed.
	link2, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateLink(t, link2)
	mPlain := mustTestNewMessage(t, destination, source, "x", "", nil)
	mPlain.Method = MethodDirect
	mPlain.Packed = compressible
	mPlain.AutoCompress = false
	mPlain.setDeliveryDestination(link2)
	rPlain, err := mPlain.asResource()
	if err != nil {
		t.Fatalf("asResource (plain): %v", err)
	}
	if rPlain.IsCompressed() {
		t.Fatal("expected uncompressed resource when AutoCompress=false")
	}
}

// TestAsResourceDirectAutoCompressFromAppData covers Phase 14 task 3 end-to-end:
// DetermineCompressionSupport driven by recalled app-data flows through to the
// DIRECT resource's compression flag. A functionality list omitting
// SF_COMPRESSION disables compression even for a compressible payload.
func TestAsResourceDirectAutoCompressFromAppData(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	compressible := bytes.Repeat([]byte("BBBBBBBBBBBBBBBB"), 1024)

	// app-data whose functionality list omits SF_COMPRESSION: [b"A", 1, [1]].
	unsupportedAppData := mustHexDecode(t, "93c40141019101")
	// app-data whose functionality list contains SF_COMPRESSION: [b"A", 1, [0]].
	supportedAppData := mustHexDecode(t, "93c40141019100")

	// Unsupported → AutoCompress false → uncompressed resource.
	link1, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateLink(t, link1)
	m1 := mustTestNewMessage(t, destination, source, "x", "", nil)
	m1.Method = MethodDirect
	m1.Packed = compressible
	if err := m1.DetermineCompressionSupport(unsupportedAppData); err != nil {
		t.Fatalf("DetermineCompressionSupport (unsupported): %v", err)
	}
	if m1.AutoCompress {
		t.Fatal("AutoCompress = true for unsupported functionality list, want false")
	}
	m1.setDeliveryDestination(link1)
	r1, err := m1.asResource()
	if err != nil {
		t.Fatalf("asResource (unsupported): %v", err)
	}
	if r1.IsCompressed() {
		t.Fatal("expected uncompressed resource for unsupported functionality list")
	}

	// Supported → AutoCompress true → compressed resource.
	link2, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateLink(t, link2)
	m2 := mustTestNewMessage(t, destination, source, "x", "", nil)
	m2.Method = MethodDirect
	m2.Packed = compressible
	if err := m2.DetermineCompressionSupport(supportedAppData); err != nil {
		t.Fatalf("DetermineCompressionSupport (supported): %v", err)
	}
	if !m2.AutoCompress {
		t.Fatal("AutoCompress = false for supported functionality list, want true")
	}
	m2.setDeliveryDestination(link2)
	r2, err := m2.asResource()
	if err != nil {
		t.Fatalf("asResource (supported): %v", err)
	}
	if !r2.IsCompressed() {
		t.Fatal("expected compressed resource for supported functionality list")
	}
}

// TestAsResourcePropagatedOmitsAutoCompress covers Phase 14 task 3: the
// PROPAGATED resource path does not pass auto_compress, matching Python's
// LXMessage.__as_resource (LXMF/LXMessage.py:660, v1.1.0) which only sets it on
// the DIRECT branch. Even with AutoCompress true, a propagated resource stays
// uncompressed.
func TestAsResourcePropagatedOmitsAutoCompress(t *testing.T) {
	t.Parallel()

	destinationID := mustTestNewIdentity(t, true)
	sourceID := mustTestNewIdentity(t, true)
	ts := rns.NewTransportSystem(nil)
	destination := mustTestNewDestination(t, ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	source := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	compressible := bytes.Repeat([]byte("CCCCCCCCCCCCCCCC"), 1024)

	link, err := rns.NewLink(ts, destination)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	activateLink(t, link)

	m := mustTestNewMessage(t, destination, source, "x", "", nil)
	m.Method = MethodPropagated
	m.PropagationPacked = compressible
	m.AutoCompress = true
	m.setDeliveryDestination(link)
	r, err := m.asResource()
	if err != nil {
		t.Fatalf("asResource (propagated): %v", err)
	}
	if r.IsCompressed() {
		t.Fatal("expected uncompressed propagated resource even when AutoCompress=true")
	}
}
