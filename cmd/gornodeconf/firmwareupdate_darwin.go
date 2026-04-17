// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

type firmwareUpdateIndicatorWriter interface {
	Write([]byte) (int, error)
}

type firmwareUpdateIndicatorState struct {
	name   string
	writer firmwareUpdateIndicatorWriter
}

func (s *firmwareUpdateIndicatorState) indicateFirmwareUpdate() error {
	command := []byte{kissFend, 0x61, 0x01, kissFend}
	return writeModeCommand(s.writer, command, "sending firmware update command to device")
}
