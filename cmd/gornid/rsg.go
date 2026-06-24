// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"errors"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// sigLen is the byte length of an Ed25519 signature.
const sigLen = rns.IdentityKeySize / 8

// rsgHashType is the only hash type supported by the canonical RSG format.
const rsgHashType = "sha256"

// createRSG builds the canonical Reticulum Signature Graph (RSG) binary blob
// for message, signed by signer. The format is: signature + msgpack envelope,
// where the envelope contains the hash type, the SHA-256 of the message, and
// metadata (signer hash + public key). This matches Python's create_rsg.
func createRSG(signer *rns.Identity, message []byte) ([]byte, error) {
	if signer == nil || signer.GetPrivateKey() == nil {
		return nil, errors.New("signer does not hold a private key")
	}
	hash := sha256.Sum256(message)
	signedData := map[string]any{
		"hashtype": rsgHashType,
		"hash":     hash[:],
		"meta": map[string]any{
			"signer": signer.Hash,
			"pubkey": signer.GetPublicKey(),
		},
	}
	envelope, err := msgpack.Pack(signedData)
	if err != nil {
		return nil, err
	}
	signature, err := signer.Sign(envelope)
	if err != nil {
		return nil, err
	}
	return append(signature, envelope...), nil
}

// validateRSG validates an RSG blob against message, optionally requiring the
// signer to match requiredSignerHash. It returns the signing identity on
// success. This matches Python's validate_rsg.
func validateRSG(rsgData, message []byte, requiredSignerHash []byte) (*rns.Identity, error) {
	if len(rsgData) < sigLen+1 {
		return nil, errors.New("rsg data too short")
	}
	signature := rsgData[:sigLen]
	envelope := rsgData[sigLen:]

	unpacked, err := msgpack.UnpackPreserveBinMapKeys(envelope)
	if err != nil {
		return nil, err
	}
	signedData, ok := unpacked.(map[any]any)
	if !ok {
		return nil, errors.New("envelope is not a map")
	}
	hashType, _ := signedData["hashtype"].(string)
	if hashType != rsgHashType {
		return nil, errors.New("unsupported hashtype")
	}
	storedHash, _ := signedData["hash"].([]byte)
	computed := sha256.Sum256(message)
	if !equalBytes(storedHash, computed[:]) {
		return nil, errors.New("hash mismatch")
	}
	meta, _ := signedData["meta"].(map[any]any)
	if meta == nil {
		return nil, errors.New("missing meta")
	}
	signerHash, _ := meta["signer"].([]byte)
	pubKey, _ := meta["pubkey"].([]byte)
	if pubKey == nil {
		return nil, errors.New("missing pubkey in meta")
	}

	signingID, err := rns.NewIdentity(false, nil)
	if err != nil {
		return nil, err
	}
	if err := signingID.LoadPublicKey(pubKey); err != nil {
		return nil, err
	}

	if requiredSignerHash != nil && !equalBytes(signerHash, requiredSignerHash) {
		return signingID, errors.New("signer hash mismatch")
	}
	if requiredSignerHash == nil {
		requiredSignerHash = signingID.Hash
	}
	if !equalBytes(signingID.Hash, requiredSignerHash) {
		return signingID, errors.New("signing identity hash mismatch")
	}
	if !signingID.Verify(signature, envelope) {
		return signingID, errors.New("signature verification failed")
	}
	return signingID, nil
}

// rsgIsLegacyFormat reports whether rsgData is a legacy raw-signature format
// (exactly one signature length, no envelope).
func rsgIsLegacyFormat(rsgData []byte) bool {
	return len(rsgData) == sigLen
}

// validateLegacyRSG validates a legacy raw-signature RSG against message using
// the provided identity's public key.
func validateLegacyRSG(rsgData, message []byte, identity *rns.Identity) bool {
	if len(rsgData) != sigLen {
		return false
	}
	return identity.Verify(rsgData, message)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
