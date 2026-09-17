#!/bin/bash -e
# Convenience script to check or update offline public datasets used in go-reticulum.
#
# Usage:
#   ./scripts/update-offline-data.sh        # Update all offline data tables
#   ./scripts/update-offline-data.sh -n     # Dry-run: check upstream without modifying files
#   ./scripts/update-offline-data.sh -v     # Verbose progress output
#
# This script ONLY makes local file updates and makes NO git calls or state changes.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${SCRIPT_DIR}/.."

cd "${REPO_ROOT}"

go run ./cmd/update-offline-data "$@"
