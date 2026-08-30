// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	rnscrypto "github.com/gmlewis/go-reticulum/rns/crypto"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRegisterDeliveryIdentityEnablesRatchets verifies that registering a
// delivery identity enables ratchets on the destination and persists the
// ratchet data file, matching LXMRouter.register_delivery_identity.
func TestRegisterDeliveryIdentityEnablesRatchets(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)

	id := mustTestNewIdentity(t, true)
	var zero int
	destination, err := router.RegisterDeliveryIdentity(id, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}

	ratchetFile := filepath.Join(tmpDir, "lxmf", "ratchets", hex.EncodeToString(destination.Hash)+".ratchets")
	if _, err := os.Stat(ratchetFile); err != nil {
		t.Fatalf("delivery destination ratchet file was not created: %v", err)
	}

	// The destination must now be rotatable: previously this failed with
	// "ratchets are not enabled" because the router never enabled them.
	if err := destination.RotateRatchets(); err != nil {
		t.Fatalf("RotateRatchets after registration: %v", err)
	}

	if len(destination.LatestRatchetID()) == 0 {
		t.Fatal("expected LatestRatchetID to be set after ratchet rotation")
	}

	// The persisted file must use the signed {"signature", "ratchets"} format
	// that the Python implementation also reads and writes.
	data, err := os.ReadFile(ratchetFile)
	if err != nil {
		t.Fatalf("read ratchet file: %v", err)
	}
	unpacked, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("unpack ratchet file: %v", err)
	}
	fields, ok := unpacked.(map[any]any)
	if !ok {
		t.Fatalf("ratchet file should decode to a map, got %T", unpacked)
	}
	signature, ok := fields["signature"].([]byte)
	if !ok || len(signature) != 64 {
		t.Fatalf("ratchet file signature field invalid: %T len ok=%v", fields["signature"], ok)
	}
	packedRatchets, ok := fields["ratchets"].([]byte)
	if !ok {
		t.Fatalf("ratchet file ratchets field missing, got %T", fields["ratchets"])
	}
	list, err := msgpack.Unpack(packedRatchets)
	if err != nil {
		t.Fatalf("unpack ratchets list: %v", err)
	}
	keys, ok := list.([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("expected 1 ratchet key after rotation, got %T (len ok=%v)", list, ok)
	}
}

// TestRatchetedDestinationDecrypt verifies the full ratcheted encryption
// round trip: a sender that learned the receiving destination's ratchet key
// (as clients learn it from its announce) can encrypt, and the receiving
// delivery destination decrypts using its own ratchets. Without ratchets
// enabled on the receiving destination, decryption falls back to the primary
// identity key and fails with an invalid-token HMAC.
func TestRatchetedDestinationDecryptRoundTrip(t *testing.T) {
	t.Parallel()
	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	receiver := mustTestNewRouter(t, ts, nil, tmpDir)
	receiverID := mustTestNewIdentity(t, true)
	var zero int
	destination, err := receiver.RegisterDeliveryIdentity(receiverID, "", &zero)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}
	if err := destination.RotateRatchets(); err != nil {
		t.Fatalf("RotateRatchets: %v", err)
	}

	// Read the receiver's current ratchet public key from its persisted
	// ratchet file, as a remote client would receive it from the announce.
	ratchetFile := filepath.Join(tmpDir, "lxmf", "ratchets", hex.EncodeToString(destination.Hash)+".ratchets")
	data, err := os.ReadFile(ratchetFile)
	if err != nil {
		t.Fatalf("read ratchet file: %v", err)
	}
	fields, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("unpack ratchet file: %v", err)
	}
	m, ok := fields.(map[any]any)
	if !ok {
		t.Fatalf("ratchet file should decode to a map, got %T", fields)
	}
	list, err := msgpack.Unpack(m["ratchets"].([]byte))
	if err != nil {
		t.Fatalf("unpack ratchets list: %v", err)
	}
	items, ok := list.([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected at least 1 ratchet key, got %T", list)
	}
	prv, ok := items[0].([]byte)
	if !ok || len(prv) != 32 {
		t.Fatalf("ratchet key should be 32 bytes, got %T", items[0])
	}
	ratchetPrv, err := rnscrypto.NewX25519PrivateKeyFromBytes(prv)
	if err != nil {
		t.Fatalf("parse ratchet private key: %v", err)
	}
	ratchetPub := ratchetPrv.PublicKey().PublicBytes()

	// Simulate a sender that learned the ratchet from the announce, then
	// encrypt as python's Destination.encrypt does when a ratchet is known.
	senderDest := mustTestNewDestination(t, ts, receiverID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.SetRatchet(senderDest.Hash, ratchetPub)
	ciphertext, err := senderDest.Encrypt([]byte("ratchet round trip"))
	if err != nil {
		t.Fatalf("Encrypt with learned ratchet: %v", err)
	}

	decrypted, err := destination.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt with receiver ratchets: %v", err)
	}
	if string(decrypted) != "ratchet round trip" {
		t.Fatalf("round trip plaintext mismatch: %q", decrypted)
	}
}