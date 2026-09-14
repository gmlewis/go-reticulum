// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rrc

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Test peer hashes. H1 and H2 share both a nick and a six-hex prefix; H4 and
// the prefix-only H5 share a six-hex prefix; H6 exercises a five-character
// non-hex nick, which must NOT be mistaken for a too-short hash prefix.
const (
	resolveH1 = "aaaaaa11111111111111111111111111"
	resolveH2 = "aaaaaa22222222222222222222222222"
	resolveH3 = "bbbbbb33333333333333333333333333"
	resolveH4 = "cccccc44444444444444444444444444"
	resolveH5 = "cccccc123456"
	resolveH6 = "dddddd55555555555555555555555555"
)

// resolveTestHub builds a hub whose membership index carries the ambiguity and
// prefix cases the resolver must handle.
func resolveTestHub(t *testing.T) *RRCHub {
	t.Helper()
	_, hub := newHookTestHub(t)
	own := hexString(hub.Manager.identityHash())

	hub.lock.Lock()
	hub.Members["general"] = map[string]bool{
		resolveH1: true,
		resolveH2: true,
		resolveH3: true,
		resolveH4: true,
		resolveH5: true,
		resolveH6: true,
		own:       true,
	}
	hub.Nicks = map[string]string{
		resolveH1: "Alice",
		resolveH2: "Alice",
		resolveH3: "Bob",
		resolveH4: "Carol",
		resolveH5: "Dave",
		resolveH6: "glenn",
		own:       "TestNick",
	}
	hub.lock.Unlock()
	return hub
}

// TestResolvePeerToken covers the token forms a command may receive.
func TestResolvePeerToken(t *testing.T) {
	t.Parallel()

	hub := resolveTestHub(t)
	own := hexString(hub.Manager.identityHash())

	tests := []struct {
		name       string
		token      string
		wantHash   string
		wantNick   string
		prefixOnly bool
		wantErr    error
		errSubstr  string
	}{
		{name: "nick exact", token: "Bob", wantHash: resolveH3, wantNick: "Bob"},
		{name: "nick lowercased", token: "bob", wantHash: resolveH3, wantNick: "Bob"},
		{name: "nick with mixed case", token: "cArOl", wantHash: resolveH4, wantNick: "Carol"},
		{name: "nick five non-hex characters", token: "glenn", wantHash: resolveH6, wantNick: "glenn"},
		{name: "six hex prefix", token: "bbbbbb", wantHash: resolveH3, wantNick: "Bob"},
		{name: "full hash", token: resolveH4, wantHash: resolveH4, wantNick: "Carol"},
		{name: "full hash uppercase", token: strings.ToUpper(resolveH4), wantHash: resolveH4, wantNick: "Carol"},
		{name: "our own hash", token: own, wantHash: own, wantNick: "TestNick"},
		{name: "our own nick", token: "TestNick", wantHash: own, wantNick: "TestNick"},
		{name: "prefix-only member", token: "cccccc1", wantHash: resolveH5, wantNick: "Dave", prefixOnly: true},

		{
			name: "duplicate nick is ambiguous", token: "Alice", wantErr: ErrPeerAmbiguous,
			errSubstr: "Alice",
		},
		{
			name: "prefix shared by two hashes is ambiguous", token: "aaaaaa", wantErr: ErrPeerAmbiguous,
			errSubstr: resolveH1[:12],
		},
		{
			name: "prefix shared with a prefix-only member is ambiguous", token: "cccccc", wantErr: ErrPeerAmbiguous,
			errSubstr: "Dave",
		},
		{
			name: "five hex prefix is too short", token: "bbbbb", wantErr: ErrPeerTokenTooShort,
			errSubstr: "6",
		},
		{
			name: "four hex prefix is too short", token: "aaaa", wantErr: ErrPeerTokenTooShort,
			errSubstr: "6",
		},
		{name: "empty token", token: "", wantErr: ErrPeerTokenEmpty},
		{name: "whitespace-only token", token: "   ", wantErr: ErrPeerTokenEmpty},
		{name: "unknown nick", token: "Nobody", wantErr: ErrPeerNotFound},
		{name: "unknown full hash", token: strings.Repeat("ff", 16), wantErr: ErrPeerNotFound},
		{name: "unknown six hex prefix", token: "eeeeee", wantErr: ErrPeerNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := hub.ResolvePeerToken(tt.token)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolvePeerToken(%q) error = %v, want %v", tt.token, err, tt.wantErr)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePeerToken(%q) error = %v, want nil", tt.token, err)
			}
			if got.HashHex != tt.wantHash {
				t.Errorf("HashHex = %q, want %q", got.HashHex, tt.wantHash)
			}
			if got.Nick != tt.wantNick {
				t.Errorf("Nick = %q, want %q", got.Nick, tt.wantNick)
			}
			if tt.prefixOnly {
				if got.Resolvable() {
					t.Errorf("Resolvable() = true for the truncated hash %q, want false", tt.wantHash)
				}
				if len(got.Hash) != 0 {
					t.Errorf("Hash = %v, want empty for a truncated hash", hex.EncodeToString(got.Hash))
				}
				return
			}
			if !got.Resolvable() {
				t.Errorf("Resolvable() = false for %q, want true", tt.wantHash)
			}
			if hex.EncodeToString(got.Hash) != tt.wantHash {
				t.Errorf("Hash = %v, want %v", hex.EncodeToString(got.Hash), tt.wantHash)
			}
		})
	}
}

// TestResolvePeerTokenPrefixOnlyTargetIsNotResolvable asserts a member known
// only by a truncated hash resolves for display but is honestly reported as
// unusable as a direct-notice destination.
func TestResolvePeerTokenPrefixOnlyTargetIsNotResolvable(t *testing.T) {
	t.Parallel()

	hub := resolveTestHub(t)
	got, err := hub.ResolvePeerToken("Dave")
	if err != nil {
		t.Fatalf("ResolvePeerToken(Dave) error = %v", err)
	}
	if got.HashHex != resolveH5 {
		t.Errorf("HashHex = %q, want %q", got.HashHex, resolveH5)
	}
	if got.Resolvable() {
		t.Error("Resolvable() = true for a twelve-hex member, want false")
	}
	if len(got.Hash) != 0 {
		t.Errorf("Hash = %v, want empty for a truncated hash", hex.EncodeToString(got.Hash))
	}
}

// TestResolvePeerTokenAmbiguousErrorListsCandidates asserts the ambiguity error
// names every candidate with its nick, using the hub's own "ambiguous" wording,
// in a stable order.
func TestResolvePeerTokenAmbiguousErrorListsCandidates(t *testing.T) {
	t.Parallel()

	hub := resolveTestHub(t)
	_, err := hub.ResolvePeerToken("Alice")
	if err == nil {
		t.Fatal("ResolvePeerToken(Alice) = nil error, want ambiguity")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ambiguous") {
		t.Errorf("error = %q, want it to say ambiguous", msg)
	}
	for _, want := range []string{
		resolveH1[:12], resolveH2[:12], "Alice",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error = %q, want it to contain %q", msg, want)
		}
	}

	// Determinism: the candidate order must not depend on map iteration.
	_, second := hub.ResolvePeerToken("Alice")
	if second == nil || second.Error() != msg {
		t.Errorf("second ambiguity error = %v, want the same text as %q", second, msg)
	}
}

// TestResolvePeerTokenEmptyIndex asserts an unknown token on a hub that has
// learned nobody reports not-found rather than an empty target.
func TestResolvePeerTokenEmptyIndex(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got, err := hub.ResolvePeerToken("anyone")
	if !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("error = %v, want ErrPeerNotFound", err)
	}
	if got.HashHex != "" || len(got.Hash) != 0 {
		t.Errorf("target = %+v, want the zero value on error", got)
	}
}
