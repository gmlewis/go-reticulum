// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "slices"

type liveSerialSafety string

const (
	liveSerialSafetyReadOnly        liveSerialSafety = "read-only"
	liveSerialSafetyReversibleWrite liveSerialSafety = "reversible-write"
	liveSerialSafetyDestructive     liveSerialSafety = "destructive"
)

type liveSerialAction struct {
	Flags                   []string
	Helper                  string
	SupportedOS             []string
	Safety                  liveSerialSafety
	HasLiveHardwareCoverage bool
}

func liveSerialActionMatrix() []liveSerialAction {
	actions := []liveSerialAction{
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

	cloned := make([]liveSerialAction, 0, len(actions))
	for _, action := range actions {
		cloned = append(cloned, liveSerialAction{
			Flags:                   slices.Clone(action.Flags),
			Helper:                  action.Helper,
			SupportedOS:             slices.Clone(action.SupportedOS),
			Safety:                  action.Safety,
			HasLiveHardwareCoverage: action.HasLiveHardwareCoverage,
		})
	}
	return cloned
}
