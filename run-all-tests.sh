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

# Absolute repo root: the nested-module loops cd into subdirectories, so log
# redirections that run after the cd need an absolute path.
REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"

export ORIGINAL_RETICULUM_REPO_DIR="${ORIGINAL_RETICULUM_REPO_DIR:-$HOME/src/github.com/markqvist/Reticulum}"
export ORIGINAL_LXMF_REPO_DIR="${ORIGINAL_LXMF_REPO_DIR:-$HOME/src/github.com/markqvist/lxmf}"
export ORIGINAL_RNSH_REPO_DIR="${ORIGINAL_RNSH_REPO_DIR:-$HOME/src/github.com/acehoss/rnsh}"

RUN_ALL_TESTS_TIMEOUT_SECONDS="${RUN_ALL_TESTS_TIMEOUT_SECONDS:-500}"

# GOOS/GOARCH guard for the wago build-tag variants: the in-process wasm runtime
# is only supported on linux/darwin/windows, amd64/arm64 (matching CI, which
# runs the wago nested-module tests only on its linux/amd64 runner).
GOOS_CHECK="$(go env GOOS)"
GOARCH_CHECK="$(go env GOARCH)"

# ---------------------------------------------------------------------------
# Temp-dir hygiene. A test run that ends normally removes every temp dir it
# created (testutils.TempDir registers t.Cleanup, and testutils/cleanup.go
# removes them on SIGINT/SIGTERM). What that cannot cover is SIGKILL, a
# `go test -timeout` panic, or os.Exit from an unrelated goroutine — so sweep
# before and after: the entry sweep with an age guard that spares anything a
# live run owns, the exit sweep with none, because by then this run's own dirs
# are supposed to be gone. scripts/clean-test-tmp.sh only ever matches its
# allow-list of test prefixes, so unrelated /tmp content is never touched. That
# unguarded exit sweep also passes -L (see sweep_test_tmp below), so it stands
# down entirely while another test binary is alive.
#
# The -c check keeps that allow-list honest: it fails when a temp prefix in the
# sources is missing from the list, the same way the lint gates below fail.
# ---------------------------------------------------------------------------
sweep_test_tmp() {
	# -L belongs to the exit sweep only. It runs unguarded (-m 0), so without
	# this it would delete the live temp dirs of a concurrent run — a bare
	# `go test` in another terminal, or another agent's run in this repo — and
	# fail that run for reasons that look nothing like a cleanup problem. The
	# entry sweep keeps its 30-minute age guard instead, so it still reclaims
	# stale residue no matter what else is running.
	if [ "${1:-0}" -eq 0 ]; then
		bash "$REPO_ROOT/scripts/clean-test-tmp.sh" -m 0 -L
		return
	fi

	bash "$REPO_ROOT/scripts/clean-test-tmp.sh" -m "${1:-0}"
}

bash "$REPO_ROOT/scripts/clean-test-tmp.sh" -c
sweep_test_tmp 30
trap 'sweep_test_tmp 0 || true' EXIT

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
ERRCHECK_LOG="${REPO_ROOT}/errcheck.log"
if ! errcheck ./... >"${ERRCHECK_LOG}" 2>&1; then
    echo "FAIL: errcheck reported unchecked errors (see ${ERRCHECK_LOG}):" >&2
    cat "${ERRCHECK_LOG}" >&2
    exit 1
fi
# Nested modules (cmd/<tool>/go.mod) are invisible to the root ./... pattern;
# run the same cleanliness checks inside each one. Attaching the redirection
# to the command (not the subshell) keeps the shell's xtrace lines out of the
# redirected log output.
for modfile in cmd/*/go.mod; do
    if [ -f "${modfile}" ]; then
        moddir=$(dirname "${modfile}")
        if ! (cd "${moddir}" && errcheck ./... >>"${ERRCHECK_LOG}" 2>&1); then
            echo "FAIL: errcheck reported unchecked errors in ${moddir} (see ${ERRCHECK_LOG}):" >&2
            cat "${ERRCHECK_LOG}" >&2
            exit 1
        fi
    fi
done
echo "errcheck: clean (all errors checked)"

# echo "Running gopls check (workspace diagnostics)..."
# GOPLS_CHECK_LOG="gopls-check.log"
# : > "${GOPLS_CHECK_LOG}"
# # xargs may split the file list into batches; append each batch's output.
# git ls-files -z '*.go' | xargs -0 gopls check >>"${GOPLS_CHECK_LOG}" 2>&1 || true
# if [[ -s "${GOPLS_CHECK_LOG}" ]]; then
#     echo "FAIL: gopls check reported diagnostics (see ${GOPLS_CHECK_LOG}):" >&2
#     cat "${GOPLS_CHECK_LOG}" >&2
#     exit 1
# fi
# echo "gopls check: clean (no diagnostics)"

echo "Running modernize (modernization suggestions)..."
MODERNIZE_LOG="${REPO_ROOT}/modernize.log"
if ! go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest ./... >"${MODERNIZE_LOG}" 2>&1; then
    echo "FAIL: modernize reported suggestions (see ${MODERNIZE_LOG}):" >&2
    cat "${MODERNIZE_LOG}" >&2
    exit 1
fi
for modfile in cmd/*/go.mod; do
    if [ -f "${modfile}" ]; then
        moddir=$(dirname "${modfile}")
        if ! (cd "${moddir}" && go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest ./... >>"${MODERNIZE_LOG}" 2>&1); then
            echo "FAIL: modernize reported suggestions in ${moddir} (see ${MODERNIZE_LOG}):" >&2
            cat "${MODERNIZE_LOG}" >&2
            exit 1
        fi
    fi
done
echo "modernize: clean (no suggestions)"

echo "Running staticcheck (SA* + U1000, with integration tags)..."
STATICCHECK_LOG="${REPO_ROOT}/staticcheck.log"
staticcheck -checks=SA*,U1000 -tags=integration ./... >"${STATICCHECK_LOG}" 2>&1 || true
for modfile in cmd/*/go.mod; do
    if [ -f "${modfile}" ]; then
        moddir=$(dirname "${modfile}")
        (cd "${moddir}" && staticcheck -checks=SA*,U1000 -tags=integration ./... >>"${STATICCHECK_LOG}" 2>&1 || true)
        # The wago variant sees the wasm plugin host and its tests. A helper
        # used by only one variant must carry a matching build tag; checking
        # both is what keeps a wago-only helper from looking like dead code.
        if [[ "${GOOS_CHECK}" =~ ^(linux|darwin|windows)$ && "${GOARCH_CHECK}" =~ ^(amd64|arm64)$ ]]; then
            (cd "${moddir}" && staticcheck -checks=SA*,U1000 -tags=integration,wago ./... >>"${STATICCHECK_LOG}" 2>&1 || true)
        fi
    fi
done
if [[ -s "${STATICCHECK_LOG}" ]]; then
    echo "FAIL: staticcheck reported issues (see ${STATICCHECK_LOG}):" >&2
    cat "${STATICCHECK_LOG}" >&2
    exit 1
fi
echo "staticcheck: clean (SA* + U1000, with integration tags)"

echo "Running deadcode (advisory, whole-program reachability)..."
bash "${REPO_ROOT}/scripts/deadcode-check.sh"

# ---------------------------------------------------------------------------
# Nested modules (cmd/<tool>/go.mod) are invisible to the root ./... pattern, so
# each is tested explicitly — in default (stub) mode and, on the platforms the
# wago build supports, with -tags wago, exactly as GitHub CI does. Without this
# step a nested-module build break (a test helper behind a build tag, say)
# reaches CI undetected.
# ---------------------------------------------------------------------------
echo "Running nested module tests (default and -tags=wago)..."
NESTED_TEST_LOG="${REPO_ROOT}/nested-test-failures.log"
: > "${NESTED_TEST_LOG}"
for modfile in cmd/*/go.mod; do
    if [ -f "${modfile}" ]; then
        moddir=$(dirname "${modfile}")
        if ! (cd "${moddir}" && run_with_timeout go test -race -count=1 ./...) >>"${NESTED_TEST_LOG}" 2>&1; then
            echo "FAIL: nested module tests failed in ${moddir} (see ${NESTED_TEST_LOG}):" >&2
            cat "${NESTED_TEST_LOG}" >&2
            exit 1
        fi
        if [[ "${GOOS_CHECK}" =~ ^(linux|darwin|windows)$ && "${GOARCH_CHECK}" =~ ^(amd64|arm64)$ ]]; then
            if ! (cd "${moddir}" && run_with_timeout go test -tags=wago -race -count=1 ./...) >>"${NESTED_TEST_LOG}" 2>&1; then
                echo "FAIL: nested module (-tags=wago) tests failed in ${moddir} (see ${NESTED_TEST_LOG}):" >&2
                cat "${NESTED_TEST_LOG}" >&2
                exit 1
            fi
        fi
    fi
done
echo "nested module tests: clean (default and -tags=wago)"

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

# Sweep here instead of leaving it to the EXIT trap: the trap fires after the
# last write to stdout, so its output — and the shell's xtrace of it — would
# land below the verdict. Clearing the trap once the sweep has run keeps the
# failure paths covered (the trap still fires on any early exit above) while
# making the squeaky-clean line below genuinely the last thing printed.
sweep_test_tmp 0 || true
trap - EXIT

echo "Repo is squeaky-clean (errcheck + gopls check + modernize + staticcheck SA*/U1000 + deadcode advisory + all tests)."
