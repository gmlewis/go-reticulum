// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestTTYRestorerMethodsNoOpWithoutHooks(t *testing.T) {
	t.Parallel()

	restorer := &ttyRestorer{}
	if err := restorer.raw(); err != nil {
		t.Fatalf("raw() error: %v", err)
	}
	if err := restorer.restore(); err != nil {
		t.Fatalf("restore() error: %v", err)
	}
}

func TestTTYRestorerMethodsInvokeHooks(t *testing.T) {
	t.Parallel()

	rawCalls := 0
	restoreCalls := 0
	restorer := &ttyRestorer{
		rawFn: func() error {
			rawCalls++
			return nil
		},
		restoreFn: func() error {
			restoreCalls++
			return nil
		},
	}

	if err := restorer.raw(); err != nil {
		t.Fatalf("raw() error: %v", err)
	}
	if err := restorer.restore(); err != nil {
		t.Fatalf("restore() error: %v", err)
	}
	if rawCalls != 1 {
		t.Fatalf("raw hook called %v times, want 1", rawCalls)
	}
	if restoreCalls != 1 {
		t.Fatalf("restore hook called %v times, want 1", restoreCalls)
	}
}
