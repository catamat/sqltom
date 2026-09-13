package dialect_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
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

func TestRenderersPreserveTheSameGoSurface(t *testing.T) {
	backends := []struct {
		name    string
		backend dialect.Backend
	}{
		{name: dialect.SQLServer, backend: sqlserver.New()},
		{name: dialect.Postgres, backend: postgres.New()},
		{name: dialect.MySQL, backend: mysql.New()},
		{name: dialect.SQLite, backend: sqlite.New()},
	}
	document := surfaceManifest()
	var baseline string
	for index, entry := range backends {
		output := filepath.Join(t.TempDir(), entry.name)
		if err := entry.backend.Render(context.Background(), document, output); err != nil {
			t.Fatalf("%s Render() error = %v", entry.name, err)
		}
		filename := filepath.Join(output, "Vehicle", "Vehicle.go")
		surface := exportedSurface(t, filename)
		if index == 0 {
			baseline = surface
			continue
		}
		if surface != baseline {
			t.Errorf("%s generated Go surface differs from %s\n%s", entry.name, backends[0].name, surface)
		}
	}
}

func TestRenderersUseTheSameWritableColumnPolicy(t *testing.T) {
	backends := []struct {
		name    string
		backend dialect.Backend
	}{
		{name: dialect.SQLServer, backend: sqlserver.New()},
		{name: dialect.Postgres, backend: postgres.New()},
		{name: dialect.MySQL, backend: mysql.New()},
		{name: dialect.SQLite, backend: sqlite.New()},
	}

	for _, entry := range backends {
		t.Run(entry.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), entry.name)
			if err := entry.backend.Render(context.Background(), writableColumnsManifest(), output); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			filename := filepath.Join(output, "Vehicle", "Vehicle.go")
			insert := generatedFunction(t, filename, "Insert")
			update := generatedFunction(t, filename, "Update")

			for _, field := range []string{"Name", "Defaulted"} {
				if !strings.Contains(insert, "r."+field) || !strings.Contains(update, "r."+field) {
					t.Errorf("writable field %s is missing from Insert or Update", field)
				}
			}
			for _, field := range []string{"Computed", "Generated", "RowVersion"} {
				if strings.Contains(insert, field) || strings.Contains(update, field) {
					t.Errorf("database-generated field %s is present in Insert or Update", field)
				}
			}
		})
	}
}

func generatedFunction(t *testing.T, filename, name string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, filename, data, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != name {
			continue
		}
		start := files.Position(function.Pos()).Offset
		end := files.Position(function.End()).Offset
		return string(data[start:end])
	}
	t.Fatalf("generated function %s not found in %s", name, filename)
	return ""
}

func exportedSurface(t *testing.T, filename string) string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), filename, data, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	var result strings.Builder
	for _, declaration := range file.Decls {
		switch node := declaration.(type) {
		case *ast.GenDecl:
			if node.Tok != token.TYPE {
				continue
			}
			for _, specification := range node.Specs {
				typeSpecification, ok := specification.(*ast.TypeSpec)
				if !ok || !typeSpecification.Name.IsExported() {
					continue
				}
				writeNode(t, &result, typeSpecification)
			}
		case *ast.FuncDecl:
			if !node.Name.IsExported() {
				continue
			}
			copy := *node
			copy.Doc = nil
			copy.Body = nil
			writeNode(t, &result, &copy)
		}
	}
	return result.String()
}

func writeNode(t *testing.T, output *strings.Builder, node ast.Node) {
	t.Helper()
	var formatted bytes.Buffer
	if err := format.Node(&formatted, token.NewFileSet(), node); err != nil {
		t.Fatal(err)
	}
	output.Write(formatted.Bytes())
	output.WriteByte('\n')
}

func surfaceManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Version:      manifest.CurrentVersion,
		DatabaseName: "MainDB",
		TypeMappings: map[string]manifest.TypeMapping{
			manifest.DataTypeUUID: {
				GoType:           "uuid.UUID",
				GoImport:         "github.com/google/uuid",
				NullableGoType:   "null.UUID",
				NullableGoImport: "github.com/catamat/null",
			},
			manifest.DataTypeString: {
				GoType:           "string",
				NullableGoType:   "null.String",
				NullableGoImport: "github.com/catamat/null",
			},
		},
		Tables: []manifest.Table{{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "VEHICLE",
			TableType:    "BASE TABLE",
			IsManaged:    true,
			FileName:     "Vehicle",
			PackageName:  "vehicle",
			GoName:       "Vehicle",
			Columns: []manifest.Column{
				{
					ColumnName:        "FW_ID",
					OrdinalPosition:   1,
					DataType:          manifest.DataTypeUUID,
					IsIdentity:        true,
					IsPrimaryKey:      true,
					PrimaryKeyOrdinal: 1,
					GoName:            "ID",
					JSONName:          "id",
				},
				{
					ColumnName:      "DESCRIPTION",
					OrdinalPosition: 2,
					IsNullable:      true,
					DataType:        manifest.DataTypeString,
					GoName:          "Description",
					JSONName:        "description",
				},
			},
		}},
	}
}

func writableColumnsManifest() *manifest.Manifest {
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
					ColumnName:      "Name",
					OrdinalPosition: 1,
					DataType:        manifest.DataTypeString,
					GoName:          "Name",
				},
				{
					ColumnName:        "ID",
					OrdinalPosition:   2,
					DataType:          manifest.DataTypeInteger,
					IsIdentity:        true,
					IsPrimaryKey:      true,
					PrimaryKeyOrdinal: 1,
					GoName:            "ID",
				},
				{
					ColumnName:      "Defaulted",
					OrdinalPosition: 3,
					DataType:        manifest.DataTypeString,
					HasDefault:      true,
					GoName:          "Defaulted",
				},
				{
					ColumnName:      "Computed",
					OrdinalPosition: 4,
					DataType:        manifest.DataTypeString,
					IsComputed:      true,
					GoName:          "Computed",
				},
				{
					ColumnName:        "Generated",
					OrdinalPosition:   5,
					DataType:          manifest.DataTypeString,
					IsGeneratedAlways: true,
					GoName:            "Generated",
				},
				{
					ColumnName:      "RowVersion",
					OrdinalPosition: 6,
					DataType:        manifest.DataTypeRowVersion,
					IsRowVersion:    true,
					GoName:          "RowVersion",
				},
			},
		}},
	}
}
