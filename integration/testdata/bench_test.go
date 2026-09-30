package models_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	values "generated.test/models/Values"
	vehicle "generated.test/models/Vehicle"
	"generated.test/models/query"
)

var benchmarkValues values.Rows
var benchmarkPointers vehicle.Rows
var benchmarkStructs []vehicle.Vehicle
var benchmarkExists bool

// This benchmark is compiled alongside the actual generated models. Every leaf
// uses the real database/sql driver, a warmed single-connection pool and B.Loop.
// No schema setup, seed insertion, compilation or warm-up enters the metrics.
func BenchmarkDriver(b *testing.B) {
	db, err := sql.Open(os.Getenv("SQLTOM_E2E_DRIVER"), os.Getenv("SQLTOM_E2E_DSN"))
	if err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	b.Cleanup(func() { db.Close() })
	if err := db.Ping(); err != nil {
		b.Fatal(err)
	}
	benchmarkServerVersion(b, db)
	valueIDs, vehicleIDs := seedBenchmarkRows(b, db)
	projection := valuesProjection([]string{"ID", "Clock", "Flag"})
	for _, size := range []int{1, 100, 1000} {
		suffix := "WHERE " + q("ID") + " <= " + placeholder(1) + " ORDER BY " + q("ID")
		args := []any{valueIDs[size-1]}
		rawAll := checkRawValues(b, db, suffix, args, false, size)
		rawCols := checkRawValues(b, db, suffix, args, true, size)
		b.Run(fmt.Sprintf("Select/Rows=%d/All", size), func(b *testing.B) {
			benchmarkReadValues(b, size, valueIDs[0], valueIDs[size-1], func() (values.Rows, error) { return values.SelectAll(db, suffix, args...) })
		})
		b.Run(fmt.Sprintf("Select/Rows=%d/RawAll", size), func(b *testing.B) {
			benchmarkReadValues(b, size, valueIDs[0], valueIDs[size-1], func() (values.Rows, error) { return rawSelectAllValues(db, rawAll, args) })
		})
		b.Run(fmt.Sprintf("Select/Rows=%d/Cols", size), func(b *testing.B) {
			benchmarkReadValues(b, size, valueIDs[0], valueIDs[size-1], func() (values.Rows, error) { return values.SelectCols(db, projection, suffix, args...) })
		})
		b.Run(fmt.Sprintf("Select/Rows=%d/RawCols", size), func(b *testing.B) {
			benchmarkReadValues(b, size, valueIDs[0], valueIDs[size-1], func() (values.Rows, error) { return rawSelectColsValues(db, rawCols, args) })
		})
	}
	for _, size := range []int{1, 16, 128} {
		ids := valueIDs[:size]
		build := func() (string, []any, error) {
			return query.New().Write("WHERE "+q("ID")+" IN (:ids)").Bind("ids", ids).
				Write("AND ("+q("Clock")+" = :clock OR "+q("OptionalClock")+" = :clock)").Bind("clock", "13:14:15.123456").
				Write("ORDER BY " + q("ID")).Build()
		}
		stmt, args, err := build()
		if err != nil {
			b.Fatal(err)
		}
		rawStmt := checkRawValues(b, db, stmt, args, true, size)
		b.Run(fmt.Sprintf("Dynamic/IDs=%d/Raw", size), func(b *testing.B) {
			benchmarkReadValues(b, size, ids[0], ids[size-1], func() (values.Rows, error) { return rawSelectColsValues(db, rawStmt, args) })
		})
		b.Run(fmt.Sprintf("Dynamic/IDs=%d/Prebuilt", size), func(b *testing.B) {
			benchmarkReadValues(b, size, ids[0], ids[size-1], func() (values.Rows, error) { return values.SelectCols(db, projection, stmt, args...) })
		})
		b.Run(fmt.Sprintf("Dynamic/IDs=%d/BuildAndSelect", size), func(b *testing.B) {
			benchmarkReadValues(b, size, ids[0], ids[size-1], func() (values.Rows, error) {
				stmt, args, err := build()
				if err != nil {
					return nil, err
				}
				return values.SelectCols(db, projection, stmt, args...)
			})
		})
	}
	// Use the same SQL and Scan destinations for both representations. The
	// value slice scans directly into its appended slot, avoiding an unnecessary
	// temporary heap-allocated struct. Both slices grow from zero capacity.
	for _, size := range []int{1, 100, 1000} {
		suffix := "WHERE " + q("ID") + " <= " + placeholder(1) + " ORDER BY " + q("ID")
		args := []any{vehicleIDs[size-1]}
		capture := &benchmarkCapture{DBTX: db}
		warm, err := vehicle.SelectAll(capture, suffix, args...)
		if err != nil || len(warm) != size {
			b.Fatalf("warm-up rows=%d err=%v", len(warm), err)
		}
		stmt := capture.statement
		structs, err := benchmarkScanStructs(db, stmt, args)
		if err != nil || len(structs) != len(warm) {
			b.Fatalf("value baseline rows=%d err=%v", len(structs), err)
		}
		for i := range warm {
			if !reflect.DeepEqual(*warm[i], structs[i]) {
				b.Fatalf("pointer/value baseline mismatch at row %d", i)
			}
		}
		raw, err := rawSelectVehicle(db, stmt, args)
		if err != nil || !reflect.DeepEqual(raw, warm) {
			b.Fatalf("raw pointer baseline mismatch: %v", err)
		}
		b.Run(fmt.Sprintf("RowStorage/Rows=%d/Pointers", size), func(b *testing.B) {
			benchmarkReadPointers(b, size, vehicleIDs[0], vehicleIDs[size-1], func() (vehicle.Rows, error) { return vehicle.SelectAll(db, suffix, args...) })
		})
		b.Run(fmt.Sprintf("RowStorage/Rows=%d/RawPointers", size), func(b *testing.B) {
			benchmarkReadPointers(b, size, vehicleIDs[0], vehicleIDs[size-1], func() (vehicle.Rows, error) { return rawSelectVehicle(db, stmt, args) })
		})
		b.Run(fmt.Sprintf("RowStorage/Rows=%d/Values", size), func(b *testing.B) {
			b.Cleanup(func() { benchmarkStructs = nil })
			warm, err := benchmarkScanStructs(db, stmt, args)
			if err != nil || len(warm) != size || warm[0].ID != vehicleIDs[0] || warm[size-1].ID != vehicleIDs[size-1] {
				b.Fatalf("invalid value result: %v", err)
			}
			b.ReportAllocs()
			for b.Loop() {
				rows, err := benchmarkScanStructs(db, stmt, args)
				if err != nil || len(rows) != size {
					b.Fatalf("rows=%d err=%v", len(rows), err)
				}
				benchmarkStructs = rows
			}
			b.ReportMetric(float64(size), "rows/op")
		})
	}
	for _, matches := range []int{0, 1, len(vehicleIDs)} {
		last := vehicleIDs[0] - 1
		if matches > 0 {
			last = vehicleIDs[matches-1]
		}
		suffix := "WHERE " + q("ID") + " <= " + placeholder(1)
		args := []any{last}
		b.Run(fmt.Sprintf("Exists/Matches=%d/Generated", matches), func(b *testing.B) {
			benchmarkReadExists(b, matches > 0, func() (bool, error) { return vehicle.Exists(db, suffix, args...) })
		})
	}
}

func benchmarkReadValues(b *testing.B, size, firstID, lastID int, read func() (values.Rows, error)) {
	b.Helper()
	b.Cleanup(func() { benchmarkValues = nil })
	warm, err := read()
	if err != nil || len(warm) != size || warm[0].ID != firstID || warm[size-1].ID != lastID || warm[0].Clock != "13:14:15.123456" {
		b.Fatalf("invalid read warm-up: rows=%d err=%v", len(warm), err)
	}
	b.ReportAllocs()
	for b.Loop() {
		rows, err := read()
		if err != nil || len(rows) != size {
			b.Fatalf("rows=%d err=%v", len(rows), err)
		}
		benchmarkValues = rows
	}
	b.ReportMetric(float64(size), "rows/op")
}

func benchmarkReadPointers(b *testing.B, size, firstID, lastID int, read func() (vehicle.Rows, error)) {
	b.Helper()
	b.Cleanup(func() { benchmarkPointers = nil })
	warm, err := read()
	if err != nil || len(warm) != size || warm[0].ID != firstID || warm[size-1].ID != lastID {
		b.Fatalf("invalid pointer result: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		rows, err := read()
		if err != nil || len(rows) != size {
			b.Fatalf("rows=%d err=%v", len(rows), err)
		}
		benchmarkPointers = rows
	}
	b.ReportMetric(float64(size), "rows/op")
}

func benchmarkReadExists(b *testing.B, want bool, read func() (bool, error)) {
	b.Helper()
	if got, err := read(); err != nil || got != want {
		b.Fatalf("exists=%v err=%v", got, err)
	}
	b.ReportAllocs()
	for b.Loop() {
		got, err := read()
		if err != nil || got != want {
			b.Fatalf("exists=%v err=%v", got, err)
		}
		benchmarkExists = got
	}
}

type benchmarkCapture struct {
	vehicle.DBTX
	statement string
	arguments []any
	columns   []string
}

func (c *benchmarkCapture) Query(stmt string, args ...any) (*sql.Rows, error) {
	c.statement = stmt
	c.arguments = append([]any(nil), args...)
	rows, err := c.DBTX.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	c.columns, err = rows.Columns()
	if err != nil {
		rows.Close()
		return nil, err
	}
	return rows, nil
}

func benchmarkScanStructs(db *sql.DB, stmt string, args []any) ([]vehicle.Vehicle, error) {
	rows, err := db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []vehicle.Vehicle{}
	for rows.Next() {
		result = append(result, vehicle.Vehicle{})
		r := &result[len(result)-1]
		if err := rows.Scan(&r.Name, &r.ID, &r.Defaulted, &r.Slug, &r.Notes); err != nil {
			return nil, err
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func seedBenchmarkRows(b *testing.B, db *sql.DB) ([]int, []int) {
	b.Helper()
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	for _, name := range []string{"Values", "Vehicle"} {
		if _, err := tx.Exec("DELETE FROM " + table(name)); err != nil {
			b.Fatal(err)
		}
	}
	day := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	stamp := day.Add(13*time.Hour + 14*time.Minute + 15*time.Second)
	jsonText := `{"label":"` + strings.Repeat("vehicle-", 16) + `","tags":["service","active"],"count":42}`
	binary := []byte(strings.Repeat("data", 64))
	benchmarkInsertBatches(b, tx, "Values", []string{"Clock", "OptionalClock", "JSONValue", "OptionalJSON", "Flag", "OptionalFlag", "BinaryValue", "Day", "Stamp", "Amount"}, 1000, func(i int) []any {
		var clock, optionalJSON, flag any
		if i%2 == 0 {
			clock, optionalJSON, flag = "23:59:58.123456", `{"optional":true}`, false
		}
		return []any{"13:14:15.123456", clock, jsonText, optionalJSON, i%2 == 0, flag, binary, day, stamp, 123.45}
	})
	benchmarkInsertBatches(b, tx, "Vehicle", []string{"Name", "Defaulted", "Notes"}, 10000, func(i int) []any {
		var defaulted, notes any
		if i%2 == 0 {
			defaulted, notes = "default-value", strings.Repeat("detail ", 12)
		}
		return []any{fmt.Sprintf("vehicle-%05d", i), defaulted, notes}
	})
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	valueIDs, vehicleIDs := benchmarkIDs(b, db, "Values"), benchmarkIDs(b, db, "Vehicle")
	if len(valueIDs) != 1000 || len(vehicleIDs) != 10000 {
		b.Fatal("incomplete benchmark seed")
	}
	// Validate representative payloads outside the timing loop, including both
	// populated and NULL nullable fields. Benchmarks must not time empty reads.
	rows, err := values.SelectAll(db, "WHERE "+q("ID")+" <= "+placeholder(1)+" ORDER BY "+q("ID"), valueIDs[1])
	if err != nil || len(rows) != 2 {
		b.Fatalf("seed read: %v", err)
	}
	if !json.Valid(rows[0].JSONValue) || rows[0].BinaryValue == nil || len(*rows[0].BinaryValue) != 256 || rows[0].OptionalJSON == nil || rows[1].OptionalJSON != nil {
		b.Fatal("seed payload/nullable mismatch")
	}
	return valueIDs, vehicleIDs
}

func benchmarkInsertBatches(b *testing.B, tx *sql.Tx, name string, columns []string, count int, row func(int) []any) {
	b.Helper()
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = q(column)
	}
	for start := 0; start < count; start += 100 {
		var groups []string
		var args []any
		for i := start; i < min(start+100, count); i++ {
			value := row(i)
			placeholders := make([]string, len(value))
			for j, arg := range value {
				args = append(args, arg)
				placeholders[j] = placeholder(len(args))
			}
			groups = append(groups, "("+strings.Join(placeholders, ",")+")")
		}
		stmt := "INSERT INTO " + table(name) + " (" + strings.Join(quoted, ",") + ") VALUES " + strings.Join(groups, ",")
		if _, err := tx.Exec(stmt, args...); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkIDs(b *testing.B, db *sql.DB, name string) []int {
	b.Helper()
	rows, err := db.Query("SELECT " + q("ID") + " FROM " + table(name) + " ORDER BY " + q("ID"))
	if err != nil {
		b.Fatal(err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			b.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		b.Fatal(err)
	}
	return ids
}

func benchmarkServerVersion(b *testing.B, db *sql.DB) {
	b.Helper()
	dialect := os.Getenv("SQLTOM_E2E_DIALECT")
	stmt := "SELECT VERSION()"
	switch dialect {
	case "sqlserver":
		stmt = "SELECT CONVERT(nvarchar(128), SERVERPROPERTY('ProductVersion'))"
	case "sqlite":
		stmt = "SELECT sqlite_version()"
	}
	var version string
	if err := db.QueryRow(stmt).Scan(&version); err != nil {
		b.Fatal(err)
	}
	b.Logf("dialect=%s server=%s driver=%s Go=%s pool=1; seeded Values=1000 Vehicle=10000", dialect, version, os.Getenv("SQLTOM_E2E_DRIVER"), runtime.Version())
}
