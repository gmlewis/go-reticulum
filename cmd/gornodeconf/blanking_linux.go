// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type displayBlankingSetterWriter interface {
	Write([]byte) (int, error)
}

type displayBlankingSetterState struct {
	name            string
	blankingTimeout int
	writer          displayBlankingSetterWriter
}

func (s *displayBlankingSetterState) setDisplayBlanking() error {
	command := []byte{kissFend, 0x64, byte(s.blankingTimeout), kissFend}
	return writeModeCommand(s.writer, command, "sending display blanking timeout command to device")
}
