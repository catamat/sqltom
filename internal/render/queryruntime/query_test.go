package query

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

var dialects = []string{"sqlserver", "postgres", "mysql", "sqlite"}

func TestBuildAcrossDialects(t *testing.T) {
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			q := New().Write("WHERE (id IN (:ids) OR owner = :owner)").Write("AND other IN (:ids) AND label = :label AND backup = :owner")
			q.Bind("label", "O'Brien :not_a_param").Bind("owner", 7).Bind("ids", []int{2, 3}) // Deliberately not SQL order.
			wantSQL := "WHERE (id IN (?, ?) OR owner = ?)\nAND other IN (?, ?) AND label = ? AND backup = ?"
			wantArgs := []any{2, 3, 7, 2, 3, "O'Brien :not_a_param", 7}
			if dialect == "postgres" || dialect == "sqlserver" {
				prefix := "$"
				if dialect == "sqlserver" {
					prefix = "@p"
				}
				wantSQL = fmt.Sprintf("WHERE (id IN (%[1]s1, %[1]s2) OR owner = %[1]s3)\nAND other IN (%[1]s1, %[1]s2) AND label = %[1]s4 AND backup = %[1]s3", prefix)
				wantArgs = []any{2, 3, 7, "O'Brien :not_a_param"}
			}
			sql, args, err := q.build(dialect)
			if err != nil || sql != wantSQL || !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("Build=%q, %#v, %v", sql, args, err)
			}
			args[0] = 99
			again, fresh, err := q.build(dialect)
			if err != nil || again != sql || !reflect.DeepEqual(fresh, wantArgs) {
				t.Fatal("Build result mutated the builder")
			}
		})
	}
}

func TestBuildPreservesScalarsWithoutCallingValuer(t *testing.T) {
	calls := 0
	codec := &countingValuer{calls: &calls}
	var typedNil *countingValuer
	bytes := []byte{0, 1, 255}
	type binary []byte
	array := [3]int{1, 2, 3}
	ids := []int{4, 5}
	clock := time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC)
	for _, dialect := range dialects {
		q := New().Write(":nil, :typedNil, :codec, :bytes, :binary, :array, :ids, :empty, :clock, :pointer, :json")
		q.Bind("nil", nil).Bind("typedNil", typedNil).Bind("codec", codec).Bind("bytes", bytes).Bind("binary", binary(bytes)).Bind("array", array).
			Bind("ids", Scalar(Scalar(ids))).Bind("empty", Scalar([]string{})).Bind("clock", clock).Bind("pointer", &ids).Bind("json", valueSlice{1, 2})
		_, args, err := q.build(dialect)
		want := []any{nil, typedNil, codec, bytes, binary(bytes), array, ids, []string{}, clock, &ids, valueSlice{1, 2}}
		if err != nil || !reflect.DeepEqual(args, want) || calls != 0 {
			t.Fatalf("%s: %#v, %v, calls=%d", dialect, args, err, calls)
		}
		if _, err := q.preview(dialect); err != nil || calls != 0 {
			t.Fatal("Preview evaluated Valuer")
		}
	}
}

type countingValuer struct{ calls *int }

func (v *countingValuer) Value() (driver.Value, error) { *v.calls++; return "codec", nil }

type valueSlice []int

func (valueSlice) Value() (driver.Value, error) { panic("must not evaluate codec") }

func TestListsAreExpandedAtBuildTime(t *testing.T) {
	ids := []int{1, 2}
	q := New().Write("IN (:ids)").Bind("ids", ids)
	ids[0] = 3
	for _, dialect := range dialects {
		_, args, err := q.build(dialect)
		if err != nil || !reflect.DeepEqual(args, []any{3, 2}) {
			t.Fatalf("Build=%#v, %v", args, err)
		}
	}
	q = New().Write("IN (:ids)").Bind("ids", []any{nil, Scalar([]int{7}), []byte{8}, valueSlice{9}})
	if _, args, err := q.Build(); err != nil || len(args) != 4 {
		t.Fatalf("mixed list=%#v, %v", args, err)
	}
}

func TestRepeatedBuildReadsCurrentListValues(t *testing.T) {
	type recordID int64
	type recordIDs []int
	for _, test := range []struct {
		name        string
		list        any
		first, next any
	}{
		{"int", []int{1_000_000}, 1_000_000, 2_000_000},
		{"int64", []int64{1_000_000}, int64(1_000_000), int64(2_000_000)},
		{"string", []string{"first"}, "first", "next"},
		{"any", []any{"first"}, "first", "next"},
		{"named element", []recordID{1_000_000}, recordID(1_000_000), recordID(2_000_000)},
		{"named slice", recordIDs{1_000_000}, 1_000_000, 2_000_000},
	} {
		for _, dialect := range dialects {
			t.Run(dialect+"/"+test.name, func(t *testing.T) {
				list := reflect.ValueOf(test.list)
				list.Index(0).Set(reflect.ValueOf(test.first))
				q := New().Write("WHERE id IN (:ids) OR parent IN (:ids)").Bind("ids", test.list)
				sql, original, err := q.build(dialect)
				if err != nil || original[0] != test.first {
					t.Fatalf("first Build=%#v %v", original, err)
				}
				list.Index(0).Set(reflect.ValueOf(test.next))
				again, fresh, err := q.build(dialect)
				if err != nil || again != sql || original[0] != test.first {
					t.Fatalf("second Build changed SQL or previous result: %q %#v %v", again, original, err)
				}
				count := 1
				if dialect == "mysql" || dialect == "sqlite" {
					count = 2
				}
				if len(fresh) != count {
					t.Fatalf("args=%#v, want %d values", fresh, count)
				}
				for _, arg := range fresh {
					if arg != test.next {
						t.Fatalf("stale or incorrectly typed value: %#v", fresh)
					}
				}
				fresh[0] = "modified result"
				preview, err := q.preview(dialect)
				if err != nil || !strings.Contains(preview, fmt.Sprintf("(%T) %#v", test.next, test.next)) || strings.Contains(preview, "modified result") {
					t.Fatalf("Preview=%q %v", preview, err)
				}
			})
		}
	}
}

func TestBuildLongQueryAndRepeatedListNumbering(t *testing.T) {
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			q := New().Write(`SELECT \:`)
			want := "SELECT :"
			var wantArgs []any
			placeholder := func(index int) string {
				switch dialect {
				case "sqlserver":
					return fmt.Sprintf("@p%d", index)
				case "postgres":
					return fmt.Sprintf("$%d", index)
				default:
					return "?"
				}
			}
			// More than eight tokens, with indexes crossing both 9 and 99.
			for i := 1; i <= 12; i++ {
				name := fmt.Sprintf("value%d", i)
				q.Write(", :"+name).Bind(name, i)
				want += "\n, " + placeholder(i)
				wantArgs = append(wantArgs, i)
			}
			ids := make([]int64, 128)
			var placeholders []string
			for i := range ids {
				ids[i] = int64(1_000_000 + i)
				placeholders = append(placeholders, placeholder(13+i))
				wantArgs = append(wantArgs, ids[i])
			}
			q.Write("WHERE a IN (:ids) OR b IN (:ids)").Bind("ids", ids)
			list := strings.Join(placeholders, ", ")
			want += "\nWHERE a IN (" + list + ") OR b IN (" + list + ")"
			if dialect == "mysql" || dialect == "sqlite" {
				for _, id := range ids {
					wantArgs = append(wantArgs, id)
				}
			}
			q.Write("AND c = :last AND d = :value1").Bind("last", "last")
			want += "\nAND c = " + placeholder(141) + " AND d = " + placeholder(1)
			wantArgs = append(wantArgs, "last")
			if dialect == "mysql" || dialect == "sqlite" {
				wantArgs = append(wantArgs, 1)
			}
			sql, args, err := q.build(dialect)
			if err != nil || sql != want || !reflect.DeepEqual(args, wantArgs) {
				t.Fatalf("Build=%q, %#v, %v\nwant=%q, %#v", sql, args, err, want, wantArgs)
			}
		})
	}
}

func TestBuildErrorPrecedence(t *testing.T) {
	for _, dialect := range dialects {
		for _, test := range []struct {
			sql  string
			want error
		}{
			{":missing, :empty, 'unterminated", ErrSyntax},
			{":empty, :missing, 'unterminated", ErrSyntax},
			{":missing, :empty", ErrMissingBind},
			{":empty, :missing", ErrEmptyList},
		} {
			q := New().Write(test.sql).Bind("unused", 1).Bind("empty", []int{})
			if _, _, err := q.build(dialect); !errors.Is(err, test.want) {
				t.Fatalf("%s %q: %v, want %v", dialect, test.sql, err, test.want)
			}
		}
		q := New().Write(":values").Bind("values", []any{1, []int{2}})
		if _, _, err := q.build(dialect); !errors.Is(err, ErrInvalidList) {
			t.Fatalf("%s: nested list in []any: %v", dialect, err)
		}
		for _, statement := range []string{"", "SELECT ':not_a_bind'", `SELECT \:\:`} {
			q := New().Write(statement)
			sql, args, err := q.build(dialect)
			if err != nil || args == nil || len(args) != 0 || sql != strings.ReplaceAll(statement, `\:`, ":") {
				t.Fatalf("%s: parameter-free Build=%q %#v %v", dialect, sql, args, err)
			}
		}
	}
}

func TestBuildErrorsAreStrictAndDeterministic(t *testing.T) {
	tests := []struct {
		name  string
		build func() *Builder
		want  error
	}{
		{"duplicate nil", func() *Builder { return New().Bind("id", nil).Bind("id", 2) }, ErrDuplicateBind},
		{"missing", func() *Builder { return New().Write("x=:id") }, ErrMissingBind},
		{"unused", func() *Builder { return New().Write("x=:id").Bind("extra", 1).Bind("id", 2) }, ErrUnusedBind},
		{"case", func() *Builder { return New().Write(":ID").Bind("id", 1) }, ErrMissingBind},
		{"empty", func() *Builder { return New().Write(":ids").Bind("ids", []int{}) }, ErrEmptyList},
		{"nil list", func() *Builder { return New().Write(":ids").Bind("ids", []string(nil)) }, ErrEmptyList},
		{"nested list", func() *Builder { return New().Write(":ids").Bind("ids", [][]int{{1}}) }, ErrInvalidList},
	}
	for _, name := range []string{"", ":id", "id-name", "1id", "id.x", "nomeè", "id$"} {
		tests = append(tests, struct {
			name  string
			build func() *Builder
			want  error
		}{"name " + name, func() *Builder { return New().Bind(name, 1) }, ErrInvalidName})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, dialect := range dialects {
				q := test.build()
				sql, args, err := q.build(dialect)
				if sql != "" || args != nil || !errors.Is(err, test.want) {
					t.Fatalf("%s: %q, %#v, %v", dialect, sql, args, err)
				}
				preview, previewErr := q.preview(dialect)
				if preview != "" || !errors.Is(previewErr, test.want) || previewErr.Error() != err.Error() {
					t.Fatal("Preview differs from Build error")
				}
			}
		})
	}
	q := New().Bind("first", 1).Bind("second", 2)
	for i := 0; i < 10; i++ {
		if _, _, err := q.Build(); err == nil || !strings.Contains(err.Error(), `"first"`) {
			t.Fatal("unused binding error is nondeterministic")
		}
	}
	q = New().Write(":missing")
	if _, _, err := q.Build(); !errors.Is(err, ErrMissingBind) {
		t.Fatal(err)
	}
	q.Bind("missing", nil)
	if _, args, err := q.Build(); err != nil || !reflect.DeepEqual(args, []any{nil}) {
		t.Fatal("Build errors must not poison subsequent valid builds")
	}
	q = New().Bind("id", 1).Bind("id", 2).Bind("bad-name", 1).Write(":id")
	if _, _, err := q.Build(); !errors.Is(err, ErrDuplicateBind) {
		t.Fatal("first mutation error was lost")
	}
}

func TestWriteDoesNotInterpretClausesAndZeroValueWorks(t *testing.T) {
	var q Builder
	sql, args, err := q.Build()
	if err != nil || sql != "" || args == nil || len(args) != 0 {
		t.Fatalf("empty Build=%q %#v %v", sql, args, err)
	}
	if q.Write("AND id=:id") != &q || q.Bind("id", 1) != &q || q.Write("ORDER BY id") != &q {
		t.Fatal("chain changed builder")
	}
	sql, args, err = q.Build()
	if err != nil || sql != "AND id=$1\nORDER BY id" || !reflect.DeepEqual(args, []any{1}) {
		t.Fatal("Build validated or changed SQL clauses")
	}
	q.Write("-- end")
	if sql, _, err = q.Build(); err != nil || !strings.HasSuffix(sql, "\n-- end") {
		t.Fatal("Write after Build used stale compilation")
	}
}

func TestPreviewContainsBuiltSQLAndTypedArguments(t *testing.T) {
	for _, dialect := range dialects {
		q := New().Write("WHERE id IN (:ids) AND text=:text").Bind("ids", []int{1, 2}).Bind("text", "O'Brien\n:literal")
		sql, args, err := q.build(dialect)
		if err != nil {
			t.Fatal(err)
		}
		got, err := q.preview(dialect)
		if err != nil {
			t.Fatal(err)
		}
		want := "SQL:\n" + sql + "\nArgs:"
		for i, arg := range args {
			want += fmt.Sprintf("\n  %d: (%T) %#v", i+1, arg, arg)
		}
		if got != want || strings.Contains(sql, "O'Brien") {
			t.Fatalf("Preview=%q", got)
		}
	}
	if got, err := New().Preview(); err != nil || got != "SQL:\n\nArgs: []" {
		t.Fatalf("empty Preview=%q %v", got, err)
	}
}
