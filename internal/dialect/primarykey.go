package dialect

import (
	"fmt"

	"github.com/catamat/sqltom/internal/manifest"
)

// PrimaryKeyCounts records native key sizes independently of column visibility.
// Ordinal validation alone cannot detect a missing tail (or an entirely hidden key).
type PrimaryKeyCounts map[manifest.TableKey]int64

func (counts PrimaryKeyCounts) Observe(key manifest.TableKey, size int64) error {
	if size < 0 {
		return fmt.Errorf("invalid primary key size for %q: %d", key, size)
	}
	if previous, found := counts[key]; found && previous != size {
		return fmt.Errorf("inconsistent primary key metadata for %q", key)
	}
	counts[key] = size
	return nil
}

func (counts PrimaryKeyCounts) Validate(table manifest.Table) error {
	var visible int64
	for _, column := range table.Columns {
		if column.IsPrimaryKey {
			visible++
		}
	}
	expected, found := counts[table.Key()]
	if !found || visible != expected {
		return fmt.Errorf("incomplete primary key metadata for %q: visible %d of %d columns; inspect with permissions exposing the complete primary key", table.Key(), visible, expected)
	}
	return nil
}
