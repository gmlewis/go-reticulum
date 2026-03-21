// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "testing"

func mustTest(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func mustTestNewBackboneInterface(t *testing.T, name, bindIP string, bindPort int, handler InboundHandler) Interface {
	t.Helper()
	iface, err := NewBackboneInterface(name, bindIP, bindPort, handler, nil)
	mustTest(t, err)
	return iface
}

func mustTestNewBackboneClientInterface(t *testing.T, name, targetHost string, targetPort int, handler InboundHandler) Interface {
	t.Helper()
	iface, err := NewBackboneClientInterface(name, targetHost, targetPort, handler)
	mustTest(t, err)
	return iface
}

func mustTestNewI2PInterface(t *testing.T, name, bindIP string, bindPort int, handler InboundHandler) Interface {
	t.Helper()
	iface, err := NewI2PInterface(name, bindIP, bindPort, handler, nil)
	mustTest(t, err)
	return iface
}

func mustTestNewI2PInterfacePeer(t *testing.T, name, targetHost string, targetPort int, handler InboundHandler) Interface {
	t.Helper()
	iface, err := NewI2PInterfacePeer(name, targetHost, targetPort, handler)
	mustTest(t, err)
	return iface
}

func mustTestNewLocalServerInterface(t *testing.T, name, path string, port int, handler InboundHandler) *LocalServerInterface {
	t.Helper()
	iface, err := NewLocalServerInterface(name, path, port, handler)
	mustTest(t, err)
	return iface
}

func mustTestNewTCPClientInterface(t *testing.T, name, host string, port int, kiss bool, handler InboundHandler) *TCPClientInterface {
	t.Helper()
	iface, err := NewTCPClientInterface(name, host, port, kiss, handler)
	mustTest(t, err)
	return iface
}

func mustTestNewTCPServerInterface(t *testing.T, name, bindIP string, bindPort int, handler InboundHandler) *TCPServerInterface {
	t.Helper()
	iface, err := NewTCPServerInterface(name, bindIP, bindPort, handler, nil)
	mustTest(t, err)
	return iface
}

func mustTestNewUDPInterface(t *testing.T, name, listenIP string, listenPort int, forwardIP string, forwardPort int, handler InboundHandler) *UDPInterface {
	t.Helper()
	iface, err := NewUDPInterface(name, listenIP, listenPort, forwardIP, forwardPort, handler)
	mustTest(t, err)
	return iface
}
