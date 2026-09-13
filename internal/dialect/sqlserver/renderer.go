package sqlserver

import (
	"context"
	"embed"
	"strconv"
	"strings"

	"github.com/catamat/sqltom/internal/manifest"
	commonrender "github.com/catamat/sqltom/internal/render"
)

//go:embed sqlserver.go.tpl
var templateFiles embed.FS

func (*Backend) Render(ctx context.Context, m *manifest.Manifest, outputFolder string) error {
	return commonrender.Render(ctx, m, outputFolder, commonrender.Config{
		Dialect:            "SQL Server",
		TemplateFS:         templateFiles,
		TemplateName:       "sqlserver.go.tpl",
		SQLIdentifier:      sqlIdentifierSource,
		SQLTableIdentifier: sqlTableIdentifierSource,
	})
}

func sqlIdentifierSource(value string) string {
	return sqlIdentifierExpression(delimitSQLServerIdentifier(value))
}

func sqlTableIdentifierSource(catalog, schema, name string) string {
	var identifier string
	switch {
	case catalog != "" && schema != "":
		identifier = delimitSQLServerIdentifier(catalog) + "." +
			delimitSQLServerIdentifier(schema) + "." + delimitSQLServerIdentifier(name)
	case catalog != "":
		identifier = delimitSQLServerIdentifier(catalog) + ".." + delimitSQLServerIdentifier(name)
	case schema != "":
		identifier = delimitSQLServerIdentifier(schema) + "." + delimitSQLServerIdentifier(name)
	default:
		identifier = delimitSQLServerIdentifier(name)
	}
	return sqlIdentifierExpression(identifier)
}

func delimitSQLServerIdentifier(value string) string {
	return "[" + strings.ReplaceAll(value, "]", "]]") + "]"
}

func sqlIdentifierExpression(identifier string) string {
	return "` + " + strconv.Quote(identifier) + " + `"
}
