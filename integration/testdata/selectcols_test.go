package models_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	keyless "generated.test/models/Keyless"
	values "generated.test/models/Values"
	"generated.test/models/query"
)

func selectColsProjections() [][]string {
	return [][]string{
		{"ID", "Clock", "Flag"},
		{"OptionalJSON", "ID", "OptionalFlag", "JSONValue", "Clock", "Flag", "OptionalClock"},
		{"ID", "Clock", "OptionalClock", "JSONValue", "OptionalJSON", "Flag", "OptionalFlag", "BinaryValue", "Day", "Stamp", "Amount"},
	}
}

func valuesProjection(columns []string) string {
	quoted := make([]string, len(columns))
	for i, column := range columns {
		quoted[i] = q("Values") + "." + q(column)
	}
	return strings.Join(quoted, ", ")
}

// Validate projected fields against a complete read and check unselected fields
// remain zero. These checks run outside benchmark timers and in ordinary E2E tests.
func checkSelectColsProjection(t testing.TB, db values.DBTX, columns []string, suffix string, args []any, count int) string {
	t.Helper()
	projection := valuesProjection(columns)
	want, err := values.SelectAll(db, suffix, args...)
	if err != nil || want == nil || len(want) != count {
		t.Fatalf("full reference: rows=%d want=%d err=%v", len(want), count, err)
	}
	capture := &benchmarkCapture{DBTX: db}
	got, err := values.SelectCols(capture, projection, suffix, args...)
	if err != nil || got == nil || len(got) != count {
		t.Fatalf("projection columns=%q: rows=%d want=%d err=%v", columns, len(got), count, err)
	}
	if !strings.HasPrefix(capture.statement, "SELECT "+projection+" FROM ") ||
		!strings.HasSuffix(capture.statement, " "+suffix) || !reflect.DeepEqual(capture.arguments, args) ||
		!reflect.DeepEqual(capture.columns, columns) {
		t.Fatalf("projection SQL contract differs: %q", capture.statement)
	}
	selected := make(map[string]bool, len(columns))
	for _, column := range columns {
		selected[column] = true
	}
	for i := range want {
		expected, actual := reflect.ValueOf(*want[i]), reflect.ValueOf(*got[i])
		for field := 0; field < expected.NumField(); field++ {
			info := expected.Type().Field(field)
			if !info.IsExported() {
				continue
			}
			value := expected.Field(field)
			// The Values fixture uses identical database column and Go field names.
			if !selected[info.Name] {
				value = reflect.Zero(info.Type)
			}
			if !reflect.DeepEqual(value.Interface(), actual.Field(field).Interface()) {
				t.Fatalf("projected row %d field %s differs", i, info.Name)
			}
		}
		if len(columns) == 11 && !reflect.DeepEqual(want[i], got[i]) {
			t.Fatal("full projection retained a partial-row flag")
		}
	}
	if len(columns) < 11 && len(got) > 0 {
		if err := got[0].Insert(nil); err == nil || !strings.Contains(err.Error(), "partially loaded") {
			t.Fatal("projection lost partial Insert protection")
		}
		if err := got[0].Update(nil); err == nil || !strings.Contains(err.Error(), "partially loaded") {
			t.Fatal("projection lost partial Update protection")
		}
	}
	return projection
}

func BenchmarkSelectColsProjection(b *testing.B) {
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
	ids, _ := seedBenchmarkRows(b, db)
	for _, columns := range selectColsProjections() {
		for _, size := range []int{1, 100, 1000} {
			suffix := "WHERE " + q("ID") + " <= " + placeholder(1) + " ORDER BY " + q("ID")
			args := []any{ids[size-1]}
			projection := checkSelectColsProjection(b, db, columns, suffix, args, size)
			// The projection, suffix and arguments are prepared outside timing.
			// Query construction, result metadata and scanning remain timed.
			b.Run(fmt.Sprintf("Cols=%d/Rows=%d", len(columns), size), func(b *testing.B) {
				benchmarkReadValues(b, size, ids[0], ids[size-1], func() (values.Rows, error) {
					return values.SelectCols(db, projection, suffix, args...)
				})
			})
		}
	}
}

func TestSelectColsDistinct(t *testing.T) {
	db := openDB(t)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	// This fixture has no primary key, so identical complete rows are possible.
	for _, row := range []keyless.Keyless{
		{RecordID: 1, Name: "distinct-first"},
		{RecordID: 1, Name: "distinct-first"},
		{RecordID: 2, Name: "distinct-last"},
	} {
		must(t, row.Insert(tx))
	}
	suffix := "WHERE " + q("Name") + " LIKE " + placeholder(1) + " ORDER BY " + q("RecordID")
	all, err := keyless.SelectAll(tx, suffix, "distinct-%")
	must(t, err)
	distinct, err := keyless.SelectCols(tx, "DISTINCT *", suffix, "distinct-%")
	must(t, err)
	if len(all) != 3 || len(distinct) != 2 || !reflect.DeepEqual(distinct, keyless.Rows{all[0], all[2]}) {
		t.Fatal("DISTINCT * did not deduplicate complete model rows")
	}
}

func TestSelectColsProjection(t *testing.T) {
	db := openDB(t)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	day := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	stamp := day.Add(13*time.Hour + 14*time.Minute + 15*time.Second)
	populated := &values.Values{
		Clock: "13:14:15.123456", OptionalClock: ptr("23:59:58.123456"),
		JSONValue: json.RawMessage(`{"name":"string projection"}`), OptionalJSON: ptr(json.RawMessage(`null`)),
		Flag: true, OptionalFlag: ptr(false), BinaryValue: ptr([]byte{0, 1, 127, 255}),
		Day: &day, Stamp: &stamp, Amount: ptr(123.45),
	}
	// Keep the binary NULL typed while seeding (existing SQL Server limitation).
	nulls := &values.Values{Clock: "00:00:00", JSONValue: json.RawMessage(`[]`), BinaryValue: ptr([]byte(nil))}
	must(t, populated.Insert(tx))
	must(t, nulls.Insert(tx))
	suffix, args, err := query.New().Write("WHERE "+q("ID")+" IN (:ids) ORDER BY "+q("ID")).Bind("ids", []int{populated.ID, nulls.ID}).Build()
	must(t, err)
	for _, columns := range selectColsProjections() {
		t.Run(fmt.Sprintf("Columns=%d", len(columns)), func(t *testing.T) {
			checkSelectColsProjection(t, tx, columns, suffix, args, 2)
			checkSelectColsProjection(t, tx, columns, "WHERE 1 = 0", nil, 0)
		})
	}
	all := selectColsProjections()[2]
	for left, right := 0, len(all)-1; left < right; left, right = left+1, right-1 {
		all[left], all[right] = all[right], all[left]
	}
	checkSelectColsProjection(t, tx, all, suffix, args, 2)
	want, err := values.SelectAll(tx, suffix, args...)
	must(t, err)
	star, err := values.SelectCols(tx, "*", suffix, args...)
	must(t, err)
	if !reflect.DeepEqual(star, want) {
		t.Fatal("wildcard did not preserve full rows and partial flags")
	}
	qualified, err := values.SelectCols(tx, q("Values")+".*", suffix, args...)
	must(t, err)
	if !reflect.DeepEqual(qualified, want) {
		t.Fatal("qualified wildcard differs")
	}
	// A comma inside an expression is SQL syntax, not a column separator to parse.
	expression := "COALESCE(" + q("OptionalClock") + ", " + q("Clock") + ") AS " + q("Clock") + ", " + q("ID") + ", " + q("Flag")
	capture := &benchmarkCapture{DBTX: tx}
	aliased, err := values.SelectCols(capture, expression, suffix, args...)
	must(t, err)
	if !strings.HasPrefix(capture.statement, "SELECT "+expression+" FROM ") ||
		!strings.HasSuffix(capture.statement, " "+suffix) || !reflect.DeepEqual(capture.arguments, args) {
		t.Fatal("SelectCols rewrote the projection, suffix or arguments")
	}
	// Compare to values read back through the driver: e.g. MySQL TIME(6)
	// preserves fractional zeroes even when the inserted text omitted them.
	if len(aliased) != 2 || aliased[0].Clock != *want[0].OptionalClock || aliased[1].Clock != want[1].Clock ||
		aliased[0].ID != populated.ID || aliased[1].ID != nulls.ID || !aliased[0].Flag || aliased[1].Flag {
		t.Fatalf("aliased expression result mismatch: got=%+v want source rows=%+v", aliased, want)
	}
	if err := aliased[0].Update(nil); err == nil || !strings.Contains(err.Error(), "partially loaded") {
		t.Fatal("aliased expression lost partial protection")
	}
	// An otherwise unconstrained SELECT parameter needs an explicit SQL type
	// on PostgreSQL; keep the same numeric expression on all four engines.
	parameterized, err := values.SelectCols(tx, "CAST("+placeholder(1)+" AS DECIMAL(10,2)) AS "+q("Amount")+", "+q("ID"),
		"WHERE "+q("ID")+" = "+placeholder(2), 9.75, populated.ID)
	must(t, err)
	if len(parameterized) != 1 || parameterized[0].ID != populated.ID ||
		parameterized[0].Amount == nil || *parameterized[0].Amount != 9.75 {
		t.Fatal("projection and suffix parameters were not passed in SQL order")
	}
	join := "AS v JOIN " + table("Values") + " AS other ON v." + q("ID") + " = other." + q("ID") +
		" WHERE v." + q("ID") + " = " + placeholder(1)
	joined, err := values.SelectCols(tx, "other."+q("Clock")+", v."+q("ID"), join, populated.ID)
	must(t, err)
	if len(joined) != 1 || joined[0].ID != populated.ID || joined[0].Clock != want[0].Clock {
		t.Fatal("qualified join projection differs")
	}
	if rows, err := values.SelectCols(tx, "'not-an-integer' AS "+q("ID"), suffix, args...); err == nil || rows != nil {
		t.Fatal("string projection failed to propagate a Scan error")
	}
	must(t, tx.Rollback())
	// Use autocommit for SQL errors: one failed statement must not abort a
	// PostgreSQL transaction and accidentally make subsequent checks pass.
	for _, test := range []struct{ name, projection, message string }{
		{"unknown_alias", q("ID") + " AS " + q("Unmapped"), "unknown result column"},
		{"case_mismatch", q("ID") + " AS " + q("id"), "unknown result column"},
		{"duplicate", q("ID") + ", " + q("ID"), "duplicate result column"},
		{"duplicate_full_count", strings.Repeat(q("ID")+", ", 10) + q("ID"), "duplicate result column"},
		{"missing_column", q("Values") + "." + q("DoesNotExist"), ""},
		{"empty_projection", "", ""},
		{"invalid_syntax", q("ID") + ",", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := values.SelectCols(db, test.projection, "WHERE 1 = 0")
			if err == nil || rows != nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error %q, got rows=%v err=%v", test.message, rows, err)
			}
		})
	}
}
