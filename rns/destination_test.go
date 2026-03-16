// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

func mustTestNewDestination(t *testing.T, identity *Identity, direction int, destType int, appName string, aspects ...string) *Destination {
	t.Helper()
	dest, err := NewDestination(identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func mustTestNewDestinationWithTransport(t *testing.T, ts *TransportSystem, identity *Identity, direction int, destType int, appName string, aspects ...string) *Destination {
	t.Helper()
	dest, err := NewDestinationWithTransport(ts, identity, direction, destType, appName, aspects...)
	mustTest(t, err)
	return dest
}

func TestDestination(t *testing.T) {
	id, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}

	// Test IN SINGLE destination
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "testapp", "aspect1", "aspect2")
	if err != nil {
		t.Fatal(err)
	}
	if dest.Type != DestinationSingle {
		t.Errorf("expected SINGLE destination type")
	}

	// Test ExpandName
	expectedName := "testapp.aspect1.aspect2." + id.HexHash
	if ExpandName(id, "testapp", "aspect1", "aspect2") != expectedName {
		t.Errorf("ExpandName mismatch")
	}

	// Test CalculateHash consistency
	hash1 := CalculateHash(id, "testapp", "aspect1", "aspect2")
	if !bytes.Equal(dest.Hash, hash1) {
		t.Errorf("CalculateHash mismatch")
	}

	// Test Announce
	if err := dest.Announce(nil); err != nil {
		t.Errorf("Announce failed: %v", err)
	}
}

func TestDestinationEncryption(t *testing.T) {
	id, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "testapp")
	if err != nil {
		t.Fatal(err)
	}

	msg := []byte("secret message")
	encrypted, err := dest.Encrypt(msg)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := dest.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(msg, decrypted) {
		t.Errorf("encryption/decryption failed")
	}
}

func TestRegisterRequestHandlerWithAutoCompressLimit(t *testing.T) {
	id, _ := NewIdentity(true)
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "testapp")
	if err != nil {
		t.Fatalf("NewDestination error: %v", err)
	}

	dest.RegisterRequestHandlerWithAutoCompressLimit(
		"/test/path",
		func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
			return "ok"
		},
		AllowAll,
		nil,
		true,
		1024,
	)

	pathHash := TruncatedHash([]byte("/test/path"))
	handler, ok := dest.requestHandlers[string(pathHash)]
	if !ok {
		t.Fatalf("request handler not registered")
	}
	if !handler.AutoCompress {
		t.Fatalf("expected AutoCompress to be true")
	}
	if handler.AutoCompressLimit != 1024 {
		t.Fatalf("expected AutoCompressLimit 1024, got %v", handler.AutoCompressLimit)
	}
}

func TestRegisterRequestHandlerAutoCompressDefaults(t *testing.T) {
	id, _ := NewIdentity(true)
	dest, err := NewDestination(id, DestinationIn, DestinationSingle, "testapp")
	if err != nil {
		t.Fatalf("NewDestination error: %v", err)
	}

	handlerFn := func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		return "ok"
	}

	dest.RegisterRequestHandler("/auto/true", handlerFn, AllowAll, nil, true)
	dest.RegisterRequestHandler("/auto/false", handlerFn, AllowAll, nil, false)

	truePathHash := TruncatedHash([]byte("/auto/true"))
	falsePathHash := TruncatedHash([]byte("/auto/false"))

	trueHandler, ok := dest.requestHandlers[string(truePathHash)]
	if !ok {
		t.Fatalf("auto true handler not registered")
	}
	if !trueHandler.AutoCompress {
		t.Fatalf("expected auto true handler to enable compression")
	}
	if trueHandler.AutoCompressLimit != ResourceAutoCompressMaxSize {
		t.Fatalf("expected default auto-compress limit %v, got %v", ResourceAutoCompressMaxSize, trueHandler.AutoCompressLimit)
	}

	falseHandler, ok := dest.requestHandlers[string(falsePathHash)]
	if !ok {
		t.Fatalf("auto false handler not registered")
	}
	if falseHandler.AutoCompress {
		t.Fatalf("expected auto false handler to disable compression")
	}
	if falseHandler.AutoCompressLimit != 0 {
		t.Fatalf("expected disabled handler limit 0, got %v", falseHandler.AutoCompressLimit)
	}
}
