// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

type announceDropFake struct {
	dropped int
}

func (f *announceDropFake) DropAnnounceQueues() int {
	f.dropped++
	return f.dropped
}

func TestDoDropAnnouncesPrintsExpectedMessage(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	fake := &announceDropFake{}
	if err := doDropAnnounces(&out, fake); err != nil {
		t.Fatalf("doDropAnnounces returned error: %v", err)
	}
	if got, want := out.String(), "Dropping announce queues on all interfaces...\n"; got != want {
		t.Fatalf("drop-announces output mismatch: got %q want %q", got, want)
	}
	if fake.dropped != 1 {
		t.Fatalf("expected one drop call, got %v", fake.dropped)
	}
}
