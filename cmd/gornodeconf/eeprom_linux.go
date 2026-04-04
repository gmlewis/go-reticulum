// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"time"
)

type eepromDownloaderWriter interface {
	Write([]byte) (int, error)
}

type eepromDownloaderSleeper interface {
	Sleep(time.Duration)
}

type eepromDownloaderState struct {
	name   string
	eeprom []byte
	writer eepromDownloaderWriter
	sleeper eepromDownloaderSleeper
	parse  func() error
}

func (s *eepromDownloaderState) downloadEEPROM() error {
	s.eeprom = nil
	command := []byte{kissFend, 0x51, 0x00, kissFend}
	written, err := s.writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return errors.New("An IO error occurred while downloading EEPROM")
	}

	s.sleeper.Sleep(600 * time.Millisecond)
	if s.eeprom == nil {
		return errors.New("Could not download EEPROM from device. Is a valid firmware installed?")
	}
	if s.parse != nil {
		return s.parse()
	}
	return nil
}