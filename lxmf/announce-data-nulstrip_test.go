// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
)

// TestDisplayNameFromAppDataStripsNULAndWhitespace covers Phase 14 task 5:
// the v0.5.0+ list-format display name is NUL-stripped and whitespace-trimmed
// after UTF-8 decoding, mirroring Python's
// display_name_from_app_data (lxmf/LXMF.py:165, v0.9.7+). The original
// raw-string format is returned verbatim — Python's `else` branch does not
// strip or trim. Golden hex for the v0.5.0+ cases is captured from
// CPython+umsgpack.
func TestDisplayNameFromAppDataStripsNULAndWhitespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		appData []byte
		want    string
	}{
		// v0.5.0+ format: [b"A\x00lice"] → "A" + NUL + "lice" → "Alice".
		{"v050_nul_in_name", mustHexDecode(t, "91c40641006c696365"), "Alice"},
		// v0.5.0+ format: [b"  Alice  "] → trimmed → "Alice".
		{"v050_surrounding_whitespace", mustHexDecode(t, "91c4092020416c6963652020"), "Alice"},
		// v0.5.0+ format: [b"\x00 Alice \x00"] → NUL-stripped + trimmed → "Alice".
		{"v050_nul_and_whitespace", mustHexDecode(t, "91c4090020416c6963652000"), "Alice"},
		// v0.5.0+ format: [b"\x00\x00"] → all stripped → "".
		{"v050_all_nul", mustHexDecode(t, "91c4020000"), ""},
		// Original format: raw b"A\x00lice" → returned verbatim (no stripping).
		{"original_nul_not_stripped", []byte("A\x00lice"), "A\x00lice"},
		// Original format: raw b"  Alice  " → returned verbatim (no trimming).
		{"original_whitespace_not_trimmed", []byte("  Alice  "), "  Alice  "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := DisplayNameFromAppData(tc.appData)
			if err != nil {
				t.Fatalf("DisplayNameFromAppData: unexpected error %v", err)
			}
			if got != tc.want {
				t.Fatalf("DisplayNameFromAppData = %q, want %q", got, tc.want)
			}
		})
	}
}
