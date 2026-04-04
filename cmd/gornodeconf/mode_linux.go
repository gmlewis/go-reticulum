// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"time"
)

const romPlatformESP32 = 0x80

type modeSwitchWriter interface {
	Write([]byte) (int, error)
}

type modeSwitchSleeper interface {
	Sleep(time.Duration)
}

type modeSwitchState struct {
	platform byte
	writer   modeSwitchWriter
	sleeper  modeSwitchSleeper
}

func rnodeSetNormalMode(writer modeSwitchWriter) error {
	return writeModeCommand(writer, []byte{kissFend, 0x54, 0x00, kissFend}, "configuring device mode")
}

func (s *modeSwitchState) setTNCMode() error {
	if err := writeModeCommand(s.writer, []byte{kissFend, 0x53, 0x00, kissFend}, "configuring device mode"); err != nil {
		return err
	}
	if s.platform == romPlatformESP32 {
		if err := s.hardReset(); err != nil {
			return err
		}
	}
	return nil
}

func (s *modeSwitchState) hardReset() error {
	if err := writeModeCommand(s.writer, []byte{kissFend, 0x55, 0xf8, kissFend}, "restarting device"); err != nil {
		return err
	}
	s.sleeper.Sleep(2 * time.Second)
	return nil
}

func writeModeCommand(writer modeSwitchWriter, command []byte, action string) error {
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while %v", action)
	}
	return nil
}
