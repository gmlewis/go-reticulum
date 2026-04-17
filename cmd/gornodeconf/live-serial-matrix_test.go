// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestLiveSerialActionMatrix(t *testing.T) {
	t.Parallel()

	want := []liveSerialAction{
		{
			Flags:                   []string{"--sign", "-S", "--firmware-hash", "-H", "--get-target-firmware-hash", "-K", "--get-firmware-hash", "-L"},
			Helper:                  "resolveLivePort",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReadOnly,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--extract", "-e"},
			Helper:                  "runFirmwareExtract",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReadOnly,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--eeprom-backup"},
			Helper:                  "runEEPROMBackup",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReadOnly,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--eeprom-dump"},
			Helper:                  "runEEPROMDump",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReadOnly,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--eeprom-wipe"},
			Helper:                  "runEEPROMWipe",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyDestructive,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--rom", "-r"},
			Helper:                  "runEEPROMBootstrap",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyDestructive,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--flash", "-f"},
			Helper:                  "runFirmwareFlash",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyDestructive,
			HasLiveHardwareCoverage: false,
		},
		{
			Flags:                   []string{"--update", "-u"},
			Helper:                  "runFirmwareUpdate",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyDestructive,
			HasLiveHardwareCoverage: false,
		},
		{
			Flags:                   []string{"--get-target-firmware-hash", "-K", "--get-firmware-hash", "-L"},
			Helper:                  "runFirmwareHashReadbacks",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReadOnly,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--firmware-hash", "-H"},
			Helper:                  "runFirmwareHashSet",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReversibleWrite,
			HasLiveHardwareCoverage: true,
		},
		{
			Flags:                   []string{"--sign", "-S"},
			Helper:                  "runDeviceSigning",
			SupportedOS:             []string{"darwin", "linux"},
			Safety:                  liveSerialSafetyReversibleWrite,
			HasLiveHardwareCoverage: true,
		},
	}

	got := liveSerialActionMatrix()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("liveSerialActionMatrix() mismatch\nwant: %#v\ngot:  %#v", want, got)
	}

	for _, action := range got {
		for _, flag := range action.Flags {
			opts, _, err := parseArgs(sampleArgsForLiveSerialAction(flag))
			if err != nil {
				t.Fatalf("parseArgs(%q): %v", flag, err)
			}
			if action.Helper == "resolveLivePort" {
				if !livePortResolutionNeeded(opts) {
					t.Fatalf("%v should trigger resolveLivePort", flag)
				}
				continue
			}
			if helper := serialHelperForOptions(opts); helper != action.Helper {
				t.Fatalf("%v routed to %q, want %q", flag, helper, action.Helper)
			}
		}
	}
}

func TestLiveSerialActionMatrixCoversMainRoutes(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("ReadFile(main.go): %v", err)
	}
	got := serialHelpersFromMain(string(source))
	want := make([]string, 0, len(liveSerialActionMatrix()))
	for _, action := range liveSerialActionMatrix() {
		want = append(want, action.Helper)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("serial helpers in main.go mismatch\nwant: %v\ngot:  %v", want, got)
	}
}

func sampleArgsForLiveSerialAction(flag string) []string {
	if flag == "--firmware-hash" || flag == "-H" {
		return []string{flag, strings.Repeat("ab", 32)}
	}
	return []string{flag}
}

func livePortResolutionNeeded(opts options) bool {
	return opts.sign || opts.firmwareHash != "" || opts.getTargetFirmwareHash || opts.getFirmwareHash
}

func serialHelperForOptions(opts options) string {
	switch {
	case opts.extract:
		return "runFirmwareExtract"
	case opts.eepromBackup:
		return "runEEPROMBackup"
	case opts.eepromDump:
		return "runEEPROMDump"
	case opts.eepromWipe:
		return "runEEPROMWipe"
	case opts.rom:
		return "runEEPROMBootstrap"
	case opts.flash:
		return "runFirmwareFlash"
	case opts.update:
		return "runFirmwareUpdate"
	case opts.getTargetFirmwareHash || opts.getFirmwareHash:
		return "runFirmwareHashReadbacks"
	case opts.firmwareHash != "":
		return "runFirmwareHashSet"
	case opts.sign:
		return "runDeviceSigning"
	default:
		return ""
	}
}

func serialHelpersFromMain(source string) []string {
	start := strings.Index(source, "if port, err = rt.resolveLivePort(port, opts); err != nil {")
	end := strings.Index(source, "\n\tif port == \"\" {")
	if start < 0 || end < 0 || end <= start {
		return nil
	}

	section := source[start:end]
	re := regexp.MustCompile(`rt\.(resolveLivePort|run[A-Za-z0-9]+)\(`)
	matches := re.FindAllStringSubmatch(section, -1)
	var helpers []string
	for _, match := range matches {
		helper := match[1]
		if slices.Contains(helpers, helper) {
			continue
		}
		helpers = append(helpers, helper)
	}
	return helpers
}
