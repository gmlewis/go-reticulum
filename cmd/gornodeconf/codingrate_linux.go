// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type codingRateSetterWriter interface {
	Write([]byte) (int, error)
}

type codingRateSetterState struct {
	name       string
	codingRate int
	writer     codingRateSetterWriter
}

func (s *codingRateSetterState) setCodingRate() error {
	command := []byte{kissFend, 0x05, byte(s.codingRate), kissFend}
	return writeModeCommand(s.writer, command, "configuring coding rate for "+s.name)
}
