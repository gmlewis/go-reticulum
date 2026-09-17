// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file reads the room history the bot itself has persisted. The bot writes
// history (the RRC client appends every room row to a per-room CBOR file under
// the storage directory) but deliberately never loads the manager's
// persistence: the hubs, their rooms and their nicks come from the configuration
// file, so editing the configuration always takes effect on the next start.
//
// Commands that answer "what did I miss" or "where was that said" therefore read
// those files directly. The reads are strictly read-only, bounded in bytes and
// rows, and never touch the client's in-memory state, so a large or truncated
// history file can slow one answer but can never corrupt a session.

package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rrc"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

const (
	// historyDirName is the directory the RRC client keeps per-room history
	// files in, directly under the configured storage directory.
	historyDirName = "rrc_history"
	// maxHistoryScanBytes bounds how much of one history file a command reads,
	// so a grown-without-limit file cannot stall the command layer.
	maxHistoryScanBytes = 4 << 20
	// maxHistoryScanRows bounds how many rows a command decodes from one file.
	maxHistoryScanRows = 50000
)

// historyStore reads one hub's persisted room history. The zero value is usable
// and reads nothing, which is what a bot with no configured storage directory
// gets: the commands then answer from the live buffers alone.
type historyStore struct {
	// dir is the per-hub history directory, empty when it could not be located.
	dir string
}

// newHistoryStore locates the history directory of one hub. The directory is
// keyed by the hub's destination hash, with a "__<hash>" suffix when the hub
// carries a non-default destination name, so the exact directory is tried first
// and a prefix match is the fallback.
func newHistoryStore(storageDir, hubHashHex string) *historyStore {
	if storageDir == "" || hubHashHex == "" {
		return &historyStore{}
	}
	root := filepath.Join(storageDir, historyDirName)
	exact := filepath.Join(root, hubHashHex)
	if isDir(exact) {
		return &historyStore{dir: exact}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return &historyStore{}
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if name == hubHashHex || strings.HasPrefix(name, hubHashHex+"__") {
			return &historyStore{dir: filepath.Join(root, name)}
		}
	}
	return &historyStore{}
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// roomFile returns the history file for one room, or "" when there is none.
// The client names a file "<sanitized room>_<sha256(room)[:4] hex>.log", so the
// eight-hex-character digest suffix identifies the room even when the room name
// sanitizes to something else or to nothing at all.
func (s *historyStore) roomFile(room string) string {
	if s == nil || s.dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalizeRoom(room)))
	suffix := hex.EncodeToString(sum[:4])
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == suffix+".log" || strings.HasSuffix(name, "_"+suffix+".log") {
			return filepath.Join(s.dir, name)
		}
	}
	return ""
}

// newest reads the last rows of one room's history file, oldest first, which is
// the order the client appended them in. It returns at most limit rows, stops at
// the first decode error (keeping the valid prefix, exactly as loading history
// does), and reads nothing when there is no history for the room.
func (s *historyStore) newest(room string, limit int) []*rrc.RRCMessage {
	if limit <= 0 || !persistableRoomName(room) {
		return nil
	}
	path := s.roomFile(room)
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	rows := make([]*rrc.RRCMessage, 0, limit)
	dec := cbor.NewDecoder(io.LimitReader(f, maxHistoryScanBytes))
	for range maxHistoryScanRows {
		val, err := dec.Decode()
		if err != nil {
			break
		}
		entry := historyEntryMap(val)
		if entry == nil {
			continue
		}
		msg := rrc.DecodeHistoryEntry(entry)
		if msg == nil {
			continue
		}
		if msg.Room == "" {
			// The room is implicit in the file name.
			msg.Room = room
		}
		rows = append(rows, msg)
		if len(rows) > limit {
			rows = rows[len(rows)-limit:]
		}
	}
	return rows
}

// historyEntryMap normalizes one decoded CBOR value into the string-keyed map
// DecodeHistoryEntry expects. The decoder can hand back its own Map type or
// either map flavor, depending on the encoded key type.
func historyEntryMap(val any) map[string]any {
	switch m := val.(type) {
	case *cbor.Map:
		return m.ToStringMap()
	case map[string]any:
		return m
	case map[any]any:
		entry := make(map[string]any, len(m))
		for k, v := range m {
			if key, ok := k.(string); ok {
				entry[key] = v
			}
		}
		return entry
	default:
		return nil
	}
}

// persistableRoomName mirrors the client's own rule: only a real, named room has
// a history file, never the "*" catch-all.
func persistableRoomName(room string) bool {
	return room != "" && room != "*"
}
