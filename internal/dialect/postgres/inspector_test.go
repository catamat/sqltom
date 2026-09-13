package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect/testdb"
	"github.com/catamat/sqltom/internal/manifest"
)

const fakePostgresDriverName = "sqltom-postgres-inspector-test"

var fakePostgresDriver = testdb.Register(fakePostgresDriverName)

func TestInspectBuildsPostgreSQLManifest(t *testing.T) {
	fakePostgresDriver.SetResponse(postgresResponse([][]driver.Value{
		postgresCatalogRow("MainDB", "public", "Vehicle", "BASE TABLE", "ID", 1, false, "integer", "pg_catalog", "int4", nil, nil, true, true, false, false, false, 1),
		postgresCatalogRow("MainDB", "public", "Vehicle", "BASE TABLE", "State", 2, true, "USER-DEFINED", "business", "state", "business", "state_domain", false, false, false, false, true, 0),
		postgresCatalogRow("MainDB", "public", "VehicleView", "VIEW", "ID", 1, false, "integer", "pg_catalog", "int4", nil, nil, false, false, false, false, false, 0),
	}))

	inspection, err := (&Backend{driverName: fakePostgresDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err != nil {
		t.Fatal(err)
	}
	if fakePostgresDriver.DSN() != "opaque-dsn" || fakePostgresDriver.Queries() != 1 {
		t.Fatalf("driver state = dsn %q, queries %d", fakePostgresDriver.DSN(), fakePostgresDriver.Queries())
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
	if column := table.Columns[1]; column.ColumnName != "State" || column.DataType != `"business"."state_domain"` || !column.IsNullable || !column.HasDefault {
		t.Fatalf("domain column = %#v", column)
	}
}

func TestInspectReturnsPostgreSQLIdentityForEmptyCatalog(t *testing.T) {
	row := make([]driver.Value, 20)
	row[0], row[1] = "MainDB", "17.1"
	fakePostgresDriver.SetResponse(postgresResponse([][]driver.Value{row}))
	inspection, err := (&Backend{driverName: fakePostgresDriverName}).Inspect(context.Background(), "dsn")
	if err != nil {
		t.Fatal(err)
	}
	if inspection.DatabaseName != "MainDB" || len(inspection.Manifest.Tables) != 0 {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestInspectRejectsInvalidPostgreSQLCatalogResults(t *testing.T) {
	valid := postgresCatalogRow("MainDB", "public", "Vehicle", "BASE TABLE", "ID", 1, false, "integer", "pg_catalog", "int4", nil, nil, true, true, false, false, false, 1)
	tests := []struct {
		name     string
		response testdb.Response
		message  string
	}{
		{name: "old version", response: postgresResponse([][]driver.Value{postgresWithValue(valid, 1, "11.22")}), message: "minimum is 12.0"},
		{name: "inconsistent database", response: postgresResponse([][]driver.Value{valid, postgresWithValue(valid, 0, "OtherDB")}), message: "inconsistent database names"},
		{name: "inconsistent version", response: postgresResponse([][]driver.Value{valid, postgresWithValue(valid, 1, "16.2")}), message: "inconsistent server versions"},
		{name: "mismatched catalog", response: postgresResponse([][]driver.Value{postgresWithValue(valid, 2, "OtherDB")}), message: "does not match DatabaseName"},
		{name: "incomplete metadata", response: postgresResponse([][]driver.Value{postgresWithValue(valid, 6, nil)}), message: "incomplete catalog metadata"},
		{name: "incomplete domain", response: postgresResponse([][]driver.Value{postgresWithValue(valid, 12, "business")}), message: "incomplete domain metadata"},
		{name: "unsupported object", response: postgresResponse([][]driver.Value{postgresWithValue(valid, 5, "FOREIGN TABLE")}), message: "unsupported table type"},
		{name: "ping error", response: testdb.Response{PingError: errors.New("ping failed")}, message: "connect to PostgreSQL"},
		{name: "query error", response: testdb.Response{ExpectedQuery: catalogQuery, QueryError: errors.New("query failed")}, message: "query PostgreSQL catalog"},
		{name: "iteration error", response: postgresWithIterationError(postgresResponse([][]driver.Value{valid}), errors.New("iteration failed")), message: "iterate PostgreSQL catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fakePostgresDriver.SetResponse(test.response)
			_, err := (&Backend{driverName: fakePostgresDriverName}).Inspect(context.Background(), "dsn")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Inspect() error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func postgresResponse(rows [][]driver.Value) testdb.Response {
	return testdb.Response{
		ExpectedQuery: catalogQuery,
		Columns: []string{
			"database_name", "server_version", "table_catalog", "table_schema", "table_name", "table_type",
			"column_name", "ordinal_position", "is_nullable", "data_type", "udt_schema", "udt_name",
			"domain_schema", "domain_name", "is_identity", "is_primary_key", "is_computed",
			"is_generated_always", "has_default", "primary_key_ordinal",
		},
		Rows: rows,
	}
}

func postgresCatalogRow(database, schema, table, tableType, column string, ordinal int64, nullable bool, dataType, udtSchema, udtName string, domainSchema, domainName driver.Value, identity, primary, computed, generated, defaulted bool, primaryOrdinal int64) []driver.Value {
	return []driver.Value{database, "17.1", database, schema, table, tableType, column, ordinal, nullable, dataType, udtSchema, udtName, domainSchema, domainName, identity, primary, computed, generated, defaulted, primaryOrdinal}
}

func postgresWithValue(row []driver.Value, index int, value driver.Value) []driver.Value {
	result := append([]driver.Value(nil), row...)
	result[index] = value
	return result
}

func postgresWithIterationError(response testdb.Response, err error) testdb.Response {
	response.IterationError = err
	return response
}

func TestCanonicalDataTypeMapsPostgreSQLTypes(t *testing.T) {
	tests := []struct {
		dataType     string
		schema       string
		name         string
		domainSchema string
		domainName   string
		want         string
	}{
		{dataType: "bigint", want: manifest.DataTypeBigInt},
		{dataType: "bytea", want: manifest.DataTypeBinary},
		{dataType: "boolean", want: manifest.DataTypeBoolean},
		{dataType: "character varying", want: manifest.DataTypeString},
		{dataType: "timestamp without time zone", want: manifest.DataTypeDateTime},
		{dataType: "timestamp with time zone", want: manifest.DataTypeDateTimeOffset},
		{dataType: "numeric", want: manifest.DataTypeDecimal},
		{dataType: "double precision", want: manifest.DataTypeDouble},
		{dataType: "jsonb", want: manifest.DataTypeJSON},
		{dataType: "uuid", want: manifest.DataTypeUUID},
		{dataType: "ARRAY", schema: "pg_catalog", name: "_int4", want: `"pg_catalog"."_int4"`},
		{dataType: "USER-DEFINED", schema: `custom"schema`, name: `status"type`, want: `"custom""schema"."status""type"`},
		{dataType: "integer", schema: "pg_catalog", name: "int4", domainSchema: "business", domainName: "positive_id", want: `"business"."positive_id"`},
		{dataType: "interval", want: "interval"},
	}
	for _, test := range tests {
		if got := canonicalDataType(test.dataType, test.schema, test.name, test.domainSchema, test.domainName); got != test.want {
			t.Errorf("canonicalDataType(%q, %q, %q, %q, %q) = %q, want %q", test.dataType, test.schema, test.name, test.domainSchema, test.domainName, got, test.want)
		}
	}
}

func TestCatalogQueryCapturesPostgreSQLMetadata(t *testing.T) {
	for _, fragment := range []string{
		"current_database()",
		"current_setting('server_version')",
		"table_info.table_type IN ('BASE TABLE', 'VIEW')",
		"column_info.is_identity = 'YES'",
		"column_info.column_default LIKE 'nextval(%'",
		"LEFT JOIN information_schema.domains AS domain_info",
		"domain_info.domain_catalog = column_info.domain_catalog",
		"domain_info.domain_default IS NOT NULL",
		"column_info.is_generated <> 'NEVER'",
		"column_info.domain_schema",
		"column_info.domain_name",
		"constraint_info.constraint_type = 'PRIMARY KEY'",
		"table_info.table_schema NOT IN ('information_schema', 'pg_catalog')",
	} {
		if !strings.Contains(catalogQuery, fragment) {
			t.Errorf("catalogQuery does not contain %q", fragment)
		}
	}
}
