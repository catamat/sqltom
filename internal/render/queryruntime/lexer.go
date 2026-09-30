package query

import (
	"fmt"
	"strings"
)

type parameter struct {
	start, end int
	name       string
}

// scan recognizes only lexical regions and parameter tokens, not SQL grammar.
// Backslash escaping assumes MySQL's default mode and PostgreSQL's default
// standard_conforming_strings=on (backslashes still escape in E'...' strings).
func scan(sql, dialect string) ([]parameter, error) {
	return scanInto(sql, dialect, nil)
}

// The caller may supply scratch storage for this scan only. Build uses a small
// stack buffer; longer statements grow normally without retaining tokens.
func scanInto(sql, dialect string, tokens []parameter) ([]parameter, error) {
	for i := 0; i < len(sql); {
		start := i
		switch {
		case sql[i] == '\\' && i+1 < len(sql) && sql[i+1] == ':':
			tokens = appendParameter(tokens, parameter{start: i, end: i + 2})
			i += 2
		case sql[i] == '\'' || sql[i] == '"' || ((dialect == "mysql" || dialect == "sqlite") && sql[i] == '`') || ((dialect == "sqlserver" || dialect == "sqlite") && sql[i] == '['):
			quote := sql[i]
			end := quote
			if quote == '[' {
				end = ']'
			}
			backslash := dialect == "mysql" && (quote == '\'' || quote == '"')
			if dialect == "postgres" && quote == '\'' && i > 0 && (sql[i-1] == 'E' || sql[i-1] == 'e') && (i < 2 || !identifierPart(sql[i-2])) {
				backslash = true
			}
			i++
			closed := false
			for i < len(sql) {
				if backslash && sql[i] == '\\' && i+1 < len(sql) {
					i += 2
					continue
				}
				if sql[i] == end {
					if i+1 < len(sql) && sql[i+1] == end && !(quote == '[' && dialect == "sqlite") {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unterminated quoted text at byte %d", ErrSyntax, start)
			}
		case sql[i] == '-' && i+1 < len(sql) && sql[i+1] == '-' && (dialect != "mysql" || i+2 == len(sql) || sql[i+2] <= ' '):
			i = lineEnd(sql, i+2, dialect)
		case dialect == "mysql" && sql[i] == '#':
			i = lineEnd(sql, i+1, dialect)
		case sql[i] == '/' && i+1 < len(sql) && sql[i+1] == '*':
			i += 2
			depth := 1
			nested := dialect == "postgres" || dialect == "sqlserver"
			for i < len(sql) && depth > 0 {
				if i+1 < len(sql) && sql[i:i+2] == "*/" {
					depth--
					i += 2
				} else if nested && i+1 < len(sql) && sql[i:i+2] == "/*" {
					depth++
					i += 2
				} else {
					i++
				}
			}
			if depth != 0 {
				if dialect == "sqlite" {
					i = len(sql)
					continue
				} // SQLite comments may end at EOF.
				return nil, fmt.Errorf("%w: unterminated comment at byte %d", ErrSyntax, start)
			}
			if dialect == "mysql" && start+2 < len(sql) && sql[start+2] == '!' {
				// Versioned comments may execute conditionally on the server; their argument
				// count cannot be established by this offline builder. Preserve parameter-free
				// comments, but reject binding inside them rather than silently misbinding.
				inner, err := scan(sql[start+3:i-2], dialect)
				if err != nil {
					return nil, err
				}
				if len(inner) != 0 {
					return nil, fmt.Errorf("%w: parameters in MySQL executable comments are unsupported", ErrSyntax)
				}
			}
		case dialect == "postgres" && sql[i] == '$' && (i == 0 || !identifierPart(sql[i-1])):
			delimiter := dollarDelimiter(sql[i:])
			if delimiter != "" {
				rest := i + len(delimiter)
				end := strings.Index(sql[rest:], delimiter)
				if end < 0 {
					return nil, fmt.Errorf("%w: unterminated dollar-quoted text at byte %d", ErrSyntax, i)
				}
				i = rest + end + len(delimiter)
			} else if i+1 < len(sql) && digit(sql[i+1]) {
				return nil, nativeParameter(i)
			} else {
				i++
			}
		case sql[i] == ':':
			if i+1 < len(sql) && (sql[i+1] == ':' || sql[i+1] == '=') {
				i += 2
				continue
			}
			if i+1 == len(sql) || !nameStart(sql[i+1]) {
				i++
				continue
			}
			i += 2
			for i < len(sql) && identifierPart(sql[i]) {
				i++
			}
			name := sql[start+1 : i]
			if !validName(name) {
				return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
			}
			tokens = appendParameter(tokens, parameter{start: start, end: i, name: name})
		case (dialect == "mysql" || dialect == "sqlite") && sql[i] == '?':
			return nil, nativeParameter(i)
		case dialect == "sqlite" && (sql[i] == '@' || sql[i] == '$') && i+1 < len(sql) && identifierPart(sql[i+1]):
			return nil, nativeParameter(i)
		case dialect == "sqlserver" && sql[i] == '@':
			if i+1 < len(sql) && sql[i+1] == '@' { // Server variables, e.g. @@ROWCOUNT.
				i += 2
				for i < len(sql) && identifierPart(sql[i]) {
					i++
				}
				continue
			}
			if i+2 < len(sql) && (sql[i+1] == 'p' || sql[i+1] == 'P') && digit(sql[i+2]) {
				end := i + 3
				for end < len(sql) && digit(sql[end]) {
					end++
				}
				if end == len(sql) || !identifierPart(sql[end]) {
					return nil, nativeParameter(i)
				}
			}
			i++
		default:
			i++
		}
	}
	return tokens, nil
}

func appendParameter(tokens []parameter, token parameter) []parameter {
	if tokens == nil {
		tokens = make([]parameter, 0, 8)
	}
	return append(tokens, token)
}

func lineEnd(sql string, i int, dialect string) int {
	// MySQL and SQLite keep a bare CR inside the comment. Select the rule
	// only after finding a comment; ordinary SQL needs no extra scan or work.
	if dialect == "mysql" || dialect == "sqlite" {
		if end := strings.IndexByte(sql[i:], '\n'); end >= 0 {
			return i + end
		}
		return len(sql)
	}
	for i < len(sql) && sql[i] != '\n' && sql[i] != '\r' {
		i++
	}
	return i
}

func dollarDelimiter(sql string) string {
	if len(sql) >= 2 && sql[1] == '$' {
		return "$$"
	}
	if len(sql) < 2 || !(nameStart(sql[1]) || sql[1] >= 128) {
		return ""
	}
	for i := 2; i < len(sql); i++ {
		if sql[i] == '$' {
			return sql[:i+1]
		}
		if !namePart(sql[i]) && sql[i] < 128 {
			return ""
		}
	}
	return ""
}

func nativeParameter(offset int) error {
	return fmt.Errorf("%w at byte %d", ErrMixedParameters, offset)
}
