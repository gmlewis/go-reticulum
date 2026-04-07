// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

func (rt cliRuntime) handlePublicKeys() error {
	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}

	firmwareDir := filepath.Join(configDir, "firmware")
	signingPath := filepath.Join(firmwareDir, "signing.key")
	devicePath := filepath.Join(firmwareDir, "device.key")

	var errs []error
	signingBytes, err := os.ReadFile(signingPath)
	if err != nil {
		errs = append(errs, fmt.Errorf("Could not load EEPROM signing key (did you run \"gornodeconf --key\"?): %w", err))
	} else {
		privateKey, err := x509.ParsePKCS8PrivateKey(signingBytes)
		if err != nil {
			return fmt.Errorf("Could not deserialize signing key: %w", err)
		} else {
			rsaKey, ok := privateKey.(*rsa.PrivateKey)
			if !ok {
				return fmt.Errorf("Could not deserialize signing key: unexpected private key type")
			} else {
				publicBytes, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
				if err != nil {
					return err
				}
				fmt.Println("EEPROM Signing Public key:")
				fmt.Println(hex.EncodeToString(publicBytes))
			}
		}
	}

	if deviceSigner, err := rns.FromFile(devicePath, rt.logger); err == nil {
		fmt.Println("")
		fmt.Println("Device Signing Public key:")
		fmt.Println(colonHex(deviceSigner.GetPublicKey()[32:]))
	} else {
		errs = append(errs, fmt.Errorf("Could not load device signing key (did you run \"gornodeconf --key\"?): %w", err))
	}

	return errors.Join(errs...)
}

func (rt cliRuntime) handleGenerateKeys(autoinstall bool) error {
	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}

	firmwareDir := filepath.Join(configDir, "firmware")
	if err := os.MkdirAll(firmwareDir, 0o755); err != nil {
		return err
	}

	if _, err := os.Stat(filepath.Join(firmwareDir, "device.key")); os.IsNotExist(err) {
		fmt.Println("Generating a new device signing key...")
		deviceSigner, err := rns.NewIdentity(true, rt.logger)
		if err != nil {
			return err
		}
		if err := deviceSigner.ToFile(filepath.Join(firmwareDir, "device.key")); err != nil {
			return fmt.Errorf("Could not write device signing key file: %w", err)
		}
		fmt.Println("Device signing key written to " + filepath.Join(firmwareDir, "device.key"))
	} else if err != nil {
		return err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return err
	}
	privateBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return err
	}
	publicBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return err
	}

	signingPath := filepath.Join(firmwareDir, "signing.key")
	if _, err := os.Stat(signingPath); os.IsNotExist(err) {
		fmt.Println("Generating a new EEPROM signing key...")
		if err := os.WriteFile(signingPath, privateBytes, 0o600); err != nil {
			return fmt.Errorf("Could not write EEPROM signing key file: %w", err)
		}
		fmt.Println("Wrote signing key")
		fmt.Println("Public key:")
		fmt.Println(hex.EncodeToString(publicBytes))
	} else if err != nil {
		return err
	} else if !autoinstall {
		fmt.Println("EEPROM Signing key already exists, not overwriting!")
		fmt.Println("Manually delete this key to create a new one.")
	}

	return nil
}

func colonHex(data []byte) string {
	parts := make([]string, 0, len(data))
	for _, b := range data {
		parts = append(parts, fmt.Sprintf("%02x", b))
	}
	return strings.Join(parts, ":")
}
