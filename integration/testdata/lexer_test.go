package models_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"generated.test/models/query"
)

// These expectations describe database results, not the lexer's token stream.
// The same source is compiled with the helper generated for each real dialect.
func TestQueryLexerAgainstDatabase(t *testing.T) {
	db := openDB(t)
	dialect := os.Getenv("SQLTOM_E2E_DIALECT")
	type binding struct {
		name  string
		value any
	}
	type example struct {
		name      string
		fragments []string
		bindings  []binding
		want      []string
		columns   []string
		argCount  int
	}
	number := []binding{{"value", 7}}
	examples := []example{
		{"strings_and_block_comment", []string{`SELECT ':ignored', 'it''s :ignored', :value + 0 /* ':ignored ? $1 @p1 */`}, number, []string{":ignored", "it's :ignored", "7"}, nil, 1},
		{"comment_at_eof", []string{"SELECT :value + 0 -- :ignored ? '"}, number, []string{"7"}, nil, 1},
		{"fragments_after_line_comment", []string{"SELECT 0 -- :ignored", "+ :value"}, number, []string{"7"}, nil, 1},
		{"block_comment_across_fragments", []string{"SELECT /*", ":ignored */ :value + 0"}, number, []string{"7"}, nil, 1},
		{"list", []string{"SELECT CASE WHEN :value + 0 IN (:ids) THEN 1 ELSE 0 END"}, []binding{{"ids", []int{3, 7}}, {"value", 7}}, []string{"1"}, nil, 3},
		{"quoted_identifier", []string{"SELECT :value + 0 AS " + q("a`\"]:ignored")}, number, []string{"7"}, []string{"a`\"]:ignored"}, 1},
	}
	repeatedCount := 3
	if dialect == "postgres" || dialect == "sqlserver" {
		repeatedCount = 2
	}
	examples = append(examples, example{"repeated_binding_order", []string{"SELECT :b + 0, :a + 0, :b + 0"}, []binding{{"a", 1}, {"b", 2}}, []string{"2", "1", "2"}, nil, repeatedCount})
	for _, ending := range []struct{ name, text string }{{"LF", "\n"}, {"CRLF", "\r\n"}, {"CR", "\r"}} {
		comments := []string{"-- "}
		if dialect == "mysql" {
			comments = append(comments, "# ")
		}
		for _, comment := range comments {
			want, count := []string{"3", "2"}, repeatedCount
			if ending.text == "\r" && (dialect == "mysql" || dialect == "sqlite") {
				want, count = []string{"1", "2"}, 2
			}
			examples = append(examples, example{
				"comment_" + strings.TrimSpace(comment) + "_" + ending.name,
				[]string{"SELECT 0 " + comment + ":ignored" + ending.text + " + :b\n + :a, :b + 0"},
				[]binding{{"a", 1}, {"b", 2}}, want, nil, count,
			})
		}
	}
	switch dialect {
	case "postgres":
		examples = append(examples,
			example{"dollar_quotes", []string{`SELECT $$:ignored '$1'$$, $tag$:ignored /* $2 */$tag$, :value::int`}, number, []string{":ignored '$1'", ":ignored /* $2 */", "7"}, nil, 1},
			example{"escaped_strings", []string{`SELECT E'it\'s :ignored', e'\\:ignored', :value::int`}, number, []string{"it's :ignored", `\:ignored`, "7"}, nil, 1},
			example{"ordinary_backslash", []string{`SELECT '\', :value::int`}, number, []string{`\`, "7"}, nil, 1},
			example{"nested_comments", []string{`SELECT /* outer /* :ignored */ :ignored */ :value::int`}, number, []string{"7"}, nil, 1},
			example{"json_operator", []string{`SELECT '{"key":1}'::jsonb ? 'key', :value::int`}, number, []string{"true", "7"}, nil, 1},
			example{"literal_colon_array_slice", []string{`SELECT (ARRAY[1,2,3])[1\:upper_bound], :value::int FROM (SELECT 2 AS upper_bound) AS bounds`}, number, []string{"{1,2}", "7"}, nil, 1},
			example{"dollar_in_identifier", []string{`SELECT :value::int AS amount$1`}, number, []string{"7"}, []string{"amount$1"}, 1},
		)
	case "sqlserver":
		examples = append(examples,
			example{"national_strings", []string{`SELECT N'it''s :ignored', :value + 0`}, number, []string{"it's :ignored", "7"}, nil, 1},
			example{"nested_comments", []string{`SELECT /* outer /* :ignored */ :ignored */ :value + 0`}, number, []string{"7"}, nil, 1},
			example{"local_variable", []string{`DECLARE @local int = :value; SELECT @local`}, number, []string{"7"}, nil, 1},
		)
	case "mysql":
		examples = append(examples,
			example{"escaped_strings", []string{`SELECT 'it\'s :ignored', "it\"s :ignored", :value`}, number, []string{"it's :ignored", "it\"s :ignored", "7"}, nil, 1},
			example{"dash_arithmetic", []string{`SELECT 1--:value`}, number, []string{"8"}, nil, 1},
			example{"executable_comment", []string{`SELECT /*!80000 5 + */ :value`}, number, []string{"12"}, nil, 1},
			example{"hash_at_eof", []string{"SELECT :value # comment\r :ignored ? '"}, number, []string{"7"}, nil, 1},
			example{"non_nested_comment", []string{`SELECT /* outer /* :ignored */ :value`}, number, []string{"7"}, nil, 1},
		)
	case "sqlite":
		examples = append(examples,
			example{"block_comment_at_eof", []string{`SELECT :value /* :ignored`}, number, []string{"7"}, nil, 1},
			example{"bracket_identifier", []string{`SELECT :value AS [a:ignored]`}, number, []string{"7"}, []string{"a:ignored"}, 1},
			example{"backtick_identifier", []string{"SELECT :value AS `a``:ignored`"}, number, []string{"7"}, []string{"a`:ignored"}, 1},
			example{"ordinary_backslash", []string{`SELECT '\', :value`}, number, []string{`\`, "7"}, nil, 1},
			example{"non_nested_comment", []string{`SELECT /* outer /* :ignored */ :value`}, number, []string{"7"}, nil, 1},
		)
	default:
		t.Fatalf("unknown dialect %q", dialect)
	}
	for _, test := range examples {
		t.Run(test.name, func(t *testing.T) {
			builder := query.New()
			for _, fragment := range test.fragments {
				builder.Write(fragment)
			}
			for _, binding := range test.bindings {
				builder.Bind(binding.name, binding.value)
			}
			stmt, args, err := builder.Build()
			must(t, err)
			if len(args) != test.argCount {
				t.Fatalf("args=%#v, want %d", args, test.argCount)
			}
			rows, err := db.Query(stmt, args...)
			must(t, err)
			defer rows.Close()
			columns, err := rows.Columns()
			must(t, err)
			if test.columns != nil && !reflect.DeepEqual(columns, test.columns) {
				t.Fatalf("columns=%q, want %q", columns, test.columns)
			}
			if !rows.Next() {
				t.Fatalf("missing row: %v", rows.Err())
			}
			got := make([]string, len(test.want))
			destinations := make([]any, len(got))
			for i := range got {
				destinations[i] = &got[i]
			}
			must(t, rows.Scan(destinations...))
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SQL=%q args=%#v: got %q, want %q", stmt, args, got, test.want)
			}
			if rows.Next() {
				t.Fatal("unexpected extra row")
			}
			must(t, rows.Err())
		})
	}
}
