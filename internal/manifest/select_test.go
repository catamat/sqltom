package manifest

import (
	"strings"
	"testing"
)

func TestSelectTablesReturnsExactIndependentSubsetInManifestOrder(t *testing.T) {
	m := selectionManifest()

	selected, err := SelectTables(m, []string{"dbo.Customer", "MainDB.audit.Vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Tables) != 2 {
		t.Fatalf("len(Tables) = %d, want 2", len(selected.Tables))
	}
	if got := selected.Tables[0].Key().String(); got != "MainDB.audit.Vehicle" {
		t.Fatalf("Tables[0] = %q, want audit view", got)
	}
	if got := selected.Tables[1].Key().String(); got != "MainDB.dbo.Customer" {
		t.Fatalf("Tables[1] = %q, want customer table", got)
	}
	if len(m.Tables) != 4 {
		t.Fatalf("source manifest was filtered: len(Tables) = %d", len(m.Tables))
	}
	if selected.DatabaseName != m.DatabaseName || selected.Version != m.Version {
		t.Fatalf("manifest identity changed: %#v", selected)
	}
	if selected.TypeMappings["custom"].GoType != "Custom" {
		t.Fatalf("TypeMappings were not preserved: %#v", selected.TypeMappings)
	}

	selected.TypeMappings["custom"] = TypeMapping{GoType: "Changed"}
	selected.Tables[0].Columns[0].ColumnName = "Changed"
	if m.TypeMappings["custom"].GoType != "Custom" {
		t.Fatal("selected TypeMappings alias the source manifest")
	}
	for _, table := range m.Tables {
		if table.Key().String() == "MainDB.audit.Vehicle" && table.Columns[0].ColumnName != "ID" {
			t.Fatal("selected columns alias the source manifest")
		}
	}
}

func TestSelectTablesWithoutSelectorsReturnsAllObjects(t *testing.T) {
	m := selectionManifest()
	selected, err := SelectTables(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected == m || len(selected.Tables) != len(m.Tables) {
		t.Fatalf("selection = %#v, want independent complete manifest", selected)
	}
	selected.Tables[0].Columns[0].ColumnName = "Changed"
	if m.Tables[0].Columns[0].ColumnName == "Changed" {
		t.Fatal("complete selection aliases source columns")
	}
}

func TestSelectTablesRequiresUnambiguousExactDatabaseIdentity(t *testing.T) {
	m := selectionManifest()
	tests := []struct {
		name      string
		selectors []string
		want      string
	}{
		{
			name:      "ambiguous unqualified name",
			selectors: []string{"Vehicle"},
			want:      "ambiguous",
		},
		{
			name:      "unknown name",
			selectors: []string{"Missing"},
			want:      "did not match",
		},
		{
			name:      "case mismatch",
			selectors: []string{"dbo.customer"},
			want:      "did not match",
		},
		{
			name:      "GoName is not a selector",
			selectors: []string{"CustomerModel"},
			want:      "did not match",
		},
		{
			name:      "FileName is not a selector",
			selectors: []string{"CustomerFile"},
			want:      "did not match",
		},
		{
			name:      "PackageName is not a selector",
			selectors: []string{"customerpackage"},
			want:      "did not match",
		},
		{
			name:      "two selectors identify same table",
			selectors: []string{"dbo.Customer", "MainDB.dbo.Customer"},
			want:      "both identify",
		},
		{
			name:      "empty programmatic selector",
			selectors: []string{"   "},
			want:      "selector is empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := SelectTables(m, test.selectors)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("SelectTables() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSelectTablesAmbiguityReportsDeterministicQualifiedCandidates(t *testing.T) {
	m := selectionManifest()
	_, err := SelectTables(m, []string{"Vehicle"})
	if err == nil {
		t.Fatal("expected ambiguous selector error")
	}
	message := err.Error()
	audit := `(catalog="MainDB", schema="audit", name="Vehicle") (selector "MainDB.audit.Vehicle")`
	dbo := `(catalog="MainDB", schema="dbo", name="Vehicle") (selector "MainDB.dbo.Vehicle")`
	auditIndex := strings.Index(message, audit)
	dboIndex := strings.Index(message, dbo)
	if auditIndex < 0 || dboIndex < 0 || auditIndex >= dboIndex {
		t.Fatalf("ambiguous selector candidates are missing or unstable: %s", message)
	}
}

func TestSelectTablesPartiallyQualifiedSelectorCanBeDisambiguatedByCatalog(t *testing.T) {
	m := New("MainDB", []Table{
		{
			TableCatalog: "ArchiveDB",
			TableSchema:  "dbo",
			TableName:    "Vehicle",
			TableType:    "VIEW",
			Columns:      []Column{{ColumnName: "ID", OrdinalPosition: 1, DataType: DataTypeInteger}},
		},
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Vehicle",
			TableType:    "BASE TABLE",
			Columns:      []Column{{ColumnName: "ID", OrdinalPosition: 1, DataType: DataTypeInteger}},
		},
	})
	if _, err := SelectTables(m, []string{"dbo.Vehicle"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("partially qualified selector error = %v", err)
	}
	selected, err := SelectTables(m, []string{"ArchiveDB.dbo.Vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Tables) != 1 || selected.Tables[0].TableCatalog != "ArchiveDB" {
		t.Fatalf("catalog-qualified selection = %#v", selected.Tables)
	}
}

func TestSelectTablesQualifiedSelectorDisambiguatesAndIncludesViews(t *testing.T) {
	m := selectionManifest()
	selected, err := SelectTables(m, []string{"audit.Vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Tables) != 1 || selected.Tables[0].Key().String() != "MainDB.audit.Vehicle" || selected.Tables[0].TableType != "VIEW" {
		t.Fatalf("selected tables = %#v", selected.Tables)
	}
}

func TestSelectTablesAcceptsExactNameContainingComma(t *testing.T) {
	m := selectionManifest()
	selected, err := SelectTables(m, []string{"Order,Archive"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Tables) != 1 || selected.Tables[0].TableName != "Order,Archive" {
		t.Fatalf("selected tables = %#v", selected.Tables)
	}
}

func TestSelectTablesEscapedQualificationDisambiguatesDots(t *testing.T) {
	m := New("MainDB", []Table{
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Order.Item",
			TableType:    "BASE TABLE",
			Columns:      []Column{{ColumnName: "ID", OrdinalPosition: 1, DataType: DataTypeInteger}},
		},
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo.Order",
			TableName:    "Item",
			TableType:    "VIEW",
			Columns:      []Column{{ColumnName: "ID", OrdinalPosition: 1, DataType: DataTypeInteger}},
		},
	})

	if _, err := SelectTables(m, []string{"dbo.Order.Item"}); err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("unescaped colliding selector error = %v", err)
	}

	table, err := SelectTables(m, []string{"MainDB.dbo.Order\\.Item"})
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Tables) != 1 || table.Tables[0].TableName != "Order.Item" {
		t.Fatalf("escaped table-name selection = %#v", table.Tables)
	}

	view, err := SelectTables(m, []string{"MainDB.dbo\\.Order.Item"})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Tables) != 1 || view.Tables[0].TableSchema != "dbo.Order" {
		t.Fatalf("escaped schema selection = %#v", view.Tables)
	}
}

func TestSelectTablesFullQualificationPreservesEmptyComponents(t *testing.T) {
	m := New("MainDB", []Table{
		{
			TableSchema: "dbo",
			TableName:   "Vehicle",
			TableType:   "VIEW",
			Columns:     []Column{{ColumnName: "ID", OrdinalPosition: 1, DataType: DataTypeInteger}},
		},
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Vehicle",
			TableType:    "BASE TABLE",
			Columns:      []Column{{ColumnName: "ID", OrdinalPosition: 1, DataType: DataTypeInteger}},
		},
	})

	selected, err := SelectTables(m, []string{".dbo.Vehicle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Tables) != 1 || selected.Tables[0].TableCatalog != "" {
		t.Fatalf("empty-catalog selection = %#v", selected.Tables)
	}
}

func TestSelectTablesRejectsNilManifest(t *testing.T) {
	if _, err := SelectTables(nil, []string{"Vehicle"}); err == nil {
		t.Fatal("expected nil manifest error")
	}
}

func TestManagedTablesReturnsIndependentRenderableSubset(t *testing.T) {
	m := selectionManifest()
	for tableIndex := range m.Tables {
		if m.Tables[tableIndex].TableName == "Customer" {
			m.Tables[tableIndex].IsManaged = false
			m.Tables[tableIndex].FileName = "Discarded"
			m.Tables[tableIndex].PackageName = "discarded"
			m.Tables[tableIndex].GoName = "Discarded"
		}
	}
	Canonicalize(m)

	managed, err := ManagedTables(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed.Tables) != 3 {
		t.Fatalf("managed tables = %#v", managed.Tables)
	}
	for _, table := range managed.Tables {
		if !table.IsManaged || table.TableName == "Customer" {
			t.Fatalf("unmanaged table survived filtering: %#v", table)
		}
	}
	if len(m.Tables) != 4 {
		t.Fatalf("source manifest was modified: %#v", m.Tables)
	}
	managed.TypeMappings["custom"] = TypeMapping{GoType: "Changed"}
	if m.TypeMappings["custom"].GoType != "Custom" {
		t.Fatal("managed manifest mappings alias the source")
	}
	if _, err := ManagedTables(nil); err == nil {
		t.Fatal("ManagedTables(nil) succeeded")
	}
}

func selectionManifest() *Manifest {
	column := func() []Column {
		return []Column{{
			ColumnName:      "ID",
			OrdinalPosition: 1,
			DataType:        DataTypeInteger,
		}}
	}
	m := New("MainDB", []Table{
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Vehicle",
			TableType:    "BASE TABLE",
			Columns:      column(),
		},
		{
			TableCatalog: "MainDB",
			TableSchema:  "audit",
			TableName:    "Vehicle",
			TableType:    "VIEW",
			Columns:      column(),
		},
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Customer",
			TableType:    "BASE TABLE",
			FileName:     "CustomerFile",
			PackageName:  "customerpackage",
			GoName:       "CustomerModel",
			Columns:      column(),
		},
		{
			TableCatalog: "MainDB",
			TableSchema:  "dbo",
			TableName:    "Order,Archive",
			TableType:    "BASE TABLE",
			Columns:      column(),
		},
	})
	m.TypeMappings["custom"] = TypeMapping{GoType: "Custom"}
	return m
}
