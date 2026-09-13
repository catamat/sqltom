package manifest

import (
	"strings"
	"testing"
)

func TestNewIncludesDatabaseIdentity(t *testing.T) {
	m := New("MainDB", nil)
	if m.DatabaseName != "MainDB" {
		t.Fatalf("DatabaseName = %q", m.DatabaseName)
	}
	if err := ValidateStructure(m); err != nil {
		t.Fatalf("ValidateStructure(New()) = %v", err)
	}
}

func TestValidateStructureRequiresDatabaseIdentityButNotDialectSpecificLocation(t *testing.T) {
	t.Run("missing database", func(t *testing.T) {
		m := validManifest()
		m.DatabaseName = ""
		if err := ValidateStructure(m); err == nil || !strings.Contains(err.Error(), "DatabaseName is empty") {
			t.Fatalf("ValidateStructure() = %v", err)
		}
	})

	t.Run("catalog and schema are optional and independent", func(t *testing.T) {
		m := validManifest()
		m.DatabaseName = "OtherDB"
		m.Tables[0].TableCatalog = ""
		m.Tables[0].TableSchema = ""
		if err := ValidateStructure(m); err != nil {
			t.Fatalf("ValidateStructure() = %v", err)
		}
	})
}

func TestMergeRejectsDifferentDatabase(t *testing.T) {
	previous := validManifest()
	fresh := validManifest()
	fresh.DatabaseName = "OtherDB"
	fresh.Tables[0].TableCatalog = "OtherDB"

	_, err := Merge(fresh, previous)
	if err == nil || !strings.Contains(err.Error(), "DatabaseName changed") {
		t.Fatalf("Merge() = %v", err)
	}
}

func TestMergeKeepsFreshGeneratedAlwaysFact(t *testing.T) {
	previous := validManifest()
	previous.Tables[0].Columns[0].IsGeneratedAlways = true
	fresh := validManifest()

	merged, err := Merge(fresh, previous)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Tables[0].Columns[0].IsGeneratedAlways {
		t.Fatal("IsGeneratedAlways was copied from the previous manifest")
	}
}

func TestPortableFileNames(t *testing.T) {
	sanitizeCases := map[string]string{
		"_private":  "private",
		".hidden":   "hidden",
		"CON":       "CON_",
		"nul.json":  "nul_.json",
		"__COM1.go": "COM1_.go",
	}
	for input, want := range sanitizeCases {
		got, err := SanitizeFileComponent(input)
		if err != nil {
			t.Fatalf("SanitizeFileComponent(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("SanitizeFileComponent(%q) = %q, want %q", input, got, want)
		}
	}

	for _, value := range []string{"_ignored", ".ignored", "CON", "con.txt", "COM1", "lpt9.go"} {
		if err := ValidateFileComponent(value); err == nil {
			t.Errorf("ValidateFileComponent(%q) accepted an unsafe name", value)
		}
	}
}

func TestFileCollisionKeyNormalizesCaseAndUnicode(t *testing.T) {
	composed := "Caf\u00e9"
	decomposed := "Cafe\u0301"
	if FileCollisionKey(composed) != FileCollisionKey(decomposed) {
		t.Fatal("canonically equivalent Unicode names have different collision keys")
	}
	if FileCollisionKey("VEHICLE") != FileCollisionKey("vehicle") {
		t.Fatal("case variants have different collision keys")
	}

	m := validManifest()
	m.Tables[0].TableName = composed
	second := m.Tables[0]
	second.TableName = decomposed
	m.Tables = append(m.Tables, second)
	if err := ValidateForRender(m); err == nil || !strings.Contains(err.Error(), "same output name") {
		t.Fatalf("ValidateForRender() = %v", err)
	}
}
