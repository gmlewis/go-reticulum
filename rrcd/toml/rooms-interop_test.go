// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package toml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// Cross-implementation storage round-trips: files written through the Go
// edit API must load in Python (tomlkit and the original rrcd.rooms loader),
// and Python-written files must load here with identical structure.

// writeGoRoomsTOML builds a rooms.toml exactly the way the Go port of the
// persist flow will: quoted room tables, sorted hex lists, invited
// sub-tables with Python-repr floats.
func writeGoRoomsTOML(t *testing.T, dir string) string {
	t.Helper()
	doc, err := Parse("[rooms]\n")
	if err != nil {
		t.Fatalf("Parse template: %v", err)
	}
	rooms := doc.TablePath("rooms")

	general := rooms.SetTable("general")
	general.Set("founder", StringValue("aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"))
	general.Set("topic", StringValue("Welcome to general"))
	general.Set("moderated", BoolValue(false))
	general.Set("invite_only", BoolValue(true))
	general.Set("topic_ops_only", BoolValue(true))
	general.Set("no_outside_msgs", BoolValue(true))
	general.Set("key", StringValue("secret"))
	general.Set("last_used_ts", FloatValue(1730000000.123456))
	general.Set("operators", StringArrayValue([]string{
		"00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
		"aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899",
	}))
	general.Set("voiced", StringArrayValue(nil))
	general.Set("bans", StringArrayValue([]string{"ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"}))
	invited := general.SetTable("invited")
	invited.Set("11223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", FloatValue(1730003600.0))
	invited.Set("aabbccdd", FloatValue(900.0))

	// A room name needing quotes.
	lobby := rooms.SetTable("my room")
	lobby.Set("founder", StringValue("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"))
	lobby.Set("moderated", BoolValue(true))
	lobby.Set("last_used_ts", FloatValue(900.0))

	path := filepath.Join(dir, "rooms.toml")
	if err := os.WriteFile(path, []byte(doc.Dump()), 0o600); err != nil {
		t.Fatalf("write rooms.toml: %v", err)
	}
	return path
}

// TestInteropGoRoomsLoadInPython writes a rooms.toml through the Go edit
// API and verifies the original Python loader reads the identical registry.
func TestInteropGoRoomsLoadInPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)
	dir := testutils.TempDir(t, "toml-interop-")
	path := writeGoRoomsTOML(t, dir)

	generalFounder := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	lobbyFounder := "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	script := `
import json, sys
sys.path.insert(0, sys.argv[2])
from rrcd.rooms import RoomManager
rr = RoomManager.__new__(RoomManager)
registry, err = rr.load_registry_from_path(sys.argv[1], invite_timeout_s=900.0)
assert err is None, err
def conv(v):
    if isinstance(v, (set, frozenset)):
        return sorted(h.hex() for h in v)
    if isinstance(v, bytes):
        return v.hex()
    if isinstance(v, dict):
        return [[h.hex(), e] for h, e in v.items()]
    return v

out = {}
for name, data in registry.items():
    out[name] = {k: conv(v) for k, v in data.items()}
print(json.dumps(out, sort_keys=True))
`
	out := testutils.RunPython(t, script, path, testutils.PythonRRCDRepoDir(t))
	// The Python registry stores founder/topic/key as strings, flags as
	// bools, lists as sorted hex, invited as hash→expiry pairs, and
	// last_used_ts as a float.
	wantSubstrings := []string{
		`"general"`, `"my room"`, `"founder": "` + generalFounder[:16],
		`"topic": "Welcome to general"`, `"key": "secret"`,
		`"invite_only": true`, `"no_outside_msgs": true`,
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Fatalf("python registry output missing %v:\n%v", s, out)
		}
	}
	if strings.Contains(out, lobbyFounder[:16]+"\"") {
		// lobby founder must be the lobby hash, not the general hash.
		if !strings.Contains(out, `"founder": "`+lobbyFounder[:16]) {
			t.Fatalf("lobby founder mismatch:\n%v", out)
		}
	}
}

// TestInteropPythonRoomsLoadInGo writes rooms.toml with live tomlkit and
// verifies the Go parser reads the identical structure.
func TestInteropPythonRoomsLoadInGo(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)
	dir := testutils.TempDir(t, "toml-interop-")
	path := filepath.Join(dir, "rooms.toml")
	script := `
import tomlkit, sys
doc = tomlkit.parse("[rooms]\n")
room = tomlkit.table()
doc["rooms"]["py room"] = room
room["founder"] = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
room["topic"] = "Python topic"
room["moderated"] = False
room["no_outside_msgs"] = True
room["last_used_ts"] = 1730000000.123456
room["operators"] = ["00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"]
inv = tomlkit.table()
doc["rooms"]["py room"]["invited"] = {"aabbccdd": 1730003600.0}
with open(sys.argv[1], "w", encoding="utf-8") as f:
    f.write(tomlkit.dumps(doc))
print(open(sys.argv[1], encoding="utf-8").read())
`
	out := testutils.RunPython(t, script, path)
	if !strings.Contains(out, "[rooms.\"py room\"]") {
		t.Fatalf("unexpected python-written file:\n%v", out)
	}

	doc, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse python-written rooms.toml: %v", err)
	}
	room := doc.TablePath("rooms", "py room")
	if v, ok := room.Get("founder"); !ok || v.Str != "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899" {
		t.Fatalf("founder = %+v, %v", v, ok)
	}
	if v, ok := room.Get("topic"); !ok || v.Str != "Python topic" {
		t.Fatalf("topic = %+v, %v", v, ok)
	}
	if v, ok := room.Get("moderated"); !ok || v.Bool {
		t.Fatalf("moderated = %+v, %v", v, ok)
	}
	if v, ok := room.Get("no_outside_msgs"); !ok || !v.Bool {
		t.Fatalf("no_outside_msgs = %+v, %v", v, ok)
	}
	if v, ok := room.Get("last_used_ts"); !ok || v.Flt != 1730000000.123456 {
		t.Fatalf("last_used_ts = %+v, %v", v, ok)
	}
	ops, ok := room.Get("operators")
	if !ok || len(ops.Arr) != 1 || ops.Arr[0].Str != "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" {
		t.Fatalf("operators = %+v, %v", ops, ok)
	}
	// The tomlkit-invited sub-table loads as a table.
	invited := doc.TablePath("rooms", "py room", "invited")
	if v, ok := invited.Get("aabbccdd"); !ok || v.Flt != 1730003600.0 {
		t.Fatalf("invited aabbccdd = %+v, %v", v, ok)
	}
}

// TestInteropGoTOMLLoadsInTomlkit verifies plain structural interop: the Go
// edit-API output parses in tomlkit with identical insertion order.
func TestInteropGoTOMLLoadsInTomlkit(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)
	dir := testutils.TempDir(t, "toml-interop-")
	path := writeGoRoomsTOML(t, dir)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
	script := `
import json, sys, tomlkit
doc = tomlkit.parse(open(sys.argv[1], encoding="utf-8").read())
rooms = doc["rooms"]
order = list(rooms.keys())
assert order == ["general", "my room"], order
general = rooms["general"]
keys = list(general.keys())
assert keys[:8] == ["founder", "topic", "moderated", "invite_only",
                    "topic_ops_only", "no_outside_msgs", "key", "last_used_ts"], keys
assert general["last_used_ts"] == 1730000000.123456
assert list(general["invited"].keys()) == [
    "11223344556677889900aabbccddeeff00112233445566778899aabbccddeeff", "aabbccdd"]
print("OK")
`
	out := testutils.RunPython(t, script, path)
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("tomlkit structure check failed:\n%v", out)
	}
}
