package sqlserver

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect/testdb"
	"github.com/catamat/sqltom/internal/manifest"
)

const fakeDriverName = "sqltom-sqlserver-test"

var registeredFakeDriver = testdb.Register(fakeDriverName)

func TestInspectBuildsManifestFromOneCatalogQuery(t *testing.T) {
	registeredFakeDriver.SetResponse(sqlserverResponse([][]driver.Value{
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "RecordID", 1, false, "int", true, true, false, false, false, false, 1),
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "Total", 2, true, "[moneytypes].[Amount]", false, false, false, false, false, true, 0),
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "Stamp", 3, false, "timestamp", false, false, false, false, true, false, 0),
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "PeriodStart", 4, false, "datetime2", false, false, false, true, false, false, 0),
		catalogRow("MainDB", "audit", "Vehicle", "VIEW", "RecordID", 1, false, "int", false, false, true, false, false, false, 0),
	}))
	inspection, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err != nil {
		t.Fatal(err)
	}
	if registeredFakeDriver.DSN() != "opaque-dsn" || registeredFakeDriver.Queries() != 1 {
		t.Fatalf("driver state = dsn %q, queries %d", registeredFakeDriver.DSN(), registeredFakeDriver.Queries())
	}
	if inspection.DatabaseName != "MainDB" || inspection.Manifest.DatabaseName != "MainDB" || len(inspection.Manifest.Tables) != 2 {
		t.Fatalf("inspection = %#v", inspection)
	}
	for _, table := range inspection.Manifest.Tables {
		if !table.IsManaged {
			t.Fatalf("unmanaged table: %#v", table)
		}
	}
	view, table := inspection.Manifest.Tables[0], inspection.Manifest.Tables[1]
	if view.TableCatalog != "MainDB" || view.TableSchema != "audit" || view.TableName != "Vehicle" || view.TableType != "VIEW" {
		t.Fatalf("view = %#v", view)
	}
	if table.TableCatalog != "MainDB" || table.TableSchema != "dbo" || table.TableName != "Vehicle" || table.TableType != "BASE TABLE" || len(table.Columns) != 4 {
		t.Fatalf("table = %#v", table)
	}
	if c := table.Columns[0]; c.ColumnName != "RecordID" || c.DataType != manifest.DataTypeInteger || !c.IsIdentity || !c.IsPrimaryKey || c.PrimaryKeyOrdinal != 1 || c.IsNullable {
		t.Fatalf("primary key = %#v", c)
	}
	if c := table.Columns[1]; c.ColumnName != "Total" || c.DataType != "[moneytypes].[Amount]" || !c.IsNullable || !c.HasDefault {
		t.Fatalf("UDT/default = %#v", c)
	}
	if c := table.Columns[2]; c.DataType != manifest.DataTypeRowVersion || !c.IsRowVersion || c.IsComputed || c.IsGeneratedAlways {
		t.Fatalf("rowversion = %#v", c)
	}
	if c := table.Columns[3]; c.DataType != manifest.DataTypeDateTime || c.IsComputed || !c.IsGeneratedAlways {
		t.Fatalf("generated column = %#v", c)
	}
}

func TestInspectReturnsDatabaseIdentityForEmptyVisibleCatalog(t *testing.T) {
	row := make([]driver.Value, 18)
	row[0], row[1] = "MainDB", "16.0.1000.6"
	registeredFakeDriver.SetResponse(sqlserverResponse([][]driver.Value{row}))
	inspection, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "dsn")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.DatabaseName != "MainDB" || len(inspection.Manifest.Tables) != 0 || registeredFakeDriver.Queries() != 1 {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestInspectRejectsInvalidSQLServerCatalogResults(t *testing.T) {
	valid := catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "ID", 1, false, "int", true, true, false, false, false, false, 1)
	with := func(index int, value driver.Value) []driver.Value {
		row := append([]driver.Value(nil), valid...)
		row[index] = value
		return row
	}
	iteration := sqlserverResponse([][]driver.Value{valid})
	iteration.IterationError = errors.New("iteration failed")
	tests := []struct {
		name     string
		response testdb.Response
		message  string
	}{
		{"old version", sqlserverResponse([][]driver.Value{with(1, "12.0.6024.0")}), "minimum is 13.0"},
		{"inconsistent version", sqlserverResponse([][]driver.Value{valid, with(1, "15.0.2000.5")}), "inconsistent server versions"},
		{"inconsistent database", sqlserverResponse([][]driver.Value{valid, with(0, "OtherDB")}), "inconsistent database names"},
		{"mismatched catalog", sqlserverResponse([][]driver.Value{with(2, "OtherDB")}), "does not match DatabaseName"},
		{"incomplete metadata", sqlserverResponse([][]driver.Value{with(6, nil)}), "incomplete catalog metadata"},
		{"unsupported object", sqlserverResponse([][]driver.Value{with(5, "EXTERNAL TABLE")}), "unsupported table type"},
		{"scan error", sqlserverResponse([][]driver.Value{with(7, "invalid ordinal")}), "scan SQL Server catalog"},
		{"ping error", testdb.Response{PingError: errors.New("ping failed")}, "connect to SQL Server"},
		{"query error", testdb.Response{ExpectedQuery: catalogQuery, QueryError: errors.New("query failed")}, "query SQL Server catalog"},
		{"iteration error", iteration, "iterate SQL Server catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registeredFakeDriver.SetResponse(test.response)
			_, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "dsn")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Inspect() error = %v, want %q", err, test.message)
			}
		})
	}
}

func sqlserverResponse(rows [][]driver.Value) testdb.Response {
	rows = testdb.WithPrimaryKeyCounts(rows)
	return testdb.Response{ExpectedQuery: catalogQuery, Rows: rows, Columns: []string{
		"DATABASE_NAME", "SERVER_VERSION", "TABLE_CATALOG", "TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE",
		"COLUMN_NAME", "ORDINAL_POSITION", "IS_NULLABLE", "DATA_TYPE", "TYPE_PRECISION", "IS_IDENTITY",
		"IS_PRIMARY_KEY", "IS_COMPUTED", "IS_GENERATED_ALWAYS", "IS_ROW_VERSION", "HAS_DEFAULT", "PRIMARY_KEY_ORDINAL", "PRIMARY_KEY_COUNT",
	}}
}

func TestValidateInspectionManifestRequiresSQLServerSourceIdentity(t *testing.T) {
	tests := []struct {
		name    string
		m       *manifest.Manifest
		message string
	}{
		{name: "nil", message: "manifest is nil"},
		{
			name: "catalog",
			m: &manifest.Manifest{
				DatabaseName: "MainDB",
				Tables:       []manifest.Table{{TableSchema: "dbo"}},
			},
			message: "must include TableCatalog and TableSchema",
		},
		{
			name: "schema",
			m: &manifest.Manifest{
				DatabaseName: "MainDB",
				Tables:       []manifest.Table{{TableCatalog: "MainDB"}},
			},
			message: "must include TableCatalog and TableSchema",
		},
		{
			name: "database",
			m: &manifest.Manifest{
				DatabaseName: "MainDB",
				Tables:       []manifest.Table{{TableCatalog: "OtherDB", TableSchema: "dbo"}},
			},
			message: `TableCatalog "OtherDB" does not match DatabaseName "MainDB"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateInspectionManifest(test.m)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("validateInspectionManifest() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestCanonicalDataTypeMapsSQLServerTypesToManifestVocabulary(t *testing.T) {
	tests := []struct {
		name         string
		nativeType   string
		precision    int64
		isRowVersion bool
		want         string
	}{
		{name: "bigint", nativeType: "bigint", want: "bigint"},
		{name: "binary", nativeType: "binary", want: "binary"},
		{name: "image", nativeType: "image", want: "binary"},
		{name: "varbinary", nativeType: "varbinary", want: "binary"},
		{name: "bit", nativeType: "bit", want: "boolean"},
		{name: "char", nativeType: "char", want: "string"},
		{name: "nchar", nativeType: "nchar", want: "string"},
		{name: "ntext", nativeType: "ntext", want: "string"},
		{name: "nvarchar", nativeType: "nvarchar", want: "string"},
		{name: "sysname", nativeType: "sysname", want: "string"},
		{name: "text", nativeType: "text", want: "string"},
		{name: "varchar", nativeType: "varchar", want: "string"},
		{name: "date", nativeType: "date", want: "date"},
		{name: "datetime", nativeType: "datetime", want: "datetime"},
		{name: "datetime2", nativeType: "datetime2", want: "datetime"},
		{name: "smalldatetime", nativeType: "smalldatetime", want: "datetime"},
		{name: "datetimeoffset", nativeType: "datetimeoffset", want: "datetimeoffset"},
		{name: "decimal", nativeType: "decimal", want: "decimal"},
		{name: "numeric", nativeType: "numeric", want: "decimal"},
		{name: "float 24", nativeType: "float", precision: 24, want: "real"},
		{name: "float 25", nativeType: "float", precision: 25, want: "double"},
		{name: "float default", nativeType: "float", precision: 53, want: "double"},
		{name: "int", nativeType: "int", want: "integer"},
		{name: "json", nativeType: "json", want: "json"},
		{name: "money", nativeType: "money", want: "money"},
		{name: "smallmoney", nativeType: "smallmoney", want: "money"},
		{name: "real", nativeType: "real", want: "real"},
		{name: "smallint", nativeType: "smallint", want: "smallint"},
		{name: "time", nativeType: "time", want: "time"},
		{name: "tinyint", nativeType: "tinyint", want: "tinyint"},
		{name: "uuid", nativeType: "uniqueidentifier", want: "uuid"},
		{name: "sql variant", nativeType: "sql_variant", want: "variant"},
		{name: "xml", nativeType: "xml", want: "xml"},
		{name: "timestamp flagged as rowversion", nativeType: "timestamp", isRowVersion: true, want: "rowversion"},
		{name: "alias flagged as rowversion", nativeType: "[dbo].[Version]", isRowVersion: true, want: "rowversion"},
		{name: "unflagged timestamp remains exact", nativeType: "timestamp", want: "timestamp"},
		{name: "UDT remains exact", nativeType: "[moneytypes].[Amount]", want: "[moneytypes].[Amount]"},
		{name: "unknown remains exact", nativeType: "Geography", want: "Geography"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := canonicalDataType(test.nativeType, test.precision, test.isRowVersion); got != test.want {
				t.Fatalf("canonicalDataType(%q, %d, %t) = %q, want %q", test.nativeType, test.precision, test.isRowVersion, got, test.want)
			}
		})
	}
}

func TestCatalogQueryEnforcesSupportedSQLServerMetadata(t *testing.T) {
	for _, fragment := range []string{
		"CONVERT(nvarchar(128), DB_NAME()) AS DATABASE_NAME",
		"SERVERPROPERTY('ProductVersion')",
		"object_info.type IN ('U', 'V')",
		"object_info.type <> 'ET'",
		"column_info.is_hidden = 0",
		"column_info.precision AS TYPE_PRECISION",
		"type_info.is_user_defined = 1",
		"QUOTENAME(type_schema_info.name) + N'.' + QUOTENAME(type_info.name)",
		"column_info.generated_always_type = 0",
		"column_info.default_object_id <> 0 OR type_info.default_object_id <> 0",
		"object_info.type = 'U'",
		"schema_info.name = N'dbo'",
		"object_info.name = N'sysdiagrams'",
		"MSSQL[_]DroppedLedgerTable[_]%",
		"MSSQL[_]DroppedLedgerHistory[_]%",
		"MSSQL[_]DroppedLedgerView[_]%",
		"LEFT JOIN (",
	} {
		if !strings.Contains(catalogQuery, fragment) {
			t.Errorf("catalogQuery does not contain %q", fragment)
		}
	}
}

func catalogRow(
	databaseName, schema, table, tableType, column string,
	ordinal int64,
	nullable bool,
	dataType string,
	identity, primaryKey, computed, generatedAlways, rowVersion, hasDefault bool,
	primaryKeyOrdinal int64,
) []driver.Value {
	return []driver.Value{
		databaseName,
		"16.0.1000.6",
		databaseName,
		schema,
		table,
		tableType,
		column,
		ordinal,
		nullable,
		dataType,
		int64(0),
		identity,
		primaryKey,
		computed,
		generatedAlways,
		rowVersion,
		hasDefault,
		primaryKeyOrdinal,
	}
}

func TestInspectRejectsIncompletePrimaryKey(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprintf("entire_key_hidden=%v", hidden), func(t *testing.T) {
			row := []driver.Value{"MainDB", "16.0.1000.6", "MainDB", "dbo", "Vehicle", "BASE TABLE", "ID", int64(1), false, "int", int64(10), false, true, false, false, false, false, int64(1)}
			if hidden {
				row[12] = false
				row[len(row)-1] = int64(0)
			}
			response := sqlserverResponse([][]driver.Value{row})
			response.Rows[0][len(row)] = int64(2)
			registeredFakeDriver.SetResponse(response)
			inspection, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "dsn")
			if inspection != nil || err == nil || !strings.Contains(err.Error(), "incomplete primary key metadata") {
				t.Fatalf("Inspect = %#v, %v; want incomplete-key error", inspection, err)
			}
		})
	}
}
