#!/usr/bin/env sh

set -eu

project_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$project_directory"

usage() {
	cat <<'EOF'
Usage: ./run_tests.sh [all|unit|integration]

  all          Run unit tests, then integration tests (default).
  unit         Run the Go test suite without integration tests.
  integration  Run the full integration suite with Docker Compose.
EOF
}

run_unit_tests() {
	printf '%s\n' 'Running unit tests...'
	go test -count=1 ./...
}

cleanup_integration() {
	docker compose down --volumes --remove-orphans || true
}

run_integration_tests() {
	if ! command -v docker >/dev/null 2>&1; then
		printf '%s\n' 'error: Docker is required to run integration tests.' >&2
		exit 1
	fi
	if ! docker compose version >/dev/null 2>&1; then
		printf '%s\n' 'error: Docker Compose is required to run integration tests.' >&2
		exit 1
	fi

	printf '%s\n' 'Running integration tests...'
	trap cleanup_integration EXIT HUP INT TERM
	docker compose up --build --abort-on-container-exit --exit-code-from tests tests
	trap - EXIT HUP INT TERM
	cleanup_integration
}

case "${1:-all}" in
	all)
		run_unit_tests
		run_integration_tests
		;;
	unit)
		run_unit_tests
		;;
	integration)
		run_integration_tests
		;;
	-h|--help|help)
		usage
		;;
	*)
		usage >&2
		exit 2
		;;
esac
