package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/manifest"
	_ "github.com/microsoft/go-mssqldb"
)

const catalogQuery = `
SELECT
    database_info.DATABASE_NAME,
    database_info.SERVER_VERSION,
    catalog_info.TABLE_CATALOG,
    catalog_info.TABLE_SCHEMA,
    catalog_info.TABLE_NAME,
    catalog_info.TABLE_TYPE,
    catalog_info.COLUMN_NAME,
    catalog_info.ORDINAL_POSITION,
    catalog_info.IS_NULLABLE,
    catalog_info.DATA_TYPE,
    catalog_info.TYPE_PRECISION,
    catalog_info.IS_IDENTITY,
    catalog_info.IS_PRIMARY_KEY,
    catalog_info.IS_COMPUTED,
    catalog_info.IS_GENERATED_ALWAYS,
    catalog_info.IS_ROW_VERSION,
    catalog_info.HAS_DEFAULT,
    catalog_info.PRIMARY_KEY_ORDINAL
FROM (
    SELECT
        CONVERT(nvarchar(128), DB_NAME()) AS DATABASE_NAME,
        CONVERT(nvarchar(128), SERVERPROPERTY('ProductVersion')) AS SERVER_VERSION
) AS database_info
LEFT JOIN (
    SELECT
        CONVERT(nvarchar(128), DB_NAME()) AS TABLE_CATALOG,
        schema_info.name AS TABLE_SCHEMA,
        object_info.name AS TABLE_NAME,
        CASE object_info.type
            WHEN 'U' THEN 'BASE TABLE'
            WHEN 'V' THEN 'VIEW'
        END AS TABLE_TYPE,
        column_info.name AS COLUMN_NAME,
        column_info.column_id AS ORDINAL_POSITION,
        CONVERT(bit, column_info.is_nullable) AS IS_NULLABLE,
        CASE
            WHEN type_info.is_user_defined = 1
                THEN QUOTENAME(type_schema_info.name) + N'.' + QUOTENAME(type_info.name)
            ELSE type_info.name
        END AS DATA_TYPE,
        column_info.precision AS TYPE_PRECISION,
        CONVERT(bit, column_info.is_identity) AS IS_IDENTITY,
        CONVERT(bit, CASE WHEN primary_key.key_ordinal IS NULL THEN 0 ELSE 1 END) AS IS_PRIMARY_KEY,
        CONVERT(bit, column_info.is_computed) AS IS_COMPUTED,
        CONVERT(bit, CASE WHEN column_info.generated_always_type = 0 THEN 0 ELSE 1 END) AS IS_GENERATED_ALWAYS,
        CONVERT(bit, CASE WHEN type_info.system_type_id = 189 THEN 1 ELSE 0 END) AS IS_ROW_VERSION,
        CONVERT(bit, CASE
            WHEN column_info.default_object_id <> 0 OR type_info.default_object_id <> 0 THEN 1
            ELSE 0
        END) AS HAS_DEFAULT,
        COALESCE(primary_key.key_ordinal, 0) AS PRIMARY_KEY_ORDINAL
    FROM sys.objects AS object_info
    INNER JOIN sys.schemas AS schema_info
        ON schema_info.schema_id = object_info.schema_id
    INNER JOIN sys.columns AS column_info
        ON column_info.object_id = object_info.object_id
    INNER JOIN sys.types AS type_info
        ON type_info.user_type_id = column_info.user_type_id
    INNER JOIN sys.schemas AS type_schema_info
        ON type_schema_info.schema_id = type_info.schema_id
    LEFT JOIN (
        SELECT
            index_column.object_id,
            index_column.column_id,
            index_column.key_ordinal
        FROM sys.indexes AS index_info
        INNER JOIN sys.index_columns AS index_column
            ON index_column.object_id = index_info.object_id
            AND index_column.index_id = index_info.index_id
        WHERE index_info.is_primary_key = 1
    ) AS primary_key
        ON primary_key.object_id = column_info.object_id
        AND primary_key.column_id = column_info.column_id
    WHERE
        object_info.type IN ('U', 'V')
        AND object_info.type <> 'ET'
        AND object_info.is_ms_shipped = 0
        AND column_info.is_hidden = 0
        AND NOT (
            object_info.type = 'U'
            AND schema_info.name = N'dbo'
            AND object_info.name = N'sysdiagrams'
        )
        AND NOT (
            (
                object_info.type = 'U'
                AND (
                    object_info.name LIKE N'MSSQL[_]DroppedLedgerTable[_]%'
                    OR object_info.name LIKE N'MSSQL[_]DroppedLedgerHistory[_]%'
                )
            )
            OR (
                object_info.type = 'V'
                AND object_info.name LIKE N'MSSQL[_]DroppedLedgerView[_]%'
            )
        )
) AS catalog_info
    ON 1 = 1
ORDER BY
    catalog_info.TABLE_CATALOG,
    catalog_info.TABLE_SCHEMA,
    catalog_info.TABLE_NAME,
    catalog_info.ORDINAL_POSITION`

type Backend struct {
	driverName string
}

var _ dialect.Backend = (*Backend)(nil)

func New() *Backend {
	return &Backend{driverName: dialect.SQLServer}
}

func (b *Backend) Inspect(ctx context.Context, dsn string) (*dialect.Inspection, error) {
	db, err := sql.Open(b.driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQL Server connection: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("connect to SQL Server: %w", err)
	}
	return inspectCatalog(ctx, db)
}

type catalogQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func inspectCatalog(ctx context.Context, queryer catalogQuerier) (*dialect.Inspection, error) {
	rows, err := queryer.QueryContext(ctx, catalogQuery)
	if err != nil {
		return nil, fmt.Errorf("query SQL Server catalog: %w", err)
	}
	defer rows.Close()

	var databaseName string
	var serverVersion string
	databaseNameSeen := false
	tables := map[manifest.TableKey]*manifest.Table{}
	for rows.Next() {
		var rowDatabaseName, rowServerVersion string
		var tableCatalog, tableSchema, tableName, tableType sql.NullString
		var columnName, dataType sql.NullString
		var ordinalPosition, typePrecision, primaryKeyOrdinal sql.NullInt64
		var isNullable, isIdentity, isPrimaryKey sql.NullBool
		var isComputed, isGeneratedAlways, isRowVersion, hasDefault sql.NullBool
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
			&typePrecision,
			&isIdentity,
			&isPrimaryKey,
			&isComputed,
			&isGeneratedAlways,
			&isRowVersion,
			&hasDefault,
			&primaryKeyOrdinal,
		); err != nil {
			return nil, fmt.Errorf("scan SQL Server catalog: %w", err)
		}

		if !databaseNameSeen {
			if err := dialect.RequireMinimumVersion("SQL Server", rowServerVersion, dialect.Version{Major: 13}); err != nil {
				return nil, err
			}
			databaseName = rowDatabaseName
			serverVersion = rowServerVersion
			databaseNameSeen = true
		} else if rowDatabaseName != databaseName {
			return nil, fmt.Errorf("SQL Server returned inconsistent database names %q and %q", databaseName, rowDatabaseName)
		} else if rowServerVersion != serverVersion {
			return nil, fmt.Errorf("SQL Server returned inconsistent server versions %q and %q", serverVersion, rowServerVersion)
		}

		catalogValuesValid := []bool{
			tableCatalog.Valid,
			tableSchema.Valid,
			tableName.Valid,
			tableType.Valid,
			columnName.Valid,
			ordinalPosition.Valid,
			isNullable.Valid,
			dataType.Valid,
			typePrecision.Valid,
			isIdentity.Valid,
			isPrimaryKey.Valid,
			isComputed.Valid,
			isGeneratedAlways.Valid,
			isRowVersion.Valid,
			hasDefault.Valid,
			primaryKeyOrdinal.Valid,
		}
		validCount := 0
		for _, valid := range catalogValuesValid {
			if valid {
				validCount++
			}
		}
		if validCount == 0 {
			continue
		}
		if validCount != len(catalogValuesValid) {
			return nil, fmt.Errorf("SQL Server returned incomplete catalog metadata for database %q", rowDatabaseName)
		}
		if tableType.String != "BASE TABLE" && tableType.String != "VIEW" {
			key := manifest.TableKey{Catalog: tableCatalog.String, Schema: tableSchema.String, Name: tableName.String}
			return nil, fmt.Errorf("SQL Server returned unsupported table type %q for %q", tableType.String, key)
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
			return nil, fmt.Errorf("SQL Server returned conflicting table types %q and %q for %q", table.TableType, tableType.String, key)
		}
		table.Columns = append(table.Columns, manifest.Column{
			ColumnName:        columnName.String,
			OrdinalPosition:   int(ordinalPosition.Int64),
			IsNullable:        isNullable.Bool,
			DataType:          canonicalDataType(dataType.String, typePrecision.Int64, isRowVersion.Bool),
			IsIdentity:        isIdentity.Bool,
			IsPrimaryKey:      isPrimaryKey.Bool,
			IsComputed:        isComputed.Bool,
			IsGeneratedAlways: isGeneratedAlways.Bool,
			IsRowVersion:      isRowVersion.Bool,
			HasDefault:        hasDefault.Bool,
			PrimaryKeyOrdinal: int(primaryKeyOrdinal.Int64),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQL Server catalog: %w", err)
	}
	if !databaseNameSeen {
		return nil, fmt.Errorf("SQL Server catalog query returned no rows")
	}

	result := make([]manifest.Table, 0, len(tables))
	for _, table := range tables {
		result = append(result, *table)
	}
	document := manifest.New(databaseName, result)
	if err := manifest.ValidateStructure(document); err != nil {
		return nil, fmt.Errorf("validate SQL Server inspection structure: %w", err)
	}
	if err := validateInspectionManifest(document); err != nil {
		return nil, fmt.Errorf("validate SQL Server inspection: %w", err)
	}
	return &dialect.Inspection{
		DatabaseName: databaseName,
		Manifest:     document,
	}, nil
}

func canonicalDataType(nativeType string, typePrecision int64, isRowVersion bool) string {
	if isRowVersion {
		return manifest.DataTypeRowVersion
	}

	switch strings.ToLower(strings.TrimSpace(nativeType)) {
	case "bigint":
		return manifest.DataTypeBigInt
	case "binary", "image", "varbinary":
		return manifest.DataTypeBinary
	case "bit":
		return manifest.DataTypeBoolean
	case "char", "nchar", "ntext", "nvarchar", "sysname", "text", "varchar":
		return manifest.DataTypeString
	case "date":
		return manifest.DataTypeDate
	case "datetime", "datetime2", "smalldatetime":
		return manifest.DataTypeDateTime
	case "datetimeoffset":
		return manifest.DataTypeDateTimeOffset
	case "decimal", "numeric":
		return manifest.DataTypeDecimal
	case "float":
		if typePrecision > 0 && typePrecision <= 24 {
			return manifest.DataTypeReal
		}
		return manifest.DataTypeDouble
	case "int":
		return manifest.DataTypeInteger
	case "json":
		return manifest.DataTypeJSON
	case "money", "smallmoney":
		return manifest.DataTypeMoney
	case "real":
		return manifest.DataTypeReal
	case "smallint":
		return manifest.DataTypeSmallInt
	case "time":
		return manifest.DataTypeTime
	case "tinyint":
		return manifest.DataTypeTinyInt
	case "uniqueidentifier":
		return manifest.DataTypeUUID
	case "sql_variant":
		return manifest.DataTypeVariant
	case "xml":
		return manifest.DataTypeXML
	}
	return nativeType
}
