// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"fmt"
	"net"
	"testing"
)

func allocateUDPPort(t *testing.T) int {
	t.Helper()

	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate UDP port: %v", err)
	}
	defer pc.Close()

	addr, ok := pc.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected local address type: %T", pc.LocalAddr())
	}
	if addr.Port <= 0 {
		t.Fatalf("invalid allocated UDP port: %v", addr.Port)
	}

	return addr.Port
}

func allocateUDPPortPair(t *testing.T) (int, int) {
	t.Helper()

	first := allocateUDPPort(t)
	for i := 0; i < 10; i++ {
		second := allocateUDPPort(t)
		if second != first {
			return first, second
		}
	}

	t.Fatalf("failed to allocate distinct UDP port pair, first=%v", first)
	return 0, 0
}

func mustUDPConfig(instanceName string, listenPort, forwardPort int, enableTransport bool) string {
	transport := "False"
	if enableTransport {
		transport = "True"
	}
	return fmt.Sprintf(`[reticulum]
instance_name = %v
enable_transport = %v
share_instance = No

[interfaces]
  [[UDP Interface]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v
`, instanceName, transport, listenPort, forwardPort)
}
