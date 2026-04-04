// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
)

type wifiSSIDSetterWriter interface {
	Write([]byte) (int, error)
}

type wifiSSIDSetterState struct {
	name   string
	ssid   any
	writer wifiSSIDSetterWriter
}

func (s *wifiSSIDSetterState) setWiFiSSID() error {
	data, err := encodeWiFiString(s.ssid, 1, 33, "SSID")
	if err != nil {
		return err
	}
	command := append([]byte{kissFend, 0x6b}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending wifi SSID to device")
}

func encodeWiFiString(value any, minLen, maxLen int, field string) ([]byte, error) {
	if value == nil {
		return []byte{0x00}, nil
	}

	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("Invalid %v length", field)
	}

	encoded := append([]byte(text), 0x00)
	if len(encoded) < minLen || len(encoded) > maxLen {
		return nil, fmt.Errorf("Invalid %v length", field)
	}

	return kissEscape(encoded), nil
}
