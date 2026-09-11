#!/bin/bash -e
# -*- compile-command: "./test-all.sh"; -*-

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-2m}"

cd "${REPO_ROOT}"

gofmt -s -w .

# Root module (stdlib-only) tests.
go test -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" ./...
go vet ./...

# Nested module tests (cmd/<tool>/go.mod): running go test from the repo
# root silently skips nested modules, so each one is entered explicitly,
# both in default (stub) mode and, on supported host platforms, with
# -tags wago linking the in-process wasm runtime.
for modfile in cmd/*/go.mod; do
	if [ -f "${modfile}" ]; then
		moddir=$(dirname "${modfile}")
		echo "Testing nested module: ${moddir} (default)..."
		(cd "${moddir}" && gofmt -s -w . && go test -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" . && go vet ./...)

		# Also test with -tags wago on supported host platforms.
		if [[ "$(go env GOOS)" =~ ^(linux|darwin|windows)$ ]] && [[ "$(go env GOARCH)" =~ ^(amd64|arm64)$ ]]; then
			echo "Testing nested module: ${moddir} (-tags wago)..."
			(cd "${moddir}" && go test -tags=wago -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" .)
		fi
	fi
done

echo "Done."
