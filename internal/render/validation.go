package render

import (
	"fmt"
	"go/types"
	"strings"

	"github.com/catamat/sqltom/internal/manifest"
)

func Validate(m *manifest.Manifest) error {
	if err := manifest.ValidateForRender(m); err != nil {
		return err
	}

	for _, table := range m.Tables {
		fileName, err := manifest.EffectiveFileName(table)
		if err != nil {
			return fmt.Errorf("table %q: %w", table.Key(), err)
		}
		if manifest.FileCollisionKey(fileName) == manifest.FileCollisionKey("query") {
			return fmt.Errorf("table %q output name %q conflicts with generated support package query; set a different FileName", table.Key(), fileName)
		}
		tableGoName, err := manifest.EffectiveTableGoName(table)
		if err != nil {
			return fmt.Errorf("table %q: %w", table.Key(), err)
		}
		declarations := map[string]struct{}{
			"DBTX":               {},
			"SelectAll":          {},
			"SelectCols":         {},
			"Rows":               {},
			"ErrMultipleRows":    {},
			"ErrIndexOutOfRange": {},
			"Exists":             {},
			"Query":              {},
		}
		if table.TableType == "BASE TABLE" {
			declarations["Delete"] = struct{}{}
		}
		if _, exists := declarations[tableGoName]; exists {
			return fmt.Errorf("table %q GoName %q conflicts with a generated declaration", table.Key(), tableGoName)
		}
		declarations[tableGoName] = struct{}{}

		imports := map[string]string{
			"sql": "database/sql",
		}
		for _, column := range table.Columns {
			columnGoName, err := manifest.EffectiveColumnGoName(column)
			if err != nil {
				return fmt.Errorf("table %q column %q: %w", table.Key(), column.ColumnName, err)
			}
			if table.TableType == "BASE TABLE" && (columnGoName == "Insert" || columnGoName == "Update") {
				return fmt.Errorf("table %q column %q conflicts with generated method %s", table.Key(), column.ColumnName, columnGoName)
			}

			resolved, err := manifest.ResolveType(m, column)
			if err != nil {
				return fmt.Errorf("table %q column %q: %w", table.Key(), column.ColumnName, err)
			}
			goImport := strings.TrimSpace(resolved.GoImport)
			if goImport == "" {
				continue
			}
			qualifier, ok := manifest.GoTypeQualifier(resolved.GoType)
			if !ok {
				return fmt.Errorf("table %q column %q cannot determine import qualifier for GoType %q", table.Key(), column.ColumnName, resolved.GoType)
			}
			if goImport == "database/sql" && qualifier != "sql" {
				return fmt.Errorf("table %q column %q must use qualifier %q for import %q", table.Key(), column.ColumnName, "sql", goImport)
			}
			if types.Universe.Lookup(qualifier) != nil {
				return fmt.Errorf("table %q column %q import qualifier %q is predeclared by Go", table.Key(), column.ColumnName, qualifier)
			}
			if _, exists := declarations[qualifier]; exists {
				return fmt.Errorf("table %q column %q import qualifier %q conflicts with a generated declaration", table.Key(), column.ColumnName, qualifier)
			}
			if previous, exists := imports[qualifier]; exists && previous != goImport {
				return fmt.Errorf("table %q imports %q and %q with the same qualifier %q", table.Key(), previous, goImport, qualifier)
			}
			imports[qualifier] = goImport
		}
		if importPath, exists := imports[tableGoName]; exists {
			return fmt.Errorf("table %q GoName %q conflicts with import %q", table.Key(), tableGoName, importPath)
		}
	}
	return nil
}
