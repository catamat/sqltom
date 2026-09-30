//go:build integration

package integration_test

import (
	"bytes"
	"context"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/manifest"
)

//go:embed testdata/models_test.go.tpl
var generatedTests string

//go:embed testdata/uuid_scan_test.go
var uuidScanTests []byte

//go:embed testdata/uuid_string_test.go
var uuidStringTests []byte

//go:embed testdata/query_test.go
var queryBuilderTests []byte

//go:embed testdata/lexer_test.go
var queryLexerTests []byte

//go:embed testdata/bench_test.go
var generatedBenchmarks []byte

//go:embed testdata/raw_test.go
var rawReadTests []byte

//go:embed testdata/binary_test.go
var binaryTests []byte

//go:embed testdata/binarytypes.go
var binaryTypesSource []byte

//go:embed testdata/selectcols_test.go
var selectColsTests []byte

func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "sqltom")
	command := exec.Command("go", "build", "-o", binary, "./cmd/sqltom")
	command.Dir = ".."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}

func runCLI(t *testing.T, ctx context.Context, binary, directory string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		// Do not print arguments: they include the connection string.
		t.Fatalf("CLI failed: %v\n%s", err, output)
	}
	return string(output)
}

func generateModels(t *testing.T, ctx context.Context, binary, directory string, current fixture, selected []string) string {
	t.Helper()
	output := runCLI(t, ctx, binary, directory, "-generate", "-dialect", current.name, "-dsn", current.dsn,
		"-output", "models", "-tables", strings.Join(selected, ","))
	for line := range strings.SplitSeq(output, "\n") {
		if filename, ok := strings.CutPrefix(line, "manifest: "); ok {
			if !filepath.IsAbs(filename) {
				filename = filepath.Join(directory, filename)
			}
			return filename
		}
	}
	t.Fatalf("CLI did not report a manifest: %s", output)
	return ""
}

func runGeneratedModels(t *testing.T, ctx context.Context, binary, directory string, current fixture, selected []string) {
	t.Helper()
	prepareGeneratedModule(t, ctx, binary, directory, current, selected)
	runGeneratedCommand(t, ctx, directory, current, "-count=1", "-v", "./...")
}

func prepareGeneratedModule(t *testing.T, ctx context.Context, binary, directory string, current fixture, selected []string) {
	t.Helper()
	extra := prepareRuntimeSchema(t, ctx, current)
	filename := generateModels(t, ctx, binary, directory, current, append(selected, extra...))
	document, err := manifest.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	for ti := range document.Tables {
		table := &document.Tables[ti]
		for ci := range table.Columns {
			column := &table.Columns[ci]
			// MySQL/SQLite store UUIDs as text in this common fixture; declare
			// the logical type to test the same model API as native UUID servers.
			if table.TableName == "UUIDValue" && (column.ColumnName == "ID" || column.ColumnName == "OptionalID") {
				if current.name == dialect.MySQL || current.name == dialect.SQLite {
					column.DataType = manifest.DataTypeUUID
				}
				if current.name != dialect.MySQL && current.name != dialect.SQLite && column.DataType != manifest.DataTypeUUID {
					t.Fatalf("native UUID column inspected as %q", column.DataType)
				}
			}
			if column.ColumnName == "ID" {
				switch table.TableName {
				case "ScannerIdentity":
					column.GoType, column.GoImport = "sql.NullInt64", "database/sql"
				case "PointerIdentity":
					column.GoType = "*int64"
				case "NarrowIdentity":
					column.GoType = "int8"
				case "UnsignedPointer":
					column.GoType = "*uint64"
				case "UnsignedScanner":
					column.GoType, column.GoImport = "identitytypes.UnsignedID", "generated.test/identitytypes"
				case "UnsignedNarrow":
					column.GoType = "int64"
				case "NullableScannerKey":
					column.GoType, column.GoImport = "sql.NullInt64", "database/sql"
				case "NullableValuerKey":
					column.GoType, column.GoImport = "identitytypes.Key", "generated.test/identitytypes"
				case "NamedIdentity":
					column.GoType, column.GoImport = "identitytypes.ID", "generated.test/identitytypes"
				}
			}
			// SQL Server 2022 stores JSON as text. Declare its logical type explicitly;
			// the other three dialects inspect their JSON declarations directly.
			if current.name == dialect.SQLServer && table.TableName == "Values" && strings.Contains(column.ColumnName, "JSON") {
				column.DataType = manifest.DataTypeJSON
			}
		}
	}
	if err := manifest.SaveAtomic(filename, document); err != nil {
		t.Fatal(err)
	}
	runCLI(t, ctx, binary, directory, "-render", "-dialect", current.name, "-manifest", filename, "-output", "models")
	renderUUIDVariants(t, ctx, binary, directory, current.name, document)
	renderBinaryVariant(t, ctx, binary, directory, current.name, document)
	if current.name == dialect.SQLServer {
		if err := os.WriteFile(filepath.Join(directory, "models", "UUIDValue", "uuid_scan_test.go"), uuidScanTests, 0600); err != nil {
			t.Fatal(err)
		}
	}

	// A standalone consumer module catches missing imports and accidental runtime
	// dependencies on sqltom. Use the project's pinned driver versions offline.
	module, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	module = bytes.Replace(module, []byte("module github.com/catamat/sqltom"), []byte("module generated.test"), 1)
	if err := os.WriteFile(filepath.Join(directory, "go.mod"), module, 0600); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile("../go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "go.sum"), sums, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "identitytypes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "identitytypes", "id.go"), []byte(identityTypesSource), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "binarytypes"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "binarytypes", "binary.go"), binaryTypesSource, 0600); err != nil {
		t.Fatal(err)
	}
	parsed := template.Must(template.New("tests").Parse(generatedTests))
	var source bytes.Buffer
	if err := parsed.Execute(&source, current.name); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "models_test.go"), source.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "query_test.go"), queryBuilderTests, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "lexer_test.go"), queryLexerTests, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "bench_test.go"), generatedBenchmarks, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "raw_test.go"), rawReadTests, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "binary_test.go"), binaryTests, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "uuid_string_test.go"), uuidStringTests, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "selectcols_test.go"), selectColsTests, 0600); err != nil {
		t.Fatal(err)
	}
}

func runGeneratedCommand(t *testing.T, ctx context.Context, directory string, current fixture, args ...string) {
	t.Helper()
	command := exec.CommandContext(ctx, "go", append([]string{"test", "-mod=readonly"}, args...)...)
	command.Dir = directory
	command.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=off",
		"SQLTOM_E2E_DSN="+current.dsn, "SQLTOM_E2E_DRIVER="+current.driverName,
		"SQLTOM_E2E_DIALECT="+current.name, "SQLTOM_E2E_SCHEMA="+current.schema)
	output, err := command.CombinedOutput()
	t.Logf("generated consumer (%s):\n%s", current.name, output)
	if err != nil {
		t.Fatalf("generated module: %v", err)
	}
}

func runtimeIdentifier(current fixture, name string) string {
	switch current.name {
	case dialect.MySQL:
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	case dialect.SQLServer:
		return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
}

func prepareRuntimeSchema(t *testing.T, ctx context.Context, current fixture) []string {
	t.Helper()
	db := openDatabase(t, ctx, current.driverName, current.dsn)
	defer db.Close()
	requireTestDatabase(t, ctx, db, current.name)
	id, narrow, textType, clockType, boolType, jsonType, binaryType, stampType :=
		"INTEGER PRIMARY KEY AUTOINCREMENT", "INTEGER PRIMARY KEY AUTOINCREMENT", "TEXT", "TIME", "BOOLEAN", "JSON", "BLOB", "DATETIME"
	switch current.name {
	case dialect.Postgres:
		id, narrow = "INTEGER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY", "INTEGER GENERATED BY DEFAULT AS IDENTITY (START WITH 256) PRIMARY KEY"
		clockType, jsonType, binaryType, stampType = "TIME(6)", "JSONB", "BYTEA", "TIMESTAMP"
	case dialect.MySQL:
		id, narrow = "INTEGER AUTO_INCREMENT PRIMARY KEY", "INTEGER AUTO_INCREMENT PRIMARY KEY"
		textType, clockType, boolType = "VARCHAR(100)", "TIME(6)", "BIT(1)"
	case dialect.SQLServer:
		id, narrow = "INTEGER IDENTITY(1,1) PRIMARY KEY", "INTEGER IDENTITY(256,1) PRIMARY KEY"
		textType, clockType, boolType, jsonType, binaryType, stampType = "NVARCHAR(100)", "TIME(6)", "BIT", "NVARCHAR(MAX)", "VARBINARY(MAX)", "DATETIME2"
	}
	q := func(name string) string { return runtimeIdentifier(current, name) }
	uuidType := "CHAR(36)"
	if current.name == dialect.Postgres {
		uuidType = "UUID"
	} else if current.name == dialect.SQLServer {
		uuidType = "UNIQUEIDENTIFIER"
	} else if current.name == dialect.SQLite {
		uuidType = "TEXT"
	}
	tables := []struct{ name, definition, suffix string }{
		{"IdentityOnly", q("ID") + " " + id, ""},
		{"ScannerIdentity", q("ID") + " " + id + ", " + q("Name") + " " + textType + " NOT NULL", ""},
		{"PointerIdentity", q("ID") + " " + id + ", " + q("Name") + " " + textType + " NOT NULL", ""},
		{"NarrowIdentity", q("ID") + " " + narrow, ""},
		{"NamedIdentity", q("ID") + " " + id + ", " + q("Name") + " " + textType + " NOT NULL", ""},
		{"Keyless", q("RecordID") + " INTEGER NOT NULL, " + q("Name") + " " + textType + " NOT NULL", ""},
		{"UUIDValue", q("ID") + " " + uuidType + " NOT NULL PRIMARY KEY, " + q("Name") + " " + textType + " NOT NULL, " + q("OptionalID") + " " + uuidType + " NULL", ""},
		{"Values", strings.Join([]string{
			q("ID") + " " + id,
			q("Clock") + " " + clockType + " NOT NULL", q("OptionalClock") + " " + clockType + " NULL",
			q("JSONValue") + " " + jsonType + " NOT NULL", q("OptionalJSON") + " " + jsonType + " NULL",
			q("Flag") + " " + boolType + " NOT NULL", q("OptionalFlag") + " " + boolType + " NULL",
			q("BinaryValue") + " " + binaryType + " NULL", q("Day") + " DATE NULL", q("Stamp") + " " + stampType + " NULL",
			q("Amount") + " DECIMAL(10,2) NULL",
		}, ", "), ""},
	}
	if current.name == dialect.MySQL {
		tables[3].suffix = " AUTO_INCREMENT=256"
		for _, name := range []string{"UnsignedIdentity", "UnsignedPointer", "UnsignedScanner", "UnsignedNarrow"} {
			tables = append(tables, struct{ name, definition, suffix string }{name, q("ID") + " BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY", ""})
		}
	}
	if current.name == dialect.SQLite {
		for _, name := range []string{"NullableKey", "NullableScannerKey", "NullableValuerKey", "NullableBinaryKey"} {
			keyType := "INT"
			if name == "NullableBinaryKey" {
				keyType = "BLOB"
			}
			tables = append(tables, struct{ name, definition, suffix string }{name, q("ID") + " " + keyType + " PRIMARY KEY, " + q("Name") + " TEXT NOT NULL", ""})
		}
		tables = append(tables, struct{ name, definition, suffix string }{"NullableComposite", q("First") + " INT, " + q("Second") + " TEXT, " + q("Name") + " TEXT NOT NULL, PRIMARY KEY (" + q("First") + ", " + q("Second") + ")", ""})
		tables = append(tables, struct{ name, definition, suffix string }{"DescendingKey", q("ID") + " INTEGER PRIMARY KEY DESC, " + q("Name") + " TEXT NOT NULL", ""})
	} else {
		identity := strings.TrimSuffix(id, " PRIMARY KEY")
		tables = append(tables, struct{ name, definition, suffix string }{"NonKeyIdentity", q("Key") + " INTEGER PRIMARY KEY, " + q("Sequence") + " " + identity + " UNIQUE, " + q("Name") + " " + textType + " NOT NULL", ""})
	}
	if current.name == dialect.Postgres {
		tables = append(tables, struct{ name, definition, suffix string }{"MultipleIdentity", q("ID") + " " + id + ", " + q("Sequence") + " INTEGER GENERATED ALWAYS AS IDENTITY, " + q("Name") + " TEXT NOT NULL", ""})
	}
	var names []string
	for _, table := range tables {
		names = append(names, table.name)
	}
	cleanupObjects(t, current, "TABLE", names)
	for _, table := range tables {
		name := q(current.schema) + "." + q(table.name)
		for _, statement := range []string{"DROP TABLE IF EXISTS " + name, "CREATE TABLE " + name + " (" + table.definition + ")" + table.suffix} {
			if _, err := db.ExecContext(ctx, statement); err != nil {
				t.Fatalf("runtime fixture %s: %v", table.name, err)
			}
		}
	}
	if current.name == dialect.SQLite {
		if _, err := db.ExecContext(ctx, `INSERT INTO NarrowIdentity (ID) VALUES (255)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DELETE FROM NarrowIdentity`); err != nil {
			t.Fatal(err)
		}
	}
	return names
}

func assertPostgresReadOnlyKeys(t *testing.T, ctx context.Context, current fixture) {
	t.Helper()
	db := openDatabase(t, ctx, current.driverName, current.dsn)
	defer db.Close()
	const role = "sqltom_inspection_reader"
	if _, err := db.ExecContext(ctx, "CREATE ROLE "+role+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, query := range []string{"DROP OWNED BY " + role, "DROP ROLE " + role} {
			if _, err := db.ExecContext(ctx, query); err != nil {
				t.Errorf("reader role cleanup: %v", err)
			}
		}
	}()
	for _, query := range []string{"GRANT USAGE ON SCHEMA public TO " + role, `GRANT SELECT ON "Vehicle", "CompositeKey" TO ` + role} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	dsn := postgresRoleDSN(t, current.dsn, role)
	inspection, err := current.backend.Inspect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Manifest.Tables) != 2 {
		t.Fatalf("SELECT-only role sees %d tables, want 2", len(inspection.Manifest.Tables))
	}
	for _, name := range []string{"Vehicle", "CompositeKey"} {
		table := findTable(t, inspection.Manifest, current.schema, name)
		for _, column := range table.Columns {
			want := 0
			if column.ColumnName == "ID" || column.ColumnName == "ItemID" {
				want = 1
			}
			if column.ColumnName == "TenantID" {
				want = 2
			}
			if column.IsPrimaryKey != (want > 0) || column.PrimaryKeyOrdinal != want {
				t.Errorf("SELECT-only role: %s.%s key = %v/%d, want %d", name, column.ColumnName, column.IsPrimaryKey, column.PrimaryKeyOrdinal, want)
			}
		}
	}
	t.Log("SELECT-only PostgreSQL role retains primary keys and their order")
}

const identityTypesSource = `package identitytypes

import (
 "database/sql"
 "database/sql/driver"
)

type ID int64

type UnsignedID struct { N uint64 }
func (id *UnsignedID) Scan(value any) error {
 return sql.ConvertAssign(driver.ScanContext{}, &id.N, value)
}

type Key struct { N int64; Valid bool; Calls *int; Err error }
func (key Key) Value() (driver.Value, error) {
 if key.Calls != nil { *key.Calls++ }
 if key.Err != nil { return nil, key.Err }
 if !key.Valid { return nil, nil }
 return key.N, nil
}
func (key *Key) Scan(value any) error {
 key.Valid = value != nil
 if !key.Valid { key.N = 0; return nil }
 return sql.ConvertAssign(driver.ScanContext{}, &key.N, value)
}
`

func renderUUIDVariants(t *testing.T, ctx context.Context, binary, directory, dialectName string, document *manifest.Manifest) {
	t.Helper()
	variants := []string{"googlemodels"}
	if dialectName == dialect.SQLServer {
		variants = append(variants, "nativemodels", "wrappedmodels", "aliasmodels")
	}
	for _, variant := range variants {
		selected, err := manifest.SelectTables(document, []string{"UUIDValue"})
		if err != nil {
			t.Fatal(err)
		}
		selected.TypeMappings[manifest.DataTypeUUID] = manifest.TypeMapping{GoType: "uuid.UUID", GoImport: "github.com/google/uuid"}
		if variant == "nativemodels" {
			selected.TypeMappings[manifest.DataTypeUUID] = manifest.TypeMapping{GoType: "mssql.UniqueIdentifier", GoImport: "github.com/microsoft/go-mssqldb"}
		}
		if variant == "wrappedmodels" {
			selected.TypeMappings[manifest.DataTypeUUID] = manifest.TypeMapping{GoType: "uuid.UUID", GoImport: "github.com/google/uuid", NullableGoType: "uuid.NullUUID", NullableGoImport: "github.com/google/uuid"}
			// A column override wins over the global mapping; the adapter also reuses
			// this custom driver alias instead of importing the package a second time.
			selected.Tables[0].Columns[0].GoType, selected.Tables[0].Columns[0].GoImport = "converted.UniqueIdentifier", "github.com/microsoft/go-mssqldb"
		}
		if variant == "aliasmodels" {
			selected.TypeMappings[manifest.DataTypeUUID] = manifest.TypeMapping{GoType: "err.UUID", GoImport: "github.com/google/uuid"}
			selected.Tables[0].GoName = "sqltomMSSQL"
		}
		filename := filepath.Join(directory, variant+".json")
		if err := manifest.SaveAtomic(filename, selected); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		runCLI(t, ctx, binary, directory, "-render", "-dialect", dialectName, "-manifest", filename, "-output", variant)
		after, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("render persisted dialect defaults in the manifest")
		}
	}
}

func renderBinaryVariant(t *testing.T, ctx context.Context, binary, directory, dialectName string, document *manifest.Manifest) {
	t.Helper()
	selected, err := manifest.SelectTables(document, []string{"Values"})
	if err != nil {
		t.Fatal(err)
	}
	selected.TypeMappings[manifest.DataTypeBinary] = manifest.TypeMapping{
		GoType: "binarytypes.Value", GoImport: "generated.test/binarytypes",
		NullableGoType: "binarytypes.Value", NullableGoImport: "generated.test/binarytypes",
	}
	filename := filepath.Join(directory, "binarymodels.json")
	if err := manifest.SaveAtomic(filename, selected); err != nil {
		t.Fatal(err)
	}
	runCLI(t, ctx, binary, directory, "-render", "-dialect", dialectName, "-manifest", filename, "-output", "binarymodels")
}
