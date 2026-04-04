// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"strconv"
	"strings"
)

type wifiIPSetterWriter interface {
	Write([]byte) (int, error)
}

type wifiIPSetterState struct {
	name   string
	ip     any
	writer wifiIPSetterWriter
}

func (s *wifiIPSetterState) setWiFiIP() error {
	ipData, err := encodeWiFiIPv4(s.ip)
	if err != nil {
		return err
	}
	data := kissEscape(ipData)
	command := append([]byte{kissFend, 0x84}, data...)
	command = append(command, kissFend)
	return writeModeCommand(s.writer, command, "sending wifi IP address to device")
}

func encodeWiFiIPv4(value any) ([]byte, error) {
	if value == nil {
		return []byte{0x00, 0x00, 0x00, 0x00}, nil
	}

	address, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("Invalid IP address")
	}

	parts := strings.Split(address, ".")
	if len(parts) != 4 {
		return nil, fmt.Errorf("Invalid IP address length")
	}

	result := make([]byte, 0, 4)
	for _, part := range parts {
		number, parseErr := strconv.Atoi(part)
		if parseErr != nil {
			return nil, fmt.Errorf("Could not decode IP address octet: %v", parseErr)
		}
		if number < 0 || number > 255 {
			return nil, fmt.Errorf("Could not decode IP address octet: Invalid IP octet value")
		}
		result = append(result, byte(number))
	}

	return result, nil
}
