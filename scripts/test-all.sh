#!/bin/bash -e
# -*- compile-command: "./test-all.sh"; -*-

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

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

GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-2m}"

cd "${REPO_ROOT}"

go fmt ./...
go test -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" ./...
go vet ./...
"${ERRCHECK_BIN}" ./...
"${STATICCHECK_BIN}" -checks=SA* ./...

echo "Done."
