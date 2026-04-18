// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "testing"

func TestInterfaceErrorPolicy(t *testing.T) {
	t.Parallel()

	var got string
	restoreHook := setInterfacePanicHookForTest(func(msg string) {
		got = msg
	})
	defer restoreHook()

	SetPanicOnInterfaceErrorEnabled(false)
	panicOnInterfaceErrorf("interface %v failed", "alpha")
	if got != "" {
		t.Fatalf("disabled policy unexpectedly triggered hard stop with %q", got)
	}

	SetPanicOnInterfaceErrorEnabled(true)
	panicOnInterfaceErrorf("interface %v failed", "beta")
	if got == "" {
		t.Fatal("enabled policy did not trigger hard stop hook")
	}
}
