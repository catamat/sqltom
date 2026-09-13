package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect/testdb"
	"github.com/catamat/sqltom/internal/manifest"
)

const fakeSQLiteDriverName = "sqltom-sqlite-inspector-test"

var fakeSQLiteDriver = testdb.Register(fakeSQLiteDriverName)

func TestInspectSQLiteCatalog(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "fleet.db")
	db, err := sql.Open("sqlite", filename)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE Vehicle (
            ID INTEGER PRIMARY KEY AUTOINCREMENT,
            Name TEXT NOT NULL DEFAULT 'unknown',
            Payload JSON,
            Flexible ANY,
            Total DECIMAL(12,2),
            Generated TEXT GENERATED ALWAYS AS (Name || '-generated') STORED
        )`,
		`CREATE TABLE Composite (
            LeftID INTEGER,
            RightID INTEGER,
            PRIMARY KEY (LeftID, RightID)
        ) WITHOUT ROWID`,
		`CREATE VIEW VehicleNames AS SELECT ID, Name FROM Vehicle`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	inspection, err := New().Inspect(context.Background(), filename)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.DatabaseName != "fleet.db" || inspection.Manifest.DatabaseName != "fleet.db" {
		t.Fatalf("database identity = %q / %q", inspection.DatabaseName, inspection.Manifest.DatabaseName)
	}
	vehicle := findTable(t, inspection.Manifest, "main", "Vehicle")
	if vehicle.TableType != "BASE TABLE" || len(vehicle.Columns) != 6 {
		t.Fatalf("Vehicle metadata = %#v", vehicle)
	}
	id := findColumn(t, vehicle, "ID")
	if !id.IsIdentity || !id.IsPrimaryKey || id.PrimaryKeyOrdinal != 1 || id.IsNullable {
		t.Fatalf("ID metadata = %#v", id)
	}
	name := findColumn(t, vehicle, "Name")
	if name.DataType != manifest.DataTypeString || name.IsNullable || !name.HasDefault {
		t.Fatalf("Name metadata = %#v", name)
	}
	if payload := findColumn(t, vehicle, "Payload"); payload.DataType != manifest.DataTypeJSON {
		t.Fatalf("Payload metadata = %#v", payload)
	}
	if flexible := findColumn(t, vehicle, "Flexible"); flexible.DataType != manifest.DataTypeAny {
		t.Fatalf("Flexible metadata = %#v", flexible)
	}
	generated := findColumn(t, vehicle, "Generated")
	if !generated.IsComputed || !generated.IsGeneratedAlways {
		t.Fatalf("Generated metadata = %#v", generated)
	}

	composite := findTable(t, inspection.Manifest, "main", "Composite")
	left := findColumn(t, composite, "LeftID")
	right := findColumn(t, composite, "RightID")
	if left.IsIdentity || right.IsIdentity || left.PrimaryKeyOrdinal != 1 || right.PrimaryKeyOrdinal != 2 {
		t.Fatalf("composite key metadata = %#v / %#v", left, right)
	}
	view := findTable(t, inspection.Manifest, "main", "VehicleNames")
	if view.TableType != "VIEW" {
		t.Fatalf("view metadata = %#v", view)
	}
	for _, table := range inspection.Manifest.Tables {
		if table.TableName == "sqlite_sequence" {
			t.Fatal("system table sqlite_sequence was included")
		}
	}
}

func TestInspectDoesNotCreateMissingSQLiteDatabase(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "missing.db")

	if _, err := New().Inspect(context.Background(), filename); err == nil {
		t.Fatal("Inspect() unexpectedly accepted a missing SQLite database")
	}
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection created the missing SQLite database: %v", err)
	}
}

func TestInspectAllowsExplicitInMemorySQLiteDatabase(t *testing.T) {
	inspection, err := New().Inspect(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.DatabaseName != "main" || len(inspection.Manifest.Tables) != 0 {
		t.Fatalf("in-memory inspection = %#v", inspection)
	}
}

func TestInspectRejectsInvalidSQLiteCatalogResults(t *testing.T) {
	version := sqliteResponse(versionQuery, []string{"sqlite_version()"}, [][]driver.Value{{"3.51.2"}})
	databases := sqliteResponse(databaseListQuery, []string{"seq", "name", "file"}, [][]driver.Value{{int64(0), "main", ""}})
	objectsQuery := `PRAGMA "main".table_list`
	objects := sqliteResponse(objectsQuery, []string{"schema", "name", "type", "ncol", "wr", "strict"}, [][]driver.Value{{"main", "Vehicle", "table", int64(1), int64(0), int64(0)}})
	columnsQuery := `PRAGMA "main".table_xinfo('Vehicle')`
	columns := sqliteResponse(columnsQuery, []string{"cid", "name", "type", "notnull", "dflt_value", "pk", "hidden"}, [][]driver.Value{{int64(0), "ID", "INTEGER", int64(0), nil, int64(1), int64(0)}})

	tests := []struct {
		name      string
		responses []testdb.Response
		message   string
	}{
		{name: "ping error", responses: []testdb.Response{{PingError: errors.New("ping failed")}}, message: "connect to SQLite"},
		{name: "version query error", responses: []testdb.Response{{ExpectedQuery: versionQuery, QueryError: errors.New("query failed")}}, message: "query SQLite version"},
		{name: "old version", responses: []testdb.Response{sqliteResponse(versionQuery, []string{"sqlite_version()"}, [][]driver.Value{{"3.36.0"}})}, message: "minimum is 3.37"},
		{name: "database list query error", responses: []testdb.Response{version, {ExpectedQuery: databaseListQuery, QueryError: errors.New("query failed")}}, message: "query SQLite database list"},
		{name: "database list iteration error", responses: []testdb.Response{version, withSQLiteIterationError(databases, errors.New("iteration failed"))}, message: "iterate SQLite database list"},
		{name: "object query error", responses: []testdb.Response{version, databases, {ExpectedQuery: objectsQuery, QueryError: errors.New("query failed")}}, message: "query SQLite objects"},
		{name: "object iteration error", responses: []testdb.Response{version, databases, withSQLiteIterationError(objects, errors.New("iteration failed"))}, message: "iterate SQLite objects"},
		{name: "column query error", responses: []testdb.Response{version, databases, objects, {ExpectedQuery: columnsQuery, QueryError: errors.New("query failed")}}, message: "query SQLite columns"},
		{name: "column iteration error", responses: []testdb.Response{version, databases, objects, withSQLiteIterationError(columns, errors.New("iteration failed"))}, message: "iterate SQLite columns"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeSQLiteDriver.SetResponses(test.responses...)
			_, err := (&Backend{driverName: fakeSQLiteDriverName}).Inspect(context.Background(), "file::memory:")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Inspect() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func sqliteResponse(query string, columns []string, rows [][]driver.Value) testdb.Response {
	return testdb.Response{ExpectedQuery: query, Columns: columns, Rows: rows}
}

func withSQLiteIterationError(response testdb.Response, err error) testdb.Response {
	response.IterationError = err
	return response
}

func TestCanonicalDataTypeMapsSQLiteAffinities(t *testing.T) {
	tests := map[string]string{
		"":                 manifest.DataTypeAny,
		"ANY":              manifest.DataTypeAny,
		"VARCHAR(100)":     manifest.DataTypeString,
		"BLOB":             manifest.DataTypeBinary,
		"DOUBLE PRECISION": manifest.DataTypeDouble,
		"BOOLEAN":          manifest.DataTypeBoolean,
		"DATETIME":         manifest.DataTypeDateTime,
		"JSON":             manifest.DataTypeJSON,
		"UUID":             manifest.DataTypeUUID,
		"custom_type":      "custom_type",
	}
	for native, want := range tests {
		if got := canonicalDataType(native); got != want {
			t.Errorf("canonicalDataType(%q) = %q, want %q", native, got, want)
		}
	}
}

func TestSQLiteQuoting(t *testing.T) {
	if got, want := quoteSQLiteIdentifier(`a"b`), `"a""b"`; got != want {
		t.Fatalf("quoteSQLiteIdentifier() = %q, want %q", got, want)
	}
	if got, want := quoteSQLiteString(`a'b`), `'a''b'`; got != want {
		t.Fatalf("quoteSQLiteString() = %q, want %q", got, want)
	}
}

func findTable(t *testing.T, document *manifest.Manifest, schema, name string) manifest.Table {
	t.Helper()
	for _, table := range document.Tables {
		if table.TableSchema == schema && table.TableName == name {
			return table
		}
	}
	t.Fatalf("table %s.%s not found", schema, name)
	return manifest.Table{}
}

func findColumn(t *testing.T, table manifest.Table, name string) manifest.Column {
	t.Helper()
	for _, column := range table.Columns {
		if column.ColumnName == name {
			return column
		}
	}
	t.Fatalf("column %s.%s not found", table.TableName, name)
	return manifest.Column{}
}
