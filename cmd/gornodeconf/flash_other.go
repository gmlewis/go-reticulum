// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package main

import (
	"fmt"
	"io"
	"runtime"
)

func runFirmwareFlash(out io.Writer, port string, opts options) error {
	_ = out
	_ = port
	_ = opts
	return fmt.Errorf("firmware flash not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runFirmwareFlash(out io.Writer, port string, opts options) error {
	_ = rt
	return runFirmwareFlash(out, port, opts)
}
