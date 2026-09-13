package manifest

const (
	DataTypeBigInt           = "bigint"
	DataTypeBinary           = "binary"
	DataTypeBoolean          = "boolean"
	DataTypeDate             = "date"
	DataTypeDateTime         = "datetime"
	DataTypeDateTimeOffset   = "datetimeoffset"
	DataTypeDecimal          = "decimal"
	DataTypeDouble           = "double"
	DataTypeInteger          = "integer"
	DataTypeJSON             = "json"
	DataTypeMoney            = "money"
	DataTypeReal             = "real"
	DataTypeRowVersion       = "rowversion"
	DataTypeSmallInt         = "smallint"
	DataTypeString           = "string"
	DataTypeTime             = "time"
	DataTypeTinyInt          = "tinyint"
	DataTypeUnsignedBigInt   = "unsignedbigint"
	DataTypeUnsignedInteger  = "unsignedinteger"
	DataTypeUnsignedSmallInt = "unsignedsmallint"
	DataTypeUnsignedTinyInt  = "unsignedtinyint"
	DataTypeUUID             = "uuid"
	DataTypeAny              = "any"
	DataTypeVariant          = "variant"
	DataTypeXML              = "xml"
)

var builtinTypeMappings = map[string]TypeMapping{
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
	DataTypeAny:              {GoType: "any", NullableGoType: "any"},
	DataTypeVariant:          {GoType: "any", NullableGoType: "any"},
	DataTypeXML:              {GoType: "[]byte"},
}

var dataTypeAliases = map[string]string{
	DataTypeBigInt:           DataTypeBigInt,
	DataTypeBinary:           DataTypeBinary,
	DataTypeBoolean:          DataTypeBoolean,
	DataTypeDate:             DataTypeDate,
	DataTypeDateTime:         DataTypeDateTime,
	DataTypeDateTimeOffset:   DataTypeDateTimeOffset,
	DataTypeDecimal:          DataTypeDecimal,
	DataTypeDouble:           DataTypeDouble,
	DataTypeInteger:          DataTypeInteger,
	DataTypeJSON:             DataTypeJSON,
	DataTypeMoney:            DataTypeMoney,
	DataTypeReal:             DataTypeReal,
	DataTypeRowVersion:       DataTypeRowVersion,
	DataTypeSmallInt:         DataTypeSmallInt,
	DataTypeString:           DataTypeString,
	DataTypeTime:             DataTypeTime,
	DataTypeTinyInt:          DataTypeTinyInt,
	DataTypeUnsignedBigInt:   DataTypeUnsignedBigInt,
	DataTypeUnsignedInteger:  DataTypeUnsignedInteger,
	DataTypeUnsignedSmallInt: DataTypeUnsignedSmallInt,
	DataTypeUnsignedTinyInt:  DataTypeUnsignedTinyInt,
	DataTypeUUID:             DataTypeUUID,
	DataTypeAny:              DataTypeAny,
	DataTypeVariant:          DataTypeVariant,
	DataTypeXML:              DataTypeXML,

	"bool":             DataTypeBoolean,
	"bytes":            DataTypeBinary,
	"date time":        DataTypeDateTime,
	"date time offset": DataTypeDateTimeOffset,
	"float32":          DataTypeReal,
	"float64":          DataTypeDouble,
	"guid":             DataTypeUUID,
	"int":              DataTypeInteger,
	"int16":            DataTypeSmallInt,
	"int64":            DataTypeBigInt,
	"text":             DataTypeString,
	"uint8":            DataTypeTinyInt,
}

// CanonicalDataType translates a canonical data type or a known alias into the
// dialect-neutral vocabulary used by manifests.
func CanonicalDataType(dataType string) (string, bool) {
	canonical, ok := dataTypeAliases[normalize(dataType)]
	return canonical, ok
}

func builtinMapping(dataType string) (TypeMapping, bool) {
	canonical, ok := CanonicalDataType(dataType)
	if !ok {
		return TypeMapping{}, false
	}
	mapping, ok := builtinTypeMappings[canonical]
	return mapping, ok
}
