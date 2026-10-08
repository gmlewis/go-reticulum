// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file publishes the hub's own destination hash to a file the moment the hub
// is up, so that a supervisor which has to tell a bot where to dial does not have
// to reimplement Reticulum's naming, or scrape a log line, or ship a hub identity
// it generated at build time.
//
// It exists because the one consumer that needs the value is a different program in
// a different language: an Android appliance starts this hub and then renders the
// bot's configuration, and the bot needs the hub's rrc.hub destination hash before
// it starts. Deriving it requires an Identity and a TransportSystem, which only the
// running hub has; the running hub therefore writes it down.

package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rrc"
)

// hubDestinationSuffix is appended to the identity path to name the file the hub
// publishes its destination hash in. It sits beside the identity because that is
// the one path a supervisor already knows: it had to name the identity in order to
// start the hub at all.
const hubDestinationSuffix = ".rrc.hub"

// hubDestinationPath is where a hub with this identity publishes its destination.
func hubDestinationPath(identityPath string) string {
	return identityPath + hubDestinationSuffix
}

// effectiveIdentityPath is the identity the hub actually loaded, which is not always the
// one the command line named: the TOML configuration is applied over the path seeds, so
// `identity_path` in the file wins over --identity. Publishing beside the flag's value when
// the file said otherwise would put the destination where the operator is not looking.
func effectiveIdentityPath(cfg rrc.HubConfig, fallback string) string {
	if cfg.IdentityPath != nil && strings.TrimSpace(*cfg.IdentityPath) != "" {
		return *cfg.IdentityPath
	}
	return fallback
}

// publishHubDestination writes the hub's rrc.hub destination hash, in lowercase
// hexadecimal, where a supervisor can read it. The file is written whole and
// replaced, never appended to, so a reader never sees half a hash.
//
// A failure to write is reported but not fatal: the hub's job is to serve chat, and
// a supervisor that cannot read the hash has lost a convenience, not the hub.
func publishHubDestination(identityPath string, destinationHash []byte) error {
	if identityPath == "" {
		return nil
	}
	path := hubDestinationPath(identityPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("publishing hub destination: %w", err)
	}
	text := strings.ToLower(hex.EncodeToString(destinationHash)) + "\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fmt.Errorf("publishing hub destination to %v: %w", path, err)
	}
	return nil
}
