// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// TrustHooks wires a TrustManager to the hub services it needs.
type TrustHooks struct {
	// ConfigPath resolves the config file for ban persistence; nil when
	// unset (persistence then emits the no-config_path NOTICE).
	ConfigPath func() string
	// Notice emits a NOTICE on persistence failures.
	Notice func(link *rns.Link, room, text string)
}

// TrustManager manages trusted and banned identities for the hub, mirroring
// Python's TrustManager. update_from_config is intentionally not ported
// (dead code in rrcd 0.3.2).
type TrustManager struct {
	hooks TrustHooks

	mu       sync.Mutex
	trusted  map[string]bool
	banned   map[string]bool
	parseHex func(string) ([]byte, error)
}

// NewTrustManager creates a trust manager wired to the given hooks.
func NewTrustManager(hooks TrustHooks) *TrustManager {
	t := &TrustManager{
		hooks:   hooks,
		trusted: map[string]bool{},
		banned:  map[string]bool{},
	}
	t.parseHex = func(text string) ([]byte, error) {
		return ParseIdentityHash(text)
	}
	return t
}

// LoadFromConfig loads trusted and banned identities from config lists,
// mirroring load_from_config. Parse errors are returned and abort startup.
func (t *TrustManager) LoadFromConfig(trustedList, bannedList []string) error {
	trusted := map[string]bool{}
	for _, h := range trustedList {
		if strings.TrimSpace(h) == "" {
			continue
		}
		b, err := t.parseHex(h)
		if err != nil {
			return err
		}
		trusted[hexKey(b)] = true
	}
	banned := map[string]bool{}
	for _, h := range bannedList {
		if strings.TrimSpace(h) == "" {
			continue
		}
		b, err := t.parseHex(h)
		if err != nil {
			return err
		}
		banned[hexKey(b)] = true
	}
	t.trusted = trusted
	t.banned = banned
	return nil
}

// IsTrusted reports whether the peer identity is in the trusted list.
func (t *TrustManager) IsTrusted(peerHash []byte) bool {
	if len(peerHash) == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.trusted[hexKey(peerHash)]
}

// IsServerOp reports whether the peer is a server operator (currently the
// same as trusted).
func (t *TrustManager) IsServerOp(peerHash []byte) bool {
	return t.IsTrusted(peerHash)
}

// IsBanned reports whether the peer identity is in the banned list.
func (t *TrustManager) IsBanned(peerHash []byte) bool {
	if len(peerHash) == 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.banned[hexKey(peerHash)]
}

// AddBan adds a peer identity to the banned list.
func (t *TrustManager) AddBan(peerHash []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.banned[hexKey(peerHash)] = true
}

// RemoveBan removes a peer identity from the banned list.
func (t *TrustManager) RemoveBan(peerHash []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.banned, hexKey(peerHash))
}

// TrustStats holds the trust statistics for the hub stats.
type TrustStats struct {
	TrustedCount int
	BannedCount  int
}

// TrustedHexSet returns the trusted identity hashes as lowercase hex.
func (t *TrustManager) TrustedHexSet() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.trusted))
	for key := range t.trusted {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// ReplaceIdentities swaps the live trusted and banned identity sets,
// mirroring the reload's direct assignment of the parsed sets.
func (t *TrustManager) ReplaceIdentities(trusted, banned [][]byte) {
	newTrusted := map[string]bool{}
	for _, hash := range trusted {
		newTrusted[hexKey(hash)] = true
	}
	newBanned := map[string]bool{}
	for _, hash := range banned {
		newBanned[hexKey(hash)] = true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.trusted = newTrusted
	t.banned = newBanned
}

// GetStats returns statistics about trusted and banned identities.
func (t *TrustManager) GetStats() TrustStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TrustStats{TrustedCount: len(t.trusted), BannedCount: len(t.banned)}
}

// BannedHexList returns the sorted banned identity hashes as lowercase hex.
func (t *TrustManager) BannedHexList() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.banned))
	for k := range t.banned {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PersistBannedIdentitiesToConfig persists the current banned identities
// list to the config file, mirroring persist_banned_identities_to_config:
// no config path produces the "ban updated (not persisted; no
// config_path)" NOTICE, an existing list is normalized (strip, lower,
// 0x-stripped, empties skipped), union-merged with the live sorted set, and
// sorted before an in-place truncate+rewrite with the mode restored.
func (t *TrustManager) PersistBannedIdentitiesToConfig(link *rns.Link, room string) {
	if t.hooks.ConfigPath == nil {
		return
	}
	cfgPath := t.hooks.ConfigPath()
	if cfgPath == "" {
		if t.hooks.Notice != nil {
			t.hooks.Notice(link, room, "ban updated (not persisted; no config_path)")
		}
		return
	}
	if err := t.persistBannedFile(cfgPath); err != nil {
		if t.hooks.Notice != nil {
			t.hooks.Notice(link, room, fmt.Sprintf("ban updated (persist failed: %v)", err))
		}
	}
}

// persistBannedFile performs the config read-modify-write for the
// banned_identities key: stat for the mode restore, parse, [hub] creation
// when missing, normalization of the existing list, union-merge with the
// live sorted set, and an in-place truncate+rewrite with the mode restored.
func (t *TrustManager) persistBannedFile(cfgPath string) error {
	var fileMode os.FileMode
	if info, statErr := os.Stat(cfgPath); statErr == nil {
		fileMode = info.Mode()
	} else {
		return statErr
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return err
	}
	doc, err := toml.Parse(string(data))
	if err != nil {
		return err
	}

	hub := doc.TablePath("hub")
	existingList := []string{}
	if v, ok := hub.Get("banned_identities"); ok && v.Kind == toml.KindArray {
		for _, item := range v.Arr {
			if item.Kind != toml.KindString {
				continue
			}
			s := strings.ToLower(strings.TrimSpace(item.Str))
			s = strings.TrimPrefix(s, "0x")
			if s != "" {
				existingList = append(existingList, s)
			}
		}
	}

	merged := map[string]bool{}
	for _, s := range existingList {
		merged[s] = true
	}
	for _, h := range sortedHexKeys(t.banned) {
		merged[h] = true
	}
	list := make([]string, 0, len(merged))
	for k := range merged {
		list = append(list, k)
	}
	sort.Strings(list)
	hub.Set("banned_identities", toml.StringArrayValue(list))

	newText := doc.Dump()
	if err := os.WriteFile(cfgPath, []byte(newText), fileMode); err != nil {
		return err
	}
	_ = os.Chmod(cfgPath, fileMode)
	return nil
}
