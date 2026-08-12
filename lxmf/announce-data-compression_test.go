// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"testing"
)

// mustHexDecode decodes s or fails the test.
func mustHexDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", s, err)
	}
	return b
}

// TestSFCompressionConstant covers Phase 14 task 1: the SFCompression
// supported-functionality code matches Python LXMF.SF_COMPRESSION = 0x00
// (lxmf/LXMF.py:108, v0.9.5).
func TestSFCompressionConstant(t *testing.T) {
	t.Parallel()
	if SFCompression != 0x00 {
		t.Fatalf("SFCompression = %#x, want 0x00", SFCompression)
	}
}

// TestCompressionSupportFromAppData covers Phase 14 task 1: the helper unpacks
// the announce peer_data[2] supported-functionality list and reports whether
// SF_COMPRESSION is signalled, matching Python
// LXMF.compression_support_from_app_data (lxmf/LXMF.py:154-166, v0.9.5) on
// golden msgpack payloads captured from CPython+umsgpack. The (supported,
// present) return distinguishes Python's None (no app data) from True/False.
func TestCompressionSupportFromAppData(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		hex       string
		supported bool
		present   bool
	}{
		{"nil_app_data", "", false, false},
		{"original_string_format", "a5416c696365", true, true},
		{"two_element_no_functionality_list", "92a5416c69636564", true, true},
		{"three_with_sf_compression", "93a5416c696365649100", true, true},
		{"three_empty_functionality_list", "93a5416c6963656490", false, true},
		{"three_other_functionality_only", "93a5416c696365649101", false, true},
		{"three_non_list_third_element", "93a5416c6963656400", true, true},
		{"three_two_functionalities_with_sf", "93a5416c69636564920001", true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var appData []byte
			if tc.hex != "" {
				appData = mustHexDecode(t, tc.hex)
			}
			supported, present, err := CompressionSupportFromAppData(appData)
			if err != nil {
				t.Fatalf("CompressionSupportFromAppData(%q): unexpected error %v", tc.hex, err)
			}
			if present != tc.present {
				t.Errorf("present = %v, want %v", present, tc.present)
			}
			if supported != tc.supported {
				t.Errorf("supported = %v, want %v", supported, tc.supported)
			}
		})
	}
}

// TestCompressionSupportFromAppDataMalformed covers Phase 14 task 1: a
// v0.5.0+-prefixed payload that fails to unpack returns a non-nil error
// (mirroring the msgpack.unpackb exception path in
// compression_support_from_app_data).
func TestCompressionSupportFromAppDataMalformed(t *testing.T) {
	t.Parallel()
	// fixarray header claiming 3 elements but truncated.
	malformed := mustHexDecode(t, "93a5416c")
	_, _, err := CompressionSupportFromAppData(malformed)
	if err == nil {
		t.Fatal("expected error for malformed msgpack payload, got nil")
	}
}
