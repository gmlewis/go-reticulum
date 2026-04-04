// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type txPowerSetterWriter interface {
	Write([]byte) (int, error)
}

type txPowerSetterState struct {
	name    string
	txpower int
	writer  txPowerSetterWriter
}

func (s *txPowerSetterState) setTXPower() error {
	command := []byte{kissFend, 0x03, byte(s.txpower), kissFend}
	return writeModeCommand(s.writer, command, "configuring TX power for "+s.name)
}
