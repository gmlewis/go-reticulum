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
	AX25KISSDefaultSpeed       = 9600
	AX25KISSDefaultDataBits    = 8
	AX25KISSDefaultStopBits    = 1
	AX25KISSDefaultParity      = "N"
	AX25KISSDefaultPreambleMS  = 350
	AX25KISSDefaultTxTailMS    = 20
	AX25KISSDefaultPersistence = 64
	AX25KISSDefaultSlotTimeMS  = 20
	AX25KISSDefaultDstCall     = "APZRNS"
	AX25KISSBitrateGuess       = 1200
	AX25KISSHeaderSize         = 16
	ax25KISSReconnectDelay     = 5 * time.Second
	ax25KISSFlowControlTimeout = 5 * time.Second
)

type ax25KISSInterface struct {
	*BaseInterface

	port     string
	speed    int
	databits int
	parity   string
	stopbits int

	file           *os.File
	inboundHandler InboundHandler

	srcCall []byte
	srcSSID int
	dstCall []byte
	dstSSID int

	preambleMS  int
	txTailMS    int
	persistence int
	slotTimeMS  int

	flowControl      bool
	interfaceReady   bool
	flowControlUntil time.Time
	packetQueue      [][]byte

	running int32
	mu      sync.Mutex
}

// NewAX25KISSInterface instantiates a low-level AX.25 amateur-radio packet
// interface over a standard serial TTY port using the KISS protocol framing.
// It orchestrates hardware-level flow control, termios configuration, and
// asynchronous read/write queues to ensure robust RF communication.
func NewAX25KISSInterface(name, port string, speed, databits, stopbits int, parity, callsign string, ssid, preambleMS, txTailMS, persistence, slotTimeMS int, flowControl bool, handler InboundHandler) (Interface, error) {
	if strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("no port specified for serial interface")
	}

	if speed <= 0 {
		speed = AX25KISSDefaultSpeed
	}
	if databits <= 0 {
		databits = AX25KISSDefaultDataBits
	}
	if stopbits <= 0 {
		stopbits = AX25KISSDefaultStopBits
	}
	if strings.TrimSpace(parity) == "" {
		parity = AX25KISSDefaultParity
	}
	if strings.TrimSpace(callsign) == "" {
		return nil, fmt.Errorf("invalid callsign for AX25KISSInterface[%v]", name)
	}
	callsign = strings.ToUpper(strings.TrimSpace(callsign))
	if len(callsign) < 3 || len(callsign) > 6 {
		return nil, fmt.Errorf("invalid callsign for AX25KISSInterface[%v]", name)
	}
	if ssid < 0 || ssid > 15 {
		return nil, fmt.Errorf("invalid SSID for AX25KISSInterface[%v]", name)
	}

	if preambleMS <= 0 {
		preambleMS = AX25KISSDefaultPreambleMS
	}
	if txTailMS <= 0 {
		txTailMS = AX25KISSDefaultTxTailMS
	}
	if persistence < 0 {
		persistence = AX25KISSDefaultPersistence
	}
	if slotTimeMS <= 0 {
		slotTimeMS = AX25KISSDefaultSlotTimeMS
	}

	ai := &ax25KISSInterface{
		BaseInterface: NewBaseInterface(name, ModeFull, AX25KISSBitrateGuess),
		port:          port,
		speed:         speed,
		databits:      databits,
		parity:        parity,
		stopbits:      stopbits,

		inboundHandler: handler,

		srcCall: []byte(callsign),
		srcSSID: ssid,
		dstCall: []byte(AX25KISSDefaultDstCall),
		dstSSID: 0,

		preambleMS:  preambleMS,
		txTailMS:    txTailMS,
		persistence: persistence,
		slotTimeMS:  slotTimeMS,

		flowControl: flowControl,
	}

	if err := ai.openAndConfigure(); err != nil {
		return nil, err
	}

	if err := ai.configureDevice(); err != nil {
		_ = ai.closePort()
		return nil, err
	}

	atomic.StoreInt32(&ai.running, 1)
	go ai.readLoop()

	return ai, nil
}

func (ai *ax25KISSInterface) Type() string {
	return "AX25KISSInterface"
}

func (ai *ax25KISSInterface) IsOut() bool {
	return true
}

func (ai *ax25KISSInterface) Status() bool {
	return atomic.LoadInt32(&ai.running) == 1
}

func (ai *ax25KISSInterface) Send(data []byte) error {
	if atomic.LoadInt32(&ai.running) != 1 {
		return fmt.Errorf("interface %v is not running", ai.name)
	}

	ai.mu.Lock()
	if ai.flowControl && !ai.interfaceReady {
		buf := make([]byte, len(data))
		copy(buf, data)
		ai.packetQueue = append(ai.packetQueue, buf)
		ai.mu.Unlock()
		return nil
	}
	if ai.flowControl {
		ai.interfaceReady = false
		ai.flowControlUntil = time.Now().Add(ax25KISSFlowControlTimeout)
	}
	ai.mu.Unlock()

	return ai.sendAX25Payload(data)
}

func (ai *ax25KISSInterface) sendAX25Payload(payload []byte) error {
	ax25 := ai.encodeAX25(payload)
	frame := make([]byte, 0, len(ax25)+3)
	frame = append(frame, KISSFend, KISSCmdData)
	frame = append(frame, KISSEscape(ax25)...)
	frame = append(frame, KISSFend)

	ai.mu.Lock()
	file := ai.file
	ai.mu.Unlock()
	if file == nil {
		return fmt.Errorf("AX25 KISS interface %v has no open port", ai.name)
	}

	written, err := file.Write(frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		if ai.flowControl {
			ai.mu.Lock()
			ai.interfaceReady = true
			ai.mu.Unlock()
		}
		return fmt.Errorf("AX.25 interface only wrote %v bytes of %v", written, len(frame))
	}

	atomic.AddUint64(&ai.txBytes, uint64(len(payload)))
	return nil
}

func (ai *ax25KISSInterface) Detach() error {
	ai.SetDetached(true)
	atomic.StoreInt32(&ai.running, 0)

	ai.mu.Lock()
	defer ai.mu.Unlock()
	if ai.file == nil {
		return nil
	}
	err := ai.file.Close()
	ai.file = nil
	return err
}

func (ai *ax25KISSInterface) openAndConfigure() error {
	file, err := os.OpenFile(ai.port, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return err
	}

	if err := configureTermios(file.Fd(), ai.speed, ai.databits, ai.parity, ai.stopbits); err != nil {
		_ = file.Close()
		return err
	}

	ai.mu.Lock()
	ai.file = file
	ai.mu.Unlock()
	return nil
}

func (ai *ax25KISSInterface) configureDevice() error {
	time.Sleep(2 * time.Second)
	if err := ai.writeKISSParam(0x01, toTenthsClamped(ai.preambleMS)); err != nil {
		return err
	}
	if err := ai.writeKISSParam(0x04, toTenthsClamped(ai.txTailMS)); err != nil {
		return err
	}
	if err := ai.writeKISSParam(0x02, clampByte(ai.persistence)); err != nil {
		return err
	}
	if err := ai.writeKISSParam(0x03, toTenthsClamped(ai.slotTimeMS)); err != nil {
		return err
	}
	if err := ai.writeKISSParam(0x0F, 0x01); err != nil {
		return err
	}

	ai.mu.Lock()
	ai.interfaceReady = true
	ai.mu.Unlock()
	return nil
}

func (ai *ax25KISSInterface) writeKISSParam(command, value byte) error {
	frame := []byte{KISSFend, command, value, KISSFend}

	ai.mu.Lock()
	file := ai.file
	ai.mu.Unlock()
	if file == nil {
		return fmt.Errorf("AX25 KISS interface %v has no open port", ai.name)
	}

	written, err := file.Write(frame)
	if err != nil {
		return err
	}
	if written != len(frame) {
		return fmt.Errorf("could not configure AX.25 KISS interface command %v", command)
	}
	return nil
}

func (ai *ax25KISSInterface) closePort() error {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if ai.file == nil {
		return nil
	}
	err := ai.file.Close()
	ai.file = nil
	return err
}

func (ai *ax25KISSInterface) readLoop() {
	for !ai.IsDetached() {
		if err := ai.readLoopOnce(); err != nil {
			atomic.StoreInt32(&ai.running, 0)
			if ai.IsDetached() {
				return
			}
			panicOnInterfaceErrorf("AX.25 KISS interface %v read failed: %v", ai.name, err)
			time.Sleep(ax25KISSReconnectDelay)
			if err := ai.openAndConfigure(); err != nil {
				panicOnInterfaceErrorf("AX.25 KISS interface %v reopen failed: %v", ai.name, err)
				continue
			}
			if err := ai.configureDevice(); err != nil {
				panicOnInterfaceErrorf("AX.25 KISS interface %v reconfigure failed: %v", ai.name, err)
				_ = ai.closePort()
				continue
			}
			atomic.StoreInt32(&ai.running, 1)
		}
	}
}

func (ai *ax25KISSInterface) readLoopOnce() error {
	buf := make([]byte, 1024)
	frameBuffer := make([]byte, 0, 4096)

	for atomic.LoadInt32(&ai.running) == 1 {
		ai.mu.Lock()
		if ai.flowControl && !ai.interfaceReady && !ai.flowControlUntil.IsZero() && time.Now().After(ai.flowControlUntil) {
			ai.interfaceReady = true
		}
		file := ai.file
		ai.mu.Unlock()
		if file == nil {
			return fmt.Errorf("AX25 KISS port closed")
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
				switch command {
				case KISSCmdData:
					payload := KISSUnescape(frame[1:])
					if len(payload) > AX25KISSHeaderSize {
						userPayload := payload[AX25KISSHeaderSize:]
						atomic.AddUint64(&ai.rxBytes, uint64(len(userPayload)))
						if ai.inboundHandler != nil {
							ai.inboundHandler(userPayload, ai)
						}
					}
				case 0x0F:
					ai.processQueue()
				}
			}

			frameBuffer = frameBuffer[end:]
		}
	}

	return ai.closePort()
}

func (ai *ax25KISSInterface) processQueue() {
	ai.mu.Lock()
	if len(ai.packetQueue) == 0 {
		ai.interfaceReady = true
		ai.mu.Unlock()
		return
	}
	next := ai.packetQueue[0]
	ai.packetQueue = ai.packetQueue[1:]
	ai.interfaceReady = true
	ai.mu.Unlock()

	_ = ai.Send(next)
}

func (ai *ax25KISSInterface) encodeAX25(payload []byte) []byte {
	addr := make([]byte, 0, AX25KISSHeaderSize)

	for i := 0; i < 6; i++ {
		if i < len(ai.dstCall) {
			addr = append(addr, ai.dstCall[i]<<1)
		} else {
			addr = append(addr, 0x20)
		}
	}
	addr = append(addr, byte(0x60|(ai.dstSSID<<1)))

	for i := 0; i < 6; i++ {
		if i < len(ai.srcCall) {
			addr = append(addr, ai.srcCall[i]<<1)
		} else {
			addr = append(addr, 0x20)
		}
	}
	addr = append(addr, byte(0x60|(ai.srcSSID<<1)|0x01))

	addr = append(addr, 0x03, 0xF0)
	addr = append(addr, payload...)
	return addr
}

func clampByte(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

func toTenthsClamped(ms int) byte {
	if ms < 0 {
		ms = 0
	}
	return clampByte(ms / 10)
}
