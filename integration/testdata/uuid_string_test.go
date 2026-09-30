package models_test

import (
	"database/sql"
	"os"
	"reflect"
	"strings"
	"testing"

	uuidvalue "generated.test/models/UUIDValue"
	"generated.test/models/query"
)

// The same contract runs against UNIQUEIDENTIFIER, UUID, CHAR(36) and TEXT.
// No Go type override is installed on these generated models.
func TestUUIDStringDefaultRoundTrip(t *testing.T) {
	db := openDB(t)
	const canonical = "00112233-4455-6677-8899-aabbccddeeff"
	const optional = "10213243-5465-7687-98a9-bacbdcedfe0f"
	const zero = "00000000-0000-0000-0000-000000000000"
	for _, id := range []string{canonical, zero} {
		for _, value := range []*string{nil, ptr(strings.ToUpper(optional)), ptr(zero)} {
			t.Run(id+"/"+nullableUUIDLabel(value), func(t *testing.T) {
				tx, err := db.Begin()
				must(t, err)
				defer tx.Rollback()
				row := &uuidvalue.UUIDValue{ID: strings.ToUpper(id), Name: "insert", OptionalID: value}
				must(t, row.Insert(tx))
				want := &uuidvalue.UUIDValue{ID: id, Name: row.Name, OptionalID: lowerUUID(value)}
				assertUUIDStrings(t, tx, want)
				if row.ID != strings.ToUpper(id) || !reflect.DeepEqual(row.OptionalID, value) {
					t.Fatal("Insert mutated the caller's UUID fields")
				}

				// Also bind an uppercase primary key in Update: text databases
				// must locate the lowercase key written by Insert.
				for _, next := range []*string{ptr(strings.ToUpper(optional)), ptr(zero), nil} {
					row.Name, row.OptionalID = "update", next
					must(t, row.Update(tx))
					want.Name, want.OptionalID = row.Name, lowerUUID(next)
					assertUUIDStrings(t, tx, want)
					if row.ID != strings.ToUpper(id) || !reflect.DeepEqual(row.OptionalID, next) {
						t.Fatal("Update mutated the caller's UUID fields")
					}
				}

				loaded, err := uuidvalue.SelectAll(tx, where("ID"), id)
				must(t, err)
				loaded[0].Name = "read then write"
				must(t, loaded[0].Update(tx))
				want.Name = loaded[0].Name
				assertUUIDStrings(t, tx, want)

				stmt, args, err := query.New().Write("WHERE "+q("ID")+" IN (:ids)").Bind("ids", []string{id}).Build()
				must(t, err)
				built, err := uuidvalue.SelectAll(tx, stmt, args...)
				must(t, err)
				if len(built) != 1 || built[0].ID != id {
					t.Fatal("query builder did not preserve the UUID string")
				}
				exists, err := uuidvalue.Exists(tx, where("ID"), id)
				must(t, err)
				if !exists {
					t.Fatal("UUID lookup failed")
				}
				must(t, uuidvalue.Delete(tx, where("ID"), id))
				exists, err = uuidvalue.Exists(tx, where("ID"), id)
				must(t, err)
				if exists {
					t.Fatal("UUID delete failed")
				}
			})
		}
	}
}

func nullableUUIDLabel(value *string) string {
	if value == nil {
		return "NULL"
	}
	return *value
}

func lowerUUID(value *string) *string {
	if value == nil {
		return nil
	}
	return ptr(strings.ToLower(*value))
}

func assertUUIDStrings(t *testing.T, tx *sql.Tx, want *uuidvalue.UUIDValue) {
	t.Helper()
	// Check storage independently of the generated scanner. SQL Server's
	// native UUID is binary; its textual CAST uses uppercase hex digits.
	id, optional := q("ID"), q("OptionalID")
	if os.Getenv("SQLTOM_E2E_DIALECT") == "sqlserver" {
		id = "LOWER(CAST(" + id + " AS CHAR(36)))"
		optional = "LOWER(CAST(" + optional + " AS CHAR(36)))"
	}
	var storedID string
	var storedOptional *string
	must(t, tx.QueryRow("SELECT "+id+", "+optional+" FROM "+table("UUIDValue")+" "+where("ID"), want.ID).Scan(&storedID, &storedOptional))
	if storedID != want.ID || !reflect.DeepEqual(storedOptional, want.OptionalID) {
		t.Fatalf("raw UUID storage differs: ID=%q OptionalID=%v, want %+v", storedID, storedOptional, want)
	}
	for _, read := range []struct {
		name string
		call func() (uuidvalue.Rows, error)
	}{
		{"SelectAll", func() (uuidvalue.Rows, error) { return uuidvalue.SelectAll(tx, where("ID"), want.ID) }},
		{"SelectCols", func() (uuidvalue.Rows, error) { return uuidvalue.SelectCols(tx, "*", where("ID"), want.ID) }},
		{"Query/native", func() (uuidvalue.Rows, error) { return uuidvalue.Query(tx, uuidQuery(false), want.ID) }},
		{"Query/text", func() (uuidvalue.Rows, error) { return uuidvalue.Query(tx, uuidQuery(true), want.ID) }},
	} {
		rows, err := read.call()
		must(t, err)
		if len(rows) != 1 || !reflect.DeepEqual(rows[0], want) {
			t.Fatalf("%s UUID round trip: got %+v, want %+v", read.name, rows, want)
		}
	}
	partial, err := uuidvalue.SelectCols(tx, q("OptionalID")+", "+q("ID"), where("ID"), want.ID)
	must(t, err)
	if len(partial) != 1 || partial[0].ID != want.ID || !reflect.DeepEqual(partial[0].OptionalID, want.OptionalID) {
		t.Fatalf("partial UUID round trip: got %+v, want %+v", partial, want)
	}
}

func TestUUIDStringTextStorage(t *testing.T) {
	dialect := os.Getenv("SQLTOM_E2E_DIALECT")
	if dialect != "mysql" && dialect != "sqlite" {
		t.Skip("native UUID columns validate their own format; this checks text storage")
	}
	db := openDB(t)
	tx, err := db.Begin()
	must(t, err)
	defer tx.Rollback()
	const id = "00112233-4455-6677-8899-aabbccddeeff"
	const optional = "10213243-5465-7687-98a9-bacbdcedfe0f"
	// Data written externally can contain uppercase text. Read methods must
	// normalize it even when Insert/Update did not prepare the parameter.
	_, err = tx.Exec("INSERT INTO "+table("UUIDValue")+" ("+q("ID")+", "+q("Name")+", "+q("OptionalID")+") VALUES ("+placeholder(1)+", "+placeholder(2)+", "+placeholder(3)+")", strings.ToUpper(id), "external", strings.ToUpper(optional))
	must(t, err)
	selected, err := uuidvalue.SelectAll(tx, "")
	must(t, err)
	queried, err := uuidvalue.Query(tx, uuidQuery(false), strings.ToUpper(id))
	must(t, err)
	projected, err := uuidvalue.SelectCols(tx, "*", "")
	must(t, err)
	want := &uuidvalue.UUIDValue{ID: id, Name: "external", OptionalID: ptr(optional)}
	if len(selected) != 1 || !reflect.DeepEqual(selected[0], want) || !reflect.DeepEqual(selected, queried) || !reflect.DeepEqual(selected, projected) {
		t.Fatal("externally written UUIDs were not lowercased consistently")
	}
	// Empty text stays empty; it is neither a zero UUID nor SQL NULL.
	row := &uuidvalue.UUIDValue{ID: "", Name: "empty", OptionalID: ptr("")}
	must(t, row.Insert(tx))
	assertUUIDStrings(t, tx, row)
}
