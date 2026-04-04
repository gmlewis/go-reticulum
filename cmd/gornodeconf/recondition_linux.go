// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type displayReconditionSetterWriter interface {
	Write([]byte) (int, error)
}

type displayReconditionSetterState struct {
	name   string
	writer displayReconditionSetterWriter
}

func (s *displayReconditionSetterState) reconditionDisplay() error {
	command := []byte{kissFend, 0x68, 0x01, kissFend}
	return writeModeCommand(s.writer, command, "sending display recondition command to device")
}
