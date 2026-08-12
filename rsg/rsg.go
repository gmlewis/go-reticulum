// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package rsg implements the canonical Reticulum Signature Graph (RSG)
// format: a signed envelope containing the hash type, the SHA-256 of the
// signed message, and metadata (the signer's identity hash and public key).
//
// An RSG blob is the concatenation of an Ed25519 signature over the
// msgpack-encoded envelope and the envelope itself:
//
//	signature || msgpack({hashtype, hash, meta, [message]})
//
// When the optional message field is present the blob is an RSM (Reticulum
// Signed Message). This package mirrors Python's create_rsg,
// create_rsg(embed=True), validate_rsg and extract_signed_rsg_data helpers
// from RNS/Utilities/rnid.py so that tools such as gornid and gorngcs can
// share a single implementation.
package rsg

import (
	"crypto/sha256"
	"errors"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// SigLen is the byte length of an Ed25519 signature.
const SigLen = rns.IdentityKeySize / 8

// HashType is the only hash type supported by the canonical RSG format.
const HashType = "sha256"

// Options controls the extended CreateWithOptions behaviour, matching
// Python's create_rsg embed and meta parameters.
type Options struct {
	// Embed, when true, stores the message bytes under the "message" key
	// of the envelope (an embedded signed message, RSM).
	Embed bool
	// Meta is an optional ordered map of extra metadata keys merged into
	// the envelope's "meta" map. Existing keys ("signer", "pubkey") are
	// never overwritten by entries in Meta. The order of entries in Meta
	// is preserved on the wire so that the packed envelope is
	// byte-identical to Python's create_rsg, which iterates a regular
	// dict in insertion order. Use msgpack.OrderedMap (built with
	// append or Set) so the key order matches the Python dict's
	// insertion order.
	Meta msgpack.OrderedMap
}

// Create builds the canonical Reticulum Signature Graph (RSG) binary blob
// for message, signed by signer. The format is: signature + msgpack
// envelope, where the envelope contains the hash type, the SHA-256 of the
// message, and metadata (signer hash + public key). This matches Python's
// create_rsg.
func Create(signer *rns.Identity, message []byte) ([]byte, error) {
	return CreateWithOptions(signer, message, Options{})
}

// CreateWithOptions builds an RSG (or RSM when Embed is true) blob for
// message, signed by signer, optionally embedding the message and merging
// extra metadata. This matches Python's create_rsg(signer, message,
// embed=False, meta=None, output="bin").
//
// The envelope is built with OrderedMap so the on-wire key order matches
// Python dict insertion order (hashtype, hash, meta, [message]; meta:
// signer, pubkey, ...). This makes signatures byte-identical to Python's
// create_rsg for the deterministic (no-user-meta) case, since Ed25519
// signing is deterministic.
func CreateWithOptions(signer *rns.Identity, message []byte, opts Options) ([]byte, error) {
	if signer == nil || signer.GetPrivateKey() == nil {
		return nil, errors.New("signer does not hold a private key")
	}
	hash := sha256.Sum256(message)
	meta := msgpack.OrderedMap{
		{Key: "signer", Value: signer.Hash},
		{Key: "pubkey", Value: signer.GetPublicKey()},
	}
	if opts.Meta != nil {
		for _, entry := range opts.Meta {
			if _, exists := meta.Get(entry.Key); !exists {
				meta = meta.Set(entry.Key, entry.Value)
			}
		}
	}
	signedData := msgpack.OrderedMap{
		{Key: "hashtype", Value: HashType},
		{Key: "hash", Value: hash[:]},
		{Key: "meta", Value: meta},
	}
	if opts.Embed {
		signedData = signedData.Set("message", message)
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

// ExtractSignedData unpacks the envelope portion of an RSG/RSM blob
// (skipping the leading signature) into a map[any]any, matching Python's
// extract_signed_rsg_data. The returned map has string keys; nested meta
// is a map[any]any. Returns an error if the blob is too short or not a map.
func ExtractSignedData(rsgData []byte) (map[any]any, error) {
	if len(rsgData) < SigLen {
		return nil, errors.New("rsg data too short to contain signature")
	}
	envelope := rsgData[SigLen:]
	unpacked, err := msgpack.UnpackPreserveBinMapKeys(envelope)
	if err != nil {
		return nil, err
	}
	m, ok := unpacked.(map[any]any)
	if !ok {
		return nil, errors.New("envelope is not a map")
	}
	return m, nil
}

// Validate validates an RSG blob against message, optionally requiring the
// signer to match requiredSignerHash. It returns the signing identity on
// success. This matches Python's validate_rsg.
func Validate(rsgData, message []byte, requiredSignerHash []byte) (*rns.Identity, error) {
	if len(rsgData) < SigLen+1 {
		return nil, errors.New("rsg data too short")
	}
	signature := rsgData[:SigLen]
	envelope := rsgData[SigLen:]

	unpacked, err := msgpack.UnpackPreserveBinMapKeys(envelope)
	if err != nil {
		return nil, err
	}
	signedData, ok := unpacked.(map[any]any)
	if !ok {
		return nil, errors.New("envelope is not a map")
	}
	hashType, _ := signedData["hashtype"].(string)
	if hashType != HashType {
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

// IsLegacyFormat reports whether rsgData is a legacy raw-signature format
// (exactly one signature length, no envelope).
func IsLegacyFormat(rsgData []byte) bool {
	return len(rsgData) == SigLen
}

// ValidateLegacy validates a legacy raw-signature RSG against message using
// the provided identity's public key.
func ValidateLegacy(rsgData, message []byte, identity *rns.Identity) bool {
	if len(rsgData) != SigLen {
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
