package testdb

import "database/sql/driver"

// WithPrimaryKeyCounts extends catalog fixtures with the native key size.
// Tests for filtered columns override this value independently of visible rows.
func WithPrimaryKeyCounts(rows [][]driver.Value) [][]driver.Value {
	sizes := map[[3]driver.Value]int64{}
	for _, row := range rows {
		key := [3]driver.Value{row[2], row[3], row[4]}
		if ordinal, ok := row[len(row)-1].(int64); ok && ordinal > sizes[key] {
			sizes[key] = ordinal
		}
	}
	result := make([][]driver.Value, len(rows))
	for i, row := range rows {
		var size driver.Value
		if row[4] != nil {
			size = sizes[[3]driver.Value{row[2], row[3], row[4]}]
		}
		result[i] = append(append([]driver.Value(nil), row...), size)
	}
	return result
}
