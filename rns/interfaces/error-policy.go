// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"log"
)

// SetPanicOnInterfaceErrorEnabled controls whether unrecoverable errors on this
// interface immediately terminate the process, matching Python's RNS.panic().
func (bi *BaseInterface) SetPanicOnInterfaceErrorEnabled(enabled bool) {
	if bi == nil {
		return
	}
	bi.panicOnError.Store(enabled)
}

// PanicOnInterfaceErrorEnabled reports whether unrecoverable errors on this
// interface currently trigger an immediate hard stop.
func (bi *BaseInterface) PanicOnInterfaceErrorEnabled() bool {
	return bi != nil && bi.panicOnError.Load()
}

func (bi *BaseInterface) panicOnInterfaceErrorf(format string, args ...any) {
	if bi == nil || !bi.PanicOnInterfaceErrorEnabled() {
		return
	}

	msg := fmt.Sprintf(format, args...)

	bi.errorPolicyMu.RLock()
	hook := bi.interfacePanicHook
	bi.errorPolicyMu.RUnlock()

	if hook == nil {
		log.Fatalf("%v", msg)
	}
	hook(msg)
}

func (bi *BaseInterface) setInterfacePanicHookForTest(hook func(string)) func() {
	if bi == nil {
		return func() {}
	}

	bi.errorPolicyMu.Lock()
	prev := bi.interfacePanicHook
	bi.interfacePanicHook = hook
	bi.errorPolicyMu.Unlock()

	return func() {
		bi.errorPolicyMu.Lock()
		bi.interfacePanicHook = prev
		bi.errorPolicyMu.Unlock()
	}
}

func (bi *BaseInterface) copyPanicOnInterfaceErrorFrom(src *BaseInterface) {
	if bi == nil || src == nil {
		return
	}

	bi.SetPanicOnInterfaceErrorEnabled(src.PanicOnInterfaceErrorEnabled())

	src.errorPolicyMu.RLock()
	hook := src.interfacePanicHook
	src.errorPolicyMu.RUnlock()

	bi.errorPolicyMu.Lock()
	bi.interfacePanicHook = hook
	bi.errorPolicyMu.Unlock()
}
