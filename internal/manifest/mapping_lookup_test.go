package manifest

import (
	"strings"
	"testing"
)

func TestResolveTypePrefersExactCaseSensitiveMapping(t *testing.T) {
	m := validManifest()
	m.TypeMappings = map[string]TypeMapping{
		"[dbo].[MyType]": {GoType: "ExactUpper"},
		"[dbo].[mytype]": {GoType: "ExactLower"},
	}
	if err := ValidateStructure(m); err != nil {
		t.Fatalf("case-distinct mappings rejected: %v", err)
	}

	upper, err := ResolveType(m, Column{DataType: " [dbo].[MyType] "})
	if err != nil || upper.GoType != "ExactUpper" {
		t.Fatalf("upper exact mapping = %#v, %v", upper, err)
	}
	lower, err := ResolveType(m, Column{DataType: "[dbo].[mytype]"})
	if err != nil || lower.GoType != "ExactLower" {
		t.Fatalf("lower exact mapping = %#v, %v", lower, err)
	}
}

func TestResolveTypeUsesOnlyUnambiguousNormalizedFallback(t *testing.T) {
	m := validManifest()
	m.TypeMappings = map[string]TypeMapping{
		"[dbo].[MyType]": {GoType: "OnlyMapping"},
	}
	resolved, err := ResolveType(m, Column{DataType: "[DBO].[MYTYPE]"})
	if err != nil || resolved.GoType != "OnlyMapping" {
		t.Fatalf("normalized fallback = %#v, %v", resolved, err)
	}

	m.TypeMappings["[dbo].[mytype]"] = TypeMapping{GoType: "OtherMapping"}
	_, err = ResolveType(m, Column{DataType: "[DBO].[MYTYPE]"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous TypeMappings") {
		t.Fatalf("ambiguous normalized fallback = %v", err)
	}
	if !strings.Contains(err.Error(), "[dbo].[MyType]") || !strings.Contains(err.Error(), "[dbo].[mytype]") {
		t.Fatalf("ambiguous error omits candidates: %v", err)
	}
}

func TestTypeMappingKeysUseTrimmedExactIdentity(t *testing.T) {
	m := validManifest()
	m.TypeMappings = map[string]TypeMapping{
		" [dbo].[MyType] ": {GoType: "TrimmedMapping"},
	}
	if err := ValidateStructure(m); err != nil {
		t.Fatalf("trimmed key rejected: %v", err)
	}
	resolved, err := ResolveType(m, Column{DataType: "[dbo].[MyType]"})
	if err != nil || resolved.GoType != "TrimmedMapping" {
		t.Fatalf("trimmed exact mapping = %#v, %v", resolved, err)
	}

	m.TypeMappings["[dbo].[MyType]"] = TypeMapping{GoType: "Duplicate"}
	if err := ValidateStructure(m); err == nil || !strings.Contains(err.Error(), "collide after trimming") {
		t.Fatalf("trimmed duplicate validation = %v", err)
	}
}
