// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
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

const cbaud = 0x100f

func configureTermios(fd uintptr, speed, databits int, parity string, stopbits int) error {
	termios := &syscall.Termios{}
	if _, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(termios)), 0, 0, 0); errno != 0 {
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

	baud, err := linuxBaudConstant(speed)
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

func linuxBaudConstant(speed int) (uint32, error) {
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
