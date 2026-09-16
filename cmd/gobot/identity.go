// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// State file names inside a Reticulum configuration directory. The persistent
// transport identity is the 64-byte private key Reticulum writes on its first
// run; it is the one private identity a Reticulum directory is guaranteed to
// hold, and it is what this tool reuses so its identity hash is stable across
// invocations.
const (
	defaultConfigDirName   = ".reticulum"
	reticulumStorageDir    = "storage"
	transportIdentityName  = "transport_identity"
	identityHashLenMessage = "the file does not hold a 64-byte Reticulum private key"
)

// reticulumConfigDir resolves the Reticulum configuration directory: an
// explicit --config path, or ~/.reticulum. It does not create anything.
func reticulumConfigDir(override string) (string, error) {
	if strings.TrimSpace(override) != "" {
		return expandUser(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate the home directory to find %v: %w",
			defaultConfigDirName, err)
	}
	return filepath.Join(home, defaultConfigDirName), nil
}

// identityCandidates returns the identity files to try, in order. An explicit
// --identity always wins and is used alone, so an operator who names a file
// gets that file or an error, never a silent fallback.
func identityCandidates(configDir, explicit string) []string {
	if strings.TrimSpace(explicit) != "" {
		path, err := expandUser(explicit)
		if err != nil {
			return []string{explicit}
		}
		return []string{path}
	}
	return []string{filepath.Join(configDir, reticulumStorageDir, transportIdentityName)}
}

// loadIdentity loads the Reticulum identity this tool will present to the hub.
// It never creates one: a missing identity is an error, not a first run,
// because generating a new identity on the fly would give the tool a different
// identity hash on every invocation and quietly defeat any reply the bot keys
// on it.
func loadIdentity(configDir, explicit string) (*rns.Identity, string, error) {
	candidates := identityCandidates(configDir, explicit)
	logger := silentLogger()
	for _, path := range candidates {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		identity, err := rns.FromFile(path, logger)
		if err != nil {
			return nil, path, fmt.Errorf(
				"could not load the Reticulum identity from %v: the file may be corrupt or truncated: %w",
				path, err)
		}
		if len(identity.Hash) != rrc.IdentityHashLen {
			return nil, path, fmt.Errorf("could not load the Reticulum identity from %v: %v",
				path, identityHashLenMessage)
		}
		return identity, path, nil
	}
	return nil, "", noIdentityError(configDir, candidates)
}

// noIdentityError explains where an identity was looked for and how to get one,
// because the fix is always to run another Reticulum tool once.
func noIdentityError(configDir string, candidates []string) error {
	if len(candidates) == 0 {
		return errors.New("no Reticulum identity file to load")
	}
	return fmt.Errorf(
		"no Reticulum identity found in %v: %v does not exist; run a Reticulum tool once "+
			"(for example gornstatus) to create one, or pass --identity",
		configDir, strings.Join(candidates, ", "))
}

// silentLogger returns a Reticulum logger that emits nothing, so identity
// loading cannot write protocol chatter into the tool's output.
func silentLogger() *rns.Logger {
	logger := rns.NewLogger()
	logger.SetLogLevel(rns.LogNone)
	return logger
}

// expandUser expands a leading ~ or ~/ in path to the user's home directory.
func expandUser(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand %q: %w", path, err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}
