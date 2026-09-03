// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import "os"

// DefaultRrcdDir returns the state directory: the RRCD_HOME env value used
// literally when truthy, otherwise ~/.rrcd.
func DefaultRrcdDir() string {
	if override := os.Getenv("RRCD_HOME"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".rrcd"
	}
	return home + "/.rrcd"
}

// DefaultConfigPath returns the default rrcd.toml path.
func DefaultConfigPath() string {
	return DefaultRrcdDir() + "/rrcd.toml"
}

// DefaultIdentityPath returns the default hub_identity path.
func DefaultIdentityPath() string {
	return DefaultRrcdDir() + "/hub_identity"
}

// DefaultRoomRegistryPath returns the default rooms.toml path.
func DefaultRoomRegistryPath() string {
	return DefaultRrcdDir() + "/rooms.toml"
}

// EnsurePrivateDir creates the directory (with parents) and tightens its
// mode to 0o700 best-effort.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o700)
	return nil
}
