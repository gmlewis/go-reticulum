// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

type bootstrapChecksumSigner interface {
	Sign([]byte) ([]byte, error)
}

type rsaBootstrapSigner struct {
	privateKey *rsa.PrivateKey
}

func (s rsaBootstrapSigner) Sign(message []byte) ([]byte, error) {
	checksum := sha256.Sum256(message)
	return rsa.SignPSS(rand.Reader, s.privateKey, crypto.SHA256, checksum[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256})
}

func loadBootstrapSigner(configDir string) (bootstrapChecksumSigner, error) {
	path := filepath.Join(configDir, "firmware", "signing.key")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Could not load EEPROM signing key (did you run \"gornodeconf --key\"?): %w", err)
	}
	privateKey, err := x509.ParsePKCS8PrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("Could not deserialize signing key: %w", err)
	}
	rsaKey, ok := privateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("Could not deserialize signing key: unexpected private key type")
	}
	return rsaBootstrapSigner{privateKey: rsaKey}, nil
}

func handleTrustKey(hexBytes string) error {
	publicBytes, err := hex.DecodeString(hexBytes)
	if err != nil {
		fmt.Println("Invalid key data supplied")
		return nil
	}

	if _, err := x509.ParsePKIXPublicKey(publicBytes); err != nil {
		fmt.Println("Could not create public key from supplied data. Check that the key format is valid.")
		fmt.Println(err)
		return nil
	}

	hash := sha256.Sum256(publicBytes)
	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}

	trustedDir := filepath.Join(configDir, "trusted_keys")
	if err := os.MkdirAll(trustedDir, 0o755); err != nil {
		return fmt.Errorf("Could not create trusted key directory: %w", err)
	}

	keyPath := filepath.Join(trustedDir, fmt.Sprintf("%x.pubkey", hash[:]))
	if err := os.WriteFile(keyPath, publicBytes, 0o644); err != nil {
		return fmt.Errorf("Could not write trusted key file: %w", err)
	}

	fmt.Printf("Trusting key: %x\n", hash[:])
	return nil
}

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

func rnodeconfConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "rnodeconf"), nil
}

func colonHex(data []byte) string {
	parts := make([]string, 0, len(data))
	for _, b := range data {
		parts = append(parts, fmt.Sprintf("%02x", b))
	}
	return strings.Join(parts, ":")
}
