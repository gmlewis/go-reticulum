// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestRenderPathTableTextSortsAndFormatsEntries(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{
		{Hash: []byte{0x02}, NextHop: []byte{0x22}, Hops: 2, Interface: pathTableTestInterface{name: "eth1"}, Expires: time.Date(2026, 4, 5, 14, 30, 45, 0, time.UTC)},
		{Hash: []byte{0x01}, NextHop: []byte{0x11}, Hops: 1, Interface: pathTableTestInterface{name: "eth0"}, Expires: time.Date(2026, 4, 5, 14, 30, 46, 0, time.UTC)},
		{Hash: []byte{0x03}, NextHop: []byte{0x33}, Hops: 3, Interface: pathTableTestInterface{name: "eth0"}, Expires: time.Date(2026, 4, 5, 14, 30, 47, 0, time.UTC)},
	}

	got, err := renderPathTable(paths, 0, false, nil)
	if err != nil {
		t.Fatalf("renderPathTable returned error: %v", err)
	}
	want := strings.Join([]string{
		"01 is 1 hop  away via 11 on eth0 expires 2026-04-05 14:30:46",
		"03 is 3 hops away via 33 on eth0 expires 2026-04-05 14:30:47",
		"02 is 2 hops away via 22 on eth1 expires 2026-04-05 14:30:45",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("path table text mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderPathTableTextFiltersByHopAndDestination(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{
		{Hash: []byte{0x01}, NextHop: []byte{0x11}, Hops: 1, Interface: pathTableTestInterface{name: "eth0"}, Expires: time.Date(2026, 4, 5, 14, 30, 46, 0, time.UTC)},
		{Hash: []byte{0x02}, NextHop: []byte{0x22}, Hops: 2, Interface: pathTableTestInterface{name: "eth1"}, Expires: time.Date(2026, 4, 5, 14, 30, 45, 0, time.UTC)},
	}

	got, err := renderPathTable(paths, 1, false, []byte{0x01})
	if err != nil {
		t.Fatalf("renderPathTable returned error: %v", err)
	}
	if !strings.Contains(got, "01 is 1 hop  away via 11 on eth0 expires 2026-04-05 14:30:46") {
		t.Fatalf("missing filtered path entry: %q", got)
	}
	if strings.Contains(got, "02 is") {
		t.Fatalf("unexpected extra path entry: %q", got)
	}
}

func TestRenderPathTableJSONUsesPythonFieldNames(t *testing.T) {
	t.Parallel()

	path := rns.PathInfo{
		Timestamp: time.Unix(123, 0),
		Hash:      []byte{0xaa, 0xbb},
		NextHop:   []byte{0xcc, 0xdd},
		Hops:      2,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Unix(456, 0),
	}

	got, err := renderPathTable([]rns.PathInfo{path}, 0, true, nil)
	if err != nil {
		t.Fatalf("renderPathTable returned error: %v", err)
	}
	want := `[{"hash":"aabb","timestamp":123,"via":"ccdd","hops":2,"expires":456,"interface":"eth0"}]`
	if got != want {
		t.Fatalf("path table JSON mismatch:\n got: %q\nwant: %q", got, want)
	}
}
