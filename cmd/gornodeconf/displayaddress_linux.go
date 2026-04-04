// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type displayAddressSetterWriter interface {
	Write([]byte) (int, error)
}

type displayAddressSetterState struct {
	name    string
	address int
	writer  displayAddressSetterWriter
}

func (s *displayAddressSetterState) setDisplayAddress() error {
	command := []byte{kissFend, 0x63, byte(s.address), kissFend}
	return writeModeCommand(s.writer, command, "sending display address command to device")
}
