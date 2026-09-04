// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rrcd"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
	"github.com/gmlewis/go-reticulum/testutils"
)

const goldenRoomsSHA256 = "79ea3400117e265b0513c2fc0f86059cf20a8a6d207f8325e5eed2a6023b633b"
const goldenRoomsLen = 1081

// TestRoomsTemplateGolden pins the first-run rooms.toml template to the
// byte-captured Python output (1081 bytes ending "[rooms]\n", chmod 0o600).
func TestRoomsTemplateGolden(t *testing.T) {
	t.Parallel()
	rooms := defaultRoomsContent()
	if len(rooms) != goldenRoomsLen {
		t.Fatalf("rooms template = %v bytes, want %v", len(rooms), goldenRoomsLen)
	}
	sum := sha256.Sum256([]byte(rooms))
	if got := hex.EncodeToString(sum[:]); got != goldenRoomsSHA256 {
		t.Fatalf("rooms template sha256 = %v, want %v", got, goldenRoomsSHA256)
	}
	if !strings.HasSuffix(rooms, "[rooms]\n") {
		t.Fatalf("rooms template must end with \"[rooms]\\n\"; got suffix %q", rooms[len(rooms)-20:])
	}
}

// TestConfigTemplateRenderGolden checks the rendered rrcd.toml against the
// freshly captured Python first-run output (gated on a python3 that can
// import the original rrcd CLI). The fixed test paths stand in for the
// home-directory-derived paths Python would interpolate.
func TestConfigTemplateRenderGolden(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping live first-run template capture")
	}
	probe := exec.Command("python3", "-c", "import RNS")
	if err := probe.Run(); err != nil {
		t.Skip("python3 RNS not available; skipping live first-run template capture")
	}

	home := testutils.TempDir(t, "gorrcd-first-run-")
	out := testutils.RunPython(t, firstRunCaptureScript, home, testutils.PythonRRCDRepoDir(t))
	gotCfg := extractSection(t, out, "CFG")
	gotRooms := extractSection(t, out, "ROOMS")

	const testIdentityPath = "/home/test/.rrcd/hub_identity"
	const testRegistryPath = "/home/test/.rrcd/rooms.toml"
	wantCfg := defaultConfigContent(testIdentityPath, testRegistryPath)
	if gotCfg != wantCfg {
		t.Fatalf("rendered config template differs from live Python capture:\n--- Go (%d bytes) ---\n%v\n--- Python (%d bytes) ---\n%v",
			len(wantCfg), wantCfg, len(gotCfg), gotCfg)
	}
	if gotRooms != defaultRoomsContent() {
		t.Fatalf("rooms template differs from live Python capture:\n--- Go ---\n%v\n--- Python ---\n%v",
			defaultRoomsContent(), gotRooms)
	}
	_ = filepath.Join // keep filepath imported for future assertions
}

// extractSection pulls one "---NAME---" delimited section from the capture
// output.
func extractSection(t *testing.T, out, name string) string {
	t.Helper()
	start := strings.Index(out, "---"+name+"---")
	if start < 0 {
		t.Fatalf("capture output missing section %v:\n%v", name, out)
	}
	start += len("---" + name + "---")
	if start < len(out) && out[start] == '\n' {
		start++ // skip the newline that terminates the marker line
	}
	end := strings.Index(out[start:], "\n---")
	if end < 0 {
		t.Fatalf("capture output missing section terminator after %v", name)
	}
	// Keep the newline that precedes the terminator: it belongs to the
	// captured file's final line.
	return out[start : start+end+1]
}

// firstRunCaptureScript runs the original Python first-run bootstrap with
// RRCD_HOME set to a fresh temp dir and prints both templates, with the
// interpolated paths normalized to fixed test paths. argv[2] is the
// original-rrcd-repo directory.
const firstRunCaptureScript = `
import hashlib, os, sys
os.environ["RRCD_HOME"] = sys.argv[1]
sys.path.insert(0, sys.argv[2])
from rrcd import cli
from rrcd.paths import (
    default_config_path,
    default_identity_path,
    default_room_registry_path,
)
cp, ip, rp = default_config_path(), default_identity_path(), default_room_registry_path()
cli._ensure_first_run_files(str(cp), str(ip), str(rp))
cfg = open(cp, encoding="utf-8").read()
rooms = open(rp, encoding="utf-8").read()
cfg = cfg.replace(repr(str(ip)), repr("/home/test/.rrcd/hub_identity"))
cfg = cfg.replace(repr(str(rp)), repr("/home/test/.rrcd/rooms.toml"))
rdata = open(rp, "rb").read()
cdata = cfg.encode("utf-8")
print("rooms_sha", hashlib.sha256(rdata).hexdigest(), len(rdata))
print("cfg_sha", hashlib.sha256(cdata).hexdigest(), len(cdata))
print("---CFG---")
sys.stdout.write(cfg)
sys.stdout.write("---ROOMS---\n")
sys.stdout.write(rooms)
print("---END---")
`

// TestTemplatesRoundTripThroughTOML verifies both first-run templates parse
// with the rrcd/toml package and dump back byte-identically.
func TestTemplatesRoundTripThroughTOML(t *testing.T) {
	t.Parallel()
	cfg := defaultConfigContent("/home/test/.rrcd/hub_identity", "/home/test/.rrcd/rooms.toml")
	cfgDoc, err := toml.Parse(cfg)
	if err != nil {
		t.Fatalf("Parse config template: %v", err)
	}
	if got := cfgDoc.Dump(); got != cfg {
		t.Fatalf("config template round-trip mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, cfg)
	}
	roomsDoc, err := toml.Parse(defaultRoomsContent())
	if err != nil {
		t.Fatalf("Parse rooms template: %v", err)
	}
	if got := roomsDoc.Dump(); got != defaultRoomsContent() {
		t.Fatalf("rooms template round-trip mismatch:\n%q", got)
	}
}

// G16.21 The first-run path quoting must escape embedded quotes the way
// Python's repr does: a path containing both quote characters stays
// single-quoted with the embedded single quotes escaped, so the
// generated TOML parses. Golden: python3 -c "print(repr('/a\'b\"c'))".
func TestPythonReprStringBothQuotes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{`/a'b"c`, `'/a\'b"c'`},
		{"/plain/path", `'/plain/path'`},
		{`/has"double`, `'/has"double'`},
		{`/has'single`, `"/has'single"`},
		{`/back\slash`, `'/back\\slash'`},
		{"tab\there", `'tab\there'`},
	}
	for _, tt := range tests {
		if got := pythonReprString(tt.in); got != tt.want {
			t.Errorf("pythonReprString(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

// G16.24 The end-to-end first-run flow must create the FULL default
// rrcd.toml (every field, every comment) through the real file-writing
// path: the written file byte-equals the rendered template, the mode is
// 0o644 with no extra chmod, the identity and rooms files land at 0o600,
// the first-run message renders, and an existing rrcd.toml — even a
// one-byte junk file — is left untouched (Python's skip-if-exists).
func TestFirstRunCreatesFullDefaultConfig(t *testing.T) {
	// t.Setenv forbids parallel execution.
	home := testutils.TempDir(t, "gorrcd-firstrun-")
	t.Setenv("RRCD_HOME", home)

	configPath := rrcd.DefaultConfigPath()
	identityPath := rrcd.DefaultIdentityPath()
	roomRegistryPath := rrcd.DefaultRoomRegistryPath()

	created := ensureFirstRunFiles(configPath, identityPath, roomRegistryPath, nil)
	if !created {
		t.Fatal("the first run created nothing")
	}

	// The written config byte-equals the rendered template with the
	// RRCD_HOME-derived paths.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config read: %v", err)
	}
	if got, want := string(data), defaultConfigContent(identityPath, roomRegistryPath); got != want {
		t.Errorf("the written rrcd.toml does not match the template:\n got %v bytes\nwant %v bytes",
			len(got), len(want))
	}

	// Every [hub] and [logging] key appears with its comment block.
	for _, key := range []string{
		"configdir", "identity_path", "room_registry_path", "announce_on_start",
		"announce_period_s", "hub_name", "greeting", "trusted_identities",
		"banned_identities", "room_registry_prune_after_s",
		"room_registry_prune_interval_s", "room_invite_timeout_s",
		"include_joined_member_list", "max_nick_bytes", "max_room_name_bytes",
		"max_msg_body_bytes", "max_rooms_per_session", "rate_limit_msgs_per_minute",
		"ping_interval_s", "ping_timeout_s", "enable_resource_transfer",
		"max_resource_bytes", "max_pending_resource_expectations",
		"resource_expectation_ttl_s",
	} {
		if !strings.Contains(string(data), key+" =") {
			t.Errorf("the first-run config is missing the %v key", key)
		}
	}
	for _, key := range []string{"level", "rns_level", "console", "file", "format", "datefmt"} {
		if !strings.Contains(string(data), key+" =") {
			t.Errorf("the first-run config is missing the [logging] %v key", key)
		}
	}
	for _, comment := range []string{"# This file was created on first run.", "# Hub-initiated liveness checks (0 disables).", "# Large payload transfer via RNS.Resource"} {
		if !strings.Contains(string(data), comment) {
			t.Errorf("the first-run config lost its comment block: %q", comment)
		}
	}

	// The template is the full ~3.6 KB default.
	if len(data) < 3500 {
		t.Errorf("the written config is %v bytes, want the full ~3.6 KB template", len(data))
	}

	// Modes: the config at 0o644 with no extra chmod, identity and rooms
	// at 0o600.
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("config mode = %v, want 0644", info.Mode().Perm())
	}
	identInfo, err := os.Stat(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	if identInfo.Mode().Perm() != 0o600 {
		t.Errorf("identity mode = %v, want 0600", identInfo.Mode().Perm())
	}
	roomsInfo, err := os.Stat(roomRegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	if roomsInfo.Mode().Perm() != 0o600 {
		t.Errorf("rooms mode = %v, want 0600", roomsInfo.Mode().Perm())
	}

	// The first-run message mirrors Python's stderr notice with the
	// go-prefix self-reference.
	message := firstRunMessage(configPath, identityPath, roomRegistryPath)
	if !strings.HasPrefix(message, "Created default gorrcd files.") ||
		!strings.Contains(message, "- Config:   "+configPath) {
		t.Errorf("first-run message = %q", message)
	}

	// The skip-if-exists quirk: a pre-existing rrcd.toml — even one byte
	// of junk — is left untouched and the config is never rewritten.
	home2 := testutils.TempDir(t, "gorrcd-firstrun2-")
	t.Setenv("RRCD_HOME", home2)
	configPath2 := rrcd.DefaultConfigPath()
	if err := os.WriteFile(configPath2, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Python still creates the missing identity and rooms files, so
	// created_any can stay true; the pinned parity is that the config
	// itself is never rewritten.
	_ = ensureFirstRunFiles(rrcd.DefaultConfigPath(), rrcd.DefaultIdentityPath(),
		rrcd.DefaultRoomRegistryPath(), nil)
	data2, err := os.ReadFile(configPath2)
	if err != nil {
		t.Fatal(err)
	}
	if string(data2) != "x" {
		t.Errorf("the existing config was touched: %q", string(data2))
	}
}
