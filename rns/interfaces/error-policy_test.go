// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "testing"

func TestBaseInterfaceErrorPolicyIsolation(t *testing.T) {
	alpha := NewBaseInterface("alpha", ModeFull, 1000)
	beta := NewBaseInterface("beta", ModeFull, 1000)

	var alphaGot string
	restoreAlpha := alpha.setInterfacePanicHookForTest(func(msg string) {
		alphaGot = msg
	})
	defer restoreAlpha()

	var betaGot string
	restoreBeta := beta.setInterfacePanicHookForTest(func(msg string) {
		betaGot = msg
	})
	defer restoreBeta()

	alpha.SetPanicOnInterfaceErrorEnabled(true)
	beta.SetPanicOnInterfaceErrorEnabled(false)

	alpha.panicOnInterfaceErrorf("interface %v failed", alpha.Name())
	beta.panicOnInterfaceErrorf("interface %v failed", beta.Name())

	if alphaGot == "" {
		t.Fatal("enabled interface did not trigger hard stop hook")
	}
	if betaGot != "" {
		t.Fatalf("disabled interface unexpectedly triggered hard stop with %q", betaGot)
	}
}

func TestBaseInterfaceErrorPolicyCopy(t *testing.T) {
	parent := NewBaseInterface("parent", ModeFull, 1000)
	child := NewBaseInterface("child", ModeFull, 1000)

	var got string
	restoreHook := parent.setInterfacePanicHookForTest(func(msg string) {
		got = msg
	})
	defer restoreHook()

	parent.SetPanicOnInterfaceErrorEnabled(true)
	child.copyPanicOnInterfaceErrorFrom(parent)

	if !child.PanicOnInterfaceErrorEnabled() {
		t.Fatal("expected copied interface policy to be enabled")
	}

	child.panicOnInterfaceErrorf("interface %v failed", child.Name())
	if got == "" {
		t.Fatal("copied interface policy did not trigger inherited hook")
	}
}
