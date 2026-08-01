// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package cmd holds the command-line tools built on top of the go-reticulum
// RNS/LXMF stack. Each subdirectory is a standalone main package implementing
// one tool (for example golxmd, gorncp, gornid, gornnodeconf, gornprobe,
// gornstatus). The cmd package itself contains no runtime code; it exists as
// the shared parent for the CLI-contract test that enforces a consistent
// usage/flags layout across those tools.
package cmd
