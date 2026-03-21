// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	PipeBitrateGuess          = 1 * 1000 * 1000
	PipeHWMTU                 = 1064
	PipeDefaultRespawnDelay   = 5 * time.Second
	pipeReadSleep             = 50 * time.Millisecond
	pipeReconnectPollInterval = 100 * time.Millisecond
)

type pipeSubprocessInterface struct {
	*BaseInterface

	command      string
	respawnDelay time.Duration

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	inboundHandler InboundHandler

	running int32
	mu      sync.Mutex
}

// NewPipeSubprocessInterface forks a child OS process and establishes a
// bidirectional HDLC-framed communication channel over its standard I/O
// streams. It provides a resilient bridge and can automatically respawn the
// external command if it terminates unexpectedly.
func NewPipeSubprocessInterface(name, command string, respawnDelay time.Duration, handler InboundHandler) (Interface, error) {
	if strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("no command specified for PipeInterface")
	}
	if respawnDelay <= 0 {
		respawnDelay = PipeDefaultRespawnDelay
	}

	pi := &pipeSubprocessInterface{
		BaseInterface:  NewBaseInterface(name, ModeFull, PipeBitrateGuess),
		command:        command,
		respawnDelay:   respawnDelay,
		inboundHandler: handler,
	}

	if err := pi.spawnProcess(); err != nil {
		return nil, err
	}

	atomic.StoreInt32(&pi.running, 1)
	go pi.readLoop()

	return pi, nil
}

func (pi *pipeSubprocessInterface) Type() string {
	return "PipeInterface"
}

func (pi *pipeSubprocessInterface) IsOut() bool {
	return true
}

func (pi *pipeSubprocessInterface) Status() bool {
	return atomic.LoadInt32(&pi.running) == 1
}

func (pi *pipeSubprocessInterface) Send(data []byte) error {
	if atomic.LoadInt32(&pi.running) != 1 {
		return fmt.Errorf("interface %v is not running", pi.name)
	}

	frame := append([]byte{HDLCFlag}, HDLCEscape(data)...)
	frame = append(frame, HDLCFlag)

	pi.mu.Lock()
	stdin := pi.stdin
	pi.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("pipe interface %v has no subprocess stdin", pi.name)
	}

	written, err := stdin.Write(frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("pipe interface only wrote %v bytes of %v", written, len(frame))
	}

	atomic.AddUint64(&pi.txBytes, uint64(len(frame)))
	return nil
}

func (pi *pipeSubprocessInterface) Detach() error {
	pi.SetDetached(true)
	atomic.StoreInt32(&pi.running, 0)
	return pi.killProcess()
}

func (pi *pipeSubprocessInterface) spawnProcess() error {
	parts := strings.Fields(pi.command)
	if len(parts) == 0 {
		return fmt.Errorf("no command specified for PipeInterface")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}

	pi.mu.Lock()
	pi.cmd = cmd
	pi.stdin = stdin
	pi.stdout = stdout
	pi.mu.Unlock()

	return nil
}

func (pi *pipeSubprocessInterface) killProcess() error {
	pi.mu.Lock()
	defer pi.mu.Unlock()

	var firstErr error
	if pi.stdin != nil {
		if err := pi.stdin.Close(); err != nil {
			firstErr = err
		}
		pi.stdin = nil
	}
	if pi.stdout != nil {
		if err := pi.stdout.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		pi.stdout = nil
	}
	if pi.cmd != nil && pi.cmd.Process != nil {
		if err := pi.cmd.Process.Kill(); err != nil && !strings.Contains(err.Error(), "process already finished") && firstErr == nil {
			firstErr = err
		}
		if _, err := pi.cmd.Process.Wait(); err != nil && firstErr == nil {
			firstErr = err
		}
		pi.cmd = nil
	}

	return firstErr
}

func (pi *pipeSubprocessInterface) readLoop() {
	for !pi.IsDetached() {
		err := pi.readFrames()
		if pi.IsDetached() {
			return
		}

		atomic.StoreInt32(&pi.running, 0)
		if killErr := pi.killProcess(); killErr != nil {
			log.Printf("readLoop: failed to kill pipe process: %v", killErr)
		}
		if err != nil {
			log.Printf("readLoop: failed to read pipe frames: %v", err)
		}

		timer := time.NewTimer(pi.respawnDelay)
		select {
		case <-timer.C:
		case <-time.After(pipeReconnectPollInterval):
			if !timer.Stop() {
				<-timer.C
			}
		}

		if pi.IsDetached() {
			return
		}
		if err := pi.spawnProcess(); err != nil {
			continue
		}
		atomic.StoreInt32(&pi.running, 1)
	}
}

func (pi *pipeSubprocessInterface) readFrames() error {
	pi.mu.Lock()
	stdout := pi.stdout
	pi.mu.Unlock()
	if stdout == nil {
		return fmt.Errorf("pipe stdout is nil")
	}

	inFrame := false
	escape := false
	dataBuffer := make([]byte, 0, PipeHWMTU)
	one := make([]byte, 1)

	for atomic.LoadInt32(&pi.running) == 1 {
		n, err := stdout.Read(one)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if n == 0 {
			time.Sleep(pipeReadSleep)
			continue
		}

		b := one[0]
		if inFrame && b == HDLCFlag {
			inFrame = false
			if len(dataBuffer) > 0 {
				payload := make([]byte, len(dataBuffer))
				copy(payload, dataBuffer)
				atomic.AddUint64(&pi.rxBytes, uint64(len(payload)))
				if pi.inboundHandler != nil {
					pi.inboundHandler(payload, pi)
				}
			}
			dataBuffer = dataBuffer[:0]
			continue
		}

		if b == HDLCFlag {
			inFrame = true
			escape = false
			dataBuffer = dataBuffer[:0]
			continue
		}

		if !inFrame || len(dataBuffer) >= PipeHWMTU {
			continue
		}

		if b == HDLCEsc {
			escape = true
			continue
		}

		if escape {
			b = b ^ HDLCEscMask
			escape = false
		}

		dataBuffer = append(dataBuffer, b)
	}

	return nil
}
