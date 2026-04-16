// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

import (
	"fmt"
	"io"
	"runtime"
)

func runFirmwareExtract(out io.Writer, port string, opts options) error {
	_ = out
	_ = port
	_ = opts
	return fmt.Errorf("firmware extraction not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runFirmwareExtract(out io.Writer, port string, opts options) error {
	_ = rt
	return runFirmwareExtract(out, port, opts)
}
