package cli

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/dialect/mysql"
	"github.com/catamat/sqltom/internal/dialect/postgres"
	"github.com/catamat/sqltom/internal/dialect/sqlite"
	"github.com/catamat/sqltom/internal/dialect/sqlserver"
	"github.com/catamat/sqltom/internal/manifest"
)

type Runner struct {
	workingDirectory string
	backends         map[string]dialect.Backend
}

var _ Service = (*Runner)(nil)

type Dependencies struct {
	WorkingDirectory string
	Backends         map[string]dialect.Backend
}

type savedInspection struct {
	filename string
	document *manifest.Manifest
	backend  dialect.Backend
}

func New(workingDirectory string) *Runner {
	return NewWithDependencies(Dependencies{
		WorkingDirectory: workingDirectory,
		Backends: map[string]dialect.Backend{
			dialect.MySQL:     mysql.New(),
			dialect.Postgres:  postgres.New(),
			dialect.SQLite:    sqlite.New(),
			dialect.SQLServer: sqlserver.New(),
		},
	})
}

func NewWithDependencies(dependencies Dependencies) *Runner {
	backends := make(map[string]dialect.Backend, len(dependencies.Backends))
	for name, backend := range dependencies.Backends {
		backends[normalizeDialect(name)] = backend
	}
	return &Runner{
		workingDirectory: dependencies.WorkingDirectory,
		backends:         backends,
	}
}

func (s *Runner) Inspect(ctx context.Context, dialectName, dsn string, tables []string) (OperationResult, error) {
	result, err := s.inspectAndSave(ctx, dialectName, dsn, tables)
	if err != nil {
		return OperationResult{}, err
	}
	return resultForManifest(result.filename, result.document), nil
}

func (s *Runner) Render(ctx context.Context, dialectName, manifestFilename, outputFolder string, tables []string) (OperationResult, error) {
	backend, err := s.backend(dialectName)
	if err != nil {
		return OperationResult{}, err
	}
	m, err := manifest.Load(manifestFilename)
	if err != nil {
		return OperationResult{}, fmt.Errorf("load manifest %q: %w", manifestFilename, err)
	}
	selected, err := manifest.SelectTables(m, tables)
	if err != nil {
		return OperationResult{}, fmt.Errorf("select tables from manifest %q: %w", manifestFilename, err)
	}
	managed, err := manifest.ManagedTables(selected)
	if err != nil {
		return OperationResult{}, fmt.Errorf("select managed tables from manifest %q: %w", manifestFilename, err)
	}
	result := resultForManifest("", selected)
	if err := backend.Render(ctx, managed, outputFolder); err != nil {
		renderErr := fmt.Errorf("render manifest %q: %w", manifestFilename, err)
		if outputWasCommitted(err) {
			result.Warnings = append(result.Warnings, renderErr.Error())
			return result, nil
		}
		return OperationResult{}, renderErr
	}
	return result, nil
}

func (s *Runner) Generate(ctx context.Context, dialectName, dsn, outputFolder string, tables []string) (OperationResult, error) {
	result, err := s.inspectAndSave(ctx, dialectName, dsn, tables)
	if err != nil {
		return OperationResult{}, err
	}
	operationResult := resultForManifest(result.filename, result.document)
	managed, err := manifest.ManagedTables(result.document)
	if err != nil {
		return OperationResult{}, fmt.Errorf("select managed tables from generated manifest %q: %w", result.filename, err)
	}
	if err := result.backend.Render(ctx, managed, outputFolder); err != nil {
		renderErr := fmt.Errorf("render generated manifest %q: %w", result.filename, err)
		if outputWasCommitted(err) {
			operationResult.Warnings = append(operationResult.Warnings, renderErr.Error())
			return operationResult, nil
		}
		return OperationResult{}, renderErr
	}
	return operationResult, nil
}

func outputWasCommitted(err error) bool {
	var state interface{ OutputCommitted() bool }
	return errors.As(err, &state) && state.OutputCommitted()
}

func resultForManifest(filename string, document *manifest.Manifest) OperationResult {
	result := OperationResult{ManifestFilename: filename}
	if document == nil {
		return result
	}
	result.Statistics.Tables = len(document.Tables)
	for _, table := range document.Tables {
		if table.IsManaged {
			result.Statistics.ManagedTables++
			if goName, err := manifest.EffectiveTableGoName(table); err == nil && !ast.IsExported(goName) {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"table %q effective GoName %q is not exported; set GoName explicitly to expose the generated model",
					table.Key(),
					goName,
				))
			}
		}
		if table.GoName != "" && table.GoName != table.TableName {
			result.Statistics.RenamedTables++
		}
		result.Statistics.Columns += len(table.Columns)
		for _, column := range table.Columns {
			if goName, err := manifest.EffectiveColumnGoName(column); err == nil && !ast.IsExported(goName) {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"table %q column %q effective GoName %q is not exported; set GoName explicitly to expose the generated field",
					table.Key(),
					column.ColumnName,
					goName,
				))
			}
			if column.GoName != "" && column.GoName != column.ColumnName {
				result.Statistics.RenamedColumns++
			}
		}
	}
	return result
}

func (s *Runner) inspectAndSave(ctx context.Context, dialectName, dsn string, tables []string) (savedInspection, error) {
	dialectName = normalizeDialect(dialectName)
	backend, err := s.backend(dialectName)
	if err != nil {
		return savedInspection{}, err
	}
	inspection, err := backend.Inspect(ctx, dsn)
	if err != nil {
		return savedInspection{}, fmt.Errorf("inspect %s database: %w", dialectName, err)
	}
	if inspection == nil || inspection.Manifest == nil {
		return savedInspection{}, fmt.Errorf("inspect %s database: backend returned no manifest", dialectName)
	}
	if inspection.DatabaseName == "" {
		return savedInspection{}, fmt.Errorf("inspect %s database: backend returned an empty database name", dialectName)
	}
	if inspection.Manifest.DatabaseName != inspection.DatabaseName {
		return savedInspection{}, fmt.Errorf("inspect %s database: connection DatabaseName %q does not match manifest DatabaseName %q", dialectName, inspection.DatabaseName, inspection.Manifest.DatabaseName)
	}
	if err := manifest.ValidateStructure(inspection.Manifest); err != nil {
		return savedInspection{}, fmt.Errorf("validate inspected manifest: %w", err)
	}
	selected, err := manifest.SelectTables(inspection.Manifest, tables)
	if err != nil {
		return savedInspection{}, fmt.Errorf("select inspected tables: %w", err)
	}
	inspection.Manifest = selected

	filename, err := manifest.ManifestFilename(inspection.DatabaseName)
	if err != nil {
		return savedInspection{}, fmt.Errorf("derive manifest filename: %w", err)
	}
	workingDirectory := s.workingDirectory
	if workingDirectory == "" {
		workingDirectory, err = os.Getwd()
		if err != nil {
			return savedInspection{}, fmt.Errorf("read working directory: %w", err)
		}
	}
	manifestFilename := filepath.Join(workingDirectory, filename)

	var previous *manifest.Manifest
	if _, err := os.Stat(manifestFilename); err == nil {
		previous, err = manifest.Load(manifestFilename)
		if err != nil {
			return savedInspection{}, fmt.Errorf("load existing manifest %q: %w", manifestFilename, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return savedInspection{}, fmt.Errorf("inspect existing manifest %q: %w", manifestFilename, err)
	}
	merged, err := manifest.Merge(inspection.Manifest, previous)
	if err != nil {
		return savedInspection{}, fmt.Errorf("merge manifest %q: %w", manifestFilename, err)
	}
	if err := ctx.Err(); err != nil {
		return savedInspection{}, fmt.Errorf("inspection canceled before saving manifest %q: %w", manifestFilename, err)
	}
	if err := manifest.SaveAtomic(manifestFilename, merged); err != nil {
		return savedInspection{}, fmt.Errorf("save manifest %q: %w", manifestFilename, err)
	}
	return savedInspection{filename: manifestFilename, document: merged, backend: backend}, nil
}

func (s *Runner) backend(dialectName string) (dialect.Backend, error) {
	dialectName = normalizeDialect(dialectName)
	if !dialect.IsKnown(dialectName) {
		return nil, fmt.Errorf("unknown dialect %q", dialectName)
	}
	backend, exists := s.backends[dialectName]
	if !exists || backend == nil {
		return nil, dialect.NotImplementedError{Dialect: dialectName}
	}
	return backend, nil
}

func normalizeDialect(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
