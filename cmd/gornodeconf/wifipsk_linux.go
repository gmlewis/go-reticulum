// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type wifiPSKSetterWriter interface {
	Write([]byte) (int, error)
}

type wifiPSKSetterState struct {
	name   string
	psk    any
	writer wifiPSKSetterWriter
}

func (s *wifiPSKSetterState) setWiFiPSK() error {
	data, err := encodeWiFiString(s.psk, 8, 33, "psk")
	if err != nil {
		return err
	}
	command := append([]byte{kissFend, 0x6c}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending wifi PSK to device")
}
