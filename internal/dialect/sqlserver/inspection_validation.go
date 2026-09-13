package sqlserver

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
		if strings.TrimSpace(table.TableCatalog) == "" || strings.TrimSpace(table.TableSchema) == "" {
			return fmt.Errorf("%s must include TableCatalog and TableSchema", location)
		}
		if table.TableCatalog != m.DatabaseName {
			return fmt.Errorf("%s.TableCatalog %q does not match DatabaseName %q", location, table.TableCatalog, m.DatabaseName)
		}
	}
	return nil
}
