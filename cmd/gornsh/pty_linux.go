// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux && !windows

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

	unlock := int32(0)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(syscall.TIOCSPTLCK), uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = master.Close()
		return nil, errno
	}

	var ptyNum uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(masterFD), uintptr(syscall.TIOCGPTN), uintptr(unsafe.Pointer(&ptyNum))); errno != 0 {
		_ = master.Close()
		return nil, errno
	}

	slavePath := fmt.Sprintf("/dev/pts/%v", ptyNum)
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
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
		Iflag:  flags.IFlag,
		Oflag:  flags.OFlag,
		Cflag:  flags.CFlag,
		Lflag:  flags.LFlag,
		Ispeed: flags.ISpeed,
		Ospeed: flags.OSpeed,
	}
	copy(term.Cc[:], flags.CC)
	return term, nil
}
