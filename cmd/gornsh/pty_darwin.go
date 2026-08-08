// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func openPTY() (*ptyPair, error) {
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	if master == nil {
		_ = syscall.Close(masterFD)
		return nil, fmt.Errorf("could not create PTY master file")
	}

	// TIOCPTYGRANT adjusts the slave pty's ownership/permissions so the
	// current user may open it; under macOS SIP the slave is otherwise not
	// accessible. The ioctl takes no argument (IOC_VOID).
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(syscall.TIOCPTYGRANT), 0); errno != 0 {
		_ = master.Close()
		return nil, errno
	}

	// TIOCPTYUNLK (unlockpt) unlocks the pty so the slave can be opened; the
	// kernel returns EAGAIN on the slave open without it. IOC_VOID, no arg.
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(syscall.TIOCPTYUNLK), 0); errno != 0 {
		_ = master.Close()
		return nil, errno
	}

	// TIOCPTYGNAME fills a 128-byte buffer with the slave device path as a
	// NUL-terminated C string (e.g. "/dev/ttys004").
	var nameBuf [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(syscall.TIOCPTYGNAME), uintptr(unsafe.Pointer(&nameBuf[0]))); errno != 0 {
		_ = master.Close()
		return nil, errno
	}
	n := 0
	for n < len(nameBuf) && nameBuf[n] != 0 {
		n++
	}
	slavePath := string(nameBuf[:n])

	slaveFD, err := syscall.Open(slavePath, syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		_ = master.Close()
		return nil, err
	}

	slave := os.NewFile(uintptr(slaveFD), slavePath)
	if slave == nil {
		_ = syscall.Close(slaveFD)
		_ = master.Close()
		return nil, fmt.Errorf("could not create PTY slave file")
	}

	return &ptyPair{master: master, slave: slave}, nil
}

func configurePTYCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setctty: true, Setsid: true}
}

func setPTYTermios(fd int, raw any) error {
	termios, err := termiosFromPTYTCFlags(raw)
	if err != nil || termios == nil {
		return err
	}
	return ioctlSetTermios(fd, termios)
}

func termiosFromPTYTCFlags(raw any) (*syscall.Termios, error) {
	flags, err := decodePTYTCFlags(raw)
	if err != nil || flags == nil {
		return nil, err
	}

	term := &syscall.Termios{
		Iflag:  uint64(flags.IFlag),
		Oflag:  uint64(flags.OFlag),
		Cflag:  uint64(flags.CFlag),
		Lflag:  uint64(flags.LFlag),
		Ispeed: uint64(flags.ISpeed),
		Ospeed: uint64(flags.OSpeed),
	}
	copy(term.Cc[:], flags.CC)
	return term, nil
}
