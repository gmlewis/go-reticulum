// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rsg

import (
	"testing"
)

// TestCheckReleaseRSMStructureGolden covers each branch of Python's
// check_release_rsm_structure (rnid.py) with a valid manifest and each
// invalid variant, asserting the exact Python error string.
func TestCheckReleaseRSMStructureGolden(t *testing.T) {
	validOrigin := make([]byte, 16)
	for i := range validOrigin {
		validOrigin[i] = byte(i)
	}

	tests := []struct {
		name    string
		signed  map[any]any
		wantErr error
	}{
		{
			name: "valid manifest",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"origin":  validOrigin,
					"path":    "group/repo",
				},
			},
			wantErr: nil,
		},
		{
			name:    "no meta key",
			signed:  map[any]any{},
			wantErr: ErrRSMNoMeta,
		},
		{
			name: "meta nil",
			signed: map[any]any{
				"meta": nil,
			},
			wantErr: ErrRSMNoMeta,
		},
		{
			name: "meta not a map",
			signed: map[any]any{
				"meta": "not-a-map",
			},
			wantErr: ErrRSMNoMeta,
		},
		{
			name: "incomplete package missing name",
			signed: map[any]any{
				"meta": map[any]any{
					"version": "1.0.0",
					"origin":  validOrigin,
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMIncompletePkg,
		},
		{
			name: "incomplete package empty version",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "",
					"origin":  validOrigin,
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMIncompletePkg,
		},
		{
			name: "incomplete origin missing origin",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMIncompleteOrg,
		},
		{
			name: "incomplete origin empty path",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"origin":  validOrigin,
					"path":    "",
				},
			},
			wantErr: ErrRSMIncompleteOrg,
		},
		{
			name: "invalid data slash in name",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg/sub",
					"version": "1.0.0",
					"origin":  validOrigin,
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMInvalidData,
		},
		{
			name: "invalid data slash in version",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0/0",
					"origin":  validOrigin,
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMInvalidData,
		},
		{
			name: "invalid origin length too short",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"origin":  []byte{1, 2, 3},
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMInvalidLen,
		},
		{
			name: "invalid origin length too long",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"origin":  make([]byte, 32),
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMInvalidLen,
		},
		{
			name: "invalid origin type string correct length",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"origin":  "0123456789abcdef",
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMInvalidOrigin,
		},
		{
			name: "invalid origin type int",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0.0",
					"origin":  42,
					"path":    "g/r",
				},
			},
			wantErr: ErrRSMInvalidOrigin,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckReleaseRSMStructure(tc.signed)
			if err != tc.wantErr {
				t.Fatalf("CheckReleaseRSMStructure = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestCheckReleaseRSMStructureErrorStrings asserts the exported error
// vars carry the exact Python user-facing strings.
func TestCheckReleaseRSMStructureErrorStrings(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrRSMNoMeta, "No release metadata in manifest"},
		{ErrRSMIncompletePkg, "Incomplete package data in manifest"},
		{ErrRSMIncompleteOrg, "Incomplete release origin data in manifest"},
		{ErrRSMInvalidData, "Invalid data in release manifest"},
		{ErrRSMInvalidLen, "Invalid origin hash length in manifest"},
		{ErrRSMInvalidOrigin, "Invalid origin hash in manifest"},
	}
	for _, c := range cases {
		if c.err.Error() != c.want {
			t.Errorf("err = %q, want %q", c.err.Error(), c.want)
		}
	}
}
