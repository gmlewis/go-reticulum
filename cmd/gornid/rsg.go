// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/rsg"
)

// sigLen is the byte length of an Ed25519 signature.
const sigLen = rsg.SigLen

// rsgHashType is the only hash type supported by the canonical RSG format.
const rsgHashType = rsg.HashType

// rsgOptions controls the extended createRSGWithOptions behaviour,
// matching Python's create_rsg embed and meta parameters. It is a thin
// adapter over rsg.Options so existing gornid callers keep their
// lower-case field names.
type rsgOptions struct {
	embed bool
	meta  msgpack.OrderedMap
}

// createRSG builds a canonical RSG blob for message, signed by signer.
func createRSG(signer *rns.Identity, message []byte) ([]byte, error) {
	return rsg.Create(signer, message)
}

// createRSGWithOptions builds an RSG (or RSM when embed is true) blob.
func createRSGWithOptions(signer *rns.Identity, message []byte, opts rsgOptions) ([]byte, error) {
	return rsg.CreateWithOptions(signer, message, rsg.Options{Embed: opts.embed, Meta: opts.meta})
}

// extractSignedRSGData unpacks the envelope portion of an RSG/RSM blob.
func extractSignedRSGData(rsgData []byte) (map[any]any, error) {
	return rsg.ExtractSignedData(rsgData)
}

// validateRSG validates an RSG blob against message.
func validateRSG(rsgData, message []byte, requiredSignerHash []byte) (*rns.Identity, error) {
	return rsg.Validate(rsgData, message, requiredSignerHash)
}

// rsgIsLegacyFormat reports whether rsgData is a legacy raw-signature blob.
func rsgIsLegacyFormat(rsgData []byte) bool {
	return rsg.IsLegacyFormat(rsgData)
}

// validateLegacyRSG validates a legacy raw-signature RSG against message.
func validateLegacyRSG(rsgData, message []byte, identity *rns.Identity) bool {
	return rsg.ValidateLegacy(rsgData, message, identity)
}
