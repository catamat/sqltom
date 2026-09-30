//go:build integration

package integration_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/catamat/sqltom/internal/dialect"
)

// The outer test prepares disposable fixtures and generated consumer modules.
// Actual Benchmark functions run in those modules so schema setup, compilation,
// process startup and data seeding never contribute to ns/op, B/op or allocs/op.
func TestGeneratedBenchmarks(t *testing.T) {
	if os.Getenv("SQLTOM_BENCHMARKS") != "1" {
		t.Skip("set SQLTOM_BENCHMARKS=1 to run the real-driver benchmarks")
	}
	benchTime := os.Getenv("SQLTOM_BENCHTIME")
	if benchTime == "" {
		benchTime = "300ms"
	}
	if duration, err := time.ParseDuration(benchTime); err != nil || duration <= 0 {
		t.Fatal("SQLTOM_BENCHTIME must be a positive duration, e.g. 300ms or 2s")
	}
	count := os.Getenv("SQLTOM_BENCHCOUNT")
	if count == "" {
		count = "3"
	}
	if n, err := strconv.Atoi(count); err != nil || n < 1 {
		t.Fatal("SQLTOM_BENCHCOUNT must be a positive integer")
	}
	pattern := os.Getenv("SQLTOM_BENCH")
	if pattern == "" {
		pattern = "^Benchmark(Driver|SelectColsProjection)$"
	}
	binary := buildCLI(t)
	for _, current := range integrationFixtures(t) {
		t.Run(current.name, func(t *testing.T) {
			if current.dsn == "" {
				t.Skip("external database DSN is not configured")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
			defer cancel()
			if current.name == dialect.SQLServer {
				prepareSQLServerDatabase(t, ctx, current.adminDSN)
			}
			prepareSchema(t, ctx, current)
			directory := t.TempDir()
			prepareGeneratedModule(t, ctx, binary, directory, current, []string{"Vehicle", "VehicleView", "CompositeKey"})
			runGeneratedCommand(t, ctx, directory, current, "-run=^$", "-bench="+pattern, "-benchmem", "-benchtime="+benchTime, "-count="+count, "-timeout=25m", "-v", ".")
		})
	}
}
