// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func allocateUDPPortPair(t *testing.T) (int, int) {
	t.Helper()
	p1 := testutils.ReserveUDPPort(t)
	p2 := testutils.ReserveUDPPort(t)
	return p1, p2
}
