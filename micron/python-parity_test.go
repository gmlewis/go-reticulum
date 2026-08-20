// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package micron

import (
	"os/exec"
	"sync"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// These tests cross-check the Go micron port against the live reference
// Python implementation in RNS.Utilities.rngit (util.convert_markdown_to_micron
// and highlight.highlight_code), captured fresh from the installed RNS at test
// time. They replace the prior committed "DO NOT EDIT" golden snapshots with a
// real Go↔Python diff, gated on testutils.SkipIfNoPythonRNS (and Pygments for
// the coloured-reference structural check) so they skip cleanly in a
// Python-less environment and fail hard when Go diverges from Python.

// pythonCache memoizes live Python captures across tests so the convert and
// format-block tests (which share the goldenConvert inputs) don't each spawn
// a python3 process for the same input.
var pythonCache sync.Map

type pythonCacheKey struct{ kind, a, b, c string }

// pythonConvert captures Python's convert_markdown_to_micron(input) live from
// the installed RNS.Utilities.rngit. The converter uses its default settings
// (no syntax highlighter, default max width), matching NewConverter().
func pythonConvert(t *testing.T, input string) string {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	key := pythonCacheKey{"convert", input, "", ""}
	if v, ok := pythonCache.Load(key); ok {
		return v.(string)
	}
	script := `
from RNS.Utilities.rngit.util import convert_markdown_to_micron
import sys
sys.stdout.write(convert_markdown_to_micron(sys.argv[1]))
`
	got := testutils.RunPython(t, script, input)
	pythonCache.Store(key, got)
	return got
}

// pythonHighlightPlain captures Python's highlight_code with Pygments forced
// OFF, matching the Go port (which has no Pygments and therefore always takes
// the plain-text fallback path: _plain_text(content).replace("\\","\\\")).
// This is the byte-exact parity path for the no-language/no-filename cases.
func pythonHighlightPlain(t *testing.T, content, filename, language string) string {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	key := pythonCacheKey{"highlight_plain", content, filename, language}
	if v, ok := pythonCache.Load(key); ok {
		return v.(string)
	}
	script := `
from RNS.Utilities.rngit import highlight as _h
def _no_pygments(self):
    self.pygments_available = False
    self.pygments = None
_h.SyntaxHighlighter._check_pygments = _no_pygments
from RNS.Utilities.rngit.highlight import highlight_code
import sys
sys.stdout.write(highlight_code(sys.argv[1], sys.argv[2] or None, sys.argv[3] or None))
`
	got := testutils.RunPython(t, script, content, filename, language)
	pythonCache.Store(key, got)
	return got
}

// pythonHighlightColored captures Python's highlight_code with Pygments ON,
// producing the reference coloured output the Go hand-tokenisers are checked
// against (structurally — every reference colour must appear in Go output).
func pythonHighlightColored(t *testing.T, content, filename, language string) string {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	skipIfNoPygments(t)
	key := pythonCacheKey{"highlight_colored", content, filename, language}
	if v, ok := pythonCache.Load(key); ok {
		return v.(string)
	}
	script := `
from RNS.Utilities.rngit.highlight import highlight_code
import sys
sys.stdout.write(highlight_code(sys.argv[1], sys.argv[2] or None, sys.argv[3] or None))
`
	got := testutils.RunPython(t, script, content, filename, language)
	pythonCache.Store(key, got)
	return got
}

// pythonPygmentsAvailable reports whether the Pygments package is importable,
// which the coloured-reference structural check requires (without Pygments,
// highlight_code emits no colours and the colour-set comparison is vacuous).
func pythonPygmentsAvailable() bool {
	return exec.Command("python3", "-c", "import pygments").Run() == nil
}

func skipIfNoPygments(t *testing.T) {
	t.Helper()
	if !pythonPygmentsAvailable() {
		t.Skip("pygments not available; skipping coloured-reference cross-check")
	}
}
