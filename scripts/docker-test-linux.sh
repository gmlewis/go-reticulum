#!/usr/bin/env bash
# scripts/docker-test-linux.sh
#
# Run the gornodeconf test suite + a whole-repo build inside a Linux Docker
# container. Docker Desktop runs Linux containers natively on macOS, so this
# exercises the *_linux_test.go suite that CANNOT run on a darwin host: the
# cross-compiler can build them, but only a Linux kernel can execute them.
# This is the gap left by cross-compiling alone (go build GOOS=linux) on macOS.
#
# FreeBSD is NOT covered here: Docker is Linux-only, so a GOOS=freebsd binary
# cannot run in any container. FreeBSD is verified by cross-compile (see
# scripts/publish-github-release-artifacts.sh). For *runtime* FreeBSD
# verification, use a FreeBSD VM (Lima, UTM, or QEMU) — out of scope here.
#
# Usage:
#   scripts/docker-test-linux.sh                       # default command below
#   scripts/docker-test-linux.sh go test ./...          # custom go command
#   scripts/docker-test-linux.sh go vet ./cmd/gornodeconf/
#
# Env:
#   GO_DOCKER_IMAGE  base Go image (default matches go.mod's `go 1.26.0`).
#
set -euo pipefail

IMG=${GO_DOCKER_IMAGE:-golang:1.26.4-alpine}
REPO=$(cd "$(dirname "$0")/.." && pwd)
GOMODCACHE=$(go env GOMODCACHE)

# Default: test the package most affected by cross-platform changes, then
# build the whole module as a Linux gate. Override by passing a go command.
CMD=${*:-"go test ./cmd/gornodeconf/ && go build ./..."}

echo ">> Linux Docker test"
echo ">> image: $IMG"
echo ">> repo:  $REPO"
echo ">> cmd:   $CMD"

# GOMODCACHE is mounted read-only from the host so modules are not re-downloaded
# and the host cache is not mutated. GOCACHE is kept inside the container
# (ephemeral, under /tmp) since the host build cache is for darwin and not
# reusable by a Linux toolchain. GOTOOLCHAIN=local keeps the container on the
# image's toolchain instead of downloading go.mod's.
#
# The container runs as the host user (non-root) so permission-based tests
# behave as on a real Linux dev machine: several tests (e.g. the key-writing
# test in signing_linux_test.go) chmod a dir 0500 and expect a write to FAIL,
# which does not happen when the process is root (root bypasses DAC). Running
# as the host UID:GID also means files written into the mounted repo keep the
# host owner, so the checkout is not left root-owned.
exec docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$REPO":/work \
  -v "$GOMODCACHE":/gomodcache:ro \
  -w /work \
  -e GOMODCACHE=/gomodcache \
  -e GOCACHE=/tmp/go-cache \
  -e GOFLAGS=-mod=readonly \
  -e GOTOOLCHAIN=local \
  -e CGO_ENABLED=0 \
  "$IMG" sh -c "$CMD"