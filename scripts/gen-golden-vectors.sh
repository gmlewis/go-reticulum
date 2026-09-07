#!/usr/bin/env bash
# Copyright 2026 Glenn Lewis. All rights reserved.
# Use of this source code is governed by the Reticulum License
# that can be found in the LICENSE file.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

OUT_FILE="${1:-}"

if [[ -z "${OUT_FILE}" ]]; then
  # Default to sibling asic-reticulum repo if present
  DEFAULT_ASIC_PATH="${REPO_DIR}/../asic-reticulum/hw/sim/reticulum/parity/GoldenVectors.scala"
  if [[ -d "${REPO_DIR}/../asic-reticulum" ]]; then
    OUT_FILE="${DEFAULT_ASIC_PATH}"
  fi
fi

if [[ -n "${OUT_FILE}" ]]; then
  echo "==> Generating Reticulum ASIC golden vectors into ${OUT_FILE}..."
  go run -C "${REPO_DIR}" ./cmd/gen-golden-vectors -output "${OUT_FILE}"
else
  echo "==> No output file specified and ../asic-reticulum not found. Printing to stdout:"
  go run -C "${REPO_DIR}" ./cmd/gen-golden-vectors
fi
