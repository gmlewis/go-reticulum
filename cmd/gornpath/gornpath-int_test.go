// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestIntegrationRenderPathTable(t *testing.T) {
	t.Parallel()

	paths := []rns.PathInfo{{
		Hash:      []byte{0x01},
		Timestamp: time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC),
		NextHop:   []byte{0x11},
		Hops:      1,
		Interface: pathTableTestInterface{name: "eth0"},
		Expires:   time.Date(2026, 4, 5, 15, 7, 36, 0, time.UTC),
	}, {
		Hash:      []byte{0x02},
		Timestamp: time.Date(2026, 4, 5, 15, 1, 0, 0, time.UTC),
		NextHop:   []byte{0x22},
		Hops:      2,
		Interface: pathTableTestInterface{name: "eth1"},
		Expires:   time.Date(2026, 4, 5, 15, 8, 36, 0, time.UTC),
	}}

	got, err := renderPathTable(paths, 0, false, nil)
	if err != nil {
		t.Fatalf("renderPathTable returned error: %v", err)
	}
	want := "01 is 1 hop  away via 11 on eth0 expires 2026-04-05 15:07:36\n02 is 2 hops away via 22 on eth1 expires 2026-04-05 15:08:36\n"
	if got != want {
		t.Fatalf("renderPathTable mismatch:\nwant:\n%sgot:\n%s", want, got)
	}
}

func TestIntegrationRenderRateTable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	rows := []any{
		map[string]any{
			"hash":            []byte{0x01},
			"last":            float64(now.Add(-2 * time.Minute).Unix()),
			"rate_violations": 0,
			"blocked_until":   float64(0),
			"timestamps":      []any{float64(now.Add(-3 * time.Hour).Unix())},
		},
	}

	got, err := renderRateTable(rows, now, nil, false)
	if err != nil {
		t.Fatalf("renderRateTable returned error: %v", err)
	}
	want := "01 last heard 2 minutes ago, 0.333 announces/hour in the last 3 hours\n"
	if got != want {
		t.Fatalf("renderRateTable mismatch:\nwant:\n%sgot:\n%s", want, got)
	}
}

func TestIntegrationRenderBlackholedIdentities(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 5, 15, 0, 0, 0, time.UTC)
	rows := []any{
		map[string]any{
			"identity_hash": []byte{0x01},
			"until":         int64(0),
			"reason":        "Announce spam",
			"source":        []byte{0x09, 0x09, 0x09},
		},
	}

	got, err := renderBlackholedIdentities(rows, now, "", nil)
	if err != nil {
		t.Fatalf("renderBlackholedIdentities returned error: %v", err)
	}
	want := "<01> blackholed indefinitely (Announce spam) by <090909>\n"
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Fatalf("renderBlackholedIdentities mismatch:\nwant:\n%sgot:\n%s", want, got)
	}
}

type integrationPathRequestFake struct {
	requested bool
	path      *rns.PathInfo
}

func (f *integrationPathRequestFake) HasPath([]byte) bool {
	return f.requested
}

func (f *integrationPathRequestFake) RequestPath([]byte) error {
	f.requested = true
	f.path = &rns.PathInfo{
		Hash:      []byte{0xaa, 0xbb},
		NextHop:   []byte{0xcc, 0xdd},
		Hops:      2,
		Interface: pathRequestInterface{},
		Expires:   time.Date(2026, 4, 5, 15, 7, 36, 0, time.UTC),
	}
	return nil
}

func (f *integrationPathRequestFake) GetPathEntry([]byte) *rns.PathInfo {
	return f.path
}

func TestIntegrationDoRequest(t *testing.T) {
	t.Parallel()

	fake := &integrationPathRequestFake{}
	var out bytes.Buffer
	if err := doRequestAt(&out, fake, []byte{0xaa, 0xbb}, 1.0, func() time.Time { return time.Unix(0, 0) }, func(time.Duration) {}); err != nil {
		t.Fatalf("doRequestAt returned error: %v", err)
	}
	want := "Path to aabb requested  \rPath found, destination aabb is 2 hops away via ccdd on eth0\n"
	if out.String() != want {
		t.Fatalf("doRequestAt mismatch:\nwant:\n%sgot:\n%s", want, out.String())
	}
}

func TestIntegrationBlackholeRoundTrip(t *testing.T) {
	t.Parallel()

	fake := &blackholeFake{entries: map[string]map[string]any{}}
	var out bytes.Buffer
	if err := doBlackhole(&out, fake, []byte{0x01, 0x02}, 0, "integration-test"); err != nil {
		t.Fatalf("doBlackhole returned error: %v", err)
	}
	if got, want := out.String(), "Blackholed identity <0102>\n"; got != want {
		t.Fatalf("doBlackhole output mismatch: got %q want %q", got, want)
	}

	out.Reset()
	if err := doUnblackhole(&out, fake, []byte{0x01, 0x02}); err != nil {
		t.Fatalf("doUnblackhole returned error: %v", err)
	}
	if got, want := out.String(), "Lifted blackhole for identity <0102>\n"; got != want {
		t.Fatalf("doUnblackhole output mismatch: got %q want %q", got, want)
	}
}

type integrationDropperFake struct {
	dropPathResult bool
	dropViaCount   int
}

func (f *integrationDropperFake) InvalidatePath([]byte) bool {
	return f.dropPathResult
}

func (f *integrationDropperFake) InvalidatePathsViaNextHop([]byte) int {
	return f.dropViaCount
}

func TestIntegrationDropOperations(t *testing.T) {
	t.Parallel()

	fake := &integrationDropperFake{dropPathResult: true, dropViaCount: 2}
	var out bytes.Buffer
	if err := doDrop(&out, fake, []byte{0xaa, 0xbb}); err != nil {
		t.Fatalf("doDrop returned error: %v", err)
	}
	if got, want := out.String(), "Dropped path to aabb\n"; got != want {
		t.Fatalf("doDrop output mismatch: got %q want %q", got, want)
	}

	out.Reset()
	if err := doDropVia(&out, fake, []byte{0xcc, 0xdd}); err != nil {
		t.Fatalf("doDropVia returned error: %v", err)
	}
	if got, want := out.String(), "Dropped all paths via ccdd\n"; got != want {
		t.Fatalf("doDropVia output mismatch: got %q want %q", got, want)
	}
}
