// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type interferenceAvoidanceSetterWriter interface {
	Write([]byte) (int, error)
}

type interferenceAvoidanceSetterState struct {
	name     string
	disabled bool
	writer   interferenceAvoidanceSetterWriter
}

func (s *interferenceAvoidanceSetterState) setDisableInterferenceAvoidance() error {
	data := byte(0x00)
	if s.disabled {
		data = 0x01
	}
	command := []byte{kissFend, 0x69, data, kissFend}
	return writeModeCommand(s.writer, command, "sending interference avoidance configuration command to device")
}
