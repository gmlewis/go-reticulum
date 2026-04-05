// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"
)

type blackholedProviderFunc func() ([]any, error)

func (f blackholedProviderFunc) BlackholedIdentities() ([]any, error) {
	return f()
}

func TestRenderBlackholedIdentitiesFormatsLocalEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	rows := []any{
		map[string]any{
			"identity_hash": []byte{0x02},
			"source":        []byte{0x09, 0x09, 0x09},
			"until":         int64(now.Add(90 * time.Minute).Unix()),
			"reason":        "Excessive announces",
		},
		map[string]any{
			"identity_hash": []byte{0x01},
			"source":        []byte{0x01, 0x02, 0x03},
			"until":         int64(0),
			"reason":        "Announce spam",
		},
	}

	got, err := renderBlackholedIdentities(rows, now, "", []byte{0x09, 0x09, 0x09})
	if err != nil {
		t.Fatalf("renderBlackholedIdentities returned error: %v", err)
	}
	want := "<01> blackholed indefinitely (Announce spam) by <010203>\n<02> blackholed for 1h and 30m (Excessive announces)\n"
	if got != want {
		t.Fatalf("blackhole list mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestDoBlackholedReportsNoData(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := doBlackholed(&out, blackholedProviderFunc(func() ([]any, error) {
		return nil, nil
	}), "", nil)
	if err != errNoBlackholedInformation {
		t.Fatalf("doBlackholed error = %v, want %v", err, errNoBlackholedInformation)
	}
	if got, want := out.String(), "No blackholed identity data available\n"; got != want {
		t.Fatalf("unexpected no-data output: got %q want %q", got, want)
	}
}
