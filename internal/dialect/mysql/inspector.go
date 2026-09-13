package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/manifest"
	_ "github.com/go-sql-driver/mysql"
)

const catalogQuery = `
SELECT
    database_info.database_name,
    database_info.server_version,
    catalog_info.table_catalog,
    catalog_info.table_schema,
    catalog_info.table_name,
    catalog_info.table_type,
    catalog_info.column_name,
    catalog_info.ordinal_position,
    catalog_info.is_nullable,
    catalog_info.data_type,
    catalog_info.column_type,
    catalog_info.is_identity,
    catalog_info.is_primary_key,
    catalog_info.is_computed,
    catalog_info.is_generated_always,
    catalog_info.has_default,
    catalog_info.primary_key_ordinal
FROM (
    SELECT COALESCE(DATABASE(), '') AS database_name, VERSION() AS server_version
) AS database_info
LEFT JOIN (
    SELECT
        column_info.table_catalog,
        column_info.table_schema,
        column_info.table_name,
        table_info.table_type,
        column_info.column_name,
        column_info.ordinal_position,
        column_info.is_nullable = 'YES' AS is_nullable,
        column_info.data_type,
        column_info.column_type,
        LOCATE('auto_increment', LOWER(column_info.extra)) > 0 AS is_identity,
        primary_key.ordinal_position IS NOT NULL AS is_primary_key,
        column_info.generation_expression <> '' AS is_computed,
        column_info.generation_expression <> '' AS is_generated_always,
        (
            column_info.column_default IS NOT NULL
            OR LOCATE('default_generated', LOWER(column_info.extra)) > 0
        ) AS has_default,
        COALESCE(primary_key.ordinal_position, 0) AS primary_key_ordinal
    FROM information_schema.tables AS table_info
    INNER JOIN information_schema.columns AS column_info
        ON column_info.table_catalog = table_info.table_catalog
        AND column_info.table_schema = table_info.table_schema
        AND column_info.table_name = table_info.table_name
    LEFT JOIN information_schema.key_column_usage AS primary_key
        ON primary_key.table_catalog = column_info.table_catalog
        AND primary_key.table_schema = column_info.table_schema
        AND primary_key.table_name = column_info.table_name
        AND primary_key.column_name = column_info.column_name
        AND primary_key.constraint_name = 'PRIMARY'
    WHERE
        table_info.table_schema = DATABASE()
        AND table_info.table_type IN ('BASE TABLE', 'VIEW')
) AS catalog_info
    ON TRUE
ORDER BY
    catalog_info.table_catalog,
    catalog_info.table_schema,
    catalog_info.table_name,
    catalog_info.ordinal_position`

type Backend struct {
	driverName string
}

var _ dialect.Backend = (*Backend)(nil)

func New() *Backend {
	return &Backend{driverName: dialect.MySQL}
}

func (b *Backend) Inspect(ctx context.Context, dsn string) (*dialect.Inspection, error) {
	db, err := sql.Open(b.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open MySQL connection: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to MySQL: %w", err)
	}
	return inspectCatalog(ctx, db)
}

type catalogQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func inspectCatalog(ctx context.Context, queryer catalogQuerier) (*dialect.Inspection, error) {
	rows, err := queryer.QueryContext(ctx, catalogQuery)
	if err != nil {
		return nil, fmt.Errorf("query MySQL catalog: %w", err)
	}
	defer rows.Close()

	var databaseName string
	var serverVersion string
	databaseNameSeen := false
	tables := map[manifest.TableKey]*manifest.Table{}
	for rows.Next() {
		var rowDatabaseName, rowServerVersion string
		var tableCatalog, tableSchema, tableName, tableType sql.NullString
		var columnName, dataType, columnType sql.NullString
		var ordinalPosition, primaryKeyOrdinal sql.NullInt64
		var isNullable, isIdentity, isPrimaryKey sql.NullBool
		var isComputed, isGeneratedAlways, hasDefault sql.NullBool
		if err := rows.Scan(
			&rowDatabaseName,
			&rowServerVersion,
			&tableCatalog,
			&tableSchema,
			&tableName,
			&tableType,
			&columnName,
			&ordinalPosition,
			&isNullable,
			&dataType,
			&columnType,
			&isIdentity,
			&isPrimaryKey,
			&isComputed,
			&isGeneratedAlways,
			&hasDefault,
			&primaryKeyOrdinal,
		); err != nil {
			return nil, fmt.Errorf("scan MySQL catalog: %w", err)
		}

		if !databaseNameSeen {
			if err := validateServerVersion(rowServerVersion); err != nil {
				return nil, err
			}
			databaseName = rowDatabaseName
			serverVersion = rowServerVersion
			databaseNameSeen = true
		} else if rowDatabaseName != databaseName {
			return nil, fmt.Errorf("MySQL returned inconsistent database names %q and %q", databaseName, rowDatabaseName)
		} else if rowServerVersion != serverVersion {
			return nil, fmt.Errorf("MySQL returned inconsistent server versions %q and %q", serverVersion, rowServerVersion)
		}

		valuesValid := []bool{
			tableCatalog.Valid, tableSchema.Valid, tableName.Valid, tableType.Valid,
			columnName.Valid, ordinalPosition.Valid, isNullable.Valid, dataType.Valid,
			columnType.Valid, isIdentity.Valid, isPrimaryKey.Valid, isComputed.Valid,
			isGeneratedAlways.Valid, hasDefault.Valid, primaryKeyOrdinal.Valid,
		}
		validCount := 0
		for _, valid := range valuesValid {
			if valid {
				validCount++
			}
		}
		if validCount == 0 {
			continue
		}
		if validCount != len(valuesValid) {
			return nil, fmt.Errorf("MySQL returned incomplete catalog metadata for database %q", rowDatabaseName)
		}
		if tableType.String != "BASE TABLE" && tableType.String != "VIEW" {
			key := manifest.TableKey{Catalog: tableCatalog.String, Schema: tableSchema.String, Name: tableName.String}
			return nil, fmt.Errorf("MySQL returned unsupported table type %q for %q", tableType.String, key)
		}

		key := manifest.TableKey{Catalog: tableCatalog.String, Schema: tableSchema.String, Name: tableName.String}
		table, exists := tables[key]
		if !exists {
			table = &manifest.Table{
				TableCatalog: tableCatalog.String,
				TableSchema:  tableSchema.String,
				TableName:    tableName.String,
				TableType:    tableType.String,
				Columns:      []manifest.Column{},
			}
			tables[key] = table
		} else if table.TableType != tableType.String {
			return nil, fmt.Errorf("MySQL returned conflicting table types %q and %q for %q", table.TableType, tableType.String, key)
		}
		table.Columns = append(table.Columns, manifest.Column{
			ColumnName:        columnName.String,
			OrdinalPosition:   int(ordinalPosition.Int64),
			IsNullable:        isNullable.Bool,
			DataType:          canonicalDataType(dataType.String, columnType.String),
			IsIdentity:        isIdentity.Bool,
			IsPrimaryKey:      isPrimaryKey.Bool,
			IsComputed:        isComputed.Bool,
			IsGeneratedAlways: isGeneratedAlways.Bool,
			HasDefault:        hasDefault.Bool,
			PrimaryKeyOrdinal: int(primaryKeyOrdinal.Int64),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate MySQL catalog: %w", err)
	}
	if !databaseNameSeen || strings.TrimSpace(databaseName) == "" {
		return nil, fmt.Errorf("MySQL connection does not select a database")
	}

	result := make([]manifest.Table, 0, len(tables))
	for _, table := range tables {
		result = append(result, *table)
	}
	document := manifest.New(databaseName, result)
	if err := manifest.ValidateStructure(document); err != nil {
		return nil, fmt.Errorf("validate MySQL inspection structure: %w", err)
	}
	if err := validateInspectionManifest(document); err != nil {
		return nil, fmt.Errorf("validate MySQL inspection: %w", err)
	}
	return &dialect.Inspection{DatabaseName: databaseName, Manifest: document}, nil
}

func validateServerVersion(version string) error {
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return fmt.Errorf("MariaDB version %q is not supported; MySQL 8.0 or newer is required", version)
	}
	return dialect.RequireMinimumVersion("MySQL", version, dialect.Version{Major: 8})
}

func canonicalDataType(dataType, columnType string) string {
	dataType = strings.TrimSpace(dataType)
	columnType = strings.TrimSpace(columnType)
	normalizedDataType := strings.ToLower(dataType)
	normalizedColumnType := strings.ToLower(columnType)
	unsigned := strings.Contains(normalizedColumnType, "unsigned")
	switch normalizedDataType {
	case "bigint":
		if unsigned {
			return manifest.DataTypeUnsignedBigInt
		}
		return manifest.DataTypeBigInt
	case "binary", "blob", "longblob", "mediumblob", "tinyblob", "varbinary":
		return manifest.DataTypeBinary
	case "bit":
		if normalizedColumnType == "bit(1)" {
			return manifest.DataTypeBoolean
		}
		return manifest.DataTypeBinary
	case "bool", "boolean":
		return manifest.DataTypeBoolean
	case "char", "enum", "longtext", "mediumtext", "set", "text", "tinytext", "varchar":
		return manifest.DataTypeString
	case "date":
		return manifest.DataTypeDate
	case "datetime", "timestamp":
		return manifest.DataTypeDateTime
	case "decimal", "numeric":
		return manifest.DataTypeDecimal
	case "double":
		return manifest.DataTypeDouble
	case "float":
		return manifest.DataTypeReal
	case "int", "integer", "mediumint":
		if unsigned {
			return manifest.DataTypeUnsignedInteger
		}
		return manifest.DataTypeInteger
	case "json":
		return manifest.DataTypeJSON
	case "smallint":
		if unsigned {
			return manifest.DataTypeUnsignedSmallInt
		}
		return manifest.DataTypeSmallInt
	case "time":
		return manifest.DataTypeTime
	case "tinyint":
		if unsigned {
			return manifest.DataTypeUnsignedTinyInt
		}
		return manifest.DataTypeTinyInt
	case "uuid":
		return manifest.DataTypeUUID
	case "year":
		return manifest.DataTypeInteger
	default:
		if columnType != "" {
			return columnType
		}
		return dataType
	}
}
