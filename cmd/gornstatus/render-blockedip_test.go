// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestRenderInterfaceBlockedIPs verifies that gornstatus renders the
// fast-flap "Blocked   : N IPs" line appended to the clients string when the
// interface stat reports blocked_ips > 0, mirroring Python rnstatus.py:457-459.
func TestRenderInterfaceBlockedIPs(t *testing.T) {
	t.Parallel()
	clients := 5
	blocked := 3
	ifstat := rns.InterfaceStat{
		Name:          "BackboneInterface[core-1]",
		Status:        true,
		Mode:          modeFull,
		Clients:       &clients,
		BlockedIPs:    &blocked,
		BlockedIPList: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()

	if !strings.Contains(got, "    Clients   : 5\n") {
		t.Errorf("expected Clients line, got:\n%v", got)
	}
	if !strings.Contains(got, "    Blocked   : 3 IPs\n") {
		t.Errorf("expected Blocked line, got:\n%v", got)
	}
}

// TestRenderInterfaceBlockedIPsZeroOmitted verifies that when
// blocked_ips is 0 (or nil), the Blocked line is omitted, matching Python's
// `p = ifstat["blocked_ips"] > 0` guard (rnstatus.py:458).
func TestRenderInterfaceBlockedIPsZeroOmitted(t *testing.T) {
	t.Parallel()
	clients := 5
	blocked := 0
	ifstat := rns.InterfaceStat{
		Name:       "BackboneInterface[core-2]",
		Status:     true,
		Mode:       modeFull,
		Clients:    &clients,
		BlockedIPs: &blocked,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()

	if !strings.Contains(got, "    Clients   : 5\n") {
		t.Errorf("expected Clients line, got:\n%v", got)
	}
	if strings.Contains(got, "Blocked") {
		t.Errorf("expected no Blocked line when blocked_ips == 0, got:\n%v", got)
	}
}

// TestRenderInterfaceBlockedIPsNilOmitted verifies that a nil
// BlockedIPs (non-blocker interface) renders no Blocked line.
func TestRenderInterfaceBlockedIPsNilOmitted(t *testing.T) {
	t.Parallel()
	clients := 5
	ifstat := rns.InterfaceStat{
		Name:    "BackboneInterface[core-3]",
		Status:  true,
		Mode:    modeFull,
		Clients: &clients,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()

	if strings.Contains(got, "Blocked") {
		t.Errorf("expected no Blocked line when BlockedIPs nil, got:\n%v", got)
	}
}
