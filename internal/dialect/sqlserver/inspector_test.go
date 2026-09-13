package sqlserver

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/catamat/sqltom/internal/manifest"
)

const fakeDriverName = "sqltom-sqlserver-test"

var registeredFakeDriver = &fakeDriver{}

func init() {
	sql.Register(fakeDriverName, registeredFakeDriver)
}

func TestInspectBuildsManifestFromOneCatalogQuery(t *testing.T) {
	registeredFakeDriver.reset()
	t.Cleanup(registeredFakeDriver.reset)

	inspection, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got := registeredFakeDriver.openedDSN(); got != "opaque-dsn" {
		t.Fatalf("driver DSN = %q, want opaque-dsn", got)
	}
	if inspection.DatabaseName != "MainDB" || inspection.Manifest.DatabaseName != "MainDB" {
		t.Fatalf("database identity = inspection %q, manifest %q", inspection.DatabaseName, inspection.Manifest.DatabaseName)
	}
	if len(inspection.Manifest.Tables) != 2 {
		t.Fatalf("len(Tables) = %d, want 2", len(inspection.Manifest.Tables))
	}
	for _, table := range inspection.Manifest.Tables {
		if !table.IsManaged {
			t.Fatalf("inspected table is not managed by default: %#v", table)
		}
	}

	auditView := inspection.Manifest.Tables[0]
	if auditView.TableCatalog != "MainDB" || auditView.TableSchema != "audit" || auditView.TableName != "Vehicle" || auditView.TableType != "VIEW" {
		t.Fatalf("audit view identity = %#v", auditView)
	}

	table := inspection.Manifest.Tables[1]
	if table.TableCatalog != "MainDB" || table.TableSchema != "dbo" || table.TableName != "Vehicle" || table.TableType != "BASE TABLE" {
		t.Fatalf("base table identity = %#v", table)
	}
	if len(table.Columns) != 4 {
		t.Fatalf("len(base table Columns) = %d, want 4", len(table.Columns))
	}
	primaryKey := table.Columns[0]
	if primaryKey.ColumnName != "FW_ID" || primaryKey.DataType != "uuid" || !primaryKey.IsIdentity || !primaryKey.IsPrimaryKey || primaryKey.PrimaryKeyOrdinal != 1 || primaryKey.IsNullable {
		t.Fatalf("primary-key metadata = %#v", primaryKey)
	}
	withTypeDefault := table.Columns[1]
	if withTypeDefault.ColumnName != "Total" || withTypeDefault.DataType != "[moneytypes].[Amount]" || !withTypeDefault.IsNullable || !withTypeDefault.HasDefault {
		t.Fatalf("UDT/default metadata = %#v", withTypeDefault)
	}
	rowVersion := table.Columns[2]
	if rowVersion.ColumnName != "Stamp" || rowVersion.DataType != "rowversion" || !rowVersion.IsRowVersion || rowVersion.IsComputed || rowVersion.IsGeneratedAlways {
		t.Fatalf("row-version metadata = %#v", rowVersion)
	}
	generatedAlways := table.Columns[3]
	if generatedAlways.ColumnName != "PeriodStart" || generatedAlways.DataType != "datetime" || generatedAlways.IsComputed || !generatedAlways.IsGeneratedAlways {
		t.Fatalf("generated-always metadata = %#v", generatedAlways)
	}

	state := registeredFakeDriver.callState()
	if state.queries != 1 || state.begins != 0 {
		t.Fatalf("database calls = %#v, want one query and no transaction", state)
	}
}

func TestInspectReturnsDatabaseIdentityForEmptyVisibleCatalog(t *testing.T) {
	registeredFakeDriver.reset()
	registeredFakeDriver.emptyCatalog = true
	t.Cleanup(registeredFakeDriver.reset)

	inspection, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.DatabaseName != "MainDB" || inspection.Manifest.DatabaseName != "MainDB" {
		t.Fatalf("database identity = inspection %q, manifest %q", inspection.DatabaseName, inspection.Manifest.DatabaseName)
	}
	if len(inspection.Manifest.Tables) != 0 {
		t.Fatalf("len(Tables) = %d, want 0", len(inspection.Manifest.Tables))
	}
	state := registeredFakeDriver.callState()
	if state.queries != 1 || state.begins != 0 {
		t.Fatalf("database calls = %#v, want one query and no transaction", state)
	}
}

func TestInspectRejectsExternalTablesDefensively(t *testing.T) {
	registeredFakeDriver.reset()
	registeredFakeDriver.externalTable = true
	t.Cleanup(registeredFakeDriver.reset)

	_, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err == nil || !strings.Contains(err.Error(), `unsupported table type "EXTERNAL TABLE"`) {
		t.Fatalf("Inspect() error = %v", err)
	}
	state := registeredFakeDriver.callState()
	if state.queries != 1 || state.begins != 0 {
		t.Fatalf("database calls = %#v, want one query and no transaction", state)
	}
}

func TestInspectRejectsInconsistentDatabaseIdentity(t *testing.T) {
	registeredFakeDriver.reset()
	registeredFakeDriver.inconsistentDatabase = true
	t.Cleanup(registeredFakeDriver.reset)

	_, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err == nil || !strings.Contains(err.Error(), `inconsistent database names "MainDB" and "OtherDB"`) {
		t.Fatalf("Inspect() error = %v", err)
	}
}

func TestInspectRejectsUnsupportedAndInconsistentSQLServerVersions(t *testing.T) {
	tests := []struct {
		name         string
		configure    func(*fakeDriver)
		wantContains string
	}{
		{name: "unsupported", configure: func(driver *fakeDriver) { driver.oldVersion = true }, wantContains: "minimum is 13.0"},
		{name: "inconsistent", configure: func(driver *fakeDriver) { driver.inconsistentVersion = true }, wantContains: "inconsistent server versions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registeredFakeDriver.reset()
			test.configure(registeredFakeDriver)
			t.Cleanup(registeredFakeDriver.reset)
			_, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
			if err == nil || !strings.Contains(err.Error(), test.wantContains) {
				t.Fatalf("Inspect() error = %v, want substring %q", err, test.wantContains)
			}
		})
	}
}

func TestInspectRejectsSQLServerOperationalErrors(t *testing.T) {
	tests := []struct {
		name         string
		configure    func(*fakeDriver)
		wantContains string
	}{
		{name: "ping", configure: func(driver *fakeDriver) { driver.pingError = errors.New("ping failed") }, wantContains: "connect to SQL Server"},
		{name: "query", configure: func(driver *fakeDriver) { driver.queryError = errors.New("query failed") }, wantContains: "query SQL Server catalog"},
		{name: "iteration", configure: func(driver *fakeDriver) { driver.iterationError = errors.New("iteration failed") }, wantContains: "iterate SQL Server catalog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registeredFakeDriver.reset()
			test.configure(registeredFakeDriver)
			t.Cleanup(registeredFakeDriver.reset)
			_, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
			if err == nil || !strings.Contains(err.Error(), test.wantContains) {
				t.Fatalf("Inspect() error = %v, want substring %q", err, test.wantContains)
			}
		})
	}
}

func TestInspectRejectsTableCatalogDifferentFromDatabase(t *testing.T) {
	registeredFakeDriver.reset()
	registeredFakeDriver.mismatchedCatalog = true
	t.Cleanup(registeredFakeDriver.reset)

	_, err := (&Backend{driverName: fakeDriverName}).Inspect(context.Background(), "opaque-dsn")
	if err == nil || !strings.Contains(err.Error(), `TableCatalog "OtherDB" does not match DatabaseName "MainDB"`) {
		t.Fatalf("Inspect() error = %v", err)
	}
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

type databaseCallState struct {
	queries int
	begins  int
}

type fakeDriver struct {
	mu                   sync.Mutex
	dsn                  string
	pingError            error
	queryError           error
	iterationError       error
	emptyCatalog         bool
	externalTable        bool
	inconsistentDatabase bool
	mismatchedCatalog    bool
	oldVersion           bool
	inconsistentVersion  bool
	calls                databaseCallState
}

func (d *fakeDriver) Open(dsn string) (driver.Conn, error) {
	d.mu.Lock()
	d.dsn = dsn
	pingError := d.pingError
	queryError := d.queryError
	iterationError := d.iterationError
	emptyCatalog := d.emptyCatalog
	externalTable := d.externalTable
	inconsistentDatabase := d.inconsistentDatabase
	mismatchedCatalog := d.mismatchedCatalog
	oldVersion := d.oldVersion
	inconsistentVersion := d.inconsistentVersion
	d.mu.Unlock()
	return &fakeConn{
		driver:               d,
		pingError:            pingError,
		queryError:           queryError,
		iterationError:       iterationError,
		emptyCatalog:         emptyCatalog,
		externalTable:        externalTable,
		inconsistentDatabase: inconsistentDatabase,
		mismatchedCatalog:    mismatchedCatalog,
		oldVersion:           oldVersion,
		inconsistentVersion:  inconsistentVersion,
	}, nil
}

func (d *fakeDriver) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dsn = ""
	d.pingError = nil
	d.queryError = nil
	d.iterationError = nil
	d.emptyCatalog = false
	d.externalTable = false
	d.inconsistentDatabase = false
	d.mismatchedCatalog = false
	d.oldVersion = false
	d.inconsistentVersion = false
	d.calls = databaseCallState{}
}

func (d *fakeDriver) openedDSN() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dsn
}

func (d *fakeDriver) recordQuery() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls.queries++
}

func (d *fakeDriver) recordBegin() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls.begins++
}

func (d *fakeDriver) callState() databaseCallState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

type fakeConn struct {
	driver               *fakeDriver
	pingError            error
	queryError           error
	iterationError       error
	emptyCatalog         bool
	externalTable        bool
	inconsistentDatabase bool
	mismatchedCatalog    bool
	oldVersion           bool
	inconsistentVersion  bool
}

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare is not supported")
}

func (c *fakeConn) Close() error { return nil }

func (c *fakeConn) Begin() (driver.Tx, error) {
	c.driver.recordBegin()
	return nil, errors.New("transactions are not supported")
}

func (c *fakeConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.driver.recordBegin()
	return nil, errors.New("transactions are not supported")
}

func (c *fakeConn) Ping(context.Context) error { return c.pingError }

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.driver.recordQuery()
	if c.queryError != nil {
		return nil, c.queryError
	}
	if query != catalogQuery {
		return nil, errors.New("unexpected query")
	}
	columns := []string{
		"DATABASE_NAME", "SERVER_VERSION", "TABLE_CATALOG", "TABLE_SCHEMA", "TABLE_NAME", "TABLE_TYPE",
		"COLUMN_NAME", "ORDINAL_POSITION", "IS_NULLABLE", "DATA_TYPE", "TYPE_PRECISION", "IS_IDENTITY",
		"IS_PRIMARY_KEY", "IS_COMPUTED", "IS_GENERATED_ALWAYS", "IS_ROW_VERSION",
		"HAS_DEFAULT", "PRIMARY_KEY_ORDINAL",
	}
	if c.emptyCatalog {
		return &fakeRows{
			columns: columns,
			values:  [][]driver.Value{{"MainDB", "16.0.1000.6", nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}},
		}, nil
	}

	values := [][]driver.Value{
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "FW_ID", 1, false, "uniqueidentifier", true, true, false, false, false, false, 1),
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "Total", 2, true, "[moneytypes].[Amount]", false, false, false, false, false, true, 0),
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "Stamp", 3, false, "timestamp", false, false, false, false, true, false, 0),
		catalogRow("MainDB", "dbo", "Vehicle", "BASE TABLE", "PeriodStart", 4, false, "datetime2", false, false, false, true, false, false, 0),
		catalogRow("MainDB", "audit", "Vehicle", "VIEW", "FW_ID", 1, false, "int", false, false, true, false, false, false, 0),
	}
	if c.oldVersion {
		for _, row := range values {
			row[1] = "12.0.6024.0"
		}
	}
	if c.inconsistentVersion {
		values[len(values)-1][1] = "15.0.2000.5"
	}
	if c.externalTable {
		values = append(values, catalogRow("MainDB", "ext", "RemoteData", "EXTERNAL TABLE", "ID", 1, false, "int", false, false, false, false, false, false, 0))
	}
	if c.inconsistentDatabase {
		values[len(values)-1][0] = "OtherDB"
		values[len(values)-1][2] = "OtherDB"
	}
	if c.mismatchedCatalog {
		values[0][2] = "OtherDB"
	}
	return &fakeRows{columns: columns, values: values, iterationError: c.iterationError}, nil
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

type fakeRows struct {
	columns        []string
	values         [][]driver.Value
	index          int
	iterationError error
}

func (r *fakeRows) Columns() []string { return r.columns }

func (r *fakeRows) Close() error { return nil }

func (r *fakeRows) Next(destination []driver.Value) error {
	if r.index < len(r.values) {
		copy(destination, r.values[r.index])
		r.index++
		return nil
	}
	if r.iterationError != nil {
		err := r.iterationError
		r.iterationError = nil
		return err
	}
	return io.EOF
}
