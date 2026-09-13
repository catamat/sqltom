package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/manifest"
	_ "github.com/jackc/pgx/v5/stdlib"
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
    catalog_info.udt_schema,
    catalog_info.udt_name,
    catalog_info.domain_schema,
    catalog_info.domain_name,
    catalog_info.is_identity,
    catalog_info.is_primary_key,
    catalog_info.is_computed,
    catalog_info.is_generated_always,
    catalog_info.has_default,
    catalog_info.primary_key_ordinal
FROM (
    SELECT
        current_database()::text AS database_name,
        current_setting('server_version')::text AS server_version
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
        column_info.udt_schema,
        column_info.udt_name,
        column_info.domain_schema,
        column_info.domain_name,
        (
            column_info.is_identity = 'YES'
            OR COALESCE(column_info.column_default LIKE 'nextval(%', FALSE)
        ) AS is_identity,
        primary_key.ordinal_position IS NOT NULL AS is_primary_key,
        column_info.is_generated <> 'NEVER' AS is_computed,
        column_info.is_generated <> 'NEVER' AS is_generated_always,
        (
            column_info.column_default IS NOT NULL
            OR domain_info.domain_default IS NOT NULL
        ) AS has_default,
        COALESCE(primary_key.ordinal_position, 0) AS primary_key_ordinal
    FROM information_schema.tables AS table_info
    INNER JOIN information_schema.columns AS column_info
        ON column_info.table_catalog = table_info.table_catalog
        AND column_info.table_schema = table_info.table_schema
        AND column_info.table_name = table_info.table_name
    LEFT JOIN (
        SELECT
            constraint_info.table_catalog,
            constraint_info.table_schema,
            constraint_info.table_name,
            key_info.column_name,
            key_info.ordinal_position
        FROM information_schema.table_constraints AS constraint_info
        INNER JOIN information_schema.key_column_usage AS key_info
            ON key_info.constraint_catalog = constraint_info.constraint_catalog
            AND key_info.constraint_schema = constraint_info.constraint_schema
            AND key_info.constraint_name = constraint_info.constraint_name
            AND key_info.table_catalog = constraint_info.table_catalog
            AND key_info.table_schema = constraint_info.table_schema
            AND key_info.table_name = constraint_info.table_name
        WHERE constraint_info.constraint_type = 'PRIMARY KEY'
    ) AS primary_key
        ON primary_key.table_catalog = column_info.table_catalog
        AND primary_key.table_schema = column_info.table_schema
        AND primary_key.table_name = column_info.table_name
        AND primary_key.column_name = column_info.column_name
    LEFT JOIN information_schema.domains AS domain_info
        ON domain_info.domain_catalog = column_info.domain_catalog
        AND domain_info.domain_schema = column_info.domain_schema
        AND domain_info.domain_name = column_info.domain_name
    WHERE
        table_info.table_catalog = current_database()
        AND table_info.table_type IN ('BASE TABLE', 'VIEW')
        AND table_info.table_schema NOT IN ('information_schema', 'pg_catalog')
        AND table_info.table_schema NOT LIKE 'pg_toast%'
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
	return &Backend{driverName: "pgx"}
}

func (b *Backend) Inspect(ctx context.Context, dsn string) (*dialect.Inspection, error) {
	db, err := sql.Open(b.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL connection: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return inspectCatalog(ctx, db)
}

type catalogQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func inspectCatalog(ctx context.Context, queryer catalogQuerier) (*dialect.Inspection, error) {
	rows, err := queryer.QueryContext(ctx, catalogQuery)
	if err != nil {
		return nil, fmt.Errorf("query PostgreSQL catalog: %w", err)
	}
	defer rows.Close()

	var databaseName string
	var serverVersion string
	databaseNameSeen := false
	tables := map[manifest.TableKey]*manifest.Table{}
	for rows.Next() {
		var rowDatabaseName, rowServerVersion string
		var tableCatalog, tableSchema, tableName, tableType sql.NullString
		var columnName, dataType, udtSchema, udtName sql.NullString
		var domainSchema, domainName sql.NullString
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
			&udtSchema,
			&udtName,
			&domainSchema,
			&domainName,
			&isIdentity,
			&isPrimaryKey,
			&isComputed,
			&isGeneratedAlways,
			&hasDefault,
			&primaryKeyOrdinal,
		); err != nil {
			return nil, fmt.Errorf("scan PostgreSQL catalog: %w", err)
		}

		if !databaseNameSeen {
			if err := dialect.RequireMinimumVersion("PostgreSQL", rowServerVersion, dialect.Version{Major: 12}); err != nil {
				return nil, err
			}
			databaseName = rowDatabaseName
			serverVersion = rowServerVersion
			databaseNameSeen = true
		} else if rowDatabaseName != databaseName {
			return nil, fmt.Errorf("PostgreSQL returned inconsistent database names %q and %q", databaseName, rowDatabaseName)
		} else if rowServerVersion != serverVersion {
			return nil, fmt.Errorf("PostgreSQL returned inconsistent server versions %q and %q", serverVersion, rowServerVersion)
		}

		valuesValid := []bool{
			tableCatalog.Valid, tableSchema.Valid, tableName.Valid, tableType.Valid,
			columnName.Valid, ordinalPosition.Valid, isNullable.Valid, dataType.Valid,
			udtSchema.Valid, udtName.Valid, isIdentity.Valid, isPrimaryKey.Valid,
			isComputed.Valid, isGeneratedAlways.Valid, hasDefault.Valid,
			primaryKeyOrdinal.Valid,
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
			return nil, fmt.Errorf("PostgreSQL returned incomplete catalog metadata for database %q", rowDatabaseName)
		}
		if domainSchema.Valid != domainName.Valid {
			return nil, fmt.Errorf("PostgreSQL returned incomplete domain metadata for database %q", rowDatabaseName)
		}
		if tableType.String != "BASE TABLE" && tableType.String != "VIEW" {
			key := manifest.TableKey{Catalog: tableCatalog.String, Schema: tableSchema.String, Name: tableName.String}
			return nil, fmt.Errorf("PostgreSQL returned unsupported table type %q for %q", tableType.String, key)
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
			return nil, fmt.Errorf("PostgreSQL returned conflicting table types %q and %q for %q", table.TableType, tableType.String, key)
		}
		table.Columns = append(table.Columns, manifest.Column{
			ColumnName:        columnName.String,
			OrdinalPosition:   int(ordinalPosition.Int64),
			IsNullable:        isNullable.Bool,
			DataType:          canonicalDataType(dataType.String, udtSchema.String, udtName.String, domainSchema.String, domainName.String),
			IsIdentity:        isIdentity.Bool,
			IsPrimaryKey:      isPrimaryKey.Bool,
			IsComputed:        isComputed.Bool,
			IsGeneratedAlways: isGeneratedAlways.Bool,
			HasDefault:        hasDefault.Bool,
			PrimaryKeyOrdinal: int(primaryKeyOrdinal.Int64),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostgreSQL catalog: %w", err)
	}
	if !databaseNameSeen || strings.TrimSpace(databaseName) == "" {
		return nil, fmt.Errorf("PostgreSQL catalog query returned no database name")
	}

	result := make([]manifest.Table, 0, len(tables))
	for _, table := range tables {
		result = append(result, *table)
	}
	document := manifest.New(databaseName, result)
	if err := manifest.ValidateStructure(document); err != nil {
		return nil, fmt.Errorf("validate PostgreSQL inspection structure: %w", err)
	}
	if err := validateInspectionManifest(document); err != nil {
		return nil, fmt.Errorf("validate PostgreSQL inspection: %w", err)
	}
	return &dialect.Inspection{DatabaseName: databaseName, Manifest: document}, nil
}

func canonicalDataType(dataType, udtSchema, udtName, domainSchema, domainName string) string {
	if domainName != "" {
		return qualifiedType(domainSchema, domainName)
	}
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "bigint":
		return manifest.DataTypeBigInt
	case "binary", "bytea":
		return manifest.DataTypeBinary
	case "boolean":
		return manifest.DataTypeBoolean
	case "character", "character varying", "text":
		return manifest.DataTypeString
	case "date":
		return manifest.DataTypeDate
	case "timestamp without time zone":
		return manifest.DataTypeDateTime
	case "timestamp with time zone":
		return manifest.DataTypeDateTimeOffset
	case "decimal", "numeric":
		return manifest.DataTypeDecimal
	case "double precision":
		return manifest.DataTypeDouble
	case "integer":
		return manifest.DataTypeInteger
	case "json", "jsonb":
		return manifest.DataTypeJSON
	case "money":
		return manifest.DataTypeMoney
	case "real":
		return manifest.DataTypeReal
	case "smallint":
		return manifest.DataTypeSmallInt
	case "time without time zone", "time with time zone":
		return manifest.DataTypeTime
	case "uuid":
		return manifest.DataTypeUUID
	case "xml":
		return manifest.DataTypeXML
	case "array", "user-defined":
		return qualifiedType(udtSchema, udtName)
	default:
		return strings.TrimSpace(dataType)
	}
}

func qualifiedType(schema, name string) string {
	if strings.TrimSpace(schema) == "" {
		return strings.TrimSpace(name)
	}
	return quoteTypeIdentifier(schema) + "." + quoteTypeIdentifier(name)
}

func quoteTypeIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
