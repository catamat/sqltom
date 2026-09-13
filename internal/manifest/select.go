package manifest

import (
	"fmt"
	"sort"
	"strings"
)

// SelectTables returns an independent manifest containing exactly the objects
// identified by selectors. Selectors match exact database names and may be
// qualified as schema.name or catalog.schema.name. Dots and backslashes inside
// qualified identity components are backslash-escaped.
func SelectTables(m *Manifest, selectors []string) (*Manifest, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	result := cloneManifest(m)
	if len(selectors) == 0 {
		Canonicalize(result)
		return result, nil
	}

	selected := make(map[int]string, len(selectors))
	for _, rawSelector := range selectors {
		selector := strings.TrimSpace(rawSelector)
		if selector == "" {
			return nil, fmt.Errorf("table selector is empty")
		}

		matches := make([]int, 0, 1)
		for tableIndex, table := range m.Tables {
			if matchesTableSelector(selector, table) {
				matches = append(matches, tableIndex)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("table or view selector %q did not match any object", selector)
		}
		if len(matches) > 1 {
			identities := make([]string, 0, len(matches))
			for _, tableIndex := range matches {
				table := m.Tables[tableIndex]
				identities = append(
					identities,
					fmt.Sprintf("%s (selector %q)", tableIdentityDescription(table), fullyQualifiedTableSelector(table)),
				)
			}
			sort.Strings(identities)
			return nil, fmt.Errorf(
				"table or view selector %q is ambiguous; matches %s",
				selector,
				strings.Join(identities, ", "),
			)
		}

		tableIndex := matches[0]
		if previous, exists := selected[tableIndex]; exists {
			return nil, fmt.Errorf(
				"table selectors %q and %q both identify %s",
				previous,
				selector,
				tableIdentityDescription(m.Tables[tableIndex]),
			)
		}
		selected[tableIndex] = selector
	}

	allTables := result.Tables
	result.Tables = make([]Table, 0, len(selected))
	for tableIndex, table := range allTables {
		if _, exists := selected[tableIndex]; exists {
			result.Tables = append(result.Tables, table)
		}
	}
	Canonicalize(result)
	return result, nil
}

// ManagedTables returns an independent manifest containing only tables that
// are enabled for rendering.
func ManagedTables(m *Manifest) (*Manifest, error) {
	if m == nil {
		return nil, fmt.Errorf("manifest is nil")
	}
	result := cloneManifest(m)
	tables := result.Tables
	result.Tables = make([]Table, 0, len(tables))
	for _, table := range tables {
		if table.IsManaged {
			result.Tables = append(result.Tables, table)
		}
	}
	Canonicalize(result)
	return result, nil
}

func matchesTableSelector(selector string, table Table) bool {
	for _, candidate := range tableSelectorCandidates(table) {
		if selector == candidate {
			return true
		}
	}
	return false
}

func tableSelectorCandidates(table Table) []string {
	name := escapeTableSelectorComponent(table.TableName)
	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	add := func(candidate string) {
		if _, exists := seen[candidate]; exists {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	// The raw physical name is a convenient unqualified selector. The escaped
	// form remains available when that spelling collides with a qualified name.
	add(table.TableName)
	add(name)
	if table.TableSchema != "" {
		add(escapeTableSelectorComponent(table.TableSchema) + "." + name)
	}
	add(fullyQualifiedTableSelector(table))
	return candidates
}

func fullyQualifiedTableSelector(table Table) string {
	return escapeTableSelectorComponent(table.TableCatalog) + "." +
		escapeTableSelectorComponent(table.TableSchema) + "." +
		escapeTableSelectorComponent(table.TableName)
}

func escapeTableSelectorComponent(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", ".", "\\.")
	return replacer.Replace(value)
}

func tableIdentityDescription(table Table) string {
	return fmt.Sprintf(
		"(catalog=%q, schema=%q, name=%q)",
		table.TableCatalog,
		table.TableSchema,
		table.TableName,
	)
}

func cloneManifest(m *Manifest) *Manifest {
	result := *m
	result.TypeMappings = cloneMappings(m.TypeMappings)
	result.Tables = make([]Table, len(m.Tables))
	for tableIndex, table := range m.Tables {
		result.Tables[tableIndex] = table
		result.Tables[tableIndex].Columns = append([]Column(nil), table.Columns...)
	}
	return &result
}
