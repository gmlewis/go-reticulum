// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"strings"
	"testing"
)

func TestNewRNodeInterfaceValidation(t *testing.T) {
	t.Parallel()

	base := func() (string, int, int, int, int, int) {
		return "/dev/ttyUSB0", 433050000, 125000, 10, 7, 5
	}

	t.Run("missing port", func(t *testing.T) {
		_, frequency, bandwidth, txpower, sf, cr := base()
		iface, err := NewRNodeInterface("rnode0", "", 115200, 8, 1, "N", frequency, bandwidth, txpower, sf, cr, false, 0, "", nil)
		if err == nil || !strings.Contains(err.Error(), "no port specified") {
			t.Fatalf("expected missing port error, got %v", err)
		}
		if iface != nil {
			t.Fatalf("expected nil iface on validation failure")
		}
	})

	t.Run("invalid frequency", func(t *testing.T) {
		port, _, bandwidth, txpower, sf, cr := base()
		iface, err := NewRNodeInterface("rnode0", port, 115200, 8, 1, "N", 1, bandwidth, txpower, sf, cr, false, 0, "", nil)
		if err == nil || !strings.Contains(err.Error(), "invalid frequency") {
			t.Fatalf("expected invalid frequency error, got %v", err)
		}
		if iface != nil {
			t.Fatalf("expected nil iface on validation failure")
		}
	})

	t.Run("invalid spreading factor", func(t *testing.T) {
		port, frequency, bandwidth, txpower, _, cr := base()
		iface, err := NewRNodeInterface("rnode0", port, 115200, 8, 1, "N", frequency, bandwidth, txpower, 2, cr, false, 0, "", nil)
		if err == nil || !strings.Contains(err.Error(), "invalid spreading factor") {
			t.Fatalf("expected invalid spreading factor error, got %v", err)
		}
		if iface != nil {
			t.Fatalf("expected nil iface on validation failure")
		}
	})

	t.Run("id fields must be paired", func(t *testing.T) {
		port, frequency, bandwidth, txpower, sf, cr := base()
		iface, err := NewRNodeInterface("rnode0", port, 115200, 8, 1, "N", frequency, bandwidth, txpower, sf, cr, false, 10, "", nil)
		if err == nil || !strings.Contains(err.Error(), "id_interval and id_callsign") {
			t.Fatalf("expected id pairing error, got %v", err)
		}
		if iface != nil {
			t.Fatalf("expected nil iface on validation failure")
		}
	})
}
