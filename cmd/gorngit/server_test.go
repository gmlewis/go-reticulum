// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestParseRequestRepositoryPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		path  string
		group string
		repo  string
		ok    bool
	}{
		{name: "simple", path: "group/repo", group: "group", repo: "repo", ok: true},
		{name: "no slash", path: "grouprepo", group: "", repo: "", ok: false},
		{name: "three components", path: "a/b/c", group: "", repo: "", ok: false},
		{name: "empty", path: "", group: "", repo: "", ok: false},
		{name: "trailing slash", path: "group/repo/", group: "", repo: "", ok: false},
		{name: "group too long", path: pad256("g") + "/repo", group: "", repo: "", ok: false},
		{name: "repo too long", path: "group/" + pad256("r"), group: "", repo: "", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			group, repo := parseRequestRepositoryPath(tc.path)
			if tc.ok {
				if group != tc.group || repo != tc.repo {
					t.Fatalf("parseRequestRepositoryPath(%q) = (%q, %q), want (%q, %q)",
						tc.path, group, repo, tc.group, tc.repo)
				}
			} else {
				if group != "" || repo != "" {
					t.Fatalf("parseRequestRepositoryPath(%q) = (%q, %q), want empty",
						tc.path, group, repo)
				}
			}
		})
	}
}

func pad256(prefix string) string {
	b := make([]byte, 257)
	for i := range b {
		b[i] = 'x'
	}
	copy(b, prefix)
	return string(b)
}

func TestReadHeadSymref(t *testing.T) {
	t.Parallel()

	t.Run("ref prefix", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-head-")
		headPath := filepath.Join(dir, "HEAD")
		if err := os.WriteFile(headPath, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readHeadSymref(dir)
		want := "refs/heads/main"
		if got != want {
			t.Fatalf("readHeadSymref() = %q, want %q", got, want)
		}
	})

	t.Run("default master when missing", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-head-")
		got := readHeadSymref(dir)
		want := "master"
		if got != want {
			t.Fatalf("readHeadSymref() = %q, want %q", got, want)
		}
	})

	t.Run("raw sha falls back to master", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-head-")
		headPath := filepath.Join(dir, "HEAD")
		if err := os.WriteFile(headPath, []byte("abc123def456\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readHeadSymref(dir)
		want := "master"
		if got != want {
			t.Fatalf("readHeadSymref() = %q, want %q", got, want)
		}
	})

	t.Run("ref no newline", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-head-")
		headPath := filepath.Join(dir, "HEAD")
		if err := os.WriteFile(headPath, []byte("ref: refs/heads/develop"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readHeadSymref(dir)
		want := "refs/heads/develop"
		if got != want {
			t.Fatalf("readHeadSymref() = %q, want %q", got, want)
		}
	})
}

func TestFormatRefList(t *testing.T) {
	t.Parallel()

	t.Run("with refs", func(t *testing.T) {
		t.Parallel()

		eachRef := "abc123 refs/heads/main\ndef456 refs/heads/main\n789abc refs/tags/v1\n"
		got := string(formatRefList(eachRef, "refs/heads/main"))
		want := "\x00abc123 refs/heads/main\n789abc refs/tags/v1\n@refs/heads/main HEAD\n"
		if got != want {
			t.Fatalf("formatRefList() = %q, want %q", got, want)
		}
	})

	t.Run("empty refs", func(t *testing.T) {
		t.Parallel()

		got := string(formatRefList("", "refs/heads/main"))
		want := "\x00@refs/heads/main HEAD\n"
		if got != want {
			t.Fatalf("formatRefList() = %q, want %q", got, want)
		}
	})

	t.Run("blank lines skipped", func(t *testing.T) {
		t.Parallel()

		eachRef := "abc123 refs/heads/main\n\n\n789abc refs/tags/v1\n"
		got := string(formatRefList(eachRef, "refs/heads/main"))
		want := "\x00abc123 refs/heads/main\n789abc refs/tags/v1\n@refs/heads/main HEAD\n"
		if got != want {
			t.Fatalf("formatRefList() = %q, want %q", got, want)
		}
	})
}

func TestParseListResponse(t *testing.T) {
	t.Parallel()

	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		resp := []byte("\x00abc123 refs/heads/main\n@refs/heads/main HEAD\n")
		code, payload, ok := parseListResponse(resp)
		if !ok {
			t.Fatal("parseListResponse() ok = false, want true")
		}
		if code != resOK {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resOK)
		}
		want := "abc123 refs/heads/main\n@refs/heads/main HEAD\n"
		if payload != want {
			t.Fatalf("payload = %q, want %q", payload, want)
		}
	})

	t.Run("disallowed", func(t *testing.T) {
		t.Parallel()

		resp := []byte("\x01Not identified")
		code, payload, ok := parseListResponse(resp)
		if !ok {
			t.Fatal("parseListResponse() ok = false, want true")
		}
		if code != resDisallowed {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resDisallowed)
		}
		if payload != "Not identified" {
			t.Fatalf("payload = %q, want %q", payload, "Not identified")
		}
	})

	t.Run("empty response", func(t *testing.T) {
		t.Parallel()

		_, _, ok := parseListResponse(nil)
		if ok {
			t.Fatal("parseListResponse(nil) ok = true, want false")
		}
	})
}

func TestGetMapValue(t *testing.T) {
	t.Parallel()

	m := map[any]any{
		int64(0):   "group/repo",
		"for_push": true,
	}

	repo, ok := getMapValue(m, idxRepository)
	if !ok {
		t.Fatal("getMapValue(idxRepository) ok = false, want true")
	}
	got, _ := repo.(string)
	if got != "group/repo" {
		t.Fatalf("getMapValue(idxRepository) = %q, want %q", got, "group/repo")
	}

	fp, ok := getMapValue(m, "for_push")
	if !ok {
		t.Fatal("getMapValue(for_push) ok = false, want true")
	}
	if fp != true {
		t.Fatalf("getMapValue(for_push) = %v, want true", fp)
	}

	_, ok = getMapValue(m, "missing")
	if ok {
		t.Fatal("getMapValue(missing) ok = true, want false")
	}
}

func TestParseFetchRefs(t *testing.T) {
	t.Parallel()

	t.Run("three entries with have", func(t *testing.T) {
		t.Parallel()

		input := []any{
			map[any]any{"sha": "aaa", "ref": "refs/heads/main", "have": "bbb"},
			map[any]any{"sha": "ccc", "ref": "refs/heads/dev"},
			map[any]any{"sha": "ddd", "ref": "refs/tags/v1", "have": ""},
		}
		refs, ok := parseFetchRefs(input)
		if !ok {
			t.Fatal("parseFetchRefs ok = false, want true")
		}
		if len(refs) != 3 {
			t.Fatalf("len(refs) = %d, want 3", len(refs))
		}
		if refs[0].sha != "aaa" || refs[0].ref != "refs/heads/main" || refs[0].have != "bbb" {
			t.Fatalf("refs[0] = sha=%q ref=%q have=%q, want sha=aaa ref=refs/heads/main have=bbb",
				refs[0].sha, refs[0].ref, refs[0].have)
		}
		if refs[1].have != "" {
			t.Fatalf("refs[1].have = %q, want empty", refs[1].have)
		}
	})

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		refs, ok := parseFetchRefs([]any{})
		if !ok {
			t.Fatal("parseFetchRefs ok = false, want true")
		}
		if len(refs) != 0 {
			t.Fatalf("len(refs) = %d, want 0", len(refs))
		}
	})

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()

		_, ok := parseFetchRefs(nil)
		if ok {
			t.Fatal("parseFetchRefs(nil) ok = true, want false")
		}
	})

	t.Run("entry missing ref", func(t *testing.T) {
		t.Parallel()

		input := []any{map[any]any{"sha": "aaa"}}
		_, ok := parseFetchRefs(input)
		if ok {
			t.Fatal("parseFetchRefs ok = true for missing ref, want false")
		}
	})

	t.Run("entry not a map", func(t *testing.T) {
		t.Parallel()

		input := []any{"not a map"}
		_, ok := parseFetchRefs(input)
		if ok {
			t.Fatal("parseFetchRefs ok = true for non-map entry, want false")
		}
	})

	t.Run("non-list input", func(t *testing.T) {
		t.Parallel()

		_, ok := parseFetchRefs("not a list")
		if ok {
			t.Fatal("parseFetchRefs ok = true for non-list, want false")
		}
	})
}

func TestParseStringList(t *testing.T) {
	t.Parallel()

	t.Run("strings", func(t *testing.T) {
		t.Parallel()

		input := []any{"aaa", "bbb", "ccc"}
		got, ok := parseStringList(input)
		if !ok {
			t.Fatal("parseStringList ok = false, want true")
		}
		want := []string{"aaa", "bbb", "ccc"}
		if len(got) != len(want) {
			t.Fatalf("parseStringList len = %d, want %d (got %q, want %q)", len(got), len(want), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("parseStringList[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		got, ok := parseStringList([]any{})
		if !ok {
			t.Fatal("parseStringList ok = false, want true")
		}
		if len(got) != 0 {
			t.Fatalf("len(got) = %d, want 0", len(got))
		}
	})

	t.Run("nil input", func(t *testing.T) {
		t.Parallel()

		got, ok := parseStringList(nil)
		if !ok {
			t.Fatal("parseStringList(nil) ok = false, want true")
		}
		if len(got) != 0 {
			t.Fatalf("len(got) = %d, want 0", len(got))
		}
	})

	t.Run("non-string element", func(t *testing.T) {
		t.Parallel()

		input := []any{"aaa", 123}
		_, ok := parseStringList(input)
		if ok {
			t.Fatal("parseStringList ok = true for non-string element, want false")
		}
	})

	t.Run("non-list input", func(t *testing.T) {
		t.Parallel()

		_, ok := parseStringList(42)
		if ok {
			t.Fatal("parseStringList ok = true for non-list, want false")
		}
	})
}

func TestParseBundleData(t *testing.T) {
	t.Parallel()

	t.Run("bytes", func(t *testing.T) {
		t.Parallel()

		input := []byte{0x01, 0x02, 0x03}
		got, ok := parseBundleData(input)
		if !ok {
			t.Fatal("parseBundleData ok = false, want true")
		}
		if !bytes.Equal(got, input) {
			t.Fatalf("parseBundleData = %x, want %x", got, input)
		}
	})

	t.Run("string coerced to bytes", func(t *testing.T) {
		t.Parallel()

		got, ok := parseBundleData("hello")
		if !ok {
			t.Fatal("parseBundleData ok = false, want true")
		}
		if !bytes.Equal(got, []byte("hello")) {
			t.Fatalf("parseBundleData = %q, want %q", got, "hello")
		}
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		_, ok := parseBundleData(nil)
		if ok {
			t.Fatal("parseBundleData(nil) ok = true, want false")
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		t.Parallel()

		_, ok := parseBundleData(42)
		if ok {
			t.Fatal("parseBundleData(42) ok = true, want false")
		}
	})
}

func TestParseFetchResponse(t *testing.T) {
	t.Parallel()

	t.Run("ok with bundle", func(t *testing.T) {
		t.Parallel()

		bundle := []byte{0x42, 0x55, 0x4e, 0x44, 0x4c, 0x45}
		resp := append([]byte{resOK}, bundle...)
		code, data, msg, ok := parseFetchResponse(resp)
		if !ok {
			t.Fatal("parseFetchResponse ok = false, want true")
		}
		if code != resOK {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resOK)
		}
		if !bytes.Equal(data, bundle) {
			t.Fatalf("data = %x, want %x", data, bundle)
		}
		if msg != "" {
			t.Fatalf("msg = %q, want empty", msg)
		}
	})

	t.Run("ok empty bundle", func(t *testing.T) {
		t.Parallel()

		code, data, msg, ok := parseFetchResponse([]byte{resOK})
		if !ok {
			t.Fatal("parseFetchResponse ok = false, want true")
		}
		if code != resOK {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resOK)
		}
		if len(data) != 0 {
			t.Fatalf("len(data) = %d, want 0", len(data))
		}
		if msg != "" {
			t.Fatalf("msg = %q, want empty", msg)
		}
	})

	t.Run("remote fail with message", func(t *testing.T) {
		t.Parallel()

		resp := append([]byte{resRemoteFail}, []byte("Could not fetch refs")...)
		code, data, msg, ok := parseFetchResponse(resp)
		if !ok {
			t.Fatal("parseFetchResponse ok = false, want true")
		}
		if code != resRemoteFail {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resRemoteFail)
		}
		if len(data) != 0 {
			t.Fatalf("len(data) = %d, want 0", len(data))
		}
		if msg != "Could not fetch refs" {
			t.Fatalf("msg = %q, want %q", msg, "Could not fetch refs")
		}
	})

	t.Run("empty response", func(t *testing.T) {
		t.Parallel()

		_, _, _, ok := parseFetchResponse(nil)
		if ok {
			t.Fatal("parseFetchResponse(nil) ok = true, want false")
		}
	})
}

func TestBuildBundleCreateArgs(t *testing.T) {
	t.Parallel()

	t.Run("refs only no haves", func(t *testing.T) {
		t.Parallel()

		refs := []fetchRefEntry{
			{sha: "aaa", ref: "refs/heads/main"},
			{sha: "ccc", ref: "refs/tags/v1"},
		}
		args := buildBundleCreateArgs("/tmp/bundle.bundle", refs, nil)
		want := []string{"bundle", "create", "--no-progress", "/tmp/bundle.bundle",
			"refs/heads/main", "refs/tags/v1"}
		if len(args) != len(want) {
			t.Fatalf("len(args) = %d, want %d (%q)", len(args), len(want), args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Fatalf("args[%d] = %q, want %q (full: %q)", i, args[i], want[i], args)
			}
		}
	})

	t.Run("per-ref have excluded", func(t *testing.T) {
		t.Parallel()

		refs := []fetchRefEntry{
			{sha: "aaa", ref: "refs/heads/main", have: "bbb"},
		}
		args := buildBundleCreateArgs("/tmp/b.bundle", refs, []string{"bbb"})
		want := []string{"bundle", "create", "--no-progress", "/tmp/b.bundle",
			"refs/heads/main", "^bbb"}
		if len(args) != len(want) {
			t.Fatalf("len(args) = %d, want %d (%q)", len(args), len(want), args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Fatalf("args[%d] = %q, want %q (full: %q)", i, args[i], want[i], args)
			}
		}
	})

	t.Run("global haves excluded", func(t *testing.T) {
		t.Parallel()

		refs := []fetchRefEntry{
			{sha: "aaa", ref: "refs/heads/main"},
		}
		args := buildBundleCreateArgs("/tmp/b.bundle", refs, []string{"sha1", "sha2"})
		want := []string{"bundle", "create", "--no-progress", "/tmp/b.bundle",
			"refs/heads/main", "^sha1", "^sha2"}
		if len(args) != len(want) {
			t.Fatalf("len(args) = %d, want %d (%q)", len(args), len(want), args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Fatalf("args[%d] = %q, want %q (full: %q)", i, args[i], want[i], args)
			}
		}
	})

	t.Run("mixed refs and exclusions", func(t *testing.T) {
		t.Parallel()

		refs := []fetchRefEntry{
			{sha: "aaa", ref: "refs/heads/main"},
			{sha: "ccc", ref: "refs/tags/v1"},
		}
		args := buildBundleCreateArgs("/tmp/b.bundle", refs, []string{"sha1"})
		want := []string{"bundle", "create", "--no-progress", "/tmp/b.bundle",
			"refs/heads/main", "refs/tags/v1", "^sha1"}
		if len(args) != len(want) {
			t.Fatalf("len(args) = %d, want %d (%q)", len(args), len(want), args)
		}
		for i := range want {
			if args[i] != want[i] {
				t.Fatalf("args[%d] = %q, want %q (full: %q)", i, args[i], want[i], args)
			}
		}
	})
}

func TestBuildFetchRequestMap(t *testing.T) {
	t.Parallel()

	refs := []fetchRefEntry{
		{sha: "aaa", ref: "refs/heads/main", have: "bbb"},
		{sha: "ccc", ref: "refs/tags/v1"},
	}
	haves := []string{"global1"}

	m := buildFetchRequestMap("group/repo.git", refs, haves)

	repo, ok := getMapValue(m, idxRepository)
	if !ok || repo != "group/repo.git" {
		t.Fatalf("repo = %q, ok=%t, want group/repo.git", repo, ok)
	}

	refsVal, ok := getMapValue(m, "refs")
	if !ok {
		t.Fatal("missing refs key")
	}
	refsList, ok := refsVal.([]map[string]string)
	if !ok {
		t.Fatalf("refs type = %T, want []map[string]string", refsVal)
	}
	if len(refsList) != 2 {
		t.Fatalf("len(refsList) = %d, want 2", len(refsList))
	}
	if refsList[0]["ref"] != "refs/heads/main" || refsList[0]["have"] != "bbb" {
		t.Fatalf("refsList[0] = ref=%q have=%q, want ref=refs/heads/main have=bbb",
			refsList[0]["ref"], refsList[0]["have"])
	}

	haveVal, ok := getMapValue(m, "have")
	if !ok {
		t.Fatal("missing have key")
	}
	haveList, ok := haveVal.([]string)
	if !ok {
		t.Fatalf("have type = %T, want []string", haveVal)
	}
	if len(haveList) != 1 || haveList[0] != "global1" {
		t.Fatalf("have = %q, want [global1]", haveList)
	}
}

func TestBuildFetchRequestMapNoHaves(t *testing.T) {
	t.Parallel()

	refs := []fetchRefEntry{{sha: "aaa", ref: "refs/heads/main"}}
	m := buildFetchRequestMap("group/repo.git", refs, nil)

	if _, ok := getMapValue(m, "have"); ok {
		t.Fatal("have key present, want absent when no haves")
	}
}

func TestBuildFetchRequestMapRoundTrip(t *testing.T) {
	t.Parallel()

	refs := []fetchRefEntry{
		{sha: "aaa", ref: "refs/heads/main", have: "bbb"},
	}
	m := buildFetchRequestMap("group/repo.git", refs, []string{"g1"})
	packed, err := msgpack.Pack(m)
	if err != nil {
		t.Fatalf("msgpack.Pack failed: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("msgpack.UnpackPreserveBinMapKeys failed: %s", err)
	}
	rt, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	repo, ok := getMapValue(rt, idxRepository)
	if !ok || repo != "group/repo.git" {
		t.Fatalf("round-trip repo = %q ok=%t, want group/repo.git", repo, ok)
	}
	refsVal, ok := getMapValue(rt, "refs")
	if !ok {
		t.Fatal("round-trip missing refs")
	}
	refs2, ok := parseFetchRefs(refsVal)
	if !ok || len(refs2) != 1 {
		t.Fatalf("round-trip parseFetchRefs ok=%t len=%d", ok, len(refs2))
	}
	if refs2[0].ref != "refs/heads/main" || refs2[0].have != "bbb" {
		t.Fatalf("round-trip refs[0] = ref=%q have=%q", refs2[0].ref, refs2[0].have)
	}
}

func TestBuildPushRequestMap(t *testing.T) {
	t.Parallel()

	bundle := []byte{0x01, 0x02, 0x03}
	m := buildPushRequestMap("group/repo.git", "refs/heads/main", "refs/heads/main", true, bundle)

	repo, ok := getMapValue(m, idxRepository)
	if !ok || repo != "group/repo.git" {
		t.Fatalf("repo = %q ok=%t, want group/repo.git", repo, ok)
	}
	lr, ok := getMapValue(m, "local_ref")
	if !ok || lr != "refs/heads/main" {
		t.Fatalf("local_ref = %q ok=%t", lr, ok)
	}
	rr, ok := getMapValue(m, "remote_ref")
	if !ok || rr != "refs/heads/main" {
		t.Fatalf("remote_ref = %q ok=%t", rr, ok)
	}
	f, ok := getMapValue(m, "force")
	if !ok || f != true {
		t.Fatalf("force = %t ok=%t, want true", f, ok)
	}
	b, ok := getMapValue(m, "bundle")
	if !ok {
		t.Fatal("missing bundle key")
	}
	bb, ok := b.([]byte)
	if !ok || !bytes.Equal(bb, bundle) {
		t.Fatalf("bundle = %x, want %x", bb, bundle)
	}
}

func TestBuildPushRequestMapRoundTrip(t *testing.T) {
	t.Parallel()

	bundle := []byte{0x42, 0x55, 0x4e, 0x44, 0x4c, 0x45, 0x00, 0xff}
	m := buildPushRequestMap("group/repo.git", "refs/heads/main", "refs/heads/main", false, bundle)
	packed, err := msgpack.Pack(m)
	if err != nil {
		t.Fatalf("msgpack.Pack failed: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("msgpack.UnpackPreserveBinMapKeys failed: %s", err)
	}
	rt, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	b, ok := getMapValue(rt, "bundle")
	if !ok {
		t.Fatal("round-trip missing bundle")
	}
	bb, ok := b.([]byte)
	if !ok || !bytes.Equal(bb, bundle) {
		t.Fatalf("round-trip bundle = %x ok=%t, want %x", bb, ok, bundle)
	}
}

func TestSourceSchemeAllowed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		allowed bool
	}{
		{name: "http", source: "http://example.com/repo.git", allowed: true},
		{name: "https uppercase scheme", source: "HTTPS://example.com/repo.git", allowed: true},
		{name: "ssh", source: "ssh://git@example.com/repo.git", allowed: true},
		{name: "rns", source: "rns://abcdef/main/repo.git", allowed: true},
		{name: "file rejected", source: "file:///tmp/repo.git", allowed: false},
		{name: "no scheme", source: "/tmp/repo.git", allowed: false},
		{name: "empty", source: "", allowed: false},
		{name: "git protocol rejected", source: "git://example.com/repo.git", allowed: false},
		{name: "scheme only with separator", source: "http://", allowed: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := sourceSchemeAllowed(tc.source)
			if got != tc.allowed {
				t.Fatalf("sourceSchemeAllowed(%q) = %t, want %t", tc.source, got, tc.allowed)
			}
		})
	}
}

func TestParseLsRemoteSymref(t *testing.T) {
	t.Parallel()

	t.Run("symref head line", func(t *testing.T) {
		t.Parallel()

		output := "ref: refs/heads/main\tHEAD\nabc123\tHEAD\n"
		got := parseLsRemoteSymref(output)
		want := "refs/heads/main"
		if got != want {
			t.Fatalf("parseLsRemoteSymref() = %q, want %q", got, want)
		}
	})

	t.Run("no symref line", func(t *testing.T) {
		t.Parallel()

		output := "abc123\tHEAD\n"
		got := parseLsRemoteSymref(output)
		if got != "" {
			t.Fatalf("parseLsRemoteSymref() = %q, want empty", got)
		}
	})

	t.Run("symref not head", func(t *testing.T) {
		t.Parallel()

		output := "ref: refs/heads/main\trefs/heads/main\n"
		got := parseLsRemoteSymref(output)
		if got != "" {
			t.Fatalf("parseLsRemoteSymref() = %q, want empty (tab value not HEAD)", got)
		}
	})

	t.Run("empty output", func(t *testing.T) {
		t.Parallel()

		if got := parseLsRemoteSymref(""); got != "" {
			t.Fatalf("parseLsRemoteSymref(\"\") = %q, want empty", got)
		}
	})

	t.Run("develop branch", func(t *testing.T) {
		t.Parallel()

		output := "ref: refs/heads/develop\tHEAD\n"
		got := parseLsRemoteSymref(output)
		want := "refs/heads/develop"
		if got != want {
			t.Fatalf("parseLsRemoteSymref() = %q, want %q", got, want)
		}
	})
}

func TestBuildRemoteCloneRequestMap(t *testing.T) {
	t.Parallel()

	m := buildRemoteCloneRequestMap("main/repo.git", "http://example.com/src.git")

	repo, ok := getMapValue(m, idxRepository)
	if !ok || repo != "main/repo.git" {
		t.Fatalf("repo = %q ok=%t, want main/repo.git", repo, ok)
	}
	src, ok := getMapValue(m, "source")
	if !ok || src != "http://example.com/src.git" {
		t.Fatalf("source = %q ok=%t, want http://example.com/src.git", src, ok)
	}
}

func TestBuildRemoteCloneRequestMapRoundTrip(t *testing.T) {
	t.Parallel()

	m := buildRemoteCloneRequestMap("main/repo.git", "http://example.com/src.git")
	packed, err := msgpack.Pack(m)
	if err != nil {
		t.Fatalf("msgpack.Pack failed: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("msgpack.UnpackPreserveBinMapKeys failed: %s", err)
	}
	rt, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	repo, ok := getMapValue(rt, idxRepository)
	if !ok || repo != "main/repo.git" {
		t.Fatalf("round-trip repo = %q ok=%t, want main/repo.git", repo, ok)
	}
	src, ok := getMapValue(rt, "source")
	if !ok || src != "http://example.com/src.git" {
		t.Fatalf("round-trip source = %q ok=%t, want http://example.com/src.git", src, ok)
	}
}

func TestBuildSyncRequestMap(t *testing.T) {
	t.Parallel()

	m := buildSyncRequestMap("main/repo.git")
	repo, ok := getMapValue(m, idxRepository)
	if !ok || repo != "main/repo.git" {
		t.Fatalf("repo = %q ok=%t, want main/repo.git", repo, ok)
	}
	if _, ok := getMapValue(m, "source"); ok {
		t.Fatal("sync request map has source key, want absent")
	}
}

func TestBuildDeleteRequestMap(t *testing.T) {
	t.Parallel()

	m := buildDeleteRequestMap("main/repo.git", "refs/heads/main")
	repo, ok := getMapValue(m, idxRepository)
	if !ok || repo != "main/repo.git" {
		t.Fatalf("repo = %q ok=%t, want main/repo.git", repo, ok)
	}
	ref, ok := getMapValue(m, "ref")
	if !ok || ref != "refs/heads/main" {
		t.Fatalf("ref = %q ok=%t, want refs/heads/main", ref, ok)
	}
}

func TestParseResultResponse(t *testing.T) {
	t.Parallel()

	t.Run("ok no payload", func(t *testing.T) {
		t.Parallel()

		code, msg := parseResultResponse([]byte{resOK})
		if code != resOK {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resOK)
		}
		if msg != "" {
			t.Fatalf("msg = %q, want empty", msg)
		}
	})

	t.Run("not found with payload", func(t *testing.T) {
		t.Parallel()

		code, msg := parseResultResponse([]byte{resNotFound, 'N', 'o', 't'})
		if code != resNotFound {
			t.Fatalf("code = 0x%02x, want 0x%02x", code, resNotFound)
		}
		if msg != "Not" {
			t.Fatalf("msg = %q, want %q", msg, "Not")
		}
	})

	t.Run("empty response", func(t *testing.T) {
		t.Parallel()

		code, msg := parseResultResponse(nil)
		if code != 0 || msg != "" {
			t.Fatalf("code = 0x%02x msg = %q, want 0/empty", code, msg)
		}
	})
}

func TestRepoUpstreamType(t *testing.T) {
	t.Parallel()

	t.Run("plain repo no upstream", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-upstream-plain-")
		repoPath := filepath.Join(dir, "plain.git")
		runTestGitInitBare(t, repoPath)

		repoType, source := repoUpstreamType(repoPath)
		if repoType != "" || source != "" {
			t.Fatalf("repoUpstreamType() = (%q, %q), want empty", repoType, source)
		}
	})

	t.Run("mirror with source", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-upstream-mirror-")
		repoPath := filepath.Join(dir, "mirror.git")
		runTestGitInitBare(t, repoPath)
		runTestGit(t, repoPath, "config", "repository.rngit.type", "mirror")
		runTestGit(t, repoPath, "config", "repository.rngit.upstream.source", "http://example.com/src.git")

		repoType, source := repoUpstreamType(repoPath)
		if repoType != "mirror" {
			t.Fatalf("repoType = %q, want mirror", repoType)
		}
		if source != "http://example.com/src.git" {
			t.Fatalf("source = %q, want http://example.com/src.git", source)
		}
	})

	t.Run("fork with source", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-upstream-fork-")
		repoPath := filepath.Join(dir, "fork.git")
		runTestGitInitBare(t, repoPath)
		runTestGit(t, repoPath, "config", "repository.rngit.type", "fork")
		runTestGit(t, repoPath, "config", "repository.rngit.upstream.source", "http://example.com/src.git")

		repoType, source := repoUpstreamType(repoPath)
		if repoType != "fork" {
			t.Fatalf("repoType = %q, want fork", repoType)
		}
		if source != "http://example.com/src.git" {
			t.Fatalf("source = %q, want http://example.com/src.git", source)
		}
	})

	t.Run("type set but unknown value", func(t *testing.T) {
		t.Parallel()

		dir := testutils.TempDir(t, "rngit-upstream-unknown-")
		repoPath := filepath.Join(dir, "weird.git")
		runTestGitInitBare(t, repoPath)
		runTestGit(t, repoPath, "config", "repository.rngit.type", "bogus")

		repoType, _ := repoUpstreamType(repoPath)
		if repoType != "" {
			t.Fatalf("repoType = %q, want empty for bogus type", repoType)
		}
	})
}

// runTestGitInitBare creates a bare git repo at repoPath for unit tests.
func runTestGitInitBare(t *testing.T, repoPath string) {
	t.Helper()
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir bare repo: %s", err)
	}
	runTestGit(t, repoPath, "init", "--bare")
}

// runTestGit runs a git command in dir for unit tests, failing on error.
func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s in %s failed: %s\nstderr: %s", strings.Join(args, " "), dir, err, stderr.String())
	}
}
