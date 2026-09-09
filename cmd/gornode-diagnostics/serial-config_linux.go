// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// errBusy is the errno returned when a serial line is held by another process.
func errBusy() error { return syscall.EBUSY }

// ioctlTCFLSH is the Linux TCFLSH ioctl (not exported by syscall on all
// architectures); argument 0 is TCIFLUSH — flush the input queue.
const ioctlTCFLSH = 0x540B

// flushSerialInput discards any input buffered on the serial device (ioctl
// TCFLSH with TCIFLUSH) so data a previous reader left unread cannot be
// mistaken for fresh traffic.
func flushSerialInput(fd uintptr) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlTCFLSH, 0); errno != 0 {
		return errno
	}
	return nil
}

// deviceID returns the underlying character-device ID of a path, used to
// recognize when two paths (e.g. /dev/serial/by-id/… and /dev/ttyACM0) name
// the same physical serial device. Non-device paths report ok=false.
func deviceID(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&(os.ModeDevice|os.ModeCharDevice) != os.ModeDevice|os.ModeCharDevice {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Rdev, true
}

// errTimeout matches a timed-out raw read (no data available).
func errTimeout() error { return os.ErrDeadlineExceeded }

// nocttyFlag prevents the opened serial device from becoming this process's
// controlling terminal, and opens O_NONBLOCK so the open() itself never
// blocks on carrier detect (the caller drops O_NONBLOCK right after open).
func nocttyFlag() int { return syscall.O_NOCTTY | syscall.O_NONBLOCK }

// postOpenSerialPort drops O_NONBLOCK from an opened serial device so reads
// and writes behave normally (blocking, with the termios VTIME timeout).
func postOpenSerialPort(file *os.File) error {
	fd := file.Fd()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(syscall.F_GETFL), 0)
	if errno != 0 {
		return errno
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(syscall.F_SETFL), uintptr(int(flags)&^syscall.O_NONBLOCK)); errno != 0 {
		return errno
	}
	return nil
}

// serialBusyCheck reports whether the given serial device appears to be held
// by another process. Linux permits duplicate open()s of a TTY (the kernel
// does not arbitrate), so instead of relying on EBUSY this scans /proc/*/fd
// for file descriptors pointing at the device — a reliable way to see that
// another process (e.g. gornsd) is using the same serial line. This process
// itself is excluded so that a path already opened by an earlier sniff step
// (e.g. the same radio under its /dev/serial/by-id alias, whose fd readlink
// canonicalizes to the plain device path) does not mark the port busy.
func serialBusyCheck(port string) (bool, string) {
	if holders := portHolders(port); len(holders) > 0 {
		return true, fmt.Sprintf("held by other process(es): pid(s) %v", strings.Join(holders, ", "))
	}
	return false, ""
}

// portHolders returns the PIDs of processes (other than this one) holding an
// open descriptor on the given device path.
func portHolders(port string) []string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	self := strconv.Itoa(os.Getpid())
	var holders []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		if entry.Name() == self {
			continue
		}
		fdDir := "/proc/" + entry.Name() + "/fd"
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue // permission denied etc. — not a holder we can see
		}
		for _, fd := range fds {
			target, err := os.Readlink(fdDir + "/" + fd.Name())
			if err != nil {
				continue
			}
			if target == port {
				holders = append(holders, entry.Name())
				break
			}
		}
	}
	sortStrings(holders)
	return holders
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// cbaud is not always defined in syscall for all Linux platforms.
const cbaud = 0x100f

// configureSerialPort configures the raw TTY for binary KISS traffic at the
// given baud rate (mirrors RNS/Interfaces/serial_linux.go configureTermios):
// raw mode, 8 data bits, no parity, 1 stop bit, and a 100 ms inter-byte read
// timeout (VMIN=0/VTIME=1) so reads never block indefinitely.
func configureSerialPort(fd uintptr, speed int) error {
	termios := &syscall.Termios{}
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
		return errno
	}

	termios.Iflag = 0
	termios.Oflag = 0
	termios.Lflag = 0

	termios.Cflag &^= syscall.CSIZE
	termios.Cflag |= syscall.CS8
	termios.Cflag |= syscall.CREAD | syscall.CLOCAL
	termios.Cflag &^= syscall.PARENB | syscall.PARODD
	termios.Cflag &^= syscall.CSTOPB

	baud, err := baudConstant(speed)
	if err != nil {
		return err
	}
	termios.Cflag &^= cbaud
	termios.Cflag |= baud
	termios.Ispeed = baud
	termios.Ospeed = baud

	termios.Cc[syscall.VMIN] = 0
	termios.Cc[syscall.VTIME] = 1

	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

func baudConstant(speed int) (uint32, error) {
	switch speed {
	case 1200:
		return syscall.B1200, nil
	case 2400:
		return syscall.B2400, nil
	case 4800:
		return syscall.B4800, nil
	case 9600:
		return syscall.B9600, nil
	case 19200:
		return syscall.B19200, nil
	case 38400:
		return syscall.B38400, nil
	case 57600:
		return syscall.B57600, nil
	case 115200:
		return syscall.B115200, nil
	case 230400:
		return syscall.B230400, nil
	default:
		return 0, fmt.Errorf("unsupported serial speed: %v", speed)
	}
}
