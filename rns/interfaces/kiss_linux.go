// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	KISSDefaultSpeed    = 9600
	KISSDefaultDataBits = 8
	KISSDefaultStopBits = 1
	KISSDefaultParity   = "N"
	kissReconnectDelay  = 5 * time.Second
)

type kissInterface struct {
	*BaseInterface

	port     string
	speed    int
	databits int
	parity   string
	stopbits int

	file           *os.File
	inboundHandler InboundHandler

	running int32
	mu      sync.Mutex
}

// NewKISSInterface opens and configures a serial KISS link to a TNC (Terminal
// Node Controller) using the supplied serial settings.
func NewKISSInterface(name, port string, speed, databits, stopbits int, parity string, handler InboundHandler) (Interface, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("no port specified for KISS interface")
	}

	if speed <= 0 {
		speed = KISSDefaultSpeed
	}
	if databits <= 0 {
		databits = KISSDefaultDataBits
	}
	if stopbits <= 0 {
		stopbits = KISSDefaultStopBits
	}
	if strings.TrimSpace(parity) == "" {
		parity = KISSDefaultParity
	}

	ki := &kissInterface{
		BaseInterface:  NewBaseInterface(name, ModeFull, speed),
		port:           port,
		speed:          speed,
		databits:       databits,
		parity:         parity,
		stopbits:       stopbits,
		inboundHandler: handler,
	}

	if err := ki.openAndConfigure(); err != nil {
		return nil, err
	}

	atomic.StoreInt32(&ki.running, 1)
	go ki.readLoop()

	return ki, nil
}

func (ki *kissInterface) Type() string {
	return "KISSInterface"
}

func (ki *kissInterface) IsOut() bool {
	return true
}

func (ki *kissInterface) Status() bool {
	return atomic.LoadInt32(&ki.running) == 1
}

func (ki *kissInterface) Send(data []byte) error {
	if atomic.LoadInt32(&ki.running) != 1 {
		return fmt.Errorf("interface %v is not running", ki.name)
	}

	frame := make([]byte, 0, len(data)+3)
	frame = append(frame, KISSFend, KISSCmdData)
	frame = append(frame, KISSEscape(data)...)
	frame = append(frame, KISSFend)

	ki.mu.Lock()
	file := ki.file
	ki.mu.Unlock()
	if file == nil {
		return fmt.Errorf("KISS interface %v has no open port", ki.name)
	}

	written, err := file.Write(frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("KISS interface only wrote %v bytes of %v", written, len(frame))
	}

	atomic.AddUint64(&ki.txBytes, uint64(len(frame)))
	return nil
}

func (ki *kissInterface) Detach() error {
	ki.SetDetached(true)
	atomic.StoreInt32(&ki.running, 0)

	ki.mu.Lock()
	defer ki.mu.Unlock()
	if ki.file == nil {
		return nil
	}
	err := ki.file.Close()
	ki.file = nil
	return err
}

func (ki *kissInterface) openAndConfigure() error {
	file, err := os.OpenFile(ki.port, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}

	if err := configureTermios(file.Fd(), ki.speed, ki.databits, ki.parity, ki.stopbits); err != nil {
		_ = file.Close()
		return err
	}

	ki.mu.Lock()
	ki.file = file
	ki.mu.Unlock()
	return nil
}

func (ki *kissInterface) closePort() error {
	ki.mu.Lock()
	defer ki.mu.Unlock()
	if ki.file == nil {
		return nil
	}
	err := ki.file.Close()
	ki.file = nil
	return err
}

func (ki *kissInterface) readLoop() {
	for !ki.IsDetached() {
		if err := ki.readLoopOnce(); err != nil {
			atomic.StoreInt32(&ki.running, 0)
			if ki.IsDetached() {
				return
			}
			time.Sleep(kissReconnectDelay)
			if err := ki.openAndConfigure(); err != nil {
				continue
			}
			atomic.StoreInt32(&ki.running, 1)
		}
	}
}

func (ki *kissInterface) readLoopOnce() error {
	buf := make([]byte, 1024)
	frameBuffer := make([]byte, 0, 4096)

	for atomic.LoadInt32(&ki.running) == 1 {
		ki.mu.Lock()
		file := ki.file
		ki.mu.Unlock()
		if file == nil {
			return fmt.Errorf("KISS port closed")
		}

		n, err := file.Read(buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}

		if n == 0 {
			time.Sleep(20 * time.Millisecond)
			continue
		}

		frameBuffer = append(frameBuffer, buf[:n]...)
		for {
			start := bytes.IndexByte(frameBuffer, KISSFend)
			if start == -1 {
				frameBuffer = frameBuffer[:0]
				break
			}
			end := bytes.IndexByte(frameBuffer[start+1:], KISSFend)
			if end == -1 {
				frameBuffer = frameBuffer[start:]
				break
			}
			end += start + 1

			frame := frameBuffer[start+1 : end]
			if len(frame) > 0 {
				command := frame[0] & 0x0F
				if command == KISSCmdData {
					payload := KISSUnescape(frame[1:])
					if len(payload) > 0 {
						atomic.AddUint64(&ki.rxBytes, uint64(len(payload)))
						if ki.inboundHandler != nil {
							ki.inboundHandler(payload, ki)
						}
					}
				}
			}

			frameBuffer = frameBuffer[end:]
		}
	}

	return ki.closePort()
}
