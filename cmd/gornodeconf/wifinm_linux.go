// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type wifiNetmaskSetterWriter interface {
	Write([]byte) (int, error)
}

type wifiNetmaskSetterState struct {
	name   string
	nm     any
	writer wifiNetmaskSetterWriter
}

func (s *wifiNetmaskSetterState) setWiFiNM() error {
	nmData, err := encodeWiFiIPv4(s.nm)
	if err != nil {
		return err
	}
	data := kissEscape(nmData)
	command := append([]byte{kissFend, 0x85}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending wifi netmask to device")
}
