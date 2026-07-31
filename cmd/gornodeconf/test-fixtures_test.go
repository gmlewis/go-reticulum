// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

type liveHashSerial struct {
	mu           sync.Mutex
	reads        []byte
	writes       [][]byte
	closed       bool
	blockOnEmpty bool
	wait         chan struct{}
	closeOnce    sync.Once
}

func (s *liveHashSerial) Read(data []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, io.EOF
	}
	if len(s.reads) == 0 {
		if s.blockOnEmpty {
			wait := s.wait
			s.mu.Unlock()
			if wait != nil {
				<-wait
			}
			return 0, io.EOF
		}
		s.mu.Unlock()
		return 0, io.EOF
	}
	data[0] = s.reads[0]
	s.reads = s.reads[1:]
	s.mu.Unlock()
	return 1, nil
}

func (s *liveHashSerial) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyData := append([]byte(nil), data...)
	s.writes = append(s.writes, copyData)
	return len(data), nil
}

func (s *liveHashSerial) Close() error {
	s.mu.Lock()
	s.closed = true
	wait := s.wait
	s.mu.Unlock()

	s.closeOnce.Do(func() {
		if wait != nil {
			close(wait)
		}
	})
	return nil
}

func validRnodeEEPROMFrame() []byte {
	eeprom := make([]byte, 0xa8)
	eeprom[0x00] = 0x03
	eeprom[0x01] = 0xa4
	eeprom[0x02] = 0x05
	eeprom[0x03] = 0x01
	eeprom[0x04] = 0x02
	eeprom[0x05] = 0x03
	eeprom[0x06] = 0x04
	eeprom[0x07] = 0x05
	eeprom[0x08] = 0x06
	eeprom[0x09] = 0x07
	eeprom[0x0a] = 0x08
	copy(eeprom[0x0b:0x1b], []byte{0x30, 0x60, 0x23, 0x43, 0x25, 0x77, 0x8c, 0x41, 0x9d, 0x48, 0xbf, 0xec, 0x0e, 0x87, 0x13, 0x71})
	for i := 0; i < 128; i++ {
		eeprom[0x1b+i] = byte(i)
	}
	eeprom[0x9b] = 0x73
	eeprom[0x9c] = 0x07
	eeprom[0x9d] = 0x05
	eeprom[0x9e] = 0x11
	eeprom[0x9f] = 0x00
	eeprom[0xa0] = 0x01
	eeprom[0xa1] = 0xe8
	eeprom[0xa2] = 0x48
	eeprom[0xa3] = 0x19
	eeprom[0xa4] = 0xcf
	eeprom[0xa5] = 0xd1
	eeprom[0xa6] = 0x90
	eeprom[0xa7] = 0x73

	frame := append([]byte{kissFend, rnodeKISSCommandROMRead}, eeprom...)
	frame = append(frame, kissFend)
	return frame
}

func runGornodeconfWithEnv(extraEnv map[string]string, args ...string) (string, error) {
	return runGornodeconfWithInputAndEnv("", extraEnv, args...)
}

func runGornodeconfWithInputAndEnv(input string, extraEnv map[string]string, args ...string) (string, error) {
	taskArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", taskArgs...)
	cmd.Dir = "."
	cmd.Env = os.Environ()
	cmd.Stdin = strings.NewReader(input)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustDecodeHex(t *testing.T, hexStr string) []byte {
	t.Helper()

	data, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return data
}
