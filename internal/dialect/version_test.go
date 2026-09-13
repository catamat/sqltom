package dialect

import (
	"reflect"
	"strings"
	"testing"
)

func TestNamesIsCompleteStableAndIndependent(t *testing.T) {
	want := []string{Postgres, MySQL, SQLite, SQLServer}
	got := Names()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %#v, want %#v", got, want)
	}
	got[0] = "changed"
	if again := Names(); !reflect.DeepEqual(again, want) {
		t.Fatalf("Names() exposed mutable state: %#v", again)
	}
	for _, name := range want {
		if !IsKnown("  " + strings.ToUpper(name) + "  ") {
			t.Errorf("IsKnown() rejected dialect %q from Names()", name)
		}
	}
}

func TestRequireMinimumVersion(t *testing.T) {
	tests := []struct {
		name    string
		current string
		minimum Version
		wantErr string
	}{
		{name: "exact", current: "12.0", minimum: Version{Major: 12}},
		{name: "newer minor", current: "8.4.6", minimum: Version{Major: 8}},
		{name: "vendor suffix", current: "8.0.36-0ubuntu0.22.04.1", minimum: Version{Major: 8}},
		{name: "surrounding whitespace", current: " 16.0.1000.6 ", minimum: Version{Major: 13}},
		{name: "too old", current: "11.22", minimum: Version{Major: 12}, wantErr: "minimum is 12.0"},
		{name: "old minor", current: "3.36.0", minimum: Version{Major: 3, Minor: 37}, wantErr: "minimum is 3.37"},
		{name: "missing minor", current: "16", minimum: Version{Major: 13}, wantErr: "expected a leading major.minor"},
		{name: "invalid", current: "development", minimum: Version{Major: 1}, wantErr: "expected a leading major.minor"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := RequireMinimumVersion("database", test.current, test.minimum)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("RequireMinimumVersion() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("RequireMinimumVersion() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}
