// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDiscoveryStorePathAndNameMatchPython pins where the discovered-interface
// store lives and what its files are called, by asking the Python reference to
// compute both from the same inputs. Python builds the path as
// `os.path.join(RNS.Reticulum.storagepath, "discovery", "interfaces")` with
// `Reticulum.storagepath = configdir+"/storage"` (RNS/Discovery.py:462,
// RNS/Reticulum.py:249) and the file name as the bare lowercase hex of the
// discovery hash, with no extension (RNS/Discovery.py:539-540).
//
// The store had drifted to <configdir>/discovery/interfaces/<hash>.data: a
// missing "storage" segment and an invented ".data" suffix. Neither
// implementation filters the directory when reading, so the records were still
// mutually visible — but the re-hear stat guard (RNS/Discovery.py:543,
// discovery.go persistDiscoveredInterface) keys on the canonical name, so with
// two spellings a re-announced interface was written a second time instead of
// updating the entry the other implementation was holding.
func TestDiscoveryStorePathAndNameMatchPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	configDir := testutils.TempDir(t, "rns-discovery-interop-")
	r := &Reticulum{configDir: configDir, logger: NewLogger()}
	discovery := NewInterfaceDiscovery(r)

	hash := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99}

	// What the production write path actually does: persist an entry, then find
	// the file it created. Asserting on a path this test derived for itself would
	// keep passing with the store in the wrong place.
	if err := discovery.persistDiscoveredInterface(map[string]any{
		"name":           "Path Probe",
		"type":           "TCPServerInterface",
		"transport":      true,
		"transport_id":   discoveryTestTransportIDHex,
		"network_id":     discoveryValidNetworkIDHex,
		"hops":           1,
		"value":          1,
		"received":       1700000000.0,
		"discovery_hash": hash,
		"reachable_on":   "127.0.0.1",
		"port":           4242,
	}); err != nil {
		t.Fatalf("persistDiscoveredInterface: %v", err)
	}
	writtenDir, writtenName := findWrittenDiscoveryFile(t, configDir)

	// What Python computes, from the same config dir and hash bytes.
	out := testutils.RunPython(t, discoveryPathScript, configDir, hex.EncodeToString(hash))
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		t.Fatalf("python probe returned %v fields, want 2:\n%v", len(fields), out)
	}
	pyPath, pyName := fields[0], fields[1]

	// The strongest form: Python must find the file the port wrote at ITS own
	// location, which holds only when the directory and the name both agree.
	if _, statErr := os.Stat(filepath.Join(pyPath, pyName)); statErr != nil {
		t.Errorf("python cannot see the port's discovery record at %v: %v", filepath.Join(pyPath, pyName), statErr)
	}
	if writtenDir != pyPath {
		t.Errorf("discovery store directory = %v, want Python's %v", writtenDir, pyPath)
	}
	if writtenName != pyName {
		t.Errorf("discovery file name = %v, want Python's %v", writtenName, pyName)
	}
}

// findWrittenDiscoveryFile returns the directory and file name of the single
// discovery record written beneath configDir, so a test can assert on where
// production actually put it rather than on a path it assumed.
func findWrittenDiscoveryFile(t *testing.T, configDir string) (dir, name string) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(configDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == "interfaces" {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %v: %v", configDir, err)
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one discovery record beneath %v, found %v: %v", configDir, len(found), found)
	}
	return filepath.Dir(found[0]), filepath.Base(found[0])
}

// TestDiscoveryStoreRoundTripWithPython drives a real cross-implementation round
// trip of the discovered-interface record: Go persists an entry, Python reads it
// back off disk through its own store location and reports the fields, then
// Python writes an entry that Go indexes. Both directions matter because the
// store is a shared cache — a user who runs either client must keep the
// discovered interfaces the other found.
func TestDiscoveryStoreRoundTripWithPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	configDir := testutils.TempDir(t, "rns-discovery-roundtrip-")
	r := &Reticulum{configDir: configDir, logger: NewLogger()}
	discovery := NewInterfaceDiscovery(r)
	if err := discovery.Start(0); err != nil {
		t.Fatalf("discovery Start: %v", err)
	}

	hash := []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c}
	info := map[string]any{
		"name":           "Interop TCP Client",
		"type":           "TCPServerInterface",
		"transport":      true,
		"transport_id":   discoveryTestTransportIDHex,
		"network_id":     discoveryValidNetworkIDHex,
		"hops":           2,
		"value":          7,
		"received":       1700000000.5,
		"discovery_hash": hash,
		"reachable_on":   "127.0.0.1",
		"port":           4242,
	}
	if err := discovery.persistDiscoveredInterface(info); err != nil {
		t.Fatalf("persistDiscoveredInterface: %v", err)
	}

	// Go -> Python: the reference reads the file at ITS computed location.
	out := testutils.RunPython(t, discoveryReadScript, configDir, hex.EncodeToString(hash))
	if got := strings.TrimSpace(out); got != "OK name=Interop TCP Client type=TCPServerInterface port=4242 hops=2 value=7" {
		t.Fatalf("python read of the Go-written discovery record:\n%v", out)
	}

	// Python -> Go: a second config dir, written entirely by the reference.
	configDir2 := testutils.TempDir(t, "rns-discovery-roundtrip-py-")
	r2 := &Reticulum{configDir: configDir2, logger: NewLogger()}
	discovery2 := NewInterfaceDiscovery(r2)
	if err := discovery2.Start(0); err != nil {
		t.Fatalf("discovery Start: %v", err)
	}

	hash2 := []byte{0x0f, 0x1e, 0x2d, 0x3c, 0x4b, 0x5a, 0x69, 0x78, 0x87, 0x96, 0xa5, 0xb4, 0xc3, 0xd2, 0xe1, 0xf0}
	testutils.RunPython(t, discoveryWriteScript, configDir2, hex.EncodeToString(hash2), discoveryTestTransportIDHex, discoveryValidNetworkIDHex)

	// Enable discovery's own cleanup paths, then index what Python left behind.
	entries, err := discovery2.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces: %v", err)
	}
	var found *DiscoveredInterface
	for i := range entries {
		if entries[i].Name == "Python Written Client" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("Go did not index the Python-written discovery record (got %v entries)", len(entries))
	}
	if found.Type != "TCPServerInterface" {
		t.Errorf("type = %v, want %q", found.Type, "TCPServerInterface")
	}
	if found.Port == nil || *found.Port != 4243 {
		t.Errorf("port = %v, want 4243", found.Port)
	}
	if found.Hops != 1 || found.Value != 3 {
		t.Errorf("hops/value = %v/%v, want 1/3", found.Hops, found.Value)
	}
	if found.ReachableOn != "10.0.0.5" {
		t.Errorf("reachable_on = %q, want %q", found.ReachableOn, "10.0.0.5")
	}
}

// discoveryPathScript prints the configured Reticulum storage root's
// discovered-interface directory and the file name Python would use for the
// given discovery hash, both computed by the reference itself.
const discoveryPathScript = `import os, sys
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
import RNS

configdir, hash_hex = sys.argv[1:3]
RNS.Reticulum(configdir=configdir, loglevel=RNS.LOG_NONE)
storage = os.path.join(RNS.Reticulum.storagepath, "discovery", "interfaces")
name = RNS.hexrep(bytes.fromhex(hash_hex), delimit=False)
print(storage, name)
`

// discoveryReadScript reads back a Go-written record from Python's own store
// location and prints its fields for the Go test to compare.
const discoveryReadScript = `import os, sys
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
from RNS.vendor import umsgpack
import RNS

configdir, hash_hex = sys.argv[1:3]
RNS.Reticulum(configdir=configdir, loglevel=RNS.LOG_NONE)
path = os.path.join(RNS.Reticulum.storagepath, "discovery", "interfaces", hash_hex)
assert os.path.isfile(path), "no discovery record at %s" % path
with open(path, "rb") as f:
    info = umsgpack.unpackb(f.read())
assert isinstance(info, dict), "record is not a dict: %r" % type(info)
print("OK name={} type={} port={} hops={} value={}".format(
    info["name"], info["type"], info["port"], info["hops"], info["value"]))
`

// discoveryWriteScript writes a record into the Python store location exactly
// as Python's interface_discovered does, so Go has to index a Python-shaped file.
const discoveryWriteScript = `import os, sys, time
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
from RNS.vendor import umsgpack
import RNS

configdir, hash_hex, transport_id_hex, network_id_hex = sys.argv[1:5]
RNS.Reticulum(configdir=configdir, loglevel=RNS.LOG_NONE)
store = os.path.join(RNS.Reticulum.storagepath, "discovery", "interfaces")
os.makedirs(store, exist_ok=True)
info = {
    "name": "Python Written Client",
    "type": "TCPServerInterface",
    "transport": True,
    "transport_id": transport_id_hex,
    "network_id": network_id_hex,
    "hops": 1,
    "value": 3,
    "received": time.time(),
    "discovery_hash": bytes.fromhex(hash_hex),
    "reachable_on": "10.0.0.5",
    "port": 4243,
    "discovered": time.time(),
    "last_heard": time.time(),
    "heard_count": 0,
}
path = os.path.join(store, RNS.hexrep(bytes.fromhex(hash_hex), delimit=False))
with open(path, "wb") as f:
    f.write(umsgpack.packb(info))
print("OK")
`
