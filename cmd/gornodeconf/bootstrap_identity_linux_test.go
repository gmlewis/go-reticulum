// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "testing"

func TestResolveBootstrapIdentityUsesPythonAliases(t *testing.T) {
	t.Parallel()

	identity, err := resolveBootstrapIdentity(options{product: "f0", model: "a4", hwrev: 5})
	if err != nil {
		t.Fatalf("resolveBootstrapIdentity returned error: %v", err)
	}
	if identity.product != 0xf0 || identity.model != 0xa4 || identity.hwRev != 0x05 {
		t.Fatalf("identity mismatch: %#v", identity)
	}
}

func TestResolveBootstrapIdentityParsesHexFallbacks(t *testing.T) {
	t.Parallel()

	identity, err := resolveBootstrapIdentity(options{product: "eb", model: "e9", hwrev: 255})
	if err != nil {
		t.Fatalf("resolveBootstrapIdentity returned error: %v", err)
	}
	if identity.product != 0xeb || identity.model != 0xe9 || identity.hwRev != 0xff {
		t.Fatalf("identity mismatch: %#v", identity)
	}
}

func TestResolveBootstrapIdentityRequiresModelAndHwRev(t *testing.T) {
	t.Parallel()

	if _, err := resolveBootstrapIdentity(options{product: "03"}); err == nil {
		t.Fatal("expected missing model error")
	}
	if _, err := resolveBootstrapIdentity(options{product: "03", model: "a4"}); err == nil {
		t.Fatal("expected missing hwrev error")
	}
}
