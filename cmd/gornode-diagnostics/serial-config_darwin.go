// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// errBusy is the errno returned when a serial line is held by another process.
func errBusy() error { return syscall.EBUSY }

// deviceID returns the underlying character-device ID of a path, used to
// recognize when two paths (e.g. /dev/cu.* and /dev/tty.*) name the same
// physical serial device. Non-device paths report ok=false.
func deviceID(path string) (uint64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&(os.ModeDevice|os.ModeCharDevice) != os.ModeDevice|os.ModeCharDevice {
		return 0, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Rdev), true
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
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(syscall.F_SETFL), uintptr(flags&^syscall.O_NONBLOCK)); errno != 0 {
		return errno
	}
	return nil
}

// serialBusyCheck reports whether the given serial device appears to be held
// by another process. On macOS the callin (/dev/tty.*) and callout (/dev/cu.*)
// devices of one physical port are mutually exclusive: when another process
// holds one side open, opening the paired side returns EBUSY. Probing the
// paired device is therefore a reliable in-use check even though macOS allows
// duplicate opens of the same cu. device. The probe opens O_NONBLOCK because
// a callin-device open blocks on carrier detect otherwise (pyserial's
// workaround for the same macOS behavior).
func serialBusyCheck(port string) (bool, string) {
	var counterpart string
	switch {
	case strings.HasPrefix(port, "/dev/cu."):
		counterpart = "/dev/tty." + port[len("/dev/cu."):]
	case strings.HasPrefix(port, "/dev/tty."):
		counterpart = "/dev/cu." + port[len("/dev/tty."):]
	default:
		return false, ""
	}
	file, err := os.OpenFile(counterpart, os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err == nil {
		_ = file.Close()
		return false, ""
	}
	if errors.Is(err, syscall.EBUSY) {
		return true, fmt.Sprintf("paired device %v is held by another process (open() busy)", counterpart)
	}
	// An open error on the counterpart that is not EBUSY (e.g. the paired
	// device node is absent) says nothing about exclusivity.
	return false, ""
}

// configureSerialPort configures the raw TTY for binary KISS traffic at the
// given baud rate (mirrors RNS/Interfaces/serial_darwin.go configureTermios):
// raw mode, 8 data bits, no parity, 1 stop bit, no flow control, and a 100 ms
// inter-byte read timeout (VMIN=0/VTIME=1) so reads never block indefinitely.
func configureSerialPort(fd uintptr, speed int) error {
	termios := &syscall.Termios{}
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
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
	termios.Ispeed = uint64(baud)
	termios.Ospeed = uint64(baud)

	termios.Cc[syscall.VMIN] = 0
	termios.Cc[syscall.VTIME] = 1

	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCSETA), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
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
