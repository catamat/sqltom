// Package query composes SQL fragments and binds named values for its generated dialect.
// It uses only the standard library and does not execute SQL.
package query

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

var (
	ErrInvalidName     = errors.New("invalid parameter name")
	ErrDuplicateBind   = errors.New("parameter already bound")
	ErrMissingBind     = errors.New("parameter has no binding")
	ErrUnusedBind      = errors.New("binding is not used in SQL")
	ErrEmptyList       = errors.New("cannot expand an empty list")
	ErrInvalidList     = errors.New("cannot expand a nested list")
	ErrSyntax          = errors.New("cannot scan SQL parameters")
	ErrMixedParameters = errors.New("use :name parameters instead of native placeholders")
)

// Builder accumulates SQL and bindings. Its zero value is ready to use.
// Keep one builder per query; do not copy or mutate it concurrently with other calls.
// Bound values are retained, not deep-copied, and lists are expanded at Build time.
type Builder struct {
	fragments []string
	bindings  map[string]any
	order     []string
	err       error
}

// New creates a builder configured for the dialect of the generated models.
func New() *Builder { return &Builder{} }

// Write appends a fragment verbatim. Successive fragments are separated by a newline.
// It neither adds nor validates SQL clauses. Errors from Bind are reported by Build.
func (q *Builder) Write(fragment string) *Builder {
	q.fragments = append(q.fragments, fragment)
	return q
}

// Bind registers a case-sensitive name without the colon. Names use ASCII letters,
// digits and underscores, and must not begin with a digit. Duplicate/invalid names
// are recorded as errors and reported by Build and Preview, allowing chaining.
func (q *Builder) Bind(name string, value any) *Builder {
	if q.err != nil {
		return q
	}
	if !validName(name) {
		q.err = fmt.Errorf("%w: %q", ErrInvalidName, name)
		return q
	}
	if _, exists := q.bindings[name]; exists {
		q.err = fmt.Errorf("%w: %q", ErrDuplicateBind, name)
		return q
	}
	if q.bindings == nil {
		q.bindings = make(map[string]any)
	}
	q.bindings[name] = value
	q.order = append(q.order, name)
	return q
}

type scalarValue struct{ value any }

// Scalar prevents list expansion for value. It does not convert the value or
// implement a database codec: the selected database driver must accept it.
func Scalar(value any) any { return scalarValue{value: value} }

// Build returns SQL with native placeholders and positional arguments in SQL order.
// It expands direct slices except byte slices and driver.Valuer implementations.
// Arrays and pointers remain scalar. Empty lists and unbound/unused names are errors.
// It does not evaluate Valuer methods, interpolate values, or validate SQL clauses.
// Every call compiles afresh and returns a new argument slice; values remain shared.
func (q *Builder) Build() (string, []any, error) { return q.build(targetDialect) }

// argumentRange refers to values already appended during this Build call.
// Repeated numbered parameters reuse their indexes; question marks repeat values.
type argumentRange struct{ start, end int }

func (q *Builder) build(dialect string) (string, []any, error) {
	if q.err != nil {
		return "", nil, q.err
	}
	statement := strings.Join(q.fragments, "\n")
	var tokenBuffer [8]parameter
	tokens, err := scanInto(statement, dialect, tokenBuffer[:0])
	if err != nil {
		return "", nil, err
	}
	if len(tokens) == 0 {
		if len(q.order) != 0 {
			return "", nil, fmt.Errorf("%w: %q", ErrUnusedBind, q.order[0])
		}
		return statement, []any{}, nil
	}
	prefix := ""
	switch dialect {
	case "sqlserver":
		prefix = "@p"
	case "postgres":
		prefix = "$"
	}
	args := make([]any, 0, len(q.bindings))
	used := make(map[string]argumentRange, len(q.bindings))
	outputSize := len(statement)
	// Expand once per bound name, in SQL order. Validate the complete lexical
	// scan first and preserve the ordering of missing/list/unused binding errors.
	for _, token := range tokens {
		if token.name == "" {
			outputSize--
			continue
		}
		span, exists := used[token.name]
		if !exists {
			value, bound := q.bindings[token.name]
			if !bound {
				return "", nil, fmt.Errorf("%w: %q", ErrMissingBind, token.name)
			}
			span.start = len(args)
			args, err = appendArguments(args, value)
			if err != nil {
				return "", nil, fmt.Errorf("parameter %q: %w", token.name, err)
			}
			span.end = len(args)
			used[token.name] = span
		} else if prefix == "" {
			args = append(args, args[span.start:span.end]...)
		}
		width := 1 // A question mark, or the largest numbered placeholder in this range.
		if prefix != "" {
			width = len(prefix) + 1
			for n := span.end; n >= 10; n /= 10 {
				width++
			}
		}
		count := span.end - span.start
		outputSize += count*width + (count-1)*2 - (token.end - token.start)
	}
	for _, name := range q.order {
		if _, exists := used[name]; !exists {
			return "", nil, fmt.Errorf("%w: %q", ErrUnusedBind, name)
		}
	}
	var out strings.Builder
	out.Grow(outputSize)
	offset := 0
	for _, token := range tokens {
		out.WriteString(statement[offset:token.start])
		offset = token.end
		if token.name == "" {
			out.WriteByte(':')
			continue
		}
		writePlaceholders(&out, prefix, used[token.name])
	}
	out.WriteString(statement[offset:])
	return out.String(), args, nil
}

// Write into the final SQL buffer: no per-placeholder strings or temporary Join.
func writePlaceholders(out *strings.Builder, prefix string, span argumentRange) {
	var digits [20]byte
	for index := span.start; index < span.end; index++ {
		if index != span.start {
			out.WriteString(", ")
		}
		if prefix == "" {
			out.WriteByte('?')
		} else {
			out.WriteString(prefix)
			out.Write(strconv.AppendInt(digits[:0], int64(index+1), 10))
		}
	}
}

// Preview returns a debug string with the built SQL and numbered, typed arguments.
// It uses the same compilation as Build, never interpolates or executes SQL, and
// does not call driver.Valuer.Value. The result contains the actual bound values.
func (q *Builder) Preview() (string, error) { return q.preview(targetDialect) }

func (q *Builder) preview(dialect string) (string, error) {
	statement, args, err := q.build(dialect)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString("SQL:\n")
	out.WriteString(statement)
	out.WriteString("\nArgs:")
	if len(args) == 0 {
		out.WriteString(" []")
	}
	for i, arg := range args {
		fmt.Fprintf(&out, "\n  %d: (%T) %#v", i+1, arg, arg)
	}
	return out.String(), nil
}

func unwrap(value any) (any, bool) {
	forced := false
	for {
		scalar, ok := value.(scalarValue)
		if !ok {
			return value, forced
		}
		value, forced = scalar.value, true
	}
}

func isList(value any) bool {
	if value == nil {
		return false
	}
	if _, ok := value.(driver.Valuer); ok {
		return false
	}
	v := reflect.ValueOf(value)
	return v.Kind() == reflect.Slice && v.Type().Elem().Kind() != reflect.Uint8
}

func appendArguments(args []any, value any) ([]any, error) {
	value, forced := unwrap(value)
	if forced || !isList(value) {
		return append(args, value), nil
	}
	v := reflect.ValueOf(value)
	if v.Len() == 0 {
		return nil, ErrEmptyList
	}
	// Reserve space for the whole list before boxing elements, rather than
	// repeatedly growing args or allocating a separate expanded-value slice.
	args = slices.Grow(args, v.Len())
	switch values := value.(type) {
	case []int:
		for _, element := range values {
			args = append(args, element)
		}
	case []int64:
		for _, element := range values {
			args = append(args, element)
		}
	case []string:
		for _, element := range values {
			args = append(args, element)
		}
	case []any:
		for _, element := range values {
			element, forced := unwrap(element)
			if !forced && isList(element) {
				return nil, ErrInvalidList
			}
			args = append(args, element)
		}
	default:
		// Preserve named element types, pointers and custom codecs. Valuer methods
		// are never evaluated by the builder, including in the fast paths above.
		for i := 0; i < v.Len(); i++ {
			element, forced := unwrap(v.Index(i).Interface())
			if !forced && isList(element) {
				return nil, ErrInvalidList
			}
			args = append(args, element)
		}
	}
	return args, nil
}

func nameStart(c byte) bool      { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func digit(c byte) bool          { return c >= '0' && c <= '9' }
func namePart(c byte) bool       { return nameStart(c) || digit(c) }
func identifierPart(c byte) bool { return namePart(c) || c == '$' || c >= 128 }
func validName(name string) bool {
	if name == "" || !nameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !namePart(name[i]) {
			return false
		}
	}
	return true
}
