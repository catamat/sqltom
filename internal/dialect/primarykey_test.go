package dialect

import (
	"testing"

	"github.com/catamat/sqltom/internal/manifest"
)

func TestPrimaryKeyCounts(t *testing.T) {
	table := manifest.Table{TableName: "Items", Columns: []manifest.Column{{ColumnName: "First", IsPrimaryKey: true}, {ColumnName: "Second", IsPrimaryKey: true}}}
	for _, test := range []struct {
		name      string
		sizes     []int64
		wantError bool
	}{
		{"complete", []int64{2, 2}, false},
		{"missing metadata", nil, true},
		{"missing tail", []int64{3}, true},
		{"negative", []int64{-1}, true},
		{"inconsistent", []int64{2, 1}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			counts := PrimaryKeyCounts{}
			var err error
			for _, size := range test.sizes {
				if err = counts.Observe(table.Key(), size); err != nil {
					break
				}
			}
			if err == nil {
				err = counts.Validate(table)
			}
			if (err != nil) != test.wantError {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}
