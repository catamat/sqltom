package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDialectDefaultsPreserveTypePrecedenceAndManifest(t *testing.T) {
	defaults := map[string]TypeMapping{DataTypeUUID: {GoType: "native.UUID", GoImport: "example.com/native"}}
	for _, test := range []struct {
		name     string
		column   Column
		mappings map[string]TypeMapping
		want     ResolvedType
	}{
		{"default", Column{DataType: "uuid"}, nil, ResolvedType{"native.UUID", "example.com/native"}},
		{"nullable default", Column{DataType: "UUID", IsNullable: true}, nil, ResolvedType{"*native.UUID", "example.com/native"}},
		{"alias", Column{DataType: " guid "}, nil, ResolvedType{"native.UUID", "example.com/native"}},
		{"other type", Column{DataType: "integer"}, nil, ResolvedType{GoType: "int"}},
		{"mapping", Column{DataType: "uuid"}, map[string]TypeMapping{" UUID ": {GoType: "app.UUID", GoImport: "example.com/app"}}, ResolvedType{"app.UUID", "example.com/app"}},
		{"nullable mapping", Column{DataType: "uuid", IsNullable: true}, map[string]TypeMapping{"uuid": {GoType: "app.UUID", GoImport: "example.com/app", NullableGoType: "app.NullUUID", NullableGoImport: "example.com/app"}}, ResolvedType{"app.NullUUID", "example.com/app"}},
		{"column", Column{DataType: "uuid", GoType: "string"}, map[string]TypeMapping{"uuid": {GoType: "app.UUID", GoImport: "example.com/app"}}, ResolvedType{GoType: "string"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := New("test", []Table{{TableName: "Items", Columns: []Column{test.column}}})
			m.TypeMappings = test.mappings
			before, _ := json.Marshal(m)
			effective, err := WithTypeDefaults(m, defaults)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ResolveType(effective, test.column)
			if err != nil || got != test.want {
				t.Fatalf("ResolveType = %#v, %v; want %#v", got, err, test.want)
			}
			after, _ := json.Marshal(m)
			if string(before) != string(after) {
				t.Fatal("dialect defaults modified the caller's manifest")
			}
		})
	}
}

func TestDialectDefaultsDoNotResolveAmbiguousOverrides(t *testing.T) {
	m := New("test", []Table{{TableName: "Items", Columns: []Column{{ColumnName: "ID", DataType: "Uuid"}}}})
	m.TypeMappings = map[string]TypeMapping{"uuid": {GoType: "string"}, "UUID": {GoType: "[]byte"}}
	_, err := WithTypeDefaults(m, map[string]TypeMapping{DataTypeUUID: {GoType: "native.UUID"}})
	if err == nil || !strings.Contains(err.Error(), "ambiguous TypeMappings") {
		t.Fatalf("defaults masked ambiguity: %v", err)
	}
	if result, err := WithTypeDefaults(nil, nil); result != nil || err != nil {
		t.Fatalf("nil manifest = %v, %v", result, err)
	}
}
