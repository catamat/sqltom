package manifest

import (
	"strings"
	"testing"
)

func TestFileCollisionKeyUsesFullUnicodeCaseFold(t *testing.T) {
	if got, want := FileCollisionKey("Straße"), FileCollisionKey("STRASSE"); got != want {
		t.Fatalf("FileCollisionKey() = %q and %q", got, want)
	}
	if got, want := FileCollisionKey("Caf\u00e9"), FileCollisionKey("Cafe\u0301"); got != want {
		t.Fatalf("canonically equivalent keys = %q and %q", got, want)
	}
}

func TestGoOutputFileNameSanitization(t *testing.T) {
	tests := map[string]string{
		"AN01_MEZZI":            "AN01_MEZZI",
		"My Table":              "MyTable",
		"Città":                 "Citt",
		"_private":              "private",
		"-private":              "private",
		"foo..bar":              "foo.bar",
		"foo~1.txt":             "foo~1_.txt",
		"CON":                   "CON_",
		"COM¹":                  "COM",
		"Vendor":                "Vendor_",
		"testdata":              "testdata_",
		"model_test":            "model_test_",
		"model_windows":         "model_windows_",
		"model_amd64":           "model_amd64_",
		"model_linux_amd64":     "model_linux_amd64_",
		"model_linux.meta":      "model_linux_.meta",
		"model_linux_test.meta": "model_linux_test_.meta",
	}
	for input, want := range tests {
		got, err := SanitizeFileComponent(input)
		if err != nil {
			t.Fatalf("SanitizeFileComponent(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("SanitizeFileComponent(%q) = %q, want %q", input, got, want)
		}
		if err := ValidateFileComponent(got); err != nil {
			t.Errorf("sanitized %q is invalid: %v", got, err)
		}
	}
}

func TestExplicitGoOutputFileNameValidation(t *testing.T) {
	for _, valid := range []string{"Vehicle", "model-name", "model.name", "model_1", "model~name", "model+name"} {
		if err := ValidateFileComponent(valid); err != nil {
			t.Errorf("ValidateFileComponent(%q) = %v", valid, err)
		}
	}

	invalid := []string{
		".ignored", "_ignored", "-argument", "trailing.", "two..dots",
		"with space", "Città", "model@name", "model~1", "CON", "com1.txt",
		"vendor", "TestData", "model_test", "model_windows", "model_amd64",
		"model_linux_amd64", "model_linux.meta", strings.Repeat("a", maxGoFileStemBytes+1),
	}
	for _, value := range invalid {
		if err := ValidateFileComponent(value); err == nil {
			t.Errorf("ValidateFileComponent(%q) accepted an unsafe Go output name", value)
		}
	}
}

func TestLongGoOutputFileNameIsStableAndDistinct(t *testing.T) {
	first, err := SanitizeFileComponent(strings.Repeat("a", 300) + "x")
	if err != nil {
		t.Fatal(err)
	}
	second, err := SanitizeFileComponent(strings.Repeat("a", 300) + "y")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("long names collided: %q", first)
	}
	if len(first) > maxGoFileStemBytes || len(first+".go") > maxPortableComponentBytes {
		t.Fatalf("sanitized name is too long: stem=%d source=%d", len(first), len(first+".go"))
	}
	again, err := SanitizeFileComponent(strings.Repeat("a", 300) + "x")
	if err != nil || again != first {
		t.Fatalf("sanitization is not stable: %q, %q, %v", first, again, err)
	}
}

func TestValidateForRenderUsesSafeDerivedFileNameAndDetectsSanitizedCollision(t *testing.T) {
	m := validManifest()
	m.Tables[0].TableName = "Order Item"
	if err := ValidateForRender(m); err != nil {
		t.Fatalf("ValidateForRender(derived safe name) = %v", err)
	}
	if got, err := EffectiveFileName(m.Tables[0]); err != nil || got != "OrderItem" {
		t.Fatalf("EffectiveFileName() = %q, %v", got, err)
	}

	m = validManifest()
	m.Tables[0].TableName = "A/B"
	second := m.Tables[0]
	second.TableName = "AB"
	m.Tables = append(m.Tables, second)
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "same output name") {
		t.Fatalf("ValidateForRender(sanitized collision) = %v", err)
	}
}

func TestManifestFilenameUsesIndependentPortableSanitization(t *testing.T) {
	unchanged := map[string]string{
		"Main DB": "sqltom_Main DB.json",
		"_":       "sqltom__.json",
		".Main":   "sqltom_.Main.json",
		"CON":     "sqltom_CON.json",
	}
	for database, want := range unchanged {
		got, err := ManifestFilename(database)
		if err != nil {
			t.Fatalf("ManifestFilename(%q): %v", database, err)
		}
		if got != want {
			t.Errorf("ManifestFilename(%q) = %q, want %q", database, got, want)
		}
	}

	unsafe, err := ManifestFilename("A/B")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ManifestFilename("AB")
	if err != nil {
		t.Fatal(err)
	}
	if unsafe == plain || !strings.HasPrefix(unsafe, "sqltom_AB_") {
		t.Fatalf("sanitized filenames = %q and %q", unsafe, plain)
	}
	again, err := ManifestFilename("A/B")
	if err != nil || again != unsafe {
		t.Fatalf("ManifestFilename is not stable: %q, %q, %v", unsafe, again, err)
	}

	longName, err := ManifestFilename(strings.Repeat("界", 128))
	if err != nil {
		t.Fatal(err)
	}
	if !portableComponentFits(longName, maxPortableComponentBytes, maxPortableComponentBytes) {
		t.Fatalf("long manifest filename is not portable: bytes=%d, name=%q", len(longName), longName)
	}
}

func TestReservedPackageAndTableNames(t *testing.T) {
	for _, tableName := range []string{"main", "init"} {
		got, err := EffectivePackageName(Table{TableName: tableName})
		if err != nil || got != tableName+"_" {
			t.Errorf("EffectivePackageName(%q) = %q, %v", tableName, got, err)
		}
		if _, err := EffectivePackageName(Table{TableName: "Vehicle", PackageName: tableName}); err == nil {
			t.Errorf("explicit PackageName %q was accepted", tableName)
		}
	}

	for _, tableName := range []string{"string", "error", "bool", "nil", "true", "int", "append", "init"} {
		got, err := EffectiveTableGoName(Table{TableName: tableName})
		if err != nil || got != tableName+"_" {
			t.Errorf("EffectiveTableGoName(%q) = %q, %v", tableName, got, err)
		}
		if _, err := EffectiveTableGoName(Table{TableName: "Vehicle", GoName: tableName}); err == nil {
			t.Errorf("explicit GoName %q was accepted", tableName)
		}
	}

	if got, err := EffectiveColumnGoName(Column{ColumnName: "string"}); err != nil || got != "string" {
		t.Fatalf("column predeclared name was changed: %q, %v", got, err)
	}

	m := validManifest()
	m.Tables[0].TableName = "string"
	if err := ValidateForRender(m); err != nil {
		t.Fatalf("ValidateForRender(default predeclared name) = %v", err)
	}
	m.Tables[0].GoName = "string"
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("ValidateForRender(explicit predeclared name) = %v", err)
	}
}

func TestValidateForRenderHasNoBackendGeneratedNameOrImportAssumptions(t *testing.T) {
	m := validManifest()
	m.Tables[0].GoName = "Select"
	m.Tables[0].Columns[0].GoName = "Insert"
	m.Tables[0].Columns[0].GoType = "sql.Value"
	m.Tables[0].Columns[0].GoImport = "example.com/sql"
	if err := ValidateForRender(m); err != nil {
		t.Fatalf("generic names and import rejected: %v", err)
	}
}

func TestValidateForRenderDetectsOnlyEffectiveImportCollisions(t *testing.T) {
	m := validManifest()
	m.Tables[0].GoName = "foo"
	m.Tables[0].Columns[0].GoType = "foo.Value"
	m.Tables[0].Columns[0].GoImport = "example.com/foo"
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "conflicts with import") {
		t.Fatalf("table/import collision = %v", err)
	}

	m = validManifest()
	m.Tables[0].Columns = append(m.Tables[0].Columns, Column{
		ColumnName:      "OTHER",
		OrdinalPosition: 2,
		DataType:        DataTypeString,
		GoType:          "foo.Other",
		GoImport:        "other.example/foo",
	})
	m.Tables[0].Columns[0].GoType = "foo.Value"
	m.Tables[0].Columns[0].GoImport = "example.com/foo"
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "same qualifier") {
		t.Fatalf("effective import collision = %v", err)
	}
}
