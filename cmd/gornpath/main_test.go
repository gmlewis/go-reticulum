// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

const tempDirPrefix = "gornpath-test-"

func TestDoTableUsesRenderer(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{{
		Hash:      []byte{0x01},
		NextHop:   []byte{0x11},
		Hops:      1,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Date(2026, 4, 5, 14, 30, 46, 0, time.UTC),
	}}

	var out bytes.Buffer
	if err := doTable(&out, pathTableProviderFunc(func() []rns.PathInfo { return paths }), 0, false); err != nil {
		t.Fatalf("doTable returned error: %v", err)
	}
	if got, want := out.String(), "01 is 1 hop  away via 11 on eth0 expires 2026-04-05 14:30:46\n"; got != want {
		t.Fatalf("table output mismatch: got %q want %q", got, want)
	}
}

type pathTableProviderFunc func() []rns.PathInfo

func (f pathTableProviderFunc) GetPathTable() []rns.PathInfo {
	return f()
}

var _ pathTableProvider = pathTableProviderFunc(nil)

func TestDoTableJSONUsesRenderer(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{{
		Timestamp: time.Unix(123, 0),
		Hash:      []byte{0xaa, 0xbb},
		NextHop:   []byte{0xcc, 0xdd},
		Hops:      2,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Unix(456, 0),
	}}

	var out bytes.Buffer
	if err := doTable(&out, pathTableProviderFunc(func() []rns.PathInfo { return paths }), 0, true); err != nil {
		t.Fatalf("doTable returned error: %v", err)
	}
	if got, want := out.String(), "[{\"hash\":\"aabb\",\"timestamp\":123,\"via\":\"ccdd\",\"hops\":2,\"expires\":456,\"interface\":\"eth0\"}]"; got != want {
		t.Fatalf("table JSON mismatch: got %q want %q", got, want)
	}
}
