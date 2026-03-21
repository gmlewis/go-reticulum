#!/bin/bash -e
# -*- compile-command: "./lint.sh"; -*-

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

GOLANGCI_LINT_BIN="$(command -v golangci-lint || true)"
if [[ -z "${GOLANGCI_LINT_BIN}" ]]; then
    echo "golangci-lint not found. Installing latest version to $(go env GOPATH)/bin..."
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
    GOLANGCI_LINT_BIN="$(go env GOPATH)/bin/golangci-lint"
fi

cd "${REPO_ROOT}"

echo "Running golangci-lint..."
"${GOLANGCI_LINT_BIN}" run ./...

echo "Done."
