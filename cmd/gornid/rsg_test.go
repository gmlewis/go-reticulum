// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func newSigningIdentity(t *testing.T) *rns.Identity {
	t.Helper()
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return id
}

func TestCreateRSGEmbeddedRoundTrip(t *testing.T) {
	t.Parallel()
	id := newSigningIdentity(t)
	msg := []byte("hello world")

	rsm, err := createRSGWithOptions(id, msg, rsgOptions{embed: true})
	if err != nil {
		t.Fatalf("createRSGWithOptions: %v", err)
	}

	extracted, err := extractSignedRSGData(rsm)
	if err != nil {
		t.Fatalf("extractSignedRSGData: %v", err)
	}
	gotMsg, ok := extracted["message"].([]byte)
	if !ok {
		t.Fatalf("extracted message is %T, not []byte", extracted["message"])
	}
	if !bytes.Equal(gotMsg, msg) {
		t.Errorf("embedded message = %q, want %q", gotMsg, msg)
	}

	// Validate the embedded RSM against the extracted message.
	signer, err := validateRSG(rsm, gotMsg, nil)
	if err != nil {
		t.Fatalf("validateRSG(embedded, correct message): %v", err)
	}
	if signer == nil {
		t.Fatal("validateRSG returned nil identity")
	}
	if !bytes.Equal(signer.Hash, id.Hash) {
		t.Errorf("signer hash mismatch: got %x, want %x", signer.Hash, id.Hash)
	}

	// Validating against the wrong message must fail.
	if _, err := validateRSG(rsm, []byte("WRONG"), nil); err == nil {
		t.Fatal("validateRSG with wrong message returned nil error")
	}
}

func TestCreateRSGWithMeta(t *testing.T) {
	t.Parallel()
	id := newSigningIdentity(t)
	msg := []byte("release payload")
	origin := make([]byte, 16)
	copy(origin, id.Hash[:16])

	meta := msgpack.OrderedMap{
		{Key: "name", Value: "pkg"},
		{Key: "version", Value: "1.0"},
		{Key: "origin", Value: origin},
		{Key: "path", Value: "/release"},
		// "signer" must NOT be overwritten by user meta.
		{Key: "signer", Value: []byte("should-not-overwrite")},
	}
	rsm, err := createRSGWithOptions(id, msg, rsgOptions{embed: true, meta: meta})
	if err != nil {
		t.Fatalf("createRSGWithOptions: %v", err)
	}

	extracted, err := extractSignedRSGData(rsm)
	if err != nil {
		t.Fatalf("extractSignedRSGData: %v", err)
	}
	metaMap, ok := extracted["meta"].(map[any]any)
	if !ok {
		t.Fatalf("meta is %T, want map[any]any", extracted["meta"])
	}
	for _, key := range []string{"name", "version", "origin", "path"} {
		if _, ok := metaMap[key]; !ok {
			t.Errorf("meta missing key %q", key)
		}
	}
	// signer must remain the identity hash, not the user-supplied value.
	signer, ok := metaMap["signer"].([]byte)
	if !ok {
		t.Fatalf("meta[signer] is %T, want []byte", metaMap["signer"])
	}
	if !bytes.Equal(signer, id.Hash) {
		t.Errorf("meta[signer] overwritten: got %x, want %x", signer, id.Hash)
	}
}

func TestCheckReleaseRSMStructureIntegration(t *testing.T) {
	t.Parallel()
	id := newSigningIdentity(t)
	origin := make([]byte, 16)
	copy(origin, id.Hash[:16])

	// Valid release meta.
	rsm, err := createRSGWithOptions(id, []byte("release"), rsgOptions{
		embed: true,
		meta: msgpack.OrderedMap{
			{Key: "name", Value: "pkg"},
			{Key: "version", Value: "1.0"},
			{Key: "origin", Value: origin},
			{Key: "path", Value: "/release"},
		},
	})
	if err != nil {
		t.Fatalf("createRSGWithOptions: %v", err)
	}
	extracted, err := extractSignedRSGData(rsm)
	if err != nil {
		t.Fatalf("extractSignedRSGData: %v", err)
	}
	if err := checkReleaseRSMStructure(extracted); err != nil {
		t.Errorf("valid release: expected nil, got %v", err)
	}

	// Incomplete release meta (missing origin).
	rsm2, err := createRSGWithOptions(id, []byte("release2"), rsgOptions{
		embed: true,
		meta: msgpack.OrderedMap{
			{Key: "name", Value: "pkg"},
			{Key: "version", Value: "1.0"},
			{Key: "path", Value: "/release"},
		},
	})
	if err != nil {
		t.Fatalf("createRSGWithOptions: %v", err)
	}
	extracted2, err := extractSignedRSGData(rsm2)
	if err != nil {
		t.Fatalf("extractSignedRSGData: %v", err)
	}
	if err := checkReleaseRSMStructure(extracted2); err == nil {
		t.Error("incomplete release: expected error, got nil")
	}
}

func TestExtractSignedRSGDataTooShort(t *testing.T) {
	t.Parallel()
	if _, err := extractSignedRSGData([]byte{1, 2, 3}); err == nil {
		t.Fatal("extractSignedRSGData on short input returned nil error")
	}
}

func TestCreateRSGWrapperUnchanged(t *testing.T) {
	t.Parallel()
	id := newSigningIdentity(t)
	msg := []byte("plain sign")
	rsg, err := createRSG(id, msg)
	if err != nil {
		t.Fatalf("createRSG: %v", err)
	}
	// No embedded message key.
	extracted, err := extractSignedRSGData(rsg)
	if err != nil {
		t.Fatalf("extractSignedRSGData: %v", err)
	}
	if _, ok := extracted["message"]; ok {
		t.Error("plain createRSG embedded a message key; it should not")
	}
}
