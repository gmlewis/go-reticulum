// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"reflect"
	"testing"
)

func TestFlasherCommandArgsESP32(t *testing.T) {
	t.Parallel()

	got, err := flasherCommandArgs(equivalencePlatformESP32, 0xa1, "ttyUSB0", "921600")
	if err != nil {
		t.Fatalf("flasherCommandArgs returned error: %v", err)
	}
	want := []string{
		"esptool.py",
		"--chip", "esp32",
		"--port", "ttyUSB0",
		"--baud", "921600",
		"--before", "default_reset",
		"--after", "hard_reset",
		"write_flash", "-z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ESP32 flasher prefix mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestFlasherCommandArgsAVR1284P(t *testing.T) {
	t.Parallel()

	got, err := flasherCommandArgs(romPlatformAVR, 0xa4, "ttyUSB0", "921600")
	if err != nil {
		t.Fatalf("flasherCommandArgs returned error: %v", err)
	}
	want := []string{
		"avrdude",
		"-P", "ttyUSB0",
		"-p", "m1284p",
		"-c", "arduino",
		"-b", "115200",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AVR 1284P flasher prefix mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestFlasherCommandArgsAVR2560(t *testing.T) {
	t.Parallel()

	got, err := flasherCommandArgs(romPlatformAVR, 0xa9, "ttyUSB0", "921600")
	if err != nil {
		t.Fatalf("flasherCommandArgs returned error: %v", err)
	}
	want := []string{
		"avrdude",
		"-P", "ttyUSB0",
		"-p", "atmega2560",
		"-c", "wiring",
		"-D",
		"-b", "115200",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AVR 2560 flasher prefix mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
