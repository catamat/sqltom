package cli

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect"
	"github.com/catamat/sqltom/internal/manifest"
)

type fakeBackend struct {
	inspection   *dialect.Inspection
	inspectErr   error
	renderErr    error
	inspectCalls int
	renderCalls  int
	dsn          string
	m            *manifest.Manifest
	output       string
	inspectHook  func()
}

type committedOutputError struct{}

func (committedOutputError) Error() string {
	return "new output is active; cleanup failed"
}

func (committedOutputError) OutputCommitted() bool {
	return true
}

var _ dialect.Backend = (*fakeBackend)(nil)

func (f *fakeBackend) Inspect(_ context.Context, dsn string) (*dialect.Inspection, error) {
	f.inspectCalls++
	f.dsn = dsn
	if f.inspectHook != nil {
		f.inspectHook()
	}
	return f.inspection, f.inspectErr
}

func (f *fakeBackend) Render(_ context.Context, m *manifest.Manifest, outputFolder string) error {
	f.renderCalls++
	f.m, f.output = m, outputFolder
	return f.renderErr
}

func TestGenerateSavesMergedManifestThenRendersIt(t *testing.T) {
	directory := t.TempDir()
	previous := serviceManifest()
	addServiceTable(previous, "dbo", "Widget", "BASE TABLE")
	previous.Tables[0].GoName = "VehicleModel"
	previous.TypeMappings["int"] = manifest.TypeMapping{GoType: "int64"}
	filename := filepath.Join(directory, "sqltom_MainDB.json")
	if err := manifest.SaveAtomic(filename, previous); err != nil {
		t.Fatal(err)
	}

	fresh := serviceManifest()
	addServiceTable(fresh, "dbo", "Widget", "BASE TABLE")
	fresh.Tables[0].Columns[0].IsNullable = true
	backend := &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: fresh}}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})

	result, err := service.Generate(context.Background(), dialect.SQLServer, "opaque-dsn", "models", []string{"dbo.Vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ManifestFilename != filename || backend.inspectCalls != 1 || backend.dsn != "opaque-dsn" || backend.renderCalls != 1 {
		t.Fatalf("orchestration: result=%#v backend=%#v", result, backend)
	}
	if backend.m == nil {
		t.Fatal("renderer received no manifest")
	}
	if len(backend.m.Tables) != 1 || backend.m.Tables[0].TableName != "Vehicle" || backend.m.Tables[0].GoName != "VehicleModel" || !backend.m.Tables[0].Columns[0].IsNullable {
		t.Fatalf("backend manifest tables = %#v", backend.m.Tables)
	}
	if backend.m.TypeMappings["int"].GoType != "int64" {
		t.Fatalf("backend mappings = %#v", backend.m.TypeMappings)
	}
	loaded, err := manifest.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tables) != 1 || loaded.Tables[0].TableName != "Vehicle" || loaded.Tables[0].GoName != backend.m.Tables[0].GoName {
		t.Fatal("backend did not receive the persisted manifest")
	}
}

func TestGeneratePersistsUnmanagedStubsAndRendersOnlyManagedTables(t *testing.T) {
	directory := t.TempDir()
	previous := serviceManifest()
	addServiceTable(previous, "dbo", "Widget", "BASE TABLE")
	for tableIndex := range previous.Tables {
		if previous.Tables[tableIndex].TableName == "Widget" {
			previous.Tables[tableIndex].IsManaged = false
			previous.Tables[tableIndex].FileName = "WidgetFile"
			previous.Tables[tableIndex].PackageName = "widgetpackage"
			previous.Tables[tableIndex].GoName = "WidgetModel"
		}
	}
	filename := filepath.Join(directory, "sqltom_MainDB.json")
	if err := manifest.SaveAtomic(filename, previous); err != nil {
		t.Fatal(err)
	}

	fresh := serviceManifest()
	addServiceTable(fresh, "dbo", "Widget", "BASE TABLE")
	backend := &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: fresh}}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})

	result, err := service.Generate(context.Background(), dialect.SQLServer, "dsn", "models", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ManifestFilename != filename || result.Statistics.Tables != 2 || result.Statistics.ManagedTables != 1 || result.Statistics.Columns != 1 {
		t.Fatalf("Generate() result = %#v", result)
	}
	if backend.m == nil || len(backend.m.Tables) != 1 || backend.m.Tables[0].TableName != "Vehicle" {
		t.Fatalf("renderer received unmanaged tables: %#v", backend.m)
	}

	loaded, err := manifest.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tables) != 2 {
		t.Fatalf("persisted tables = %#v", loaded.Tables)
	}
	for _, table := range loaded.Tables {
		if table.TableName != "Widget" {
			continue
		}
		if table.IsManaged || table.FileName != "" || table.PackageName != "" || table.GoName != "" || table.Columns == nil || len(table.Columns) != 0 {
			t.Fatalf("persisted unmanaged table = %#v", table)
		}
	}
}

func TestInspectPersistsOnlySelectedTablesAndPreservesOverrides(t *testing.T) {
	directory := t.TempDir()
	previous := serviceManifest()
	addServiceTable(previous, "dbo", "Widget", "BASE TABLE")
	previous.Tables[0].GoName = "VehicleModel"
	previous.TypeMappings["integer"] = manifest.TypeMapping{GoType: "int64"}
	filename := filepath.Join(directory, "sqltom_MainDB.json")
	if err := manifest.SaveAtomic(filename, previous); err != nil {
		t.Fatal(err)
	}

	fresh := serviceManifest()
	addServiceTable(fresh, "dbo", "Widget", "BASE TABLE")
	backend := &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: fresh}}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	if _, err := service.Inspect(context.Background(), dialect.SQLServer, "dsn", []string{"dbo.Vehicle"}); err != nil {
		t.Fatal(err)
	}

	loaded, err := manifest.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tables) != 1 || loaded.Tables[0].TableName != "Vehicle" || loaded.Tables[0].GoName != "VehicleModel" {
		t.Fatalf("filtered manifest tables = %#v", loaded.Tables)
	}
	if loaded.TypeMappings["integer"].GoType != "int64" {
		t.Fatalf("filtered manifest mappings = %#v", loaded.TypeMappings)
	}
}

func TestInspectUnknownSelectionDoesNotOverwriteManifest(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "sqltom_MainDB.json")
	if err := manifest.SaveAtomic(filename, serviceManifest()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: serviceManifest()}}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	if _, err := service.Inspect(context.Background(), dialect.SQLServer, "dsn", []string{"Missing"}); err == nil {
		t.Fatal("expected unknown table selection error")
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("selection error overwrote the existing manifest")
	}
}

func TestGenerateUnknownSelectionDoesNotOverwriteManifestOrRender(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "sqltom_MainDB.json")
	if err := manifest.SaveAtomic(filename, serviceManifest()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: serviceManifest()}}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	if _, err := service.Generate(context.Background(), dialect.SQLServer, "dsn", "models", []string{"Missing"}); err == nil {
		t.Fatal("expected unknown table selection error")
	}
	if backend.renderCalls != 0 {
		t.Fatalf("renderer called after selection error: %#v", backend)
	}
	after, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("generate selection error overwrote the existing manifest")
	}
}

func TestInvalidExistingManifestIsNotOverwritten(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "sqltom_MainDB.json")
	original := []byte(`{"broken":true}`)
	if err := os.WriteFile(filename, original, 0o644); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: serviceManifest()}}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	if _, err := service.Inspect(context.Background(), dialect.SQLServer, "dsn", nil); err == nil {
		t.Fatal("expected invalid existing manifest error")
	}
	current, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatalf("invalid manifest was overwritten: %q", current)
	}
}

func TestInspectionDatabaseMustMatchManifest(t *testing.T) {
	directory := t.TempDir()
	fresh := serviceManifest()
	fresh.DatabaseName = "OtherDB"
	fresh.Tables[0].TableCatalog = "OtherDB"
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends: map[string]dialect.Backend{
			dialect.SQLServer: &fakeBackend{inspection: &dialect.Inspection{
				DatabaseName: "MainDB",
				Manifest:     fresh,
			}},
		},
	})
	if _, err := service.Inspect(context.Background(), dialect.SQLServer, "dsn", nil); err == nil {
		t.Fatal("expected database identity error")
	}
}

func TestCanceledInspectionDoesNotPersistManifest(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	backend := &fakeBackend{
		inspection:  &dialect.Inspection{DatabaseName: "MainDB", Manifest: serviceManifest()},
		inspectHook: cancel,
	}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
	})

	_, err := service.Inspect(ctx, dialect.SQLServer, "dsn", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Inspect() error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "sqltom_MainDB.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("canceled inspection persisted a manifest: %v", statErr)
	}
}

func TestSanitizedManifestFilenamesLetDifferentDatabasesCoexist(t *testing.T) {
	directory := t.TempDir()
	previous := serviceManifest()
	previous.DatabaseName = "A/B"
	previous.Tables[0].TableCatalog = "A/B"
	previousBase, err := manifest.ManifestFilename(previous.DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	previousFilename := filepath.Join(directory, previousBase)
	if err := manifest.SaveAtomic(previousFilename, previous); err != nil {
		t.Fatal(err)
	}
	fresh := serviceManifest()
	fresh.DatabaseName = "AB"
	fresh.Tables[0].TableCatalog = "AB"
	freshBase, err := manifest.ManifestFilename(fresh.DatabaseName)
	if err != nil {
		t.Fatal(err)
	}
	if previousBase == freshBase {
		t.Fatalf("manifest filenames collide: %q", previousBase)
	}
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends: map[string]dialect.Backend{
			dialect.SQLServer: &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "AB", Manifest: fresh}},
		},
	})
	result, err := service.Inspect(context.Background(), dialect.SQLServer, "dsn", nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(directory, freshBase); result.ManifestFilename != want {
		t.Fatalf("manifest filename = %q, want %q", result.ManifestFilename, want)
	}
	loadedPrevious, err := manifest.Load(previousFilename)
	if err != nil {
		t.Fatal(err)
	}
	loadedFresh, err := manifest.Load(result.ManifestFilename)
	if err != nil {
		t.Fatal(err)
	}
	if loadedPrevious.DatabaseName != "A/B" || loadedFresh.DatabaseName != "AB" {
		t.Fatalf("database manifests = %q and %q", loadedPrevious.DatabaseName, loadedFresh.DatabaseName)
	}
}

func TestExistingManifestForAnotherDatabaseIsNotOverwritten(t *testing.T) {
	directory := t.TempDir()
	previous := serviceManifest()
	previous.DatabaseName = "A/B"
	previous.Tables[0].TableCatalog = "A/B"
	filename := filepath.Join(directory, "sqltom_AB.json")
	if err := manifest.SaveAtomic(filename, previous); err != nil {
		t.Fatal(err)
	}
	fresh := serviceManifest()
	fresh.DatabaseName = "AB"
	fresh.Tables[0].TableCatalog = "AB"
	service := NewWithDependencies(Dependencies{
		WorkingDirectory: directory,
		Backends: map[string]dialect.Backend{
			dialect.SQLServer: &fakeBackend{inspection: &dialect.Inspection{DatabaseName: "AB", Manifest: fresh}},
		},
	})
	if _, err := service.Inspect(context.Background(), dialect.SQLServer, "dsn", nil); err == nil {
		t.Fatal("expected existing manifest database identity error")
	}
	loaded, err := manifest.Load(filename)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DatabaseName != "A/B" {
		t.Fatalf("existing manifest was overwritten: DatabaseName = %q", loaded.DatabaseName)
	}
}

func TestNewRegistersAllSupportedBackends(t *testing.T) {
	service := New(t.TempDir())
	for _, name := range dialect.Names() {
		if service.backends[name] == nil {
			t.Errorf("backend %q is not registered", name)
		}
	}
	if len(service.backends) != len(dialect.Names()) {
		t.Errorf("registered backends = %d, supported dialects = %d", len(service.backends), len(dialect.Names()))
	}
}

func TestSQLiteGenerateRunsEndToEnd(t *testing.T) {
	directory := t.TempDir()
	databaseFilename := filepath.Join(directory, "fleet.db")
	db, err := sql.Open("sqlite", databaseFilename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE Vehicle (ID INTEGER PRIMARY KEY, Name TEXT NOT NULL)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	service := New(directory)
	output := filepath.Join(directory, "models")
	result, err := service.Generate(context.Background(), dialect.SQLite, databaseFilename, output, []string{"main.Vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Statistics.Tables != 1 || result.Statistics.ManagedTables != 1 || result.Statistics.Columns != 2 {
		t.Fatalf("statistics = %#v", result.Statistics)
	}
	if _, err := os.Stat(result.ManifestFilename); err != nil {
		t.Fatalf("manifest was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "Vehicle", "Vehicle.go")); err != nil {
		t.Fatalf("model was not written: %v", err)
	}
}

func TestKnownUnimplementedDialectStopsBeforeInspection(t *testing.T) {
	service := NewWithDependencies(Dependencies{Backends: map[string]dialect.Backend{}})
	if _, err := service.Inspect(context.Background(), dialect.Postgres, "dsn", nil); err == nil {
		t.Fatal("expected not implemented error")
	} else {
		var notImplemented dialect.NotImplementedError
		if !errors.As(err, &notImplemented) {
			t.Fatalf("error = %T %v", err, err)
		}
	}
}

func TestRenderSelectsBackendFromExplicitDialect(t *testing.T) {
	directory := t.TempDir()
	manifestFilename := filepath.Join(directory, "manifest.json")
	if err := manifest.SaveAtomic(manifestFilename, serviceManifest()); err != nil {
		t.Fatal(err)
	}
	sqlServerBackend := &fakeBackend{}
	postgresBackend := &fakeBackend{}
	service := NewWithDependencies(Dependencies{
		Backends: map[string]dialect.Backend{
			dialect.SQLServer: sqlServerBackend,
			dialect.Postgres:  postgresBackend,
		},
	})
	if _, err := service.Render(context.Background(), dialect.Postgres, manifestFilename, "models", nil); err != nil {
		t.Fatal(err)
	}
	if postgresBackend.renderCalls != 1 || postgresBackend.m == nil || postgresBackend.output != "models" {
		t.Fatalf("PostgreSQL render dispatch = %#v", postgresBackend)
	}
	if sqlServerBackend.renderCalls != 0 {
		t.Fatalf("SQL Server backend called for PostgreSQL target: %#v", sqlServerBackend)
	}
}

func TestRenderRejectsKnownUnimplementedExplicitDialect(t *testing.T) {
	directory := t.TempDir()
	m := serviceManifest()
	manifestFilename := filepath.Join(directory, "manifest.json")
	if err := manifest.SaveAtomic(manifestFilename, m); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{}
	service := NewWithDependencies(Dependencies{
		Backends: map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	_, err := service.Render(context.Background(), dialect.Postgres, manifestFilename, "models", nil)
	var notImplemented dialect.NotImplementedError
	if !errors.As(err, &notImplemented) {
		t.Fatalf("Render() error = %T %v", err, err)
	}
	if backend.renderCalls != 0 {
		t.Fatalf("SQL Server backend called for PostgreSQL target: %#v", backend)
	}
}

func TestFilteredRenderOutputContainsOnlySelectedModels(t *testing.T) {
	directory := t.TempDir()
	manifestFilename := filepath.Join(directory, "manifest.json")
	output := filepath.Join(directory, "models")

	first := serviceManifest()
	obsolete := first.Tables[0]
	obsolete.TableSchema = "archive"
	obsolete.TableName = "Obsolete"
	obsolete.Columns = append([]manifest.Column(nil), obsolete.Columns...)
	first.Tables = append(first.Tables, obsolete)
	if err := manifest.SaveAtomic(manifestFilename, first); err != nil {
		t.Fatal(err)
	}

	service := New(directory)
	if _, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, output, nil); err != nil {
		t.Fatal(err)
	}
	obsoleteModel := filepath.Join(output, "Obsolete", "Obsolete.go")
	if _, err := os.Stat(obsoleteModel); err != nil {
		t.Fatalf("first render did not create obsolete fixture model: %v", err)
	}

	if _, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, output, []string{"dbo.Vehicle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(obsoleteModel); !os.IsNotExist(err) {
		t.Fatalf("model omitted by the current selection survived output replacement: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "Vehicle", "Vehicle.go")); err != nil {
		t.Fatalf("current model is missing after output replacement: %v", err)
	}
	loaded, err := manifest.Load(manifestFilename)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tables) != 2 {
		t.Fatalf("filtered render modified the source manifest: %#v", loaded.Tables)
	}
}

func TestRenderPassesOnlyManagedTablesToBackend(t *testing.T) {
	directory := t.TempDir()
	manifestFilename := filepath.Join(directory, "manifest.json")
	m := serviceManifest()
	addServiceTable(m, "audit", "VehicleLog", "VIEW")
	for tableIndex := range m.Tables {
		if m.Tables[tableIndex].TableName == "VehicleLog" {
			m.Tables[tableIndex].IsManaged = false
		}
	}
	if err := manifest.SaveAtomic(manifestFilename, m); err != nil {
		t.Fatal(err)
	}

	backend := &fakeBackend{}
	service := NewWithDependencies(Dependencies{
		Backends: map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	result, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, "models", nil)
	if err != nil {
		t.Fatal(err)
	}
	if backend.m == nil || len(backend.m.Tables) != 1 || backend.m.Tables[0].TableName != "Vehicle" {
		t.Fatalf("renderer manifest = %#v", backend.m)
	}
	if result.Statistics.Tables != 2 || result.Statistics.ManagedTables != 1 || result.Statistics.Columns != 1 {
		t.Fatalf("Render() statistics = %#v", result.Statistics)
	}
	loaded, err := manifest.Load(manifestFilename)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tables) != 2 {
		t.Fatalf("render modified source manifest: %#v", loaded.Tables)
	}
}

func TestRenderFiltersBeforeBackendSpecificValidation(t *testing.T) {
	directory := t.TempDir()
	manifestFilename := filepath.Join(directory, "manifest.json")
	output := filepath.Join(directory, "models")
	m := serviceManifest()
	broken := m.Tables[0]
	broken.TableName = "Broken"
	broken.Columns = append([]manifest.Column(nil), broken.Columns...)
	broken.Columns[0].ColumnName = "ID"
	broken.Columns[0].IsPrimaryKey = false
	broken.Columns[0].PrimaryKeyOrdinal = 0
	m.Tables = append(m.Tables, broken)
	if err := manifest.SaveAtomic(manifestFilename, m); err != nil {
		t.Fatal(err)
	}

	service := New(directory)
	if _, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, filepath.Join(directory, "unfiltered"), nil); err == nil {
		t.Fatal("invalid unselected fixture unexpectedly rendered without a filter")
	}
	if _, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, output, []string{"dbo.Vehicle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "Vehicle", "Vehicle.go")); err != nil {
		t.Fatalf("selected model is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "Broken", "Broken.go")); !os.IsNotExist(err) {
		t.Fatalf("unselected invalid model was rendered: %v", err)
	}
}

func TestRenderUnknownSelectionStopsBeforeBackend(t *testing.T) {
	directory := t.TempDir()
	manifestFilename := filepath.Join(directory, "manifest.json")
	if err := manifest.SaveAtomic(manifestFilename, serviceManifest()); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{}
	service := NewWithDependencies(Dependencies{
		Backends: map[string]dialect.Backend{dialect.SQLServer: backend},
	})
	if _, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, "models", []string{"Missing"}); err == nil {
		t.Fatal("expected unknown table selection error")
	}
	if backend.renderCalls != 0 {
		t.Fatalf("backend rendered an invalid selection: %#v", backend)
	}
}

func TestCommittedRenderErrorsBecomeWarnings(t *testing.T) {
	t.Run("render", func(t *testing.T) {
		directory := t.TempDir()
		manifestFilename := filepath.Join(directory, "manifest.json")
		if err := manifest.SaveAtomic(manifestFilename, serviceManifest()); err != nil {
			t.Fatal(err)
		}
		backend := &fakeBackend{renderErr: committedOutputError{}}
		service := NewWithDependencies(Dependencies{
			Backends: map[string]dialect.Backend{dialect.SQLServer: backend},
		})

		result, err := service.Render(context.Background(), dialect.SQLServer, manifestFilename, "models", nil)
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "render manifest") {
			t.Fatalf("Render() result = %#v", result)
		}
	})

	t.Run("generate", func(t *testing.T) {
		directory := t.TempDir()
		backend := &fakeBackend{
			inspection: &dialect.Inspection{DatabaseName: "MainDB", Manifest: serviceManifest()},
			renderErr:  committedOutputError{},
		}
		service := NewWithDependencies(Dependencies{
			WorkingDirectory: directory,
			Backends:         map[string]dialect.Backend{dialect.SQLServer: backend},
		})

		result, err := service.Generate(context.Background(), dialect.SQLServer, "dsn", "models", nil)
		if err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "render generated manifest") {
			t.Fatalf("Generate() result = %#v", result)
		}
	})
}

func TestResultStatisticsCountManifestRenames(t *testing.T) {
	m := serviceManifest()
	m.Tables[0].GoName = "VehicleModel"
	m.Tables[0].Columns[0].GoName = "ID"
	m.Tables[0].Columns = append(m.Tables[0].Columns,
		manifest.Column{ColumnName: "Description", GoName: "Description"},
		manifest.Column{ColumnName: "CREATED_AT"},
	)
	addServiceTable(m, "audit", "VehicleLog", "VIEW")
	for index := range m.Tables {
		if m.Tables[index].TableName == "VehicleLog" {
			m.Tables[index].GoName = "VehicleLog"
		}
	}

	result := resultForManifest("manifest.json", m)
	want := Statistics{Tables: 2, ManagedTables: 2, RenamedTables: 1, Columns: 6, RenamedColumns: 2}
	if result.ManifestFilename != "manifest.json" || result.Statistics != want {
		t.Fatalf("resultForManifest() = %#v, want filename and %#v", result, want)
	}
}

func TestResultWarnsAboutUnexportedGeneratedNames(t *testing.T) {
	m := manifest.New("MainDB", []manifest.Table{{
		TableCatalog: "MainDB",
		TableSchema:  "public",
		TableName:    "vehicle",
		TableType:    "BASE TABLE",
		Columns: []manifest.Column{{
			ColumnName:      "description",
			OrdinalPosition: 1,
			DataType:        manifest.DataTypeString,
		}},
	}})

	result := resultForManifest("", m)
	if len(result.Warnings) != 2 {
		t.Fatalf("warnings = %#v, want table and column warnings", result.Warnings)
	}
	if !strings.Contains(result.Warnings[0], "effective GoName \"vehicle\" is not exported") ||
		!strings.Contains(result.Warnings[1], "effective GoName \"description\" is not exported") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func serviceManifest() *manifest.Manifest {
	return manifest.New("MainDB", []manifest.Table{{
		TableCatalog: "MainDB",
		TableSchema:  "dbo",
		TableName:    "Vehicle",
		TableType:    "BASE TABLE",
		Columns: []manifest.Column{{
			ColumnName:        "FW_ID",
			OrdinalPosition:   1,
			DataType:          "int",
			IsPrimaryKey:      true,
			PrimaryKeyOrdinal: 1,
		}},
	}})
}

func addServiceTable(m *manifest.Manifest, schema, name, tableType string) {
	table := m.Tables[0]
	table.TableSchema = schema
	table.TableName = name
	table.TableType = tableType
	table.Columns = append([]manifest.Column(nil), table.Columns...)
	m.Tables = append(m.Tables, table)
	manifest.Canonicalize(m)
}
