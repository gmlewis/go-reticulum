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

// flasherCommandCall returns the full flashing command for a representative
// firmware bundle layout.
func flasherCommandCall(platform, model byte, port, baudFlash, firmwareDir, fwFilename string) ([]string, error) {
	switch platform {
	case equivalencePlatformESP32:
		if baudFlash == "" {
			baudFlash = "921600"
		}
		return []string{
			"python",
			filepathJoin(firmwareDir, "recovery_esptool.py"),
			"--chip", "esp32",
			"--port", port,
			"--baud", baudFlash,
			"--before", "default_reset",
			"--after", "hard_reset",
			"write_flash", "-z",
			"--flash_mode", "dio",
			"--flash_freq", "80m",
			"--flash_size", "4MB",
			"0xe000", filepathJoin(firmwareDir, fwFilename+".boot_app0"),
			"0x1000", filepathJoin(firmwareDir, fwFilename+".bootloader"),
			"0x10000", filepathJoin(firmwareDir, fwFilename+".bin"),
			"0x210000", filepathJoin(firmwareDir, "extracted_console_image.bin"),
			"0x8000", filepathJoin(firmwareDir, fwFilename+".partitions"),
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
				"-U", "flash:w:" + filepathJoin(firmwareDir, fwFilename) + ":i",
			}, nil
		case 0xa9:
			return []string{
				"avrdude",
				"-P", port,
				"-p", "atmega2560",
				"-c", "wiring",
				"-D",
				"-b", "115200",
				"-U", "flash:w:" + filepathJoin(firmwareDir, fwFilename),
			}, nil
		default:
			return nil, fmt.Errorf("unsupported AVR model %v", model)
		}
	default:
		return nil, fmt.Errorf("unsupported platform %v", platform)
	}
}

func filepathJoin(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	joined := parts[0]
	for _, part := range parts[1:] {
		if joined == "" {
			joined = part
			continue
		}
		if part == "" {
			continue
		}
		if joined[len(joined)-1] == '/' {
			joined += part
			continue
		}
		joined += "/" + part
	}
	return joined
}
