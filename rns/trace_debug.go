// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"os"
	"sync"
)

var traceDebugMu sync.Mutex

func traceDebugf(format string, args ...any) {
	path := os.Getenv("GORN_TRACE_LOG_PATH")
	if path == "" {
		return
	}
	traceDebugMu.Lock()
	defer traceDebugMu.Unlock()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, format+"\n", args...)
}
