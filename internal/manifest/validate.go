package manifest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"unicode/utf8"
)

func ValidateStructure(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if m.Version != CurrentVersion {
		return fmt.Errorf("unsupported Version %d (expected %d)", m.Version, CurrentVersion)
	}
	if strings.TrimSpace(m.DatabaseName) == "" {
		return fmt.Errorf("DatabaseName is empty")
	}

	trimmedMappings := map[string]string{}
	for key, mapping := range m.TypeMappings {
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			return fmt.Errorf("TypeMappings contains an empty key")
		}
		if previous, ok := trimmedMappings[trimmed]; ok {
			return fmt.Errorf("TypeMappings keys %q and %q collide after trimming", previous, key)
		}
		trimmedMappings[trimmed] = key
		if err := validateMapping(mapping); err != nil {
			return fmt.Errorf("TypeMappings[%q]: %w", key, err)
		}
	}

	tables := map[TableKey]struct{}{}
	for tableIndex, table := range m.Tables {
		location := fmt.Sprintf("Tables[%d]", tableIndex)
		if table.TableName == "" {
			return fmt.Errorf("%s.TableName is empty", location)
		}
		if table.TableType != "BASE TABLE" && table.TableType != "VIEW" {
			return fmt.Errorf("%s.TableType %q is not supported", location, table.TableType)
		}
		if _, exists := tables[table.Key()]; exists {
			return fmt.Errorf("duplicate table identity %q", table.Key())
		}
		tables[table.Key()] = struct{}{}
		if !table.IsManaged {
			if table.FileName != "" || table.PackageName != "" || table.GoName != "" {
				return fmt.Errorf("%s naming overrides must be empty when IsManaged is false", location)
			}
			if len(table.Columns) != 0 {
				return fmt.Errorf("%s.Columns must be empty when IsManaged is false", location)
			}
			continue
		}
		if table.FileName != "" {
			if err := ValidateFileComponent(table.FileName); err != nil {
				return fmt.Errorf("%s.FileName: %w", location, err)
			}
		}
		for field, value := range map[string]string{"PackageName": table.PackageName, "GoName": table.GoName} {
			if value != "" {
				if err := ValidateGoIdentifier(value); err != nil {
					return fmt.Errorf("%s.%s: %w", location, field, err)
				}
			}
		}
		if err := validateColumns(location, table.Columns); err != nil {
			return err
		}
	}
	return nil
}

func ValidateForRender(m *Manifest) error {
	if err := ValidateStructure(m); err != nil {
		return err
	}
	outputNames := map[string]TableKey{}
	for _, table := range m.Tables {
		if !table.IsManaged {
			continue
		}
		if len(table.Columns) == 0 {
			return fmt.Errorf("table %q Columns is empty", table.Key())
		}
		fileName, err := EffectiveFileName(table)
		if err != nil {
			return fmt.Errorf("table %q: %w", table.Key(), err)
		}
		folded := FileCollisionKey(fileName)
		if previous, exists := outputNames[folded]; exists {
			return fmt.Errorf("tables %q and %q produce the same output name %q", previous, table.Key(), fileName)
		}
		outputNames[folded] = table.Key()
		if _, err := EffectivePackageName(table); err != nil {
			return fmt.Errorf("table %q: %w", table.Key(), err)
		}
		goName, err := EffectiveTableGoName(table)
		if err != nil {
			return fmt.Errorf("table %q: %w", table.Key(), err)
		}

		fieldNames := map[string]string{}
		jsonNames := map[string]string{}
		imports := map[string]string{}
		importQualifiers := map[string]string{}
		for _, column := range table.Columns {
			goName, err := EffectiveColumnGoName(column)
			if err != nil {
				return fmt.Errorf("table %q column %q: %w", table.Key(), column.ColumnName, err)
			}
			if previous, exists := fieldNames[goName]; exists {
				return fmt.Errorf("table %q columns %q and %q produce the same Go field %q", table.Key(), previous, column.ColumnName, goName)
			}
			fieldNames[goName] = column.ColumnName
			jsonName := EffectiveJSONName(column)
			if strings.Contains(jsonName, ",") {
				return fmt.Errorf("table %q column %q JSONName %q contains a comma", table.Key(), column.ColumnName, jsonName)
			}
			if jsonName != "-" {
				if previous, exists := jsonNames[jsonName]; exists {
					return fmt.Errorf("table %q columns %q and %q produce the same JSON name %q", table.Key(), previous, column.ColumnName, jsonName)
				}
				jsonNames[jsonName] = column.ColumnName
			}

			resolved, err := ResolveType(m, column)
			if err != nil {
				return fmt.Errorf("table %q column %q: %w", table.Key(), column.ColumnName, err)
			}
			if err := validateGoTypeAndImport(resolved.GoType, resolved.GoImport); err != nil {
				return fmt.Errorf("table %q column %q: %w", table.Key(), column.ColumnName, err)
			}
			if resolved.GoImport != "" {
				qualifier, ok := GoTypeQualifier(resolved.GoType)
				if !ok {
					return fmt.Errorf("table %q column %q: cannot determine qualifier for GoType %q", table.Key(), column.ColumnName, resolved.GoType)
				}
				if previous, exists := imports[qualifier]; exists && previous != resolved.GoImport {
					return fmt.Errorf("table %q imports %q and %q with the same qualifier %q", table.Key(), previous, resolved.GoImport, qualifier)
				}
				imports[qualifier] = resolved.GoImport
				if previous, exists := importQualifiers[resolved.GoImport]; exists && previous != qualifier {
					return fmt.Errorf("table %q imports %q with both qualifiers %q and %q", table.Key(), resolved.GoImport, previous, qualifier)
				}
				importQualifiers[resolved.GoImport] = qualifier
			}
		}
		if importPath, exists := imports[goName]; exists {
			return fmt.Errorf("table %q GoName %q conflicts with import %q", table.Key(), goName, importPath)
		}
	}
	return nil
}

func validateColumns(location string, columns []Column) error {
	names := map[string]struct{}{}
	ordinals := map[int]string{}
	primaryOrdinals := map[int]string{}
	for columnIndex, column := range columns {
		columnLocation := fmt.Sprintf("%s.Columns[%d]", location, columnIndex)
		if column.ColumnName == "" {
			return fmt.Errorf("%s.ColumnName is empty", columnLocation)
		}
		if _, exists := names[column.ColumnName]; exists {
			return fmt.Errorf("%s has duplicate ColumnName %q", location, column.ColumnName)
		}
		names[column.ColumnName] = struct{}{}
		if column.OrdinalPosition <= 0 {
			return fmt.Errorf("%s.OrdinalPosition must be positive", columnLocation)
		}
		if previous, exists := ordinals[column.OrdinalPosition]; exists {
			return fmt.Errorf("%s columns %q and %q share OrdinalPosition %d", location, previous, column.ColumnName, column.OrdinalPosition)
		}
		ordinals[column.OrdinalPosition] = column.ColumnName
		if strings.TrimSpace(column.DataType) == "" {
			return fmt.Errorf("%s.DataType is empty", columnLocation)
		}
		if column.PrimaryKeyOrdinal < 0 {
			return fmt.Errorf("%s.PrimaryKeyOrdinal must not be negative", columnLocation)
		}
		if column.IsPrimaryKey != (column.PrimaryKeyOrdinal > 0) {
			return fmt.Errorf("%s IsPrimaryKey and PrimaryKeyOrdinal are inconsistent", columnLocation)
		}
		if column.PrimaryKeyOrdinal > 0 {
			if previous, exists := primaryOrdinals[column.PrimaryKeyOrdinal]; exists {
				return fmt.Errorf("%s primary-key columns %q and %q share PrimaryKeyOrdinal %d", location, previous, column.ColumnName, column.PrimaryKeyOrdinal)
			}
			primaryOrdinals[column.PrimaryKeyOrdinal] = column.ColumnName
		}
		if column.GoName != "" {
			if err := ValidateGoIdentifier(column.GoName); err != nil {
				return fmt.Errorf("%s.GoName: %w", columnLocation, err)
			}
		}
		if column.GoType == "" && column.GoImport != "" {
			return fmt.Errorf("%s.GoImport requires GoType", columnLocation)
		}
		if column.GoType != "" {
			if err := validateGoTypeAndImport(column.GoType, column.GoImport); err != nil {
				return fmt.Errorf("%s: %w", columnLocation, err)
			}
		}
	}
	if len(primaryOrdinals) > 0 {
		keys := make([]int, 0, len(primaryOrdinals))
		for ordinal := range primaryOrdinals {
			keys = append(keys, ordinal)
		}
		sort.Ints(keys)
		for index, ordinal := range keys {
			if ordinal != index+1 {
				return fmt.Errorf("%s primary-key ordinals must be contiguous from 1", location)
			}
		}
	}
	return nil
}

func validateMapping(mapping TypeMapping) error {
	if strings.TrimSpace(mapping.GoType) == "" {
		return fmt.Errorf("GoType is empty")
	}
	if err := validateGoTypeAndImport(mapping.GoType, mapping.GoImport); err != nil {
		return err
	}
	if mapping.NullableGoType == "" && mapping.NullableGoImport != "" {
		return fmt.Errorf("NullableGoImport requires NullableGoType")
	}
	if mapping.NullableGoType != "" {
		if err := validateGoTypeAndImport(mapping.NullableGoType, mapping.NullableGoImport); err != nil {
			return fmt.Errorf("nullable mapping: %w", err)
		}
	}
	return nil
}

func validateGoTypeAndImport(goType, goImport string) error {
	goType = strings.TrimSpace(goType)
	if _, err := parser.ParseFile(
		token.NewFileSet(),
		"manifest_type.go",
		"package manifesttype\ntype checked "+goType,
		parser.AllErrors,
	); err != nil {
		return fmt.Errorf("GoType %q is invalid: %w", goType, err)
	}
	expression, err := parser.ParseExpr(goType)
	if err != nil {
		return fmt.Errorf("GoType %q is invalid: %w", goType, err)
	}
	qualifiers := map[string]struct{}{}
	nestedSelector := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			nestedSelector = true
			return true
		}
		qualifiers[identifier.Name] = struct{}{}
		return true
	})
	if nestedSelector {
		return fmt.Errorf("GoType %q contains a nested selector", goType)
	}
	if len(qualifiers) > 1 {
		return fmt.Errorf("GoType %q uses multiple import qualifiers", goType)
	}
	goImport = strings.TrimSpace(goImport)
	if len(qualifiers) == 1 && goImport == "" {
		return fmt.Errorf("GoType %q requires GoImport", goType)
	}
	if len(qualifiers) == 0 && goImport != "" {
		return fmt.Errorf("GoImport %q is unused by GoType %q", goImport, goType)
	}
	if goImport != "" {
		if err := validateGoImport(goImport); err != nil {
			return fmt.Errorf("GoImport %q is invalid: %w", goImport, err)
		}
	}
	return nil
}

// GoTypeQualifier returns the single package qualifier used by a validated Go
// type expression. The import path alone cannot determine a package name: in
// particular, versioned modules commonly end in /vN.
func GoTypeQualifier(goType string) (string, bool) {
	expression, err := parser.ParseExpr(strings.TrimSpace(goType))
	if err != nil {
		return "", false
	}
	qualifier := ""
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if identifier, ok := selector.X.(*ast.Ident); ok {
			qualifier = identifier.Name
		}
		return true
	})
	return qualifier, qualifier != ""
}

func validateGoImport(goImport string) error {
	if !utf8.ValidString(goImport) {
		return fmt.Errorf("invalid UTF-8")
	}
	if goImport == "" {
		return fmt.Errorf("empty path")
	}
	if goImport[0] == '-' {
		return fmt.Errorf("leading dash")
	}
	if strings.Contains(goImport, "//") {
		return fmt.Errorf("double slash")
	}
	if strings.HasSuffix(goImport, "/") {
		return fmt.Errorf("trailing slash")
	}
	for _, element := range strings.Split(goImport, "/") {
		if element == "" {
			return fmt.Errorf("empty path element")
		}
		if strings.Trim(element, ".") == "" {
			return fmt.Errorf("invalid path element %q", element)
		}
		if strings.HasSuffix(element, ".") {
			return fmt.Errorf("trailing dot in path element %q", element)
		}
		for _, r := range element {
			if !isGoImportPathRune(r) {
				return fmt.Errorf("invalid character %q", r)
			}
		}
		if isReservedWindowsName(element) {
			return fmt.Errorf("reserved Windows path element %q", element)
		}
		if hasWindowsShortNameSuffix(element) {
			return fmt.Errorf("windows short-name path element %q", element)
		}
	}
	return nil
}
