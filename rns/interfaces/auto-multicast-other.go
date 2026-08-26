// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build windows

package interfaces

import "syscall"

// setIPv6MulticastIf sets the outgoing interface for IPv6 multicast packets on
// Windows, where syscall.SetsockoptInt takes the socket handle as a
// syscall.Handle (uintptr) rather than an int. Mirrors the IPV6_MULTICAST_IF
// step in Python's AutoInterface.peer_announce.
func setIPv6MulticastIf(fd uintptr, ifaceIndex int) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IPV6, ipv6MulticastIf, ifaceIndex)
}
