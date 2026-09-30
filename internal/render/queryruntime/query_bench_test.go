package query

import (
	"fmt"
	"strconv"
	"testing"
)

// Keep outputs observable without retaining all results of a benchmark run.
var benchmarkSQL string
var benchmarkArgs []any
var benchmarkTokens []parameter

// This isolates lexer work from database latency, and is also a baseline for
// checking that comment handling does not add allocations to ordinary queries.
func BenchmarkScanNoComments(b *testing.B) {
	const statement = "WHERE company_id = :company AND (name LIKE :name OR notes LIKE :name) AND id IN (:ids) AND created_at >= :since ORDER BY id"
	for _, dialect := range dialects {
		b.Run(dialect, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				tokens, err := scan(statement, dialect)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkTokens = tokens
			}
		})
	}
}

// Large integer values avoid drawing conclusions only from Go's small-integer
// interface boxing optimization. Named elements exercise the reflection fallback.
func BenchmarkBuildListTypes(b *testing.B) {
	type recordID int64
	integers := make([]int, 128)
	int64s := make([]int64, 128)
	strings := make([]string, 128)
	interfaces := make([]any, 128)
	named := make([]recordID, 128)
	for i := range integers {
		id := 1_000_000 + i
		integers[i], int64s[i], named[i] = id, int64(id), recordID(id)
		strings[i], interfaces[i] = "item-"+strconv.Itoa(id), id
	}
	for _, dialect := range dialects {
		for _, test := range []struct {
			name string
			list any
		}{
			{"Int", integers}, {"Int64", int64s}, {"String", strings},
			{"Any", interfaces}, {"Named", named},
		} {
			b.Run(dialect+"/"+test.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					builder := New().Write("WHERE id IN (:ids) OR parent_id IN (:ids)").Bind("ids", test.list)
					stmt, args, err := builder.build(dialect)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkSQL, benchmarkArgs = stmt, args
				}
			})
		}
	}
}

func BenchmarkBuildDynamicFilters(b *testing.B) {
	for _, dialect := range dialects {
		for _, count := range []int{1, 16, 128} {
			b.Run(fmt.Sprintf("%s/IDs=%d", dialect, count), func(b *testing.B) {
				ids := make([]int, count)
				for i := range ids {
					ids[i] = i + 1
				}
				b.ReportAllocs()
				for b.Loop() {
					builder := New().Write("WHERE company_id = :company").Bind("company", 42)
					builder.Write("AND (name LIKE :name OR notes LIKE :name)").Bind("name", "vehicle-%")
					builder.Write("AND id IN (:ids)").Bind("ids", ids)
					builder.Write("AND created_at >= :since").Bind("since", "2026-01-01")
					builder.Write("ORDER BY id")
					stmt, args, err := builder.build(dialect)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkSQL, benchmarkArgs = stmt, args
				}
			})
		}
	}
}
