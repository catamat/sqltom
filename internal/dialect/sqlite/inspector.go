package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/manifest"
	_ "modernc.org/sqlite"
)

const databaseListQuery = `PRAGMA database_list`
const versionQuery = `SELECT sqlite_version()`

type Backend struct {
	driverName string
}

var _ dialect.Backend = (*Backend)(nil)

func New() *Backend {
	return &Backend{driverName: dialect.SQLite}
}

func (b *Backend) Inspect(ctx context.Context, dsn string) (*dialect.Inspection, error) {
	inspectionDSN, err := readOnlyInspectionDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(b.driverName, inspectionDSN)
	if err != nil {
		return nil, fmt.Errorf("open SQLite connection: %w", err)
	}
	defer db.Close()
	connection, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire SQLite connection: %w", err)
	}
	defer connection.Close()
	if err := connection.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to SQLite: %w", err)
	}
	return inspectCatalog(ctx, connection)
}

func readOnlyInspectionDSN(dsn string) (string, error) {
	if strings.TrimSpace(dsn) == "" {
		return "", fmt.Errorf("SQLite DSN is empty")
	}
	lowerDSN := strings.ToLower(dsn)
	if dsn == ":memory:" || strings.HasPrefix(lowerDSN, ":memory:?") ||
		strings.HasPrefix(lowerDSN, "file::memory:") {
		return dsn, nil
	}

	if !strings.HasPrefix(lowerDSN, "file:") {
		absolute, err := filepath.Abs(dsn)
		if err != nil {
			return "", fmt.Errorf("resolve SQLite database path: %w", err)
		}
		location := &url.URL{Scheme: "file", Path: absolute}
		query := location.Query()
		query.Set("mode", "ro")
		location.RawQuery = query.Encode()
		return location.String(), nil
	}

	location, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse SQLite DSN: %w", err)
	}
	query := location.Query()
	if strings.EqualFold(query.Get("mode"), "memory") {
		return dsn, nil
	}
	query.Set("mode", "ro")
	location.RawQuery = query.Encode()
	return location.String(), nil
}

type catalogQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type databaseInfo struct {
	schema string
	file   string
}

type objectInfo struct {
	schema       string
	name         string
	tableType    string
	withoutRowID bool
}

func inspectCatalog(ctx context.Context, queryer catalogQuerier) (*dialect.Inspection, error) {
	var serverVersion string
	if err := queryer.QueryRowContext(ctx, versionQuery).Scan(&serverVersion); err != nil {
		return nil, fmt.Errorf("query SQLite version: %w", err)
	}
	if err := dialect.RequireMinimumVersion("SQLite", serverVersion, dialect.Version{Major: 3, Minor: 37}); err != nil {
		return nil, err
	}
	databases, err := inspectDatabases(ctx, queryer)
	if err != nil {
		return nil, err
	}
	databaseName := "main"
	for _, database := range databases {
		if database.schema == "main" {
			databaseName = databaseNameFromFile(database.file)
			break
		}
	}

	objects := make([]objectInfo, 0)
	for _, database := range databases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		schemaObjects, err := inspectObjects(ctx, queryer, database.schema)
		if err != nil {
			return nil, err
		}
		objects = append(objects, schemaObjects...)
	}

	tables := make([]manifest.Table, 0, len(objects))
	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		columns, err := inspectColumns(ctx, queryer, object)
		if err != nil {
			return nil, err
		}
		tables = append(tables, manifest.Table{
			TableCatalog: databaseName,
			TableSchema:  object.schema,
			TableName:    object.name,
			TableType:    object.tableType,
			Columns:      columns,
		})
	}

	document := manifest.New(databaseName, tables)
	if err := manifest.ValidateStructure(document); err != nil {
		return nil, fmt.Errorf("validate SQLite inspection structure: %w", err)
	}
	if err := validateInspectionManifest(document); err != nil {
		return nil, fmt.Errorf("validate SQLite inspection: %w", err)
	}
	return &dialect.Inspection{DatabaseName: databaseName, Manifest: document}, nil
}

func inspectDatabases(ctx context.Context, queryer catalogQuerier) ([]databaseInfo, error) {
	rows, err := queryer.QueryContext(ctx, databaseListQuery)
	if err != nil {
		return nil, fmt.Errorf("query SQLite database list: %w", err)
	}
	defer rows.Close()

	result := make([]databaseInfo, 0, 1)
	for rows.Next() {
		var sequence int
		var schema, file string
		if err := rows.Scan(&sequence, &schema, &file); err != nil {
			return nil, fmt.Errorf("scan SQLite database list: %w", err)
		}
		if strings.TrimSpace(schema) == "" {
			return nil, fmt.Errorf("SQLite returned an empty database schema name")
		}
		result = append(result, databaseInfo{schema: schema, file: file})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite database list: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("SQLite database list is empty")
	}
	return result, nil
}

func inspectObjects(ctx context.Context, queryer catalogQuerier, schema string) ([]objectInfo, error) {
	query := "PRAGMA " + quoteSQLiteIdentifier(schema) + ".table_list"
	rows, err := queryer.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query SQLite objects in schema %q: %w", schema, err)
	}
	defer rows.Close()

	result := make([]objectInfo, 0)
	for rows.Next() {
		var returnedSchema, name, nativeType string
		var columnCount, withoutRowID, strict int
		if err := rows.Scan(&returnedSchema, &name, &nativeType, &columnCount, &withoutRowID, &strict); err != nil {
			return nil, fmt.Errorf("scan SQLite objects in schema %q: %w", schema, err)
		}
		if returnedSchema != schema {
			return nil, fmt.Errorf("SQLite returned schema %q while inspecting %q", returnedSchema, schema)
		}
		if strings.HasPrefix(strings.ToLower(name), "sqlite_") {
			continue
		}
		var tableType string
		switch nativeType {
		case "table", "virtual":
			tableType = "BASE TABLE"
		case "view":
			tableType = "VIEW"
		default:
			continue
		}
		result = append(result, objectInfo{
			schema:       schema,
			name:         name,
			tableType:    tableType,
			withoutRowID: withoutRowID != 0,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite objects in schema %q: %w", schema, err)
	}
	return result, nil
}

func inspectColumns(ctx context.Context, queryer catalogQuerier, object objectInfo) ([]manifest.Column, error) {
	query := "PRAGMA " + quoteSQLiteIdentifier(object.schema) + ".table_xinfo(" + quoteSQLiteString(object.name) + ")"
	rows, err := queryer.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query SQLite columns for %q.%q: %w", object.schema, object.name, err)
	}
	defer rows.Close()

	columns := make([]manifest.Column, 0)
	declaredTypes := make([]string, 0)
	for rows.Next() {
		var columnID, notNull, primaryKeyOrdinal, hidden int
		var name, declaredType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &declaredType, &notNull, &defaultValue, &primaryKeyOrdinal, &hidden); err != nil {
			return nil, fmt.Errorf("scan SQLite columns for %q.%q: %w", object.schema, object.name, err)
		}
		if hidden == 1 {
			continue
		}
		if columnID < 0 {
			return nil, fmt.Errorf("SQLite returned invalid column id %d for %q.%q", columnID, object.schema, object.name)
		}
		isGenerated := hidden == 2 || hidden == 3
		columns = append(columns, manifest.Column{
			ColumnName:        name,
			OrdinalPosition:   columnID + 1,
			IsNullable:        notNull == 0,
			DataType:          canonicalDataType(declaredType),
			IsPrimaryKey:      primaryKeyOrdinal > 0,
			IsComputed:        isGenerated,
			IsGeneratedAlways: isGenerated,
			HasDefault:        defaultValue.Valid,
			PrimaryKeyOrdinal: primaryKeyOrdinal,
		})
		declaredTypes = append(declaredTypes, declaredType)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQLite columns for %q.%q: %w", object.schema, object.name, err)
	}

	primaryKeyIndex := -1
	primaryKeyCount := 0
	for index, column := range columns {
		if column.IsPrimaryKey {
			primaryKeyIndex = index
			primaryKeyCount++
		}
	}
	if !object.withoutRowID && primaryKeyCount == 1 &&
		strings.EqualFold(strings.TrimSpace(declaredTypes[primaryKeyIndex]), "INTEGER") {
		columns[primaryKeyIndex].IsIdentity = true
		columns[primaryKeyIndex].IsNullable = false
	}
	return columns, nil
}

func databaseNameFromFile(filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "main"
	}
	name := filepath.Base(filename)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "main"
	}
	return name
}

func canonicalDataType(declaredType string) string {
	native := strings.ToUpper(strings.TrimSpace(declaredType))
	switch {
	case native == "":
		return manifest.DataTypeAny
	case native == "ANY":
		return manifest.DataTypeAny
	case strings.Contains(native, "JSON"):
		return manifest.DataTypeJSON
	case strings.Contains(native, "UUID"):
		return manifest.DataTypeUUID
	case strings.Contains(native, "BIGINT"):
		return manifest.DataTypeBigInt
	case strings.Contains(native, "SMALLINT"):
		return manifest.DataTypeSmallInt
	case strings.Contains(native, "TINYINT"):
		return manifest.DataTypeTinyInt
	case strings.Contains(native, "INT"):
		return manifest.DataTypeInteger
	case strings.Contains(native, "CHAR"), strings.Contains(native, "CLOB"), strings.Contains(native, "TEXT"):
		return manifest.DataTypeString
	case strings.Contains(native, "BLOB"):
		return manifest.DataTypeBinary
	case strings.Contains(native, "REAL"), strings.Contains(native, "FLOA"), strings.Contains(native, "DOUB"):
		return manifest.DataTypeDouble
	case strings.Contains(native, "BOOL"):
		return manifest.DataTypeBoolean
	case strings.Contains(native, "DATETIME"), strings.Contains(native, "TIMESTAMP"):
		return manifest.DataTypeDateTime
	case native == "DATE":
		return manifest.DataTypeDate
	case native == "TIME":
		return manifest.DataTypeTime
	case strings.Contains(native, "DEC"), strings.Contains(native, "NUM"):
		return manifest.DataTypeDecimal
	default:
		return strings.TrimSpace(declaredType)
	}
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteSQLiteString(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
