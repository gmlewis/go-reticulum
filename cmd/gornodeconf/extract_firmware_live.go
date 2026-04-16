// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runFirmwareExtract(out io.Writer, port string, opts options) error {
	return newRuntime().runFirmwareExtract(out, port, opts)
}

func (rt cliRuntime) runFirmwareExtract(out io.Writer, port string, opts options) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	serialClosed := false
	defer func() {
		if serialClosed {
			return
		}
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if _, err := fmt.Fprintln(out, "RNode Firmware Extraction"); err != nil {
		return err
	}

	platform, err := readRnodePlatform(port, serial, 5*time.Second)
	if err != nil {
		return err
	}
	if platform != romPlatformESP32 {
		return errors.New("Firmware extraction is currently only supported on ESP32-based RNodes.")
	}

	if _, err := fmt.Fprintln(out, "Ready to extract firmware images from the RNode"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "Press enter to start the extraction process"); err != nil {
		return err
	}
	input := rt.stdin
	if input == nil {
		input = os.Stdin
	}
	if _, err := bufio.NewReader(input).ReadString('\n'); err != nil {
		return err
	}

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}
	if err := serial.Close(); err != nil {
		return err
	}
	serialClosed = true

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}
	extractedDir := filepath.Join(configDir, "extracted")
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		return err
	}

	baud := opts.baudFlash
	if baud == "" {
		baud = "921600"
	}
	esptoolPath, err := ensureRecoveryEsptoolInDir(configDir)
	if err != nil {
		return err
	}
	useNoStubFallback := false
	for _, target := range defaultExtractTargets() {
		outputPath := filepath.Join(extractedDir, target.filename)
		args := recoveryEsptoolCommandArgs(esptoolPath, port, baud, target.offset, target.size, outputPath)
		if useNoStubFallback {
			args = recoveryEsptoolNoStubCommandArgs(esptoolPath, port, baud, target.offset, target.size, outputPath)
		}
		commandName, commandArgs, err := rt.prepareRecoveryEsptoolCommand(args)
		if err != nil {
			return err
		}
		output, commandErr := rt.runCommand(commandName, commandArgs...)
		if commandErr != nil && !useNoStubFallback && shouldRetryRecoveryEsptoolNoStub(output) {
			useNoStubFallback = true
			if _, err := fmt.Fprintln(out, "Recovery helper stub failed; retrying extraction via ROM bootloader path."); err != nil {
				return err
			}
			args = recoveryEsptoolNoStubCommandArgs(esptoolPath, port, baud, target.offset, target.size, outputPath)
			commandName, commandArgs, err = rt.prepareRecoveryEsptoolCommand(args)
			if err != nil {
				return err
			}
			output, commandErr = rt.runCommand(commandName, commandArgs...)
		}
		if commandErr != nil {
			return recoveryEsptoolCommandError(args, output)
		}
	}

	versionPath := filepath.Join(extractedDir, "extracted_rnode_firmware.version")
	if err := os.WriteFile(versionPath, []byte(hex.EncodeToString(snapshot.firmwareHash)), 0o644); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Firmware successfully extracted!"); err != nil {
		return err
	}
	return nil
}

func shouldRetryRecoveryEsptoolNoStub(output []byte) bool {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "Wrong --chip argument?") {
		return true
	}
	return strings.Contains(trimmed, "Guru Meditation Error") && strings.Contains(trimmed, "Invalid head of packet")
}

func recoveryEsptoolCommandError(args []string, output []byte) error {
	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		return fmt.Errorf("The extraction failed, the following command did not complete successfully:\n%v\n%v", strings.Join(args, " "), trimmed)
	}
	return fmt.Errorf("The extraction failed, the following command did not complete successfully:\n%v", strings.Join(args, " "))
}
