// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type bandwidthSetterWriter interface {
	Write([]byte) (int, error)
}

type bandwidthSetterState struct {
	name      string
	bandwidth int
	writer    bandwidthSetterWriter
}

func (s *bandwidthSetterState) setBandwidth() error {
	payload := []byte{
		byte(s.bandwidth >> 24),
		byte(s.bandwidth >> 16),
		byte(s.bandwidth >> 8),
		byte(s.bandwidth),
	}
	command := append([]byte{kissFend, 0x02}, kissEscape(payload)...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "configuring bandwidth for "+s.name)
}
