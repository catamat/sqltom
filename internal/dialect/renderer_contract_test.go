package dialect_test

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/dialect/mysql"
	"github.com/catamat/sqltom/internal/dialect/postgres"
	"github.com/catamat/sqltom/internal/dialect/sqlite"
	"github.com/catamat/sqltom/internal/dialect/sqlserver"
	"github.com/catamat/sqltom/internal/manifest"
)

type rendererContract struct {
	name                  string
	backend               dialect.Backend
	poisonTableName       string
	poisonColumnName      string
	escapedTableFragment  string
	escapedColumnFragment string
	qualifiedTarget       string
	compositeWhere        string
	emptyInsert           string
}

func TestRenderersSatisfyTheSameContract(t *testing.T) {
	contracts := []rendererContract{
		{
			name:                  dialect.Postgres,
			backend:               postgres.New(),
			poisonTableName:       "Ta\"ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co\"l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  `Ta\"\"ble`,
			escapedColumnFragment: `Co\"\"l`,
			qualifiedTarget:       "` + \"\\\"dbo\\\".\\\"VEHICLE\\\"\" + `",
			compositeWhere:        `"TenantID" = $2 AND "ItemID" = $3`,
			emptyInsert:           `DEFAULT VALUES RETURNING "ID"`,
		},
		{
			name:                  dialect.MySQL,
			backend:               mysql.New(),
			poisonTableName:       "Ta]ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co]l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  "Ta]ble``",
			escapedColumnFragment: "Co]l``",
			qualifiedTarget:       "` + \"`dbo`.`VEHICLE`\" + `",
			compositeWhere:        "`TenantID` = ? AND `ItemID` = ?",
			emptyInsert:           "() VALUES ()",
		},
		{
			name:                  dialect.SQLite,
			backend:               sqlite.New(),
			poisonTableName:       "Ta\"ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co\"l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  `Ta\"\"ble`,
			escapedColumnFragment: `Co\"\"l`,
			qualifiedTarget:       "` + \"\\\"dbo\\\".\\\"VEHICLE\\\"\" + `",
			compositeWhere:        `"TenantID" = ? AND "ItemID" = ?`,
			emptyInsert:           `DEFAULT VALUES`,
		},
		{
			name:                  dialect.SQLServer,
			backend:               sqlserver.New(),
			poisonTableName:       "Ta]ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co]l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  "[Ta]]ble",
			escapedColumnFragment: "[Co]]l",
			qualifiedTarget:       "` + \"[MainDB].[dbo].[VEHICLE]\" + `",
			compositeWhere:        `[TenantID] = @p2 AND [ItemID] = @p3`,
			emptyInsert:           `DEFAULT VALUES; SELECT SCOPE_IDENTITY()`,
		},
	}

	if len(contracts) != len(dialect.Names()) {
		t.Fatalf("renderer contracts = %d, dialects = %d", len(contracts), len(dialect.Names()))
	}
	for index, name := range dialect.Names() {
		if contracts[index].name != name {
			t.Fatalf("renderer contract %d is %q, want %q", index, contracts[index].name, name)
		}
	}
	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			runRendererContract(t, contract)
		})
	}
}

func runRendererContract(t *testing.T, contract rendererContract) {
	t.Helper()

	t.Run("keyless tables retain reads and insert", func(t *testing.T) {
		for _, name := range []string{"ID", "RecordID", "NaturalKey"} {
			m := runtimeManifest()
			m.Tables[0].Columns[0].ColumnName = name
			m.Tables[0].Columns[0].IsPrimaryKey = false
			m.Tables[0].Columns[0].PrimaryKeyOrdinal = 0
			m.Tables[0].Columns[0].IsIdentity = false
			source := renderContractSource(t, contract.backend, m)
			typeCheckContractSource(t, source)
			if strings.Contains(source, "func (r *Vehicle) Update(") {
				t.Fatalf("keyless table with column %q has Update", name)
			}
			for _, operation := range []string{"SelectAll", "SelectCols", "First", "Last", "One", "At", "IsEmpty", "Filter", "Insert", "Query", "Exists", "Delete"} {
				contractFunction(t, source, operation)
			}
		}
	})

	t.Run("model names do not collide with generated locals", func(t *testing.T) {
		for _, name := range []string{"db", "stmt", "args", "rows", "err", "query", "result", "r", "row", "one", "lastInsertID", "sqltomScanValue", "sqltomJSONValue", "sqltomDriver", "sqltomTime", "keyArgs", "keyIndex", "keyValue", "keyNames", "sqltomFmt", "sqltomStrconv", "sqltomStrings", "sqltomPartial", "columns", "projection", "indexes", "seen", "index", "column", "destinations", "predicate", "sqltomErrors", "Select", "First", "Last", "One", "At", "IsEmpty", "Filter"} {
			m := runtimeManifest()
			m.Tables[0].GoName = name
			m.Tables[0].Columns[1].DataType = manifest.DataTypeJSON
			m.Tables[0].Columns = append(m.Tables[0].Columns, manifest.Column{ColumnName: "Clock", OrdinalPosition: 3, DataType: manifest.DataTypeTime})
			typeCheckContractSource(t, renderContractSource(t, contract.backend, m))
		}
	})

	t.Run("projection metadata does not collide with fields", func(t *testing.T) {
		m := runtimeManifest()
		m.Tables[0].Columns[1].GoName = "sqltomPartial"
		typeCheckContractSource(t, renderContractSource(t, contract.backend, m))
	})

	t.Run("new declarations reject name conflicts", func(t *testing.T) {
		for _, name := range []string{"SelectAll", "SelectCols", "Rows", "ErrMultipleRows", "ErrIndexOutOfRange"} {
			for _, asImport := range []bool{false, true} {
				m := runtimeManifest()
				if asImport {
					m.Tables[0].Columns[1].GoType = name + ".Time"
					m.Tables[0].Columns[1].GoImport = "time"
				} else {
					m.Tables[0].GoName = name
				}
				if err := contract.backend.Render(context.Background(), m, filepath.Join(t.TempDir(), "models")); err == nil || !strings.Contains(err.Error(), "conflicts with a generated declaration") {
					t.Fatalf("name %s (import=%v): %v", name, asImport, err)
				}
			}
		}
	})

	t.Run("identity overrides compile", func(t *testing.T) {
		for _, mapping := range []manifest.ResolvedType{
			{GoType: "sql.NullInt64", GoImport: "database/sql"},
			{GoType: "*int64"},
			{GoType: "uint8"},
			{GoType: "clock.Duration", GoImport: "time"},
		} {
			m := runtimeManifest()
			m.Tables[0].Columns[0].GoType = mapping.GoType
			m.Tables[0].Columns[0].GoImport = mapping.GoImport
			typeCheckContractSource(t, renderContractSource(t, contract.backend, m))
		}
	})

	t.Run("unsigned identity identifiers", func(t *testing.T) {
		for _, name := range []string{"sqltomStrconv", "identityValue"} {
			m := runtimeManifest()
			m.Tables[0].GoName = name
			m.Tables[0].Columns[0].DataType = manifest.DataTypeUnsignedBigInt
			m.Tables[0].Columns[1].GoType, m.Tables[0].Columns[1].GoImport = name+"_.Time", "time"
			typeCheckContractSource(t, renderContractSource(t, contract.backend, m))
		}
	})

	t.Run("custom import aliases do not collide with generated locals", func(t *testing.T) {
		for _, alias := range []string{"r", "db", "err", "rows", "value", "converted", "bytes", "sqltomDriver", "sqltomScanValue", "keyArgs", "keyIndex", "keyValue", "keyNames", "sqltomFmt", "sqltomStrconv", "sqltomStrings", "sqltomPartial", "columns", "projection", "indexes", "seen", "index", "column", "destinations", "predicate", "sqltomErrors", "Select", "First", "Last", "One", "At", "IsEmpty", "Filter"} {
			m := runtimeManifest()
			m.Tables[0].Columns[1].GoType = alias + ".Time"
			m.Tables[0].Columns[1].GoImport = "time"
			m.Tables[0].Columns = append(m.Tables[0].Columns, manifest.Column{ColumnName: "Clock", OrdinalPosition: 3, DataType: manifest.DataTypeTime})
			typeCheckContractSource(t, renderContractSource(t, contract.backend, m))
		}
	})

	t.Run("model surface", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, surfaceManifest())
		if strings.Contains(source, "func Select(") {
			t.Fatal("obsolete Select function is still generated")
		}
		for _, fragment := range []string{
			"\"database/sql\"", "null \"github.com/catamat/null\"", "uuid \"github.com/google/uuid\"",
			"type DBTX interface {",
			"type Vehicle struct {\n\tID          uuid.UUID   `db:\"RecordID\" json:\"id\"`\n\tDescription null.String `db:\"DESCRIPTION\" json:\"description\"`",
			"func SelectAll(db DBTX, stmt string, args ...interface{}) (Rows, error)",
			"func SelectCols(db DBTX, projection string, stmt string, args ...interface{}) (Rows, error)",
			"type Rows []*Vehicle",
			"func (rows Rows) First() (*Vehicle, error)",
			"func (rows Rows) Last() (*Vehicle, error)",
			"func (rows Rows) One() (*Vehicle, error)",
			"func (rows Rows) At(index int) (*Vehicle, error)",
			"func (rows Rows) IsEmpty() bool",
			"func (rows Rows) Filter(predicate func(*Vehicle) bool) Rows",
			"func (r *Vehicle) Insert(db DBTX)",
			"func (r *Vehicle) Update(db DBTX)",
			"func Delete(db DBTX,",
			"func Exists(db DBTX, stmt string, args ...interface{}) (bool, error)",
			"func Query(db DBTX,",
		} {
			if !strings.Contains(source, fragment) {
				t.Errorf("generated source missing %q\n%s", fragment, source)
			}
		}
	})

	t.Run("identifier escaping", func(t *testing.T) {
		m := surfaceManifest()
		m.Tables[0].TableName = contract.poisonTableName
		m.Tables[0].Columns[1].ColumnName = contract.poisonColumnName
		source := renderContractSource(t, contract.backend, m)
		parsed, err := parser.ParseFile(token.NewFileSet(), "Vehicle.go", source, parser.AllErrors)
		if err != nil {
			t.Fatalf("generated source does not parse: %v\n%s", err, source)
		}
		for _, declaration := range parsed.Decls {
			generated, ok := declaration.(*ast.GenDecl)
			if !ok || generated.Tok != token.VAR {
				continue
			}
			for _, specification := range generated.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					if name.Name == "Injected" || name.Name == "ColumnInjected" {
						t.Fatalf("database identifier injected declaration %q:\n%s", name.Name, source)
					}
				}
			}
		}
		if !strings.Contains(source, contract.escapedTableFragment) || !strings.Contains(source, contract.escapedColumnFragment) {
			t.Fatalf("identifiers were not escaped as expected:\n%s", source)
		}
	})

	t.Run("generated package type checks", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, runtimeManifest())
		typeCheckContractSource(t, source)
		if got := strings.Count(source, "if err := rows.Scan("); got != 2 {
			t.Fatalf("guarded rows.Scan calls = %d, want 2\n%s", got, source)
		}
		if strings.Contains(source, "strings.Replace") || !strings.Contains(source, "db.Query(stmt, args...)") {
			t.Fatalf("Query does not execute the supplied statement verbatim:\n%s", source)
		}
	})

	t.Run("complete composite key update", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, compositeKeyManifest())
		typeCheckContractSource(t, source)
		query := contractQuery(t, source, "Update")
		if !strings.Contains(query, contract.compositeWhere) {
			t.Fatalf("Update query = %q, want complete key fragment %q", query, contract.compositeWhere)
		}
		update := contractFunction(t, source, "Update")
		arguments := "r.Payload,\n\t\tr.TenantID,\n\t\tr.ItemID,"
		if contract.name == dialect.SQLite {
			arguments = "r.Payload,\n\t\tkeyArgs[0],\n\t\tkeyArgs[1],"
			if !strings.Contains(update, "r.TenantID,\n\t\tr.ItemID,") {
				t.Fatalf("key conversion order is incorrect:\n%s", update)
			}
		}
		if !strings.Contains(update, arguments) {
			t.Fatalf("Update arguments are not value followed by the complete key:\n%s", update)
		}
	})

	t.Run("identity is independent from the key", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, nonKeyIdentityManifest())
		typeCheckContractSource(t, source)
		insert := contractFunction(t, source, "Insert")
		if !strings.Contains(insert, "r.Sequence") {
			t.Fatalf("Insert does not assign the non-key identity column:\n%s", insert)
		}
		update := contractFunction(t, source, "Update")
		if strings.Contains(update, "r.Sequence,") || !strings.Contains(update, "r.NaturalKey,") {
			t.Fatalf("Update does not keep identity and key roles separate:\n%s", update)
		}
	})

	if contract.name == dialect.Postgres {
		t.Run("multiple identity columns", func(t *testing.T) {
			source := renderContractSource(t, contract.backend, multipleIdentityManifest())
			typeCheckContractSource(t, source)
			query := contractQuery(t, source, "Insert")
			if !strings.Contains(query, `RETURNING "SequenceA", "SequenceB"`) {
				t.Fatalf("Insert query does not return every PostgreSQL identity: %q", query)
			}
			insert := contractFunction(t, source, "Insert")
			if !strings.Contains(insert, "&r.SequenceA,\n\t\t&r.SequenceB,") {
				t.Fatalf("Insert does not scan every PostgreSQL identity:\n%s", insert)
			}
		})
	}

	t.Run("identity only table", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, identityOnlyManifest())
		typeCheckContractSource(t, source)
		if strings.Contains(source, "func (r *IdentityOnly) Update(") {
			t.Fatalf("identity-only table has an unusable Update method:\n%s", source)
		}
		query := contractQuery(t, source, "Insert")
		if !strings.Contains(query, contract.emptyInsert) {
			t.Fatalf("Insert query = %q, want fragment %q", query, contract.emptyInsert)
		}
	})

	t.Run("explicit import alias", func(t *testing.T) {
		m := runtimeManifest()
		m.Tables[0].Columns[1].GoType = "clock.Duration"
		m.Tables[0].Columns[1].GoImport = "time"
		source := renderContractSource(t, contract.backend, m)
		if !strings.Contains(source, `clock "time"`) {
			t.Fatalf("custom import was not explicitly aliased:\n%s", source)
		}
		typeCheckContractSource(t, source)
	})

	t.Run("qualified objects", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, surfaceManifest())
		for _, operation := range []string{"FROM", "INSERT INTO", "UPDATE", "DELETE FROM"} {
			fragment := operation + " " + contract.qualifiedTarget
			if !strings.Contains(source, fragment) {
				t.Errorf("generated source does not qualify %s target; missing %q\n%s", operation, fragment, source)
			}
		}
	})

	t.Run("UUID defaults and SQL projection", func(t *testing.T) {
		m := runtimeManifest()
		m.Tables[0].Columns[0].DataType = manifest.DataTypeUUID
		m.Tables[0].Columns[0].IsIdentity = false
		m.Tables[0].Columns = append(m.Tables[0].Columns, manifest.Column{ColumnName: "OptionalID", OrdinalPosition: 3, DataType: "guid", IsNullable: true})
		before, _ := json.Marshal(m)
		source := renderContractSource(t, contract.backend, m)
		after, _ := json.Marshal(m)
		if string(before) != string(after) {
			t.Fatal("render modified the input manifest")
		}
		want := "string"
		if !strings.Contains(source, " "+want+" ") || !strings.Contains(source, "*"+want) {
			t.Fatalf("wrong UUID defaults:\n%s", source)
		}
		query := contractFunction(t, source, "SelectAll")
		if strings.Contains(query, "CAST(") {
			t.Fatalf("unexpected UUID projection: %s", query)
		}
		for _, name := range []string{"SelectCols", "Query"} {
			function := contractFunction(t, source, name)
			if strings.Count(function, `kind: "uuid"`) != 2 {
				t.Fatalf("%s must adapt both UUID fields:\n%s", name, function)
			}
		}
	})

	t.Run("binary defaults and overrides", func(t *testing.T) {
		for _, goType := range []string{"", "[]byte", "*[]byte", "json.RawMessage", "*json.RawMessage"} {
			for _, asKey := range []bool{false, true} {
				m := runtimeManifest()
				index := 1
				if asKey {
					index = 0
					m.Tables[0].Columns[0].IsIdentity = false
				}
				column := &m.Tables[0].Columns[index]
				column.DataType, column.IsNullable, column.GoType = "bytes", true, goType
				if strings.Contains(goType, "json.") {
					column.GoImport = "encoding/json"
				}
				source := renderContractSource(t, contract.backend, m)
				typeCheckContractSource(t, source)
				wantAdapter := contract.name == dialect.SQLServer && (goType == "" || goType == "*[]byte")
				for _, name := range []string{"Insert", "Update"} {
					if got := strings.Contains(contractFunction(t, source, name), "sqltomBinaryValue("); got != wantAdapter {
						t.Fatalf("%s binary argument adaptation for %q (key=%v): got %v, want %v", name, goType, asKey, got, wantAdapter)
					}
				}
				wantScan := contract.name == dialect.SQLite && !strings.Contains(goType, "json.")
				for _, name := range []string{"Query", "SelectCols"} {
					if got := strings.Contains(contractFunction(t, source, name), `kind: "binary"`); got != wantScan {
						t.Fatalf("%s binary scan adaptation for %q: got %v, want %v", name, goType, got, wantScan)
					}
				}
			}
		}
	})

	t.Run("binary helper identifiers", func(t *testing.T) {
		m := runtimeManifest()
		m.Tables[0].GoName = "sqltomBinaryValue"
		m.Tables[0].Columns[1].DataType, m.Tables[0].Columns[1].IsNullable = manifest.DataTypeBinary, true
		m.Tables[0].Columns = append(m.Tables[0].Columns, manifest.Column{ColumnName: "Clock", OrdinalPosition: 3, DataType: manifest.DataTypeDateTime, GoType: "sqltomBinaryValue_.Time", GoImport: "time"})
		typeCheckContractSource(t, renderContractSource(t, contract.backend, m))
	})

	t.Run("column override", func(t *testing.T) {
		m := surfaceManifest()
		m.Tables[0].Columns[1].GoType = "sql.NullString"
		m.Tables[0].Columns[1].GoImport = "database/sql"
		if source := renderContractSource(t, contract.backend, m); !strings.Contains(source, "Description sql.NullString") {
			t.Fatalf("column override not rendered:\n%s", source)
		}
	})

	t.Run("versioned import", func(t *testing.T) {
		m := surfaceManifest()
		m.TypeMappings[manifest.DataTypeString] = manifest.TypeMapping{
			GoType:           "string",
			NullableGoType:   "null.String",
			NullableGoImport: "github.com/guregu/null/v6",
		}
		source := renderContractSource(t, contract.backend, m)
		if !strings.Contains(source, `"github.com/guregu/null/v6"`) || !strings.Contains(source, "Description null.String") {
			t.Fatalf("versioned import not rendered:\n%s", source)
		}
	})

	t.Run("neutral source identity", func(t *testing.T) {
		m := surfaceManifest()
		m.Tables[0].TableCatalog = ""
		m.Tables[0].TableSchema = ""
		renderContractSource(t, contract.backend, m)
	})

	t.Run("nullable variant", func(t *testing.T) {
		m := surfaceManifest()
		m.Tables[0].Columns[1].DataType = manifest.DataTypeVariant
		source := renderContractSource(t, contract.backend, m)
		if !strings.Contains(source, "Description any") || strings.Contains(source, "Description *any") {
			t.Fatalf("nullable variant was not rendered as any:\n%s", source)
		}
	})

	t.Run("declaration validation", func(t *testing.T) {
		tests := []struct {
			name    string
			mutate  func(*manifest.Manifest)
			message string
		}{
			{name: "generated function", mutate: func(m *manifest.Manifest) { m.Tables[0].GoName = "SelectAll" }, message: "conflicts with a generated declaration"},
			{name: "generated method", mutate: func(m *manifest.Manifest) { m.Tables[0].Columns[1].GoName = "Insert" }, message: "conflicts with generated method Insert"},
			{name: "fixed import", mutate: func(m *manifest.Manifest) { m.Tables[0].GoName = "sql" }, message: `GoName "sql" conflicts with import "database/sql"`},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				m := surfaceManifest()
				test.mutate(m)
				err := contract.backend.Render(context.Background(), m, filepath.Join(t.TempDir(), "models"))
				if err == nil || !strings.Contains(err.Error(), test.message) {
					t.Fatalf("Render() error = %v, want substring %q", err, test.message)
				}
			})
		}
	})

	t.Run("failed render preserves output", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "models")
		if err := os.MkdirAll(output, 0o755); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(output, "keep.txt")
		if err := os.WriteFile(marker, []byte("previous"), 0o644); err != nil {
			t.Fatal(err)
		}
		m := surfaceManifest()
		m.Tables[0].Columns[0].DataType = "unknown_type"
		m.TypeMappings = map[string]manifest.TypeMapping{}
		if err := contract.backend.Render(context.Background(), m, output); err == nil {
			t.Fatal("expected render error")
		}
		assertContractFileContent(t, marker, "previous")
	})

	t.Run("canceled render preserves output", func(t *testing.T) {
		output := filepath.Join(t.TempDir(), "models")
		if err := contract.backend.Render(context.Background(), surfaceManifest(), output); err != nil {
			t.Fatal(err)
		}
		keep := filepath.Join(output, "keep.txt")
		if err := os.WriteFile(keep, []byte("previous"), 0o644); err != nil {
			t.Fatal(err)
		}
		m := surfaceManifest()
		m.Tables = nil
		ctx := &contractCancelContext{Context: context.Background(), cancelAt: 4}
		err := contract.backend.Render(ctx, m, output)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Render() error = %v, want context.Canceled", err)
		}
		assertContractFileContent(t, keep, "previous")
	})
}

func renderContractSource(t *testing.T, backend dialect.Backend, m *manifest.Manifest) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "models")
	if err := backend.Render(context.Background(), m, output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "Vehicle", "Vehicle.go"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runtimeManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Version:      manifest.CurrentVersion,
		DatabaseName: "MainDB",
		TypeMappings: map[string]manifest.TypeMapping{},
		Tables: []manifest.Table{{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Vehicle",
			TableType:    "BASE TABLE",
			IsManaged:    true,
			FileName:     "Vehicle",
			PackageName:  "vehicle",
			GoName:       "Vehicle",
			Columns: []manifest.Column{
				{
					ColumnName:        "ID",
					OrdinalPosition:   1,
					DataType:          manifest.DataTypeInteger,
					IsIdentity:        true,
					IsPrimaryKey:      true,
					PrimaryKeyOrdinal: 1,
					GoName:            "ID",
				},
				{
					ColumnName:      "Name",
					OrdinalPosition: 2,
					DataType:        manifest.DataTypeString,
					GoName:          "Name",
				},
			},
		}},
	}
}

func compositeKeyManifest() *manifest.Manifest {
	m := runtimeManifest()
	m.Tables[0].GoName = "CompositeKey"
	m.Tables[0].Columns = []manifest.Column{
		{
			ColumnName:        "TenantID",
			OrdinalPosition:   1,
			DataType:          manifest.DataTypeInteger,
			IsPrimaryKey:      true,
			PrimaryKeyOrdinal: 1,
			GoName:            "TenantID",
		},
		{
			ColumnName:        "ItemID",
			OrdinalPosition:   2,
			DataType:          manifest.DataTypeInteger,
			IsPrimaryKey:      true,
			PrimaryKeyOrdinal: 2,
			GoName:            "ItemID",
		},
		{
			ColumnName:      "Payload",
			OrdinalPosition: 3,
			DataType:        manifest.DataTypeString,
			GoName:          "Payload",
		},
	}
	return m
}

func identityOnlyManifest() *manifest.Manifest {
	m := runtimeManifest()
	m.Tables[0].GoName = "IdentityOnly"
	m.Tables[0].Columns = m.Tables[0].Columns[:1]
	return m
}

func nonKeyIdentityManifest() *manifest.Manifest {
	m := runtimeManifest()
	m.Tables[0].Columns = []manifest.Column{
		{
			ColumnName:        "NaturalKey",
			OrdinalPosition:   1,
			DataType:          manifest.DataTypeInteger,
			IsPrimaryKey:      true,
			PrimaryKeyOrdinal: 1,
			GoName:            "NaturalKey",
		},
		{
			ColumnName:      "Sequence",
			OrdinalPosition: 2,
			DataType:        manifest.DataTypeInteger,
			IsIdentity:      true,
			GoName:          "Sequence",
		},
		{
			ColumnName:      "Name",
			OrdinalPosition: 3,
			DataType:        manifest.DataTypeString,
			GoName:          "Name",
		},
	}
	return m
}

func multipleIdentityManifest() *manifest.Manifest {
	m := nonKeyIdentityManifest()
	m.Tables[0].Columns[1].ColumnName = "SequenceA"
	m.Tables[0].Columns[1].GoName = "SequenceA"
	m.Tables[0].Columns = append(m.Tables[0].Columns, manifest.Column{
		ColumnName:      "SequenceB",
		OrdinalPosition: 4,
		DataType:        manifest.DataTypeInteger,
		IsIdentity:      true,
		GoName:          "SequenceB",
	})
	return m
}

func typeCheckContractSource(t *testing.T, source string) {
	t.Helper()
	source += "\nvar _ DBTX = (*sql.DB)(nil)\nvar _ DBTX = (*sql.Tx)(nil)\n"
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "Vehicle.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse generated source: %v\n%s", err, source)
	}
	configuration := types.Config{Importer: importer.Default()}
	if _, err := configuration.Check("generated.test/vehicle", files, []*ast.File{file}, nil); err != nil {
		t.Fatalf("type-check generated source: %v\n%s", err, source)
	}
}

func contractFunction(t *testing.T, source, name string) string {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "Vehicle.go", source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name {
			continue
		}
		start := files.Position(function.Pos()).Offset
		end := files.Position(function.End()).Offset
		return source[start:end]
	}
	t.Fatalf("generated function %s not found", name)
	return ""
}

func contractQuery(t *testing.T, source, functionName string) string {
	t.Helper()
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "Vehicle.go", source, parser.AllErrors)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName || function.Body == nil {
			continue
		}
		var query string
		var found bool
		ast.Inspect(function.Body, func(node ast.Node) bool {
			specification, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for index, name := range specification.Names {
				if name.Name != "query" || index >= len(specification.Values) {
					continue
				}
				query, found = contractConstantString(specification.Values[index])
				return false
			}
			return true
		})
		if !found {
			t.Fatalf("constant query not found in %s", functionName)
		}
		return query
	}
	t.Fatalf("generated function %s not found", functionName)
	return ""
}

func contractConstantString(expression ast.Expr) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(value.Value)
		return unquoted, err == nil
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := contractConstantString(value.X)
		right, rightOK := contractConstantString(value.Y)
		return left + right, leftOK && rightOK
	case *ast.ParenExpr:
		return contractConstantString(value.X)
	default:
		return "", false
	}
}

type contractCancelContext struct {
	context.Context
	cancelAt int
	calls    int
}

func (c *contractCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func assertContractFileContent(t *testing.T, filename, want string) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", filename, data, want)
	}
}
