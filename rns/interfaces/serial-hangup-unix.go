// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"os"

	"golang.org/x/sys/unix"
)

// portHungUp reports whether the far end of a serial port has gone away.
//
// A read cannot answer this. The kernel reports a hangup as EIO exactly once, and
// only to a reader that is already parked in the read when the close happens; a
// read started afterwards gets the same zero-byte read as a line that is simply
// idle, and goes on getting it forever. The transport cannot therefore tell a radio
// that has been unplugged from one that has nothing to say — and the two need
// opposite answers, because one is a healthy radio and the other is a descriptor
// that will never carry another byte.
//
// poll(2) can tell them apart, on every platform this package builds for: a
// pseudo-terminal whose master has closed reports POLLHUP, which is set in revents
// whether or not it was asked for. So the read loop asks the port, and this is that
// question. A conn that is not a serial port — the tcp:// RNode path — has no such
// state to report, and answers false.
func portHungUp(conn rnodeConn) bool {
	file, ok := conn.(*os.File)
	if !ok {
		return false
	}
	fds := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	count, err := unix.Poll(fds, 0)
	if err != nil {
		// A port that cannot be asked is not a port this can be said to have lost:
		// the read that follows decides, exactly as it did before.
		return false
	}
	return count > 0 && fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0
}
