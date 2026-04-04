// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type frequencySetterWriter interface {
	Write([]byte) (int, error)
}

type frequencySetterState struct {
	name      string
	frequency int
	writer    frequencySetterWriter
}

func (s *frequencySetterState) setFrequency() error {
	payload := []byte{
		byte(s.frequency >> 24),
		byte(s.frequency >> 16),
		byte(s.frequency >> 8),
		byte(s.frequency),
	}
	command := append([]byte{kissFend, 0x01}, kissEscape(payload)...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "configuring frequency for "+s.name)
}
