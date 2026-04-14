// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func defaultOpenSerial(settings serialSettings) (serialPort, error) {
	file, err := os.OpenFile(settings.Port, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}

	if err := configureTermios(file.Fd(), settings.BaudRate, settings.ByteSize, settings.Parity, settings.StopBits); err != nil {
		_ = file.Close()
		return nil, err
	}

	return file, nil
}

func configureTermios(fd uintptr, speed, databits int, parity string, stopbits int) error {
	termios := &syscall.Termios{}
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
		return errno
	}

	termios.Iflag = 0
	termios.Oflag = 0
	termios.Lflag = 0

	termios.Cflag &^= syscall.CSIZE
	switch databits {
	case 5:
		termios.Cflag |= syscall.CS5
	case 6:
		termios.Cflag |= syscall.CS6
	case 7:
		termios.Cflag |= syscall.CS7
	default:
		termios.Cflag |= syscall.CS8
	}

	termios.Cflag |= syscall.CREAD | syscall.CLOCAL

	termios.Cflag &^= syscall.PARENB | syscall.PARODD
	switch strings.ToLower(strings.TrimSpace(parity)) {
	case "e", "even":
		termios.Cflag |= syscall.PARENB
	case "o", "odd":
		termios.Cflag |= syscall.PARENB | syscall.PARODD
	}

	termios.Cflag &^= syscall.CSTOPB
	if stopbits == 2 {
		termios.Cflag |= syscall.CSTOPB
	}

	baud, err := serialBaudConstant(speed)
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
