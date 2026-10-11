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

// setWaitingRead switches the port's read from the idle-timed form (VMIN=0/VTIME=1,
// which hands back a zero-byte read after a tenth of a second) to one that waits
// for a byte (VMIN=1/VTIME=0). A read with no idle timeout cannot return on its
// own, so once a reader is parked in one it stays parked until the far end closes
// — and the kernel reports the hang-up as EIO only to a read that is already
// parked. That is what makes the EIO assertion deterministic instead of a race
// against the idle timer.
func setWaitingRead(fd uintptr) error {
	termios := &syscall.Termios{}
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
		return errno
	}
	termios.Cc[syscall.VMIN] = 1
	termios.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd,
		uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}
