// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux && !windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

func newTTYRestorer(fd int) (*ttyRestorer, error) {
	if fd < 0 {
		return &ttyRestorer{}, nil
	}

	var saved syscall.Termios
	if _, err := ioctlGetTermios(fd, &saved); err != nil {
		return &ttyRestorer{}, nil
	}

	restorer := &ttyRestorer{}
	restorer.active = true
	restorer.rawFn = func() error {
		raw := saved
		raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
		raw.Oflag &^= syscall.OPOST
		raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
		raw.Cflag |= syscall.CS8
		raw.Cc[syscall.VMIN] = 1
		raw.Cc[syscall.VTIME] = 0
		if err := ioctlSetTermios(fd, &raw); err != nil {
			return fmt.Errorf("could not enable raw mode: %w", err)
		}
		return nil
	}
	restorer.restoreFn = func() error {
		if err := ioctlSetTermios(fd, &saved); err != nil {
			return fmt.Errorf("could not restore terminal mode: %w", err)
		}
		return nil
	}
	return restorer, nil
}

func ioctlGetTermios(fd int, termios *syscall.Termios) (syscall.Errno, error) {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0)
	if errno != 0 {
		return errno, errno
	}
	return 0, nil
}

func ioctlSetTermios(fd int, termios *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
