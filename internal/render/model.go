package render

import (
	"fmt"
	"sort"

	"github.com/catamat/sqltom/internal/manifest"
)

type Data struct {
	Table   Table
	Index   string
	Imports []string
}

type Table struct {
	TableCatalog string
	TableSchema  string
	TableName    string
	TableType    string
	FileName     string
	PackageName  string
	GoName       string
	Columns      []Column
	WriteColumns []Column
}

type Column struct {
	ColumnName     string
	IsUUID         bool
	IsIdentity     bool
	GoName         string
	JSONName       string
	IncludeJSONTag bool
	ResolvedGoType string
}

func BuildData(m *manifest.Manifest, table manifest.Table) (Data, error) {
	fileName, err := manifest.EffectiveFileName(table)
	if err != nil {
		return Data{}, err
	}
	packageName, err := manifest.EffectivePackageName(table)
	if err != nil {
		return Data{}, err
	}
	goName, err := manifest.EffectiveTableGoName(table)
	if err != nil {
		return Data{}, err
	}

	imports := map[string]struct{}{
		"database/sql": {},
		"strings":      {},
	}
	columns := make([]Column, 0, len(table.Columns))
	writeColumns := make([]Column, 0, len(table.Columns))
	for _, column := range table.Columns {
		columnGoName, err := manifest.EffectiveColumnGoName(column)
		if err != nil {
			return Data{}, fmt.Errorf("column %q: %w", column.ColumnName, err)
		}
		resolvedType, err := manifest.ResolveType(m, column)
		if err != nil {
			return Data{}, fmt.Errorf("column %q: %w", column.ColumnName, err)
		}
		if resolvedType.GoImport != "" {
			imports[resolvedType.GoImport] = struct{}{}
		}
		jsonName := manifest.EffectiveJSONName(column)
		canonicalDataType, _ := manifest.CanonicalDataType(column.DataType)
		templateValue := Column{
			ColumnName:     column.ColumnName,
			IsUUID:         canonicalDataType == manifest.DataTypeUUID,
			IsIdentity:     column.IsIdentity,
			GoName:         columnGoName,
			JSONName:       jsonName,
			IncludeJSONTag: column.JSONName != "" || jsonName != columnGoName,
			ResolvedGoType: resolvedType.GoType,
		}
		columns = append(columns, templateValue)
		if IsWritableColumn(column) {
			writeColumns = append(writeColumns, templateValue)
		}
	}

	importList := make([]string, 0, len(imports))
	for importPath := range imports {
		importList = append(importList, importPath)
	}
	sort.Strings(importList)
	index := CompatibilityIndex(table)
	if table.TableType == "BASE TABLE" && index == "" {
		return Data{}, fmt.Errorf("base table %q has no primary key and no FW_ID compatibility column", table.Key())
	}
	return Data{
		Table: Table{
			TableCatalog: table.TableCatalog,
			TableSchema:  table.TableSchema,
			TableName:    table.TableName,
			TableType:    table.TableType,
			FileName:     fileName,
			PackageName:  packageName,
			GoName:       goName,
			Columns:      columns,
			WriteColumns: writeColumns,
		},
		Index:   index,
		Imports: importList,
	}, nil
}

func IsWritableColumn(column manifest.Column) bool {
	return !column.IsIdentity && !column.IsComputed && !column.IsGeneratedAlways && !column.IsRowVersion
}

func CompatibilityIndex(table manifest.Table) string {
	for _, column := range table.Columns {
		if column.IsPrimaryKey && column.PrimaryKeyOrdinal == 1 {
			return column.ColumnName
		}
	}
	for _, column := range table.Columns {
		if column.ColumnName == "FW_ID" {
			return column.ColumnName
		}
	}
	return ""
}
