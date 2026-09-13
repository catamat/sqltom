package sqlite

import (
	"strings"
	"testing"
)

func TestSQLIdentifierSourceEscapesDoubleQuoteAndBacktick(t *testing.T) {
	if got, want := sqlIdentifierSource(`a"b`+"`"+`c`), "` + \"\\\"a\\\"\\\"b`c\\\"\" + `"; got != want {
		t.Fatalf("sqlIdentifierSource() = %q, want %q", got, want)
	}
	if got := sqlIdentifierSource("a\r\x00b"); strings.ContainsAny(got, "\r\x00") {
		t.Fatalf("sqlIdentifierSource() copied a control character into Go source: %q", got)
	}
}

func TestSQLTableIdentifierSourceUsesAvailableIdentity(t *testing.T) {
	tests := map[string]struct {
		catalog string
		schema  string
		name    string
		want    string
	}{
		"catalog ignored": {catalog: "MainDB", schema: "main", name: "Vehicle", want: "` + \"\\\"main\\\".\\\"Vehicle\\\"\" + `"},
		"schema only":     {schema: "audit", name: "Vehicle", want: "` + \"\\\"audit\\\".\\\"Vehicle\\\"\" + `"},
		"name only":       {name: "Vehicle", want: "` + \"\\\"Vehicle\\\"\" + `"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := sqlTableIdentifierSource(test.catalog, test.schema, test.name); got != test.want {
				t.Fatalf("sqlTableIdentifierSource() = %q, want %q", got, test.want)
			}
		})
	}
}
