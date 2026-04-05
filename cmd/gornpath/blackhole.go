// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type blackholeMutator interface {
	BlackholedIdentities() ([]any, error)
	BlackholeIdentity([]byte, *int64, string) (bool, error)
	UnblackholeIdentity([]byte) (bool, error)
}

func doBlackhole(out io.Writer, provider blackholeMutator, identityHash []byte, durationHours float64, reason string) error {
	if alreadyBlackholed(provider, identityHash) {
		_, err := fmt.Fprintf(out, "Identity %s already blackholed\n", rns.PrettyHex(identityHash))
		return err
	}

	var until *int64
	if durationHours > 0 {
		value := time.Now().Add(time.Duration(durationHours * float64(time.Hour))).Unix()
		until = &value
	}

	ok, err := provider.BlackholeIdentity(identityHash, until, reason)
	if err != nil {
		return fmt.Errorf("Could not blackhole identity: %v", err)
	}
	if ok {
		_, writeErr := fmt.Fprintf(out, "Blackholed identity %s\n", rns.PrettyHex(identityHash))
		return writeErr
	}
	_, writeErr := fmt.Fprintf(out, "Could not blackhole identity %s\n", rns.PrettyHex(identityHash))
	return writeErr
}

func doUnblackhole(out io.Writer, provider blackholeMutator, identityHash []byte) error {
	if !alreadyBlackholed(provider, identityHash) {
		_, err := fmt.Fprintf(out, "Identity %s not blackholed\n", rns.PrettyHex(identityHash))
		return err
	}

	ok, err := provider.UnblackholeIdentity(identityHash)
	if err != nil {
		return fmt.Errorf("Could not unblackhole identity: %v", err)
	}
	if ok {
		_, writeErr := fmt.Fprintf(out, "Lifted blackhole for identity %s\n", rns.PrettyHex(identityHash))
		return writeErr
	}
	_, writeErr := fmt.Fprintf(out, "Could not unblackhole identity %s\n", rns.PrettyHex(identityHash))
	return writeErr
}

func alreadyBlackholed(provider blackholeMutator, identityHash []byte) bool {
	rows, err := provider.BlackholedIdentities()
	if err != nil {
		return false
	}
	for _, row := range rows {
		entry, ok := row.(map[string]any)
		if !ok {
			continue
		}
		hash, err := asBytes(entry["identity_hash"])
		if err != nil {
			continue
		}
		if string(hash) == string(identityHash) {
			return true
		}
	}
	return false
}
