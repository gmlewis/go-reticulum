// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func TestNewRuntime(t *testing.T) {
	t.Parallel()

	rt := newRuntime(nil)
	if rt == nil {
		t.Fatal("newRuntime() returned nil")
	}
	if rt.app == nil {
		t.Fatal("newRuntime() did not initialize the app state")
	}
	if rt.logger == nil {
		t.Fatal("newRuntime() did not initialize a logger")
	}
}
