package mysql

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect/testdb"
	"github.com/catamat/sqltom/internal/manifest"
)

const fakeMySQLDriverName = "sqltom-mysql-inspector-test"

var fakeMySQLDriver = testdb.Register(fakeMySQLDriverName)

func TestInspectBuildsMySQLManifest(t *testing.T) {
	fakeMySQLDriver.SetResponse(mysqlResponse([][]driver.Value{
		mysqlCatalogRow("MainDB", "MainDB", "Vehicle", "BASE TABLE", "ID", 1, false, "int", "int", true, true, false, false, false, 1),
		mysqlCatalogRow("MainDB", "MainDB", "Vehicle", "BASE TABLE", "Name", 2, true, "varchar", "varchar(100)", false, false, false, false, true, 0),
		mysqlCatalogRow("MainDB", "MainDB", "VehicleView", "VIEW", "ID", 1, false, "int", "int", false, false, false, false, false, 0),
	}))

	inspection, err := (&Backend{driverName: fakeMySQLDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err != nil {
		t.Fatal(err)
	}
	if fakeMySQLDriver.DSN() != "opaque-dsn" || fakeMySQLDriver.Queries() != 1 {
		t.Fatalf("driver state = dsn %q, queries %d", fakeMySQLDriver.DSN(), fakeMySQLDriver.Queries())
	}
	if inspection.DatabaseName != "MainDB" || len(inspection.Manifest.Tables) != 2 {
		t.Fatalf("inspection = %#v", inspection)
	}
	table := inspection.Manifest.Tables[0]
	if table.TableName != "Vehicle" || table.TableType != "BASE TABLE" || !table.IsManaged || len(table.Columns) != 2 {
		t.Fatalf("table = %#v", table)
	}
	if column := table.Columns[0]; column.ColumnName != "ID" || column.DataType != manifest.DataTypeInteger || !column.IsIdentity || !column.IsPrimaryKey || column.PrimaryKeyOrdinal != 1 {
		t.Fatalf("identity column = %#v", column)
	}
	if column := table.Columns[1]; column.ColumnName != "Name" || column.DataType != manifest.DataTypeString || !column.IsNullable || !column.HasDefault {
		t.Fatalf("name column = %#v", column)
	}
}

func TestInspectReturnsMySQLIdentityForEmptyCatalog(t *testing.T) {
	row := make([]driver.Value, 17)
	row[0], row[1] = "MainDB", "8.0.36"
	fakeMySQLDriver.SetResponse(mysqlResponse([][]driver.Value{row}))
	inspection, err := (&Backend{driverName: fakeMySQLDriverName}).Inspect(context.Background(), "dsn")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.DatabaseName != "MainDB" || len(inspection.Manifest.Tables) != 0 {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestInspectRejectsInvalidMySQLCatalogResults(t *testing.T) {
	valid := mysqlCatalogRow("MainDB", "MainDB", "Vehicle", "BASE TABLE", "ID", 1, false, "int", "int", true, true, false, false, false, 1)
	tests := []struct {
		name     string
		response testdb.Response
		message  string
	}{
		{name: "old version", response: mysqlResponse([][]driver.Value{withValue(valid, 1, "5.7.44")}), message: "minimum is 8.0"},
		{name: "MariaDB", response: mysqlResponse([][]driver.Value{withValue(valid, 1, "10.11.6-MariaDB")}), message: "MariaDB version"},
		{name: "inconsistent database", response: mysqlResponse([][]driver.Value{valid, withValue(valid, 0, "OtherDB")}), message: "inconsistent database names"},
		{name: "inconsistent version", response: mysqlResponse([][]driver.Value{valid, withValue(valid, 1, "8.4.1")}), message: "inconsistent server versions"},
		{name: "mismatched schema", response: mysqlResponse([][]driver.Value{withValue(valid, 3, "OtherDB")}), message: "does not match DatabaseName"},
		{name: "incomplete metadata", response: mysqlResponse([][]driver.Value{withValue(valid, 6, nil)}), message: "incomplete catalog metadata"},
		{name: "unsupported object", response: mysqlResponse([][]driver.Value{withValue(valid, 5, "SYSTEM VIEW")}), message: "unsupported table type"},
		{name: "ping error", response: testdb.Response{PingError: errors.New("ping failed")}, message: "connect to MySQL"},
		{name: "query error", response: testdb.Response{ExpectedQuery: catalogQuery, QueryError: errors.New("query failed")}, message: "query MySQL catalog"},
		{name: "iteration error", response: withIterationError(mysqlResponse([][]driver.Value{valid}), errors.New("iteration failed")), message: "iterate MySQL catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakeMySQLDriver.SetResponse(test.response)
			_, err := (&Backend{driverName: fakeMySQLDriverName}).Inspect(context.Background(), "dsn")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Inspect() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func mysqlResponse(rows [][]driver.Value) testdb.Response {
	return testdb.Response{
		ExpectedQuery: catalogQuery,
		Columns: []string{
			"database_name", "server_version", "table_catalog", "table_schema", "table_name", "table_type",
			"column_name", "ordinal_position", "is_nullable", "data_type", "column_type", "is_identity",
			"is_primary_key", "is_computed", "is_generated_always", "has_default", "primary_key_ordinal",
		},
		Rows: rows,
	}
}

func mysqlCatalogRow(database, schema, table, tableType, column string, ordinal int64, nullable bool, dataType, columnType string, identity, primary, computed, generated, defaulted bool, primaryOrdinal int64) []driver.Value {
	return []driver.Value{database, "8.0.36", "def", schema, table, tableType, column, ordinal, nullable, dataType, columnType, identity, primary, computed, generated, defaulted, primaryOrdinal}
}

func withValue(row []driver.Value, index int, value driver.Value) []driver.Value {
	result := append([]driver.Value(nil), row...)
	result[index] = value
	return result
}

func withIterationError(response testdb.Response, err error) testdb.Response {
	response.IterationError = err
	return response
}

func TestCanonicalDataTypeMapsMySQLTypes(t *testing.T) {
	tests := []struct {
		dataType   string
		columnType string
		want       string
	}{
		{dataType: "bigint", columnType: "bigint", want: manifest.DataTypeBigInt},
		{dataType: "bigint", columnType: "bigint unsigned", want: manifest.DataTypeUnsignedBigInt},
		{dataType: "int", columnType: "int unsigned", want: manifest.DataTypeUnsignedInteger},
		{dataType: "smallint", columnType: "smallint unsigned", want: manifest.DataTypeUnsignedSmallInt},
		{dataType: "tinyint", columnType: "tinyint unsigned", want: manifest.DataTypeUnsignedTinyInt},
		{dataType: "bit", columnType: "bit(1)", want: manifest.DataTypeBoolean},
		{dataType: "bit", columnType: "bit(8)", want: manifest.DataTypeBinary},
		{dataType: "longblob", columnType: "longblob", want: manifest.DataTypeBinary},
		{dataType: "enum", columnType: "enum('new','done')", want: manifest.DataTypeString},
		{dataType: "timestamp", columnType: "timestamp", want: manifest.DataTypeDateTime},
		{dataType: "json", columnType: "json", want: manifest.DataTypeJSON},
		{dataType: "geometry", columnType: "geometry", want: "geometry"},
		{dataType: "GEOMETRY", columnType: "Geometry", want: "Geometry"},
		{dataType: "custom", columnType: "CustomType(17)", want: "CustomType(17)"},
	}
	for _, test := range tests {
		if got := canonicalDataType(test.dataType, test.columnType); got != test.want {
			t.Errorf("canonicalDataType(%q, %q) = %q, want %q", test.dataType, test.columnType, got, test.want)
		}
	}
}

func TestCatalogQueryCapturesMySQLMetadata(t *testing.T) {
	for _, fragment := range []string{
		"COALESCE(DATABASE(), '')",
		"VERSION() AS server_version",
		"table_info.table_type IN ('BASE TABLE', 'VIEW')",
		"LOCATE('auto_increment', LOWER(column_info.extra))",
		"column_info.generation_expression <> ''",
		"primary_key.constraint_name = 'PRIMARY'",
		"table_info.table_schema = DATABASE()",
	} {
		if !strings.Contains(catalogQuery, fragment) {
			t.Errorf("catalogQuery does not contain %q", fragment)
		}
	}
	if strings.Contains(catalogQuery, "LOCATE('generated', LOWER(column_info.extra))") {
		t.Error("catalogQuery treats DEFAULT_GENERATED as a computed column")
	}
}
