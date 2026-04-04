// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package main

import "errors"

func handlePublicKeys() error {
	return errors.New("public key display is only supported on linux")
}

func handleGenerateKeys(bool) error {
	return errors.New("key generation is only supported on linux")
}
