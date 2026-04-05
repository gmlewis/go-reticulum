// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "fmt"

// flasherCommandArgs returns the fixed command prefix used to flash a device
// for the supported platform families.
func flasherCommandArgs(platform, model byte, port, baudFlash string) ([]string, error) {
	switch platform {
	case equivalencePlatformESP32:
		if baudFlash == "" {
			baudFlash = "921600"
		}
		return []string{
			"esptool.py",
			"--chip", "esp32",
			"--port", port,
			"--baud", baudFlash,
			"--before", "default_reset",
			"--after", "hard_reset",
			"write_flash", "-z",
		}, nil
	case romPlatformAVR:
		switch model {
		case 0xa4:
			return []string{
				"avrdude",
				"-P", port,
				"-p", "m1284p",
				"-c", "arduino",
				"-b", "115200",
			}, nil
		case 0xa9:
			return []string{
				"avrdude",
				"-P", port,
				"-p", "atmega2560",
				"-c", "wiring",
				"-D",
				"-b", "115200",
			}, nil
		default:
			return nil, fmt.Errorf("unsupported AVR model %v", model)
		}
	default:
		return nil, fmt.Errorf("unsupported platform %v", platform)
	}
}
