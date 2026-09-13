package dialect

import (
	"context"
	"fmt"
	"strings"

	"github.com/catamat/sqltom/internal/manifest"
)

const (
	Postgres  = "postgres"
	MySQL     = "mysql"
	SQLite    = "sqlite"
	SQLServer = "sqlserver"
)

var names = [...]string{Postgres, MySQL, SQLite, SQLServer}

// Names returns every dialect accepted by the CLI in display order. The
// returned slice is independent and may be modified by the caller.
func Names() []string {
	return append([]string(nil), names[:]...)
}

func IsKnown(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range names {
		if name == candidate {
			return true
		}
	}
	return false
}

type Inspection struct {
	DatabaseName string
	Manifest     *manifest.Manifest
}

type Backend interface {
	Inspect(ctx context.Context, dsn string) (*Inspection, error)
	Render(ctx context.Context, m *manifest.Manifest, outputFolder string) error
}

type NotImplementedError struct {
	Dialect string
}

func (e NotImplementedError) Error() string {
	return fmt.Sprintf("dialect %q is not implemented", e.Dialect)
}
