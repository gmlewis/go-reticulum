// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the small JSON state-file helpers the field-assistant stores
// share. The stores keep distress beacons, check-in timers, and situation
// reports across restarts, and all three need the same two properties: a write
// must never leave a half-written file behind, because a mesh node can lose
// power at any moment, and a read must be bounded, because the file is on a
// disk the bot does not control.
//
// The write is therefore a temporary file in the same directory followed by a
// rename, which is atomic on every platform this bot runs on: a reader sees
// either the whole previous file or the whole new one.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// State-file bounds and modes.
const (
	// stateFileMode is the mode a state file is created with. The contents name
	// people and places, so it is readable only by the bot's own user.
	stateFileMode = 0o600
	// stateDirMode is the mode a state directory is created with.
	stateDirMode = 0o700
	// maxStateBytes bounds how much of a state file is read back, so a
	// corrupted or hostile file cannot exhaust memory at startup.
	maxStateBytes = 4 << 20
)

// writeJSONState atomically writes v as JSON to path. An empty path is a no-op,
// which is how a bot with no configured storage directory keeps its state in
// memory only.
func writeJSONState(path string, v any) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, stateDirMode); err != nil {
		return fmt.Errorf("creating %v: %w", dir, err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %v: %w", path, err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("creating a temporary state file in %v: %w", dir, err)
	}
	tempName := temp.Name()
	cleanup := func() { _ = os.Remove(tempName) }
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		cleanup()
		return fmt.Errorf("writing %v: %w", tempName, err)
	}
	// The rename is only atomic once the bytes are on the disk, so the file is
	// synced before it is closed.
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		cleanup()
		return fmt.Errorf("syncing %v: %w", tempName, err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing %v: %w", tempName, err)
	}
	if err := os.Chmod(tempName, stateFileMode); err != nil {
		cleanup()
		return fmt.Errorf("setting the mode of %v: %w", tempName, err)
	}
	if err := os.Rename(tempName, path); err != nil {
		cleanup()
		return fmt.Errorf("replacing %v: %w", path, err)
	}
	return nil
}

// readJSONState reads path into v. A file that does not exist yet is not an
// error and reports false, which is the state a store starts in. A file that is
// unreadable or malformed is an error, and the caller decides whether to start
// empty rather than crash.
func readJSONState(path string, v any) (bool, error) {
	if path == "" {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("opening %v: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxStateBytes))
	if err != nil {
		return false, fmt.Errorf("reading %v: %w", path, err)
	}
	if len(data) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("decoding %v: %w", path, err)
	}
	return true, nil
}
