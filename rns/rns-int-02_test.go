// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

const integratedReceiverPy = `import RNS
import sys
import time
import os

def start_receiver(config_dir, listen_port, forward_port):
    if not os.path.exists(config_dir):
        os.makedirs(config_dir)

    config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
		listen_port = {listen_port}
    forward_ip = 127.0.0.1
		forward_port = {forward_port}
"""
    with open(os.path.join(config_dir, "config"), "w") as f:
        f.write(config_content)

    # Clean up storage from previous runs
    storage_dir = os.path.join(config_dir, "storage")
    if os.path.exists(storage_dir):
        import shutil
        shutil.rmtree(storage_dir)

    reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
    RNS.logdest = RNS.LOG_STDOUT

    identity = RNS.Identity()
    destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "integrated_test", "parity")

    def link_established(link):
        print(f"Link Established: {link.hash.hex()}")
        sys.stdout.flush()

    destination.set_link_established_callback(link_established)

    def request_handler(path, data, request_id, link_id, remote_identity, requested_at):
        print(f"Request Received: {path}")
        sys.stdout.flush()
        return b"response from python"

    destination.register_request_handler("test_path", request_handler, RNS.Destination.ALLOW_ALL)

    print(f"Destination Hash: {destination.hash.hex()}")
    print(f"Identity Public Key: {identity.get_public_key().hex()}")
    sys.stdout.flush()

    # Periodically announce until done
    done_file = os.path.join(config_dir, "done")
    if os.path.exists(done_file):
        os.remove(done_file)

    timeout = time.time() + 20
    while time.time() < timeout:
        destination.announce()
        time.sleep(0.5)
        if os.path.exists(done_file):
            print("Receiver received done signal")
            break

    print("Receiver exiting")

if __name__ == "__main__":
    if len(sys.argv) != 4:
        print("Usage: integrated_receiver.py <config_dir> <listen_port> <forward_port>")
        sys.exit(1)
    start_receiver(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]))
`

const integratedPathResponseTargetPy = `import RNS
import sys
import time
import os

def start_target(config_dir, listen_port, forward_port, tag_hex):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = True
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	storage_dir = os.path.join(config_dir, "storage")
	if os.path.exists(storage_dir):
		import shutil
		shutil.rmtree(storage_dir)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	identity = RNS.Identity()
	destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "integrated_test", "parity")

	print(f"Destination Hash: {destination.hash.hex()}")
	print(f"Identity Public Key: {identity.get_public_key().hex()}")
	sys.stdout.flush()

	done_file = os.path.join(config_dir, "done")
	emit_file = os.path.join(config_dir, "emit_path_response")
	announce_tag = bytes.fromhex(tag_hex)
	if os.path.exists(done_file):
		os.remove(done_file)
	if os.path.exists(emit_file):
		os.remove(emit_file)

	timeout = time.time() + 25
	emitted = False
	while time.time() < timeout:
		if (not emitted) and os.path.exists(emit_file):
			destination.announce(path_response=True, tag=announce_tag)
			print("TARGET_PATH_RESPONSE_SENT")
			sys.stdout.flush()
			emitted = True
		if os.path.exists(done_file):
			print("TARGET_DONE")
			sys.stdout.flush()
			return
		time.sleep(0.2)

	print("TARGET_TIMEOUT")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 5:
		print("Usage: integrated_path_response_target.py <config_dir> <listen_port> <forward_port> <tag_hex>")
		sys.exit(1)
	start_target(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4])
`

const integratedInitiatorPy = `import RNS
import sys
import time
import os

def start_initiator(dest_hash_hex, pub_key_hex, config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	dest_hash = bytes.fromhex(dest_hash_hex)
	pub_key = bytes.fromhex(pub_key_hex)

	print(f"Waiting for path to {dest_hash_hex}...")
	sys.stdout.flush()
	timeout = time.time() + 10
	while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
		time.sleep(0.5)

	if not RNS.Transport.has_path(dest_hash):
		print("Timed out waiting for path")
		sys.exit(1)

	remote_identity = RNS.Identity(create_keys=False)
	remote_identity.load_public_key(pub_key)
	destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "integrated_test", "parity")

	if destination.hash != dest_hash:
		print(f"Destination hash mismatch! Expected {dest_hash_hex}, got {destination.hash.hex()}")
		sys.exit(1)

	print("Establishing link...")
	sys.stdout.flush()
	link = RNS.Link(destination)

	link_established = [False]
	def established(l):
		print(f"Link Established: {l.hash.hex()}")
		sys.stdout.flush()
		link_established[0] = True

	link.set_link_established_callback(established)

	timeout = time.time() + 10
	while not link_established[0] and time.time() < timeout:
		time.sleep(0.5)

	if not link_established[0]:
		print("Timed out waiting for link establishment")
		sys.exit(1)

	print("Sending request...")
	sys.stdout.flush()

	response_received = [None]
	def response_callback(r):
		print(f"Received response: {r}")
		sys.stdout.flush()
		if hasattr(r, "response"):
			print(f"Extracted receipt.response: {r.response} ({type(r.response)})")
			sys.stdout.flush()
			response_received[0] = r.response
		else:
			print(f"Using direct response object: {r} ({type(r)})")
			sys.stdout.flush()
			response_received[0] = r

	link.request("test_path", b"request from python", response_callback)

	timeout = time.time() + 10
	while response_received[0] is None and time.time() < timeout:
		time.sleep(0.5)

	if response_received[0] is None:
		print("Timed out waiting for response")
		sys.exit(1)

	print("Initiator exiting")
	sys.stdout.flush()

if __name__ == "__main__":
    if len(sys.argv) != 6:
        print("Usage: integrated_initiator.py <dest_hash_hex> <pub_key_hex> <config_dir> <listen_port> <forward_port>")
        sys.exit(1)
    start_initiator(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]))
`

const integratedLargeInitiatorPy = `import RNS
import sys
import time
import os

def start_initiator(dest_hash_hex, pub_key_hex, config_dir, listen_port, forward_port, payload_size):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	dest_hash = bytes.fromhex(dest_hash_hex)
	pub_key = bytes.fromhex(pub_key_hex)

	timeout = time.time() + 10
	while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
		time.sleep(0.5)

	if not RNS.Transport.has_path(dest_hash):
		print("Timed out waiting for path")
		sys.stdout.flush()
		sys.exit(1)

	remote_identity = RNS.Identity(create_keys=False)
	remote_identity.load_public_key(pub_key)
	destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "integrated_test", "parity")

	if destination.hash != dest_hash:
		print("Destination hash mismatch")
		sys.stdout.flush()
		sys.exit(1)

	link = RNS.Link(destination)
	link_established = [False]

	def established(l):
		print(f"Link Established: {l.hash.hex()}")
		sys.stdout.flush()
		link_established[0] = True

	link.set_link_established_callback(established)

	timeout = time.time() + 10
	while not link_established[0] and time.time() < timeout:
		time.sleep(0.2)

	if not link_established[0]:
		print("Timed out waiting for link establishment")
		sys.stdout.flush()
		sys.exit(1)

	payload = os.urandom(int(payload_size))
	response_received = [None]

	def response_callback(r):
		print(f"Received response: {r}")
		sys.stdout.flush()
		if hasattr(r, "response"):
			response_received[0] = r.response
		else:
			response_received[0] = r

	link.request("test_path", payload, response_callback)

	timeout = time.time() + 20
	while response_received[0] is None and time.time() < timeout:
		time.sleep(0.2)

	if response_received[0] is None:
		print("Timed out waiting for response")
		sys.stdout.flush()
		sys.exit(1)

	if response_received[0] != b"response from go":
		print("Unexpected response")
		sys.stdout.flush()
		sys.exit(1)

	print("LARGE_REQUEST_SUCCEEDED")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 7:
		print("Usage: integrated_large_initiator.py <dest_hash_hex> <pub_key_hex> <config_dir> <listen_port> <forward_port> <payload_size>")
		sys.exit(1)
	start_initiator(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]), int(sys.argv[6]))
`

const integratedPathRequesterPy = `import RNS
import sys
import time
import os

def start_requester(dest_hash_hex, config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	dest_hash = bytes.fromhex(dest_hash_hex)
	print(f"REQUEST_PATH:{dest_hash_hex}")
	sys.stdout.flush()

	RNS.Transport.request_path(dest_hash)

	timeout = time.time() + 15
	while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
		time.sleep(0.2)

	if not RNS.Transport.has_path(dest_hash):
		print("PATH_REQUEST_FAILED")
		sys.stdout.flush()
		sys.exit(1)

	print("PATH_REQUEST_SUCCEEDED")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 5:
		print("Usage: integrated_path_requester.py <dest_hash_hex> <config_dir> <listen_port> <forward_port>")
		sys.exit(1)
	start_requester(sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]))
`

const integratedRelayPy = `import RNS
import sys
import time
import os

def start_relay(config_dir, ingress_listen_port, ingress_forward_port, egress_listen_port, egress_forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = True
share_instance = No

[interfaces]
  [[Ingress]]
	type = UDPInterface
	enabled = True
	mode = access_point
	listen_ip = 127.0.0.1
	listen_port = {ingress_listen_port}
	forward_ip = 127.0.0.1
	forward_port = {ingress_forward_port}

  [[Egress]]
	type = UDPInterface
	enabled = True
	mode = access_point
	listen_ip = 127.0.0.1
	listen_port = {egress_listen_port}
	forward_ip = 127.0.0.1
	forward_port = {egress_forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	print("RELAY_READY")
	sys.stdout.flush()

	done_file = os.path.join(config_dir, "done")
	if os.path.exists(done_file):
		os.remove(done_file)

	timeout = time.time() + 25
	while time.time() < timeout:
		if os.path.exists(done_file):
			print("RELAY_DONE")
			sys.stdout.flush()
			return
		time.sleep(0.2)

	print("RELAY_TIMEOUT")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 6:
		print("Usage: integrated_relay.py <config_dir> <ingress_listen_port> <ingress_forward_port> <egress_listen_port> <egress_forward_port>")
		sys.exit(1)
	start_relay(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5]))
`

const integratedRelayFullModePy = `import RNS
import sys
import time
import os

def start_relay(config_dir, ingress_listen_port, ingress_forward_port, egress_listen_port, egress_forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = True
share_instance = No

[interfaces]
  [[Ingress]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {ingress_listen_port}
	forward_ip = 127.0.0.1
	forward_port = {ingress_forward_port}

  [[Egress]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {egress_listen_port}
	forward_ip = 127.0.0.1
	forward_port = {egress_forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	print("RELAY_READY")
	sys.stdout.flush()

	done_file = os.path.join(config_dir, "done")
	if os.path.exists(done_file):
		os.remove(done_file)

	timeout = time.time() + 25
	while time.time() < timeout:
		if os.path.exists(done_file):
			print("RELAY_DONE")
			sys.stdout.flush()
			return
		time.sleep(0.2)

	print("RELAY_TIMEOUT")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 6:
		print("Usage: integrated_relay.py <config_dir> <ingress_listen_port> <ingress_forward_port> <egress_listen_port> <egress_forward_port>")
		sys.exit(1)
	start_relay(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5]))
`

const integratedPathInvalidationRequesterPy = `import RNS
import sys
import time
import os

def start_invalidation_requester(dest_hash_hex, config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	dest_hash = bytes.fromhex(dest_hash_hex)
	timeout = time.time() + 15
	while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
		time.sleep(0.2)

	if not RNS.Transport.has_path(dest_hash):
		print("INITIAL_PATH_MISSING")
		sys.stdout.flush()
		sys.exit(1)

	print("PATH_LEARNED")
	sys.stdout.flush()
	with open(os.path.join(config_dir, "learned"), "w") as f:
		f.write("1")

	time.sleep(1.0)

	RNS.Transport.expire_path(dest_hash)
	invalidation_deadline = time.time() + 2.0
	while RNS.Transport.has_path(dest_hash) and time.time() < invalidation_deadline:
		RNS.Transport.expire_path(dest_hash)
		time.sleep(0.1)

	if RNS.Transport.has_path(dest_hash):
		print("PATH_INVALIDATION_FAILED")
		sys.stdout.flush()
		sys.exit(1)

	print("PATH_INVALIDATED")
	sys.stdout.flush()

	RNS.Transport.request_path(dest_hash)
	timeout = time.time() + 15
	while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
		time.sleep(0.2)

	if not RNS.Transport.has_path(dest_hash):
		print("PATH_REDISCOVERY_FAILED")
		sys.stdout.flush()
		sys.exit(1)

	print("PATH_REDISCOVERED")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 5:
		print("Usage: integrated_path_invalidation_requester.py <dest_hash_hex> <config_dir> <listen_port> <forward_port>")
		sys.exit(1)
	start_invalidation_requester(sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]))
`

const integratedRelayPathRequestEmitterPy = `import RNS
import sys
import time
import os

def start_emitter(config_dir, ingress_listen_port, ingress_forward_port, egress_listen_port, egress_forward_port, dest_hash_hex, tag_hex):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = True
share_instance = No

[interfaces]
  [[Ingress]]
	type = UDPInterface
	enabled = True
	mode = access_point
	listen_ip = 127.0.0.1
	listen_port = {ingress_listen_port}
	forward_ip = 127.0.0.1
	forward_port = {ingress_forward_port}

  [[Egress]]
	type = UDPInterface
	enabled = True
	mode = access_point
	listen_ip = 127.0.0.1
	listen_port = {egress_listen_port}
	forward_ip = 127.0.0.1
	forward_port = {egress_forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	egress = None
	for interface in RNS.Transport.interfaces:
		if str(interface).startswith("UDPInterface[Egress/"):
			egress = interface
			break

	if egress is None:
		print("EMITTER_EGRESS_MISSING")
		sys.stdout.flush()
		sys.exit(1)

	dest_hash = bytes.fromhex(dest_hash_hex)
	tag = bytes.fromhex(tag_hex)
	print("EMITTER_READY")
	sys.stdout.flush()

	time.sleep(0.5)
	RNS.Transport.request_path(dest_hash, on_interface=egress, tag=tag)
	print("EMITTER_SENT")
	sys.stdout.flush()

	time.sleep(1.0)

if __name__ == "__main__":
	if len(sys.argv) != 8:
		print("Usage: integrated_relay_path_request_emitter.py <config_dir> <ingress_listen_port> <ingress_forward_port> <egress_listen_port> <egress_forward_port> <dest_hash_hex> <tag_hex>")
		sys.exit(1)
	start_emitter(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5]), sys.argv[6], sys.argv[7])
`

const integratedPathRequesterEmitterPy = `import RNS
import sys
import time
import os

def start_requester(config_dir, listen_port, forward_port, dest_hash_hex, tag_hex):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	dest_hash = bytes.fromhex(dest_hash_hex)
	tag = bytes.fromhex(tag_hex)

	print("REQUESTER_READY")
	sys.stdout.flush()

	time.sleep(0.5)
	RNS.Transport.request_path(dest_hash, tag=tag)
	print("REQUESTER_SENT")
	sys.stdout.flush()
	time.sleep(1.0)

if __name__ == "__main__":
	if len(sys.argv) != 6:
		print("Usage: integrated_path_requester_emitter.py <config_dir> <listen_port> <forward_port> <dest_hash_hex> <tag_hex>")
		sys.exit(1)
	start_requester(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4], sys.argv[5])
`

const integratedAnnounceEmitterPy = `import RNS
import sys
import time
import os

def start_emitter(config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = True
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	identity = RNS.Identity()
	destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "relay", "announce_target")

	print("EMITTER_READY")
	print(f"EMITTER_DEST_HASH:{destination.hash.hex()}")
	sys.stdout.flush()

	done_file = os.path.join(config_dir, "done")
	if os.path.exists(done_file):
		os.remove(done_file)

	timeout = time.time() + 20
	sent = 0
	while time.time() < timeout:
		try:
			destination.announce()
			sent += 1
			print(f"EMITTER_SENT:{sent}")
			sys.stdout.flush()
		except Exception as e:
			print(f"EMITTER_SEND_ERROR:{e}")
			sys.stdout.flush()
		time.sleep(0.5)
		if os.path.exists(done_file):
			print("EMITTER_DONE")
			sys.stdout.flush()
			return

	print("EMITTER_TIMEOUT")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 4:
		print("Usage: integrated_announce_emitter.py <config_dir> <listen_port> <forward_port>")
		sys.exit(1)
	start_emitter(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]))
`

func TestIntegratedHandshakeGoToPython(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-integrated-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "integrated_receiver.py")
	if err := os.WriteFile(scriptPath, []byte(integratedReceiverPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_receiver")

	// Start Python receiver
	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout // Combine stderr and stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	// Read destination hash from Python output
	scanner := bufio.NewScanner(pyStdout)
	var destHashHex, pyPubHex string
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python] %v\n", line)
		if strings.HasPrefix(line, "Destination Hash: ") {
			destHashHex = strings.TrimPrefix(line, "Destination Hash: ")
		} else if strings.HasPrefix(line, "Identity Public Key: ") {
			pyPubHex = strings.TrimPrefix(line, "Identity Public Key: ")
			break
		}
	}

	if destHashHex == "" || pyPubHex == "" {
		t.Fatal("Could not get destination hash or public key from Python receiver")
	}
	destHash, _ := HexToBytes(destHashHex)
	pyPub, _ := HexToBytes(pyPubHex)

	// Keep reading Python output in background
	go func() {
		for scanner.Scan() {
			fmt.Printf("[Python] %v\n", scanner.Text())
		}
	}()

	remoteId, _ := NewIdentity(false)
	if err := remoteId.LoadPublicKey(pyPub); err != nil {
		t.Fatalf("Failed to load remote public key: %v", err)
	}

	// Initialize Go Reticulum
	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	// Wait for announce (path) from Python
	ts := GetTransport()
	timeout := time.Now().Add(10 * time.Second)
	found := false
	for time.Now().Before(timeout) {
		if ts.HasPath(destHash) {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		t.Fatal("Timed out waiting for announce from Python")
	}

	// Create Link
	// We need to create a dummy destination for the remote side
	remoteDest, err := NewDestination(remoteId, DestinationOut, DestinationSingle, "integrated_test", "parity")
	if err != nil {
		t.Fatal(err)
	}
	// The hash should match what Python reported
	if !bytes.Equal(remoteDest.Hash, destHash) {
		t.Fatalf("Remote destination hash mismatch! Expected %x, got %x", destHash, remoteDest.Hash)
	}

	l, err := NewLink(remoteDest)
	if err != nil {
		t.Fatal(err)
	}

	linkEstablished := make(chan bool, 1)
	l.SetLinkEstablishedCallback(func(link *Link) {
		fmt.Printf("Link established: %x\n", link.linkID)
		linkEstablished <- true
	})

	if err := l.Establish(); err != nil {
		t.Fatalf("Failed to establish link: %v", err)
	}

	select {
	case <-linkEstablished:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out waiting for link establishment")
	}

	// Send Request
	responseChan := make(chan string, 1)
	_, err = l.Request("test_path", []byte("request from go"), func(rr *RequestReceipt) {
		fmt.Printf("Received response: %v\n", rr.Response)
		responseChan <- string(rr.Response.([]byte))
	}, nil, nil, 0)

	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	select {
	case resp := <-responseChan:
		if resp != "response from python" {
			t.Errorf("Unexpected response: %v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for response")
	}

	// Signal Python to exit
	os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)

	pyCmd.Wait()
}

func TestIntegratedLargeRequestGoToPython(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-large-request-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "integrated_receiver.py")
	if err := os.WriteFile(scriptPath, []byte(integratedReceiverPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_receiver")

	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	scanner := bufio.NewScanner(pyStdout)
	var destHashHex, pyPubHex string
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python Receiver] %v\n", line)
		if strings.HasPrefix(line, "Destination Hash:") {
			destHashHex = strings.TrimSpace(strings.TrimPrefix(line, "Destination Hash:"))
		}
		if strings.HasPrefix(line, "Identity Public Key:") {
			pyPubHex = strings.TrimSpace(strings.TrimPrefix(line, "Identity Public Key:"))
			break
		}
	}

	if destHashHex == "" || pyPubHex == "" {
		t.Fatalf("failed to get destination hash/public key from python receiver")
	}

	destHash, err := HexToBytes(destHashHex)
	if err != nil {
		t.Fatalf("failed to parse destination hash: %v", err)
	}
	pyPub, err := HexToBytes(pyPubHex)
	if err != nil {
		t.Fatalf("failed to parse public key: %v", err)
	}

	go func() {
		for scanner.Scan() {
			fmt.Printf("[Python Receiver] %v\n", scanner.Text())
		}
	}()

	remoteID, _ := NewIdentity(false)
	if err := remoteID.LoadPublicKey(pyPub); err != nil {
		t.Fatalf("Failed to load remote public key: %v", err)
	}

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0o700)

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	ts := GetTransport()
	pathDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(pathDeadline) {
		if ts.HasPath(destHash) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ts.HasPath(destHash) {
		t.Fatal("timed out waiting for announce path from Python")
	}

	remoteDest, err := NewDestination(remoteID, DestinationOut, DestinationSingle, "integrated_test", "parity")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remoteDest.Hash, destHash) {
		t.Fatalf("remote destination hash mismatch: expected %x got %x", destHash, remoteDest.Hash)
	}

	l, err := NewLink(remoteDest)
	if err != nil {
		t.Fatal(err)
	}

	linkEstablished := make(chan bool, 1)
	l.SetLinkEstablishedCallback(func(link *Link) {
		linkEstablished <- true
	})

	if err := l.Establish(); err != nil {
		t.Fatalf("failed to establish link: %v", err)
	}

	select {
	case <-linkEstablished:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for link establishment")
	}

	largePayload := bytes.Repeat([]byte("L"), l.mdu+512)
	responseChan := make(chan string, 1)
	_, err = l.Request("test_path", largePayload, func(rr *RequestReceipt) {
		responseChan <- string(rr.Response.([]byte))
	}, nil, nil, 0)
	if err != nil {
		t.Fatalf("failed to send large request: %v", err)
	}

	select {
	case resp := <-responseChan:
		if resp != "response from python" {
			t.Fatalf("unexpected response payload: %q", resp)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for response to large request")
	}

	os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)
	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("python receiver exited with error: %v", err)
	}
}

func TestIntegratedHandshakePythonToGo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-integrated-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "integrated_initiator.py")
	if err := os.WriteFile(scriptPath, []byte(integratedInitiatorPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_initiator")

	// Initialize Go Reticulum
	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)

	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	// Create Go destination
	id, _ := NewIdentity(true)
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "integrated_test", "parity")
	if err != nil {
		t.Fatal(err)
	}

	linkEstablished := make(chan *Link, 1)
	dest.SetLinkEstablishedCallback(func(l *Link) {
		fmt.Printf("Go: Link established: %x\n", l.linkID)
		linkEstablished <- l
	})

	requestReceived := make(chan bool, 1)
	dest.RegisterRequestHandler("test_path", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		fmt.Printf("Go: Request received: %v\n", path)
		requestReceived <- true
		return []byte("response from go")
	}, AllowAll, nil, false)

	// Periodically announce
	go func() {
		for {
			dest.Announce(nil)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	// Start Python initiator
	// PYTHONPATH=$ORIGINAL_RETICULUM_REPO_DIR python3 integrated_initiator.py <dest_hash> <pub_key>
	pyCmd := exec.Command("python3", scriptPath, fmt.Sprintf("%x", dest.Hash), fmt.Sprintf("%x", id.GetPublicKey()), pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	// Keep reading Python output in background
	go func() {
		scanner := bufio.NewScanner(pyStdout)
		for scanner.Scan() {
			fmt.Printf("[Python Initiator] %v\n", scanner.Text())
		}
	}()

	// Wait for link
	select {
	case <-linkEstablished:
		// Success
	case <-time.After(15 * time.Second):
		t.Fatal("Timed out waiting for link establishment from Python")
	}

	// Wait for request
	select {
	case <-requestReceived:
		// Success
	case <-time.After(15 * time.Second):
		t.Fatal("Timed out waiting for request from Python")
	}

	// Wait for Python to exit successfully
	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("Python initiator failed: %v", err)
	}
}

func TestIntegratedLargeRequestPythonToGo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-large-py-to-go-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "integrated_large_initiator.py")
	if err := os.WriteFile(scriptPath, []byte(integratedLargeInitiatorPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_large_initiator")

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0o700)
	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	id, _ := NewIdentity(true)
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "integrated_test", "parity")
	if err != nil {
		t.Fatal(err)
	}

	requestReceived := make(chan int, 1)
	dest.RegisterRequestHandler("test_path", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		requestReceived <- len(data)
		return []byte("response from go")
	}, AllowAll, nil, false)

	go func() {
		for {
			_ = dest.Announce(nil)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	payloadSize := MDU + 640
	pyCmd := exec.Command("python3", scriptPath, fmt.Sprintf("%x", dest.Hash), fmt.Sprintf("%x", id.GetPublicKey()), pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort), strconv.Itoa(payloadSize))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	go func() {
		scanner := bufio.NewScanner(pyStdout)
		for scanner.Scan() {
			fmt.Printf("[Python Large Initiator] %v\n", scanner.Text())
		}
	}()

	select {
	case gotSize := <-requestReceived:
		if gotSize != payloadSize {
			t.Fatalf("unexpected large request payload size: got %v want %v", gotSize, payloadSize)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("timed out waiting for large request from Python")
	}

	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("python large initiator failed: %v", err)
	}
}

func TestIntegratedPathInvalidationRediscoveryGoToPython(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-integrated-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "integrated_receiver.py")
	if err := os.WriteFile(scriptPath, []byte(integratedReceiverPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_receiver")

	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	scanner := bufio.NewScanner(pyStdout)
	var destHashHex string
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python] %v\n", line)
		if strings.HasPrefix(line, "Destination Hash: ") {
			destHashHex = strings.TrimPrefix(line, "Destination Hash: ")
			break
		}
	}
	if destHashHex == "" {
		t.Fatal("could not get destination hash from Python receiver")
	}
	destHash, _ := HexToBytes(destHashHex)

	go func() {
		for scanner.Scan() {
			fmt.Printf("[Python] %v\n", scanner.Text())
		}
	}()

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	ts := GetTransport()
	initialDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(initialDeadline) {
		if ts.HasPath(destHash) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ts.HasPath(destHash) {
		t.Fatal("timed out waiting for initial path learning from Python")
	}

	if removed := ts.InvalidatePath(destHash); !removed {
		t.Fatal("expected path invalidation to remove existing path")
	}
	if ts.HasPath(destHash) {
		t.Fatal("expected path to be gone after invalidation")
	}

	if err := ts.RequestPath(destHash); err != nil {
		t.Fatalf("request path failed: %v", err)
	}

	rediscoveryDeadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(rediscoveryDeadline) {
		if ts.HasPath(destHash) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ts.HasPath(destHash) {
		t.Fatal("timed out waiting for path rediscovery after invalidation")
	}

	os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)
	pyCmd.Wait()
}

func TestIntegratedPathResponsePacketMetadataUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-pathresp-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	goListenPort, requesterPort := allocateUDPPortPair(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goConfigContent := mustUDPConfig(t.Name(), goListenPort, requesterPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	id, _ := NewIdentity(true)
	localDest, err := NewDestination(id, DestinationIn, DestinationSingle, "pathreq", "target")
	if err != nil {
		t.Fatal(err)
	}

	requestConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: requesterPort})
	if err != nil {
		t.Fatalf("failed to open requester UDP socket: %v", err)
	}
	defer func() { _ = requestConn.Close() }()

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	tag := bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)
	requestData := make([]byte, 0, TruncatedHashLength/4)
	requestData = append(requestData, localDest.Hash...)
	requestData = append(requestData, tag...)

	requestPacket := NewPacket(pathReqDest, requestData)
	if err := requestPacket.Pack(); err != nil {
		t.Fatalf("failed packing path request packet: %v", err)
	}

	if _, err := requestConn.WriteToUDP(requestPacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: goListenPort}); err != nil {
		t.Fatalf("failed sending UDP path request: %v", err)
	}

	if err := requestConn.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatalf("failed setting read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := requestConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed reading path response packet: %v", err)
	}

	response := NewPacketFromRaw(buf[:n])
	if err := response.Unpack(); err != nil {
		t.Fatalf("failed unpacking path response packet: %v", err)
	}

	if response.PacketType != PacketAnnounce {
		t.Fatalf("expected announce packet type, got %v", response.PacketType)
	}
	if response.Context != ContextPathResponse {
		t.Fatalf("expected ContextPathResponse, got %v", response.Context)
	}
	if response.HeaderType != Header2 {
		t.Fatalf("expected Header2 path response, got %v", response.HeaderType)
	}
	if response.TransportType != TransportForward {
		t.Fatalf("expected TransportForward path response, got %v", response.TransportType)
	}

	transportID := GetTransport().identity.Hash
	if !bytes.Equal(response.TransportID, transportID) {
		t.Fatalf("expected transport ID to match local transport identity")
	}
	if !bytes.Equal(response.DestinationHash, localDest.Hash) {
		t.Fatalf("expected destination hash to match requested local destination")
	}
}

func TestIntegratedMultiHopHeader2ForwardingUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-multihop-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	goListenPort, sinkPort := allocateUDPPortPair(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goConfigContent := mustUDPConfig(t.Name(), goListenPort, sinkPort, true)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	ts := GetTransport()
	var outIface interfaces.Interface
	for _, iface := range ts.GetInterfaces() {
		if iface.Type() == "UDPInterface" {
			outIface = iface
			break
		}
	}
	if outIface == nil {
		t.Fatal("expected UDP interface for multi-hop test")
	}

	remoteID, _ := NewIdentity(true)
	remoteDest, err := NewDestinationWithTransport(nil, remoteID, DestinationOut, DestinationSingle, "multihop", "target")
	if err != nil {
		t.Fatal(err)
	}

	nextHop := bytes.Repeat([]byte{0x7A}, TruncatedHashLength/8)
	ts.GetMutex().Lock()
	ts.pathTable[string(remoteDest.Hash)] = &PathEntry{
		Interface: outIface,
		Hops:      3,
		NextHop:   nextHop,
		Expires:   time.Now().Add(time.Hour),
	}
	ts.GetMutex().Unlock()

	sinkConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sinkPort})
	if err != nil {
		t.Fatalf("failed to open sink UDP socket: %v", err)
	}
	defer func() { _ = sinkConn.Close() }()

	senderConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to open sender UDP socket: %v", err)
	}
	defer func() { _ = senderConn.Close() }()

	p := NewPacket(remoteDest, []byte("multi-hop-forward"))
	p.HeaderType = Header2
	p.TransportID = ts.identity.Hash
	if err := p.Pack(); err != nil {
		t.Fatalf("failed packing inbound multi-hop packet: %v", err)
	}

	if _, err := senderConn.WriteToUDP(p.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: goListenPort}); err != nil {
		t.Fatalf("failed sending inbound packet to transport: %v", err)
	}

	if err := sinkConn.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatalf("failed setting sink read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := sinkConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed reading forwarded packet: %v", err)
	}

	forwarded := NewPacketFromRaw(buf[:n])
	if err := forwarded.Unpack(); err != nil {
		t.Fatalf("failed unpacking forwarded packet: %v", err)
	}

	if forwarded.HeaderType != Header2 {
		t.Fatalf("expected Header2 forwarded packet, got %v", forwarded.HeaderType)
	}
	if !bytes.Equal(forwarded.TransportID, nextHop) {
		t.Fatalf("expected rewritten transport ID to next-hop value")
	}
	if !bytes.Equal(forwarded.DestinationHash, remoteDest.Hash) {
		t.Fatalf("expected forwarded destination hash to remain unchanged")
	}
	if forwarded.Hops < 1 {
		t.Fatalf("expected forwarded hops to be incremented, got %v", forwarded.Hops)
	}
}

func TestIntegratedPathResponseAnnounceNotRebroadcastUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-pathresp-norebroadcast-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	ingressListenPort := allocateUDPPort(t)
	ingressForwardPort := allocateUDPPort(t)
	egressListenPort := allocateUDPPort(t)
	sinkPort := allocateUDPPort(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0700)
	goConfigContent := fmt.Sprintf(`[reticulum]
share_instance = No
enable_transport = False

[interfaces]
  [[Ingress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v

  [[Egress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v
`, ingressListenPort, ingressForwardPort, egressListenPort, sinkPort)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	sinkConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sinkPort})
	if err != nil {
		t.Fatalf("failed to open sink UDP socket: %v", err)
	}
	defer func() { _ = sinkConn.Close() }()

	senderConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("failed to open sender UDP socket: %v", err)
	}
	defer func() { _ = senderConn.Close() }()

	id, _ := NewIdentity(true)
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "pathresp", "target")
	if err != nil {
		t.Fatal(err)
	}

	announce, err := dest.buildAnnouncePacket(nil)
	if err != nil {
		t.Fatalf("failed to build announce packet: %v", err)
	}
	announce.Context = ContextPathResponse
	if err := announce.Pack(); err != nil {
		t.Fatalf("failed packing path-response announce packet: %v", err)
	}

	if _, err := senderConn.WriteToUDP(announce.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: ingressListenPort}); err != nil {
		t.Fatalf("failed sending inbound path-response announce: %v", err)
	}

	if err := sinkConn.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatalf("failed setting sink read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	if _, _, err := sinkConn.ReadFromUDP(buf); err == nil {
		t.Fatal("expected no rebroadcast for ContextPathResponse announce")
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected timeout waiting for rebroadcast suppression, got: %v", err)
	}
}

func TestIntegratedRelayedPathResponsePropagationUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-pathresp-relay-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	relayIngressListenPort := allocateUDPPort(t)
	relayEgressListenPort := allocateUDPPort(t)
	requesterPort := allocateUDPPort(t)
	responderPort := allocateUDPPort(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0o700)
	goConfigContent := fmt.Sprintf(`[reticulum]
share_instance = No
enable_transport = False

[interfaces]
  [[Ingress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v

  [[Egress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v
`, relayIngressListenPort, requesterPort, relayEgressListenPort, responderPort)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	requestConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: requesterPort})
	if err != nil {
		t.Fatalf("failed to open requester UDP socket: %v", err)
	}
	defer func() { _ = requestConn.Close() }()

	responderConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: responderPort})
	if err != nil {
		t.Fatalf("failed to open responder UDP socket: %v", err)
	}
	defer func() { _ = responderConn.Close() }()

	remoteID, _ := NewIdentity(true)
	remoteDest, err := NewDestinationWithTransport(nil, remoteID, DestinationIn, DestinationSingle, "relay", "target")
	if err != nil {
		t.Fatalf("failed creating remote destination: %v", err)
	}

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	tag := bytes.Repeat([]byte{0xCD}, TruncatedHashLength/8)
	requestData := make([]byte, 0, len(remoteDest.Hash)+len(tag))
	requestData = append(requestData, remoteDest.Hash...)
	requestData = append(requestData, tag...)

	requestPacket := NewPacket(pathReqDest, requestData)
	if err := requestPacket.Pack(); err != nil {
		t.Fatalf("failed packing relayed path request packet: %v", err)
	}

	if _, err := requestConn.WriteToUDP(requestPacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: relayIngressListenPort}); err != nil {
		t.Fatalf("failed sending relayed path request packet: %v", err)
	}

	if err := responderConn.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatalf("failed setting responder read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := responderConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed reading forwarded path request packet: %v", err)
	}

	forwardedReq := NewPacketFromRaw(buf[:n])
	if err := forwardedReq.Unpack(); err != nil {
		t.Fatalf("failed unpacking forwarded path request packet: %v", err)
	}
	if !bytes.Equal(forwardedReq.DestinationHash, pathReqDest.Hash) {
		t.Fatalf("unexpected forwarded path request destination hash")
	}
	if len(forwardedReq.Data) < len(remoteDest.Hash) || !bytes.Equal(forwardedReq.Data[:len(remoteDest.Hash)], remoteDest.Hash) {
		t.Fatalf("forwarded path request target hash mismatch")
	}

	responsePacket, err := remoteDest.buildAnnouncePacket(tag)
	if err != nil {
		t.Fatalf("failed building synthetic path response announce: %v", err)
	}
	responsePacket.Context = ContextPathResponse
	responsePacket.HeaderType = Header2
	responsePacket.TransportType = TransportForward
	responsePacket.TransportID = bytes.Repeat([]byte{0x7E}, TruncatedHashLength/8)
	if err := responsePacket.Pack(); err != nil {
		t.Fatalf("failed packing synthetic path response announce: %v", err)
	}

	if _, err := responderConn.WriteToUDP(responsePacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: relayEgressListenPort}); err != nil {
		t.Fatalf("failed sending synthetic path response announce to relay: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	buf = make([]byte, 4096)
	for time.Now().Before(deadline) {
		if err := requestConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("failed setting requester read deadline: %v", err)
		}

		n, _, err := requestConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("failed reading relayed path response packet: %v", err)
		}

		response := NewPacketFromRaw(buf[:n])
		if err := response.Unpack(); err != nil {
			continue
		}

		if response.PacketType != PacketAnnounce {
			continue
		}
		if response.Context != ContextPathResponse {
			continue
		}
		if !bytes.Equal(response.DestinationHash, remoteDest.Hash) {
			continue
		}
		if response.HeaderType != Header2 {
			t.Fatalf("expected Header2 relayed path response, got %v", response.HeaderType)
		}
		if response.TransportType != TransportForward {
			t.Fatalf("expected TransportForward relayed path response, got %v", response.TransportType)
		}
		return
	}

	t.Fatal("timed out waiting for relayed ContextPathResponse from synthetic remote target via Go relay")
}

func TestIntegratedRelayedPathResponsePropagationPythonRequesterUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-pathresp-python-requester-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort := allocateUDPPort(t)
	relayIngressListenPort := allocateUDPPort(t)
	relayEgressListenPort := allocateUDPPort(t)
	responderPort := allocateUDPPort(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0o700)
	goConfigContent := fmt.Sprintf(`[reticulum]
share_instance = No
enable_transport = False

[interfaces]
  [[Ingress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v

  [[Egress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v
`, relayIngressListenPort, pyListenPort, relayEgressListenPort, responderPort)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	responderConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: responderPort})
	if err != nil {
		t.Fatalf("failed to open responder UDP socket: %v", err)
	}
	defer func() { _ = responderConn.Close() }()

	remoteID, _ := NewIdentity(true)
	remoteDest, err := NewDestinationWithTransport(nil, remoteID, DestinationIn, DestinationSingle, "relay", "python_requester_target")
	if err != nil {
		t.Fatalf("failed creating remote destination: %v", err)
	}

	scriptPath := filepath.Join(tmpDir, "integrated_path_requester.py")
	if err := os.WriteFile(scriptPath, []byte(integratedPathRequesterPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_requester")

	pyCmd := exec.Command("python3", scriptPath, fmt.Sprintf("%x", remoteDest.Hash), pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(relayIngressListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	lineCh := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(pyStdout)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python Requester] %v\n", line)
			lineCh <- line
		}
		close(lineCh)
	}()

	if err := responderConn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("failed setting responder read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := responderConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed reading forwarded path request packet from Python requester: %v", err)
	}

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	forwardedReq := NewPacketFromRaw(buf[:n])
	if err := forwardedReq.Unpack(); err != nil {
		t.Fatalf("failed unpacking forwarded path request packet: %v", err)
	}
	if !bytes.Equal(forwardedReq.DestinationHash, pathReqDest.Hash) {
		t.Fatalf("unexpected forwarded path request destination hash")
	}
	if len(forwardedReq.Data) < len(remoteDest.Hash) || !bytes.Equal(forwardedReq.Data[:len(remoteDest.Hash)], remoteDest.Hash) {
		t.Fatalf("forwarded path request target hash mismatch")
	}

	tag := []byte(nil)
	if len(forwardedReq.Data) > len(remoteDest.Hash) {
		tag = append([]byte(nil), forwardedReq.Data[len(remoteDest.Hash):]...)
	}

	responsePacket, err := remoteDest.buildAnnouncePacket(tag)
	if err != nil {
		t.Fatalf("failed building synthetic path response announce: %v", err)
	}
	responsePacket.Context = ContextPathResponse
	responsePacket.HeaderType = Header2
	responsePacket.TransportType = TransportForward
	responsePacket.TransportID = bytes.Repeat([]byte{0x6A}, TruncatedHashLength/8)
	if err := responsePacket.Pack(); err != nil {
		t.Fatalf("failed packing synthetic path response announce: %v", err)
	}

	if _, err := responderConn.WriteToUDP(responsePacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: relayEgressListenPort}); err != nil {
		t.Fatalf("failed sending synthetic path response announce to relay: %v", err)
	}

	timeout := time.NewTimer(20 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case line, ok := <-lineCh:
			if !ok {
				if err := pyCmd.Wait(); err != nil {
					t.Fatalf("python requester exited before success: %v", err)
				}
				t.Fatal("python requester exited without reporting path learning success")
			}
			if strings.Contains(line, "PATH_REQUEST_FAILED") {
				t.Fatal("python requester reported path-request failure")
			}
			if strings.Contains(line, "PATH_REQUEST_SUCCEEDED") {
				if err := pyCmd.Wait(); err != nil {
					t.Fatalf("python requester exited with error after success: %v", err)
				}
				return
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for Python requester path-learning success")
		}
	}
}

func TestIntegratedRelayedPathResponsePropagationPythonRelayUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-pathresp-python-relay-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyIngressListenPort := allocateUDPPort(t)
	requesterPort := allocateUDPPort(t)
	pyEgressListenPort := allocateUDPPort(t)
	responderPort := allocateUDPPort(t)

	scriptPath := filepath.Join(tmpDir, "integrated_relay.py")
	if err := os.WriteFile(scriptPath, []byte(integratedRelayPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_relay")

	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyIngressListenPort), strconv.Itoa(requesterPort), strconv.Itoa(pyEgressListenPort), strconv.Itoa(responderPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	scanner := bufio.NewScanner(pyStdout)
	relayReady := false
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python Relay] %v\n", line)
		if strings.Contains(line, "RELAY_READY") {
			relayReady = true
			break
		}
	}
	if !relayReady {
		t.Fatal("python relay did not report ready")
	}

	go func() {
		for scanner.Scan() {
			fmt.Printf("[Python Relay] %v\n", scanner.Text())
		}
	}()

	requestConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: requesterPort})
	if err != nil {
		t.Fatalf("failed to open requester UDP socket: %v", err)
	}
	defer func() { _ = requestConn.Close() }()

	responderConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: responderPort})
	if err != nil {
		t.Fatalf("failed to open responder UDP socket: %v", err)
	}
	defer func() { _ = responderConn.Close() }()

	remoteID, _ := NewIdentity(true)
	remoteDest, err := NewDestinationWithTransport(nil, remoteID, DestinationIn, DestinationSingle, "relay", "python_relay_target")
	if err != nil {
		t.Fatalf("failed creating remote destination: %v", err)
	}

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	tag := bytes.Repeat([]byte{0xA5}, TruncatedHashLength/8)
	requestData := make([]byte, 0, len(remoteDest.Hash)+len(tag))
	requestData = append(requestData, remoteDest.Hash...)
	requestData = append(requestData, tag...)

	requestPacket := NewPacket(pathReqDest, requestData)
	if err := requestPacket.Pack(); err != nil {
		t.Fatalf("failed packing relayed path request packet: %v", err)
	}

	if _, err := requestConn.WriteToUDP(requestPacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: pyIngressListenPort}); err != nil {
		t.Fatalf("failed sending path request to python relay ingress: %v", err)
	}

	buf := make([]byte, 4096)
	deadline := time.Now().Add(25 * time.Second)
	var forwardedReq *Packet
	for time.Now().Before(deadline) {
		if err := responderConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("failed setting responder read deadline: %v", err)
		}
		n, _, err := responderConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("failed reading forwarded path request from python relay: %v", err)
		}

		candidate := NewPacketFromRaw(buf[:n])
		if err := candidate.Unpack(); err != nil {
			continue
		}
		if !bytes.Equal(candidate.DestinationHash, pathReqDest.Hash) {
			continue
		}
		if len(candidate.Data) < len(remoteDest.Hash) || !bytes.Equal(candidate.Data[:len(remoteDest.Hash)], remoteDest.Hash) {
			continue
		}
		forwardedReq = candidate
		break
	}
	if forwardedReq == nil {
		t.Fatal("timed out waiting for forwarded path request from python relay")
	}

	responseTag := []byte(nil)
	if len(forwardedReq.Data) > len(remoteDest.Hash) {
		responseTag = append([]byte(nil), forwardedReq.Data[len(remoteDest.Hash):]...)
	}

	responsePacket, err := remoteDest.buildAnnouncePacket(responseTag)
	if err != nil {
		t.Fatalf("failed building synthetic path response announce: %v", err)
	}
	responsePacket.Context = ContextPathResponse
	responsePacket.HeaderType = Header2
	responsePacket.TransportType = TransportForward
	responsePacket.TransportID = bytes.Repeat([]byte{0x5B}, TruncatedHashLength/8)
	if err := responsePacket.Pack(); err != nil {
		t.Fatalf("failed packing synthetic path response announce: %v", err)
	}

	if _, err := responderConn.WriteToUDP(responsePacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: pyEgressListenPort}); err != nil {
		t.Fatalf("failed sending synthetic path response announce to python relay egress: %v", err)
	}

	responseDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(responseDeadline) {
		if err := requestConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("failed setting requester read deadline: %v", err)
		}

		n, _, err := requestConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("failed reading relayed path response from python relay: %v", err)
		}

		response := NewPacketFromRaw(buf[:n])
		if err := response.Unpack(); err != nil {
			continue
		}
		if response.PacketType != PacketAnnounce {
			continue
		}
		if response.Context != ContextPathResponse {
			continue
		}
		if !bytes.Equal(response.DestinationHash, remoteDest.Hash) {
			continue
		}
		os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)
		if err := pyCmd.Wait(); err != nil {
			t.Fatalf("python relay exited with error: %v", err)
		}
		return
	}

	t.Fatal("timed out waiting for relayed ContextPathResponse via Python relay")
}

func TestIntegratedAnnouncePropagationPythonRelayUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-announce-python-relay-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyIngressListenPort := allocateUDPPort(t)
	requesterPort := allocateUDPPort(t)
	pyEgressListenPort := allocateUDPPort(t)
	sinkPort := allocateUDPPort(t)

	scriptPath := filepath.Join(tmpDir, "integrated_relay.py")
	if err := os.WriteFile(scriptPath, []byte(integratedRelayFullModePy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_relay")

	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyIngressListenPort), strconv.Itoa(sinkPort), strconv.Itoa(pyEgressListenPort), strconv.Itoa(sinkPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	scanner := bufio.NewScanner(pyStdout)
	relayReady := false
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python Relay] %v\n", line)
		if strings.Contains(line, "RELAY_READY") {
			relayReady = true
			break
		}
	}
	if !relayReady {
		t.Fatal("python relay did not report ready")
	}

	sinkConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sinkPort})
	if err != nil {
		t.Fatalf("failed to open announce sink UDP socket: %v", err)
	}
	defer func() { _ = sinkConn.Close() }()

	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python Relay] %v\n", line)
		}
	}()

	emitterScriptPath := filepath.Join(tmpDir, "integrated_announce_emitter.py")
	if err := os.WriteFile(emitterScriptPath, []byte(integratedAnnounceEmitterPy), 0o644); err != nil {
		t.Fatal(err)
	}
	emitterConfigDir := filepath.Join(tmpDir, "py_emitter")

	emitterCmd := exec.Command("python3", emitterScriptPath, emitterConfigDir, strconv.Itoa(requesterPort), strconv.Itoa(pyIngressListenPort))
	emitterCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	emitterStdout, err := emitterCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	emitterCmd.Stderr = emitterCmd.Stdout
	if err := emitterCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer emitterCmd.Process.Kill()

	emitterDestHash := []byte(nil)
	emitterScanner := bufio.NewScanner(emitterStdout)
	emitterReady := false
	for emitterScanner.Scan() {
		line := emitterScanner.Text()
		fmt.Printf("[Python Emitter] %v\n", line)
		if strings.Contains(line, "EMITTER_READY") {
			emitterReady = true
		}
		if strings.HasPrefix(line, "EMITTER_DEST_HASH:") {
			h, err := HexToBytes(strings.TrimPrefix(line, "EMITTER_DEST_HASH:"))
			if err != nil {
				t.Fatalf("failed parsing emitter destination hash: %v", err)
			}
			emitterDestHash = h
		}
		if emitterReady && len(emitterDestHash) > 0 {
			break
		}
	}
	if !emitterReady {
		t.Fatal("python announce emitter did not report ready")
	}
	if len(emitterDestHash) == 0 {
		t.Fatal("python announce emitter did not provide destination hash")
	}

	go func() {
		for emitterScanner.Scan() {
			fmt.Printf("[Python Emitter] %v\n", emitterScanner.Text())
		}
	}()

	deadline := time.Now().Add(20 * time.Second)
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		if err := sinkConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("failed setting sink read deadline: %v", err)
		}

		n, _, err := sinkConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("failed reading rebroadcast packet from python relay: %v", err)
		}

		candidate := NewPacketFromRaw(buf[:n])
		if err := candidate.Unpack(); err != nil {
			continue
		}
		if candidate.PacketType != PacketAnnounce {
			continue
		}
		if !bytes.Equal(candidate.DestinationHash, emitterDestHash) {
			continue
		}
		if candidate.HeaderType != Header2 {
			t.Fatalf("expected Header2 rebroadcast, got %v", candidate.HeaderType)
		}
		if candidate.TransportType != TransportForward {
			t.Fatalf("expected TransportForward rebroadcast, got %v", candidate.TransportType)
		}

		os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)
		os.WriteFile(filepath.Join(emitterConfigDir, "done"), []byte("done"), 0o644)
		if err := emitterCmd.Wait(); err != nil {
			t.Fatalf("python announce emitter exited with error: %v", err)
		}
		if err := pyCmd.Wait(); err != nil {
			t.Fatalf("python relay exited with error: %v", err)
		}
		return
	}

	t.Fatalf("timed out waiting for rebroadcasted announce packet for %x", emitterDestHash)
}

func TestIntegratedPathInvalidationRediscoveryPythonToGo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-path-invalidate-py-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0o700)
	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	id, _ := NewIdentity(true)
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "integrated_test", "invalidate_py")
	if err != nil {
		t.Fatal(err)
	}

	stopAnnounce := make(chan struct{})
	announceStopped := false
	defer func() {
		if !announceStopped {
			close(stopAnnounce)
		}
	}()
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = dest.Announce(nil)
			case <-stopAnnounce:
				return
			}
		}
	}()

	scriptPath := filepath.Join(tmpDir, "integrated_path_invalidation_requester.py")
	if err := os.WriteFile(scriptPath, []byte(integratedPathInvalidationRequesterPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_requester")
	learnedPath := filepath.Join(pyConfigDir, "learned")

	pyCmd := exec.Command("python3", scriptPath, fmt.Sprintf("%x", dest.Hash), pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	stopDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(stopDeadline) {
		if _, err := os.Stat(learnedPath); err == nil {
			close(stopAnnounce)
			announceStopped = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	seenLearned := false
	seenInvalidated := false
	seenRediscovered := false
	scanner := bufio.NewScanner(pyStdout)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python Invalidate] %v\n", line)
		if strings.Contains(line, "PATH_LEARNED") {
			seenLearned = true
		}
		if strings.Contains(line, "PATH_INVALIDATED") {
			seenInvalidated = true
		}
		if strings.Contains(line, "PATH_REDISCOVERED") {
			seenRediscovered = true
		}
	}

	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("python invalidation requester failed: %v", err)
	}

	if !seenLearned {
		t.Fatal("python requester did not report initial path learning")
	}
	if !seenInvalidated {
		t.Fatal("python requester did not report path invalidation")
	}
	if !seenRediscovered {
		t.Fatal("python requester did not report path rediscovery")
	}
}

func TestIntegratedRelayedPathResponseGoRequesterToPythonTargetUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-pathresp-go-relay-python-target-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyListenPort := allocateUDPPort(t)
	relayEgressListenPort := allocateUDPPort(t)
	relayIngressListenPort := allocateUDPPort(t)
	requesterPort := allocateUDPPort(t)

	scriptPath := filepath.Join(tmpDir, "integrated_path_response_target.py")
	if err := os.WriteFile(scriptPath, []byte(integratedPathResponseTargetPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_receiver")

	tag := bytes.Repeat([]byte{0xC7}, TruncatedHashLength/8)
	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(relayEgressListenPort), fmt.Sprintf("%x", tag))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	scanner := bufio.NewScanner(pyStdout)
	var destHashHex string
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python Receiver] %v\n", line)
		if strings.HasPrefix(line, "Destination Hash: ") {
			destHashHex = strings.TrimPrefix(line, "Destination Hash: ")
			break
		}
	}
	if destHashHex == "" {
		t.Fatal("could not get destination hash from Python receiver")
	}
	destHash, err := HexToBytes(destHashHex)
	if err != nil {
		t.Fatalf("failed parsing destination hash: %v", err)
	}

	go func() {
		for scanner.Scan() {
			fmt.Printf("[Python Receiver] %v\n", scanner.Text())
		}
	}()

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	os.MkdirAll(goConfigDir, 0o700)
	goConfigContent := fmt.Sprintf(`[reticulum]
share_instance = No
enable_transport = False

[interfaces]
  [[Ingress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v

  [[Egress]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v
`, relayIngressListenPort, requesterPort, relayEgressListenPort, pyListenPort)
	os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600)

	SetLogLevel(LogDebug)
	r, err := NewReticulum(goConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeReticulum(t, r)

	requestConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: requesterPort})
	if err != nil {
		t.Fatalf("failed opening requester UDP socket: %v", err)
	}
	defer func() { _ = requestConn.Close() }()

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	requestData := make([]byte, 0, len(destHash)+len(tag))
	requestData = append(requestData, destHash...)
	requestData = append(requestData, tag...)

	requestPacket := NewPacket(pathReqDest, requestData)
	if err := requestPacket.Pack(); err != nil {
		t.Fatalf("failed packing path request packet: %v", err)
	}

	if _, err := requestConn.WriteToUDP(requestPacket.Raw, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: relayIngressListenPort}); err != nil {
		t.Fatalf("failed sending path request to relay ingress: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	os.WriteFile(filepath.Join(pyConfigDir, "emit_path_response"), []byte("emit"), 0o644)

	buf := make([]byte, 4096)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := requestConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("failed setting requester read deadline: %v", err)
		}

		n, _, err := requestConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			t.Fatalf("failed reading relay response: %v", err)
		}

		p := NewPacketFromRaw(buf[:n])
		if err := p.Unpack(); err != nil {
			continue
		}
		if p.PacketType != PacketAnnounce {
			continue
		}
		if p.Context != ContextPathResponse {
			continue
		}
		if !bytes.Equal(p.DestinationHash, destHash) {
			continue
		}

		os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)
		if err := pyCmd.Wait(); err != nil {
			t.Fatalf("python receiver exited with error: %v", err)
		}
		return
	}

	t.Fatal("timed out waiting for relayed ContextPathResponse from Python target")
}

func TestIntegratedPythonRelayPathRequestEmissionUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-python-relay-pr-emitter-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyIngressListenPort := allocateUDPPort(t)
	requesterPort := allocateUDPPort(t)
	pyEgressListenPort := allocateUDPPort(t)
	sinkPort := allocateUDPPort(t)

	targetID, _ := NewIdentity(true)
	targetDest, err := NewDestinationWithTransport(nil, targetID, DestinationIn, DestinationSingle, "relay", "forward_target")
	if err != nil {
		t.Fatalf("failed creating target destination: %v", err)
	}
	tag := bytes.Repeat([]byte{0x3C}, TruncatedHashLength/8)

	scriptPath := filepath.Join(tmpDir, "integrated_relay_path_request_emitter.py")
	if err := os.WriteFile(scriptPath, []byte(integratedRelayPathRequestEmitterPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_relay_emitter")

	pyCmd := exec.Command(
		"python3", scriptPath,
		pyConfigDir,
		strconv.Itoa(pyIngressListenPort),
		strconv.Itoa(requesterPort),
		strconv.Itoa(pyEgressListenPort),
		strconv.Itoa(sinkPort),
		fmt.Sprintf("%x", targetDest.Hash),
		fmt.Sprintf("%x", tag),
	)
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyStdout, err := pyCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer pyCmd.Process.Kill()

	sinkConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sinkPort})
	if err != nil {
		t.Fatalf("failed opening sink UDP socket: %v", err)
	}
	defer func() { _ = sinkConn.Close() }()

	lineCh := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(pyStdout)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python Relay Emitter] %v\n", line)
			lineCh <- line
		}
		close(lineCh)
	}()

	readyTimeout := time.NewTimer(15 * time.Second)
	defer readyTimeout.Stop()
	for {
		select {
		case line, ok := <-lineCh:
			if !ok {
				t.Fatal("python relay emitter exited before ready")
			}
			if strings.Contains(line, "EMITTER_EGRESS_MISSING") {
				t.Fatal("python relay emitter could not find egress interface")
			}
			if strings.Contains(line, "EMITTER_READY") {
				goto waitPacket
			}
		case <-readyTimeout.C:
			t.Fatal("timed out waiting for python relay emitter readiness")
		}
	}

waitPacket:
	if err := sinkConn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("failed setting sink read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := sinkConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed reading emitted path request packet: %v", err)
	}

	packet := NewPacketFromRaw(buf[:n])
	if err := packet.Unpack(); err != nil {
		t.Fatalf("failed unpacking emitted path request packet: %v", err)
	}

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	if packet.PacketType != PacketData {
		t.Fatalf("expected DATA packet type, got %v", packet.PacketType)
	}
	if !bytes.Equal(packet.DestinationHash, pathReqDest.Hash) {
		t.Fatalf("unexpected emitted path request destination hash")
	}
	if len(packet.Data) < len(targetDest.Hash)+(TruncatedHashLength/8)+len(tag) {
		t.Fatalf("emitted path request data too short: %v", len(packet.Data))
	}
	if !bytes.Equal(packet.Data[:len(targetDest.Hash)], targetDest.Hash) {
		t.Fatalf("emitted path request target hash mismatch")
	}
	emittedTag := packet.Data[len(packet.Data)-len(tag):]
	if !bytes.Equal(emittedTag, tag) {
		t.Fatalf("emitted path request tag mismatch")
	}

	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("python relay emitter exited with error: %v", err)
	}
}

func TestIntegratedPythonRelayInboundPathRequestForwardingUDP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tmpDir, err := os.MkdirTemp("", "go-reticulum-python-relay-inbound-forward-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	ResetTransport()

	pyRelayIngressListenPort := allocateUDPPort(t)
	pyRequesterListenPort := allocateUDPPort(t)
	pyRelayEgressListenPort := allocateUDPPort(t)
	sinkPort := allocateUDPPort(t)

	targetID, _ := NewIdentity(true)
	targetDest, err := NewDestinationWithTransport(nil, targetID, DestinationIn, DestinationSingle, "relay", "inbound_forward_target")
	if err != nil {
		t.Fatalf("failed creating target destination: %v", err)
	}
	tag := bytes.Repeat([]byte{0x52}, TruncatedHashLength/8)

	relayScriptPath := filepath.Join(tmpDir, "integrated_relay.py")
	if err := os.WriteFile(relayScriptPath, []byte(integratedRelayPy), 0o644); err != nil {
		t.Fatal(err)
	}
	relayConfigDir := filepath.Join(tmpDir, "py_relay")

	relayCmd := exec.Command(
		"python3", relayScriptPath,
		relayConfigDir,
		strconv.Itoa(pyRelayIngressListenPort),
		strconv.Itoa(pyRequesterListenPort),
		strconv.Itoa(pyRelayEgressListenPort),
		strconv.Itoa(sinkPort),
	)
	relayCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	relayStdout, err := relayCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	relayCmd.Stderr = relayCmd.Stdout
	if err := relayCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer relayCmd.Process.Kill()

	relayLines := make(chan string, 128)
	go func() {
		scanner := bufio.NewScanner(relayStdout)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python Relay] %v\n", line)
			relayLines <- line
		}
		close(relayLines)
	}()

	readyTimeout := time.NewTimer(15 * time.Second)
	defer readyTimeout.Stop()
	for {
		select {
		case line, ok := <-relayLines:
			if !ok {
				t.Fatal("python relay exited before ready")
			}
			if strings.Contains(line, "RELAY_READY") {
				goto relayReady
			}
		case <-readyTimeout.C:
			t.Fatal("timed out waiting for python relay readiness")
		}
	}

relayReady:
	sinkConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: sinkPort})
	if err != nil {
		t.Fatalf("failed opening sink UDP socket: %v", err)
	}
	defer func() { _ = sinkConn.Close() }()

	requesterScriptPath := filepath.Join(tmpDir, "integrated_path_requester_emitter.py")
	if err := os.WriteFile(requesterScriptPath, []byte(integratedPathRequesterEmitterPy), 0o644); err != nil {
		t.Fatal(err)
	}
	requesterConfigDir := filepath.Join(tmpDir, "py_requester")

	requesterCmd := exec.Command(
		"python3", requesterScriptPath,
		requesterConfigDir,
		strconv.Itoa(pyRequesterListenPort),
		strconv.Itoa(pyRelayIngressListenPort),
		fmt.Sprintf("%x", targetDest.Hash),
		fmt.Sprintf("%x", tag),
	)
	requesterCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	requesterStdout, err := requesterCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	requesterCmd.Stderr = requesterCmd.Stdout
	if err := requesterCmd.Start(); err != nil {
		t.Fatal(err)
	}

	go func() {
		scanner := bufio.NewScanner(requesterStdout)
		for scanner.Scan() {
			fmt.Printf("[Python Requester] %v\n", scanner.Text())
		}
	}()

	if err := sinkConn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatalf("failed setting sink read deadline: %v", err)
	}
	buf := make([]byte, 4096)
	n, _, err := sinkConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed reading forwarded inbound path request: %v", err)
	}

	packet := NewPacketFromRaw(buf[:n])
	if err := packet.Unpack(); err != nil {
		t.Fatalf("failed unpacking forwarded inbound path request: %v", err)
	}

	pathReqDest, err := NewDestination(nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("failed creating path request destination: %v", err)
	}

	if packet.PacketType != PacketData {
		t.Fatalf("expected DATA packet type, got %v", packet.PacketType)
	}
	if !bytes.Equal(packet.DestinationHash, pathReqDest.Hash) {
		t.Fatalf("unexpected forwarded destination hash")
	}
	if len(packet.Data) < len(targetDest.Hash)+len(tag) {
		t.Fatalf("forwarded packet data too short: %v", len(packet.Data))
	}
	if !bytes.Equal(packet.Data[:len(targetDest.Hash)], targetDest.Hash) {
		t.Fatalf("forwarded packet target hash mismatch")
	}
	if !bytes.Equal(packet.Data[len(packet.Data)-len(tag):], tag) {
		t.Fatalf("forwarded packet tag mismatch")
	}

	os.WriteFile(filepath.Join(relayConfigDir, "done"), []byte("done"), 0o644)
	if err := requesterCmd.Wait(); err != nil {
		t.Fatalf("python requester exited with error: %v", err)
	}
	if err := relayCmd.Wait(); err != nil {
		t.Fatalf("python relay exited with error: %v", err)
	}
}
