package mysql

import (
	"fmt"
	"strings"

	"github.com/catamat/sqltom/internal/manifest"
)

func validateInspectionManifest(m *manifest.Manifest) error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	for tableIndex, table := range m.Tables {
		location := fmt.Sprintf("Tables[%d]", tableIndex)
		if strings.TrimSpace(table.TableSchema) == "" {
			return fmt.Errorf("%s must include TableSchema", location)
		}
		if table.TableSchema != m.DatabaseName {
			return fmt.Errorf("%s.TableSchema %q does not match DatabaseName %q", location, table.TableSchema, m.DatabaseName)
		}
	}
	return nil
}
