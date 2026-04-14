#!/bin/bash -e
# -*- compile-command: "./test-integration.sh"; -*-

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

# Point to the original repo directories for integration tests:
export ORIGINAL_RETICULUM_REPO_DIR=${HOME}/src/github.com/markqvist/Reticulum
export ORIGINAL_LXMF_REPO_DIR=${HOME}/src/github.com/markqvist/lxmf
export ORIGINAL_RNSH_REPO_DIR=${HOME}/src/github.com/acehoss/rnsh

ERRCHECK_BIN="$(command -v errcheck || true)"
if [[ -z "${ERRCHECK_BIN}" ]]; then
	go install github.com/kisielk/errcheck@latest
	ERRCHECK_BIN="$(go env GOPATH)/bin/errcheck"
fi

GOIMPORTS_BIN="$(command -v goimports || true)"
if [[ -z "${GOIMPORTS_BIN}" ]]; then
	go install golang.org/x/tools/cmd/goimports@latest
	GOIMPORTS_BIN="$(go env GOPATH)/bin/goimports"
fi

STATICCHECK_BIN="$(command -v staticcheck || true)"
if [[ -z "${STATICCHECK_BIN}" ]]; then
	go install honnef.co/go/tools/cmd/staticcheck@latest
	STATICCHECK_BIN="$(go env GOPATH)/bin/staticcheck"
fi

GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-4m}"

if [[ -z "${GO_TEST_TAGS:-}" ]]; then
	if [[ "$(uname -a)" == *"Darwin"* ]]; then
		GO_TEST_TAGS="integration,darwin"
	elif [[ "$(uname -a)" == *"Linux"* ]]; then
		GO_TEST_TAGS="integration,linux"
	else
		GO_TEST_TAGS="integration"
	fi
fi

echo "Using go test tags: ${GO_TEST_TAGS}"

cd "${REPO_ROOT}"

"${GOIMPORTS_BIN}" -w .

# Parse args to check if a directory/package was provided
has_dir=false
for arg in "$@"; do
	if [[ ! "$arg" =~ ^- ]]; then
		has_dir=true
		break
	fi
done

if [[ "$has_dir" == true ]]; then
	go test -race -tags="${GO_TEST_TAGS}" -count=1 -timeout "${GO_TEST_TIMEOUT}" "$@"
else
	go test -race -tags="${GO_TEST_TAGS}" -count=1 -timeout "${GO_TEST_TIMEOUT}" "$@" ./...
fi

go vet ./...
"${ERRCHECK_BIN}" ./...
"${STATICCHECK_BIN}" -checks=SA* ./...

echo "Done."
