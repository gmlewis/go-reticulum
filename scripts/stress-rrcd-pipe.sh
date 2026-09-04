#!/bin/bash -e
# -*- compile-command: "./stress-rrcd-pipe.sh"; -*-

# Stress harness: starts N CPU busy-loops, then runs one package's tests under
# that load, reproducing load-flakiness (e.g. the rrcd pipe test's
# double-reader failure that only showed up in the 8-package full run) on
# demand. Delegates to scripts/test-integration.sh, so tags, -p, -parallel,
# env vars, gofmt and vet all come along.
#
#   scripts/stress-rrcd-pipe.sh                                  # rrcd pipe tests, 8 spinners
#   STRESS_SPINNERS=16 scripts/stress-rrcd-pipe.sh               # heavier load
#   STRESS_SPINNERS=0 scripts/stress-rrcd-pipe.sh                # spinners off, plain run
#   STRESS_PACKAGE=./lxmf STRESS_RUN='TestRouterJobLoop' scripts/stress-rrcd-pipe.sh
#   GO_TEST_PARALLEL=4 scripts/stress-rrcd-pipe.sh               # also stress intra-package parallelism
#
# Env knobs: STRESS_SPINNERS (default 8), STRESS_PACKAGE (default ./rrcd),
# STRESS_RUN (default 'TestIntegration.*OverPipe'). Extra args are forwarded
# to scripts/test-integration.sh (e.g. -short, -count=2).

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
REPO_ROOT="${SCRIPT_DIR}/.."

export ORIGINAL_RETICULUM_REPO_DIR="${ORIGINAL_RETICULUM_REPO_DIR:-${HOME}/src/github.com/markqvist/Reticulum}"
export ORIGINAL_LXMF_REPO_DIR="${ORIGINAL_LXMF_REPO_DIR:-${HOME}/src/github.com/markqvist/lxmf}"
export ORIGINAL_RNSH_REPO_DIR="${ORIGINAL_RNSH_REPO_DIR:-${HOME}/src/github.com/acehoss/rnsh}"

STRESS_SPINNERS="${STRESS_SPINNERS:-8}"
STRESS_PACKAGE="${STRESS_PACKAGE:-./rrcd}"
STRESS_RUN="${STRESS_RUN:-TestIntegration.*OverPipe}"

pids=()
cleanup() {
	local p
	for p in "${pids[@]:-}"; do
		[[ -n "$p" ]] && kill "$p" 2>/dev/null || true
	done
	wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

if [[ "${STRESS_SPINNERS}" -gt 0 ]]; then
	echo "Starting ${STRESS_SPINNERS} CPU spinner(s)..."
	for _ in $(seq 1 "${STRESS_SPINNERS}"); do
		bash -c 'while :; do :; done' &
		pids+=("$!")
	done
fi

cd "${REPO_ROOT}"
scripts/test-integration.sh -run "${STRESS_RUN}" "${STRESS_PACKAGE}" "$@"