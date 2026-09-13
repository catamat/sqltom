package render

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"text/template"

	"github.com/catamat/sqltom/internal/manifest"
	"github.com/catamat/sqltom/internal/output"
)

type Config struct {
	Dialect            string
	TemplateFS         fs.FS
	TemplateName       string
	SQLIdentifier      func(string) string
	SQLTableIdentifier func(catalog, schema, name string) string
}

func Render(ctx context.Context, m *manifest.Manifest, outputFolder string, config Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(m); err != nil {
		return fmt.Errorf("validate manifest for rendering: %w", err)
	}
	tpl, err := parseTemplate(config)
	if err != nil {
		return err
	}

	return output.Build(ctx, outputFolder, func(ctx context.Context, stagingDir string) error {
		for _, table := range m.Tables {
			if err := ctx.Err(); err != nil {
				return err
			}
			data, err := BuildData(m, table)
			if err != nil {
				return fmt.Errorf("prepare table %q: %w", table.Key(), err)
			}
			if err := renderTable(tpl, config.TemplateName, stagingDir, data); err != nil {
				return fmt.Errorf("render table %q: %w", table.Key(), err)
			}
		}
		return nil
	})
}

func parseTemplate(config Config) (*template.Template, error) {
	functions := template.FuncMap{
		"add": func(x, y int) int { return x + y },
		"last": func(index int, value any) bool {
			return index == reflect.ValueOf(value).Len()-1
		},
		"quote":              strconv.Quote,
		"sqlIdentifier":      config.SQLIdentifier,
		"sqlTableIdentifier": config.SQLTableIdentifier,
		"structTag": func(databaseName, jsonName string, includeJSON bool) string {
			tag := "db:" + strconv.Quote(databaseName)
			if includeJSON {
				tag += " json:" + strconv.Quote(jsonName)
			}
			if strings.ContainsRune(tag, '`') {
				return strconv.Quote(tag)
			}
			return "`" + tag + "`"
		},
	}
	tpl, err := template.New(config.TemplateName).Funcs(functions).ParseFS(config.TemplateFS, config.TemplateName)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", config.Dialect, err)
	}
	return tpl, nil
}

func renderTable(tpl *template.Template, templateName, staging string, data Data) error {
	var rendered bytes.Buffer
	if err := tpl.ExecuteTemplate(&rendered, templateName, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	formatted, err := format.Source(rendered.Bytes())
	if err != nil {
		return fmt.Errorf("format generated Go: %w", err)
	}
	directory := filepath.Join(staging, data.Table.FileName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create model directory: %w", err)
	}
	filename := filepath.Join(directory, data.Table.FileName+".go")
	if err := os.WriteFile(filename, formatted, 0o644); err != nil {
		return fmt.Errorf("write model: %w", err)
	}
	return nil
}
