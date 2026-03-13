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

func TestNewWeaveInterfaceMissingPort(t *testing.T) {
	t.Parallel()

	iface, err := NewWeaveInterface("weave0", "", 0, nil)
	if err == nil || !strings.Contains(err.Error(), "no port specified") {
		t.Fatalf("expected missing-port error, got %v", err)
	}
	if iface != nil {
		t.Fatalf("expected nil interface on validation failure")
	}
}
