// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

type remoteRequestFake struct {
	path       string
	data       any
	response   any
	requestErr error
}

func (f *remoteRequestFake) Request(path string, data any, timeout float64) (any, error) {
	f.path = path
	f.data = data
	return f.response, f.requestErr
}

func (f *remoteRequestFake) Close() error { return nil }

func TestDoRemoteTableUsesRemoteRequestPayload(t *testing.T) {
	t.Parallel()

	fake := &remoteRequestFake{response: []any{
		map[string]any{"hash": []byte{0x01}, "timestamp": float64(123), "via": []byte{0x11}, "hops": 1, "expires": float64(456), "interface": "eth0"},
	}}
	var out bytes.Buffer
	if err := doRemoteTable(&out, fake, []byte{0xaa}, 3, false, 12); err != nil {
		t.Fatalf("doRemoteTable returned error: %v", err)
	}
	if got, want := fake.path, "/path"; got != want {
		t.Fatalf("remote path mismatch: got %q want %q", got, want)
	}
	if got, want := fmt.Sprintf("%#v", fake.data), "[]interface {}{\"table\", []uint8{0xaa}, 3}"; got != want {
		t.Fatalf("remote payload mismatch: got %s want %s", got, want)
	}
	wantExpires := time.Unix(456, 0).Format("2006-01-02 15:04:05")
	if got, want := out.String(), "01 is 1 hop  away via 11 on eth0 expires "+wantExpires+"\n"; got != want {
		t.Fatalf("remote table output mismatch: got %q want %q", got, want)
	}
}

func TestDoRemoteRatesUsesRemoteRequestPayload(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fake := &remoteRequestFake{response: []any{
		map[string]any{"hash": []byte{0x01}, "last": float64(now.Add(-2 * time.Minute).Unix()), "rate_violations": 0, "blocked_until": float64(0), "timestamps": []any{float64(now.Add(-3 * time.Hour).Unix())}},
	}}
	var out bytes.Buffer
	if err := doRemoteRates(&out, fake, []byte{0xbb}, false, 12); err != nil {
		t.Fatalf("doRemoteRates returned error: %v", err)
	}
	if got, want := fake.path, "/path"; got != want {
		t.Fatalf("remote path mismatch: got %q want %q", got, want)
	}
	if got, want := fmt.Sprintf("%#v", fake.data), "[]interface {}{\"rates\", []uint8{0xbb}}"; got != want {
		t.Fatalf("remote payload mismatch: got %s want %s", got, want)
	}
	if got, want := out.String(), "01 last heard 2 minutes ago, 0.333 announces/hour in the last 3 hours\n"; got != want {
		t.Fatalf("remote rates output mismatch: got %q want %q", got, want)
	}
}

func TestDoRemoteBlackholedListUsesRemoteRequestPayload(t *testing.T) {
	t.Parallel()

	fake := &remoteRequestFake{response: map[string]any{
		"01": map[string]any{"until": int64(0), "reason": "Announce spam", "source": []byte{0x09, 0x09, 0x09}},
	}}
	var out bytes.Buffer
	if err := doRemoteBlackholedList(&out, fake, "", []byte{0x09, 0x09, 0x09}, false, 12); err != nil {
		t.Fatalf("doRemoteBlackholedList returned error: %v", err)
	}
	if got, want := fake.path, "/list"; got != want {
		t.Fatalf("remote path mismatch: got %q want %q", got, want)
	}
	if got, want := out.String(), "<01> blackholed indefinitely (Announce spam)\n"; got != want {
		t.Fatalf("remote blackhole list output mismatch: got %q want %q", got, want)
	}
}
