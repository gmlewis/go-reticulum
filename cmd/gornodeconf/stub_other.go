// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package main

import (
	"fmt"
	"io"
)

// rnodeKISSCommandROMRead is the KISS command byte for ROM read operations.
// This is a stub for non-Linux platforms.
const rnodeKISSCommandROMRead byte = 0x00

// bootstrapChecksumSigner is an interface for signing bootstrap checksums.
// This is a stub for non-Linux platforms.
type bootstrapChecksumSigner interface {
	Sign([]byte) ([]byte, error)
}

// loadBootstrapSigner loads the bootstrap signing key from the config directory.
// This is a stub for non-Linux platforms.
func loadBootstrapSigner(configDir string) (bootstrapChecksumSigner, error) {
	return nil, fmt.Errorf("bootstrap signing not supported on platform %v", getPlatform())
}

// handlePublicKeys handles the --public flag for displaying public key information.
// This is a stub for non-Linux platforms.
func (rt cliRuntime) handlePublicKeys() error {
	return fmt.Errorf("public key operations not supported on platform %v", getPlatform())
}

// handleGenerateKeys handles the --key flag for generating keys.
// This is a stub for non-Linux platforms.
func (rt cliRuntime) handleGenerateKeys(autoinstall bool) error {
	return fmt.Errorf("key generation not supported on platform %v", getPlatform())
}

// runEEPROMBootstrap handles the --rom flag for EEPROM bootstrap operations.
// This is a stub for non-Linux platforms.
func (rt cliRuntime) runEEPROMBootstrap(port io.Writer, devicePath string, opts options) error {
	return fmt.Errorf("EEPROM bootstrap operations not supported on platform %v", getPlatform())
}

// getPlatform returns the current platform name for error messages.
func getPlatform() string {
	return "non-Linux"
}
