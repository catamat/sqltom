package manifest

import "fmt"

func Merge(fresh, previous *Manifest) (*Manifest, error) {
	if err := ValidateStructure(fresh); err != nil {
		return nil, fmt.Errorf("validate inspected manifest: %w", err)
	}
	if previous == nil {
		Canonicalize(fresh)
		return fresh, nil
	}
	if err := ValidateStructure(previous); err != nil {
		return nil, fmt.Errorf("validate existing manifest: %w", err)
	}
	if fresh.DatabaseName != previous.DatabaseName {
		return nil, fmt.Errorf("manifest DatabaseName changed from %q to %q", previous.DatabaseName, fresh.DatabaseName)
	}

	oldTables := make(map[TableKey]Table, len(previous.Tables))
	for _, table := range previous.Tables {
		oldTables[table.Key()] = table
	}
	for tableIndex := range fresh.Tables {
		newTable := &fresh.Tables[tableIndex]
		oldTable, found := oldTables[newTable.Key()]
		if !found {
			continue
		}
		newTable.IsManaged = oldTable.IsManaged
		if !newTable.IsManaged {
			clearUnmanagedTable(newTable)
			continue
		}
		newTable.FileName = oldTable.FileName
		newTable.PackageName = oldTable.PackageName
		newTable.GoName = oldTable.GoName

		oldColumns := make(map[string]Column, len(oldTable.Columns))
		for _, column := range oldTable.Columns {
			oldColumns[column.ColumnName] = column
		}
		for columnIndex := range newTable.Columns {
			newColumn := &newTable.Columns[columnIndex]
			oldColumn, found := oldColumns[newColumn.ColumnName]
			if !found {
				continue
			}
			newColumn.GoName = oldColumn.GoName
			newColumn.JSONName = oldColumn.JSONName
			newColumn.GoType = oldColumn.GoType
			newColumn.GoImport = oldColumn.GoImport
		}
	}

	fresh.TypeMappings = cloneMappings(previous.TypeMappings)
	Canonicalize(fresh)
	if err := ValidateStructure(fresh); err != nil {
		return nil, fmt.Errorf("validate merged manifest: %w", err)
	}
	return fresh, nil
}

func cloneMappings(source map[string]TypeMapping) map[string]TypeMapping {
	result := make(map[string]TypeMapping, len(source))
	for key, mapping := range source {
		result[key] = mapping
	}
	return result
}

// MergeSelected refreshes a selected subset while preserving the configuration
// and metadata of every previously known object outside that subset. A full
// inspection should use Merge, which also removes objects absent from the catalog.
func MergeSelected(fresh, previous *Manifest) (*Manifest, error) {
	merged, err := Merge(fresh, previous)
	if err != nil || previous == nil {
		return merged, err
	}
	merged = cloneManifest(merged)
	selected := make(map[TableKey]struct{}, len(merged.Tables))
	for _, table := range merged.Tables {
		selected[table.Key()] = struct{}{}
	}
	for _, table := range previous.Tables {
		if _, refreshed := selected[table.Key()]; !refreshed {
			table.Columns = append([]Column(nil), table.Columns...)
			merged.Tables = append(merged.Tables, table)
		}
	}
	Canonicalize(merged)
	return merged, nil
}
