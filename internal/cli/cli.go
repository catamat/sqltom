package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/catamat/sqltom/internal/dialect"
)

const (
	ExitSuccess     = 0
	ExitOperational = 1
	ExitUsage       = 2
)

var Version = "devel"

type Statistics struct {
	Tables         int
	ManagedTables  int
	RenamedTables  int
	Columns        int
	RenamedColumns int
}

type OperationResult struct {
	ManifestFilename string
	Statistics       Statistics
	Warnings         []string
}

type Service interface {
	Inspect(ctx context.Context, dialect, dsn string, tables []string) (OperationResult, error)
	Render(ctx context.Context, dialect, manifestFilename, outputFolder string, tables []string) (OperationResult, error)
	Generate(ctx context.Context, dialect, dsn, outputFolder string, tables []string) (OperationResult, error)
}

type options struct {
	inspect  bool
	render   bool
	generate bool
	version  bool
	manifest string
	dialect  string
	dsn      string
	output   string
	tables   []string
}

type tableListValue struct {
	value string
	set   bool
}

func (v *tableListValue) String() string {
	if v == nil {
		return ""
	}
	return v.value
}

func (v *tableListValue) Set(value string) error {
	if v.set {
		return fmt.Errorf("may be specified only once")
	}
	v.value = value
	v.set = true
	return nil
}

func Main(ctx context.Context, args []string, stdout, stderr io.Writer, service Service) int {
	configuration, err := parse(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return ExitSuccess
	}
	if err != nil {
		printError(stderr, "", err)
		return ExitUsage
	}
	if configuration.version {
		fmt.Fprintf(stdout, "sqltom %s\n", detectedVersion())
		return ExitSuccess
	}
	if service == nil {
		printError(stderr, "", errors.New("service is not configured"))
		return ExitOperational
	}

	switch {
	case configuration.inspect:
		result, err := service.Inspect(ctx, configuration.dialect, configuration.dsn, configuration.tables)
		if err != nil {
			printError(stderr, "inspect", err, configuration.dsn)
			return ExitOperational
		}
		printWarnings(stderr, result.Warnings)
		fmt.Fprintf(stdout, "manifest: %s\n", result.ManifestFilename)
		printStatistics(stdout, result.Statistics)
	case configuration.render:
		result, err := service.Render(ctx, configuration.dialect, configuration.manifest, configuration.output, configuration.tables)
		if err != nil {
			printError(stderr, "render", err)
			return ExitOperational
		}
		printWarnings(stderr, result.Warnings)
		fmt.Fprintf(stdout, "output: %s\n", configuration.output)
		printStatistics(stdout, result.Statistics)
	case configuration.generate:
		result, err := service.Generate(ctx, configuration.dialect, configuration.dsn, configuration.output, configuration.tables)
		if err != nil {
			printError(stderr, "generate", err, configuration.dsn)
			return ExitOperational
		}
		printWarnings(stderr, result.Warnings)
		fmt.Fprintf(stdout, "manifest: %s\n", result.ManifestFilename)
		fmt.Fprintf(stdout, "output: %s\n", configuration.output)
		printStatistics(stdout, result.Statistics)
	}
	return ExitSuccess
}

func detectedVersion() string {
	if Version != "" && Version != "devel" {
		return Version
	}
	build, ok := debug.ReadBuildInfo()
	if !ok || build.Main.Version == "" || build.Main.Version == "(devel)" {
		return "devel"
	}
	return build.Main.Version
}

func printError(output io.Writer, operation string, err error, sensitiveValues ...string) {
	message := redactSensitive(err.Error(), sensitiveValues...)
	if operation != "" {
		message = operation + ": " + message
	}
	printDiagnostic(output, "error", message)
}

func redactSensitive(message string, values ...string) string {
	for _, value := range values {
		if value != "" {
			message = strings.ReplaceAll(message, value, "<redacted>")
		}
		trimmed := strings.TrimSpace(value)
		if trimmed != "" && trimmed != value {
			message = strings.ReplaceAll(message, trimmed, "<redacted>")
		}
	}
	return message
}

func printWarnings(output io.Writer, warnings []string) {
	for _, warning := range warnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			printDiagnostic(output, "warning", warning)
		}
	}
}

func printDiagnostic(output io.Writer, severity, message string) {
	message = strings.TrimRight(message, "\r\n")
	if message == "" {
		fmt.Fprintf(output, "%s:\n", severity)
		return
	}
	for _, line := range strings.Split(message, "\n") {
		fmt.Fprintf(output, "%s: %s\n", severity, strings.TrimSuffix(line, "\r"))
	}
}

func printStatistics(output io.Writer, statistics Statistics) {
	fmt.Fprintf(output, "tables: %d/%d\n", statistics.ManagedTables, statistics.Tables)
	fmt.Fprintf(output, "renamed tables: %d/%d\n", statistics.RenamedTables, statistics.Tables)
	fmt.Fprintf(output, "columns: %d\n", statistics.Columns)
	fmt.Fprintf(output, "renamed columns: %d\n", statistics.RenamedColumns)
}

func parse(args []string, stderr io.Writer) (options, error) {
	var configuration options
	var tableList tableListValue
	var flagOutput bytes.Buffer
	flags := flag.NewFlagSet("sqltom", flag.ContinueOnError)
	flags.SetOutput(&flagOutput)
	flags.BoolVar(&configuration.inspect, "inspect", false, "inspect a database and save its manifest")
	flags.BoolVar(&configuration.render, "render", false, "validate and render an explicit manifest")
	flags.BoolVar(&configuration.generate, "generate", false, "inspect, save the manifest, and render it")
	flags.BoolVar(&configuration.version, "version", false, "print the sqltom version")
	flags.StringVar(&configuration.manifest, "manifest", "", "manifest file used by -render")
	flags.StringVar(&configuration.dialect, "dialect", "", "database dialect: "+strings.Join(dialect.Names(), ", "))
	flags.StringVar(&configuration.dsn, "dsn", "", "database driver connection string")
	flags.StringVar(&configuration.output, "output", "", "model output directory")
	flags.Var(&tableList, "tables", "optional comma-separated exact table/view selectors: name, schema.name, or catalog.schema.name")
	flags.Usage = func() {
		fmt.Fprintln(&flagOutput, "Usage:")
		fmt.Fprintln(&flagOutput, `  sqltom -inspect  -dialect="sqlserver" -dsn="<dsn>" -tables="dbo.Vehicle,dbo.Customer"`)
		fmt.Fprintln(&flagOutput, `  sqltom -render   -dialect="sqlserver" -manifest="<file>" -output="<dir>" -tables="dbo.Vehicle,dbo.Customer"`)
		fmt.Fprintln(&flagOutput, `  sqltom -generate -dialect="sqlserver" -dsn="<dsn>" -output="<dir>" -tables="dbo.Vehicle,dbo.Customer"`)
		fmt.Fprintln(&flagOutput, "  sqltom -version")
		fmt.Fprintln(&flagOutput)
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.Copy(stderr, &flagOutput)
		}
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	tables, err := parseTables(tableList.value, tableList.set)
	if err != nil {
		return options{}, err
	}
	configuration.tables = tables
	modeCount := 0
	for _, enabled := range []bool{configuration.inspect, configuration.render, configuration.generate, configuration.version} {
		if enabled {
			modeCount++
		}
	}
	if modeCount != 1 {
		return options{}, fmt.Errorf("exactly one of -inspect, -render, -generate, or -version is required")
	}

	switch {
	case configuration.version:
		for _, flagValue := range []struct {
			name  string
			value string
		}{
			{name: "-manifest", value: configuration.manifest},
			{name: "-dialect", value: configuration.dialect},
			{name: "-dsn", value: configuration.dsn},
			{name: "-output", value: configuration.output},
		} {
			if err := forbid(flagValue.name, flagValue.value, "-version"); err != nil {
				return options{}, err
			}
		}
		if tableList.set {
			return options{}, fmt.Errorf("-tables cannot be used with -version")
		}
	case configuration.inspect:
		if err := validateDialect(configuration.dialect); err != nil {
			return options{}, err
		}
		if err := require("-dsn", configuration.dsn); err != nil {
			return options{}, err
		}
		if err := forbid("-manifest", configuration.manifest, "-inspect"); err != nil {
			return options{}, err
		}
		if err := forbid("-output", configuration.output, "-inspect"); err != nil {
			return options{}, err
		}
	case configuration.render:
		if err := validateDialect(configuration.dialect); err != nil {
			return options{}, err
		}
		if err := require("-manifest", configuration.manifest); err != nil {
			return options{}, err
		}
		if err := require("-output", configuration.output); err != nil {
			return options{}, err
		}
		if err := forbid("-dsn", configuration.dsn, "-render"); err != nil {
			return options{}, err
		}
	case configuration.generate:
		if err := validateDialect(configuration.dialect); err != nil {
			return options{}, err
		}
		if err := require("-dsn", configuration.dsn); err != nil {
			return options{}, err
		}
		if err := require("-output", configuration.output); err != nil {
			return options{}, err
		}
		if err := forbid("-manifest", configuration.manifest, "-generate"); err != nil {
			return options{}, err
		}
	}
	return configuration, nil
}

func parseTables(value string, set bool) ([]string, error) {
	if !set {
		return nil, nil
	}
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("-tables requires at least one table or view selector")
	}

	reader := csv.NewReader(strings.NewReader(trimSpaceAfterCSVQuotes(value)))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid -tables value: %w", err)
	}
	if len(records) != 1 {
		return nil, fmt.Errorf("-tables must contain one comma-separated list")
	}

	tables := make([]string, 0, len(records[0]))
	seen := make(map[string]struct{}, len(records[0]))
	for index, raw := range records[0] {
		table := strings.TrimSpace(raw)
		if table == "" {
			return nil, fmt.Errorf("-tables contains an empty selector at position %d", index+1)
		}
		if _, exists := seen[table]; exists {
			return nil, fmt.Errorf("-tables contains duplicate selector %q", table)
		}
		seen[table] = struct{}{}
		tables = append(tables, table)
	}
	return tables, nil
}

func trimSpaceAfterCSVQuotes(value string) string {
	runes := []rune(value)
	var result strings.Builder
	result.Grow(len(value))
	inQuotes := false
	for index := 0; index < len(runes); index++ {
		current := runes[index]
		result.WriteRune(current)
		if current != '"' {
			continue
		}
		if inQuotes && index+1 < len(runes) && runes[index+1] == '"' {
			result.WriteRune(runes[index+1])
			index++
			continue
		}
		inQuotes = !inQuotes
		if inQuotes {
			continue
		}

		next := index + 1
		for next < len(runes) && (runes[next] == ' ' || runes[next] == '\t') {
			next++
		}
		if next == len(runes) || runes[next] == ',' {
			index = next - 1
		}
	}
	return result.String()
}

func validateDialect(value string) error {
	if err := require("-dialect", value); err != nil {
		return err
	}
	if !dialect.IsKnown(value) {
		return fmt.Errorf("unknown -dialect %q", value)
	}
	return nil
}

func require(flagName, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", flagName)
	}
	return nil
}

func forbid(flagName, value, mode string) error {
	if value != "" {
		return fmt.Errorf("%s cannot be used with %s", flagName, mode)
	}
	return nil
}
