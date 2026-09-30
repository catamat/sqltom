package models_test

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	values "generated.test/models/Values"
	vehicle "generated.test/models/Vehicle"
	"generated.test/models/query"
)

// These are handwritten database/sql baselines, compiled only in the temporary
// consumer's tests. They materialize the same pointer slices as the generated
// readers, starting at zero capacity, and consume every row including rows.Err.
// SQL is captured outside timing from the generated reader to keep projections,
// quoting, predicates and parameter order exactly equal on each dialect.
func rawSelectAllValues(db values.DBTX, stmt string, args []any) (values.Rows, error) {
	rows, err := db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := values.Rows{}
	adaptBinary := os.Getenv("SQLTOM_E2E_DIALECT") == "sqlite"
	for rows.Next() {
		r := &values.Values{}
		var binaryDestination any = &r.BinaryValue
		if adaptBinary {
			binaryDestination = rawLogicalValue{destination: &r.BinaryValue, kind: "binary"}
		}
		if err := rows.Scan(
			&r.ID,
			rawLogicalValue{destination: &r.Clock, kind: "time"},
			rawLogicalValue{destination: &r.OptionalClock, kind: "time"},
			rawLogicalValue{destination: &r.JSONValue, kind: "json"},
			rawLogicalValue{destination: &r.OptionalJSON, kind: "json"},
			rawLogicalValue{destination: &r.Flag, kind: "boolean"},
			rawLogicalValue{destination: &r.OptionalFlag, kind: "boolean"},
			binaryDestination, &r.Day, &r.Stamp, &r.Amount,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// This baseline has a fixed projection, like application SQL written by hand.
// It does not provide SelectCols' result-name mapping or partial-write guard.
func rawSelectColsValues(db values.DBTX, stmt string, args []any) (values.Rows, error) {
	rows, err := db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := values.Rows{}
	for rows.Next() {
		r := &values.Values{}
		if err := rows.Scan(&r.ID, rawLogicalValue{destination: &r.Clock, kind: "time"}, rawLogicalValue{destination: &r.Flag, kind: "boolean"}); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func rawSelectVehicle(db vehicle.DBTX, stmt string, args []any) (vehicle.Rows, error) {
	rows, err := db.Query(stmt, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := vehicle.Rows{}
	for rows.Next() {
		r := &vehicle.Vehicle{}
		if err := rows.Scan(&r.Name, &r.ID, &r.Defaulted, &r.Slug, &r.Notes); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Match the generated logical conversions: bypassing these would compare
// different work and can produce incorrect values (e.g. SQL Server TIME or
// MySQL BIT). database/sql still owns nullable and destination-type conversion.
type rawLogicalValue struct {
	destination any
	kind        string
}

func (s rawLogicalValue) Scan(value any) error {
	switch s.kind {
	case "binary":
		if bytes, ok := value.([]byte); ok && bytes == nil {
			value = []byte{}
		}
	case "json":
		if converted, ok := value.(string); ok {
			value = []byte(converted)
		}
	case "boolean":
		if bytes, ok := value.([]byte); ok && len(bytes) == 1 && bytes[0] <= 1 {
			value = bytes[0] == 1
		}
	case "time":
		if converted, ok := value.(time.Time); ok {
			value = converted.Format("15:04:05.999999999")
		}
	}
	return sql.ConvertAssign(driver.ScanContext{}, s.destination, value)
}

// checkRawValues runs outside benchmark timers and in the ordinary E2E suite.
// All exported fields are checked, including zero/unselected fields and NULLs.
// The private partial-write guard is a generated API feature, not read data.
func checkRawValues(t testing.TB, db values.DBTX, suffix string, args []any, partial bool, count int) string {
	t.Helper()
	capture := &benchmarkCapture{DBTX: db}
	columns := []string{"ID", "Clock", "OptionalClock", "JSONValue", "OptionalJSON", "Flag", "OptionalFlag", "BinaryValue", "Day", "Stamp", "Amount"}
	var generated values.Rows
	var err error
	read := rawSelectAllValues
	if partial {
		columns = []string{"ID", "Clock", "Flag"}
		generated, err = values.SelectCols(capture, valuesProjection(columns), suffix, args...)
		read = rawSelectColsValues
	} else {
		generated, err = values.SelectAll(capture, suffix, args...)
	}
	if err != nil || generated == nil || len(generated) != count {
		t.Fatalf("generated rows=%d want=%d err=%v", len(generated), count, err)
	}
	if !reflect.DeepEqual(capture.arguments, args) || !reflect.DeepEqual(capture.columns, columns) {
		t.Fatalf("raw SQL contract mismatch: args=%#v columns=%q", capture.arguments, capture.columns)
	}
	raw, err := read(db, capture.statement, args)
	if err != nil || raw == nil || len(raw) != len(generated) {
		t.Fatalf("raw rows=%d want=%d err=%v", len(raw), len(generated), err)
	}
	if !partial && !reflect.DeepEqual(raw, generated) {
		t.Fatal("raw/full result mismatch")
	}
	for i := range generated {
		left, right := reflect.ValueOf(*generated[i]), reflect.ValueOf(*raw[i])
		for field := 0; field < left.NumField(); field++ {
			info := left.Type().Field(field)
			if info.IsExported() && !reflect.DeepEqual(left.Field(field).Interface(), right.Field(field).Interface()) {
				t.Fatalf("raw result mismatch at row %d field %s", i, info.Name)
			}
		}
	}
	return capture.statement
}

func TestRawReadsMatchGenerated(t *testing.T) {
	db := openDB(t)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	day := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
	stamp := day.Add(13*time.Hour + 14*time.Minute + 15*time.Second)
	populated := &values.Values{
		Clock: "13:14:15.123456", OptionalClock: ptr("23:59:58.123456"),
		JSONValue: json.RawMessage(`{"name":"raw","count":42}`), OptionalJSON: ptr(json.RawMessage(`null`)),
		Flag: true, OptionalFlag: ptr(false), BinaryValue: ptr([]byte{0, 1, 127, 255}),
		Day: &day, Stamp: &stamp, Amount: ptr(123.45),
	}
	nulls := &values.Values{Clock: "00:00:00", JSONValue: json.RawMessage(`[]`), Flag: false}
	empty := &values.Values{Clock: "00:00:00", JSONValue: json.RawMessage(`[]`), BinaryValue: ptr([]byte{})}
	must(t, populated.Insert(tx))
	must(t, nulls.Insert(tx))
	must(t, empty.Insert(tx))
	var binaryIsNull bool
	must(t, tx.QueryRow("SELECT CASE WHEN "+q("BinaryValue")+" IS NULL THEN 1 ELSE 0 END FROM "+table("Values")+" "+where("ID"), nulls.ID).Scan(&binaryIsNull))
	if !binaryIsNull {
		t.Fatal("binary fixture must contain SQL NULL, not an empty binary value")
	}
	stmt, args, err := query.New().Write("WHERE "+q("ID")+" IN (:ids) ORDER BY "+q("ID")).Bind("ids", []int{populated.ID, nulls.ID, empty.ID}).Build()
	must(t, err)
	for _, partial := range []bool{false, true} {
		checkRawValues(t, tx, stmt, args, partial, 3)
		checkRawValues(t, tx, "WHERE 1 = 0", nil, partial, 0)
	}
	car := &vehicle.Vehicle{Name: "raw-parity", Defaulted: ptr("value"), Notes: ptr("notes")}
	must(t, car.Insert(tx))
	capture := &benchmarkCapture{DBTX: tx}
	generated, err := vehicle.SelectAll(capture, where("ID"), car.ID)
	must(t, err)
	raw, err := rawSelectVehicle(tx, capture.statement, capture.arguments)
	must(t, err)
	if len(raw) != 1 || !reflect.DeepEqual(raw, generated) {
		t.Fatal("raw vehicle result mismatch")
	}
}
