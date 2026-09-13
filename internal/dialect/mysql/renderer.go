package mysql

import (
	"context"
	"embed"
	"strconv"
	"strings"

	"github.com/catamat/sqltom/internal/manifest"
	commonrender "github.com/catamat/sqltom/internal/render"
)

//go:embed mysql.go.tpl
var templateFiles embed.FS

func (*Backend) Render(ctx context.Context, m *manifest.Manifest, outputFolder string) error {
	return commonrender.Render(ctx, m, outputFolder, commonrender.Config{
		Dialect:            "MySQL",
		TemplateFS:         templateFiles,
		TemplateName:       "mysql.go.tpl",
		SQLIdentifier:      sqlIdentifierSource,
		SQLTableIdentifier: sqlTableIdentifierSource,
	})
}

func sqlIdentifierSource(value string) string {
	return sqlIdentifierExpression(delimitIdentifier(value))
}

func sqlTableIdentifierSource(_ string, schema, name string) string {
	identifier := delimitIdentifier(name)
	if schema != "" {
		identifier = delimitIdentifier(schema) + "." + identifier
	}
	return sqlIdentifierExpression(identifier)
}

func delimitIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func sqlIdentifierExpression(identifier string) string {
	return "` + " + strconv.Quote(identifier) + " + `"
}
