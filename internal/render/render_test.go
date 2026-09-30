package render

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/catamat/sqltom/internal/manifest"
)

func TestRenderBuildsFormattedManagedOutput(t *testing.T) {
	document := renderTestManifest()
	output := filepath.Join(t.TempDir(), "models")
	templates := fstest.MapFS{
		"model.go.tpl": &fstest.MapFile{Data: []byte("package {{.Table.PackageName}}\n\ntype {{.Table.GoName}} struct{}\n")},
	}
	err := Render(context.Background(), document, output, Config{
		Dialect:            "SQLite",
		TemplateFS:         templates,
		TemplateName:       "model.go.tpl",
		SQLIdentifier:      func(value string) string { return value },
		SQLTableIdentifier: func(_, _, name string) string { return name },
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "Vehicle", "Vehicle.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package vehicle\n\ntype Vehicle struct{}\n" {
		t.Fatalf("generated source = %q", data)
	}
}

func TestBuildDataUsesOneSharedWritableColumnPolicy(t *testing.T) {
	document := renderTestManifest()
	document.Tables[0].Columns = append(document.Tables[0].Columns,
		manifest.Column{ColumnName: "Computed", OrdinalPosition: 2, DataType: manifest.DataTypeString, IsComputed: true},
		manifest.Column{ColumnName: "Defaulted", OrdinalPosition: 3, DataType: manifest.DataTypeString, HasDefault: true},
	)
	data, err := BuildData(document, document.Tables[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Table.Columns) != 3 || len(data.Table.WriteColumns) != 1 || data.Table.WriteColumns[0].ColumnName != "Defaulted" {
		t.Fatalf("template columns = %#v", data.Table)
	}
	if len(data.Table.UpdateColumns) != 1 || data.Table.UpdateColumns[0].ColumnName != "Defaulted" {
		t.Fatalf("update columns = %#v", data.Table.UpdateColumns)
	}
	if len(data.Table.KeyColumns) != 1 || data.Table.KeyColumns[0].ColumnName != "ID" {
		t.Fatalf("key columns = %#v", data.Table.KeyColumns)
	}
	if len(data.IdentityColumns) != 1 || data.IdentityColumns[0].ColumnName != "ID" {
		t.Fatalf("identity columns = %#v", data.IdentityColumns)
	}
}

func TestValidateRejectsGeneratedDeclarationConflicts(t *testing.T) {
	document := renderTestManifest()
	document.Tables[0].GoName = "SelectAll"
	if err := Validate(document); err == nil || !strings.Contains(err.Error(), "conflicts with a generated declaration") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsDatabaseSQLWithAnotherQualifier(t *testing.T) {
	document := renderTestManifest()
	document.Tables[0].Columns[0].GoType = "database.NullInt64"
	document.Tables[0].Columns[0].GoImport = "database/sql"
	if err := Validate(document); err == nil || !strings.Contains(err.Error(), `must use qualifier "sql"`) {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsPredeclaredImportQualifier(t *testing.T) {
	document := renderTestManifest()
	document.Tables[0].Columns[0].GoType = "string.Value"
	document.Tables[0].Columns[0].GoImport = "example.com/string"
	if err := Validate(document); err == nil || !strings.Contains(err.Error(), `qualifier "string" is predeclared`) {
		t.Fatalf("Validate() error = %v", err)
	}
}

func renderTestManifest() *manifest.Manifest {
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
			Columns: []manifest.Column{{
				ColumnName:        "ID",
				OrdinalPosition:   1,
				DataType:          manifest.DataTypeInteger,
				IsIdentity:        true,
				IsPrimaryKey:      true,
				PrimaryKeyOrdinal: 1,
				GoName:            "ID",
			}},
		}},
	}
}
