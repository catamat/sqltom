package manifest

import "testing"

func TestCanonicalDataTypeVocabularyAndDefaultMappings(t *testing.T) {
	tests := map[string]ResolvedType{
		DataTypeBigInt:           {GoType: "int64"},
		DataTypeBinary:           {GoType: "[]byte"},
		DataTypeBoolean:          {GoType: "bool"},
		DataTypeDate:             {GoType: "time.Time", GoImport: "time"},
		DataTypeDateTime:         {GoType: "time.Time", GoImport: "time"},
		DataTypeDateTimeOffset:   {GoType: "time.Time", GoImport: "time"},
		DataTypeDecimal:          {GoType: "float64"},
		DataTypeDouble:           {GoType: "float64"},
		DataTypeInteger:          {GoType: "int"},
		DataTypeJSON:             {GoType: "json.RawMessage", GoImport: "encoding/json"},
		DataTypeMoney:            {GoType: "string"},
		DataTypeReal:             {GoType: "float32"},
		DataTypeRowVersion:       {GoType: "[]byte"},
		DataTypeSmallInt:         {GoType: "int16"},
		DataTypeString:           {GoType: "string"},
		DataTypeTime:             {GoType: "time.Time", GoImport: "time"},
		DataTypeTinyInt:          {GoType: "int"},
		DataTypeUnsignedBigInt:   {GoType: "uint64"},
		DataTypeUnsignedInteger:  {GoType: "uint32"},
		DataTypeUnsignedSmallInt: {GoType: "uint16"},
		DataTypeUnsignedTinyInt:  {GoType: "uint8"},
		DataTypeUUID:             {GoType: "uuid.UUID", GoImport: "uuid"},
		DataTypeAny:              {GoType: "any"},
		DataTypeVariant:          {GoType: "any"},
		DataTypeXML:              {GoType: "[]byte"},
	}

	seen := make(map[string]struct{}, len(tests))
	for dataType, want := range tests {
		if _, duplicate := seen[dataType]; duplicate {
			t.Fatalf("duplicate canonical data type %q", dataType)
		}
		seen[dataType] = struct{}{}

		canonical, ok := CanonicalDataType("  " + dataType + "  ")
		if !ok || canonical != dataType {
			t.Errorf("CanonicalDataType(%q) = %q, %v", dataType, canonical, ok)
		}
		got, err := ResolveType(validManifest(), Column{DataType: dataType})
		if err != nil || got != want {
			t.Errorf("ResolveType(%q) = %#v, %v; want %#v", dataType, got, err, want)
		}
	}
}

func TestNullableVariantUsesAny(t *testing.T) {
	got, err := ResolveType(validManifest(), Column{DataType: DataTypeVariant, IsNullable: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := (ResolvedType{GoType: "any"}); got != want {
		t.Fatalf("ResolveType(nullable variant) = %#v, want %#v", got, want)
	}
}

func TestCanonicalDataTypeAcceptsGenericAliases(t *testing.T) {
	tests := map[string]string{
		"INT64":            DataTypeBigInt,
		"bytes":            DataTypeBinary,
		"Bool":             DataTypeBoolean,
		"date time":        DataTypeDateTime,
		"date time offset": DataTypeDateTimeOffset,
		"float64":          DataTypeDouble,
		"int":              DataTypeInteger,
		"float32":          DataTypeReal,
		"int16":            DataTypeSmallInt,
		"text":             DataTypeString,
		"uint8":            DataTypeTinyInt,
		"guid":             DataTypeUUID,
	}
	for alias, want := range tests {
		got, ok := CanonicalDataType("  " + alias + "  ")
		if !ok || got != want {
			t.Errorf("CanonicalDataType(%q) = %q, %v; want %q", alias, got, ok, want)
		}
	}
	if got, ok := CanonicalDataType("geography"); ok || got != "" {
		t.Fatalf("CanonicalDataType(unknown) = %q, %v", got, ok)
	}
}
