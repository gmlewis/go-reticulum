// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"encoding/hex"
	"fmt"
	"strconv"
)

type bootstrapIdentity struct {
	product byte
	model   byte
	hwRev   byte
}

func resolveBootstrapIdentity(opts options) (bootstrapIdentity, error) {
	identity := bootstrapIdentity{product: 0x03}

	if opts.product != "" {
		value, err := resolveBootstrapByte(opts.product, map[string]byte{
			"03": 0x03,
			"10": 0x10,
			"f0": 0xf0,
			"e0": 0xe0,
		})
		if err != nil {
			return bootstrapIdentity{}, err
		}
		identity.product = value
	}

	if opts.model == "" {
		return bootstrapIdentity{}, fmt.Errorf("model is required for bootstrap")
	}
	model, err := resolveBootstrapByte(opts.model, map[string]byte{
		"11": 0x11,
		"12": 0x12,
		"a4": 0xa4,
		"a9": 0xa9,
		"a1": 0xa1,
		"a6": 0xa6,
		"e4": 0xe4,
		"e9": 0xe9,
		"ff": 0xff,
	})
	if err != nil {
		return bootstrapIdentity{}, err
	}
	identity.model = model

	if opts.hwrev < 1 || opts.hwrev > 255 {
		return bootstrapIdentity{}, fmt.Errorf("hardware revision is required for bootstrap")
	}
	identity.hwRev = byte(opts.hwrev)

	return identity, nil
}

func resolveBootstrapByte(value string, aliases map[string]byte) (byte, error) {
	if mapped, ok := aliases[value]; ok {
		return mapped, nil
	}
	if len(value) == 2 {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 1 {
			return 0, fmt.Errorf("invalid bootstrap value %q", value)
		}
		return decoded[0], nil
	}
	parsed, err := strconv.ParseUint(value, 10, 8)
	if err == nil {
		return byte(parsed), nil
	}
	return 0, fmt.Errorf("invalid bootstrap value %q", value)
}
