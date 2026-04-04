// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type displayRotationSetterWriter interface {
	Write([]byte) (int, error)
}

type displayRotationSetterState struct {
	name     string
	rotation int
	writer   displayRotationSetterWriter
}

func (s *displayRotationSetterState) setDisplayRotation() error {
	command := []byte{kissFend, 0x67, byte(s.rotation), kissFend}
	return writeModeCommand(s.writer, command, "sending display rotation command to device")
}
