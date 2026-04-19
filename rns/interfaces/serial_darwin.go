// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package interfaces

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	SerialDefaultSpeed    = 9600
	SerialDefaultDataBits = 8
	SerialDefaultStopBits = 1
	SerialDefaultParity   = "N"
	SerialHWMTU           = 564
	SerialFrameTimeout    = 100 * time.Millisecond
	serialReconnectDelay  = 5 * time.Second
)

type serialInterface struct {
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

// NewSerialInterface opens and configures a low-level Unix TTY serial port,
// enforcing precise baud rates and framing characteristics. It scans the inbound
// bitstream for HDLC structural markers and reassembles payloads before
// delivering them to the upper routing layers.
func NewSerialInterface(name, port string, speed, databits, stopbits int, parity string, handler InboundHandler) (Interface, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("no port specified for serial interface")
	}

	if speed <= 0 {
		speed = SerialDefaultSpeed
	}
	if databits <= 0 {
		databits = SerialDefaultDataBits
	}
	if stopbits <= 0 {
		stopbits = SerialDefaultStopBits
	}
	if strings.TrimSpace(parity) == "" {
		parity = SerialDefaultParity
	}

	si := &serialInterface{
		BaseInterface:  NewBaseInterface(name, ModeFull, speed),
		port:           port,
		speed:          speed,
		databits:       databits,
		parity:         parity,
		stopbits:       stopbits,
		inboundHandler: handler,
	}

	if err := si.openAndConfigure(); err != nil {
		return nil, err
	}

	atomic.StoreInt32(&si.running, 1)
	go si.readLoop()

	return si, nil
}

func (si *serialInterface) Type() string { return "SerialInterface" }

func (si *serialInterface) IsOut() bool { return true }

func (si *serialInterface) Status() bool { return atomic.LoadInt32(&si.running) == 1 }

func (si *serialInterface) Send(data []byte) error {
	if atomic.LoadInt32(&si.running) != 1 {
		return fmt.Errorf("interface %v is not running", si.name)
	}

	frame := append([]byte{HDLCFlag}, HDLCEscape(data)...)
	frame = append(frame, HDLCFlag)

	si.mu.Lock()
	file := si.file
	si.mu.Unlock()
	if file == nil {
		return fmt.Errorf("serial interface %v has no open port", si.name)
	}

	written, err := file.Write(frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("serial interface only wrote %v bytes of %v", written, len(frame))
	}

	atomic.AddUint64(&si.txBytes, uint64(len(frame)))
	return nil
}

func (si *serialInterface) Detach() error {
	si.SetDetached(true)
	atomic.StoreInt32(&si.running, 0)

	si.mu.Lock()
	defer si.mu.Unlock()
	if si.file == nil {
		return nil
	}
	err := si.file.Close()
	si.file = nil
	return err
}

func (si *serialInterface) openAndConfigure() error {
	file, err := os.OpenFile(si.port, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}

	if err := configureTermios(file.Fd(), si.speed, si.databits, si.parity, si.stopbits); err != nil {
		_ = file.Close()
		return err
	}

	si.mu.Lock()
	si.file = file
	si.mu.Unlock()
	return nil
}

func (si *serialInterface) closePort() error {
	si.mu.Lock()
	defer si.mu.Unlock()
	if si.file == nil {
		return nil
	}
	err := si.file.Close()
	si.file = nil
	return err
}

func (si *serialInterface) readLoop() {
	for !si.IsDetached() {
		if err := si.readLoopOnce(); err != nil {
			atomic.StoreInt32(&si.running, 0)
			if si.IsDetached() {
				return
			}
			si.panicOnInterfaceErrorf("serial interface %v read failed: %v", si.name, err)
			time.Sleep(serialReconnectDelay)
			if err := si.openAndConfigure(); err != nil {
				si.panicOnInterfaceErrorf("serial interface %v reopen failed: %v", si.name, err)
				continue
			}
			atomic.StoreInt32(&si.running, 1)
		}
	}
}

func (si *serialInterface) readLoopOnce() error {
	inFrame := false
	escape := false
	dataBuffer := make([]byte, 0, SerialHWMTU)
	lastRead := time.Now()
	readBuf := make([]byte, SerialHWMTU)

	for atomic.LoadInt32(&si.running) == 1 {
		si.mu.Lock()
		file := si.file
		si.mu.Unlock()
		if file == nil {
			return fmt.Errorf("serial port closed")
		}

		n, err := file.Read(readBuf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EIO) {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		if n == 0 {
			if len(dataBuffer) > 0 && time.Since(lastRead) > SerialFrameTimeout {
				dataBuffer = dataBuffer[:0]
				inFrame = false
				escape = false
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}

		lastRead = time.Now()
		for i := range n {
			b := readBuf[i]

			if inFrame && b == HDLCFlag {
				if len(dataBuffer) > 0 {
					payload := make([]byte, len(dataBuffer))
					copy(payload, dataBuffer)
					atomic.AddUint64(&si.rxBytes, uint64(len(payload)))
					if si.inboundHandler != nil {
						si.inboundHandler(payload, si)
					}
				}
				inFrame = false
				dataBuffer = dataBuffer[:0]
				continue
			}

			if b == HDLCFlag {
				inFrame = true
				escape = false
				dataBuffer = dataBuffer[:0]
				continue
			}

			if !inFrame || len(dataBuffer) >= SerialHWMTU {
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
	}

	return si.closePort()
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

func serialBaudConstant(speed int) (uint32, error) {
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
