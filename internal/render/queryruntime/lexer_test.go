package query

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLexicalRegionsAndDialectSyntax(t *testing.T) {
	tests := []struct{ name, dialect, sql, want string }{
		{"single and double quotes", "postgres", `SELECT ':skip', 'it''s :skip', ":skip", "a"":skip", :id`, `SELECT ':skip', 'it''s :skip', ":skip", "a"":skip", $1`},
		{"national string", "sqlserver", `SELECT N'it''s :skip', :id`, `SELECT N'it''s :skip', @p1`},
		{"bracket identifiers", "sqlserver", `SELECT [a]]:skip], :id`, `SELECT [a]]:skip], @p1`},
		{"sqlite identifiers", "sqlite", "SELECT [a:skip], `a``:skip`, :id", "SELECT [a:skip], `a``:skip`, ?"},
		{"mysql identifier", "mysql", "SELECT `a``:skip`, :id", "SELECT `a``:skip`, ?"},
		{"postgres cast and assignment", "postgres", `SELECT :id::int, 1::int; x := :id`, `SELECT $1::int, 1::int; x := $1`},
		{"postgres dollar quotes", "postgres", `SELECT $$:skip '$1'$$, $tag$:skip /* $2 */$tag$, :id`, `SELECT $$:skip '$1'$$, $tag$:skip /* $2 */$tag$, $1`},
		{"dollar in identifier", "postgres", `SELECT col$tag$, col$1, :id`, `SELECT col$tag$, col$1, $1`},
		{"postgres escaped strings", "postgres", `SELECT E'it\'s :skip', e'\\:skip', :id`, `SELECT E'it\'s :skip', e'\\:skip', $1`},
		{"postgres ordinary backslash", "postgres", `SELECT '\', :id`, `SELECT '\', $1`},
		{"mysql escaped strings", "mysql", `SELECT 'it\'s :skip', "it\"s :skip", :id`, `SELECT 'it\'s :skip', "it\"s :skip", ?`},
		{"nested postgres comment", "postgres", `/* outer /* :skip */ :skip */ :id`, `/* outer /* :skip */ :skip */ $1`},
		{"nested sqlserver comment", "sqlserver", `/* outer /* :skip */ :skip */ :id`, `/* outer /* :skip */ :skip */ @p1`},
		{"mysql non-nested comment", "mysql", `/* outer /* :skip */ :id`, `/* outer /* :skip */ ?`},
		{"sqlite non-nested comment", "sqlite", `/* outer /* :skip */ :id`, `/* outer /* :skip */ ?`},
		{"mysql dash arithmetic", "mysql", `SELECT 1--:id`, `SELECT 1--?`},
		{"mysql comments", "mysql", "-- :skip\r# :skip\n:id", "-- :skip\r# :skip\n?"},
		{"line comment", "postgres", "--:skip\n:id -- :skip", "--:skip\n$1 -- :skip"},
		{"postgres literal colon", "postgres", `SELECT data[1\:upper], :id`, `SELECT data[1:upper], $1`},
		{"postgres array parameters", "postgres", `SELECT ARRAY[:id], :id`, `SELECT ARRAY[$1], $1`},
		{"postgres JSON operators", "postgres", `SELECT data ? 'key', data ?| ARRAY['a'], :id`, `SELECT data ? 'key', data ?| ARRAY['a'], $1`},
		{"sqlserver variables", "sqlserver", `DECLARE @local int = :id; SELECT @local, @@ROWCOUNT, @@p1, @p1_local`, `DECLARE @local int = @p1; SELECT @local, @@ROWCOUNT, @@p1, @p1_local`},
		{"executable mysql comment", "mysql", `SELECT /*!80000 ':skip' */ :id`, `SELECT /*!80000 ':skip' */ ?`},
		{"sqlite eof comment", "sqlite", `:id /* :skip`, `? /* :skip`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, args, err := New().Write(test.sql).Bind("id", 42).build(test.dialect)
			if err != nil || got != test.want || len(args) == 0 {
				t.Fatalf("Build=%q, %#v, %v; want %q", got, args, err, test.want)
			}
			for _, arg := range args {
				if arg != 42 {
					t.Fatal("unexpected argument")
				}
			}
		})
	}
}

func TestSQLTokenErrors(t *testing.T) {
	tests := []struct {
		dialect, sql string
		want         error
	}{
		{"postgres", `SELECT 'unterminated`, ErrSyntax},
		{"sqlserver", `SELECT [unterminated`, ErrSyntax},
		{"mysql", "SELECT `unterminated", ErrSyntax},
		{"mysql", `SELECT 'escape\`, ErrSyntax},
		{"postgres", `$tag$unterminated`, ErrSyntax},
		{"postgres", `/* unterminated`, ErrSyntax},
		{"mysql", `/*!80000 SELECT :id */`, ErrSyntax},
		{"mysql", `/*!80000 SELECT ? */`, ErrMixedParameters},
		{"postgres", `SELECT $1, :id`, ErrMixedParameters},
		{"sqlserver", `SELECT @p1, :id`, ErrMixedParameters},
		{"sqlserver", `SELECT @P2`, ErrMixedParameters},
		{"mysql", `SELECT ?`, ErrMixedParameters},
		{"sqlite", `SELECT ?12`, ErrMixedParameters},
		{"sqlite", `SELECT @id`, ErrMixedParameters},
		{"sqlite", `SELECT $id`, ErrMixedParameters},
		{"postgres", `:nomeè`, ErrInvalidName},
	}
	for _, test := range tests {
		sql, args, err := New().Write(test.sql).build(test.dialect)
		if sql != "" || args != nil || !errors.Is(err, test.want) {
			t.Fatalf("%s %q: %q, %#v, %v", test.dialect, test.sql, sql, args, err)
		}
	}
}

func TestFragmentsAreScannedTogether(t *testing.T) {
	for _, dialect := range dialects {
		q := New().Write("/*").Write(":ignored */").Write("-- :ignored").Write(":id").Bind("id", 1)
		sql, args, err := q.build(dialect)
		if err != nil || !strings.HasPrefix(sql, "/*\n:ignored */\n-- :ignored\n") || !reflect.DeepEqual(args, []any{1}) {
			t.Fatalf("%q %#v %v", sql, args, err)
		}
	}
}

func TestLineCommentTerminators(t *testing.T) {
	for _, dialect := range dialects {
		for _, ending := range []string{"\n", "\r\n", "\r"} {
			comments := []string{"-- "}
			if dialect == "mysql" {
				comments = append(comments, "# ")
			}
			for _, comment := range comments {
				source := "SELECT 0 " + comment + ":ignored" + ending + " + :b\n + :a, :b"
				tokens, err := scan(source, dialect)
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, token := range tokens {
					names = append(names, token.name)
				}
				want := []string{"b", "a", "b"}
				if ending == "\r" && (dialect == "mysql" || dialect == "sqlite") {
					want = []string{"a", "b"}
				}
				if !reflect.DeepEqual(names, want) {
					t.Fatalf("%s %q: names=%v, want %v", dialect, source, names, want)
				}
			}
		}
		if dialect == "mysql" || dialect == "sqlite" {
			for _, suffix := range []string{":ignored", "?", "'unclosed", `\:ignored`} {
				source := "SELECT :a -- comment\r " + suffix
				stmt, args, err := New().Write(source).Bind("a", 1).build(dialect)
				if err != nil || !strings.HasSuffix(stmt, "-- comment\r "+suffix) || !reflect.DeepEqual(args, []any{1}) {
					t.Fatalf("%s comment at EOF: %q %#v %v", dialect, stmt, args, err)
				}
			}
		}
	}
}

func FuzzScanTokens(f *testing.F) {
	for _, seed := range []string{":id::uuid", `E'it\'s :id' :id`, "/* /* x */ y */ :id", `$x$:a$x$ :b`, "IN (:ids)", "[x]]:id]", `x[1\:upper]`, "/*!80000 :id */", "SELECT 0 -- comment\r + :b\n + :a, :b", "# comment\r:ignored"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, sql string) {
		for _, dialect := range dialects {
			tokens, err := scan(sql, dialect)
			if err != nil {
				continue
			}
			last := 0
			for _, token := range tokens {
				if token.start < last || token.end <= token.start || token.end > len(sql) {
					t.Fatalf("invalid token bounds: %#v", tokens)
				}
				if token.name != "" && (!validName(token.name) || sql[token.start:token.end] != ":"+token.name) {
					t.Fatalf("invalid name token: %#v", token)
				}
				if token.name == "" && sql[token.start:token.end] != `\:` {
					t.Fatalf("invalid escape token: %#v", token)
				}
				last = token.end
			}
		}
	})
}
