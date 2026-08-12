// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

// TestSanRefGolden checks the git ref sanitizer against golden values captured
// from Python RNS.Utilities.rngit.util.san_ref (rngit v1.4.2). A valid ref is
// returned unchanged; an invalid ref returns the empty string (mirroring
// Python's None, since a valid ref is never empty).
func TestSanRefGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string // "" means rejected (Python None)
	}{
		{name: "valid main", ref: "refs/heads/main", want: "refs/heads/main"},
		{name: "valid nested", ref: "refs/heads/feature/x", want: "refs/heads/feature/x"},
		{name: "leading dash", ref: "-refs/heads/main", want: ""},
		{name: "leading slash", ref: "/refs/heads/main", want: ""},
		{name: "trailing slash", ref: "refs/heads/main/", want: ""},
		{name: "trailing dot", ref: "refs/heads/main.", want: ""},
		{name: "space", ref: "refs/heads main", want: ""},
		{name: "no slash", ref: "headsonly", want: ""},
		{name: "double dot", ref: "refs/heads/a..b", want: ""},
		{name: "slash dot", ref: "refs/heads/./x", want: ""},
		{name: "double slash", ref: "refs/heads//x", want: ""},
		{name: "backslash", ref: "refs/heads/\\x", want: ""},
		{name: "lock component", ref: "refs/heads/x.lock", want: ""},
		{name: "tilde", ref: "refs/heads/x~y", want: ""},
		{name: "caret", ref: "refs/heads/x^y", want: ""},
		{name: "colon", ref: "refs/heads/x:y", want: ""},
		{name: "question", ref: "refs/heads/x?y", want: ""},
		{name: "asterisk", ref: "refs/heads/x*y", want: ""},
		{name: "lbracket", ref: "refs/heads/x[y", want: ""},
		{name: "at-brace", ref: "refs/heads/x@{y", want: ""},
		{name: "at within ref is valid", ref: "refs/heads/@", want: "refs/heads/@"},
		{name: "DEL char", ref: "refs/heads/x\x7fy", want: ""},
		{name: "quote below ord 40", ref: "refs/'main", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanRef(tc.ref); got != tc.want {
				t.Errorf("sanRef(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

// TestSanRefsGolden checks san_refs: a slice is returned only if every element
// is a valid ref; a non-slice (nil) or any invalid element yields nil.
func TestSanRefsGolden(t *testing.T) {
	t.Parallel()

	if got := sanRefs([]string{"refs/heads/main", "refs/tags/v1"}); got == nil || len(got) != 2 {
		t.Errorf("sanRefs(good) = %v, want [refs/heads/main refs/tags/v1]", got)
	}
	if got := sanRefs([]string{"refs/heads/main", "badref"}); got != nil {
		t.Errorf("sanRefs(mixed) = %v, want nil", got)
	}
	if got := sanRefs(nil); got != nil {
		t.Errorf("sanRefs(nil) = %v, want nil", got)
	}
}

// TestSanSHAGolden checks the git SHA validator: at least 40 hex chars.
func TestSanSHAGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sha  string
		want string
	}{
		{name: "40 a", sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "40 A", sha: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", want: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		{name: "mixed hex", sha: "abcdef0123456789abcdef0123456789abcdef01", want: "abcdef0123456789abcdef0123456789abcdef01"},
		{name: "too short", sha: "short", want: ""},
		{name: "non-hex", sha: "gggggggggggggggggggggggggggggggggggggggg", want: ""},
		{name: "64 a", sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanSHA(tc.sha); got != tc.want {
				t.Errorf("sanSHA(%q) = %q, want %q", tc.sha, got, tc.want)
			}
		})
	}
}

// TestParseRemoteURLGolden checks the rns:// URL parser for the three URL
// shapes used by rngit: repository (3 components), group (2), and destination
// (1). Error messages mirror Python's parse_remote_url family.
func TestParseRemoteURLGolden(t *testing.T) {
	t.Parallel()

	hash := "00112233445566778899aabbccddeeff"

	t.Run("repository url", func(t *testing.T) {
		t.Parallel()
		dest, group, repo, err := parseRemoteURL("rns://" + hash + "/mygroup/myrepo")
		if err != nil {
			t.Fatalf("parseRemoteURL: %v", err)
		}
		if hexDest(dest) != hash {
			t.Errorf("dest = %s, want %s", hexDest(dest), hash)
		}
		if group != "mygroup" || repo != "myrepo" {
			t.Errorf("group=%q repo=%q, want mygroup/myrepo", group, repo)
		}
	})

	t.Run("repository url case-insensitive scheme", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := parseRemoteURL("RNS://" + hash + "/g/r")
		if err != nil {
			t.Fatalf("parseRemoteURL upper scheme: %v", err)
		}
	})

	t.Run("group url", func(t *testing.T) {
		t.Parallel()
		dest, group, err := parseRemoteGroupURL("rns://" + hash + "/mygroup")
		if err != nil {
			t.Fatalf("parseRemoteGroupURL: %v", err)
		}
		if hexDest(dest) != hash || group != "mygroup" {
			t.Errorf("dest=%s group=%q, want %s/mygroup", hexDest(dest), group, hash)
		}
	})

	t.Run("destination url", func(t *testing.T) {
		t.Parallel()
		dest, err := parseRemoteDestinationURL("rns://" + hash)
		if err != nil {
			t.Fatalf("parseRemoteDestinationURL: %v", err)
		}
		if hexDest(dest) != hash {
			t.Errorf("dest = %s, want %s", hexDest(dest), hash)
		}
	})

	t.Run("wrong scheme", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := parseRemoteURL("https://" + hash + "/g/r")
		if err == nil {
			t.Fatal("parseRemoteURL wrong scheme: err = nil, want error")
		}
		if !contains(err.Error(), "Invalid protocol") {
			t.Errorf("err = %q, want it to contain 'Invalid protocol'", err.Error())
		}
	})

	t.Run("wrong component count", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := parseRemoteURL("rns://" + hash + "/onlygroup")
		if err == nil {
			t.Fatal("parseRemoteURL 2 components: err = nil, want error")
		}
		if !contains(err.Error(), "Invalid number of URL components") {
			t.Errorf("err = %q, want it to contain 'Invalid number of URL components'", err.Error())
		}
	})

	t.Run("bad hash length", func(t *testing.T) {
		t.Parallel()
		_, _, _, err := parseRemoteURL("rns://short/g/r")
		if err == nil {
			t.Fatal("parseRemoteURL short hash: err = nil, want error")
		}
		if !contains(err.Error(), "Invalid destination hash length") {
			t.Errorf("err = %q, want it to contain 'Invalid destination hash length'", err.Error())
		}
	})

	t.Run("non-hex hash", func(t *testing.T) {
		t.Parallel()
		bad := "zz" + hash[2:]
		_, _, _, err := parseRemoteURL("rns://" + bad + "/g/r")
		if err == nil {
			t.Fatal("parseRemoteURL non-hex: err = nil, want error")
		}
		if !contains(err.Error(), "Invalid destination hash") {
			t.Errorf("err = %q, want it to contain 'Invalid destination hash'", err.Error())
		}
	})
}

func hexDest(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexchars[v>>4]
		out[i*2+1] = hexchars[v&0xf]
	}
	return string(out)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
