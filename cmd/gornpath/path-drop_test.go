// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

type pathDropFake struct {
	pathResult bool
	viaCount   int
}

func (f *pathDropFake) InvalidatePath([]byte) bool           { return f.pathResult }
func (f *pathDropFake) InvalidatePathsViaNextHop([]byte) int { return f.viaCount }

func TestDoDropPrintsExpectedMessages(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := doDrop(&out, &pathDropFake{pathResult: true}, []byte{0xaa, 0xbb}); err != nil {
		t.Fatalf("doDrop returned error: %v", err)
	}
	if got, want := out.String(), "Dropped path to aabb\n"; got != want {
		t.Fatalf("drop output mismatch: got %q want %q", got, want)
	}
}

func TestDoDropReturnsErrorWhenPathMissing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := doDrop(&out, &pathDropFake{}, []byte{0xaa, 0xbb})
	if err == nil || err.Error() != "Unable to drop path to aabb. Does it exist?" {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := out.String(), "Unable to drop path to aabb. Does it exist?\n"; got != want {
		t.Fatalf("drop failure output mismatch: got %q want %q", got, want)
	}
}

func TestDoDropViaPrintsExpectedMessages(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := doDropVia(&out, &pathDropFake{viaCount: 2}, []byte{0xcc, 0xdd}); err != nil {
		t.Fatalf("doDropVia returned error: %v", err)
	}
	if got, want := out.String(), "Dropped all paths via ccdd\n"; got != want {
		t.Fatalf("drop-via output mismatch: got %q want %q", got, want)
	}
}

func TestDoDropViaReturnsErrorWhenTransportMissing(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	err := doDropVia(&out, &pathDropFake{}, []byte{0xcc, 0xdd})
	if err == nil || err.Error() != "Unable to drop paths via ccdd. Does the transport instance exist?" {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := out.String(), "Unable to drop paths via ccdd. Does the transport instance exist?\n"; got != want {
		t.Fatalf("drop-via failure output mismatch: got %q want %q", got, want)
	}
}
