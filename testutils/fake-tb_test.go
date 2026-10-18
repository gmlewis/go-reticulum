// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"fmt"
	"testing"
)

// fakeTB is a minimal testing.TB used to observe helper behavior without
// failing the surrounding test. Embedding the interface supplies every TB
// method; the recording ones are overridden so a helper's failures and warnings
// can be inspected (calling anything else on a fakeTB nil-panics, which is a
// loud misuse).
type fakeTB struct {
	testing.TB
	errors []string
	logs   []string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.Errorf(format, args...)
}

func (f *fakeTB) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}
