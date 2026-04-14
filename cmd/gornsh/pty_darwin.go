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
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type ptyPair struct {
	master *os.File
	slave  *os.File
}

func (p *ptyPair) close() {
	if p == nil {
		return
	}
	if p.master != nil {
		_ = p.master.Close()
	}
	if p.slave != nil {
		_ = p.slave.Close()
	}
}

func (rt *runtimeT) startPTYSessionCommand(sender messageSender, commandLine []string, remoteIdentity *rns.Identity, execute *executeCommandMessage) (*activeCommand, error) {
	pty, err := openPTY()
	if err != nil {
		return nil, err
	}

	termios, err := termiosFromTCFlags(execute.TCFlags)
	if err != nil {
		pty.close()
		return nil, err
	}
	if termios != nil {
		if err := setTermios(int(pty.slave.Fd()), termios); err != nil {
			pty.close()
			return nil, err
		}
	}

	cmd := exec.Command(commandLine[0], commandLine[1:]...)
	cmd.Env = buildSessionCommandEnv(os.Environ(), remoteIdentity, execute)
	cmd.Stdin = pty.slave
	cmd.Stdout = pty.slave
	cmd.Stderr = pty.slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setctty: true, Setsid: true}

	if err := cmd.Start(); err != nil {
		pty.close()
		return nil, err
	}

	active := &activeCommand{
		rt:    rt,
		stdin: pty.slave,
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
	}

	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		active.streamPipe(sender, pty.master, streamIDStdout)
	}()
	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 127
				rt.sendProtocolErrorToSender(sender, err.Error(), true)
			}
		}
		active.markFinished()
		if pty.slave != nil {
			_ = pty.slave.Close()
			pty.slave = nil
		}
		active.mu.Lock()
		active.stdin = nil
		active.mu.Unlock()
		<-streamDone
		_ = sendMessageWithRetry(sender, &commandExitedMessage{ReturnCode: exitCode}, time.Now().Add(2*time.Second), defaultRetrySleep)
		active.close()
	}()

	return active, nil
}

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

func termiosFromTCFlags(raw any) (*syscall.Termios, error) {
	if raw == nil {
		return nil, nil
	}
	parts, ok := raw.([]any)
	if !ok || len(parts) == 0 {
		return nil, fmt.Errorf("invalid tcflags payload: %#v", raw)
	}

	term := &syscall.Termios{}
	if len(parts) > 0 {
		if value, ok := toUint32(parts[0]); ok {
			term.Iflag = uint64(value)
		}
	}
	if len(parts) > 1 {
		if value, ok := toUint32(parts[1]); ok {
			term.Oflag = uint64(value)
		}
	}
	if len(parts) > 2 {
		if value, ok := toUint32(parts[2]); ok {
			term.Cflag = uint64(value)
		}
	}
	if len(parts) > 3 {
		if value, ok := toUint32(parts[3]); ok {
			term.Lflag = uint64(value)
		}
	}
	if len(parts) > 4 {
		if value, ok := toUint32(parts[4]); ok {
			term.Ispeed = uint64(value)
		}
	}
	if len(parts) > 5 {
		if value, ok := toUint32(parts[5]); ok {
			term.Ospeed = uint64(value)
		}
	}
	if len(parts) > 6 {
		cc, err := toTermiosControlChars(parts[6])
		if err != nil {
			return nil, err
		}
		copy(term.Cc[:], cc)
	}

	return term, nil
}

func toUint32(value any) (uint32, bool) {
	switch n := value.(type) {
	case int:
		return uint32(n), true
	case int8:
		return uint32(n), true
	case int16:
		return uint32(n), true
	case int32:
		return uint32(n), true
	case int64:
		return uint32(n), true
	case uint:
		return uint32(n), true
	case uint8:
		return uint32(n), true
	case uint16:
		return uint32(n), true
	case uint32:
		return n, true
	case uint64:
		return uint32(n), true
	case float64:
		return uint32(n), true
	default:
		return 0, false
	}
}

func toTermiosControlChars(value any) ([]uint8, error) {
	switch chars := value.(type) {
	case []byte:
		out := make([]uint8, len(chars))
		copy(out, chars)
		return out, nil
	case []any:
		out := make([]uint8, 0, len(chars))
		for _, char := range chars {
			value, ok := toUint32(char)
			if !ok {
				return nil, fmt.Errorf("invalid control character value: %#v", char)
			}
			out = append(out, uint8(value))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("invalid control character payload: %#v", value)
	}
}
