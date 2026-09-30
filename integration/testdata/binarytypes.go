package binarytypes

import (
	"database/sql"
	"database/sql/driver"
)

// Value is an application-owned binary codec used to test manifest overrides.
type Value struct {
	Bytes []byte
	Calls *int
	Err   error
}

func (v Value) Value() (driver.Value, error) {
	if v.Calls != nil {
		*v.Calls++
	}
	if v.Err != nil {
		return nil, v.Err
	}
	return v.Bytes, nil
}

func (v *Value) Scan(value any) error {
	// A custom codec owns its treatment of the driver's empty BLOB representation.
	if bytes, ok := value.([]byte); ok && bytes == nil {
		v.Bytes = []byte{}
		return nil
	}
	return sql.ConvertAssign(driver.ScanContext{}, &v.Bytes, value)
}
