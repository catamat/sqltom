package models_test

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	vehicle "generated.test/models/Vehicle"
	"generated.test/models/query"
)

func TestQueryBuilderWithGeneratedOperations(t *testing.T) {
	db := openDB(t)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	records := []*vehicle.Vehicle{
		{Name: "builder-first", Notes: ptr("memo")},
		{Name: "builder-second", Notes: ptr("memo")},
		{Name: "builder-third", Notes: ptr("memo")},
	}
	for _, row := range records {
		must(t, row.Insert(tx))
	}
	builder := query.New()
	ids := []int{records[0].ID, records[2].ID}
	builder.Bind("ids", ids) // Binding order does not determine SQL position.
	builder.Write("WHERE ("+q("Name")+" LIKE :prefix OR "+q("Notes")+" LIKE :prefix)").Bind("prefix", "builder-%")
	builder.Write("AND " + q("ID") + " IN (:ids)").Write("ORDER BY " + q("ID"))
	stmt, args, err := builder.Build()
	must(t, err)
	wantArgs := 3
	if dialect := os.Getenv("SQLTOM_E2E_DIALECT"); dialect == "mysql" || dialect == "sqlite" {
		wantArgs = 4
	}
	if len(args) != wantArgs {
		t.Fatalf("args=%#v", args)
	}
	rows, err := vehicle.SelectAll(tx, stmt, args...)
	must(t, err)
	if len(rows) != 2 || rows[0].ID != records[0].ID || rows[1].ID != records[2].ID {
		t.Fatalf("dynamic SelectAll=%#v", rows)
	}
	projected, err := vehicle.SelectCols(tx, q("ID")+", "+q("Name"), stmt, args...)
	must(t, err)
	if len(projected) != 2 || projected[0].ID != records[0].ID || projected[1].Name != "builder-third" {
		t.Fatal("dynamic SelectCols differs")
	}
	exists, err := vehicle.Exists(tx, stmt, args...)
	must(t, err)
	if !exists {
		t.Fatal("dynamic Exists missed rows")
	}
	preview, err := builder.Preview()
	must(t, err)
	if !strings.Contains(preview, stmt) || !strings.Contains(preview, `"builder-%"`) {
		t.Fatalf("Preview=%q", preview)
	}
	again, fresh, err := builder.Build()
	must(t, err)
	if again != stmt || !reflect.DeepEqual(fresh, args) {
		t.Fatal("Preview changed Build")
	}
	// A second Build observes list changes without modifying previously built arguments.
	ids[0] = records[1].ID
	changedSQL, changedArgs, err := builder.Build()
	must(t, err)
	changed, err := vehicle.SelectAll(tx, changedSQL, changedArgs...)
	must(t, err)
	if len(changed) != 2 || changed[0].ID != records[1].ID || changed[1].ID != records[2].ID {
		t.Fatal("Build reused stale list values")
	}
	original, err := vehicle.SelectAll(tx, stmt, args...)
	must(t, err)
	if len(original) != 2 || original[0].ID != records[0].ID || original[1].ID != records[2].ID {
		t.Fatal("subsequent Build changed previous arguments")
	}
	raw := query.New().Write("SELECT " + strings.Join([]string{q("Name"), q("ID"), q("Defaulted"), q("Slug"), q("Notes")}, ", ") + " FROM " + table("Vehicle"))
	raw.Write("WHERE "+q("ID")+" = :id").Bind("id", records[0].ID)
	rawSQL, rawArgs, err := raw.Build()
	must(t, err)
	queried, err := vehicle.Query(tx, rawSQL, rawArgs...)
	must(t, err)
	only, err := queried.One()
	must(t, err)
	if only.ID != records[0].ID {
		t.Fatal("full SQL Query builder failed")
	}
	// Build can also produce DML, without changing model APIs or checking clauses.
	update := query.New().Write("UPDATE " + table("Vehicle") + " SET " + q("Name") + " = :name WHERE " + q("ID") + " = :id")
	update.Bind("id", records[0].ID).Bind("name", "builder-updated")
	updateSQL, updateArgs, err := update.Build()
	must(t, err)
	_, err = tx.Exec(updateSQL, updateArgs...)
	must(t, err)
	readBack, err := vehicle.Query(tx, rawSQL, rawArgs...)
	must(t, err)
	if len(readBack) != 1 || readBack[0].Name != "builder-updated" {
		t.Fatal("dynamic update failed")
	}
	deletion := query.New().Write("WHERE "+q("ID")+" IN (:ids)").Bind("ids", []int{records[0].ID, records[2].ID})
	deleteSQL, deleteArgs, err := deletion.Build()
	must(t, err)
	must(t, vehicle.Delete(tx, deleteSQL, deleteArgs...))
	exists, err = vehicle.Exists(tx, deleteSQL, deleteArgs...)
	must(t, err)
	if exists {
		t.Fatal("dynamic Delete left matching rows")
	}
	untouched, err := vehicle.SelectAll(tx, where("ID"), records[1].ID)
	must(t, err)
	if len(untouched) != 1 {
		t.Fatal("dynamic Delete affected a row outside the list")
	}
}

func TestQueryBuilderPreservesLiteralsAndScalars(t *testing.T) {
	db := openDB(t)
	// The integer literal also gives PostgreSQL enough context to infer the
	// parameter type when preparing the query, independently of its bound value.
	builder := query.New().Write("SELECT ':untouched', :n + 0 + :n /* :comment */").Bind("n", 7)
	stmt, args, err := builder.Build()
	must(t, err)
	var literal string
	var sum int
	must(t, db.QueryRow(stmt, args...).Scan(&literal, &sum))
	if literal != ":untouched" || sum != 14 {
		t.Fatalf("literal=%q sum=%d", literal, sum)
	}
	for _, forced := range []bool{false, true} {
		value := []byte{0, 1, 127, 255}
		var bound any = value
		if forced {
			bound = query.Scalar(value)
		}
		expression := ":bytes"
		if os.Getenv("SQLTOM_E2E_DIALECT") == "postgres" {
			expression = "CAST(:bytes AS bytea)"
		}
		builder = query.New().Write("SELECT "+expression).Bind("bytes", bound)
		stmt, args, err = builder.Build()
		must(t, err)
		if len(args) != 1 {
			t.Fatal("binary scalar expanded")
		}
		var got []byte
		must(t, db.QueryRow(stmt, args...).Scan(&got))
		if !reflect.DeepEqual(got, value) {
			t.Fatalf("binary round trip=%v", got)
		}
	}
	builder = query.New().Write("SELECT :encoded").Bind("encoded", queryEncodedList{3, 5})
	stmt, args, err = builder.Build()
	must(t, err)
	if len(args) != 1 {
		t.Fatal("Valuer slice expanded")
	}
	var encoded string
	must(t, db.QueryRow(stmt, args...).Scan(&encoded))
	if encoded != "[3 5]" {
		t.Fatalf("Valuer scalar=%q", encoded)
	}
	builder = query.New().Write("SELECT :null").Bind("null", query.Scalar(nil))
	stmt, args, err = builder.Build()
	must(t, err)
	var nullable any
	must(t, db.QueryRow(stmt, args...).Scan(&nullable))
	if nullable != nil {
		t.Fatalf("SQL NULL=%#v", nullable)
	}
}

type queryEncodedList []int

func (v queryEncodedList) Value() (driver.Value, error) { return fmt.Sprint([]int(v)), nil }

func TestQueryBuilderScalarPostgresArray(t *testing.T) {
	if os.Getenv("SQLTOM_E2E_DIALECT") != "postgres" {
		t.Skip("native SQL arrays are a PostgreSQL codec feature")
	}
	db := openDB(t)
	stmt, args, err := query.New().Write("SELECT cardinality(CAST(:ids AS bigint[]))").Bind("ids", query.Scalar([]int64{1, 2, 3})).Build()
	must(t, err)
	var size int
	must(t, db.QueryRow(stmt, args...).Scan(&size))
	if size != 3 {
		t.Fatalf("native array size=%d", size)
	}
}

func TestQueryBuilderReportsBindingErrors(t *testing.T) {
	tests := []struct {
		builder *query.Builder
		want    error
	}{
		{query.New().Write(":id"), query.ErrMissingBind},
		{query.New().Bind("id", nil).Bind("id", 2), query.ErrDuplicateBind},
		{query.New().Bind(":id", 2), query.ErrInvalidName},
		{query.New().Write("IN (:ids)").Bind("ids", []int{}), query.ErrEmptyList},
		{query.New().Bind("unused", 1), query.ErrUnusedBind},
	}
	for _, test := range tests {
		stmt, args, err := test.builder.Build()
		if stmt != "" || args != nil || !errors.Is(err, test.want) {
			t.Fatalf("Build=%q, %#v, %v", stmt, args, err)
		}
		if text, err := test.builder.Preview(); text != "" || !errors.Is(err, test.want) {
			t.Fatalf("Preview=%q, %v", text, err)
		}
	}
	var zero query.Builder
	if sql, args, err := zero.Write("AND 1 = 1").Build(); err != nil || sql != "AND 1 = 1" || len(args) != 0 {
		t.Fatal("builder enforced SQL clause grammar")
	}
}
