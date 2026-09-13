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
