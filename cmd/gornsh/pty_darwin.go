// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

/*
#include <stdlib.h>
#include <unistd.h>
#include <util.h>
#include <termios.h>
*/
import "C"

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func openPTY() (*ptyPair, error) {
	var masterFD, slaveFD C.int
	if C.openpty(&masterFD, &slaveFD, nil, nil, nil) != 0 {
		return nil, fmt.Errorf("openpty failed")
	}

	masterName := C.ttyname(slaveFD)
	if masterName == nil {
		C.close(masterFD)
		C.close(slaveFD)
		return nil, fmt.Errorf("ttyname failed")
	}

	master := os.NewFile(uintptr(masterFD), "/dev/ptmx")
	slave := os.NewFile(uintptr(slaveFD), C.GoString(masterName))

	if master == nil {
		C.close(masterFD)
		C.close(slaveFD)
		return nil, fmt.Errorf("could not create PTY master file")
	}

	if slave == nil {
		C.close(masterFD)
		C.close(slaveFD)
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
	return setTermios(fd, termios)
}

func setTermios(fd int, termios *syscall.Termios) error {
	var ctermios C.struct_termios
	ctermios.c_iflag = C.tcflag_t(termios.Iflag)
	ctermios.c_oflag = C.tcflag_t(termios.Oflag)
	ctermios.c_cflag = C.tcflag_t(termios.Cflag)
	ctermios.c_lflag = C.tcflag_t(termios.Lflag)
	ctermios.c_ispeed = C.speed_t(termios.Ispeed)
	ctermios.c_ospeed = C.speed_t(termios.Ospeed)
	for i, v := range termios.Cc {
		ctermios.c_cc[i] = C.cc_t(v)
	}

	if _, err := C.tcsetattr(C.int(fd), C.TCSAFLUSH, &ctermios); err != nil {
		return err
	}
	return nil
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
