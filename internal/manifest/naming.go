package manifest

import (
	"fmt"
	"go/token"
	"go/types"
	"unicode"
)

func EffectiveFileName(table Table) (string, error) {
	if table.FileName != "" {
		if err := ValidateFileComponent(table.FileName); err != nil {
			return "", fmt.Errorf("FileName %q: %w", table.FileName, err)
		}
		return table.FileName, nil
	}
	return SanitizeFileComponent(table.TableName)
}

func EffectivePackageName(table Table) (string, error) {
	if table.PackageName != "" {
		if err := ValidateGoIdentifier(table.PackageName); err != nil {
			return "", fmt.Errorf("PackageName %q: %w", table.PackageName, err)
		}
		if isReservedPackageName(table.PackageName) {
			return "", fmt.Errorf("PackageName %q is reserved by Go tooling", table.PackageName)
		}
		return table.PackageName, nil
	}
	name, err := SanitizeGoIdentifier(table.TableName)
	if err != nil {
		return "", err
	}
	if isReservedPackageName(name) {
		name += "_"
	}
	return name, nil
}

func EffectiveTableGoName(table Table) (string, error) {
	if table.GoName != "" {
		if err := ValidateGoIdentifier(table.GoName); err != nil {
			return "", fmt.Errorf("GoName %q: %w", table.GoName, err)
		}
		if isReservedTableGoName(table.GoName) {
			return "", fmt.Errorf("GoName %q is reserved by Go", table.GoName)
		}
		return table.GoName, nil
	}
	name, err := SanitizeGoIdentifier(table.TableName)
	if err != nil {
		return "", err
	}
	if isReservedTableGoName(name) {
		name += "_"
	}
	return name, nil
}

func EffectiveColumnGoName(column Column) (string, error) {
	if column.GoName != "" {
		if err := ValidateGoIdentifier(column.GoName); err != nil {
			return "", fmt.Errorf("GoName %q: %w", column.GoName, err)
		}
		return column.GoName, nil
	}
	return SanitizeGoIdentifier(column.ColumnName)
}

func EffectiveJSONName(column Column) string {
	if column.JSONName != "" {
		return column.JSONName
	}
	return column.ColumnName
}

func SanitizeGoIdentifier(value string) (string, error) {
	var out []rune
	for _, r := range value {
		if len(out) == 0 {
			switch {
			case unicode.IsLetter(r) || r == '_':
				out = append(out, r)
			case unicode.IsDigit(r):
				out = append(out, '_', r)
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			out = append(out, r)
		}
	}
	result := string(out)
	if token.Lookup(result).IsKeyword() {
		result += "_"
	}
	if err := ValidateGoIdentifier(result); err != nil {
		return "", fmt.Errorf("cannot derive a Go identifier from %q: %w", value, err)
	}
	return result, nil
}

func ValidateGoIdentifier(value string) error {
	if value == "" || value == "_" {
		return fmt.Errorf("identifier is empty")
	}
	if !token.IsIdentifier(value) {
		return fmt.Errorf("not a valid Go identifier")
	}
	return nil
}

func SanitizeFileComponent(value string) (string, error) {
	return sanitizeGoFileComponent(value)
}

func ValidateFileComponent(value string) error {
	return validateGoFileComponent(value)
}

func ManifestFilename(databaseName string) (string, error) {
	return buildManifestFilename(databaseName)
}

func isReservedPackageName(value string) bool {
	return value == "main" || value == "init"
}

func isPredeclaredGoIdentifier(value string) bool {
	return types.Universe.Lookup(value) != nil
}

func isReservedTableGoName(value string) bool {
	return value == "init" || isPredeclaredGoIdentifier(value)
}
