// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPropagationStoreStampedRoundTripWithPython pins the LXMF propagation
// message store's on-disk contract against the reference for the case that
// carries real traffic.
//
// Python names a store file
// `f"{messagepath}/{hexrep(transient_id)}_{received}{value_component}"` with
// `value_component = f"_{stamp_value}" if stamp_value and stamp_value > 0 else ""`
// (LXMRouter.py:2513-2514) and writes the body as `lxmf_data + stamp_data`
// (LXMRouter.py:2512,2515). A normally stamped message therefore has a
// three-component name and a body of the packed message followed by the 32-byte
// stamp, and both implementations must agree on both byte for byte — the store is
// the only copy of a message a propagation node is holding, so a divergence here
// is a message the other side cannot read.
//
// The unstamped case is the deliberate, documented deviation explained in
// writePropagationMessageFile and is asserted separately below.
func TestPropagationStoreStampedRoundTripWithPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	storageRoot := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, rns.NewTransportSystem(nil), nil, storageRoot)

	destinationHash := bytes.Repeat([]byte{0x7e}, 16)
	payload := bytes.Repeat([]byte{0x5a}, 96) // stands in for the packed LXMF message
	transientID := rns.FullHash(payload)
	stampData := bytes.Repeat([]byte{0x11}, 32)
	stampValue := 4
	receivedAt := time.Unix(1700000000, 123456789)

	path, size, err := router.writePropagationMessageFile(transientID, receivedAt, stampValue, destinationHash, payload, stampData)
	if err != nil {
		t.Fatalf("writePropagationMessageFile: %v", err)
	}
	if size != len(payload)+len(stampData) {
		t.Errorf("reported size = %v, want %v", size, len(payload)+len(stampData))
	}
	if got := filepath.Dir(path); got != router.propagationMessageStorePath() {
		t.Errorf("store directory = %v, want %v", got, router.propagationMessageStorePath())
	}

	// The file name the reference would have chosen for the same inputs, and the
	// reference's reading of the body Go wrote.
	out := testutils.RunPython(t, storeStampedScript, filepath.Dir(path),
		hex.EncodeToString(transientID), filepath.Base(path), hex.EncodeToString(payload),
		hex.EncodeToString(stampData), strconv.Itoa(stampValue))
	got := strings.TrimSpace(out)
	pyName, body, ok := strings.Cut(got, " ")
	if !ok {
		t.Fatalf("unexpected python output:\n%v", out)
	}
	if filepath.Base(path) != pyName {
		t.Errorf("store file name = %v, want Python's %v", filepath.Base(path), pyName)
	}
	if body != "body=lxmf_data+stamp" {
		t.Errorf("python read of the Go-written store body: %v (want body=lxmf_data+stamp)", body)
	}
}

// TestPropagationStoreIndexesPythonWrittenFile pins the read direction: a store
// file written by the reference is indexed by the port with the same transient id,
// payload and size. The port's re-index is what repopulates a node's message store
// after a restart, so an unreadable Python-written file is a message a Go node
// silently drops.
func TestPropagationStoreIndexesPythonWrittenFile(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	storageRoot := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, rns.NewTransportSystem(nil), nil, storageRoot)

	// The stored body is the packed message, which BEGINS with the destination
	// hash; the store does not carry the hash separately. Both implementations
	// recover it from the first DestinationLength bytes.
	destinationHash := bytes.Repeat([]byte{0x8f}, 16)
	payload := append(append([]byte{}, destinationHash...), bytes.Repeat([]byte{0x6b}, 64)...)
	transientID := rns.FullHash(payload)
	stampData := bytes.Repeat([]byte{0x22}, 32)

	storePath := router.propagationMessageStorePath()
	testutils.RunPython(t, storeWriteScript, storePath, hex.EncodeToString(transientID),
		hex.EncodeToString(payload), hex.EncodeToString(stampData), "5")

	router.mu.Lock()
	router.reindexPropagationStoreLocked()
	entry := router.propagationEntries[string(transientID)]
	router.mu.Unlock()

	if entry == nil {
		t.Fatalf("port did not index the Python-written store file in %v", storePath)
	}
	if !bytes.Equal(entry.payload, payload) {
		t.Errorf("payload = %x (len %v), want %x (len %v)", entry.payload[:min(8, len(entry.payload))], len(entry.payload), payload[:8], len(payload))
	}
	if !bytes.Equal(entry.destinationHash, destinationHash) {
		t.Errorf("destination hash = %x, want %x", entry.destinationHash, destinationHash)
	}
	if want := len(payload) + len(stampData); entry.size != want {
		t.Errorf("size = %v, want %v", entry.size, want)
	}
}

// TestPropagationStoreUnstampedNameDeviationIsDocumented pins the one deliberate
// deviation in this format. For a message accepted with no stamp, Python omits the
// value component (LXMRouter.py:2513-2514) and writes a TWO-component name that
// Python's own indexer then skips, because it requires three components
// (LXMRouter.py:568). The port keeps the explicit zero, which both indexers accept,
// so a store the port writes stays readable — including by Python.
func TestPropagationStoreUnstampedNameDeviationIsDocumented(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	storageRoot := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, rns.NewTransportSystem(nil), nil, storageRoot)

	transientID := bytes.Repeat([]byte{0x9e}, 32)
	destinationHash := bytes.Repeat([]byte{0xa1}, 16)
	payload := bytes.Repeat([]byte{0x7c}, 40)

	path, _, err := router.writePropagationMessageFile(transientID, time.Unix(1700000000, 0), 0, destinationHash, payload, nil)
	if err != nil {
		t.Fatalf("writePropagationMessageFile: %v", err)
	}
	goName := filepath.Base(path)

	// Python's own rule for the same inputs, evaluated by the reference.
	out := strings.TrimSpace(testutils.RunPython(t, storeUnstampedNameScript, hex.EncodeToString(transientID), goName))
	parts := strings.Fields(out)
	if len(parts) != 3 {
		t.Fatalf("unexpected python output %q, want '<pyname> <components> <indexes>'", out)
	}
	pyName, pyComponents, pyIndexes := parts[0], parts[1], parts[2]

	if pyComponents != "2" {
		t.Errorf("Python's unstamped name %v has %v components, want 2", pyName, pyComponents)
	}
	if pyIndexes != "False" {
		t.Errorf("Python indexes its own unstamped name %v = %v, want False", pyName, pyIndexes)
	}

	// The port's name is three components and IS indexed by the port's parser.
	if got := len(strings.Split(goName, "_")); got != 3 {
		t.Errorf("port's unstamped name %v has %v components, want 3", goName, got)
	}
	if _, _, _, ok := parsePropagationStoreFilename(goName); !ok {
		t.Errorf("port rejects its own unstamped store file name %v", goName)
	}
	// And Python's name is rejected by the port's parser, as it is by Python's.
	if _, _, _, ok := parsePropagationStoreFilename(pyName); ok {
		t.Errorf("port accepts Python's unstamped name %v, which Python itself cannot re-index", pyName)
	}
}

// storeStampedScript applies the reference's own store-file naming rule and body
// rule to the inputs, and reads back the file the port wrote, so both the name and
// the body are compared against the reference's behavior.
const storeStampedScript = `import os, sys
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
from RNS.vendor import umsgpack
import RNS

store_dir, transient_hex, go_name, payload_hex, stamp_hex, stamp_value = sys.argv[1:7]

transient_id = bytes.fromhex(transient_hex)
payload = bytes.fromhex(payload_hex)
stamp_data = bytes.fromhex(stamp_hex)
stamp_value = int(stamp_value)

# The reference's own naming rule (LXMRouter.py:2513-2515).
value_component = f"_{stamp_value}" if stamp_value and stamp_value > 0 else ""
received = float(go_name.split("_")[1])
py_name = f"{RNS.hexrep(transient_id, delimit=False)}_{received}{value_component}"

# The reference's own body rule and its read-back assumption: the stored data is
# the packed message followed by the stamp, and serving strips the trailing stamp
# (LXMRouter.py:2512, 1549).
path = os.path.join(store_dir, go_name)
assert os.path.isfile(path), "no store file at %s" % path
data = open(path, "rb").read()
lxmf_data = data[:-32]
assert lxmf_data == payload, "stored body is not the packed message"
assert data[-32:] == stamp_data, "stored body does not end with the stamp"

print(py_name, "body=lxmf_data+stamp")
`

// storeWriteScript writes a store file exactly as the reference would, into the
// directory the port indexes.
const storeWriteScript = `import os, sys, time
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
import RNS

store_dir, transient_hex, payload_hex, stamp_hex, stamp_value = sys.argv[1:6]

transient_id = bytes.fromhex(transient_hex)
payload = bytes.fromhex(payload_hex)
stamp_data = bytes.fromhex(stamp_hex)
stamp_value = int(stamp_value)
received = 1700000000.123456789

value_component = f"_{stamp_value}" if stamp_value and stamp_value > 0 else ""
name = f"{RNS.hexrep(transient_id, delimit=False)}_{received}{value_component}"

os.makedirs(store_dir, exist_ok=True)
with open(os.path.join(store_dir, name), "wb") as f:
    f.write(payload + stamp_data)
print("OK")
`

// storeUnstampedNameScript evaluates the reference's own naming and indexing rules
// for an unstamped message and reports what it produces: the name, its component
// count, and whether the reference's indexer would accept it (LXMRouter.py:568).
const storeUnstampedNameScript = `import sys
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
import RNS

transient_hex, go_name = sys.argv[1:3]
transient_id = bytes.fromhex(transient_hex)
received = float(go_name.split("_")[1])
stamp_value = 0

value_component = f"_{stamp_value}" if stamp_value and stamp_value > 0 else ""
py_name = f"{RNS.hexrep(transient_id, delimit=False)}_{received}{value_component}"
components = py_name.split("_")
indexes = len(components) >= 3 and float(components[1]) > 0 and len(components[0]) == RNS.Identity.HASHLENGTH//8*2
print(py_name, len(components), indexes)
`
