package manifest

import (
	"fmt"
	"sort"
	"strings"
)

const CurrentVersion = 1

type Manifest struct {
	Version      int                    `json:"Version"`
	DatabaseName string                 `json:"DatabaseName"`
	TypeMappings map[string]TypeMapping `json:"TypeMappings"`
	Tables       []Table                `json:"Tables"`
}

type TypeMapping struct {
	GoType           string `json:"GoType"`
	GoImport         string `json:"GoImport,omitempty"`
	NullableGoType   string `json:"NullableGoType,omitempty"`
	NullableGoImport string `json:"NullableGoImport,omitempty"`
}

type Table struct {
	TableCatalog string   `json:"TableCatalog"`
	TableSchema  string   `json:"TableSchema"`
	TableName    string   `json:"TableName"`
	TableType    string   `json:"TableType"`
	IsManaged    bool     `json:"IsManaged"`
	FileName     string   `json:"FileName"`
	PackageName  string   `json:"PackageName"`
	GoName       string   `json:"GoName"`
	Columns      []Column `json:"Columns"`
}

type Column struct {
	ColumnName        string `json:"ColumnName"`
	OrdinalPosition   int    `json:"OrdinalPosition"`
	IsNullable        bool   `json:"IsNullable"`
	DataType          string `json:"DataType"`
	IsIdentity        bool   `json:"IsIdentity"`
	IsPrimaryKey      bool   `json:"IsPrimaryKey"`
	IsComputed        bool   `json:"IsComputed"`
	IsGeneratedAlways bool   `json:"IsGeneratedAlways"`
	IsRowVersion      bool   `json:"IsRowVersion"`
	HasDefault        bool   `json:"HasDefault"`
	PrimaryKeyOrdinal int    `json:"PrimaryKeyOrdinal"`
	GoName            string `json:"GoName"`
	JSONName          string `json:"JSONName"`
	GoType            string `json:"GoType"`
	GoImport          string `json:"GoImport"`
}

type TableKey struct {
	Catalog string
	Schema  string
	Name    string
}

type ResolvedType struct {
	GoType   string
	GoImport string
}

func New(databaseName string, tables []Table) *Manifest {
	for tableIndex := range tables {
		tables[tableIndex].IsManaged = true
	}
	m := &Manifest{
		Version:      CurrentVersion,
		DatabaseName: databaseName,
		TypeMappings: map[string]TypeMapping{},
		Tables:       tables,
	}
	Canonicalize(m)
	return m
}

func (t Table) Key() TableKey {
	return TableKey{Catalog: t.TableCatalog, Schema: t.TableSchema, Name: t.TableName}
}

func (k TableKey) String() string {
	parts := make([]string, 0, 3)
	for _, part := range []string{k.Catalog, k.Schema, k.Name} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ".")
}

func ResolveType(m *Manifest, column Column) (ResolvedType, error) {
	if strings.TrimSpace(column.GoType) != "" {
		return ResolvedType{
			GoType:   strings.TrimSpace(column.GoType),
			GoImport: strings.TrimSpace(column.GoImport),
		}, nil
	}

	mapping, ok, err := customMapping(m.TypeMappings, strings.TrimSpace(column.DataType))
	if err != nil {
		return ResolvedType{}, err
	}
	if ok {
		return resolveMapping(mapping, column.IsNullable)
	}

	mapping, ok = builtinMapping(column.DataType)
	if !ok {
		return ResolvedType{}, fmt.Errorf("unknown data type %q", column.DataType)
	}
	return resolveMapping(mapping, column.IsNullable)
}

func resolveMapping(mapping TypeMapping, nullable bool) (ResolvedType, error) {
	goType := strings.TrimSpace(mapping.GoType)
	if goType == "" {
		return ResolvedType{}, fmt.Errorf("mapping GoType is empty")
	}
	if !nullable {
		return ResolvedType{GoType: goType, GoImport: strings.TrimSpace(mapping.GoImport)}, nil
	}
	if nullableType := strings.TrimSpace(mapping.NullableGoType); nullableType != "" {
		return ResolvedType{
			GoType:   nullableType,
			GoImport: strings.TrimSpace(mapping.NullableGoImport),
		}, nil
	}
	return ResolvedType{GoType: "*" + goType, GoImport: strings.TrimSpace(mapping.GoImport)}, nil
}

func customMapping(mappings map[string]TypeMapping, dataType string) (TypeMapping, bool, error) {
	type candidate struct {
		key     string
		mapping TypeMapping
	}

	target := strings.TrimSpace(dataType)
	normalizedTarget := normalize(target)
	exact := make([]candidate, 0, 1)
	fallback := make([]candidate, 0, 1)
	for key, mapping := range mappings {
		trimmedKey := strings.TrimSpace(key)
		match := candidate{key: key, mapping: mapping}
		if trimmedKey == target {
			exact = append(exact, match)
		}
		if normalize(trimmedKey) == normalizedTarget {
			fallback = append(fallback, match)
		}
	}

	selected := fallback
	if len(exact) > 0 {
		selected = exact
	}
	if len(selected) == 0 {
		return TypeMapping{}, false, nil
	}
	if len(selected) > 1 {
		keys := make([]string, 0, len(selected))
		for _, match := range selected {
			keys = append(keys, match.key)
		}
		sort.Strings(keys)
		return TypeMapping{}, false, fmt.Errorf("ambiguous TypeMappings for data type %q: %q", dataType, keys)
	}
	return selected[0].mapping, true, nil
}

func Canonicalize(m *Manifest) {
	if m == nil {
		return
	}
	if m.TypeMappings == nil {
		m.TypeMappings = map[string]TypeMapping{}
	}
	if m.Tables == nil {
		m.Tables = []Table{}
	}
	sort.SliceStable(m.Tables, func(i, j int) bool {
		a, b := m.Tables[i], m.Tables[j]
		if a.TableCatalog != b.TableCatalog {
			return a.TableCatalog < b.TableCatalog
		}
		if a.TableSchema != b.TableSchema {
			return a.TableSchema < b.TableSchema
		}
		return a.TableName < b.TableName
	})
	for i := range m.Tables {
		if !m.Tables[i].IsManaged {
			clearUnmanagedTable(&m.Tables[i])
			continue
		}
		if m.Tables[i].Columns == nil {
			m.Tables[i].Columns = []Column{}
		}
		sort.SliceStable(m.Tables[i].Columns, func(a, b int) bool {
			left, right := m.Tables[i].Columns[a], m.Tables[i].Columns[b]
			if left.OrdinalPosition != right.OrdinalPosition {
				return left.OrdinalPosition < right.OrdinalPosition
			}
			return left.ColumnName < right.ColumnName
		})
	}
}

func normalizeTableManagement(m *Manifest) {
	if m == nil {
		return
	}
	for tableIndex := range m.Tables {
		if !m.Tables[tableIndex].IsManaged {
			clearUnmanagedTable(&m.Tables[tableIndex])
		}
	}
}

func clearUnmanagedTable(table *Table) {
	table.FileName = ""
	table.PackageName = ""
	table.GoName = ""
	table.Columns = []Column{}
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
