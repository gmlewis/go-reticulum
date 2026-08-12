// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// TestParseRemoteURLValid checks a well-formed rns:// URL parses into its
// destination hash, group, and repo components.
func TestParseRemoteURLValid(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("a", destHexLen)
	url := "rns://" + hash + "/main/testrepo.git"
	dest, group, repo, err := parseRemoteURL(url)
	if err != nil {
		t.Fatalf("parseRemoteURL: %s", err)
	}
	if group != "main" {
		t.Errorf("group = %q, want main", group)
	}
	if repo != "testrepo.git" {
		t.Errorf("repo = %q, want testrepo.git", repo)
	}
	want, err := rns.HexToBytes(hash)
	if err != nil {
		t.Fatalf("HexToBytes: %s", err)
	}
	if !bytes.Equal(dest, want) {
		t.Errorf("dest = %x, want %x", dest, want)
	}
}

// TestParseRemoteURLCaseInsensitiveScheme checks the scheme match is
// case-insensitive, mirroring client.py remote.lower().startswith("rns://").
func TestParseRemoteURLCaseInsensitiveScheme(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("f", destHexLen)
	url := "RnS://" + hash + "/g/r"
	if _, _, _, err := parseRemoteURL(url); err != nil {
		t.Fatalf("parseRemoteURL case-insensitive: %s", err)
	}
}

// TestParseRemoteURLBadScheme checks a non-rns scheme is rejected.
func TestParseRemoteURLBadScheme(t *testing.T) {
	t.Parallel()

	if _, _, _, err := parseRemoteURL("https://abc/main/repo"); err == nil {
		t.Fatal("parseRemoteURL bad scheme = nil, want error")
	}
}

// TestParseRemoteURLWrongComponentCount checks URLs with too few or too many
// path components are rejected.
func TestParseRemoteURLWrongComponentCount(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("0", destHexLen)
	for _, url := range []string{
		"rns://" + hash,
		"rns://" + hash + "/main",
		"rns://" + hash + "/main/repo/extra",
	} {
		if _, _, _, err := parseRemoteURL(url); err == nil {
			t.Errorf("parseRemoteURL(%q) = nil, want error", url)
		}
	}
}

// TestParseRemoteURLBadHash checks a destination hash of the wrong length or
// non-hex content is rejected.
func TestParseRemoteURLBadHash(t *testing.T) {
	t.Parallel()

	for _, hash := range []string{"abcd", strings.Repeat("z", destHexLen)} {
		url := "rns://" + hash + "/main/repo"
		if _, _, _, err := parseRemoteURL(url); err == nil {
			t.Errorf("parseRemoteURL hash %q = nil, want error", hash)
		}
	}
}

// TestParseListResponse checks the /git/list response is split into its
// result-code byte and text payload.
func TestParseListResponse(t *testing.T) {
	t.Parallel()

	body := "abcdef0123456789abcdef0123456789abcdef01 refs/heads/main\n" +
		"@refs/heads/main HEAD\n"
	resp := append([]byte{resOK}, []byte(body)...)
	code, payload, ok := parseListResponse(resp)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if code != resOK {
		t.Errorf("code = %d, want %d", code, resOK)
	}
	if payload != body {
		t.Errorf("payload = %q, want %q", payload, body)
	}
}

// TestParseListResponseEmpty checks an empty response returns ok=false.
func TestParseListResponseEmpty(t *testing.T) {
	t.Parallel()

	if _, _, ok := parseListResponse(nil); ok {
		t.Fatal("ok = true, want false for empty response")
	}
}

// TestParseListResponseErrorCode checks a non-OK code is surfaced with its
// payload so callers can report the server's refusal message.
func TestParseListResponseErrorCode(t *testing.T) {
	t.Parallel()

	resp := append([]byte{resNotFound}, []byte("no such repo")...)
	code, payload, ok := parseListResponse(resp)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if code != resNotFound {
		t.Errorf("code = %d, want %d", code, resNotFound)
	}
	if payload != "no such repo" {
		t.Errorf("payload = %q, want %q", payload, "no such repo")
	}
}

// TestParseFetchResponseBundle checks a /git/fetch response with resOK plus
// bundle bytes is split into code and bundle data.
func TestParseFetchResponseBundle(t *testing.T) {
	t.Parallel()

	bundle := []byte("BUNDLE-DATA-HERE")
	resp := append([]byte{resOK}, bundle...)
	code, data, msg, ok := parseFetchResponse(resp)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if code != resOK {
		t.Errorf("code = %d, want %d", code, resOK)
	}
	if !bytes.Equal(data, bundle) {
		t.Errorf("bundle = %q, want %q", data, bundle)
	}
	if msg != "" {
		t.Errorf("msg = %q, want empty", msg)
	}
}

// TestParseFetchResponseEmptyBundle checks a resOK-only response (empty
// bundle: all objects already on the client) yields an empty bundle slice.
func TestParseFetchResponseEmptyBundle(t *testing.T) {
	t.Parallel()

	code, data, msg, ok := parseFetchResponse([]byte{resOK})
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if code != resOK {
		t.Errorf("code = %d, want %d", code, resOK)
	}
	if len(data) != 0 {
		t.Errorf("bundle len = %d, want 0", len(data))
	}
	if msg != "" {
		t.Errorf("msg = %q, want empty", msg)
	}
}

// TestParseFetchResponseError checks a non-OK fetch response yields the code
// and message payload.
func TestParseFetchResponseError(t *testing.T) {
	t.Parallel()

	resp := append([]byte{resRemoteFail}, []byte("Could not fetch refs")...)
	code, data, msg, ok := parseFetchResponse(resp)
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if code != resRemoteFail {
		t.Errorf("code = %d, want %d", code, resRemoteFail)
	}
	if data != nil {
		t.Errorf("bundle = %x, want empty for error response", data)
	}
	if msg != "Could not fetch refs" {
		t.Errorf("msg = %q, want %q", msg, "Could not fetch refs")
	}
}

// TestParseFetchResponseEmpty checks an empty fetch response returns ok=false.
func TestParseFetchResponseEmpty(t *testing.T) {
	t.Parallel()

	if _, _, _, ok := parseFetchResponse(nil); ok {
		t.Fatal("ok = true, want false for empty response")
	}
}

// TestParseSimpleResponse checks /git/push and /git/delete responses are split
// into code and message payload.
func TestParseSimpleResponse(t *testing.T) {
	t.Parallel()

	t.Run("ok bare", func(t *testing.T) {
		t.Parallel()
		code, msg, ok := parseSimpleResponse([]byte{resOK})
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if code != resOK {
			t.Errorf("code = %d, want %d", code, resOK)
		}
		if msg != "" {
			t.Errorf("msg = %q, want empty", msg)
		}
	})

	t.Run("error with msg", func(t *testing.T) {
		t.Parallel()
		resp := append([]byte{resRemoteFail}, []byte("Could not fetch from bundle")...)
		code, msg, ok := parseSimpleResponse(resp)
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if code != resRemoteFail {
			t.Errorf("code = %d, want %d", code, resRemoteFail)
		}
		if msg != "Could not fetch from bundle" {
			t.Errorf("msg = %q, want %q", msg, "Could not fetch from bundle")
		}
	})

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		if _, _, ok := parseSimpleResponse(nil); ok {
			t.Fatal("ok = true, want false for empty response")
		}
	})
}

// TestErrorMessage checks the result-code-to-message mapping for each non-OK
// code, mirroring the error decoding in client.py / cmd/gorngit/client.go.
func TestErrorMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code    byte
		msg     string
		wantHas string
	}{
		{resInvalidReq, "", "Remote error: Invalid request"},
		{resInvalidReq, "bad", "Remote error: bad"},
		{resNotFound, "", "Not found"},
		{resDisallowed, "", "Not allowed"},
		{resDisallowed, "denied", "denied"},
		{resRemoteFail, "", "Remote error: Unknown error"},
		{resRemoteFail, "boom", "Remote error: boom"},
		{0x42, "", "Remote error: Unknown error"},
	}
	for _, tc := range cases {
		got := errorMessage(tc.code, tc.msg)
		if !strings.Contains(got, tc.wantHas) {
			t.Errorf("errorMessage(%d, %q) = %q, want to contain %q", tc.code, tc.msg, got, tc.wantHas)
		}
	}
}

// TestListRequestMapConstruction checks the /git/list request map packs to
// msgpack with the integer IDX_REPOSITORY key and for_push flag exactly as
// the server's handleList expects (mirroring cmd/gorngit/client.go list).
func TestListRequestMapConstruction(t *testing.T) {
	t.Parallel()

	requestData := map[any]any{
		int64(idxRepository): "main/repo.git",
		"for_push":           true,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		t.Fatalf("Pack: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("UnpackPreserveBinMapKeys: %s", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	repoVal, ok := getMapValueTest(m, idxRepository)
	if !ok {
		t.Fatal("missing idxRepository key")
	}
	if repoVal.(string) != "main/repo.git" {
		t.Errorf("repoPath = %q, want %q", repoVal, "main/repo.git")
	}
	fpVal, ok := m["for_push"]
	if !ok {
		t.Fatal("missing for_push key")
	}
	if fpVal.(bool) != true {
		t.Errorf("for_push = %t, want true", fpVal)
	}
}

// TestFetchRequestMapConstruction checks the /git/fetch request map packs with
// idxRepository, refs list of {sha,ref,have} string maps, and optional global
// have list, matching server handleFetch's expectations.
func TestFetchRequestMapConstruction(t *testing.T) {
	t.Parallel()

	refsList := []map[string]string{
		{"sha": "aaa", "ref": "refs/heads/main"},
		{"sha": "bbb", "ref": "refs/heads/dev", "have": "ccc"},
	}
	requestData := map[any]any{
		int64(idxRepository): "main/repo.git",
		"refs":               refsList,
		"have":               []string{"ccc"},
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		t.Fatalf("Pack: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("UnpackPreserveBinMapKeys: %s", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	if v, ok := getMapValueTest(m, idxRepository); !ok || v.(string) != "main/repo.git" {
		t.Errorf("repoPath = %q, want %q", v, "main/repo.git")
	}
	refsVal, ok := m["refs"]
	if !ok {
		t.Fatal("missing refs key")
	}
	list, ok := refsVal.([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("refs type = %T, want 2-element []any", refsVal)
	}
	first, ok := list[0].(map[any]any)
	if !ok {
		t.Fatalf("refs[0] type = %T, want map[any]any", list[0])
	}
	if first["sha"].(string) != "aaa" || first["ref"].(string) != "refs/heads/main" {
		t.Errorf("refs[0] sha=%q ref=%q, want sha=%q ref=%q",
			first["sha"], first["ref"], "aaa", "refs/heads/main")
	}
	second := list[1].(map[any]any)
	if second["have"].(string) != "ccc" {
		t.Errorf("refs[1].have = %q, want %q", second["have"], "ccc")
	}
	haveVal, ok := m["have"]
	if !ok {
		t.Fatal("missing have key")
	}
	haveList, ok := haveVal.([]any)
	if !ok || len(haveList) != 1 || haveList[0].(string) != "ccc" {
		t.Errorf("have type = %T, want []any containing %q", haveVal, "ccc")
	}
}

// TestPushRequestMapConstruction checks the /git/push request map packs with
// idxRepository, local_ref, remote_ref, force, and bundle bytes, matching
// server handlePush's expectations (bundle as msgpack bin).
func TestPushRequestMapConstruction(t *testing.T) {
	t.Parallel()

	bundle := []byte("\x42\x55\x4e\x44\x4c\x45")
	requestData := map[any]any{
		int64(idxRepository): "main/repo.git",
		"local_ref":          "refs/heads/main",
		"remote_ref":         "refs/heads/main",
		"force":              true,
		"bundle":             bundle,
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		t.Fatalf("Pack: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("UnpackPreserveBinMapKeys: %s", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	if v, ok := getMapValueTest(m, idxRepository); !ok || v.(string) != "main/repo.git" {
		t.Errorf("repoPath = %q, want %q", v, "main/repo.git")
	}
	if m["local_ref"].(string) != "refs/heads/main" {
		t.Errorf("local_ref = %q, want %q", m["local_ref"], "refs/heads/main")
	}
	if m["remote_ref"].(string) != "refs/heads/main" {
		t.Errorf("remote_ref = %q, want %q", m["remote_ref"], "refs/heads/main")
	}
	if m["force"].(bool) != true {
		t.Errorf("force = %t, want true", m["force"])
	}
	b, ok := m["bundle"].([]byte)
	if !ok {
		t.Fatalf("bundle type = %T, want []byte", m["bundle"])
	}
	if !bytes.Equal(b, bundle) {
		t.Errorf("bundle = %x, want %x", b, bundle)
	}
}

// TestDeleteRequestMapConstruction checks the /git/delete request map packs
// with idxRepository and ref, matching the client.py deletion branch.
func TestDeleteRequestMapConstruction(t *testing.T) {
	t.Parallel()

	requestData := map[any]any{
		int64(idxRepository): "main/repo.git",
		"ref":                "refs/heads/old",
	}
	packed, err := msgpack.Pack(requestData)
	if err != nil {
		t.Fatalf("Pack: %s", err)
	}
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(packed)
	if err != nil {
		t.Fatalf("UnpackPreserveBinMapKeys: %s", err)
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", unpacked)
	}
	if v, ok := getMapValueTest(m, idxRepository); !ok || v.(string) != "main/repo.git" {
		t.Errorf("repoPath = %q, want %q", v, "main/repo.git")
	}
	if m["ref"].(string) != "refs/heads/old" {
		t.Errorf("ref = %q, want %q", m["ref"], "refs/heads/old")
	}
}

// TestCacheRemoteRefs checks the ref-list parser records ref->sha pairs and
// skips the HEAD symref line, mirroring handle_git_list's remote_refs
// bookkeeping (client.py).
func TestCacheRemoteRefs(t *testing.T) {
	t.Parallel()

	c := &rnsClient{remoteRefs: make(map[string]string)}
	body := "aaa refs/heads/main\n" +
		"bbb refs/heads/dev\n" +
		"@refs/heads/main HEAD\n"
	c.cacheRemoteRefs(body)
	if len(c.remoteRefs) != 2 {
		t.Fatalf("remoteRefs has %d entries, want 2", len(c.remoteRefs))
	}
	if c.remoteRefs["refs/heads/main"] != "aaa" {
		t.Errorf("remoteRefs[main] = %q, want aaa", c.remoteRefs["refs/heads/main"])
	}
	if c.remoteRefs["refs/heads/dev"] != "bbb" {
		t.Errorf("remoteRefs[dev] = %q, want bbb", c.remoteRefs["refs/heads/dev"])
	}
}

// TestIsRnsURL checks the scheme check is case-insensitive.
func TestIsRnsURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		want bool
	}{
		{"rns://abc/main/repo", true},
		{"RNS://abc/main/repo", true},
		{"RnS://", true},
		{"https://abc/main/repo", false},
		{"", false},
		{"rns", false},
	}
	for _, tc := range cases {
		if got := isRnsURL(tc.url); got != tc.want {
			t.Errorf("isRnsURL(%q) = %t, want %t", tc.url, got, tc.want)
		}
	}
}

// TestEqualFold checks the ASCII case-fold comparison.
func TestEqualFold(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a, b string
		want bool
	}{
		{"rns://", "rns://", true},
		{"RNS://", "rns://", true},
		{"rNs://", "rns://", true},
		{"https:", "rns://", false},
		{"rns:/", "rns://", false},
		{"rns://x", "rns://", false},
	}
	for _, tc := range cases {
		if got := equalFold(tc.a, tc.b); got != tc.want {
			t.Errorf("equalFold(%q, %q) = %t, want %t", tc.a, tc.b, got, tc.want)
		}
	}
}

// getMapValueTest mirrors cmd/gorngit/server.go getMapValue: it fetches a
// value from an unpacked msgpack map, coercing integer keys so IDX_REPOSITORY
// matches the int64/uint64 key produced by UnpackPreserveBinMapKeys.
func getMapValueTest(m map[any]any, key any) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	switch k := key.(type) {
	case int:
		if v, ok := m[int64(k)]; ok {
			return v, true
		}
		if v, ok := m[uint64(k)]; ok {
			return v, true
		}
	case int64:
		if v, ok := m[uint64(k)]; ok {
			return v, true
		}
		if v, ok := m[int(k)]; ok {
			return v, true
		}
	case uint64:
		if v, ok := m[int64(k)]; ok {
			return v, true
		}
	}
	return nil, false
}
