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

type blackholeFake struct {
	entries      map[string]map[string]any
	blackholed   []byte
	unblackholed []byte
}

func (f *blackholeFake) BlackholedIdentities() ([]any, error) {
	rows := make([]any, 0, len(f.entries))
	for _, entry := range f.entries {
		rows = append(rows, entry)
	}
	return rows, nil
}

func (f *blackholeFake) BlackholeIdentity(identityHash []byte, until *int64, reason string) (bool, error) {
	f.blackholed = append([]byte(nil), identityHash...)
	f.entries[string(identityHash)] = map[string]any{
		"identity_hash": append([]byte(nil), identityHash...),
		"source":        []byte{0x09, 0x09, 0x09},
		"reason":        reason,
		"until":         int64(0),
	}
	if until != nil {
		f.entries[string(identityHash)]["until"] = *until
	}
	return true, nil
}

func (f *blackholeFake) UnblackholeIdentity(identityHash []byte) (bool, error) {
	f.unblackholed = append([]byte(nil), identityHash...)
	delete(f.entries, string(identityHash))
	return true, nil
}

func TestDoBlackholePrintsCreatedMessage(t *testing.T) {
	t.Parallel()

	fake := &blackholeFake{entries: map[string]map[string]any{}}
	var out bytes.Buffer
	if err := doBlackhole(&out, fake, []byte{0x01, 0x02}, 0, "test-reason"); err != nil {
		t.Fatalf("doBlackhole returned error: %v", err)
	}
	if got, want := out.String(), "Blackholed identity <0102>\n"; got != want {
		t.Fatalf("blackhole output mismatch: got %q want %q", got, want)
	}
	if got, want := fake.blackholed, []byte{0x01, 0x02}; string(got) != string(want) {
		t.Fatalf("blackholed hash mismatch: got %x want %x", got, want)
	}
}

func TestDoBlackholePrintsAlreadyBlackholed(t *testing.T) {
	t.Parallel()

	fake := &blackholeFake{entries: map[string]map[string]any{
		string([]byte{0x01, 0x02}): {
			"identity_hash": []byte{0x01, 0x02},
			"source":        []byte{0x09, 0x09, 0x09},
			"reason":        "test-reason",
			"until":         int64(0),
		},
	}}
	var out bytes.Buffer
	if err := doBlackhole(&out, fake, []byte{0x01, 0x02}, 0, "test-reason"); err != nil {
		t.Fatalf("doBlackhole returned error: %v", err)
	}
	if got, want := out.String(), "Identity <0102> already blackholed\n"; got != want {
		t.Fatalf("already-blackholed output mismatch: got %q want %q", got, want)
	}
}

func TestDoUnblackholePrintsLiftedMessage(t *testing.T) {
	t.Parallel()

	fake := &blackholeFake{entries: map[string]map[string]any{
		string([]byte{0x01, 0x02}): {
			"identity_hash": []byte{0x01, 0x02},
			"source":        []byte{0x09, 0x09, 0x09},
			"reason":        "test-reason",
			"until":         int64(time.Now().Add(time.Hour).Unix()),
		},
	}}
	var out bytes.Buffer
	if err := doUnblackhole(&out, fake, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("doUnblackhole returned error: %v", err)
	}
	if got, want := out.String(), "Lifted blackhole for identity <0102>\n"; got != want {
		t.Fatalf("unblackhole output mismatch: got %q want %q", got, want)
	}
	if got, want := fake.unblackholed, []byte{0x01, 0x02}; string(got) != string(want) {
		t.Fatalf("unblackholed hash mismatch: got %x want %x", got, want)
	}
}

func TestDoUnblackholePrintsNotBlackholed(t *testing.T) {
	t.Parallel()

	fake := &blackholeFake{entries: map[string]map[string]any{}}
	var out bytes.Buffer
	if err := doUnblackhole(&out, fake, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("doUnblackhole returned error: %v", err)
	}
	if got, want := out.String(), "Identity <0102> not blackholed\n"; got != want {
		t.Fatalf("not-blackholed output mismatch: got %q want %q", got, want)
	}
}
