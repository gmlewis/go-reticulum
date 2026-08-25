#!/bin/bash -e
# -*- compile-command: "./test-all.sh"; -*-

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

GO_TEST_TIMEOUT="${GO_TEST_TIMEOUT:-2m}"

cd "${REPO_ROOT}"

gofmt -s -w .

go test -race -count=1 --timeout "${GO_TEST_TIMEOUT}" "$@" ./...
go vet ./...

echo "Done."
