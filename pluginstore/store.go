// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package pluginstore provides the per-plugin scratch key/value store that
// backs the sandboxed wasm plugin hosts' rns.kv_get and rns.kv_set host
// imports (wago-analysis.md §8.4).
//
// Every plugin receives its own directory scoped under the host's plugin
// data root — <root>/<plugin>/ — where <plugin> is the sanitized plugin name
// (typically the .wasm file's base name). Keys become file names inside that
// directory, so path separators, traversal, and oversized keys are rejected;
// the total size of a plugin's store is capped at MaxStoreBytes (10 MiB).
// The store is pure stdlib and carries no wasm dependency, so any tool may
// wire it into its plugin host.
package pluginstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MaxStoreBytes is the default total per-plugin store size cap (10 MiB).
const MaxStoreBytes = 10 << 20

// maxKeyBytes is the longest accepted key (filename length safety).
const maxKeyBytes = 128

// ErrInvalidName reports an unsanitary plugin name.
var ErrInvalidName = errors.New("invalid plugin name")

// ErrInvalidKey reports an unsanitary store key.
var ErrInvalidKey = errors.New("invalid store key")

// Store is one plugin's scoped scratch store.
type Store struct {
	dir   string
	quota int64
}

// New validates the plugin name, creates the scoped directory
// <baseDir>/<plugin>, and returns its store. The plugin name may contain
// only letters, digits, dots, dashes, and underscores.
func New(baseDir, pluginName string) (*Store, error) {
	if !validName(pluginName) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, pluginName)
	}
	dir := filepath.Join(baseDir, pluginName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create plugin store dir %q: %w", dir, err)
	}
	return &Store{dir: dir, quota: MaxStoreBytes}, nil
}

// Dir returns the store's scoped directory path.
func (s *Store) Dir() string {
	return s.dir
}

// Get returns the value stored under key, reporting whether it existed.
func (s *Store) Get(key string) ([]byte, bool, error) {
	if !validKey(key) {
		return nil, false, fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("plugin store read %q: %w", key, err)
	}
	return data, true, nil
}

// Set stores value under key. The per-plugin total-size quota counts all
// stored files with the target key's previous content replaced, so
// overwrites never double count.
func (s *Store) Set(key string, value []byte) error {
	if !validKey(key) {
		return fmt.Errorf("%w: %q", ErrInvalidKey, key)
	}
	used, err := s.usedBytes(key)
	if err != nil {
		return err
	}
	if used+int64(len(value)) > s.quota {
		return fmt.Errorf("plugin store quota exceeded: %d + %d bytes > %d bytes", used, len(value), s.quota)
	}
	if err := os.WriteFile(filepath.Join(s.dir, key), value, 0o600); err != nil {
		return fmt.Errorf("plugin store write %q: %w", key, err)
	}
	return nil
}

// usedBytes sums the sizes of every stored file except the key about to be
// written.
func (s *Store) usedBytes(exceptKey string) (int64, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, fmt.Errorf("plugin store read %q: %w", s.dir, err)
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == exceptKey {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total, nil
}

// validName reports whether pluginName is safe to use as a directory name.
func validName(pluginName string) bool {
	if pluginName == "" || pluginName == "." || pluginName == ".." {
		return false
	}
	if strings.ContainsAny(pluginName, `/\`) || strings.ContainsRune(pluginName, 0) {
		return false
	}
	for _, r := range pluginName {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// validKey reports whether key is safe to use as a filename inside the
// scoped store directory: no separators, traversal, NUL, or control
// characters (which would make hostile filenames), and at most
// maxKeyBytes long.
func validKey(key string) bool {
	if key == "" || key == "." || key == ".." || len(key) > maxKeyBytes {
		return false
	}
	if strings.ContainsAny(key, `/\`) || strings.ContainsRune(key, 0) {
		return false
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
