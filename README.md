# Sqltom
[![License](https://img.shields.io/github/license/catamat/sqltom.svg)](https://github.com/catamat/sqltom/blob/main/LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/catamat/sqltom)](https://goreportcard.com/report/github.com/catamat/sqltom)
[![Go Reference](https://pkg.go.dev/badge/github.com/catamat/sqltom.svg)](https://pkg.go.dev/github.com/catamat/sqltom)
[![Version](https://img.shields.io/github/tag/catamat/sqltom.svg?color=blue&label=version)](https://github.com/catamat/sqltom/releases)

Sqltom is a command line tool to inspect SQL database, stores its visible schema in an editable
manifest, and renders Go packages from that manifest.

## Features:
- Supports PostgreSQL, MySQL, SQLite, and SQL Server.
- Inspects tables and views.
- Generates Go models from an editable, backend-neutral manifest.
- Supports custom data type mappings through the manifest.
- Supports custom column, table, package, and file names through the manifest.

## Installation:

sqltom requires Go 1.27 or newer. Install the latest version with:

```sh
go install github.com/catamat/sqltom/cmd/sqltom@latest
```

Then verify the installation:

```sh
sqltom -version
```

## Dialects:

- PostgreSQL 12 or newer via `pgx`
- MySQL 8.0 or newer via `go-sql-driver/mysql`
- SQLite through the bundled pure-Go `modernc.org/sqlite` engine
- SQL Server 2016 or newer via `go-mssqldb`

Inspection verifies these minimum server versions before accepting catalog
metadata. MariaDB is not treated as MySQL because its catalog compatibility is
not part of the supported contract. The bundled SQLite engine must provide
SQLite 3.37 or newer, which introduced the catalog primitives used by Sqltom.

Server-dialect DSNs are passed unchanged to the selected driver. SQLite
inspection preserves explicit in-memory DSNs and forces file DSNs to read-only
mode. Typical forms are:

```text
sqlserver://user:password@host:1433?database=MainDB
postgres://user:password@host:5432/MainDB?sslmode=disable
user:password@tcp(host:3306)/MainDB?parseTime=true
./MainDB.sqlite
```

For SQLite, a file database uses its filename as `DatabaseName`; an in-memory
database uses `main`. Tables in `main` or an attached database retain the SQLite
schema alias in `TableSchema`. File databases are opened read-only during
inspection, so a missing or misspelled path fails instead of creating an empty
database. Explicit in-memory DSNs remain supported.

## Commands:

Print the installed version with `-version`.

Every database operation accepts an optional `-tables` filter. Its value is a
CSV list of exact, case-sensitive database table or view selectors. A selector
may be `name`, `schema.name`, or `catalog.schema.name`:

```sh
-tables="dbo.Vehicle,dbo.Customer"
```

Omitting the flag selects every object available to the operation;
passing an explicitly empty `-tables=""` is a usage error. Physical
names with leading or trailing whitespace cannot be selected because that
whitespace is treated as CSV syntax.

### Inspect

```sh
sqltom -inspect \
  -dialect="sqlserver" \
  -dsn="sqlserver://user:password@host:1433?database=MainDB" \
```

Inspection reads visible user tables and views, the current database name,
catalog/schema identity, identity/computed/generated/rowversion/default flags,
and ordered primary-key metadata. It writes `sqltom_MainDB.json` in the current
directory. System and backend-specific unsupported objects are excluded.

If the target manifest exists, its `DatabaseName` must match the live
connection. The source dialect is deliberately not persisted: each inspector
normalizes native metadata into the common manifest vocabulary. Sqltom
refreshes database facts while preserving:

- global `TypeMappings`;
- table `IsManaged` and, only while managed, `FileName`, `PackageName`, and
  `GoName`;
- column `GoName`, `JSONName`, `GoType`, and `GoImport`.

### Render

```sh
sqltom -render \
  -dialect="sqlserver" \
  -manifest="./sqltom_MainDB.json" \
  -output="./models" \
```

`-dialect` selects the target backend, not the provenance of the manifest. The
same manifest can therefore be rendered by any implemented backend.

Render structurally validates the manifest for the target backend, and writes and formats
every selected model; files and model directories from previous renders are removed.
A missing or empty output directory is accepted. A non-empty directory is
replaced only if it contains the exact `.sqltom-output` marker created by a
prior successful Sqltom render; unrelated directories are refused.
An output path nested inside another Sqltom-managed output is also refused:
parent and child replacement cannot be made independently safe.

This marker is intentionally fail-closed. A model directory produced by an
older Sqltom without the marker must be moved or emptied deliberately once, or
you can render to a new path. Sqltom never adopts a non-empty unmarked directory
automatically.

### Generate

```sh
sqltom -generate \
  -dialect="sqlserver" \
  -dsn="sqlserver://user:password@host:1433?database=MainDB" \
  -output="./models" \
```

Generate is Inspect followed by Render.

## Manifest v1:

The manifest is deliberately editable and backend-neutral. `DatabaseName` is
mandatory and is its only database-level identity.

```json
{
  "Version": 1,
  "DatabaseName": "MainDB",
  "TypeMappings": {
    "uuid": {
      "GoType": "uuid.UUID",
      "GoImport": "github.com/google/uuid",
      "NullableGoType": "null.UUID",
      "NullableGoImport": "github.com/catamat/null"
    },
    "string": {
      "GoType": "string",
      "NullableGoType": "null.String",
      "NullableGoImport": "github.com/catamat/null"
    }
  },
  "Tables": [
    {
      "TableCatalog": "MainDB",
      "TableSchema": "dbo",
      "TableName": "VEHICLE",
      "TableType": "BASE TABLE",
      "IsManaged": true,
      "FileName": "vehicle",
      "PackageName": "vehicle",
      "GoName": "Vehicle",
      "Columns": [
        {
          "ColumnName": "VEHICLE_ID",
          "OrdinalPosition": 1,
          "IsNullable": false,
          "DataType": "uuid",
          "IsIdentity": false,
          "IsPrimaryKey": true,
          "IsComputed": false,
          "IsGeneratedAlways": false,
          "IsRowVersion": false,
          "HasDefault": true,
          "PrimaryKeyOrdinal": 1,
          "GoName": "ID",
          "JSONName": "id",
          "GoType": "",
          "GoImport": ""
        }
      ]
    }
  ]
}
```

### Table management

Every inspected table is written with `"IsManaged": true`. A missing
`IsManaged` key is also interpreted as `true`, but the next save writes it
explicitly.

Setting `IsManaged` to `false` excludes that table from rendering. On the next
inspect or generate, Sqltom retains only `TableCatalog`, `TableSchema`,
`TableName`, `TableType`, and `IsManaged`; it clears `FileName`,
`PackageName`, and `GoName`, and writes `"Columns": []`. The identity stub
remains in the manifest and can still be addressed by `-tables`.

To re-enable a table, set `IsManaged` to `true` and run inspect or generate.
Fresh inspection metadata repopulates `Columns`; discarded naming and column
overrides are not restored. Rendering a re-enabled stub directly, before a new
inspection, fails because its managed table has no columns.

### Type precedence

For every column, the effective type is selected in this order:

1. column `GoType` and `GoImport`;
2. a matching `TypeMappings` entry;
3. the backend-neutral built-in mapping for the canonical `DataType`;
4. an error naming the unknown logical or custom type.

`TypeMappings` is partial: omitted canonical types continue to use built-in
defaults.
If a selected mapping has no `NullableGoType`, a nullable column uses `*GoType`
and reuses `GoImport`.
Mapping lookup first tries the exact trimmed `DataType`, preserving case. A
case-insensitive fallback is used only when it identifies one entry; if multiple
case variants exist without an exact match, render fails with an ambiguity
error.
The package qualifier is taken from `GoType`, not inferred from the last segment
of `GoImport`; versioned module paths such as `github.com/guregu/null/v6` are
therefore valid.

The canonical vocabulary is `any`, `bigint`, `binary`, `boolean`, `date`,
`datetime`, `datetimeoffset`, `decimal`, `double`, `integer`, `json`, `money`,
`real`, `rowversion`, `smallint`, `string`, `time`, `tinyint`, `unsignedbigint`,
`unsignedinteger`, `unsignedsmallint`, `unsignedtinyint`, `uuid`, `variant`, and
`xml`. Inspectors translate native database types into these values; renderers
never need to know which database originally produced the manifest. `json` maps
to `encoding/json.RawMessage`; `variant` maps to `any` for both nullable and
non-nullable columns because each value can have a different concrete Go type.

`IsIdentity` is the normalized identity-like flag used for a value generated by
the database and omitted from ordinary inserts. Backends map their equivalent
mechanisms to it, such as identity, auto-increment, or a SQLite rowid alias.
Computed, generated-always, and rowversion columns remain available in models
and reads but are omitted from ordinary inserts and updates. `HasDefault` alone
does not make a column read-only, so the current API can still provide an
explicit value.

Column-level `GoType` is already the final type, including nullability:

```json
"GoType": "sql.NullString",
"GoImport": "database/sql"
```

The generic `uuid` default is Go 1.27's `uuid.UUID` from package `uuid`. The
standard type does not implement `database/sql.Scanner` or `driver.Valuer`;
override it with a suitable application type or codec, such as
`github.com/google/uuid`, when transparent SQL conversion is required.

### Naming fields

- table `IsManaged`: whether Sqltom renders the table;
- table `FileName`: output directory and `.go` filename base;
- table `PackageName`: package declaration;
- table `GoName`: model type;
- column `GoName`: struct field;
- column `JSONName`: JSON field name, defaulting to the original database name;
- column `GoType`/`GoImport`: granular type override.

Explicit invalid overrides fail validation. Defaults retain case but remove
characters invalid in Go identifiers or package import paths. File defaults also
avoid `_test`, implicit GOOS/GOARCH suffixes, `vendor`, `testdata`, leading
dot/underscore, Windows device names, and overlong components. Output-name
collisions are checked with Unicode case folding for case-insensitive
filesystems.

Because defaults do not change case, a lowercase database name produces a
lowercase, unexported Go identifier. The CLI emits a warning for every effective
table or column `GoName` that is not exported; set it explicitly in the
manifest when the model or field must be visible outside its package.

## Gotchas & tips:

- The project is designed for local, sequential use. Concurrent use in distributed or automated environments may cause issues and is currently outside the project's scope.
- The CLI currently accepts configuration only through command-line arguments, not environment variables or file descriptors.
- Databases with the same `DatabaseName` are treated as the same logical source, regardless of host or instance.
