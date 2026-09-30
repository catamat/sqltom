package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/catamat/sqltom/internal/manifest"
)

type Data struct {
	Table                Table
	IdentityColumns      []Column
	Imports              []Import
	InsertStrategy       InsertStrategy
	EmptyInsert          string
	Names                map[string]string
	ScanJSON             bool
	ScanBoolean          bool
	ScanTime             bool
	ScanUUID             bool
	ScanSQLServerUUID    bool
	UUIDArguments        bool
	JSONArguments        bool
	BinaryArguments      bool
	ScanBinary           bool
	UnsignedLastInsertID bool
	RejectNullUpdateKey  bool
}

type Table struct {
	TableCatalog  string
	TableSchema   string
	TableName     string
	TableType     string
	FileName      string
	PackageName   string
	GoName        string
	Columns       []Column
	WriteColumns  []Column
	UpdateColumns []Column
	KeyColumns    []Column
}

type Column struct {
	ColumnName     string
	IsUUID         bool
	IsUUIDString   bool
	IsBinaryBytes  bool
	GoName         string
	JSONName       string
	IncludeJSONTag bool
	ResolvedGoType string
	ScanKind       string
	IsJSON         bool
	IsUnsigned     bool
}

type Import struct {
	Qualifier string
	Path      string
}

type InsertStrategy string

const (
	InsertExecLastInsertID InsertStrategy = "last-insert-id"
	InsertQueryReturning   InsertStrategy = "returning"
	InsertScopeIdentity    InsertStrategy = "scope-identity"
)

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

	imports := map[string]string{
		"database/sql": "",
	}
	columns := make([]Column, 0, len(table.Columns))
	writeColumns := make([]Column, 0, len(table.Columns))
	identityColumns := make([]Column, 0, 1)
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
			qualifier, ok := manifest.GoTypeQualifier(resolvedType.GoType)
			if !ok {
				return Data{}, fmt.Errorf("column %q: cannot determine import qualifier for GoType %q", column.ColumnName, resolvedType.GoType)
			}
			if resolvedType.GoImport != "database/sql" {
				imports[resolvedType.GoImport] = qualifier
			}
		}
		jsonName := manifest.EffectiveJSONName(column)
		canonicalDataType, _ := manifest.CanonicalDataType(column.DataType)
		isUUIDString := canonicalDataType == manifest.DataTypeUUID && (resolvedType.GoType == "string" || resolvedType.GoType == "*string")
		scanKind := ""
		switch canonicalDataType {
		case manifest.DataTypeJSON, manifest.DataTypeBoolean:
			scanKind = canonicalDataType
		case manifest.DataTypeUUID:
			if isUUIDString {
				scanKind = canonicalDataType
			}
		case manifest.DataTypeTime:
			// A custom time type owns its Scanner semantics. The built-in
			// string representation also accepts SQL Server's time.Time values.
			if strings.TrimLeft(resolvedType.GoType, "*") == "string" {
				scanKind = canonicalDataType
			}
		}
		templateValue := Column{
			ColumnName:     column.ColumnName,
			IsUUID:         canonicalDataType == manifest.DataTypeUUID,
			IsUUIDString:   isUUIDString,
			IsBinaryBytes:  (canonicalDataType == manifest.DataTypeBinary || canonicalDataType == manifest.DataTypeRowVersion) && (resolvedType.GoType == "[]byte" || resolvedType.GoType == "*[]byte"),
			GoName:         columnGoName,
			JSONName:       jsonName,
			IncludeJSONTag: column.JSONName != "" || jsonName != columnGoName,
			ResolvedGoType: resolvedType.GoType,
			ScanKind:       scanKind,
			IsJSON:         canonicalDataType == manifest.DataTypeJSON,
			IsUnsigned:     strings.HasPrefix(canonicalDataType, "unsigned"),
		}
		columns = append(columns, templateValue)
		if column.IsIdentity {
			identityColumns = append(identityColumns, templateValue)
		}
		if IsWritableColumn(column) {
			writeColumns = append(writeColumns, templateValue)
		}
	}

	keyNames := primaryKeyNames(table)
	keySet := make(map[string]struct{}, len(keyNames))
	for _, name := range keyNames {
		keySet[name] = struct{}{}
	}
	keyColumns := make([]Column, 0, len(keyNames))
	for _, name := range keyNames {
		for _, column := range columns {
			if column.ColumnName == name {
				keyColumns = append(keyColumns, column)
				break
			}
		}
	}
	updateColumns := make([]Column, 0, len(writeColumns))
	for _, column := range writeColumns {
		if _, isKey := keySet[column.ColumnName]; !isKey {
			updateColumns = append(updateColumns, column)
		}
	}

	importList := make([]Import, 0, len(imports))
	for importPath, qualifier := range imports {
		importList = append(importList, Import{Qualifier: qualifier, Path: importPath})
	}
	sort.Slice(importList, func(i, j int) bool {
		if importList[i].Path != importList[j].Path {
			return importList[i].Path < importList[j].Path
		}
		return importList[i].Qualifier < importList[j].Qualifier
	})
	return Data{
		Table: Table{
			TableCatalog:  table.TableCatalog,
			TableSchema:   table.TableSchema,
			TableName:     table.TableName,
			TableType:     table.TableType,
			FileName:      fileName,
			PackageName:   packageName,
			GoName:        goName,
			Columns:       columns,
			WriteColumns:  writeColumns,
			UpdateColumns: updateColumns,
			KeyColumns:    keyColumns,
		},
		IdentityColumns: identityColumns,
		Imports:         importList,
	}, nil
}

// configureRuntime allocates package and local identifiers together. A model
// name or custom import alias must never be hidden by a generated local name.
func (data *Data) configureRuntime(config Config) {
	data.InsertStrategy = config.InsertStrategy
	data.EmptyInsert = config.EmptyInsert
	data.UnsignedLastInsertID = config.UnsignedLastInsertID && data.Table.TableType == "BASE TABLE" &&
		len(data.IdentityColumns) > 0 && data.IdentityColumns[0].IsUnsigned && config.InsertStrategy == InsertExecLastInsertID
	data.RejectNullUpdateKey = config.RejectNullUpdateKey && data.Table.TableType == "BASE TABLE" &&
		len(data.Table.KeyColumns) > 0 && len(data.Table.UpdateColumns) > 0
	used := map[string]bool{data.Table.GoName: true, "sql": true}
	imports := make(map[string]string, len(data.Imports))
	for _, entry := range data.Imports {
		imports[entry.Path] = entry.Qualifier
		used[entry.Qualifier] = true
	}
	allocate := func(base string) string {
		name := base
		for used[name] {
			name += "_"
		}
		used[name] = true
		return name
	}
	data.Names = make(map[string]string)
	for i := range data.Table.Columns {
		column := &data.Table.Columns[i]
		if config.ScanSQLServerUUID && column.IsUUID {
			column.ScanKind = manifest.DataTypeUUID
		}
		if config.ScanBinary && column.IsBinaryBytes {
			column.ScanKind = manifest.DataTypeBinary
		}
		data.ScanBinary = data.ScanBinary || column.ScanKind == manifest.DataTypeBinary
		data.ScanUUID = data.ScanUUID || column.ScanKind == manifest.DataTypeUUID
		data.ScanJSON = data.ScanJSON || column.ScanKind == manifest.DataTypeJSON
		data.ScanBoolean = data.ScanBoolean || column.ScanKind == manifest.DataTypeBoolean
		data.ScanTime = data.ScanTime || column.ScanKind == manifest.DataTypeTime
	}
	if data.Table.TableType == "BASE TABLE" {
		for _, column := range append(append([]Column(nil), data.Table.WriteColumns...), data.Table.KeyColumns...) {
			data.JSONArguments = data.JSONArguments || column.IsJSON
			data.UUIDArguments = data.UUIDArguments || column.IsUUIDString
			data.BinaryArguments = data.BinaryArguments || (config.TypedBinaryNulls && column.IsBinaryBytes && column.ResolvedGoType == "*[]byte")
		}
	}
	data.ScanSQLServerUUID = config.ScanSQLServerUUID && data.ScanUUID
	addImport := func(path, base string) string {
		if qualifier, ok := imports[path]; ok {
			return qualifier
		}
		qualifier := allocate(base)
		imports[path] = qualifier
		data.Imports = append(data.Imports, Import{Path: path, Qualifier: qualifier})
		return qualifier
	}
	if data.ScanJSON || data.ScanBoolean || data.ScanTime || data.ScanUUID || data.ScanBinary || data.JSONArguments || data.RejectNullUpdateKey ||
		(data.Table.TableType == "BASE TABLE" && len(data.IdentityColumns) > 0 && data.InsertStrategy == InsertExecLastInsertID) {
		data.Names["driver"] = addImport("database/sql/driver", "sqltomDriver")
	}
	if data.UnsignedLastInsertID {
		data.Names["strconv"] = addImport("strconv", "sqltomStrconv")
	}
	data.Names["errors"] = addImport("errors", "sqltomErrors")
	data.Names["fmt"] = addImport("fmt", "sqltomFmt")
	if data.ScanTime {
		data.Names["time"] = addImport("time", "sqltomTime")
	}
	if data.ScanUUID {
		data.Names["strings"] = addImport("strings", "sqltomStrings")
	}
	if data.ScanSQLServerUUID {
		data.Names["mssql"] = addImport("github.com/microsoft/go-mssqldb", "sqltomMSSQL")
	}
	data.Names["scanValue"] = allocate("sqltomScanValue")
	data.Names["jsonValue"] = allocate("sqltomJSONValue")
	data.Names["uuidValue"] = allocate("sqltomUUIDValue")
	data.Names["binaryValue"] = allocate("sqltomBinaryValue")
	// The private projection flag must not hide a manifest field, even when
	// the caller deliberately uses an unexported field name.
	partial := "sqltomPartial"
	for {
		collision := false
		for _, column := range data.Table.Columns {
			if column.GoName == partial {
				collision = true
				break
			}
		}
		if !collision && !used[partial] {
			break
		}
		partial += "_"
	}
	data.Names["partial"] = allocate(partial)
	for _, name := range []string{"predicate", "projection", "columns", "indexes", "seen", "index", "column", "destinations", "db", "stmt", "args", "query", "rows", "err", "result", "r", "row", "lastInsertID", "identityValue", "one", "s", "a", "value", "converted", "bytes", "keyArgs", "keyIndex", "keyValue", "keyNames"} {
		data.Names[name] = allocate(name)
	}
	sort.Slice(data.Imports, func(i, j int) bool { return data.Imports[i].Path < data.Imports[j].Path })
}

func IsWritableColumn(column manifest.Column) bool {
	return !column.IsIdentity && !column.IsComputed && !column.IsGeneratedAlways && !column.IsRowVersion
}

func primaryKeyNames(table manifest.Table) []string {
	primaryKey := make([]manifest.Column, 0)
	for _, column := range table.Columns {
		if column.IsPrimaryKey {
			primaryKey = append(primaryKey, column)
		}
	}
	if len(primaryKey) > 0 {
		sort.Slice(primaryKey, func(i, j int) bool {
			return primaryKey[i].PrimaryKeyOrdinal < primaryKey[j].PrimaryKeyOrdinal
		})
		result := make([]string, len(primaryKey))
		for index, column := range primaryKey {
			result[index] = column.ColumnName
		}
		return result
	}
	return nil
}
