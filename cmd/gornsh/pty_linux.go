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
	"time"
	"unsafe"

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
		if err := ioctlSetTermios(int(pty.slave.Fd()), termios); err != nil {
			pty.close()
			return nil, err
		}
	}

	cmd := exec.Command(commandLine[0], commandLine[1:]...)
	cmd.Env = buildSessionCommandEnv(os.Environ(), remoteIdentity, execute)
	cmd.Stdin = pty.slave
	cmd.Stdout = pty.slave
	cmd.Stderr = pty.slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

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
		_ = sendMessageWithRetry(sender, &commandExitedMessage{ReturnCode: exitCode}, time.Now().Add(2*time.Second))
		active.close()
	}()

	return active, nil
}

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
			term.Iflag = value
		}
	}
	if len(parts) > 1 {
		if value, ok := toUint32(parts[1]); ok {
			term.Oflag = value
		}
	}
	if len(parts) > 2 {
		if value, ok := toUint32(parts[2]); ok {
			term.Cflag = value
		}
	}
	if len(parts) > 3 {
		if value, ok := toUint32(parts[3]); ok {
			term.Lflag = value
		}
	}
	if len(parts) > 4 {
		if value, ok := toUint32(parts[4]); ok {
			term.Ispeed = value
		}
	}
	if len(parts) > 5 {
		if value, ok := toUint32(parts[5]); ok {
			term.Ospeed = value
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
