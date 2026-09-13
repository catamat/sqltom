package dialect_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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
		},
		{
			name:                  dialect.MySQL,
			backend:               mysql.New(),
			poisonTableName:       "Ta]ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co]l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  "Ta]ble``",
			escapedColumnFragment: "Co]l``",
			qualifiedTarget:       "` + \"`dbo`.`VEHICLE`\" + `",
		},
		{
			name:                  dialect.SQLite,
			backend:               sqlite.New(),
			poisonTableName:       "Ta\"ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co\"l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  `Ta\"\"ble`,
			escapedColumnFragment: `Co\"\"l`,
			qualifiedTarget:       "` + \"\\\"dbo\\\".\\\"VEHICLE\\\"\" + `",
		},
		{
			name:                  dialect.SQLServer,
			backend:               sqlserver.New(),
			poisonTableName:       "Ta]ble`\nvar Injected = true\n//",
			poisonColumnName:      "Co]l`\nvar ColumnInjected = true\n//",
			escapedTableFragment:  "[Ta]]ble",
			escapedColumnFragment: "[Co]]l",
			qualifiedTarget:       "` + \"[MainDB].[dbo].[VEHICLE]\" + `",
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

	t.Run("legacy model surface", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, surfaceManifest())
		for _, fragment := range []string{
			"import (\n\t\"database/sql\"\n\t\"github.com/catamat/null\"\n\t\"github.com/google/uuid\"\n\t\"strings\"\n)",
			"type Vehicle struct {\n\tID          uuid.UUID   `db:\"FW_ID\" json:\"id\"`\n\tDescription null.String `db:\"DESCRIPTION\" json:\"description\"`\n}",
			"func Select(",
			"func (r *Vehicle) Insert(",
			"func (r *Vehicle) Update(",
			"func Delete(",
			"func Exists(",
			"func Query(",
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

	t.Run("qualified objects", func(t *testing.T) {
		source := renderContractSource(t, contract.backend, surfaceManifest())
		for _, operation := range []string{"FROM", "INSERT INTO", "UPDATE", "DELETE FROM"} {
			fragment := operation + " " + contract.qualifiedTarget
			if !strings.Contains(source, fragment) {
				t.Errorf("generated source does not qualify %s target; missing %q\n%s", operation, fragment, source)
			}
		}
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
			{name: "generated function", mutate: func(m *manifest.Manifest) { m.Tables[0].GoName = "Select" }, message: "conflicts with a generated function"},
			{name: "generated method", mutate: func(m *manifest.Manifest) { m.Tables[0].Columns[1].GoName = "Insert" }, message: "conflicts with generated method Insert"},
			{name: "fixed import", mutate: func(m *manifest.Manifest) { m.Tables[0].GoName = "sql" }, message: `GoName "sql" conflicts with import "database/sql"`},
			{
				name: "missing compatibility index",
				mutate: func(m *manifest.Manifest) {
					m.Tables[0].Columns[0].ColumnName = "ID"
					m.Tables[0].Columns[0].IsPrimaryKey = false
					m.Tables[0].Columns[0].PrimaryKeyOrdinal = 0
				},
				message: "has no primary key and no FW_ID compatibility column",
			},
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
