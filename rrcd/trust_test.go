// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"os"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// G5.1: vectors captured from Python HubService._parse_identity_hash.
func TestParseIdentityHash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in      string
		want    string // hex-encoded
		wantErr string
	}{
		{in: "AABB", wantErr: "identity hash too short: 'AABB'"},
		{in: "  0xAABB  ", wantErr: "identity hash too short: '  0xAABB  '"},
		{in: "aa bb", wantErr: "identity hash too short: 'aa bb'"},
		{in: "aa\nbb", wantErr: "identity hash too short: 'aa\\nbb'"},
		{in: "aabb ccdd", want: "aabbccdd"},
		{in: "zz", wantErr: "invalid identity hash 'zz': non-hexadecimal number found in fromhex() arg at position 0"},
		{in: "abc", wantErr: "invalid identity hash 'abc': non-hexadecimal number found in fromhex() arg at position 3"},
		{in: "aa", wantErr: "identity hash too short: 'aa'"},
		{in: "", wantErr: "identity hash too short: ''"},
		{in: "a_b", wantErr: "invalid identity hash 'a_b': non-hexadecimal number found in fromhex() arg at position 1"},
	} {
		got, err := ParseIdentityHash(tc.in)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("ParseIdentityHash(%q) error = %v, want %q", tc.in, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseIdentityHash(%q) = %v", tc.in, err)
			continue
		}
		if hexKey(got) != tc.want {
			t.Errorf("ParseIdentityHash(%q) = %v, want %v", tc.in, hexKey(got), tc.want)
		}
	}
}

func TestTrustManagerLoadFromConfig(t *testing.T) {
	t.Parallel()
	m := NewTrustManager(TrustHooks{})

	// Non-blank entries parse; blank entries are skipped; parse errors
	// abort the load (mirroring load_from_config).
	err := m.LoadFromConfig(
		[]string{"aa bb", " 0xCCDD ", ""},
		[]string{"0xEEFF", "zz"},
	)
	// Python's set comprehension raises on the FIRST bad entry; "aa bb"
	// (2 bytes) precedes "zz" and hits the 4-byte minimum.
	if err == nil || !strings.Contains(err.Error(), "identity hash too short: 'aa bb'") {
		t.Fatalf("LoadFromConfig error = %v, want too-short error", err)
	}

	// A clean load.
	if err := m.LoadFromConfig([]string{"aabb ccdd"}, []string{"0xeeff 0011 2233"}); err != nil {
		t.Fatalf("LoadFromConfig: %v", err)
	}
	if !m.IsTrusted([]byte{0xaa, 0xbb, 0xcc, 0xdd}) {
		t.Error("trusted check failed")
	}
	if !m.IsBanned([]byte{0xee, 0xff, 0x00, 0x11, 0x22, 0x33}) {
		t.Error("banned check failed")
	}
}

func TestTrustManagerChecks(t *testing.T) {
	t.Parallel()
	m := NewTrustManager(TrustHooks{})
	if err := m.LoadFromConfig([]string{"aabb ccdd"}, []string{"ccdd ee00 1122"}); err != nil {
		t.Fatal(err)
	}
	if !m.IsTrusted([]byte{0xaa, 0xbb, 0xcc, 0xdd}) {
		t.Error("trusted check failed")
	}
	if !m.IsServerOp([]byte{0xaa, 0xbb, 0xcc, 0xdd}) {
		t.Error("IsServerOp must match trusted")
	}
	if m.IsTrusted(nil) {
		t.Error("nil hash must not be trusted")
	}
	if !m.IsBanned([]byte{0xcc, 0xdd, 0xee, 0x00, 0x11, 0x22}) {
		t.Error("banned check failed")
	}
	if m.IsBanned([]byte{0xaa, 0xbb}) {
		t.Error("trusted hash must not be banned")
	}
	if m.IsBanned(nil) {
		t.Error("nil hash must not be banned")
	}

	empty := NewTrustManager(TrustHooks{})
	if empty.IsTrusted(nil) {
		t.Error("nil must not be trusted")
	}
	if empty.IsServerOp(nil) {
		t.Error("nil must not be a server op")
	}
}

func TestTrustManagerAddRemoveBan(t *testing.T) {
	t.Parallel()
	m := NewTrustManager(TrustHooks{})
	hash := []byte{0x01}
	m.AddBan(hash)
	if !m.IsBanned(hash) {
		t.Error("AddBan did not ban")
	}
	stats := m.GetStats()
	if stats.BannedCount != 1 || stats.TrustedCount != 0 {
		t.Errorf("stats = %+v", stats)
	}
	m.RemoveBan(hash)
	if m.IsBanned(hash) {
		t.Error("RemoveBan did not unban")
	}
}

// G5.3: banned list persistence into rrcd.toml's [hub] table.
func TestPersistBannedIdentitiesToConfig(t *testing.T) {
	dir := testutils.TempDir(t, "trust-persist-")
	cfgPath := writeTemp(t, dir, "rrcd.toml", "# rrcd config\n\n[hub]\nhub_name = \"rrc\"\n\n[logging]\nlevel = \"INFO\"\n")
	var notices []string
	tm := NewTrustManager(TrustHooks{
		ConfigPath: func() string { return cfgPath },
		Notice:     func(_ *rns.Link, _, text string) { notices = append(notices, text) },
	})
	tm.AddBan([]byte{0xaa, 0xbb})
	tm.AddBan([]byte{0xcc, 0xdd})
	tm.PersistBannedIdentitiesToConfig(&rns.Link{}, "general")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `banned_identities = ["aabb", "ccdd"]`) {
		t.Fatalf("banned list wrong:\n%v", text)
	}
	if !strings.Contains(text, "hub_name = \"rrc\"") {
		t.Fatalf("untouched key lost:\n%v", text)
	}
	if len(notices) != 0 {
		t.Errorf("unexpected failure notices: %v", notices)
	}
}

// The union-merge quirk: an entry removed from the config re-imports on
// the next persist (trust.py:152-167).
func TestPersistBannedUnionMergeQuirk(t *testing.T) {
	dir := testutils.TempDir(t, "trust-union-")
	cfgPath := writeTemp(t, dir, "rrcd.toml", "[hub]\n")

	tm := NewTrustManager(TrustHooks{
		ConfigPath: func() string { return cfgPath },
	})
	tm.AddBan([]byte{0xaa, 0xbb})
	tm.PersistBannedIdentitiesToConfig(nil, "")
	if !strings.Contains(readTemp(t, cfgPath), `banned_identities = ["aabb"]`) {
		t.Fatal("persist missing")
	}

	// A second manager (fresh load) reads the file, removes the ban from
	// its live state, and re-persists: the union-merge re-imports it.
	second := NewTrustManager(TrustHooks{
		ConfigPath: func() string { return cfgPath },
	})
	second.PersistBannedIdentitiesToConfig(nil, "")

	if !strings.Contains(readTemp(t, cfgPath), "aabb") {
		t.Error("union-merge quirk must re-import the live ban")
	}
}

// readTemp reads a file for assertions.
func readTemp(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %v: %v", path, err)
	}
	return string(data)
}
