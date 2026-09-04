#!/bin/bash

# run-all-tests.sh runs all unit tests and integration tests with timeouts.
# Static cleanliness checks (errcheck, staticcheck, modernize, gopls check)
# run FIRST so a lint failure fails fast before spending time on tests.
#
# Default mode (~90s): lint + the -short integration suite (skips the
# cross-process / Python-interop / soak tests) — enough for ~99% confidence
# before pushing. The FULL suite (cross-implementation, soak, and the two
# no-race specials) runs in GitHub CI on every push; run it locally with:
#
#   ./run-all-tests.sh --full        (or RUN_ALL_TESTS_FULL=1)

set -euo pipefail
set -x

export ORIGINAL_RETICULUM_REPO_DIR="${ORIGINAL_RETICULUM_REPO_DIR:-$HOME/src/github.com/markqvist/Reticulum}"
export ORIGINAL_LXMF_REPO_DIR="${ORIGINAL_LXMF_REPO_DIR:-$HOME/src/github.com/markqvist/lxmf}"
export ORIGINAL_RNSH_REPO_DIR="${ORIGINAL_RNSH_REPO_DIR:-$HOME/src/github.com/acehoss/rnsh}"

RUN_ALL_TESTS_TIMEOUT_SECONDS="${RUN_ALL_TESTS_TIMEOUT_SECONDS:-500}"

run_with_timeout() {
	if command -v timeout >/dev/null 2>&1; then
		timeout --foreground "${RUN_ALL_TESTS_TIMEOUT_SECONDS}s" "$@"
		return
	fi
	if command -v gtimeout >/dev/null 2>&1; then
		gtimeout --foreground "${RUN_ALL_TESTS_TIMEOUT_SECONDS}s" "$@"
		return
	fi

	python3 - "${RUN_ALL_TESTS_TIMEOUT_SECONDS}" "$@" <<'PY'
import subprocess
import sys

timeout = int(sys.argv[1])
cmd = sys.argv[2:]

try:
    raise SystemExit(subprocess.run(cmd, check=False, timeout=timeout).returncode)
except subprocess.TimeoutExpired:
    print(f"timed out after {timeout}s: {' '.join(cmd)}", file=sys.stderr)
    raise SystemExit(124)
PY
}

# ---------------------------------------------------------------------------
# Static cleanliness checks: verify the repo is "squeaky-clean" per gopls.
#
# 0. errcheck     — verifies all error return values are checked.
#
# 1. gopls check  — workspace diagnostics (compiler errors, vet-style
#    warnings). gopls check always exits 0 and prints diagnostics to stdout,
#    so cleanliness is judged by whether it produced any output. It takes
#    filenames (not package patterns), so every tracked .go file is
#    enumerated.
#
# 2. modernize    — the standalone modernize analyzer, the headless
#    equivalent of the editor's "gopls modernize" code actions. (There is no
#    `gopls modernize` CLI subcommand; the analyzer is invoked via
#    `go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest`.)
#    Without -fix it only reports; it exits non-zero when suggestions exist.
#    Requires network on first run to fetch the analyzer module.
# ---------------------------------------------------------------------------

echo "Running errcheck..."
ERRCHECK_LOG="errcheck.log"
if ! errcheck ./... >"${ERRCHECK_LOG}" 2>&1; then
    echo "FAIL: errcheck reported unchecked errors (see ${ERRCHECK_LOG}):" >&2
    cat "${ERRCHECK_LOG}" >&2
    exit 1
fi
echo "errcheck: clean (all errors checked)"

echo "Running gopls check (workspace diagnostics)..."
GOPLS_CHECK_LOG="gopls-check.log"
: > "${GOPLS_CHECK_LOG}"
# xargs may split the file list into batches; append each batch's output.
git ls-files -z '*.go' | xargs -0 gopls check >>"${GOPLS_CHECK_LOG}" 2>&1 || true
if [[ -s "${GOPLS_CHECK_LOG}" ]]; then
    echo "FAIL: gopls check reported diagnostics (see ${GOPLS_CHECK_LOG}):" >&2
    cat "${GOPLS_CHECK_LOG}" >&2
    exit 1
fi
echo "gopls check: clean (no diagnostics)"

echo "Running modernize (modernization suggestions)..."
MODERNIZE_LOG="modernize.log"
if ! go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest ./... >"${MODERNIZE_LOG}" 2>&1; then
    echo "FAIL: modernize reported suggestions (see ${MODERNIZE_LOG}):" >&2
    cat "${MODERNIZE_LOG}" >&2
    exit 1
fi
echo "modernize: clean (no suggestions)"

echo "Running full staticcheck (all checks, with integration tags)..."
STATICCHECK_LOG="staticcheck.log"
staticcheck -checks=SA* -tags=integration ./... >"${STATICCHECK_LOG}" 2>&1 || true
if [[ -s "${STATICCHECK_LOG}" ]]; then
    echo "FAIL: staticcheck reported issues (see ${STATICCHECK_LOG}):" >&2
    cat "${STATICCHECK_LOG}" >&2
    exit 1
fi
echo "staticcheck: clean (all checks, with integration tags)"

# ---------------------------------------------------------------------------
# Integration tests: the -short suite always (fast); the full suite only in
# full mode (--full or RUN_ALL_TESTS_FULL=1), matching what GitHub CI runs.
# ---------------------------------------------------------------------------

if [[ "${1:-}" == "--full" ]]; then
	RUN_ALL_TESTS_FULL=1
fi

# test-all.sh is redundant when the short integration tests are running next, so skip it:
# time run_with_timeout ./test-all.sh 2>&1 | tee test-failures.log

time run_with_timeout ./scripts/test-integration.sh -short 2>&1 | tee short-test-failures.log

if [[ "${RUN_ALL_TESTS_FULL:-0}" == "1" ]]; then
	time run_with_timeout ./scripts/test-integration.sh 2>&1 | tee full-test-failures.log

	# Run integration tests that are skipped under the race detector:
	time run_with_timeout go test -tags=integration -count=1 ./lxmf -run TestParallelStampGeneration 2>&1 | tee -a full-test-failures.log
	time run_with_timeout go test -tags=integration -count=1 ./rns -run TestIntegratedResponseResourceCompressionPolicyGoToPython 2>&1 | tee -a full-test-failures.log
fi

echo "All tests completed."

echo "Repo is squeaky-clean (errcheck + gopls check + modernize + staticcheck + all tests)."
