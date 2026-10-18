// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDeliveryDestinationRatchetPathMatchesPython pins WHERE the delivery
// destination's ratchet file lives, which is what lets a Go node and a Python
// node share one router's ratchet state across implementations. Python builds
// it as f"{self.ratchetpath}/{hex}.ratchets" with
// self.ratchetpath = storagepath + "/ratchets" (LXMRouter.py:131,357), and it
// MkdirAll's that directory before enabling ratchets (:353-354). The port must
// agree on the directory, the file name and the ".ratchets" suffix exactly: a
// ratchet file the other implementation cannot find is a ratchet that silently
// never takes effect, or worse, a peer encrypting to a ratchet the node has
// forgotten.
//
// The FILE FORMAT is already interop-tested in both directions by
// TestRatchetFileInteropParity (rns/rns-int-10_test.go); this test covers the
// path and name it does not.
func TestDeliveryDestinationRatchetPathMatchesPython(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	storagePath := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, storagePath)

	dest, err := router.RegisterDeliveryIdentity(mustTestNewIdentity(t, true), "Ratchet Node", nil)
	mustTest(t, err)

	// The router's own storage root is <storagePath>/lxmf, matching Python's
	// `self.storagepath = storagepath+"/lxmf"` (LXMRouter.py:130).
	lxmfRoot := filepath.Join(storagePath, "lxmf")
	wantDir := filepath.Join(lxmfRoot, "ratchets")
	wantPath := filepath.Join(wantDir, hex.EncodeToString(dest.Hash)+".ratchets")

	// The directory is created up front, exactly as Python's makedirs does.
	info, err := os.Stat(wantDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("ratchet directory %v missing after registering the delivery identity: %v", wantDir, err)
	}

	// Enabling ratchets creates a new ratchet file at that exact path.
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("no ratchet file at %v: %v", wantPath, err)
	}

	// The path the destination itself reports must be the same one, so a
	// rotation writes back to the file Python would read.
	if got := dest.RatchetsPath(); got != wantPath {
		t.Errorf("destination ratchets path = %v, want %v", got, wantPath)
	}
}

// TestDeliveryDestinationRatchetsDirectoryCreation pins that the router creates
// the ratchets directory rather than requiring it to pre-exist, mirroring
// Python's `if not os.path.isdir(self.ratchetpath): os.makedirs(...)`
// (LXMRouter.py:353-354). Without it a fresh install — where the storage tree
// has never held a ratchet — cannot enable ratchets at all.
func TestDeliveryDestinationRatchetsDirectoryCreation(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	storagePath := testutils.TempDir(t, tempDirPrefix)
	// A storage root with no ratchets/ subdirectory, as on a first run.
	router := mustTestNewRouter(t, ts, nil, storagePath)

	dest, err := router.RegisterDeliveryIdentity(mustTestNewIdentity(t, true), "Fresh Node", nil)
	mustTest(t, err)

	lxmfRoot := filepath.Join(storagePath, "lxmf")
	ratchetsDir := filepath.Join(lxmfRoot, "ratchets")
	if _, err := os.Stat(ratchetsDir); err != nil {
		t.Fatalf("router did not create %v on a fresh install: %v", ratchetsDir, err)
	}
	if !strings.HasSuffix(dest.RatchetsPath(), ".ratchets") {
		t.Errorf("ratchet file %v lacks the .ratchets suffix Python's f-string appends", dest.RatchetsPath())
	}
}

// TestDeliveryDestinationRatchetFileIsLoadableByPython hands the ratchet file a
// Go router just wrote to the Python reference and requires it to accept the
// signature and recover the same ratchet key. This is the cross-implementation
// half of the path pin: the file has to be at the path Python computes AND be
// valid to Python's loader.
//
// A freshly enabled ratchet file holds an EMPTY ratchet list on both sides:
// Python's _reload_ratchets sets `self.ratchets = []` and persists it when no
// file exists yet (Destination.py:469-473), and the first key appears only on
// rotation. Both states are asserted, because a port that seeded a key here
// would diverge from Python while still passing a "file parses" check.
func TestDeliveryDestinationRatchetFileIsLoadableByPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	ts := rns.NewTransportSystem(nil)
	storagePath := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, storagePath)

	identity := mustTestNewIdentity(t, true)
	dest, err := router.RegisterDeliveryIdentity(identity, "Interop Node", nil)
	mustTest(t, err)

	identityPath := filepath.Join(storagePath, "identity")
	if err := identity.ToFile(identityPath); err != nil {
		t.Fatalf("persisting identity: %v", err)
	}

	ratchetPath := filepath.Join(storagePath, "lxmf", "ratchets", hex.EncodeToString(dest.Hash)+".ratchets")

	// The just-enabled file: validly signed, empty list.
	args := []string{storagePath, identityPath, ratchetPath, "lxmf", "delivery", "empty"}
	if out := testutils.RunPython(t, ratchetLoadScript, args...); !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python rejected the freshly enabled Go ratchet file:\n%v", out)
	}

	// After a rotation the file must carry a usable ratchet key that Python
	// loads, so a peer of either implementation uses the same one.
	if err := dest.RotateRatchets(); err != nil {
		t.Fatalf("RotateRatchets: %v", err)
	}
	args = []string{storagePath, identityPath, ratchetPath, "lxmf", "delivery", "loaded"}
	if out := testutils.RunPython(t, ratchetLoadScript, args...); !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python rejected the rotated Go ratchet file:\n%v", out)
	}
}

// ratchetLoadScript loads the identity and the ratchet file the way LXMRouter
// does — by constructing the delivery destination and calling enable_ratchets on
// the computed path — and asserts Python accepts the file's signature and
// derives the expected number of ratchet keys from it. The final argument is
// "empty" for a freshly enabled store or "loaded" once a key has been rotated
// in. It prints OK on success and raises otherwise.
const ratchetLoadScript = `import os, sys, tempfile
sys.path.insert(0, "/Users/glenn/Library/Python/3.14/lib/python/site-packages")
from RNS.vendor import umsgpack
import RNS

storage, identity_path, ratchet_path, app, aspect, mode = sys.argv[1:7]

# A private, interface-less Reticulum instance, so constructing a Destination
# never touches the operator's own stack.
rns_cfg = os.path.join(storage, "rns_cfg")
os.makedirs(rns_cfg, exist_ok=True)
with open(os.path.join(rns_cfg, "config"), "w") as f:
    f.write("[reticulum]\n  share_instance = No\n  enable_transport = No\n[logging]\n  loglevel = 3\n[interfaces]\n")
RNS.Reticulum(configdir=rns_cfg)

identity = RNS.Identity.from_file(identity_path)
assert identity is not None, "could not load identity"

with open(ratchet_path, "rb") as f:
    persisted = umsgpack.unpackb(f.read())
assert isinstance(persisted, dict), "ratchet file is not a dict: %r" % type(persisted)
assert set(persisted.keys()) == {"signature", "ratchets"}, "keys %r" % sorted(persisted.keys())
assert isinstance(persisted["signature"], bytes), "signature not bytes"
assert isinstance(persisted["ratchets"], bytes), "ratchets not bytes"
assert identity.validate(persisted["signature"], persisted["ratchets"]), "signature does not validate"
ratchets = umsgpack.unpackb(persisted["ratchets"])
assert isinstance(ratchets, list), "ratchets payload is not a list: %r" % type(ratchets)
assert all(isinstance(r, bytes) and len(r) == 32 for r in ratchets), "ratchet keys are not 32-byte private keys"

if mode == "empty":
    assert len(ratchets) == 0, "a freshly enabled ratchet file must hold no keys, got %d" % len(ratchets)
else:
    assert len(ratchets) > 0, "no ratchet keys stored"

# The destination must be able to enable ratchets from this exact path.
dest = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, app, aspect)
assert dest.enable_ratchets(ratchet_path), "enable_ratchets rejected the Go-written file"
assert dest.ratchets is not None, "ratchets not loaded from the file"
assert len(dest.ratchets) == len(ratchets), "destination loaded %d ratchets, file holds %d" % (len(dest.ratchets), len(ratchets))
print("OK")
`
