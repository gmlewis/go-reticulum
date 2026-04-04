// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"time"
)

var errReadEEPROMTimeout = errors.New("timed out while reading device EEPROM")

func captureRnodeEEPROM(portName string, port serialPort, timeout time.Duration) (*eepromDownloaderState, error) {
	state := newRnodeReadLoopState()

	if _, err := port.Write([]byte{kissFend, rnodeKISSCommandROMRead, 0x00, kissFend}); err != nil {
		_ = port.Close()
		return nil, err
	}

	deadline := time.After(timeout)
	for {
		readCh := make(chan struct {
			b   byte
			n   int
			err error
		}, 1)
		go func() {
			buf := make([]byte, 1)
			n, err := port.Read(buf)
			if n > 0 {
				readCh <- struct {
					b   byte
					n   int
					err error
				}{b: buf[0], n: n, err: err}
				return
			}
			readCh <- struct {
				b   byte
				n   int
				err error
			}{err: err}
		}()

		select {
		case res := <-readCh:
			if res.n > 0 {
				if frame, ok := state.feedByte(res.b); ok && frame.command == rnodeKISSCommandROMRead {
					eepromState := &eepromDownloaderState{name: "rnode", eeprom: append([]byte(nil), frame.payload...)}
					if err := eepromState.parseEEPROM(); err != nil {
						return nil, err
					}
					return eepromState, nil
				}
			}
			if res.err != nil {
				if errors.Is(res.err, io.EOF) {
					_ = port.Close()
					return nil, fmt.Errorf("device %v closed the serial port before returning EEPROM", portName)
				}
				_ = port.Close()
				return nil, res.err
			}
		case <-deadline:
			_ = port.Close()
			return nil, errReadEEPROMTimeout
		}
	}
}
