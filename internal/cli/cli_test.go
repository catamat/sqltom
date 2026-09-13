package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect"
)

type fakeService struct {
	inspectCalls  int
	renderCalls   int
	generateCalls int
	dialect       string
	dsn           string
	manifest      string
	output        string
	tables        []string
	result        OperationResult
	err           error
}

func (f *fakeService) Inspect(_ context.Context, dialect, dsn string, tables []string) (OperationResult, error) {
	f.inspectCalls++
	f.dialect, f.dsn = dialect, dsn
	f.tables = append([]string(nil), tables...)
	result := f.result
	if result.ManifestFilename == "" {
		result.ManifestFilename = "sqltom_DB.json"
	}
	return result, f.err
}

func (f *fakeService) Render(_ context.Context, dialect, manifestFilename, outputFolder string, tables []string) (OperationResult, error) {
	f.renderCalls++
	f.dialect, f.manifest, f.output = dialect, manifestFilename, outputFolder
	f.tables = append([]string(nil), tables...)
	return f.result, f.err
}

func (f *fakeService) Generate(_ context.Context, dialect, dsn, outputFolder string, tables []string) (OperationResult, error) {
	f.generateCalls++
	f.dialect, f.dsn, f.output = dialect, dsn, outputFolder
	f.tables = append([]string(nil), tables...)
	result := f.result
	if result.ManifestFilename == "" {
		result.ManifestFilename = "sqltom_DB.json"
	}
	return result, f.err
}

func TestUsageValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no mode"},
		{name: "multiple modes", args: []string{"-inspect", "-render"}},
		{name: "inspect missing DSN", args: []string{"-inspect", "-dialect", "sqlserver"}},
		{name: "inspect rejects output", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-output", "models"}},
		{name: "render missing dialect", args: []string{"-render", "-manifest", "m.json", "-output", "models"}},
		{name: "render missing output", args: []string{"-render", "-dialect", "sqlserver", "-manifest", "m.json"}},
		{name: "render rejects DSN", args: []string{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models", "-dsn", "x"}},
		{name: "generate rejects manifest", args: []string{"-generate", "-dialect", "sqlserver", "-dsn", "x", "-output", "models", "-manifest", "m.json"}},
		{name: "unknown dialect", args: []string{"-inspect", "-dialect", "oracle", "-dsn", "x"}},
		{name: "render unknown dialect", args: []string{"-render", "-dialect", "oracle", "-manifest", "m.json", "-output", "models"}},
		{name: "positional", args: []string{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models", "extra"}},
		{name: "tables empty member", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", "Vehicle,,Customer"}},
		{name: "tables trailing comma", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", "Vehicle,"}},
		{name: "tables whitespace member", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", "Vehicle,   "}},
		{name: "tables duplicate", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", "Vehicle, Vehicle"}},
		{name: "tables malformed CSV", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", `"Vehicle`}},
		{name: "tables multiple CSV records", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", "Vehicle\nCustomer"}},
		{name: "tables explicitly empty", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", ""}},
		{name: "tables repeated flag", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-tables", "Vehicle", "-tables", "Customer"}},
		{name: "unknown flag", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", "x", "-unknown"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{}
			var stdout, stderr bytes.Buffer
			if code := Main(context.Background(), test.args, &stdout, &stderr, service); code != ExitUsage {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			if service.inspectCalls+service.renderCalls+service.generateCalls != 0 {
				t.Fatal("service called for invalid CLI")
			}
			if !strings.HasPrefix(stderr.String(), "error: ") || strings.Contains(stderr.String(), "sqltom:") {
				t.Fatalf("unclassified usage error: %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "Usage:") {
				t.Fatalf("usage error included unclassified help text: %q", stderr.String())
			}
		})
	}
}

func TestModesDelegateExactValues(t *testing.T) {
	secret := " sqlserver://user:secret@host/db "
	tableSelectors := []string{"dbo.Vehicle", "audit.Log"}
	tests := []struct {
		name  string
		args  []string
		check func(*testing.T, *fakeService)
	}{
		{
			name: "inspect",
			args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", secret, "-tables", "dbo.Vehicle, audit.Log"},
			check: func(t *testing.T, service *fakeService) {
				if service.inspectCalls != 1 || service.dsn != secret || !reflect.DeepEqual(service.tables, tableSelectors) {
					t.Fatalf("inspect delegation = %#v", service)
				}
			},
		},
		{
			name: "render",
			args: []string{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models", "-tables", "dbo.Vehicle, audit.Log"},
			check: func(t *testing.T, service *fakeService) {
				if service.renderCalls != 1 || service.dialect != "sqlserver" || service.manifest != "m.json" || service.output != "models" || !reflect.DeepEqual(service.tables, tableSelectors) {
					t.Fatalf("render delegation = %#v", service)
				}
			},
		},
		{
			name: "generate",
			args: []string{"-generate", "-dialect", "sqlserver", "-dsn", secret, "-output", "models", "-tables", "dbo.Vehicle, audit.Log"},
			check: func(t *testing.T, service *fakeService) {
				if service.generateCalls != 1 || service.dsn != secret || service.output != "models" || !reflect.DeepEqual(service.tables, tableSelectors) {
					t.Fatalf("generate delegation = %#v", service)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{}
			var stdout, stderr bytes.Buffer
			if code := Main(context.Background(), test.args, &stdout, &stderr, service); code != ExitSuccess {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			test.check(t, service)
			if strings.Contains(stdout.String()+stderr.String(), secret) {
				t.Fatal("DSN leaked to command output")
			}
		})
	}
}

func TestSuccessfulModesPrintStatistics(t *testing.T) {
	tests := [][]string{
		{"-inspect", "-dialect", "sqlserver", "-dsn", "dsn"},
		{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models"},
		{"-generate", "-dialect", "sqlserver", "-dsn", "dsn", "-output", "models"},
	}
	for _, args := range tests {
		service := &fakeService{result: OperationResult{
			ManifestFilename: "sqltom_DB.json",
			Statistics:       Statistics{Tables: 4, ManagedTables: 3, RenamedTables: 2, Columns: 27, RenamedColumns: 3},
		}}
		var stdout, stderr bytes.Buffer
		if code := Main(context.Background(), args, &stdout, &stderr, service); code != ExitSuccess {
			t.Fatalf("args %q: exit code = %d, stderr = %q", args, code, stderr.String())
		}
		for _, line := range []string{
			"tables: 3/4\n",
			"renamed tables: 2/4\n",
			"columns: 27\n",
			"renamed columns: 3\n",
		} {
			if !strings.Contains(stdout.String(), line) {
				t.Fatalf("args %q: stdout %q is missing %q", args, stdout.String(), line)
			}
		}
		if stderr.Len() != 0 {
			t.Fatalf("args %q: stderr = %q", args, stderr.String())
		}
	}
}

func TestNonBlockingWarningsPreserveSuccessfulResult(t *testing.T) {
	tests := [][]string{
		{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models"},
		{"-generate", "-dialect", "sqlserver", "-dsn", "dsn", "-output", "models"},
	}
	for _, args := range tests {
		service := &fakeService{result: OperationResult{
			ManifestFilename: "sqltom_DB.json",
			Statistics:       Statistics{Tables: 1, ManagedTables: 1, Columns: 2},
			Warnings:         []string{"output committed; cleanup failed"},
		}}
		var stdout, stderr bytes.Buffer
		if code := Main(context.Background(), args, &stdout, &stderr, service); code != ExitSuccess {
			t.Fatalf("args %q: exit code = %d, stderr = %q", args, code, stderr.String())
		}
		if stderr.String() != "warning: output committed; cleanup failed\n" {
			t.Fatalf("args %q: stderr = %q", args, stderr.String())
		}
		if !strings.Contains(stdout.String(), "tables: 1/1\n") || !strings.Contains(stdout.String(), "columns: 2\n") {
			t.Fatalf("args %q: successful result is incomplete: %q", args, stdout.String())
		}
	}
}

func TestTablesFlagUsesCSVQuotingAndPreservesDatabaseNames(t *testing.T) {
	service := &fakeService{}
	var stdout, stderr bytes.Buffer
	args := []string{
		"-inspect",
		"-dialect", "sqlserver",
		"-dsn", "dsn",
		"-tables", `dbo.Vehicle, "audit.Order,Archive" , Order Items`,
	}
	if code := Main(context.Background(), args, &stdout, &stderr, service); code != ExitSuccess {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	want := []string{"dbo.Vehicle", "audit.Order,Archive", "Order Items"}
	if !reflect.DeepEqual(service.tables, want) {
		t.Fatalf("tables = %#v, want %#v", service.tables, want)
	}
}

func TestOmittedTablesDelegatesNoFilter(t *testing.T) {
	tests := [][]string{
		{"-inspect", "-dialect", "sqlserver", "-dsn", "dsn"},
		{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models"},
		{"-generate", "-dialect", "sqlserver", "-dsn", "dsn", "-output", "models"},
	}
	for _, args := range tests {
		service := &fakeService{}
		var stdout, stderr bytes.Buffer
		if code := Main(context.Background(), args, &stdout, &stderr, service); code != ExitSuccess {
			t.Fatalf("args %q: exit code = %d, stderr = %q", args, code, stderr.String())
		}
		if service.tables != nil {
			t.Fatalf("args %q: tables = %#v, want nil", args, service.tables)
		}
	}
}

func TestHelpAndOperationalErrorExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Main(context.Background(), []string{"-h"}, &stdout, &stderr, &fakeService{}); code != ExitSuccess {
		t.Fatalf("help exit code = %d", code)
	}
	if strings.Contains(stderr.String(), "error:") || strings.Contains(stderr.String(), "warning:") || strings.Contains(stderr.String(), "sqltom:") {
		t.Fatalf("help contains a diagnostic prefix: %q", stderr.String())
	}
	for _, example := range []string{
		`sqltom -inspect  -dialect="sqlserver" -dsn="<dsn>" -tables="dbo.Vehicle,dbo.Customer"`,
		`sqltom -render   -dialect="sqlserver" -manifest="<file>" -output="<dir>" -tables="dbo.Vehicle,dbo.Customer"`,
		`sqltom -generate -dialect="sqlserver" -dsn="<dsn>" -output="<dir>" -tables="dbo.Vehicle,dbo.Customer"`,
		`sqltom -version`,
	} {
		if !strings.Contains(stderr.String(), example) {
			t.Fatalf("help is missing example %q: %q", example, stderr.String())
		}
	}
	if !strings.Contains(stderr.String(), "-tables") || !strings.Contains(stderr.String(), "schema.name") {
		t.Fatalf("help does not describe -tables selectors: %q", stderr.String())
	}
	if want := "database dialect: " + strings.Join(dialect.Names(), ", "); !strings.Contains(stderr.String(), want) {
		t.Fatalf("help does not use the central dialect list %q: %q", want, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	service := &fakeService{err: errors.New("database unavailable")}
	code := Main(context.Background(), []string{"-inspect", "-dialect", "sqlserver", "-dsn", "secret"}, &stdout, &stderr, service)
	if code != ExitOperational || stderr.String() != "error: inspect: database unavailable\n" {
		t.Fatalf("operational result = %d, %q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatal("DSN leaked in error output")
	}
	if stdout.Len() != 0 {
		t.Fatalf("failed operation wrote success output: %q", stdout.String())
	}
}

func TestVersionIsAStandaloneMode(t *testing.T) {
	previous := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = previous })

	var stdout, stderr bytes.Buffer
	if code := Main(context.Background(), []string{"-version"}, &stdout, &stderr, nil); code != ExitSuccess {
		t.Fatalf("version exit code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != "sqltom v1.2.3\n" || stderr.Len() != 0 {
		t.Fatalf("version output = (%q, %q)", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Main(context.Background(), []string{"-version", "-dsn", "unused"}, &stdout, &stderr, nil); code != ExitUsage {
		t.Fatalf("version with flags exit code = %d", code)
	}
	if !strings.Contains(stderr.String(), "-dsn cannot be used with -version") || stdout.Len() != 0 {
		t.Fatalf("version with flags output = (%q, %q)", stdout.String(), stderr.String())
	}
}

func TestInspectAndGenerateErrorsRedactTheSuppliedDSN(t *testing.T) {
	dsn := " sqlserver://user:TOPSECRET@host/db "
	trimmedDSN := strings.TrimSpace(dsn)
	tests := []struct {
		name string
		args []string
	}{
		{name: "inspect", args: []string{"-inspect", "-dialect", "sqlserver", "-dsn", dsn}},
		{name: "generate", args: []string{"-generate", "-dialect", "sqlserver", "-dsn", dsn, "-output", "models"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeService{err: errors.New("driver echoed " + dsn + " and normalized " + trimmedDSN)}
			var stdout, stderr bytes.Buffer
			if code := Main(context.Background(), test.args, &stdout, &stderr, service); code != ExitOperational {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			if strings.Contains(stderr.String(), dsn) || strings.Contains(stderr.String(), trimmedDSN) || strings.Contains(stderr.String(), "TOPSECRET") {
				t.Fatalf("DSN leaked in error output: %q", stderr.String())
			}
			if !strings.Contains(stderr.String(), "<redacted>") {
				t.Fatalf("error does not report redaction: %q", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed operation wrote success output: %q", stdout.String())
			}
		})
	}
}

func TestEveryOperationalModeClassifiesBlockingErrors(t *testing.T) {
	tests := [][]string{
		{"-inspect", "-dialect", "sqlserver", "-dsn", "dsn"},
		{"-render", "-dialect", "sqlserver", "-manifest", "m.json", "-output", "models"},
		{"-generate", "-dialect", "sqlserver", "-dsn", "dsn", "-output", "models"},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		code := Main(context.Background(), args, &stdout, &stderr, &fakeService{err: errors.New("blocked")})
		if code != ExitOperational || !strings.HasPrefix(stderr.String(), "error: ") {
			t.Fatalf("args %q: exit code = %d, stderr = %q", args, code, stderr.String())
		}
		if strings.Contains(stderr.String(), "warning:") || strings.Contains(stderr.String(), "sqltom:") {
			t.Fatalf("args %q: blocking error was misclassified: %q", args, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Fatalf("args %q: blocking error wrote success output: %q", args, stdout.String())
		}
	}
}

func TestEveryDiagnosticLineIsClassified(t *testing.T) {
	var output bytes.Buffer
	printError(&output, "render", errors.Join(errors.New("replace failed"), errors.New("rollback failed")))
	if want := "error: render: replace failed\nerror: rollback failed\n"; output.String() != want {
		t.Fatalf("multiline error = %q, want %q", output.String(), want)
	}

	output.Reset()
	printWarnings(&output, []string{"cleanup failed\nstale backup remains"})
	if want := "warning: cleanup failed\nwarning: stale backup remains\n"; output.String() != want {
		t.Fatalf("multiline warning = %q, want %q", output.String(), want)
	}
}

func TestMissingServiceIsClassifiedAsBlockingError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Main(
		context.Background(),
		[]string{"-inspect", "-dialect", "sqlserver", "-dsn", "dsn"},
		&stdout,
		&stderr,
		nil,
	)
	if code != ExitOperational || stderr.String() != "error: service is not configured\n" {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("missing service wrote success output: %q", stdout.String())
	}
}
