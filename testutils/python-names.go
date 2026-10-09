// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import "strings"

// pythonToolNames maps this port's own command names to the Python originals'.
// Every Go command names itself in its help text, its examples and its
// configuration prose, because a Go install has none of the Python commands to
// run. Parity comparisons against the Python source therefore normalize the
// names away, and this table is the single place that says what each one maps
// to.
var pythonToolNames = []struct{ goName, pythonName string }{
	{"gornstatus", "rnstatus"},
	{"gornprobe", "rnprobe"},
	{"gornodeconf", "rnodeconf"},
	{"gornpkg", "rnpkg"},
	{"gornpath", "rnpath"},
	{"gornid", "rnid"},
	{"gornsd", "rnsd"},
	{"gornir", "rnir"},
	{"gorncp", "rncp"},
	{"gornsh", "rnsh"},
	{"gorrcd", "rrcd"},
	{"golxmd", "lxmd"},
}

// NormalizePythonToolNames rewrites this port's command names to the Python
// originals' so a Go output can be byte-compared against a Python capture.
// Identifiers that keep the Python spelling on the wire or on disk are left
// alone; only the command names themselves are rewritten.
func NormalizePythonToolNames(text string) string {
	pairs := make([]string, 0, len(pythonToolNames)*2)
	for _, p := range pythonToolNames {
		pairs = append(pairs, p.goName, p.pythonName)
	}
	return strings.NewReplacer(pairs...).Replace(text)
}
