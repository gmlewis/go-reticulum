// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package lxmf

import (
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

const lxmfGenerateOfferRequestPy = `import RNS.vendor.umsgpack as msgpack
import sys

if len(sys.argv) != 4:
    print("ERROR: missing args")
    sys.exit(1)

out_path = sys.argv[1]
have_id = bytes.fromhex(sys.argv[2])
want_id = bytes.fromhex(sys.argv[3])

payload = [b"peering-key", [have_id, want_id]]
with open(out_path, "wb") as f:
    f.write(msgpack.packb(payload))
`

const lxmfGenerateInvalidOfferRequestPy = `import RNS.vendor.umsgpack as msgpack
import sys

if len(sys.argv) != 3:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
want_id = bytes.fromhex(sys.argv[2])

payload = [None, [want_id]]
with open(out_path, "wb") as f:
	f.write(msgpack.packb(payload))
`

const lxmfRunMessageGetPy = `import LXMF
import LXMF.LXStamper as LXStamper
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import time

if len(sys.argv) != 7:
    print("ERROR: missing args")
    sys.exit(1)

request_path = sys.argv[1]
response_path = sys.argv[2]
store_dir = sys.argv[3]
id_one = bytes.fromhex(sys.argv[4])
id_two = bytes.fromhex(sys.argv[5])
payload_two = bytes.fromhex(sys.argv[6])

config_dir = os.path.join(store_dir, "rnsconfig")
if not os.path.exists(config_dir): os.makedirs(config_dir)
with open(os.path.join(config_dir, "config"), "w") as f:
    f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath=store_dir)
remote_identity = RNS.Identity()
remote_destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")

stamp = bytes(LXStamper.STAMP_SIZE)
entry_one_path = os.path.join(store_dir, "entry-one.msg")
entry_two_path = os.path.join(store_dir, "entry-two.msg")

with open(entry_one_path, "wb") as f:
    f.write(b"payload-one" + stamp)
with open(entry_two_path, "wb") as f:
    f.write(payload_two + stamp)

router.propagation_entries[id_one] = [remote_destination.hash, entry_one_path]
router.propagation_entries[id_two] = [remote_destination.hash, entry_two_path]

with open(request_path, "rb") as f:
    request_data = msgpack.unpackb(f.read())

response = router.message_get_request("", request_data, None, remote_identity, time.time())

with open(response_path, "wb") as f:
    f.write(msgpack.packb(response))
`

const lxmfRunMessageGetRetryPy = `import LXMF
import LXMF.LXStamper as LXStamper
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import time

if len(sys.argv) != 6:
	print("ERROR: missing args")
	sys.exit(1)

response_low_path = sys.argv[1]
response_high_path = sys.argv[2]
store_dir = sys.argv[3]
id_small = bytes.fromhex(sys.argv[4])
id_large = bytes.fromhex(sys.argv[5])

config_dir = os.path.join(store_dir, "rnsconfig")
if not os.path.exists(config_dir): os.makedirs(config_dir)
with open(os.path.join(config_dir, "config"), "w") as f:
    f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath=store_dir)
remote_identity = RNS.Identity()
remote_destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")

stamp = bytes(LXStamper.STAMP_SIZE)
small_payload = b"small"
large_payload = b"L" * 350

small_path = os.path.join(store_dir, "small.msg")
large_path = os.path.join(store_dir, "large.msg")

with open(small_path, "wb") as f:
	f.write(small_payload + stamp)
with open(large_path, "wb") as f:
	f.write(large_payload + stamp)

router.propagation_entries[id_small] = [remote_destination.hash, small_path]
router.propagation_entries[id_large] = [remote_destination.hash, large_path]

request_low = [[id_small, id_large], None, 0.1]
request_high = [[id_small, id_large], None, 5.0]

response_low = router.message_get_request("", request_low, None, remote_identity, time.time())
response_high = router.message_get_request("", request_high, None, remote_identity, time.time())

with open(response_low_path, "wb") as f:
	f.write(msgpack.packb(response_low))
with open(response_high_path, "wb") as f:
	f.write(msgpack.packb(response_high))
`

const lxmfRunMessageGetAccessPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp")
router.auth_required = True

allowed_identity = RNS.Identity()
not_allowed_identity = RNS.Identity()
request_payload = [None, None]

responses = []
responses.append(router.message_get_request("", request_payload, None, None, time.time()))
responses.append(router.message_get_request("", request_payload, None, not_allowed_identity, time.time()))

router.allowed_list = [allowed_identity.hash]
allowed_response = router.message_get_request("", request_payload, None, allowed_identity, time.time())
responses.append(isinstance(allowed_response, list))
responses.append(len(allowed_response))

with open(out_path, "wb") as f:
	f.write(msgpack.packb(responses))
`

const lxmfRunMessageGetPurgeRetryPy = `import LXMF
import LXMF.LXStamper as LXStamper
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import time

if len(sys.argv) != 6:
	print("ERROR: missing args")
	sys.exit(1)

response_first_path = sys.argv[1]
response_second_path = sys.argv[2]
response_meta_path = sys.argv[3]
store_dir = sys.argv[4]
id_one = bytes.fromhex(sys.argv[5])

config_dir = os.path.join(store_dir, "rnsconfig")
if not os.path.exists(config_dir): os.makedirs(config_dir)
with open(os.path.join(config_dir, "config"), "w") as f:
    f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath=store_dir)
remote_identity = RNS.Identity()
remote_destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "delivery")

stamp = bytes(LXStamper.STAMP_SIZE)
payload_one = b"purge-me"
payload_two = b"keep-me"

entry_one_path = os.path.join(store_dir, "purge-one.msg")
entry_two_path = os.path.join(store_dir, "purge-two.msg")

with open(entry_one_path, "wb") as f:
	f.write(payload_one + stamp)
with open(entry_two_path, "wb") as f:
	f.write(payload_two + stamp)

id_two = RNS.Identity.full_hash(b"purge-retry-two")
router.propagation_entries[id_one] = [remote_destination.hash, entry_one_path]
router.propagation_entries[id_two] = [remote_destination.hash, entry_two_path]

first_request = [[id_one, id_two], [id_one], 10.0]
first_response = router.message_get_request("", first_request, None, remote_identity, time.time())

second_request = [[id_one], None, 10.0]
second_response = router.message_get_request("", second_request, None, remote_identity, time.time())

with open(response_first_path, "wb") as f:
	f.write(msgpack.packb(first_response))
with open(response_second_path, "wb") as f:
	f.write(msgpack.packb(second_response))
with open(response_meta_path, "wb") as f:
	f.write(msgpack.packb(len(router.propagation_entries)))
`

const lxmfGenerateRetryRequestsPy = `import RNS.vendor.umsgpack as msgpack
import sys

if len(sys.argv) != 5:
	print("ERROR: missing args")
	sys.exit(1)

low_path = sys.argv[1]
high_path = sys.argv[2]
id_small = bytes.fromhex(sys.argv[3])
id_large = bytes.fromhex(sys.argv[4])

low_request = [[id_small, id_large], None, 0.1]
high_request = [[id_small, id_large], None, 5.0]

with open(low_path, "wb") as f:
	f.write(msgpack.packb(low_request))
with open(high_path, "wb") as f:
	f.write(msgpack.packb(high_request))
`

const lxmfRunControlRecoveryPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp")
remote_identity = RNS.Identity()
router.control_allowed_list = [remote_identity.hash]

peer_hash = RNS.Identity().hash

class FakePeer:
	def __init__(self):
		self.sync_calls = 0
		self.peering_timebase = 0
		self.destination = None
	def sync(self):
		self.sync_calls += 1
	def queued_items(self):
		return 0

responses = []
responses.append(router.peer_sync_request("", peer_hash, None, remote_identity, time.time()))
router.peers[peer_hash] = FakePeer()
responses.append(router.peer_sync_request("", peer_hash, None, remote_identity, time.time()))
responses.append(router.peer_unpeer_request("", peer_hash, None, remote_identity, time.time()))
responses.append(router.peer_unpeer_request("", peer_hash, None, remote_identity, time.time()))

with open(out_path, "wb") as f:
	f.write(msgpack.packb(responses))
`

const lxmfRunControlPeerErrorsPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp")

allowed_identity = RNS.Identity()
not_allowed_identity = RNS.Identity()
router.control_allowed_list = [allowed_identity.hash]

peer_hash = RNS.Identity().hash

class FakePeer:
	def __init__(self):
		self.sync_calls = 0
		self.peering_timebase = 0
		self.destination = None
	def sync(self):
		self.sync_calls += 1
	def queued_items(self):
		return 0

responses = []
responses.append(router.peer_sync_request("", peer_hash, None, None, time.time()))
responses.append(router.peer_sync_request("", peer_hash, None, not_allowed_identity, time.time()))
responses.append(router.peer_sync_request("", b"short", None, allowed_identity, time.time()))
responses.append(router.peer_unpeer_request("", b"short", None, allowed_identity, time.time()))
responses.append(router.peer_sync_request("", peer_hash, None, allowed_identity, time.time()))

router.peers[peer_hash] = FakePeer()
responses.append(router.peer_sync_request("", peer_hash, None, allowed_identity, time.time()))
responses.append(router.peers[peer_hash].sync_calls)
responses.append(router.peer_unpeer_request("", peer_hash, None, allowed_identity, time.time()))
responses.append(router.peer_unpeer_request("", peer_hash, None, allowed_identity, time.time()))

with open(out_path, "wb") as f:
	f.write(msgpack.packb(responses))
`

const lxmfGenerateControlPeerHashPy = `import RNS
import sys

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
peer_hash = RNS.Identity().hash

with open(out_path, "wb") as f:
	f.write(peer_hash)
`

const lxmfGenerateTwoControlPeerHashesPy = `import RNS
import sys

if len(sys.argv) != 3:
	print("ERROR: missing args")
	sys.exit(1)

first_path = sys.argv[1]
second_path = sys.argv[2]

with open(first_path, "wb") as f:
	f.write(RNS.Identity().hash)
with open(second_path, "wb") as f:
	f.write(RNS.Identity().hash)
`

const lxmfRunControlStatsPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp")
router.propagation_node = True
router.propagation_node_start_time = time.time()
allowed_identity = RNS.Identity()
not_allowed_identity = RNS.Identity()
router.control_allowed_list = [allowed_identity.hash]

responses = []
responses.append(router.stats_get_request("", None, None, None, time.time()))
responses.append(router.stats_get_request("", None, None, not_allowed_identity, time.time()))
allowed_response = router.stats_get_request("", None, None, allowed_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb([responses[0], responses[1], allowed_response]))
`

const lxmfGenerateControlAllowedHashesPy = `import RNS
import sys

if len(sys.argv) != 3:
	print("ERROR: missing args")
	sys.exit(1)

allowed_path = sys.argv[1]
not_allowed_path = sys.argv[2]

allowed_identity = RNS.Identity()
not_allowed_identity = RNS.Identity()

with open(allowed_path, "wb") as f:
	f.write(allowed_identity.hash)
with open(not_allowed_path, "wb") as f:
	f.write(not_allowed_identity.hash)
`

const lxmfRunOfferInvalidKeyPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp")
remote_identity = RNS.Identity()
transient_id = RNS.Identity.full_hash(b"offer-invalid-key")

response = router.offer_request("", [b"", [transient_id]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfRunOfferThrottledPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp")
remote_identity = RNS.Identity()
remote_destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "propagation")
router.throttled_peers[remote_destination.hash] = time.time()+60

transient_id = RNS.Identity.full_hash(b"offer-throttled")
response = router.offer_request("", [b"key", [transient_id]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfRunOfferThrottleExpiredPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp", peering_cost=0)
remote_identity = RNS.Identity()
remote_destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "propagation")
router.throttled_peers[remote_destination.hash] = time.time()-1

transient_id = RNS.Identity.full_hash(b"offer-throttle-expired")
response = router.offer_request("", [b"key", [transient_id]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfRunOfferStaticOnlyPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp", from_static_only=True)
remote_identity = RNS.Identity()

transient_id = RNS.Identity.full_hash(b"offer-static-only")
response = router.offer_request("", [b"key", [transient_id]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfRunOfferStaticAllowedPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
remote_identity = RNS.Identity()
remote_destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, LXMF.APP_NAME, "propagation")
router = LXMF.LXMRouter(storagepath="/tmp", from_static_only=True, static_peers=[remote_destination.hash], peering_cost=0)

transient_id = RNS.Identity.full_hash(b"offer-static-allowed")
response = router.offer_request("", [b"key", [transient_id]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfRunOfferAllKnownPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp", peering_cost=0)
remote_identity = RNS.Identity()

id_one = RNS.Identity.full_hash(b"offer-all-known-one")
id_two = RNS.Identity.full_hash(b"offer-all-known-two")

router.propagation_entries[id_one] = [b"dest-hash-123456", "/tmp/nonexistent-a"]
router.propagation_entries[id_two] = [b"dest-hash-123456", "/tmp/nonexistent-b"]

response = router.offer_request("", [b"key", [id_one, id_two]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfRunOfferAllMissingPy = `import LXMF
import RNS
import RNS.vendor.umsgpack as msgpack
import os
import sys
import tempfile
import time

if len(sys.argv) != 2:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
config_dir = tempfile.mkdtemp()
with open(os.path.join(config_dir, "config"), "w") as f:
	f.write("[reticulum]\nshare_instance = No\n")
RNS.Reticulum(configdir=config_dir)
router = LXMF.LXMRouter(storagepath="/tmp", peering_cost=0)
remote_identity = RNS.Identity()

id_one = RNS.Identity.full_hash(b"offer-all-missing-one")
id_two = RNS.Identity.full_hash(b"offer-all-missing-two")

response = router.offer_request("", [b"key", [id_one, id_two]], None, b"link-id", remote_identity, time.time())

with open(out_path, "wb") as f:
	f.write(msgpack.packb(response))
`

const lxmfValidatePeeringKeyPy = `import LXMF.LXStamper as LXStamper
import RNS.vendor.umsgpack as msgpack
import sys

if len(sys.argv) != 5:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
peering_id = bytes.fromhex(sys.argv[2])
peering_key = bytes.fromhex(sys.argv[3])
target_cost = int(sys.argv[4])

result = LXStamper.validate_peering_key(peering_id, peering_key, target_cost)

with open(out_path, "wb") as f:
	f.write(msgpack.packb(result))
`

const lxmfGeneratePeeringKeyPy = `import LXMF.LXStamper as LXStamper
import RNS.vendor.umsgpack as msgpack
import sys

if len(sys.argv) != 4:
	print("ERROR: missing args")
	sys.exit(1)

out_path = sys.argv[1]
peering_id = bytes.fromhex(sys.argv[2])
target_cost = int(sys.argv[3])

stamp, value = LXStamper.generate_stamp(peering_id, target_cost, expand_rounds=LXStamper.WORKBLOCK_EXPAND_ROUNDS_PEERING)

with open(out_path, "wb") as f:
	f.write(msgpack.packb([stamp, value]))
`

func TestIntegrationPropagationOfferPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	destinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	haveID := router.storePropagationMessage(destinationHash, []byte("stored-message"))
	wantID := rns.FullHash([]byte("wanted-message"))

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(haveID), hex.EncodeToString(wantID))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read python-generated request: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	wanted, ok := response.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", response)
	}
	if len(wanted) != 1 {
		t.Fatalf("wanted len=%v want=1", len(wanted))
	}
	wantedID, ok := wanted[0].([]byte)
	if !ok {
		t.Fatalf("unexpected wanted id type %T", wanted[0])
	}
	if string(wantedID) != string(wantID) {
		t.Fatalf("wanted id mismatch got=%x want=%x", wantedID, wantID)
	}
}

func TestIntegrationPropagationOfferInvalidKeyPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_invalid_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateInvalidOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	wantID := rns.FullHash([]byte("invalid-key-wanted"))
	requestPath := filepath.Join(tmpDir, "invalid_offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(wantID))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python invalid-offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != peerErrorInvalidKey {
		t.Fatalf("response=%v want=%v", response, peerErrorInvalidKey)
	}
}

func TestIntegrationPropagationOfferThrottledPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.throttledPeers[string(remotePropagationHash)] = now.Add(time.Minute)

	haveID := rns.FullHash([]byte("have-throttled"))
	wantID := rns.FullHash([]byte("want-throttled"))

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(haveID), hex.EncodeToString(wantID))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, now)
	if response != peerErrorThrottled {
		t.Fatalf("response=%v want=%v", response, peerErrorThrottled)
	}
}

func TestIntegrationPropagationOfferThrottleExpiredPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.throttledPeers[string(remotePropagationHash)] = now.Add(-time.Second)

	haveID := rns.FullHash([]byte("have-expired-throttle"))
	wantID := rns.FullHash([]byte("want-expired-throttle"))

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(haveID), hex.EncodeToString(wantID))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, now)
	if response != true {
		t.Fatalf("response=%v want=true", response)
	}
	if _, exists := router.throttledPeers[string(remotePropagationHash)]; exists {
		t.Fatal("expected expired throttled peer to be removed")
	}
}

func TestIntegrationPropagationOfferStaticOnlyPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.fromStaticOnly = true

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	haveID := rns.FullHash([]byte("have-static"))
	wantID := rns.FullHash([]byte("want-static"))

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(haveID), hex.EncodeToString(wantID))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != peerErrorNoAccess {
		t.Fatalf("response=%v want=%v", response, peerErrorNoAccess)
	}
}

func TestIntegrationPropagationOfferStaticAllowedPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.fromStaticOnly = true

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
	remotePropagationHash := rns.CalculateHash(remoteIdentity, AppName, "propagation")
	router.staticPeers[string(remotePropagationHash)] = struct{}{}

	haveID := rns.FullHash([]byte("have-static-allowed"))
	wantID := rns.FullHash([]byte("want-static-allowed"))

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(haveID), hex.EncodeToString(wantID))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != true {
		t.Fatalf("response=%v want=true", response)
	}
}

func TestIntegrationPropagationOfferAllKnownPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	idOne := rns.FullHash([]byte("offer-all-known-one"))
	idTwo := rns.FullHash([]byte("offer-all-known-two"))
	router.propagationEntries[string(idOne)] = &propagationEntry{payload: []byte("known-one"), receivedAt: time.Now()}
	router.propagationEntries[string(idTwo)] = &propagationEntry{payload: []byte("known-two"), receivedAt: time.Now()}

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(idOne), hex.EncodeToString(idTwo))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != false {
		t.Fatalf("response=%v want=false", response)
	}
}

func TestIntegrationPropagationOfferAllMissingPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_offer_request.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateOfferRequestPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	idOne := rns.FullHash([]byte("offer-all-missing-one"))
	idTwo := rns.FullHash([]byte("offer-all-missing-two"))

	requestPath := filepath.Join(tmpDir, "offer_request.msgpack")
	cmd := exec.Command("python3", scriptPath, requestPath, hex.EncodeToString(idOne), hex.EncodeToString(idTwo))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer request generation failed: %v output=%v", err, string(out))
	}

	requestData, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read request data: %v", err)
	}

	response := router.offerRequest("", requestData, nil, []byte("link"), remoteIdentity, time.Now())
	if response != true {
		t.Fatalf("response=%v want=true", response)
	}
}

func TestIntegrationPropagationMessageGetGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_message_get.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunMessageGetPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	idOne := rns.FullHash([]byte("message-one"))
	idTwo := rns.FullHash([]byte("message-two"))
	payloadTwo := []byte("payload-two")

	requestPayload := []any{[]any{idOne, idTwo}, []any{idOne}, float64(100)}
	requestData, err := msgpack.Pack(requestPayload)
	if err != nil {
		t.Fatalf("Pack request payload: %v", err)
	}

	requestPath := filepath.Join(tmpDir, "message_get_request.msgpack")
	if err := os.WriteFile(requestPath, requestData, 0o644); err != nil {
		t.Fatalf("write request payload: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "message_get_response.msgpack")
	storeDir := filepath.Join(tmpDir, "py-store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}

	cmd := exec.Command(
		"python3",
		scriptPath,
		requestPath,
		responsePath,
		storeDir,
		hex.EncodeToString(idOne),
		hex.EncodeToString(idTwo),
		hex.EncodeToString(payloadTwo),
	)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python message_get execution failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response payload: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response payload: %v", err)
	}

	messages, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", unpacked)
	}
	if len(messages) != 1 {
		t.Fatalf("response len=%v want=1", len(messages))
	}

	gotPayload, ok := messages[0].([]byte)
	if !ok {
		t.Fatalf("unexpected payload type %T", messages[0])
	}
	if string(gotPayload) != string(payloadTwo) {
		t.Fatalf("payload mismatch got=%x want=%x", gotPayload, payloadTwo)
	}
}

func TestIntegrationPropagationMessageGetRetryGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_message_get_retry.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunMessageGetRetryPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	idSmall := rns.FullHash([]byte("retry-small"))
	idLarge := rns.FullHash([]byte("retry-large"))

	responseLowPath := filepath.Join(tmpDir, "response_low.msgpack")
	responseHighPath := filepath.Join(tmpDir, "response_high.msgpack")
	storeDir := filepath.Join(tmpDir, "py-store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}

	cmd := exec.Command(
		"python3",
		scriptPath,
		responseLowPath,
		responseHighPath,
		storeDir,
		hex.EncodeToString(idSmall),
		hex.EncodeToString(idLarge),
	)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python retry flow failed: %v output=%v", err, string(out))
	}

	lowData, err := os.ReadFile(responseLowPath)
	if err != nil {
		t.Fatalf("read low response: %v", err)
	}
	highData, err := os.ReadFile(responseHighPath)
	if err != nil {
		t.Fatalf("read high response: %v", err)
	}

	lowUnpacked, err := msgpack.Unpack(lowData)
	if err != nil {
		t.Fatalf("Unpack low response: %v", err)
	}
	highUnpacked, err := msgpack.Unpack(highData)
	if err != nil {
		t.Fatalf("Unpack high response: %v", err)
	}

	lowMessages, ok := lowUnpacked.([]any)
	if !ok {
		t.Fatalf("unexpected low response type %T", lowUnpacked)
	}
	highMessages, ok := highUnpacked.([]any)
	if !ok {
		t.Fatalf("unexpected high response type %T", highUnpacked)
	}

	if len(lowMessages) != 1 {
		t.Fatalf("low response len=%v want=1", len(lowMessages))
	}
	if len(highMessages) != 2 {
		t.Fatalf("high response len=%v want=2", len(highMessages))
	}
}

func TestIntegrationPropagationMessageGetPurgeRetryGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_message_get_purge_retry.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunMessageGetPurgeRetryPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	idOne := rns.FullHash([]byte("purge-retry-one"))
	responseFirstPath := filepath.Join(tmpDir, "response_first.msgpack")
	responseSecondPath := filepath.Join(tmpDir, "response_second.msgpack")
	responseMetaPath := filepath.Join(tmpDir, "response_meta.msgpack")
	storeDir := filepath.Join(tmpDir, "py-store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}

	cmd := exec.Command(
		"python3",
		scriptPath,
		responseFirstPath,
		responseSecondPath,
		responseMetaPath,
		storeDir,
		hex.EncodeToString(idOne),
	)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python purge/retry flow failed: %v output=%v", err, string(out))
	}

	firstData, err := os.ReadFile(responseFirstPath)
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	secondData, err := os.ReadFile(responseSecondPath)
	if err != nil {
		t.Fatalf("read second response: %v", err)
	}
	metaData, err := os.ReadFile(responseMetaPath)
	if err != nil {
		t.Fatalf("read meta response: %v", err)
	}

	firstUnpacked, err := msgpack.Unpack(firstData)
	if err != nil {
		t.Fatalf("Unpack first response: %v", err)
	}
	secondUnpacked, err := msgpack.Unpack(secondData)
	if err != nil {
		t.Fatalf("Unpack second response: %v", err)
	}
	metaUnpacked, err := msgpack.Unpack(metaData)
	if err != nil {
		t.Fatalf("Unpack meta response: %v", err)
	}

	firstMessages, ok := firstUnpacked.([]any)
	if !ok {
		t.Fatalf("unexpected first response type %T", firstUnpacked)
	}
	secondMessages, ok := secondUnpacked.([]any)
	if !ok {
		t.Fatalf("unexpected second response type %T", secondUnpacked)
	}
	remainingCount, ok := metaUnpacked.(int64)
	if !ok {
		t.Fatalf("unexpected meta response type %T", metaUnpacked)
	}

	if len(firstMessages) != 1 {
		t.Fatalf("first response len=%v want=1", len(firstMessages))
	}
	firstPayload, ok := firstMessages[0].([]byte)
	if !ok {
		t.Fatalf("unexpected first payload type %T", firstMessages[0])
	}
	if string(firstPayload) != "keep-me" {
		t.Fatalf("first payload=%q want=%q", string(firstPayload), "keep-me")
	}
	if len(secondMessages) != 0 {
		t.Fatalf("second response len=%v want=0", len(secondMessages))
	}
	if remainingCount != 1 {
		t.Fatalf("remaining propagation entries=%v want=1", remainingCount)
	}
}

func TestIntegrationPropagationMessageGetAccessGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_message_get_access.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunMessageGetAccessPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "message_get_access_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python message_get access flow failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	responses, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", unpacked)
	}
	if len(responses) != 4 {
		t.Fatalf("response len=%v want=4", len(responses))
	}
	if responses[0] != uint64(peerErrorNoIdentity) {
		t.Fatalf("no identity response=%v want=%v", responses[0], peerErrorNoIdentity)
	}
	if responses[1] != uint64(peerErrorNoAccess) {
		t.Fatalf("no access response=%v want=%v", responses[1], peerErrorNoAccess)
	}
	if responses[2] != true {
		t.Fatalf("allowed list type-check response=%v want=true", responses[2])
	}
	if responses[3] != int64(0) {
		t.Fatalf("allowed list length response=%v want=0 (type %T)", responses[3], responses[3])
	}
}

func TestIntegrationPropagationMessageGetRetryPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_retry_requests.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateRetryRequestsPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	destinationHash := rns.CalculateHash(remoteIdentity, AppName, "delivery")
	idSmall := router.storePropagationMessage(destinationHash, []byte("small"))
	idLarge := router.storePropagationMessage(destinationHash, []byte("LLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLLL"))

	lowPath := filepath.Join(tmpDir, "low_request.msgpack")
	highPath := filepath.Join(tmpDir, "high_request.msgpack")

	cmd := exec.Command(
		"python3",
		scriptPath,
		lowPath,
		highPath,
		hex.EncodeToString(idSmall),
		hex.EncodeToString(idLarge),
	)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python request generation failed: %v output=%v", err, string(out))
	}

	lowRequest, err := os.ReadFile(lowPath)
	if err != nil {
		t.Fatalf("read low request: %v", err)
	}
	highRequest, err := os.ReadFile(highPath)
	if err != nil {
		t.Fatalf("read high request: %v", err)
	}

	lowResp := router.messageGetRequest("", lowRequest, nil, nil, remoteIdentity, time.Now())
	highResp := router.messageGetRequest("", highRequest, nil, nil, remoteIdentity, time.Now())

	lowMessages, ok := lowResp.([]any)
	if !ok {
		t.Fatalf("unexpected low response type %T", lowResp)
	}
	highMessages, ok := highResp.([]any)
	if !ok {
		t.Fatalf("unexpected high response type %T", highResp)
	}

	if len(lowMessages) != 1 {
		t.Fatalf("low response len=%v want=1", len(lowMessages))
	}
	if len(highMessages) != 2 {
		t.Fatalf("high response len=%v want=2", len(highMessages))
	}
}

func TestIntegrationPropagationMessageGetAccessPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_control_allowed_hashes.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateControlAllowedHashesPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	allowedPath := filepath.Join(tmpDir, "allowed_hash.bin")
	notAllowedPath := filepath.Join(tmpDir, "not_allowed_hash.bin")
	cmd := exec.Command("python3", scriptPath, allowedPath, notAllowedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python hash generation failed: %v output=%v", err, string(out))
	}

	allowedHash, err := os.ReadFile(allowedPath)
	if err != nil {
		t.Fatalf("read allowed hash: %v", err)
	}
	notAllowedHash, err := os.ReadFile(notAllowedPath)
	if err != nil {
		t.Fatalf("read not-allowed hash: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.SetAuthRequired(true)
	if err := router.SetAllowedList([][]byte{allowedHash}); err != nil {
		t.Fatalf("SetAllowedList: %v", err)
	}

	request, err := msgpack.Pack([]any{nil, nil})
	if err != nil {
		t.Fatalf("Pack request: %v", err)
	}

	if got := router.messageGetRequest("", request, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("message_get no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	notAllowedIdentity := &rns.Identity{Hash: append([]byte{}, notAllowedHash...)}
	if got := router.messageGetRequest("", request, nil, nil, notAllowedIdentity, time.Now()); got != peerErrorNoAccess {
		t.Fatalf("message_get no access=%v want=%v", got, peerErrorNoAccess)
	}

	allowedIdentity := &rns.Identity{Hash: append([]byte{}, allowedHash...)}
	allowedResponse := router.messageGetRequest("", request, nil, nil, allowedIdentity, time.Now())
	if _, ok := allowedResponse.([]any); !ok {
		t.Fatalf("allowed response type=%T want=[]any", allowedResponse)
	}
}

func TestIntegrationPropagationControlRecoveryGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_control_recovery.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunControlRecoveryPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "control_recovery_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python control recovery flow failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	responses, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", unpacked)
	}
	if len(responses) != 4 {
		t.Fatalf("response len=%v want=4", len(responses))
	}

	if responses[0] != uint64(peerErrorNotFound) {
		t.Fatalf("sync before peer=%v want=%v", responses[0], peerErrorNotFound)
	}
	if responses[1] != true {
		t.Fatalf("sync with peer=%v want=true", responses[1])
	}
	if responses[2] != true {
		t.Fatalf("first unpeer=%v want=true", responses[2])
	}
	if responses[3] != uint64(peerErrorNotFound) {
		t.Fatalf("second unpeer=%v want=%v", responses[3], peerErrorNotFound)
	}
}

func TestIntegrationPropagationControlPeerErrorsGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_control_peer_errors.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunControlPeerErrorsPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "control_peer_errors_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python control peer errors flow failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	responses, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", unpacked)
	}
	if len(responses) != 9 {
		t.Fatalf("response len=%v want=9", len(responses))
	}

	if responses[0] != uint64(peerErrorNoIdentity) {
		t.Fatalf("sync no identity=%v want=%v", responses[0], peerErrorNoIdentity)
	}
	if responses[1] != uint64(peerErrorNoAccess) {
		t.Fatalf("sync no access=%v want=%v", responses[1], peerErrorNoAccess)
	}
	if responses[2] != uint64(peerErrorInvalidData) {
		t.Fatalf("sync invalid data=%v want=%v", responses[2], peerErrorInvalidData)
	}
	if responses[3] != uint64(peerErrorInvalidData) {
		t.Fatalf("unpeer invalid data=%v want=%v", responses[3], peerErrorInvalidData)
	}
	if responses[4] != uint64(peerErrorNotFound) {
		t.Fatalf("sync not found=%v want=%v", responses[4], peerErrorNotFound)
	}
	if responses[5] != true {
		t.Fatalf("sync existing peer=%v want=true", responses[5])
	}
	if responses[6] != int64(1) {
		t.Fatalf("sync call count=%v want=1", responses[6])
	}
	if responses[7] != true {
		t.Fatalf("first unpeer=%v want=true", responses[7])
	}
	if responses[8] != uint64(peerErrorNotFound) {
		t.Fatalf("second unpeer=%v want=%v", responses[8], peerErrorNotFound)
	}
}

func TestIntegrationPropagationControlRecoveryPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_control_peer_hash.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateControlPeerHashPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	peerHashPath := filepath.Join(tmpDir, "peer_hash.bin")
	cmd := exec.Command("python3", scriptPath, peerHashPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python peer hash generation failed: %v output=%v", err, string(out))
	}

	peerHash, err := os.ReadFile(peerHashPath)
	if err != nil {
		t.Fatalf("read peer hash: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if got := router.peerSyncRequest("", peerHash, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("sync no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
	router.controlAllowed[string(remoteIdentity.Hash)] = struct{}{}

	if got := router.peerSyncRequest("", []byte("short"), nil, nil, remoteIdentity, time.Now()); got != peerErrorInvalidData {
		t.Fatalf("sync invalid data=%v want=%v", got, peerErrorInvalidData)
	}

	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != peerErrorNotFound {
		t.Fatalf("sync not found=%v want=%v", got, peerErrorNotFound)
	}

	router.peers[string(peerHash)] = time.Now().Add(-time.Hour)
	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != true {
		t.Fatalf("sync existing peer=%v want=true", got)
	}
	if got := router.peerUnpeerRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != true {
		t.Fatalf("unpeer existing peer=%v want=true", got)
	}
	if got := router.peerUnpeerRequest("", peerHash, nil, nil, remoteIdentity, time.Now()); got != peerErrorNotFound {
		t.Fatalf("unpeer not found=%v want=%v", got, peerErrorNotFound)
	}
}

func TestIntegrationPropagationControlPeerSyncBackoffPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_control_peer_hash.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateControlPeerHashPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	peerHashPath := filepath.Join(tmpDir, "peer_hash.bin")
	cmd := exec.Command("python3", scriptPath, peerHashPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python peer hash generation failed: %v output=%v", err, string(out))
	}

	peerHash, err := os.ReadFile(peerHashPath)
	if err != nil {
		t.Fatalf("read peer hash: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	if err := router.SetPeerSyncBackoff(10 * time.Second); err != nil {
		t.Fatalf("SetPeerSyncBackoff: %v", err)
	}

	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}
	router.controlAllowed[string(remoteIdentity.Hash)] = struct{}{}
	router.peers[string(peerHash)] = now

	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, now); got != peerErrorThrottled {
		t.Fatalf("sync throttled=%v want=%v", got, peerErrorThrottled)
	}

	now = now.Add(11 * time.Second)
	if got := router.peerSyncRequest("", peerHash, nil, nil, remoteIdentity, now); got != true {
		t.Fatalf("sync after backoff=%v want=true", got)
	}
}

func TestIntegrationPropagationControlPeerPrunePythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_two_control_peer_hashes.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateTwoControlPeerHashesPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	oldHashPath := filepath.Join(tmpDir, "peer_old_hash.bin")
	newHashPath := filepath.Join(tmpDir, "peer_new_hash.bin")
	cmd := exec.Command("python3", scriptPath, oldHashPath, newHashPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python peer hash generation failed: %v output=%v", err, string(out))
	}

	peerOld, err := os.ReadFile(oldHashPath)
	if err != nil {
		t.Fatalf("read old peer hash: %v", err)
	}
	peerNew, err := os.ReadFile(newHashPath)
	if err != nil {
		t.Fatalf("read new peer hash: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	now := time.Unix(1700000000, 0)
	router.now = func() time.Time { return now }
	if err := router.SetPeerMaxAge(30 * time.Second); err != nil {
		t.Fatalf("SetPeerMaxAge: %v", err)
	}

	router.peers[string(peerOld)] = now.Add(-2 * time.Minute)
	router.peers[string(peerNew)] = now.Add(-10 * time.Second)

	removed := router.PruneStalePeers()
	if removed != 1 {
		t.Fatalf("removed=%v want=1", removed)
	}
	if _, ok := router.peers[string(peerOld)]; ok {
		t.Fatal("expected old peer removed")
	}
	if _, ok := router.peers[string(peerNew)]; !ok {
		t.Fatal("expected recent peer retained")
	}
}

func TestIntegrationPropagationControlStatsGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_control_stats.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunControlStatsPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "control_stats_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python control stats flow failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	responses, ok := unpacked.([]any)
	if !ok {
		t.Fatalf("unexpected response type %T", unpacked)
	}
	if len(responses) != 3 {
		t.Fatalf("response len=%v want=3", len(responses))
	}
	if responses[0] != uint64(peerErrorNoIdentity) {
		t.Fatalf("stats no identity=%v want=%v", responses[0], peerErrorNoIdentity)
	}
	if responses[1] != uint64(peerErrorNoAccess) {
		t.Fatalf("stats no access=%v want=%v", responses[1], peerErrorNoAccess)
	}

	var foundTotalPeers bool
	switch s := responses[2].(type) {
	case map[string]any:
		_, foundTotalPeers = s["total_peers"]
	case map[any]any:
		_, foundTotalPeers = s["total_peers"]
	default:
		t.Fatalf("allowed stats response type=%T want map", responses[2])
	}
	if !foundTotalPeers {
		t.Fatalf("allowed stats missing total_peers: %v", responses[2])
	}
}

func TestIntegrationPropagationControlStatsPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_control_allowed_hashes.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGenerateControlAllowedHashesPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	allowedPath := filepath.Join(tmpDir, "allowed_hash.bin")
	notAllowedPath := filepath.Join(tmpDir, "not_allowed_hash.bin")
	cmd := exec.Command("python3", scriptPath, allowedPath, notAllowedPath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python hash generation failed: %v output=%v", err, string(out))
	}

	allowedHash, err := os.ReadFile(allowedPath)
	if err != nil {
		t.Fatalf("read allowed hash: %v", err)
	}
	notAllowedHash, err := os.ReadFile(notAllowedPath)
	if err != nil {
		t.Fatalf("read not-allowed hash: %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	if got := router.statsGetRequest("", nil, nil, nil, nil, time.Now()); got != peerErrorNoIdentity {
		t.Fatalf("stats no identity=%v want=%v", got, peerErrorNoIdentity)
	}

	notAllowedIdentity := &rns.Identity{Hash: append([]byte{}, notAllowedHash...)}
	router.controlAllowed[string(allowedHash)] = struct{}{}
	if got := router.statsGetRequest("", nil, nil, nil, notAllowedIdentity, time.Now()); got != peerErrorNoAccess {
		t.Fatalf("stats no access=%v want=%v", got, peerErrorNoAccess)
	}

	allowedIdentity := &rns.Identity{Hash: append([]byte{}, allowedHash...)}
	allowedResponse := router.statsGetRequest("", nil, nil, nil, allowedIdentity, time.Now())
	if _, ok := allowedResponse.(map[string]any); !ok {
		t.Fatalf("allowed stats response type=%T want=map[string]any", allowedResponse)
	}
}

func TestIntegrationPropagationOfferInvalidKeyGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_invalid_key.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferInvalidKeyPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_invalid_key_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer invalid-key run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != uint64(peerErrorInvalidKey) {
		t.Fatalf("response=%v want=%v", unpacked, peerErrorInvalidKey)
	}
}

func TestIntegrationPropagationOfferThrottledGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_throttled.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferThrottledPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_throttled_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer throttled run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != uint64(peerErrorThrottled) {
		t.Fatalf("response=%v want=%v", unpacked, peerErrorThrottled)
	}
}

func TestIntegrationPropagationOfferThrottleExpiredGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_throttle_expired.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferThrottleExpiredPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_throttle_expired_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer throttle-expired run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != true {
		t.Fatalf("response=%v want=true", unpacked)
	}
}

func TestIntegrationPropagationOfferStaticOnlyGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_static_only.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferStaticOnlyPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_static_only_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer static-only run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != uint64(peerErrorNoAccess) {
		t.Fatalf("response=%v want=%v", unpacked, peerErrorNoAccess)
	}
}

func TestIntegrationPropagationOfferStaticAllowedGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_static_allowed.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferStaticAllowedPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_static_allowed_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer static-allowed run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != true {
		t.Fatalf("response=%v want=true", unpacked)
	}
}

func TestIntegrationPropagationOfferAllKnownGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_all_known.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferAllKnownPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_all_known_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer all-known run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != false {
		t.Fatalf("response=%v want=false", unpacked)
	}
}

func TestIntegrationPropagationOfferAllMissingGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "run_offer_all_missing.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfRunOfferAllMissingPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "offer_all_missing_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python offer all-missing run failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != true {
		t.Fatalf("response=%v want=true", unpacked)
	}
}

func TestIntegrationPeeringKeyValidationGoToPython(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "validate_peering_key.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfValidatePeeringKeyPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	localIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(local): %v", err)
	}
	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	peeringID := make([]byte, 0, len(localIdentity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, localIdentity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)

	key, _, _, err := GenerateStamp(peeringID, 2, WorkblockExpandRoundsPeering)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}

	responsePath := filepath.Join(tmpDir, "validate_peering_key_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath, hex.EncodeToString(peeringID), hex.EncodeToString(key), "2")
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python key validation failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	if unpacked != true {
		t.Fatalf("response=%v want=true", unpacked)
	}
}

func TestIntegrationPeeringKeyValidationPythonToGo(t *testing.T) {
	t.Parallel()
	lxmfPath, reticulumPath := requirePythonInteropPaths(t)

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	scriptPath := filepath.Join(tmpDir, "generate_peering_key.py")
	if err := os.WriteFile(scriptPath, []byte(lxmfGeneratePeeringKeyPy), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}

	localIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(local): %v", err)
	}
	remoteIdentity, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(remote): %v", err)
	}

	peeringID := make([]byte, 0, len(localIdentity.Hash)+len(remoteIdentity.Hash))
	peeringID = append(peeringID, localIdentity.Hash...)
	peeringID = append(peeringID, remoteIdentity.Hash...)

	responsePath := filepath.Join(tmpDir, "generate_peering_key_response.msgpack")
	cmd := exec.Command("python3", scriptPath, responsePath, hex.EncodeToString(peeringID), "2")
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPathEnv(lxmfPath, reticulumPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python key generation failed: %v output=%v", err, string(out))
	}

	responseData, err := os.ReadFile(responsePath)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	unpacked, err := msgpack.Unpack(responseData)
	if err != nil {
		t.Fatalf("Unpack response: %v", err)
	}

	parts, ok := unpacked.([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("unexpected response type %T value=%v", unpacked, unpacked)
	}
	key, ok := parts[0].([]byte)
	if !ok {
		t.Fatalf("unexpected key type %T", parts[0])
	}
	if !ValidatePeeringKey(peeringID, key, 2) {
		t.Fatal("expected python-generated key to validate in Go")
	}
}
