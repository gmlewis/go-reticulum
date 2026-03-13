#!/bin/bash -e
# -*- compile-command: "./test-integration.sh"; -*-

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

# Point to the original repo directories for integration tests:
export ORIGINAL_RETICULUM_REPO_DIR=${HOME}/src/github.com/markqvist/Reticulum
export ORIGINAL_LXMF_REPO_DIR=${HOME}/src/github.com/markqvist/lxmf

ERRCHECK_BIN="$(command -v errcheck || true)"
if [[ -z "${ERRCHECK_BIN}" ]]; then
	go install github.com/kisielk/errcheck@latest
	ERRCHECK_BIN="$(go env GOPATH)/bin/errcheck"
fi

STATICCHECK_BIN="$(command -v staticcheck || true)"
if [[ -z "${STATICCHECK_BIN}" ]]; then
	go install honnef.co/go/tools/cmd/staticcheck@latest
	STATICCHECK_BIN="$(go env GOPATH)/bin/staticcheck"
fi

GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-4m}"

if [[ -z "${GO_TEST_TAGS:-}" ]]; then
	if [[ "$(uname -s)" == "Linux" ]]; then
		GO_TEST_TAGS="integration,linux"
	else
		GO_TEST_TAGS="integration"
	fi
fi

echo "Using go test tags: ${GO_TEST_TAGS}"

cd "${REPO_ROOT}"

go fmt ./...
go test -race -tags="${GO_TEST_TAGS}" -count=1 -timeout "${GO_TEST_TIMEOUT}" "$@" ./...
go vet ./...
"${ERRCHECK_BIN}" ./...
"${STATICCHECK_BIN}" -checks=SA* ./...

echo "Done."
