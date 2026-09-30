package models_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	customvalues "generated.test/binarymodels/Values"
	"generated.test/binarytypes"
	values "generated.test/models/Values"
)

func TestNullableBinaryWrites(t *testing.T) {
	db := openDB(t)
	cases := []struct {
		name  string
		input *[]byte
		want  *[]byte
	}{
		{"nil pointer", nil, nil},
		{"nil slice", ptr([]byte(nil)), nil},
		{"empty slice", ptr([]byte{}), ptr([]byte{})},
		{"bytes", ptr([]byte{0, 1, 127, 128, 255}), ptr([]byte{0, 1, 127, 128, 255})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			tx, err := db.Begin()
			must(t, err)
			defer tx.Rollback()
			row := &values.Values{Clock: "00:00:00", JSONValue: json.RawMessage(`{}`), BinaryValue: test.input}
			must(t, row.Insert(tx))
			loaded := assertBinaryRoundTrip(t, tx, row, test.want)
			if row.BinaryValue != test.input {
				t.Fatal("Insert changed the caller's binary pointer")
			}

			// NULL reads as a nil pointer. Updating another field must work
			// without requiring the caller to reconstruct a typed binary NULL.
			loaded.Flag = !loaded.Flag
			must(t, loaded.Update(tx))
			assertBinaryRoundTrip(t, tx, loaded, test.want)

			// Exercise each target representation in Update, including transitions
			// from bytes to NULL and from NULL to an empty, non-NULL value.
			for _, next := range cases {
				t.Run("update/"+next.name, func(t *testing.T) {
					row.BinaryValue, row.Flag = next.input, !row.Flag
					must(t, row.Update(tx))
					assertBinaryRoundTrip(t, tx, row, next.want)
					if row.BinaryValue != next.input {
						t.Fatal("Update changed the caller's binary pointer")
					}
				})
			}
		})
	}
}

func TestBinaryMappingPreservesValuer(t *testing.T) {
	db := openDB(t)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	for _, content := range [][]byte{nil, {}, {0, 1, 127, 128, 255}} {
		calls := 0
		checked := &binaryValuerDB{DBTX: tx, t: t, calls: &calls}
		row := &customvalues.Values{Clock: "00:00:00", JSONValue: json.RawMessage(`{}`), BinaryValue: binarytypes.Value{Bytes: content, Calls: &calls}}
		must(t, row.Insert(checked))
		if calls == 0 {
			t.Fatal("Insert bypassed the binary Valuer")
		}
		calls = 0
		row.Flag = true
		must(t, row.Update(checked))
		if calls == 0 {
			t.Fatal("Update bypassed the binary Valuer")
		}
		var want *[]byte
		if content != nil {
			want = &content
		}
		assertBinaryRoundTrip(t, tx, &values.Values{ID: row.ID, Flag: true}, want)
		read, err := customvalues.SelectAll(tx, where("ID"), row.ID)
		must(t, err)
		if len(read) != 1 || !reflect.DeepEqual(read[0].BinaryValue.Bytes, content) {
			t.Fatal("custom binary Scanner changed the value")
		}

		sentinel := errors.New("custom binary conversion failed")
		row.BinaryValue.Err = sentinel
		for _, operation := range []func(customvalues.DBTX) error{row.Insert, row.Update} {
			calls = 0
			if err := operation(checked); !errors.Is(err, sentinel) {
				t.Fatalf("binary Valuer error was lost: %v", err)
			}
			if calls == 0 {
				t.Fatal("failed binary Valuer was bypassed")
			}
		}
	}
}

// Drivers may call Value more than once while choosing an encoding or falling
// back to a prepared statement. The generated layer must pass the original
// codec through, leaving those calls and any errors to database/sql/the driver.
type binaryValuerDB struct {
	customvalues.DBTX
	t     *testing.T
	calls *int
}

func (db *binaryValuerDB) check(args []any) {
	db.t.Helper()
	if *db.calls != 0 {
		db.t.Fatal("generated code called the custom binary Valuer before the driver")
	}
	for _, arg := range args {
		if value, ok := arg.(binarytypes.Value); ok && value.Calls == db.calls {
			return
		}
	}
	db.t.Fatal("custom binary argument was replaced")
}

func (db *binaryValuerDB) Exec(stmt string, args ...any) (sql.Result, error) {
	db.check(args)
	return db.DBTX.Exec(stmt, args...)
}

func (db *binaryValuerDB) QueryRow(stmt string, args ...any) *sql.Row {
	db.check(args)
	return db.DBTX.QueryRow(stmt, args...)
}

func assertBinaryRoundTrip(t *testing.T, tx *sql.Tx, row *values.Values, want *[]byte) *values.Values {
	t.Helper()
	// Independent SQL checks distinguish SQL NULL from an empty binary value.
	var isNull bool
	var raw *[]byte
	must(t, tx.QueryRow("SELECT CASE WHEN "+q("BinaryValue")+" IS NULL THEN 1 ELSE 0 END, "+q("BinaryValue")+" FROM "+table("Values")+" "+where("ID"), row.ID).Scan(&isNull, &raw))
	if isNull != (want == nil) || (raw == nil) != (want == nil) || (raw != nil && !bytes.Equal(*raw, *want)) {
		t.Fatalf("stored binary: NULL=%v bytes=%v, want %v", isNull, raw, want)
	}
	selected, err := values.SelectAll(tx, where("ID"), row.ID)
	must(t, err)
	queried, err := values.Query(tx, "SELECT * FROM "+table("Values")+" "+where("ID"), row.ID)
	must(t, err)
	projected, err := values.SelectCols(tx, "*", where("ID"), row.ID)
	must(t, err)
	if len(selected) != 1 || !reflect.DeepEqual(selected, queried) || !reflect.DeepEqual(selected, projected) {
		t.Fatal("binary read methods disagree")
	}
	if !reflect.DeepEqual(selected[0].BinaryValue, want) || selected[0].Flag != row.Flag {
		t.Fatalf("binary or updated flag differs: got %+v, want binary=%v flag=%v", selected[0], want, row.Flag)
	}
	partial, err := values.SelectCols(tx, q("BinaryValue")+", "+q("ID"), where("ID"), row.ID)
	must(t, err)
	if len(partial) != 1 || !reflect.DeepEqual(partial[0].BinaryValue, want) {
		t.Fatal("partial binary read differs")
	}
	return selected[0]
}
