// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

func TestCheckReleaseRSMStructure(t *testing.T) {
	t.Parallel()
	validOrigin := make([]byte, 16)
	for i := range validOrigin {
		validOrigin[i] = byte(i)
	}
	shortOrigin := make([]byte, 15)
	cases := []struct {
		name    string
		signed  map[any]any
		wantErr string
	}{
		{
			name: "valid",
			signed: map[any]any{
				"meta": map[any]any{
					"name":    "pkg",
					"version": "1.0",
					"origin":  validOrigin,
					"path":    "/release",
				},
			},
			wantErr: "",
		},
		{
			name:    "no meta",
			signed:  map[any]any{},
			wantErr: "No release metadata in manifest",
		},
		{
			name: "missing name",
			signed: map[any]any{
				"meta": map[any]any{"version": "1.0", "origin": validOrigin, "path": "/p"},
			},
			wantErr: "Incomplete package data in manifest",
		},
		{
			name: "missing origin",
			signed: map[any]any{
				"meta": map[any]any{"name": "pkg", "version": "1.0", "path": "/p"},
			},
			wantErr: "Incomplete release origin data in manifest",
		},
		{
			name: "slash in name",
			signed: map[any]any{
				"meta": map[any]any{"name": "a/b", "version": "1.0", "origin": validOrigin, "path": "/p"},
			},
			wantErr: "Invalid data in release manifest",
		},
		{
			name: "slash in version",
			signed: map[any]any{
				"meta": map[any]any{"name": "pkg", "version": "1/0", "origin": validOrigin, "path": "/p"},
			},
			wantErr: "Invalid data in release manifest",
		},
		{
			name: "wrong length origin",
			signed: map[any]any{
				"meta": map[any]any{"name": "pkg", "version": "1.0", "origin": shortOrigin, "path": "/p"},
			},
			wantErr: "Invalid origin hash length in manifest",
		},
		{
			name: "string origin len 16",
			signed: map[any]any{
				"meta": map[any]any{"name": "pkg", "version": "1.0", "origin": "0123456789abcdef", "path": "/p"},
			},
			wantErr: "Invalid origin hash in manifest",
		},
		{
			name: "int origin",
			signed: map[any]any{
				"meta": map[any]any{"name": "pkg", "version": "1.0", "origin": 42, "path": "/p"},
			},
			wantErr: "Invalid origin hash in manifest",
		},
		{
			name: "empty name",
			signed: map[any]any{
				"meta": map[any]any{"name": "", "version": "1.0", "origin": validOrigin, "path": "/p"},
			},
			wantErr: "Incomplete package data in manifest",
		},
		{
			name: "empty path",
			signed: map[any]any{
				"meta": map[any]any{"name": "pkg", "version": "1.0", "origin": validOrigin, "path": ""},
			},
			wantErr: "Incomplete release origin data in manifest",
		},
	}

	for _, c := range cases {
		err := checkReleaseRSMStructure(c.signed)
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: expected nil error, got %q", c.name, err.Error())
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: expected error %q, got nil", c.name, c.wantErr)
			continue
		}
		if err.Error() != c.wantErr {
			t.Errorf("%s: error = %q, want %q", c.name, err.Error(), c.wantErr)
		}
	}
}
