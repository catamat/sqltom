package {{ $.Table.PackageName }}

import (
{{- range $.Imports }}
	{{ quote . }}
{{- end }}
)

{{- $structName := $.Table.TableName -}}
{{- if ne $.Table.GoName "" -}}
	{{- $structName = $.Table.GoName -}}
{{- end -}}

{{- $structType := "table" -}}
{{- if eq $.Table.TableType "VIEW" -}}
	{{- $structType = "view" -}}
{{- end }}

// {{ $structName }} defines a row in {{ quote $.Table.TableName }} {{ $structType }}.
type {{ $structName }} struct {
{{- range $.Table.Columns }}
	{{ .GoName }} {{ .ResolvedGoType }} {{ structTag .ColumnName .JSONName .IncludeJSONTag }}
{{- end }}
}

// Select selects rows from the database using the statement.
func Select(db *sql.DB, stmt string, args ...interface{}) ([]*{{ $structName }}, error) {
	var query = `SELECT ` +
		`{{- range $i, $e := $.Table.Columns }}
			{{- if .IsUUID -}}
			CAST({{ sqlIdentifier $.Table.TableName }}.{{ sqlIdentifier .ColumnName }} AS CHAR(36)){{ if last $i $.Table.Columns | not }}, {{ end }}
			{{- else -}}
			{{ sqlIdentifier $.Table.TableName }}.{{ sqlIdentifier .ColumnName }}{{ if last $i $.Table.Columns | not }}, {{ end }}
			{{- end -}}
		{{- end }} ` +
		`FROM {{ sqlTableIdentifier $.Table.TableCatalog $.Table.TableSchema $.Table.TableName }} ` + stmt

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	rr := []*{{ $structName }}{}

	for rows.Next() {
		r := &{{ $structName }}{}
		rows.Scan(
		{{- range $i, $e := $.Table.Columns }}
		{{- if ne .GoName "" -}}
		&r.{{ .GoName }}{{ if last $i $.Table.Columns | not }}, {{ end }}
		{{- else -}}
		&r.{{ .ColumnName }}{{ if last $i $.Table.Columns | not }}, {{ end }}
		{{- end -}}
		{{- end -}}
		)

		rr = append(rr, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return rr, nil
}

{{- if eq $.Table.TableType "BASE TABLE" }}

// Insert inserts the row to the database.
func (r *{{ $structName }}) Insert(db *sql.DB) error {
	{{- $idColumnName := "" -}}
	{{- $idIsUUID := false -}}

	{{- range $i, $e := $.Table.Columns -}}
		{{- if eq .ColumnName $.Index -}}
			{{- $idColumnName = .ColumnName -}}
			{{- $idIsUUID = .IsUUID -}}
		{{- end -}}
	{{- end -}}
	
	const query = `INSERT INTO {{ sqlTableIdentifier $.Table.TableCatalog $.Table.TableSchema $.Table.TableName }} (` +
		`{{- range $i, $e := $.Table.WriteColumns }}
			{{ sqlIdentifier .ColumnName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
		{{- end -}}` +
		`) ` +
		{{- if ne $idColumnName "" }}
		{{- if $idIsUUID }}
		`OUTPUT CAST(INSERTED.{{ sqlIdentifier $idColumnName }} AS CHAR(36)) ` +
		{{- else }}
		`OUTPUT INSERTED.{{ sqlIdentifier $idColumnName }} ` +
		{{- end }}
		{{- end }}
		`VALUES (` +
		{{- $c := 0 }}
		`{{- range $i, $e := $.Table.WriteColumns }}
			{{- $c = add $c 1 -}}
			@p{{ $c }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
		{{- end -}}` +
		`)`

	{{- $idName := "" -}}
	{{- $idDataType := "" -}}

	{{- range $i, $e := $.Table.Columns -}}
		{{- if eq .ColumnName $.Index -}}
			{{- if ne .GoName "" -}}
				{{- $idName = .GoName -}}
				{{- $idDataType = .ResolvedGoType -}}
			{{- else -}}
				{{- $idName = .ColumnName -}}
				{{- $idDataType = .ResolvedGoType -}}
			{{- end -}}
		{{- end -}}
	{{- end -}}

	{{- if ne $idName "" }}

	row := db.QueryRow(query, {{ range $i, $e := $.Table.WriteColumns }}
	{{- if ne .GoName "" -}}
	r.{{ .GoName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
	{{- else -}}
	r.{{ .ColumnName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
	{{- end -}}
	{{- end -}}
	)

	var lastInsertID {{ $idDataType }}
	err := row.Scan(&lastInsertID)
	if err != nil {
		return err
	}

	r.{{ $idName }} = {{ $idDataType }}(lastInsertID)
	
	{{- else }}

	db.QueryRow(query, {{ range $i, $e := $.Table.WriteColumns }}
	{{- if ne .GoName "" -}}
	r.{{ .GoName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
	{{- else -}}
	r.{{ .ColumnName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
	{{- end -}}
	{{- end -}}
	)

	{{- end }}

	return nil
}

// Update updates the row in the database.
func (r *{{ $structName }}) Update(db *sql.DB) error {
	{{- $idColumnName := "" -}}
	{{- $idGoName := "" -}}

	{{- range $i, $e := $.Table.Columns -}}
		{{- if eq .ColumnName $.Index -}}
			{{- $idColumnName = .ColumnName -}}
			{{- $idGoName = .GoName -}}
		{{- end -}}
	{{- end -}}

	{{- $c := 0 }}
	const query = `UPDATE {{ sqlTableIdentifier $.Table.TableCatalog $.Table.TableSchema $.Table.TableName }} SET ` +	
		`{{ range $i, $e := $.Table.WriteColumns }}
			{{- $c = add $c 1 -}}
			{{ sqlIdentifier .ColumnName }} = @p{{ $c }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
		{{- end -}}`
		{{- if ne $idColumnName "" }} +
		{{- $c = add $c 1 }}
		` WHERE {{ sqlIdentifier $.Table.TableName }}.{{ sqlIdentifier $idColumnName }} = @p{{ $c }}`
		{{- end }}
	
	_, err := db.Exec(query, {{ range $i, $e := $.Table.WriteColumns }}
		{{- $c = add $c 1 -}}
		{{- if ne .GoName "" -}}
		r.{{ .GoName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
		{{- else -}}
		r.{{ .ColumnName }}{{ if last $i $.Table.WriteColumns | not }}, {{ end }}
		{{- end -}}
	{{- end -}}
	{{- if ne $idColumnName "" }}
		{{- if ne $idGoName "" -}}
		, r.{{ $idGoName }}
		{{- else -}}
		, r.{{ $idColumnName }}
		{{- end -}}
	{{- end -}}
	)
	return err
}

// Delete deletes rows from the database using the statement.
func Delete(db *sql.DB, stmt string, args ...interface{}) error {
	var query = `DELETE FROM {{ sqlTableIdentifier $.Table.TableCatalog $.Table.TableSchema $.Table.TableName }} ` + stmt

	_, err := db.Exec(query, args...)
	return err
}

{{- end }}

// Exists checks if rows exists in the database using the statement.
func Exists(db *sql.DB, stmt string, args ...interface{}) bool {
	var query = `SELECT 1 FROM {{ sqlTableIdentifier $.Table.TableCatalog $.Table.TableSchema $.Table.TableName }} ` + stmt

	err := db.QueryRow(query, args...).Scan()
	if err == sql.ErrNoRows {
		return false
	}

	return true
}

// Query queries the database using the statement.
func Query(db *sql.DB, stmt string, args ...interface{}) ([]*{{ $structName }}, error) {
	var fields = 
		`{{- range $i, $e := $.Table.Columns }}
			{{- if .IsUUID -}}
			CAST({{ sqlIdentifier $.Table.TableName }}.{{ sqlIdentifier .ColumnName }} AS CHAR(36)){{ if last $i $.Table.Columns | not }}, {{ end }}
			{{- else -}}
			{{ sqlIdentifier $.Table.TableName }}.{{ sqlIdentifier .ColumnName }}{{ if last $i $.Table.Columns | not }}, {{ end }}
			{{- end -}}
		{{- end }}`
	var query = strings.Replace(stmt, `*`, fields, -1)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	rr := []*{{ $structName }}{}

	for rows.Next() {
		r := &{{ $structName }}{}
		rows.Scan(
		{{- range $i, $e := $.Table.Columns }}
		{{- if ne .GoName "" -}}
		&r.{{ .GoName }}{{ if last $i $.Table.Columns | not }}, {{ end }}
		{{- else -}}
		&r.{{ .ColumnName }}{{ if last $i $.Table.Columns | not }}, {{ end }}
		{{- end -}}
		{{- end -}}
		)

		rr = append(rr, r)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	return rr, nil
}
