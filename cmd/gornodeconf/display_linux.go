// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type displayIntensitySetterWriter interface {
	Write([]byte) (int, error)
}

type displayIntensitySetterState struct {
	name      string
	intensity int
	writer    displayIntensitySetterWriter
}

func (s *displayIntensitySetterState) setDisplayIntensity() error {
	command := []byte{kissFend, 0x45, byte(s.intensity), kissFend}
	return writeModeCommand(s.writer, command, "sending display intensity command to device")
}
