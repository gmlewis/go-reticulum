// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type spreadingFactorSetterWriter interface {
	Write([]byte) (int, error)
}

type spreadingFactorSetterState struct {
	name            string
	spreadingFactor int
	writer          spreadingFactorSetterWriter
}

func (s *spreadingFactorSetterState) setSpreadingFactor() error {
	command := []byte{kissFend, 0x04, byte(s.spreadingFactor), kissFend}
	return writeModeCommand(s.writer, command, "configuring spreading factor for "+s.name)
}
