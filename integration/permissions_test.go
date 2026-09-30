//go:build integration

package integration_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/catamat/sqltom/internal/dialect"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// The invariant is identical across backends: expose the whole key or fail.
// Catalog visibility itself differs between database engines.
func assertColumnPermissions(t *testing.T, ctx context.Context, current fixture) {
	if current.name == dialect.SQLite {
		t.Skip("SQLite has file permissions, no SQL users or column grants; complete keys are covered by the common contract")
	}
	adminDSN := current.dsn
	if current.name == dialect.MySQL {
		if current.adminDSN == "" {
			t.Skip("SQLTOM_MYSQL_ADMIN_DSN is required to create a restricted test user")
		}
		adminDSN = current.adminDSN
	}
	db := openDatabase(t, ctx, current.driverName, adminDSN)
	defer db.Close()
	requireTestDatabase(t, ctx, db, current.name)
	q := func(s string) string { return runtimeIdentifier(current, s) }
	name := q(current.schema) + "." + q("PermissionKey")
	cleanupObjects(t, current, "TABLE", []string{"PermissionKey"})
	for _, query := range []string{
		"CREATE TABLE " + name + " (" + q("Visible") + " INT NOT NULL, " + q("Hidden") + " INT NOT NULL, " + q("Payload") + " VARCHAR(100) NOT NULL, PRIMARY KEY (" + q("Visible") + "," + q("Hidden") + "))",
		"INSERT INTO " + name + " VALUES (7,11,'first'), (7,12,'second')",
	} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name, columns string
		complete      bool
	}{
		{"missing_tail", q("Visible") + "," + q("Payload"), false},
		{"missing_head", q("Hidden") + "," + q("Payload"), false},
		{"entire_key_hidden", q("Payload"), false},
		{"complete", q("Visible") + "," + q("Hidden") + "," + q("Payload"), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const user = "sqltom_column_reader"
			const password = "Sqltom_column_2026!"
			var setup, cleanup []string
			var readerDSN string
			switch current.name {
			case dialect.Postgres:
				setup = []string{"CREATE ROLE " + user + " NOLOGIN", "GRANT USAGE ON SCHEMA " + q(current.schema) + " TO " + user,
					"GRANT SELECT (" + test.columns + "), UPDATE (" + q("Payload") + ") ON " + name + " TO " + user}
				cleanup = []string{"DROP OWNED BY " + user, "DROP ROLE " + user}
				readerDSN = postgresRoleDSN(t, current.dsn, user)
			case dialect.MySQL:
				setup = []string{"CREATE USER '" + user + "'@'%' IDENTIFIED BY '" + password + "'", "GRANT SELECT (" + test.columns + "), UPDATE (" + q("Payload") + ") ON " + name + " TO '" + user + "'@'%'"}
				cleanup = []string{"DROP USER '" + user + "'@'%'"}
				config, err := mysqldriver.ParseDSN(current.dsn)
				if err != nil {
					t.Fatal(err)
				}
				config.User, config.Passwd = user, password
				readerDSN = config.FormatDSN()
			case dialect.SQLServer:
				setup = []string{"CREATE LOGIN " + user + " WITH PASSWORD = '" + password + "', CHECK_POLICY = OFF", "CREATE USER " + user + " FOR LOGIN " + user,
					"GRANT SELECT ON OBJECT::" + name + " (" + test.columns + ") TO " + user,
					"GRANT UPDATE ON OBJECT::" + name + " (" + q("Payload") + ") TO " + user}
				cleanup = []string{"DROP USER " + user, "DROP LOGIN " + user}
				parsed, err := url.Parse(current.dsn)
				if err != nil || parsed.Scheme != "sqlserver" {
					t.Fatal("permission tests require a sqlserver:// DSN")
				}
				parsed.User = url.UserPassword(user, password)
				readerDSN = parsed.String()
			}
			for i, query := range setup {
				if _, err := db.ExecContext(ctx, query); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					defer func() {
						for _, query := range cleanup {
							if _, err := db.ExecContext(ctx, query); err != nil {
								t.Errorf("permission cleanup: %v", err)
							}
						}
					}()
				}
			}
			inspection, err := current.backend.Inspect(ctx, readerDSN)
			if !test.complete && current.name != dialect.SQLServer {
				if inspection != nil || err == nil || !strings.Contains(err.Error(), "incomplete primary key metadata") {
					t.Fatalf("partial key inspection = %#v, %v; want rejection", inspection, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			table := findTable(t, inspection.Manifest, current.schema, "PermissionKey")
			if len(table.Columns) != 3 {
				t.Fatalf("visible columns = %d, want 3", len(table.Columns))
			}
			for i, column := range table.Columns {
				ordinal := i + 1
				if i == 2 {
					ordinal = 0
				}
				if column.PrimaryKeyOrdinal != ordinal || column.IsPrimaryKey != (ordinal > 0) {
					t.Fatalf("key metadata = %#v", table.Columns)
				}
			}
		})
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+name+" WHERE "+q("Payload")+" IN ('first','second')").Scan(&count); err != nil || count != 2 {
		t.Fatalf("inspection changed source rows: count=%d, error=%v", count, err)
	}
}

func postgresRoleDSN(t *testing.T, dsn, role string) string {
	t.Helper()
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("options", strings.TrimSpace(query.Get("options")+" -c role="+role))
		parsed.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
		return parsed.String()
	}
	return dsn + " options='-c role=" + role + "'"
}
