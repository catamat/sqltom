package manifest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveTypePrecedence(t *testing.T) {
	m := validManifest()

	resolved, err := ResolveType(m, Column{DataType: DataTypeInteger, IsNullable: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != (ResolvedType{GoType: "*int"}) {
		t.Fatalf("built-in nullable type = %#v", resolved)
	}

	m.TypeMappings[DataTypeInteger] = TypeMapping{GoType: "int64"}
	resolved, err = ResolveType(m, Column{DataType: " INTEGER ", IsNullable: true})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != (ResolvedType{GoType: "*int64"}) {
		t.Fatalf("global nullable type = %#v", resolved)
	}

	column := Column{
		DataType:   DataTypeInteger,
		IsNullable: true,
		GoType:     "sql.NullInt64",
		GoImport:   "database/sql",
	}
	resolved, err = ResolveType(m, column)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != (ResolvedType{GoType: "sql.NullInt64", GoImport: "database/sql"}) {
		t.Fatalf("column override = %#v", resolved)
	}

	_, err = ResolveType(m, Column{DataType: "geography"})
	if err == nil || !strings.Contains(err.Error(), "unknown data type") {
		t.Fatalf("unknown type error = %v", err)
	}
}

func TestValidateStructureAllowsUnknownTypeButRenderDoesNot(t *testing.T) {
	m := validManifest()
	m.Tables[0].Columns[0].DataType = "geography"
	if err := ValidateStructure(m); err != nil {
		t.Fatalf("ValidateStructure() = %v", err)
	}
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "unknown data type") {
		t.Fatalf("ValidateForRender() = %v", err)
	}
}

func TestValidateForRenderAllowsRepeatedIgnoredJSONNames(t *testing.T) {
	m := validManifest()
	m.Tables[0].Columns[0].JSONName = "-"
	m.Tables[0].Columns = append(m.Tables[0].Columns, Column{
		ColumnName:      "InternalValue",
		OrdinalPosition: 2,
		DataType:        DataTypeString,
		JSONName:        "-",
	})
	if err := ValidateForRender(m); err != nil {
		t.Fatalf("ValidateForRender() rejected repeated ignored JSON names: %v", err)
	}
}

func TestValidateStructureRejectsNegativePrimaryKeyOrdinal(t *testing.T) {
	m := validManifest()
	m.Tables[0].Columns[0].IsPrimaryKey = false
	m.Tables[0].Columns[0].PrimaryKeyOrdinal = -1
	if err := ValidateStructure(m); err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("ValidateStructure() = %v", err)
	}
}

func TestValidateForRenderAcceptsVersionedImportPath(t *testing.T) {
	m := validManifest()
	m.Tables[0].Columns[0].DataType = DataTypeString
	m.TypeMappings[DataTypeString] = TypeMapping{
		GoType:   "null.String",
		GoImport: "github.com/guregu/null/v6",
	}
	if err := ValidateForRender(m); err != nil {
		t.Fatalf("ValidateForRender() rejected versioned import path: %v", err)
	}
}

func TestValidateGoImportUsesPortableToolchainRules(t *testing.T) {
	for _, goImport := range []string{
		"github.com/guregu/null/v6",
		"gopkg.in/yaml.v3",
		"example.com/compiler/c++",
	} {
		if err := validateGoImport(goImport); err != nil {
			t.Errorf("validateGoImport(%q) = %v", goImport, err)
		}
	}

	for _, goImport := range []string{
		"example.com/föö/pkg",
		"example.com/foo@v1/pkg",
		"-host.example/pkg",
		"example.com/CON/pkg",
		"example.com/foo~1/pkg",
		"example.com/foo./pkg",
		"example.com/foo//pkg",
		"example.com/../pkg",
	} {
		if err := validateGoImport(goImport); err == nil {
			t.Errorf("validateGoImport(%q) accepted an invalid import path", goImport)
		}
	}
}

func TestPartialTypeMappingsKeepOtherBuiltinDefaults(t *testing.T) {
	m := validManifest()
	m.TypeMappings[DataTypeUUID] = TypeMapping{GoType: "custom.UUID", GoImport: "example.com/custom"}
	resolved, err := ResolveType(m, Column{DataType: DataTypeInteger})
	if err != nil || resolved != (ResolvedType{GoType: "int"}) {
		t.Fatalf("ResolveType(integer) = %#v, %v", resolved, err)
	}
}

func TestMergeUsesFullTableIdentityAndPreservesOnlyOverrides(t *testing.T) {
	previous := validManifest()
	previous.TypeMappings[DataTypeInteger] = TypeMapping{GoType: "int64"}
	previous.Tables[0].FileName = "Vehicle"
	previous.Tables[0].PackageName = "VehiclePackage"
	previous.Tables[0].GoName = "VehicleModel"
	previous.Tables[0].Columns[0].GoName = "Identifier"
	previous.Tables[0].Columns[0].JSONName = "id"
	previous.Tables[0].Columns[0].GoType = "int64"

	fresh := validManifest()
	fresh.Tables[0].Columns[0].IsNullable = true
	fresh.Tables = append(fresh.Tables, Table{
		TableCatalog: "DB",
		TableSchema:  "audit",
		TableName:    "Vehicle",
		TableType:    "VIEW",
		IsManaged:    true,
		Columns: []Column{{
			ColumnName:      "FW_ID",
			OrdinalPosition: 1,
			DataType:        DataTypeInteger,
		}},
	})

	merged, err := Merge(fresh, previous)
	if err != nil {
		t.Fatal(err)
	}
	var dbo, audit Table
	for _, table := range merged.Tables {
		switch table.TableSchema {
		case "dbo":
			dbo = table
		case "audit":
			audit = table
		}
	}
	if dbo.FileName != "Vehicle" || dbo.PackageName != "VehiclePackage" || dbo.GoName != "VehicleModel" {
		t.Fatalf("table overrides not preserved: %#v", dbo)
	}
	if dbo.Columns[0].GoName != "Identifier" || dbo.Columns[0].JSONName != "id" || dbo.Columns[0].GoType != "int64" {
		t.Fatalf("column overrides not preserved: %#v", dbo.Columns[0])
	}
	if !dbo.Columns[0].IsNullable {
		t.Fatal("fresh database facts were not preserved")
	}
	if audit.FileName != "" || audit.PackageName != "" || audit.GoName != "" {
		t.Fatalf("table override leaked across schemas: %#v", audit)
	}
	if audit.Columns[0].GoName != "" || audit.Columns[0].JSONName != "" ||
		audit.Columns[0].GoType != "" || audit.Columns[0].GoImport != "" {
		t.Fatalf("column override leaked across schemas: %#v", audit.Columns[0])
	}
	if merged.TypeMappings[DataTypeInteger].GoType != "int64" {
		t.Fatalf("type mappings = %#v", merged.TypeMappings)
	}
}

func TestCanonicalizeKeepsColumnNamingOverridesLocal(t *testing.T) {
	m := validManifest()
	m.Tables[0].Columns[0].GoName = "ID"
	m.Tables[0].Columns[0].JSONName = "id"
	m.Tables = append(m.Tables, Table{
		TableCatalog: "DB",
		TableSchema:  "audit",
		TableName:    "VehicleLog",
		TableType:    "VIEW",
		IsManaged:    true,
		Columns: []Column{{
			ColumnName:      "FW_ID",
			OrdinalPosition: 1,
			DataType:        DataTypeInteger,
		}},
	})

	Canonicalize(m)

	var dbo, audit Column
	for _, table := range m.Tables {
		switch table.TableSchema {
		case "dbo":
			dbo = table.Columns[0]
		case "audit":
			audit = table.Columns[0]
		}
	}
	if dbo.GoName != "ID" || dbo.JSONName != "id" {
		t.Fatalf("local column overrides were changed: %#v", dbo)
	}
	if audit.GoName != "" || audit.JSONName != "" {
		t.Fatalf("column naming override leaked to another table: %#v", audit)
	}
}

func TestNamingPreservesCaseAndSanitizesOnlyInvalidCharacters(t *testing.T) {
	tests := map[string]string{
		"AN01_MEZZI": "AN01_MEZZI",
		"9-My Name":  "_9MyName",
		"type":       "type_",
		"Città":      "Città",
	}
	for input, want := range tests {
		got, err := SanitizeGoIdentifier(input)
		if err != nil {
			t.Fatalf("SanitizeGoIdentifier(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("SanitizeGoIdentifier(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := SanitizeGoIdentifier("---"); err == nil {
		t.Fatal("expected empty sanitized identifier error")
	}
	if err := ValidateFileComponent("../model"); err == nil {
		t.Fatal("expected path traversal error")
	}
	filename, err := ManifestFilename("Main DB")
	if err != nil {
		t.Fatal(err)
	}
	if filename != "sqltom_Main DB.json" {
		t.Fatalf("ManifestFilename() = %q", filename)
	}
}

func TestLoadIsStrictAndSaveAtomicProducesRegularJSON(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "manifest.json")
	m := validManifest()
	if err := SaveAtomic(filename, m); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != CurrentVersion || loaded.TypeMappings == nil || loaded.Tables == nil {
		t.Fatalf("loaded manifest = %#v", loaded)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("manifest permissions = %o", info.Mode().Perm())
	}
	saved, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), `"Dialect"`) ||
		!strings.Contains(string(saved), `"Version": 1`) ||
		!strings.Contains(string(saved), `"IsManaged": true`) {
		t.Fatalf("saved manifest is not the current dialect-neutral format:\n%s", saved)
	}

	invalid := filepath.Join(directory, "invalid.json")
	data := []byte(`{"Version":1,"Dialect":"sqlserver","DatabaseName":"DB","TypeMappings":{},"Tables":[]}`)
	if err := os.WriteFile(invalid, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(invalid); err == nil || !strings.Contains(err.Error(), "unknown object member") {
		t.Fatalf("Load(unknown field) = %v", err)
	}

	legacy := filepath.Join(directory, "missing-is-managed.json")
	withoutIsManaged := strings.Replace(string(saved), "      \"IsManaged\": true,\n", "", 1)
	if withoutIsManaged == string(saved) {
		t.Fatal("test fixture did not remove IsManaged")
	}
	if err := os.WriteFile(legacy, []byte(withoutIsManaged), 0o644); err != nil {
		t.Fatal(err)
	}
	defaulted, err := Load(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(defaulted.Tables) != 1 || !defaulted.Tables[0].IsManaged {
		t.Fatalf("missing IsManaged did not default to true: %#v", defaulted.Tables)
	}

	unknownTableField := filepath.Join(directory, "unknown-table-field.json")
	withUnknownTableField := strings.Replace(
		string(saved),
		"      \"IsManaged\": true,\n",
		"      \"IsManaged\": true,\n      \"Unexpected\": true,\n",
		1,
	)
	if err := os.WriteFile(unknownTableField, []byte(withUnknownTableField), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknownTableField); err == nil || !strings.Contains(err.Error(), "unknown object member") {
		t.Fatalf("Load(unknown table field) = %v", err)
	}
}

func TestSaveAtomicPreservesExistingPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX file permission bits")
	}
	filename := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(filename, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveAtomic(filename, validManifest()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadRejectsNonStrictJSON(t *testing.T) {
	directory := t.TempDir()
	validFilename := filepath.Join(directory, "valid.json")
	if err := SaveAtomic(validFilename, validManifest()); err != nil {
		t.Fatal(err)
	}
	validData, err := os.ReadFile(validFilename)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		old     string
		replace string
	}{
		{name: "null management", old: `"IsManaged": true`, replace: `"IsManaged": null`},
		{name: "null column fact", old: `"IsNullable": false`, replace: `"IsNullable": null`},
		{name: "wrong root field case", old: `"Version": 1`, replace: `"version": 1`},
		{name: "wrong table field case", old: `"IsManaged": true`, replace: `"isManaged": true`},
		{name: "duplicate root field", old: `"Version": 1`, replace: `"Version": 1, "Version": 1`},
		{name: "duplicate table field", old: `"IsManaged": true`, replace: `"IsManaged": true, "IsManaged": true`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			modified := strings.Replace(string(validData), test.old, test.replace, 1)
			if modified == string(validData) {
				t.Fatalf("fixture does not contain %q", test.old)
			}
			filename := filepath.Join(directory, strings.ReplaceAll(test.name, " ", "-")+".json")
			if err := os.WriteFile(filename, []byte(modified), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(filename); err == nil {
				t.Fatal("Load() accepted non-strict JSON")
			}
		})
	}
}

func TestUnmanagedTableIsCanonicalizedAndCanBeReenabledByMerge(t *testing.T) {
	previous := validManifest()
	previous.Tables[0].FileName = "VehicleFile"
	previous.Tables[0].PackageName = "vehiclepackage"
	previous.Tables[0].GoName = "VehicleModel"
	previous.Tables[0].Columns[0].GoName = "ID"
	previous.Tables[0].IsManaged = false
	Canonicalize(previous)

	if table := previous.Tables[0]; table.FileName != "" || table.PackageName != "" || table.GoName != "" || table.Columns == nil || len(table.Columns) != 0 {
		t.Fatalf("unmanaged table was not reduced to an identity stub: %#v", table)
	}

	unmanaged, err := Merge(validManifest(), previous)
	if err != nil {
		t.Fatal(err)
	}
	if table := unmanaged.Tables[0]; table.IsManaged || len(table.Columns) != 0 {
		t.Fatalf("merge did not preserve unmanaged state: %#v", table)
	}

	unmanaged.Tables[0].IsManaged = true
	restored, err := Merge(validManifest(), unmanaged)
	if err != nil {
		t.Fatal(err)
	}
	table := restored.Tables[0]
	if !table.IsManaged || len(table.Columns) != 1 {
		t.Fatalf("re-enabled table was not refreshed from inspection: %#v", table)
	}
	if table.FileName != "" || table.PackageName != "" || table.GoName != "" || table.Columns[0].GoName != "" {
		t.Fatalf("re-enabled table recovered discarded overrides: %#v", table)
	}
}

func TestSaveAtomicClearsUnmanagedTableDetails(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "manifest.json")
	m := validManifest()
	m.Tables[0].IsManaged = false
	m.Tables[0].FileName = "VehicleFile"
	m.Tables[0].PackageName = "vehiclepackage"
	m.Tables[0].GoName = "VehicleModel"
	if err := SaveAtomic(filename, m); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	table := loaded.Tables[0]
	if table.IsManaged || table.FileName != "" || table.PackageName != "" || table.GoName != "" || table.Columns == nil || len(table.Columns) != 0 {
		t.Fatalf("saved unmanaged table = %#v", table)
	}
	saved, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `"IsManaged": false`) || !strings.Contains(string(saved), `"Columns": []`) {
		t.Fatalf("unmanaged table was not serialized as a stub:\n%s", saved)
	}
}

func TestReenabledEmptyTableRequiresInspectionBeforeRender(t *testing.T) {
	m := validManifest()
	m.Tables[0].IsManaged = false
	Canonicalize(m)
	m.Tables[0].IsManaged = true

	if err := ValidateStructure(m); err != nil {
		t.Fatalf("transitional managed stub is structurally invalid: %v", err)
	}
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "Columns is empty") {
		t.Fatalf("ValidateForRender() = %v", err)
	}
}

func validManifest() *Manifest {
	return &Manifest{
		Version:      CurrentVersion,
		DatabaseName: "DB",
		TypeMappings: map[string]TypeMapping{},
		Tables: []Table{{
			TableCatalog: "DB",
			TableSchema:  "dbo",
			TableName:    "Vehicle",
			TableType:    "BASE TABLE",
			IsManaged:    true,
			Columns: []Column{{
				ColumnName:        "FW_ID",
				OrdinalPosition:   1,
				DataType:          DataTypeInteger,
				IsPrimaryKey:      true,
				PrimaryKeyOrdinal: 1,
			}},
		}},
	}
}
