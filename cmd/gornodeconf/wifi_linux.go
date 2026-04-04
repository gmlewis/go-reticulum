// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type wifiModeSetterWriter interface {
	Write([]byte) (int, error)
}

type wifiModeSetterState struct {
	name   string
	mode   byte
	writer wifiModeSetterWriter
}

func (s *wifiModeSetterState) setWiFiMode() error {
	command := []byte{kissFend, 0x6a, s.mode, kissFend}
	return writeModeCommand(s.writer, command, "sending wifi mode command to device")
}
