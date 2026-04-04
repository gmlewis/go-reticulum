// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

type neopixelIntensitySetterWriter interface {
	Write([]byte) (int, error)
}

type neopixelIntensitySetterState struct {
	name      string
	intensity int
	writer    neopixelIntensitySetterWriter
}

func (s *neopixelIntensitySetterState) setNeoPixelIntensity() error {
	command := []byte{kissFend, 0x65, byte(s.intensity), kissFend}
	return writeModeCommand(s.writer, command, "sending NeoPixel intensity command to device")
}
