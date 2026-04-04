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

type leaveCommandWriter interface {
	Write([]byte) (int, error)
}

type leaveCommandSleeper interface {
	Sleep(time.Duration)
}

func rnodeLeave(writer leaveCommandWriter, sleeper leaveCommandSleeper) error {
	command := []byte{kissFend, 0x0a, 0xff, kissFend}
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while sending host left command to device")
	}
	sleeper.Sleep(time.Second)
	return nil
}
