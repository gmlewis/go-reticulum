// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package utils provides small reusable helpers used across this repository.
// The package collects shared routines that reduce duplication and can also
// be reused by external Go Reticulum tools when appropriate.
package utils

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// ErrHelp reports that command-line help was requested.
var ErrHelp = errors.New("help requested")

// NewFlagSet constructs a flag set that suppresses default flag package
// output and invokes the supplied usage function when help is requested.
func NewFlagSet(name string, usage func()) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = usage
	return fs
}

// PrintVersion writes a standard "name version" line to the given writer.
func PrintVersion(w io.Writer, appName, version string) {
	_, _ = fmt.Fprintf(w, "%v %v\n", appName, version)
}

// WriteText writes the provided text to the given writer.
func WriteText(w io.Writer, text string) {
	_, _ = io.WriteString(w, text)
}
