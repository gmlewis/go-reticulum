// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type firmwareHashSetterWriter interface {
	Write([]byte) (int, error)
}

type firmwareHashSetterState struct {
	name      string
	hashBytes []byte
	writer    firmwareHashSetterWriter
}

func (s *firmwareHashSetterState) setFirmwareHash() error {
	data := kissEscape(s.hashBytes)
	command := append([]byte{kissFend, 0x58}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending firmware hash to device")
}
