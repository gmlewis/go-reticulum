// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestMessagePackUnpackRoundTrip(t *testing.T) {
	t.Parallel()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}

	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	fields := map[any]any{int64(FieldDebug): []byte("debug-data")}
	m := mustTestNewMessage(t, destination, source, "hello-content", "hello-title", fields)

	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if len(m.Packed) == 0 {
		t.Fatal("expected packed message bytes")
	}
	if len(m.Signature) != SignatureLength {
		t.Fatalf("signature length=%v want=%v", len(m.Signature), SignatureLength)
	}

	unpacked, err := UnpackMessageFromBytes(ts, m.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if !bytes.Equal(unpacked.DestinationHash, m.DestinationHash) {
		t.Fatalf("destination hash mismatch")
	}
	if !bytes.Equal(unpacked.SourceHash, m.SourceHash) {
		t.Fatalf("source hash mismatch")
	}
	if unpacked.TitleString() != "hello-title" {
		t.Fatalf("title=%q want=%q", unpacked.TitleString(), "hello-title")
	}
	if unpacked.ContentString() != "hello-content" {
		t.Fatalf("content=%q want=%q", unpacked.ContentString(), "hello-content")
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("expected signature to validate, reason=%v", unpacked.UnverifiedReason)
	}
	if got, ok := unpacked.Fields[int64(FieldDebug)].([]byte); !ok || !bytes.Equal(got, []byte("debug-data")) {
		t.Fatalf("fields mismatch: %#v", unpacked.Fields)
	}
}

func TestMessagePackIncludesStampAndUnpacksIt(t *testing.T) {
	t.Parallel()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}
	ts.Remember(nil, destination.Hash, destinationID.GetPublicKey(), nil)
	ts.Remember(nil, source.Hash, sourceID.GetPublicKey(), nil)

	m := mustTestNewMessage(t, destination, source, "content", "title", nil)
	stampCost := 4
	m.StampCost = &stampCost
	m.Stamp = []byte{0xAA, 0xBB, 0xCC}
	m.DeferStamp = false

	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	unpacked, err := UnpackMessageFromBytes(ts, m.Packed, MethodDirect)
	if err != nil {
		t.Fatalf("UnpackMessageFromBytes: %v", err)
	}

	if !bytes.Equal(unpacked.Stamp, []byte{0xAA, 0xBB, 0xCC}) {
		t.Fatalf("stamp mismatch: %x", unpacked.Stamp)
	}
	if !unpacked.SignatureValidated {
		t.Fatalf("expected stamped message signature to validate, reason=%v", unpacked.UnverifiedReason)
	}
	if len(unpacked.Payload) != 4 {
		t.Fatalf("unpacked payload length=%v want=4", len(unpacked.Payload))
	}
}

func TestMessageHashMatchesProtocolMaterial(t *testing.T) {
	t.Parallel()
	destinationID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(destination): %v", err)
	}
	sourceID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity(source): %v", err)
	}

	ts := rns.NewTransportSystem(nil)
	destination, err := rns.NewDestination(ts, destinationID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(destination): %v", err)
	}
	source, err := rns.NewDestination(ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	if err != nil {
		t.Fatalf("NewDestination(source): %v", err)
	}

	m := mustTestNewMessage(t, destination, source, "abc", "def", map[any]any{})
	m.Timestamp = 1700000000.25

	if err := m.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	payload, err := msgpack.Pack([]any{m.Timestamp, []byte("def"), []byte("abc"), map[any]any{}})
	if err != nil {
		t.Fatalf("Pack(payload): %v", err)
	}

	hashMaterial := make([]byte, 0, len(destination.Hash)+len(source.Hash)+len(payload))
	hashMaterial = append(hashMaterial, destination.Hash...)
	hashMaterial = append(hashMaterial, source.Hash...)
	hashMaterial = append(hashMaterial, payload...)

	wantHash := rns.FullHash(hashMaterial)
	if !bytes.Equal(m.Hash, wantHash) {
		t.Fatalf("hash mismatch\n got: %x\nwant: %x", m.Hash, wantHash)
	}
}

func TestWriteToDirectory(t *testing.T) {
	t.Parallel()
	destID, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	srcID, err := rns.NewIdentity(true, nil)
	mustTest(t, err)
	ts := rns.NewTransportSystem(nil)
	dest, err := rns.NewDestination(ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	mustTest(t, err)
	src, err := rns.NewDestination(ts, srcID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	mustTest(t, err)

	msg := mustTestNewMessage(t, dest, src, "hello", "greet", nil)

	dir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	path, err := msg.WriteToDirectory(dir)
	if err != nil {
		t.Fatalf("WriteToDirectory error = %v", err)
	}

	wantPath := dir + "/" + fmt.Sprintf("%x", msg.Hash)
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file error = %v", err)
	}

	// Verify it's valid msgpack with expected keys.
	v, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("unpack error = %v", err)
	}
	m, ok := v.(map[any]any)
	if !ok {
		t.Fatalf("unpacked type = %T, want map[any]any", v)
	}
	if _, ok := m["lxmf_bytes"]; !ok {
		t.Fatalf("missing 'lxmf_bytes' key in container")
	}
	if _, ok := m["state"]; !ok {
		t.Fatalf("missing 'state' key in container")
	}
	if _, ok := m["method"]; !ok {
		t.Fatalf("missing 'method' key in container")
	}
}
