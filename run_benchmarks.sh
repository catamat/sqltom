#!/usr/bin/env sh

set -eu

project_directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$project_directory"

usage() {
	cat <<'EOF'
Usage: ./run_benchmarks.sh [all|unit|integration]

  all          Run lexer/builder benchmarks, then real-driver benchmarks (default).
  unit         Run lexer/builder benchmarks locally with Go.
  integration  Run generated/raw benchmarks on all four dialects with Docker Compose.

Environment:
  SQLTOM_BENCHTIME   Duration of each benchmark sample (default: 500ms).
  SQLTOM_BENCHCOUNT  Number of samples per benchmark (default: 5).
  SQLTOM_BENCH       Go benchmark filter for integration (default: all groups).

Example:
  SQLTOM_BENCHTIME=1s SQLTOM_BENCHCOUNT=3 ./run_benchmarks.sh integration
  SQLTOM_BENCH='^BenchmarkSelectColsProjection$' ./run_benchmarks.sh integration

Use the Docker test databases exclusively; do not run tests concurrently.
Database containers remain running after the benchmarks for reuse.
EOF
}

if [ "$#" -gt 1 ]; then
	usage >&2
	exit 2
fi

benchmark_mode=${1:-all}
case "$benchmark_mode" in
	-h|--help|help)
		usage
		exit 0
		;;
	all|unit|integration) ;;
	*)
		usage >&2
		exit 2
		;;
esac

benchmark_duration=${SQLTOM_BENCHTIME:-500ms}
benchmark_count=${SQLTOM_BENCHCOUNT:-5}
if ! [ "$benchmark_count" -gt 0 ] 2>/dev/null; then
	printf '%s\n' 'error: SQLTOM_BENCHCOUNT must be a positive integer.' >&2
	exit 2
fi

run_unit_benchmarks() {
	if ! command -v go >/dev/null 2>&1; then
		printf '%s\n' 'error: Go is required to run lexer/builder benchmarks.' >&2
		exit 1
	fi
	printf '%s\n' "Running lexer/builder benchmarks (${benchmark_duration}, ${benchmark_count} samples)..."
	go test -run '^$' -bench '^Benchmark(ScanNoComments|BuildDynamicFilters|BuildListTypes)$' \
		-benchmem -benchtime="$benchmark_duration" -count="$benchmark_count" ./internal/render/queryruntime
}

run_integration_benchmarks() {
	if ! command -v docker >/dev/null 2>&1; then
		printf '%s\n' 'error: Docker is required to run real-driver benchmarks.' >&2
		exit 1
	fi
	if ! docker compose version >/dev/null 2>&1; then
		printf '%s\n' 'error: Docker Compose is required to run real-driver benchmarks.' >&2
		exit 1
	fi
	printf '%s\n' "Running real-driver benchmarks (${benchmark_duration}, ${benchmark_count} samples)..."
	docker compose run --build --rm -T \
		-e SQLTOM_BENCHMARKS=1 \
		-e "SQLTOM_BENCHTIME=$benchmark_duration" \
		-e "SQLTOM_BENCHCOUNT=$benchmark_count" \
		-e "SQLTOM_BENCH=${SQLTOM_BENCH:-^Benchmark(Driver|SelectColsProjection)$}" \
		tests go test -tags=integration -run '^TestGeneratedBenchmarks$' -count=1 -v -timeout=30m ./integration
}

case "$benchmark_mode" in
	all)
		run_unit_benchmarks
		run_integration_benchmarks
		;;
	unit)
		run_unit_benchmarks
		;;
	integration)
		run_integration_benchmarks
		;;
esac
