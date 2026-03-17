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

func TestStackIsolation(t *testing.T) {
	t.Parallel()

	// Stack A
	cfgA := tempDir(t)
	tsA := NewTransportSystem()
	r1, err := NewReticulum(tsA, cfgA)
	if err != nil {
		t.Fatalf("failed to create r1: %v", err)
	}
	defer closeReticulum(t, r1)

	// Stack B
	cfgB := tempDir(t)
	tsB := NewTransportSystem()
	r2, err := NewReticulum(tsB, cfgB)
	if err != nil {
		t.Fatalf("failed to create r2: %v", err)
	}
	defer closeReticulum(t, r2)

	// Create pipe between tsA and tsB
	pipeA, pipeB := newTestPipes(t, tsA, tsB)
	tsA.RegisterInterface(pipeA)
	tsB.RegisterInterface(pipeB)

	// Identity and Destination on Stack B
	idB := mustTestNewIdentity(t, true)
	destB, err := NewDestination(tsB, idB, DestinationIn, DestinationSingle, "stackB")
	if err != nil {
		t.Fatalf("failed to create destB: %v", err)
	}

	requestReceived := make(chan []byte, 1)
	destB.RegisterRequestHandler("/test", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		requestReceived <- data
		return []byte("pong")
	}, AllowAll, nil, true)

	// Link from Stack A to Stack B
	linkA, err := NewLink(tsA, destB)
	if err != nil {
		t.Fatalf("failed to create linkA: %v", err)
	}

	established := make(chan bool, 1)
	linkA.callbacks.LinkEstablished = func(l *Link) {
		established <- true
	}

	if err := linkA.Establish(); err != nil {
		t.Fatalf("failed to establish link: %v", err)
	}

	select {
	case <-established:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for link establishment")
	}

	// Send request
	responseReceived := make(chan []byte, 1)
	_, err = linkA.Request("/test", []byte("ping"), func(rr *RequestReceipt) {
		responseReceived <- rr.Response.([]byte)
	}, nil, nil, 0)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	select {
	case req := <-requestReceived:
		if !bytes.Equal(req, []byte("ping")) {
			t.Errorf("expected ping, got %s", string(req))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for request at receiver")
	}

	select {
	case res := <-responseReceived:
		if !bytes.Equal(res, []byte("pong")) {
			t.Errorf("expected pong, got %s", string(res))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for response at initiator")
	}
}
