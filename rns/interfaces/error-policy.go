// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

var (
	panicOnInterfaceErrorEnabled atomic.Bool

	interfacePanicHookMu sync.Mutex
	interfacePanicHook   = func(string) {
		os.Exit(255)
	}
)

// SetPanicOnInterfaceErrorEnabled controls whether unrecoverable interface
// errors immediately terminate the process, matching Python's RNS.panic().
func SetPanicOnInterfaceErrorEnabled(enabled bool) {
	panicOnInterfaceErrorEnabled.Store(enabled)
}

// PanicOnInterfaceErrorEnabled reports whether unrecoverable interface errors
// currently trigger an immediate hard stop.
func PanicOnInterfaceErrorEnabled() bool {
	return panicOnInterfaceErrorEnabled.Load()
}

func panicOnInterfaceErrorf(format string, args ...any) {
	if !panicOnInterfaceErrorEnabled.Load() {
		return
	}

	msg := fmt.Sprintf(format, args...)

	interfacePanicHookMu.Lock()
	hook := interfacePanicHook
	interfacePanicHookMu.Unlock()

	hook(msg)
}

func setInterfacePanicHookForTest(hook func(string)) func() {
	interfacePanicHookMu.Lock()
	prev := interfacePanicHook
	interfacePanicHook = hook
	interfacePanicHookMu.Unlock()

	return func() {
		interfacePanicHookMu.Lock()
		interfacePanicHook = prev
		interfacePanicHookMu.Unlock()
	}
}
