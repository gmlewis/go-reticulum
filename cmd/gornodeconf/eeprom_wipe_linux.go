// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"io"
	"time"
)

func runEEPROMWipe(out io.Writer, port string) error {
	return newRuntime().runEEPROMWipe(out, port)
}

func (rt cliRuntime) runEEPROMWipe(out io.Writer, port string) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if _, err := fmt.Fprintln(out, "WARNING: EEPROM is being wiped! Power down device NOW if you do not want this!"); err != nil {
		return err
	}

	platform, err := readRnodePlatform(port, serial, 5*time.Second)
	if err != nil {
		return err
	}

	state := &modeSwitchState{platform: platform, writer: serial, sleeper: rt}
	if err := state.wipeEEPROM(); err != nil {
		return err
	}
	if state.platform != romPlatformNRF52 {
		if err := state.hardReset(); err != nil {
			return err
		}
	}
	return nil
}

func readRnodePlatform(portName string, port serialPort, timeout time.Duration) (byte, error) {
	_ = portName
	state := newRnodeReadLoopState()

	if _, err := port.Write([]byte{kissFend, rnodeKISSCommandPlatform, 0x00, kissFend}); err != nil {
		_ = port.Close()
		return romPlatformAVR, err
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
				state.feedByte(res.b)
				if state.platform != 0 {
					return state.platform, nil
				}
			}
			if res.err != nil {
				return romPlatformAVR, nil
			}
		case <-deadline:
			_ = port.Close()
			return romPlatformAVR, nil
		}
	}
}
