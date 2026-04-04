// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type bluetoothStateWriter interface {
	Write([]byte) (int, error)
}

type bluetoothState struct {
	name   string
	writer bluetoothStateWriter
}

func (s *bluetoothState) enableBluetooth() error {
	command := []byte{kissFend, 0x46, 0x01, kissFend}
	return writeModeCommand(s.writer, command, "sending bluetooth enable command to device")
}

func (s *bluetoothState) disableBluetooth() error {
	command := []byte{kissFend, 0x46, 0x00, kissFend}
	return writeModeCommand(s.writer, command, "sending bluetooth disable command to device")
}

func (s *bluetoothState) bluetoothPair() error {
	command := []byte{kissFend, 0x46, 0x02, kissFend}
	return writeModeCommand(s.writer, command, "sending bluetooth pair command to device")
}
