// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"io"
	"time"
)

var errReadHashesTimeout = errors.New("timed out while reading device hashes")

type rnodeHashSnapshot struct {
	deviceHash         []byte
	firmwareHashTarget []byte
	firmwareHash       []byte
}

func captureRnodeHashes(port serialPort, timeout time.Duration) (rnodeHashSnapshot, error) {
	state := newRnodeReadLoopState()
	byteCh := make(chan byte, 128)
	errCh := make(chan error, 1)
	readDone := make(chan struct{})

	go func() {
		defer close(readDone)
		buf := make([]byte, 1)
		for {
			n, err := port.Read(buf)
			if n > 0 {
				byteCh <- buf[0]
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					errCh <- err
				}
				return
			}
		}
	}()

	if _, err := port.Write(rnodeDetectCommand()); err != nil {
		_ = port.Close()
		return rnodeHashSnapshot{}, err
	}

	deadline := time.After(timeout)
	for {
		if len(state.deviceHash) == 32 && len(state.firmwareHashTarget) == 32 && len(state.firmwareHash) == 32 {
			return rnodeHashSnapshot{
				deviceHash:         append([]byte(nil), state.deviceHash...),
				firmwareHashTarget: append([]byte(nil), state.firmwareHashTarget...),
				firmwareHash:       append([]byte(nil), state.firmwareHash...),
			}, nil
		}

		select {
		case b := <-byteCh:
			state.feedByte(b)
		case err := <-errCh:
			_ = port.Close()
			return rnodeHashSnapshot{}, err
		case <-deadline:
			_ = port.Close()
			return rnodeHashSnapshot{}, errReadHashesTimeout
		case <-readDone:
			for len(byteCh) > 0 {
				state.feedByte(<-byteCh)
			}
			if len(state.deviceHash) == 32 && len(state.firmwareHashTarget) == 32 && len(state.firmwareHash) == 32 {
				return rnodeHashSnapshot{
					deviceHash:         append([]byte(nil), state.deviceHash...),
					firmwareHashTarget: append([]byte(nil), state.firmwareHashTarget...),
					firmwareHash:       append([]byte(nil), state.firmwareHash...),
				}, nil
			}
			_ = port.Close()
			return rnodeHashSnapshot{}, errors.New("device closed the serial port before returning hashes")
		}
	}
}
