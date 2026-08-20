// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSanitizeDiscoveryName verifies that sanitize_name mirrors
// RNS/Discovery.py:238-244 — drop non-ASCII, strip, collapse repeated runs of
// spaces (5→1, 3→1, 2→1), trim leading non-alphanumeric chars, trim trailing
// non-alphanumeric chars except ')'. An empty input yields an empty result
// (Python returns None).
func TestSanitizeDiscoveryName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  Hello   World  ", "Hello World"},
		{"!!!Hello!!!", "Hello"},
		{"Hello)", "Hello)"},
		{"(Hello", "Hello"},
		{"café", "caf"},
		{"  \n Hello   \n   World  \n", "Hello \n World"},
		{"AB     C", "AB C"},
		{"a  b   c    d", "a b c d"},
		{"  (Test)  ", "Test)"},
	}
	for _, c := range cases {
		if got := sanitizeDiscoveryName(c.in); got != c.want {
			t.Fatalf("sanitizeDiscoveryName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDiscoveryReceiveAppliesSanitizeName verifies that a received
// announce's name is sanitized via sanitize_name on the receive path
// (RNS/Discovery.py:303). A name with leading junk and repeated spaces is
// sanitized before being persisted.
func TestDiscoveryReceiveAppliesSanitizeName(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-sanitize-receive-")
	ts := NewTransportSystem(nil)
	r := &Reticulum{configDir: tmpDir, transport: ts, logger: NewLogger()}
	var info map[string]any
	h := NewInterfaceAnnounceHandler(r, 2, func(got map[string]any) {
		info = cloneStringAnyMap(got)
	})
	src := mustTestNewIdentity(t, true)
	appData := validValidationAnnounceAppData(t, map[any]any{
		discoveryFieldName: "!!!My   Interface!!!",
	})
	h.receivedAnnounce([]byte("dest"), src, appData)
	if info == nil {
		t.Fatal("expected callback to fire")
	}
	if got := info["name"]; got != "My Interface" {
		t.Fatalf("receive name = %#v, want %q (sanitized)", got, "My Interface")
	}
}

// TestDiscoveryLoadAppliesSanitizeName verifies that a persisted discovery
// file's name is sanitized via sanitize_name on the load path
// (RNS/Discovery.py:481). A hand-written .data file with an unsanitized name
// is sanitized when listed.
func TestDiscoveryLoadAppliesSanitizeName(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-sanitize-load-")
	storagePath := tmpDir + "/discovery/interfaces"
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	packed := mustMsgpackPack(map[string]any{
		"name":         "!!!Bad   Name!!!",
		"type":         "TCPServerInterface",
		"last_heard":   float64(time.Now().UnixNano())/1e9 - 60,
		"transport":    true,
		"value":        1,
		"transport_id": "deadbeef",
		"network_id":   "feedface",
	})
	if err := os.WriteFile(filepath.Join(storagePath, "bad.data"), packed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("len(discovered) = %v, want 1", len(discovered))
	}
	if got := discovered[0].Name; got != "Bad Name" {
		t.Fatalf("loaded name = %q, want %q (sanitized on load)", got, "Bad Name")
	}
}
