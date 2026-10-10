// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux

package interfaces

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// openUnopenedPTY allocates a pseudo-terminal and returns its master plus the
// path of a slave that nobody has opened.
//
// The slave is left unopened on purpose: a test that wants to know what the
// transport sees has to open the slave the way the transport opens it, because
// how a descriptor is opened decides how a read on it behaves. os.NewFile over
// a syscall.Open descriptor — the obvious way to hand back a pair — is a
// blocking descriptor with no runtime poller, and the transport's
// openSerialPort takes a different path entirely.
func openUnopenedPTY(t *testing.T) (master *os.File, slavePath string) {
	t.Helper()

	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("opening /dev/ptmx: %v", err)
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	if master == nil {
		_ = syscall.Close(fd)
		t.Fatal("could not make a file for the pty master")
	}
	t.Cleanup(func() { _ = master.Close() })

	unlock := int32(0)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCSPTLCK), uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		t.Fatalf("unlocking the pty: %v", errno)
	}

	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd),
		uintptr(syscall.TIOCGPTN), uintptr(unsafe.Pointer(&number))); errno != 0 {
		t.Fatalf("reading the pty's number: %v", errno)
	}

	return master, fmt.Sprintf("/dev/pts/%v", number)
}
