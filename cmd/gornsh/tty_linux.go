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

type ttyRestorer struct {
	fd     int
	saved  syscall.Termios
	active bool
}

func newTTYRestorer(fd int) (*ttyRestorer, error) {
	restorer := &ttyRestorer{fd: fd}
	if fd < 0 {
		return restorer, nil
	}
	if _, err := ioctlGetTermios(fd, &restorer.saved); err != nil {
		return restorer, nil
	}
	restorer.active = true
	return restorer, nil
}

func (t *ttyRestorer) raw() error {
	if t == nil || !t.active {
		return nil
	}
	raw := t.saved
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctlSetTermios(t.fd, &raw); err != nil {
		return fmt.Errorf("could not enable raw mode: %w", err)
	}
	return nil
}

func (t *ttyRestorer) restore() error {
	if t == nil || !t.active {
		return nil
	}
	if err := ioctlSetTermios(t.fd, &t.saved); err != nil {
		return fmt.Errorf("could not restore terminal mode: %w", err)
	}
	return nil
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
