// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "fmt"

type wifiChannelSetterWriter interface {
	Write([]byte) (int, error)
}

type wifiChannelSetterState struct {
	name    string
	channel int
	writer  wifiChannelSetterWriter
}

func (s *wifiChannelSetterState) setWiFiChannel() error {
	if s.channel < 1 || s.channel > 14 {
		return fmt.Errorf("Invalid WiFi channel")
	}
	data := kissEscape([]byte{byte(s.channel)})
	command := append([]byte{kissFend, 0x6e}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending wifi channel to device")
}
