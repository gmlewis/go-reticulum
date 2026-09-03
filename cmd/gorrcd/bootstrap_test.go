// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	out := testutils.RunPython(t, firstRunCaptureScript, home)
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
// interpolated paths normalized to fixed test paths.
const firstRunCaptureScript = `
import hashlib, os, sys
os.environ["RRCD_HOME"] = sys.argv[1]
sys.path.insert(0, "/Users/glenn/src/github.com/kc1awv/rrcd")
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
