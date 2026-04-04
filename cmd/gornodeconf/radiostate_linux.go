// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type radioStateSetterWriter interface {
	Write([]byte) (int, error)
}

type radioStateSetterState struct {
	name   string
	state  byte
	writer radioStateSetterWriter
}

func (s *radioStateSetterState) setRadioState() error {
	command := []byte{kissFend, 0x06, s.state, kissFend}
	return writeModeCommand(s.writer, command, "configuring radio state for "+s.name)
}
