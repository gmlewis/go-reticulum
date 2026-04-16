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

detect_live_rnode_port() {
	local -a search_groups=()
	if [[ -n "${TEST_INTEGRATION_LIVE_RNODE_GLOBS:-}" ]]; then
		IFS=':' read -r -a search_groups <<< "${TEST_INTEGRATION_LIVE_RNODE_GLOBS}"
	else
		case "$(uname -s)" in
		Darwin)
			search_groups=(
				"/dev/cu.*JTAG*"
				"/dev/cu.*qtag*"
				"/dev/cu.usbmodem*"
				"/dev/cu.usbserial*"
				"/dev/tty.*JTAG*"
				"/dev/tty.*qtag*"
				"/dev/tty.usbmodem*"
				"/dev/tty.usbserial*"
			)
			;;
		Linux)
			search_groups=(
				"/dev/serial/by-id/*JTAG*"
				"/dev/serial/by-id/*qtag*"
				"/dev/serial/by-id/*"
				"/dev/ttyACM*"
				"/dev/ttyUSB*"
			)
			;;
		*)
			echo "live RNode auto-detect is not supported on $(uname -s)" >&2
			return 1
			;;
		esac
	fi

	local matches=()
	local group_matches=()
	local pattern
	local match
	shopt -s nullglob
	for pattern in "${search_groups[@]}"; do
		if [[ -z "${pattern}" ]]; then
			continue
		fi
		group_matches=()
		if [[ "${pattern}" == *"*"* || "${pattern}" == *"?"* || "${pattern}" == *"["* ]]; then
			local expanded=( ${pattern} )
			for match in "${expanded[@]}"; do
				group_matches+=("${match}")
			done
		else
			group_matches+=("${pattern}")
		fi

		if [[ "${#group_matches[@]}" -eq 0 ]]; then
			continue
		fi
		matches=()
		for match in "${group_matches[@]}"; do
			if [[ "${pattern}" == "/dev/serial/by-id/*" && "${match}" != *"JTAG"* && "${match}" != *"qtag"* ]]; then
				continue
			fi
			matches+=("${match}")
		done
		if [[ "${#matches[@]}" -gt 0 ]]; then
			break
		fi
	done
	shopt -u nullglob

	local unique_matches=()
	local existing
	local seen
	for match in "${matches[@]}"; do
		seen=false
		for existing in "${unique_matches[@]}"; do
			if [[ "${existing}" == "${match}" ]]; then
				seen=true
				break
			fi
		done
		if [[ "${seen}" == false ]]; then
			unique_matches+=("${match}")
		fi
	done

	case "${#unique_matches[@]}" in
	0)
		echo "live RNode auto-detect found no qtag serial devices" >&2
		return 1
		;;
	1)
		printf '%s\n' "${unique_matches[0]}"
		return 0
		;;
	*)
		echo "live RNode auto-detect found multiple qtag serial devices: ${unique_matches[*]}" >&2
		return 1
		;;
	esac
}

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

if [[ -z "${GO_TEST_P:-}" && "$(uname -a)" == *"Darwin"* ]]; then
	GO_TEST_P=8
fi
if [[ -z "${GO_TEST_PARALLEL:-}" && "$(uname -a)" == *"Darwin"* ]]; then
	GO_TEST_PARALLEL=1
fi

GO_TEST_ARGS=()
if [[ -n "${GO_TEST_P:-}" ]]; then
	GO_TEST_ARGS+=(-p "${GO_TEST_P}")
	echo "Using go test package parallelism: ${GO_TEST_P}"
fi
if [[ -n "${GO_TEST_PARALLEL:-}" ]]; then
	GO_TEST_ARGS+=(-parallel "${GO_TEST_PARALLEL}")
	echo "Using go test intra-package parallelism: ${GO_TEST_PARALLEL}"
fi

cd "${REPO_ROOT}"

"${GOIMPORTS_BIN}" -w .

# Parse args to handle script flags before forwarding to go test.
LIVE_RNODE_ENABLED=false
FILTERED_ARGS=()
has_dir=false
for arg in "$@"; do
	case "${arg}" in
	-live-rnode|-test-rnode)
		LIVE_RNODE_ENABLED=true
		continue
		;;
	esac
	FILTERED_ARGS+=("${arg}")
	if [[ ! "$arg" =~ ^- ]]; then
		has_dir=true
	fi
done

if [[ "${LIVE_RNODE_ENABLED}" == true ]]; then
	if [[ -z "${GORNODECONF_LIVE_SERIAL_PORT:-}" ]]; then
		GORNODECONF_LIVE_SERIAL_PORT="$(detect_live_rnode_port)"
		export GORNODECONF_LIVE_SERIAL_PORT
	fi
	echo "Using live RNode serial port: ${GORNODECONF_LIVE_SERIAL_PORT}"
fi

set -- "${FILTERED_ARGS[@]}"

if [[ "$has_dir" == true ]]; then
	go test "${GO_TEST_ARGS[@]}" -race -tags="${GO_TEST_TAGS}" -count=1 -timeout "${GO_TEST_TIMEOUT}" "$@"
else
	go test "${GO_TEST_ARGS[@]}" -race -tags="${GO_TEST_TAGS}" -count=1 -timeout "${GO_TEST_TIMEOUT}" "$@" ./...
fi

go vet ./...
"${ERRCHECK_BIN}" ./...
"${STATICCHECK_BIN}" -checks=SA* ./...

echo "Done."
