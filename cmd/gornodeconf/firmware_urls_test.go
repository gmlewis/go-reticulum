// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestFirmwareReleaseInfoURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		fwURL           string
		selectedVersion string
		want            string
	}{
		{
			name: "default latest",
			want: firmwareVersionURL,
		},
		{
			name:  "custom latest",
			fwURL: "https://example.invalid/",
			want:  "https://example.invalid/latest/download/release.json",
		},
		{
			name:            "custom version",
			fwURL:           "https://example.invalid/",
			selectedVersion: "1.2.3",
			want:            "https://example.invalid/download/1.2.3/release.json",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := firmwareReleaseInfoURL(test.fwURL, test.selectedVersion); got != test.want {
				t.Fatalf("firmwareReleaseInfoURL(%q, %q) = %q, want %q", test.fwURL, test.selectedVersion, got, test.want)
			}
		})
	}
}

func TestFirmwareBinaryURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		fwURL           string
		selectedVersion string
		fwFilename      string
		want            string
	}{
		{
			name:            "default firmware",
			selectedVersion: "2.0.0",
			fwFilename:      "rnode_firmware.zip",
			want:            firmwareUpdateURL + "2.0.0/rnode_firmware.zip",
		},
		{
			name:            "custom firmware",
			fwURL:           "https://example.invalid/",
			selectedVersion: "2.0.0",
			fwFilename:      "rnode_firmware.zip",
			want:            "https://example.invalid/download/2.0.0/rnode_firmware.zip",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := firmwareBinaryURL(test.fwURL, test.selectedVersion, test.fwFilename); got != test.want {
				t.Fatalf("firmwareBinaryURL(%q, %q, %q) = %q, want %q", test.fwURL, test.selectedVersion, test.fwFilename, got, test.want)
			}
		})
	}
}
