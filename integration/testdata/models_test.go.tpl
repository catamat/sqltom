package models_test

import (
 "database/sql"
 "database/sql/driver"
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "reflect"
 "strings"
 "testing"
 "time"

 _ "github.com/go-sql-driver/mysql"
 _ "github.com/jackc/pgx/v5/stdlib"
 {{ if eq . "sqlserver" }}mssql{{ else }}_{{ end }} "github.com/microsoft/go-mssqldb"
 _ "modernc.org/sqlite"

 vehicle "generated.test/models/Vehicle"
 view "generated.test/models/VehicleView"
 composite "generated.test/models/CompositeKey"
 identity "generated.test/models/IdentityOnly"
 scanner "generated.test/models/ScannerIdentity"
 pointer "generated.test/models/PointerIdentity"
 narrow "generated.test/models/NarrowIdentity"
 named "generated.test/models/NamedIdentity"
 keyless "generated.test/models/Keyless"
 values "generated.test/models/Values"
 uuidvalue "generated.test/models/UUIDValue"
 {{ if eq . "sqlite" }}descending "generated.test/models/DescendingKey"{{ else }}nonkey "generated.test/models/NonKeyIdentity"{{ end }}
 {{ if eq . "mysql" }}
 unsigned "generated.test/models/UnsignedIdentity"
 unsignedpointer "generated.test/models/UnsignedPointer"
 unsignedscanner "generated.test/models/UnsignedScanner"
 unsignednarrow "generated.test/models/UnsignedNarrow"
 {{ end }}
 uuid "github.com/google/uuid"
 googlevalue "generated.test/googlemodels/UUIDValue"
 {{ if eq . "sqlserver" }}
 "generated.test/models/query"
 nativevalue "generated.test/nativemodels/UUIDValue"
 wrappedvalue "generated.test/wrappedmodels/UUIDValue"
 {{ end }}
 {{ if eq . "sqlite" }}
 nullable "generated.test/models/NullableKey"
 nullscanner "generated.test/models/NullableScannerKey"
 nullvaluer "generated.test/models/NullableValuerKey"
 nullbinary "generated.test/models/NullableBinaryKey"
 nullcomposite "generated.test/models/NullableComposite"
 "generated.test/identitytypes"
 {{ end }}
 {{ if eq . "postgres" }}multiple "generated.test/models/MultipleIdentity"{{ end }}
)

func openDB(t *testing.T) *sql.DB {
 t.Helper()
 db, err := sql.Open(os.Getenv("SQLTOM_E2E_DRIVER"), os.Getenv("SQLTOM_E2E_DSN"))
 if err != nil { t.Fatal(err) }
 t.Cleanup(func() { db.Close() })
 if err := db.Ping(); err != nil { t.Fatal(err) }
 return db
}

func q(name string) string {
 switch os.Getenv("SQLTOM_E2E_DIALECT") {
 case "mysql": return "`"+strings.ReplaceAll(name,"`","``")+"`"
 case "sqlserver": return "["+strings.ReplaceAll(name,"]","]]")+"]"
 default: return `"`+strings.ReplaceAll(name,`"`,`""`)+`"`
 }
}
func table(name string) string { return q(os.Getenv("SQLTOM_E2E_SCHEMA"))+"."+q(name) }
func placeholder(n int) string {
 switch os.Getenv("SQLTOM_E2E_DIALECT") {
 case "postgres": return fmt.Sprintf("$%d", n)
 case "sqlserver": return fmt.Sprintf("@p%d", n)
 default: return "?"
 }
}
func where(name string) string { return "WHERE "+q(name)+" = "+placeholder(1) }
func ptr[T any](v T) *T { return &v }
func must(t *testing.T, err error) { t.Helper(); if err != nil { t.Fatal(err) } }

func TestCRUDViewsAndTransactions(t *testing.T) {
 db := openDB(t)
 row := &vehicle.Vehicle{Name:"Initial", Defaulted:ptr("explicit"), Notes:ptr("notes"), Slug:ptr("ignored")}
 must(t, row.Insert(db))
 if row.ID <= 0 { t.Fatalf("identity = %d", row.ID) }
 rows, err := vehicle.SelectAll(db, where("ID"), row.ID)
 must(t, err)
 if len(rows) != 1 || rows[0].Name != "Initial" || rows[0].Defaulted == nil || *rows[0].Defaulted != "explicit" || rows[0].Slug == nil || *rows[0].Slug != "initial" { t.Fatalf("insert round trip = %#v", rows) }
 row.Name, row.Defaulted, row.Notes = "Updated", nil, nil
 must(t, row.Update(db))
 rows, err = vehicle.SelectAll(db, where("ID"), row.ID)
 must(t, err)
 if len(rows) != 1 || rows[0].Name != "Updated" || rows[0].Defaulted != nil || rows[0].Notes != nil || rows[0].Slug == nil || *rows[0].Slug != "updated" { t.Fatalf("update round trip = %#v", rows) }
 exists, err := vehicle.Exists(db, where("ID"), row.ID)
 must(t, err)
 if !exists { t.Fatal("inserted row does not exist") }
 visible, err := view.SelectAll(db, where("ID"), row.ID)
 must(t, err)
 if len(visible) != 1 { t.Fatalf("view rows = %d",len(visible)) }
 for _, method := range []string{"Insert", "Update"} {
  if _, found := reflect.TypeOf(&view.VehicleView{}).MethodByName(method); found { t.Fatalf("view has %s", method) }
 }
 query := "SELECT "+strings.Join([]string{q("Name"),q("ID"),q("Defaulted"),q("Slug"),q("Notes")},", ")+" FROM "+table("Vehicle")+" "+where("ID")
 queried, err := vehicle.Query(db, query, row.ID)
 must(t, err)
 if !reflect.DeepEqual(rows, queried) { t.Fatalf("SelectAll and Query disagree: %#v / %#v", rows, queried) }
 // The API keeps ordinary database/sql transaction semantics.
 tx, err := db.Begin()
 must(t, err)
 transactional := &vehicle.Vehicle{Name:"RolledBack"}
 must(t, transactional.Insert(tx))
 transactional.Name = "ChangedInTransaction"
 must(t, transactional.Update(tx))
 inTx, err := vehicle.SelectAll(tx, where("ID"), transactional.ID)
 must(t, err)
 if len(inTx) != 1 || inTx[0].Name != "ChangedInTransaction" { t.Fatal("transactional update not visible") }
 must(t, vehicle.Delete(tx, where("ID"), transactional.ID))
 must(t, tx.Rollback())
 exists, err = vehicle.Exists(db, where("ID"), transactional.ID)
 must(t, err)
 if exists { t.Fatal("rolled back row persisted") }
 must(t, vehicle.Delete(db, where("ID"), row.ID))
 exists, err = vehicle.Exists(db, where("ID"), row.ID)
 must(t, err)
 if exists { t.Fatal("deleted row exists") }
 empty, err := vehicle.SelectAll(db, where("ID"), row.ID)
 must(t, err)
 if empty == nil || len(empty) != 0 { t.Fatalf("empty result = %#v",empty) }
}

// These scenarios run unchanged against all four engines.
func TestSelectAPIAndLoadedRows(t *testing.T) {
 db:=openDB(t)
 tx,err:=db.Begin();must(t,err);defer tx.Rollback()
 for _,name:=range []string{"api-first","api-middle","api-last"} {
  must(t,(&vehicle.Vehicle{Name:name,Notes:ptr("keep"),Defaulted:ptr("defaulted")}).Insert(tx))
 }
 suffix:="WHERE "+q("Name")+" LIKE "+placeholder(1)+" ORDER BY "+q("ID")
 rows,err:=vehicle.SelectAll(tx,suffix,"api-%");must(t,err)
 if len(rows)!=3 {t.Fatalf("rows=%d",len(rows))}
 first,err:=rows.First();must(t,err)
 last,err:=rows.Last();must(t,err)
 if first!=rows[0] || last!=rows[2] || first.Name!="api-first" || last.Name!="api-last" {t.Fatal("First/Last did not preserve pointers or order")}
 var ordinary []*vehicle.Vehicle=rows
 if ordinary[0]!=first {t.Fatal("conversion copied models")}
 reversed,err:=vehicle.SelectAll(tx,suffix+" DESC","api-%");must(t,err)
 reversedFirst,err:=reversed.First();must(t,err)
 if reversedFirst.ID!=last.ID {t.Fatal("First ignored SQL order")}
 queried,err:=vehicle.Query(tx,"SELECT "+strings.Join([]string{q("Name"),q("ID"),q("Defaulted"),q("Slug"),q("Notes")},", ")+" FROM "+table("Vehicle")+" "+suffix,"api-%");must(t,err)
 queryFirst,err:=queried.First();must(t,err)
 queryLast,err:=queried.Last();must(t,err)
 if queryFirst.ID!=first.ID || queryLast.ID!=last.ID {t.Fatal("Query result helpers differ")}
 singleton:=rows[1:2]
 a,err:=singleton.First();must(t,err);b,err:=singleton.Last();must(t,err)
 if a!=b || a!=rows[1] {t.Fatal("single row helpers differ")}
 unique,err:=rows.Filter(func(row *vehicle.Vehicle)bool{return row.ID==rows[1].ID}).One();must(t,err)
 if unique!=rows[1] {t.Fatal("filtered One lost the original model pointer")}
 if _,err:=queried.One();!errors.Is(err,vehicle.ErrMultipleRows) {t.Fatal("Query.One accepted multiple rows")}
 indexed,err:=queried.At(1);must(t,err)
 if indexed!=queried[1] || queried.IsEmpty() {t.Fatal("Query collection helpers disagree")}
 if one,err:=rows.One();one!=nil || !errors.Is(err,vehicle.ErrMultipleRows) {t.Fatal("One silently selected from multiple database rows")}
 for _,empty:=range []vehicle.Rows{nil,{},rows[:0]} {
  for _,pick:=range []func()(*vehicle.Vehicle,error){empty.First,empty.Last} {
   got,err:=pick();if got!=nil || !errors.Is(err,sql.ErrNoRows) {t.Fatalf("empty helper=%v, %v",got,err)}
  }
 }

 projection:=q("Notes")+", "+q("ID")
 projected,err:=vehicle.SelectCols(tx,projection,suffix,"api-%");must(t,err)
 if len(projected)!=3 {t.Fatalf("projected=%d",len(projected))}
 got,err:=projected.First();must(t,err)
 gotLast,err:=projected.Last();must(t,err)
 filtered,err:=projected.Filter(func(row *vehicle.Vehicle)bool{return row.ID==first.ID}).One();must(t,err)
 if filtered!=got {t.Fatal("Filter copied a partial model")}
 if err:=filtered.Update(nil);err==nil || !strings.Contains(err.Error(),"partially loaded") {t.Fatal("Filter lost partial-row write protection")}
 if got.ID!=first.ID || gotLast.ID!=last.ID || got.Notes==nil || *got.Notes!="keep" || got.Name!="" || got.Defaulted!=nil || got.Slug!=nil {t.Fatalf("partial fields=%#v",got)}
 // With a nil DBTX any attempted SQL execution would panic, including
 // QueryRow used by identity-returning INSERT on PostgreSQL/SQL Server.
 for _,write:=range []func(vehicle.DBTX)error{got.Insert,got.Update} {
  if err:=write(nil);err==nil || !strings.Contains(err.Error(),"partially loaded") {t.Fatalf("partial write=%v",err)}
 }
 copied:=*got
 if err:=copied.Update(nil);err==nil {t.Fatal("copy lost partial-row protection")}
 encoded,err:=json.Marshal(got);must(t,err)
 if strings.Contains(string(encoded),"sqltomPartial") {t.Fatal("private projection flag leaked to JSON")}
 stored,err:=vehicle.SelectAll(tx,where("ID"),first.ID);must(t,err)
 if stored[0].Name!=first.Name || stored[0].Notes==nil || *stored[0].Notes!="keep" {t.Fatal("partial update damaged stored values")}
 // Selecting every column, even in a different order, produces a writable row.
 full,err:=vehicle.SelectCols(tx,q("Notes")+", "+q("Slug")+", "+q("Defaulted")+", "+q("ID")+", "+q("Name"),where("ID"),first.ID);must(t,err)
 if len(full)!=1 || !reflect.DeepEqual(full[0],stored[0]) {t.Fatal("reordered full projection differs")}
 full[0].Name="api-updated";must(t,full[0].Update(tx))
 updated,err:=vehicle.SelectAll(tx,where("ID"),first.ID);must(t,err)
 if updated[0].Name!="api-updated" || updated[0].Notes==nil || *updated[0].Notes!="keep" {t.Fatal("full projection Update failed")}
 viewRows,err:=view.SelectCols(tx,q("Name"),where("ID"),first.ID);must(t,err)
 visible,err:=viewRows.First();must(t,err)
 visibleName,err:=driver.DefaultParameterConverter.ConvertValue(visible.Name);must(t,err)
 if visibleName!="api-updated" || !reflect.ValueOf(visible.ID).IsZero() {t.Fatal("view projection is incorrect")}
 empty,err:=vehicle.SelectCols(tx,q("Name"),"WHERE 1 = 0");must(t,err)
 if empty==nil || len(empty)!=0 {t.Fatal("empty SelectCols must return a non-nil empty slice")}
 if _,err:=empty.First();!errors.Is(err,sql.ErrNoRows) {t.Fatal("empty SelectCols First missing ErrNoRows")}
 emptyAll,err:=vehicle.SelectAll(tx,"WHERE 1 = 0");must(t,err)
 if _,err:=emptyAll.Last();!errors.Is(err,sql.ErrNoRows) {t.Fatal("empty SelectAll Last missing ErrNoRows")}

}

// Collection semantics are identical in the independently compiled models
// for every dialect, and work without a database connection.
func TestRowsOneAtAndIsEmpty(t *testing.T) {
 first:=&vehicle.Vehicle{ID:11,Name:"first"}
 second:=&vehicle.Vehicle{ID:22,Name:"second"}
 for _,empty:=range []vehicle.Rows{nil,{}, vehicle.Rows{first}[:0]} {
  if !empty.IsEmpty() {t.Fatal("empty collection reports non-empty")}
  got,err:=empty.One()
  if got!=nil || !errors.Is(err,sql.ErrNoRows) {t.Fatalf("empty One=%v, %v",got,err)}
  for _,index:=range []int{-1,0,1} {
   got,err:=empty.At(index)
   if got!=nil || !errors.Is(err,vehicle.ErrIndexOutOfRange) {t.Fatalf("empty At(%d)=%v, %v",index,got,err)}
  }
 }
 singleton:=vehicle.Rows{first}
 only,err:=singleton.One();must(t,err)
 if only!=first || singleton.IsEmpty() {t.Fatal("One must return the existing pointer")}
 for _,rows:=range []vehicle.Rows{ {first,second}, {first,first}} {
  got,err:=rows.One()
  if got!=nil || !errors.Is(err,vehicle.ErrMultipleRows) || errors.Is(err,sql.ErrNoRows) {t.Fatalf("multiple One=%v, %v",got,err)}
  got,err=rows.First();must(t,err)
  if got!=first {t.Fatal("First should accept multiple rows")}
  if rows.IsEmpty() {t.Fatal("non-empty collection reports empty")}
 }
 rows:=vehicle.Rows{first,second}
 for index,want:=range rows {
  got,err:=rows.At(index);must(t,err)
  if got!=want {t.Fatalf("At(%d) copied or selected the wrong model",index)}
 }
 maxInt:=int(^uint(0)>>1)
 for _,index:=range []int{-1,len(rows),len(rows)+1,maxInt,-maxInt-1} {
  got,err:=rows.At(index)
  if got!=nil || !errors.Is(err,vehicle.ErrIndexOutOfRange) || errors.Is(err,sql.ErrNoRows) {t.Fatalf("invalid At(%d)=%v, %v",index,got,err)}
 }
}

func TestRowsFilter(t *testing.T) {
 first:=&vehicle.Vehicle{ID:1,Name:"first"}
 second:=&vehicle.Vehicle{ID:2,Name:"second"}
 third:=&vehicle.Vehicle{ID:3,Name:"third"}
 rows:=vehicle.Rows{first,second,third}
 var visited vehicle.Rows
 filtered:=rows.Filter(func(row *vehicle.Vehicle)bool{visited=append(visited,row);return row.ID!=2})
 if !reflect.DeepEqual(visited,rows) || len(filtered)!=2 || filtered[0]!=first || filtered[1]!=third {t.Fatal("Filter changed visitation order or model pointers")}
 // A new backing array isolates slice edits, but the models stay shared.
 filtered[0].Name="shared"
 if rows[0].Name!="shared" {t.Fatal("Filter copied the model")}
 filtered[0]=second
 filtered=append(filtered,second)
 if len(rows)!=3 || rows[0]!=first || rows[1]!=second || rows[2]!=third {t.Fatal("Filter overwrote the input backing array")}
 all:=rows.Filter(func(*vehicle.Vehicle)bool{return true})
 all[0]=third
 if rows[0]!=first {t.Fatal("all-match Filter reused the input backing array")}
 none:=rows.Filter(func(*vehicle.Vehicle)bool{return false})
 if none==nil || !none.IsEmpty() {t.Fatal("no-match Filter must return a non-nil empty Rows")}
 if got,err:=none.One();got!=nil || !errors.Is(err,sql.ErrNoRows) {t.Fatal("empty Filter.One missing ErrNoRows")}
 for _,empty:=range []vehicle.Rows{nil,{}} {
  calls:=0
  got:=empty.Filter(func(*vehicle.Vehicle)bool{calls++;return true})
  if got==nil || !got.IsEmpty() || calls!=0 {t.Fatal("Filter on empty rows invoked the predicate or returned nil")}
 }
 // Filter evaluates every element, including duplicate or manually added nil pointers.
 mixed:=vehicle.Rows{first,nil,first,second}
 calls:=0
 selected:=mixed.Filter(func(row *vehicle.Vehicle)bool{calls++;return row==nil || row==first})
 if calls!=4 || len(selected)!=3 || selected[0]!=first || selected[1]!=nil || selected[2]!=first {t.Fatal("Filter removed duplicates or skipped nil elements")}
 chained,err:=rows.Filter(func(row *vehicle.Vehicle)bool{return row.ID>1}).Filter(func(row *vehicle.Vehicle)bool{return row.ID<3}).One();must(t,err)
 if chained!=second {t.Fatal("chained Filter.One selected the wrong model")}
 for _,input:=range []vehicle.Rows{nil,{},rows} {
  func(){
   defer func(){if recover()==nil {t.Error("nil Filter predicate did not panic")}}()
   input.Filter(nil)
  }()
 }
}

func TestCompositeKeyOrderAndIsolation(t *testing.T) {
 db := openDB(t)
 records := []*composite.CompositeKey{
  {TenantID:7,ItemID:11,Payload:ptr("first")},
  {TenantID:7,ItemID:12,Payload:ptr("second")},
  {TenantID:8,ItemID:11,Payload:ptr("third")},
 }
 for _, row := range records { must(t, row.Insert(db)) }
 if err := records[0].Insert(db); err == nil { t.Fatal("duplicate primary key was accepted") }
 records[0].Payload = ptr("updated")
 must(t, records[0].Update(db))
 rows, err := composite.SelectAll(db, "ORDER BY "+q("TenantID")+", "+q("ItemID"))
 must(t, err)
 if !reflect.DeepEqual([]*composite.CompositeKey(rows), records) { t.Fatalf("composite update changed the wrong row: %#v", rows) }
}

func TestIdentityConversions(t *testing.T) {
 db := openDB(t)
 row := &identity.IdentityOnly{}
 must(t, row.Insert(db))
 if row.ID <= 0 { t.Fatal("empty insert did not assign identity") }
 scanned := &scanner.ScannerIdentity{Name:"scanner"}
 must(t, scanned.Insert(db))
 if !scanned.ID.Valid || scanned.ID.Int64 <= 0 { t.Fatalf("Scanner identity = %#v",scanned.ID) }
 scanned.Name = "updated scanner"
 must(t, scanned.Update(db))
 scannedRows, err := scanner.SelectAll(db, where("ID"), scanned.ID)
 must(t, err)
 if len(scannedRows) != 1 || !reflect.DeepEqual(scannedRows[0],scanned) { t.Fatalf("Scanner round trip = %#v", scannedRows) }
 pointed := &pointer.PointerIdentity{Name:"pointer"}
 must(t, pointed.Insert(db))
 if pointed.ID == nil || *pointed.ID <= 0 { t.Fatalf("pointer identity = %#v", pointed.ID) }
 pointed.Name = "updated pointer"
 must(t, pointed.Update(db))
 pointedRows, err := pointer.SelectAll(db, where("ID"), pointed.ID)
 must(t, err)
 if len(pointedRows) != 1 || !reflect.DeepEqual(pointedRows[0],pointed) { t.Fatalf("pointer round trip = %#v", pointedRows) }
 namedRow := &named.NamedIdentity{Name:"named integer"}
 must(t,namedRow.Insert(db))
 if namedRow.ID <= 0 { t.Fatalf("named identity = %d",namedRow.ID) }
 namedRow.Name = "updated named integer"
 must(t,namedRow.Update(db))
 namedRows,err := named.SelectAll(db,where("ID"),namedRow.ID)
 must(t,err)
 if len(namedRows)!=1 || !reflect.DeepEqual(namedRows[0],namedRow) { t.Fatalf("named identity round trip = %#v",namedRows) }
 // The SQL insert succeeds but its returned identity cannot fit in int8.
 // The caller can roll back instead of silently accepting a wrapped key.
 tx, err := db.Begin()
 must(t, err)
 defer tx.Rollback()
 overflow := &narrow.NarrowIdentity{}
 if err := overflow.Insert(tx); err == nil { t.Fatal("identity overflow was not reported") }
 if overflow.ID != 0 { t.Fatalf("identity silently truncated to %d",overflow.ID) }
 must(t, tx.Rollback())
}

func TestLogicalTypeRoundTrips(t *testing.T) {
 db := openDB(t)
 stamp := time.Date(2026, 3, 12, 13, 14, 15, 0, time.UTC)
 day := time.Date(2026, 3, 12, 0, 0, 0, 0, time.UTC)
 row := &values.Values{Clock:"13:14:15.123456", JSONValue:json.RawMessage(`{"name":"caffè","n":42}`), Flag:true,
  BinaryValue:ptr([]byte{0,1,127,255}), Day:&day, Stamp:&stamp, Amount:ptr(123.45)}
 must(t, row.Insert(db))
 check := func() *values.Values {
  t.Helper()
  rows, err := values.SelectAll(db, where("ID"), row.ID)
  must(t, err)
  if len(rows)!=1 { t.Fatalf("value rows = %d",len(rows)) }
  got := rows[0]
  projected,err := values.SelectCols(db,q("OptionalFlag")+", "+q("JSONValue")+", "+q("Clock")+", "+q("Flag")+", "+q("OptionalClock")+", "+q("OptionalJSON"),where("ID"),row.ID)
  must(t,err)
  if len(projected)!=1 || projected[0].Clock!=got.Clock || projected[0].Flag!=got.Flag || !reflect.DeepEqual(projected[0].JSONValue,got.JSONValue) || !reflect.DeepEqual(projected[0].OptionalClock,got.OptionalClock) || !reflect.DeepEqual(projected[0].OptionalFlag,got.OptionalFlag) || !reflect.DeepEqual(projected[0].OptionalJSON,got.OptionalJSON) { t.Fatal("projected logical codecs disagree with SelectAll") }
  if got.Clock != row.Clock || got.Flag != row.Flag || !reflect.DeepEqual(got.BinaryValue,row.BinaryValue) { t.Fatalf("logical values = %#v",got) }
  var a,b any
  must(t,json.Unmarshal(row.JSONValue,&a)); must(t,json.Unmarshal(got.JSONValue,&b))
  if !reflect.DeepEqual(a,b) { t.Fatalf("JSON = %s",got.JSONValue) }
  if got.Day == nil || got.Day.Format("2006-01-02") != "2026-03-12" || got.Stamp == nil || !got.Stamp.Equal(stamp) || got.Amount == nil || *got.Amount != 123.45 { t.Fatalf("date/binary/decimal values = %#v",got) }
  return got
 }
 got := check()
 if got.OptionalClock != nil || got.OptionalJSON != nil || got.OptionalFlag != nil { t.Fatalf("NULL values = %#v",got) }
 row.Clock, row.Flag = "01:02:03.654321", false
 row.JSONValue = json.RawMessage(`[1,true,"updated"]`)
 row.OptionalClock, row.OptionalJSON, row.OptionalFlag = ptr("23:59:58.123456"), ptr(json.RawMessage(`{"nullable":true}`)), ptr(false)
 must(t,row.Update(db))
 got = check()
 if got.OptionalClock == nil || *got.OptionalClock != *row.OptionalClock || got.OptionalFlag == nil || *got.OptionalFlag || got.OptionalJSON == nil || !json.Valid(*got.OptionalJSON) { t.Fatalf("nullable round trip = %#v",got) }
 row.OptionalClock, row.OptionalJSON, row.OptionalFlag = nil,nil,nil
 must(t,row.Update(db))
 got = check()
 if got.OptionalClock != nil || got.OptionalJSON != nil || got.OptionalFlag != nil { t.Fatal("nullable values did not reset to NULL") }
 t.Run("extended MySQL duration",func(t *testing.T) {
  if os.Getenv("SQLTOM_E2E_DIALECT")!="mysql" { t.Skip("negative TIME and hours > 24 are MySQL duration semantics") }
  row.Clock = "-30:15:12.123456"
  must(t,row.Update(db))
  check()
 })
}

func TestKeylessTablesDoNotInventAnUpdateKey(t *testing.T) {
 db := openDB(t)
 if _, found := reflect.TypeOf(&keyless.Keyless{}).MethodByName("Update"); found { t.Fatal("keyless table exposes Update") }
 for _, name := range []string{"first","second"} { must(t, (&keyless.Keyless{RecordID:7,Name:name}).Insert(db)) }
 rows, err := keyless.SelectAll(db, where("RecordID"), 7)
 must(t,err)
 if len(rows)!=2 { t.Fatalf("nonunique identifier has %d rows",len(rows)) }
 must(t,keyless.Delete(db,where("Name"),"first"))
 exists,err := keyless.Exists(db,where("Name"),"second")
 must(t,err)
 if !exists { t.Fatal("delete affected an unrelated row") }
}

func TestDialectSpecificIdentity(t *testing.T) {
 db := openDB(t)
 {{ if eq . "sqlite" }}
 t.Run("descending primary key",func(t *testing.T) {
  row := &descending.DescendingKey{ID:ptr(42),Name:"explicit"}
  must(t,row.Insert(db))
  if row.ID == nil || *row.ID != 42 { t.Fatalf("explicit DESC key replaced by rowid: %#v",row.ID) }
  row.Name = "updated"
  must(t,row.Update(db))
  rows,err := descending.SelectAll(db,where("ID"),42)
  must(t,err)
  if len(rows)!=1 || rows[0].Name!="updated" { t.Fatal("descending key update failed") }
 })
 t.Run("non-key identity",func(t *testing.T) { t.Skip("SQLite rowid identity is necessarily the primary key") })
 {{ else }}
 t.Run("non-key identity",func(t *testing.T) {
  row := &nonkey.NonKeyIdentity{Key:42,Name:"initial"}
  must(t,row.Insert(db))
  if row.Key!=42 || row.Sequence<=0 { t.Fatalf("non-key identity = %#v",row) }
  row.Name="updated"
  must(t,row.Update(db))
  rows,err := nonkey.SelectAll(db,where("Key"),42)
  must(t,err)
  if len(rows)!=1 || !reflect.DeepEqual(rows[0],row) { t.Fatalf("non-key identity round trip = %#v",rows) }
 })
 {{ end }}
 t.Run("multiple identities",func(t *testing.T) {
  {{ if eq . "postgres" }}
  row := &multiple.MultipleIdentity{Name:"multiple"}
  must(t,row.Insert(db))
  if row.ID<=0 || row.Sequence<=0 { t.Fatalf("multiple identities = %#v",row) }
  {{ else }}t.Skip("this dialect supports at most one identity per table"){{ end }}
 })
}

func TestGeneratedMethodsPropagateErrors(t *testing.T) {
 db := openDB(t)
 if _,err := vehicle.Query(db,"SELECT 1"); err==nil { t.Fatal("scan error was lost") }
 if _,err := vehicle.SelectAll(db,"WHERE missing_sqltom_column = 1"); err==nil { t.Fatal("query error was lost") }
 if _,err := vehicle.SelectCols(db,q("Name"),"WHERE missing_sqltom_column = 1"); err==nil { t.Fatal("SelectCols query error was lost") }
 if _,err := vehicle.Exists(db,"WHERE missing_sqltom_column = 1"); err==nil { t.Fatal("Exists error was lost") }
 if err := vehicle.Delete(db,"WHERE missing_sqltom_column = 1"); err==nil { t.Fatal("Delete error was lost") }
 must(t,db.Close())
 row:=&vehicle.Vehicle{Name:"closed"}
 if row.Insert(db)==nil || row.Update(db)==nil { t.Fatal("closed database write error was lost") }
}

var iterationFailure = errors.New("iteration failure")
var resultFailure = errors.New("LastInsertId failure")
var rowsClosed bool
type failureDriver struct{}
type failureConn struct{}
type failureRows struct{}
func (failureDriver) Open(string) (driver.Conn,error) { return failureConn{},nil }
func (failureConn) Prepare(string) (driver.Stmt,error) { return nil,errors.New("unsupported") }
func (failureConn) Close() error { return nil }
func (failureConn) Begin() (driver.Tx,error) { return nil,errors.New("unsupported") }
func (failureConn) Query(string,[]driver.Value) (driver.Rows,error) { return failureRows{},nil }
func (failureRows) Columns() []string { return []string{"Name","ID","Defaulted","Slug","Notes"} }
func (failureRows) Close() error { rowsClosed=true; return nil }
func (failureRows) Next([]driver.Value) error { return iterationFailure }
type failedResult struct{}
func (failedResult) LastInsertId() (int64,error) { return 0,resultFailure }
func (failedResult) RowsAffected() (int64,error) { return 1,nil }
type resultDB struct { *sql.DB }
func (resultDB) Exec(string,...interface{}) (sql.Result,error) { return failedResult{},nil }

func TestGeneratedMethodsPropagateDriverFailures(t *testing.T) {
 sql.Register("generated-failures",failureDriver{})
 db,err := sql.Open("generated-failures","")
 must(t,err)
 defer db.Close()
 for _,query := range []func() error{
  func() error { _,err:=vehicle.SelectAll(db,""); return err },
  func() error { _,err:=vehicle.Query(db,"SELECT anything"); return err },
  func() error { _,err:=vehicle.SelectCols(db,q("Name"),""); return err },
 } {
  rowsClosed=false
  if err:=query(); !errors.Is(err,iterationFailure) { t.Fatalf("iteration error = %v",err) }
  if !rowsClosed { t.Fatal("rows were not closed after an iteration error") }
 }
 t.Run("LastInsertId",func(t *testing.T) {
  if os.Getenv("SQLTOM_E2E_DIALECT")!="sqlite" && os.Getenv("SQLTOM_E2E_DIALECT")!="mysql" { t.Skip("identity uses a returned SQL row in this dialect") }
  if err:=(&vehicle.Vehicle{}).Insert(resultDB{db}); !errors.Is(err,resultFailure) { t.Fatalf("LastInsertId error = %v",err) }
 })
}

func TestUnsignedIdentityRange(t *testing.T) {
 {{ if eq . "mysql" }}
 db:=openDB(t)
 for _,start:=range []uint64{1<<63-2,1<<63-1,1<<63,1<<64-2} {
  t.Run(fmt.Sprint(start),func(t *testing.T){
   _,err:=db.Exec("TRUNCATE TABLE "+table("UnsignedIdentity"));must(t,err)
   _,err=db.Exec(fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT=%d",table("UnsignedIdentity"),start));must(t,err)
   row:=&unsigned.UnsignedIdentity{}
   must(t,row.Insert(db))
   if row.ID!=start {t.Fatalf("identity=%d, want %d",row.ID,start)}
   rows,err:=unsigned.SelectAll(db,where("ID"),row.ID);must(t,err)
   if len(rows)!=1 || rows[0].ID!=start {t.Fatalf("read after insert=%#v",rows)}
  })
 }
 for _,name:=range []string{"UnsignedPointer","UnsignedScanner","UnsignedNarrow"} {
  _,err:=db.Exec(fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT=9223372036854775808",table(name)));must(t,err)
 }
 pointerRow:=&unsignedpointer.UnsignedPointer{}
 must(t,pointerRow.Insert(db))
 if pointerRow.ID==nil || *pointerRow.ID!=uint64(1<<63) {t.Fatalf("pointer identity=%v",pointerRow.ID)}
 scannerRow:=&unsignedscanner.UnsignedScanner{}
 must(t,scannerRow.Insert(db))
 if scannerRow.ID.N!=uint64(1<<63) {t.Fatalf("scanner identity=%#v",scannerRow.ID)}
 tx,err:=db.Begin();must(t,err);defer tx.Rollback()
 narrowRow:=&unsignednarrow.UnsignedNarrow{}
 if err:=narrowRow.Insert(tx);err==nil {t.Fatal("unsigned identity silently overflowed int64 override")}
 must(t,tx.Rollback())
 var count int
 must(t,db.QueryRow("SELECT COUNT(*) FROM "+table("UnsignedNarrow")).Scan(&count))
 if count!=0 {t.Fatalf("failed identity transaction left %d rows",count)}
 {{ else }}t.Skip("unsigned auto-increment identities are specific to MySQL"){{ end }}
}

func TestNullablePrimaryKeyUpdate(t *testing.T) {
 {{ if eq . "sqlite" }}
 db:=openDB(t)
 row:=&nullable.NullableKey{Name:"first"};must(t,row.Insert(db))
 must(t,(&nullable.NullableKey{Name:"second"}).Insert(db))
 row.Name="changed"
 if err:=row.Update(db);err==nil || !strings.Contains(err.Error(),"primary key") {t.Fatalf("NULL pointer key update=%v",err)}
 rows,err:=nullable.SelectAll(db,"");must(t,err)
 if len(rows)!=2 || rows[0].Name!="first" || rows[1].Name!="second" {t.Fatalf("NULL keys changed rows: %#v",rows)}
 row.ID=ptr(7);must(t,row.Insert(db));row.Name="updated";must(t,row.Update(db))
 rows,err=nullable.SelectAll(db,where("ID"),7);must(t,err)
 if len(rows)!=1 || rows[0].Name!="updated" {t.Fatalf("non-NULL key update=%#v",rows)}
 // A DBTX whose Exec fails proves NULL keys are rejected before SQL execution.
 reject:=&rejectExec{}
 tests:=[]struct{name string; update func()error}{
  {"pointer",func()error{return (&nullable.NullableKey{}).Update(reject)}},
  {"scanner",func()error{return (&nullscanner.NullableScannerKey{}).Update(reject)}},
  {"valuer",func()error{return (&nullvaluer.NullableValuerKey{}).Update(reject)}},
  {"nil binary pointer",func()error{return (&nullbinary.NullableBinaryKey{}).Update(reject)}},
  {"nil bytes",func()error{return (&nullbinary.NullableBinaryKey{ID:ptr([]byte(nil))}).Update(reject)}},
  {"composite first",func()error{return (&nullcomposite.NullableComposite{Second:ptr("x")}).Update(reject)}},
  {"composite second",func()error{return (&nullcomposite.NullableComposite{First:ptr(1)}).Update(reject)}},
 }
 for _,test:=range tests {t.Run(test.name,func(t *testing.T){if err:=test.update();err==nil || !strings.Contains(err.Error(),"is NULL") {t.Fatalf("Update=%v",err)}})}
 if reject.calls!=0 {t.Fatalf("NULL keys executed %d queries",reject.calls)}
 binaryRow:=&nullbinary.NullableBinaryKey{ID:ptr([]byte{}),Name:"empty binary is not NULL"}
 must(t,binaryRow.Insert(db));must(t,binaryRow.Update(db))
 binaryRows,err:=nullbinary.SelectAll(db,where("ID"),[]byte{});must(t,err)
 if len(binaryRows)!=1 || binaryRows[0].ID==nil || *binaryRows[0].ID==nil || len(*binaryRows[0].ID)!=0 {t.Fatal("empty binary key was read as NULL")}
 binaryRows[0].Name="updated after reading empty key";must(t,binaryRows[0].Update(db))
 binaryRows,err=nullbinary.SelectCols(db,"*",where("ID"),[]byte{});must(t,err)
 if len(binaryRows)!=1 || binaryRows[0].Name!="updated after reading empty key" {t.Fatal("read/modify/write lost the empty binary key")}
 scannerRow:=&nullscanner.NullableScannerKey{ID:sql.NullInt64{Int64:8,Valid:true},Name:"scanner"}
 must(t,scannerRow.Insert(db));must(t,scannerRow.Update(db))
 compositeRow:=&nullcomposite.NullableComposite{First:ptr(1),Second:ptr("x"),Name:"composite"}
 must(t,compositeRow.Insert(db));must(t,compositeRow.Update(db))
 calls:=0
 valuerRow:=&nullvaluer.NullableValuerKey{ID:identitytypes.Key{N:9,Valid:true,Calls:&calls},Name:"valuer"}
 must(t,valuerRow.Insert(db));calls=0;must(t,valuerRow.Update(db))
 if calls!=1 {t.Fatalf("Valuer called %d times, want 1",calls)}
 sentinel:=errors.New("key conversion failure")
 valuerRow.ID.Err=sentinel
 if err:=valuerRow.Update(reject);!errors.Is(err,sentinel) {t.Fatalf("valuer error=%v",err)}
 if reject.calls!=0 {t.Fatal("conversion error executed SQL")}
 {{ else }}t.Skip("nullable primary keys are specific to SQLite in this contract"){{ end }}
}

{{ if eq . "sqlite" }}
type rejectExec struct { *sql.DB; calls int }
func(db *rejectExec) Exec(string,...any)(sql.Result,error){db.calls++;return nil,errors.New("unexpected Exec")}
{{ end }}

func TestSelectColsUUIDProjection(t *testing.T) {
 db:=openDB(t)
 tx,err:=db.Begin();must(t,err);defer tx.Rollback()
 const canonical="00112233-4455-6677-8899-aabbccddeeff"
 id:=canonical
 optional:="10213243-5465-7687-98a9-bacbdcedfe0f"
 zero:="00000000-0000-0000-0000-000000000000"
 row:=&uuidvalue.UUIDValue{ID:id,Name:"projection"};must(t,row.Insert(tx))
 for _,value:=range []*string{nil,&optional,&zero} {
  row.OptionalID=value;must(t,row.Update(tx))
  want,err:=uuidvalue.SelectAll(tx,where("ID"),id);must(t,err)
  full,err:=uuidvalue.SelectCols(tx,"*",where("ID"),id);must(t,err)
  if len(full)!=1 || !reflect.DeepEqual(full,want) || !reflect.DeepEqual(full[0],row) {t.Fatal("native UUID projection differs from SelectAll")}
  partial,err:=uuidvalue.SelectCols(tx,q("OptionalID")+", "+q("ID"),where("ID"),id);must(t,err)
  if len(partial)!=1 || partial[0].ID!=id || !reflect.DeepEqual(partial[0].OptionalID,value) || partial[0].Name!="" {t.Fatal("partial UUID projection differs")}
  if err:=partial[0].Update(nil);err==nil || !strings.Contains(err.Error(),"partially loaded") {t.Fatal("UUID projection lost partial protection")}
 }
}

func TestNativeUUIDCodec(t *testing.T) {
 {{ if eq . "sqlserver" }}
 db:=openDB(t)
 const canonical="00112233-4455-6677-8899-aabbccddeeff"
 var id, optional, zero mssql.UniqueIdentifier
 must(t,id.Scan(canonical));must(t,optional.Scan("10213243-5465-7687-98a9-bacbdcedfe0f"))
 row:=&nativevalue.UUIDValue{ID:id,Name:"original"};must(t,row.Insert(db))
 assertStoredUUID(t,db,canonical)
 for i, value:=range []*mssql.UniqueIdentifier{nil,&optional,&zero,nil} {
  row.Name=fmt.Sprint("native-",i);row.OptionalID=value;must(t,row.Update(db))
  selected,err:=nativevalue.SelectAll(db,where("ID"),id);must(t,err)
  queried,err:=nativevalue.Query(db,uuidQuery(false),id);must(t,err)
  projected,err:=nativevalue.SelectCols(db,q("OptionalID")+", "+q("ID"),where("ID"),id);must(t,err)
  if len(projected)!=1 || projected[0].ID!=row.ID || !reflect.DeepEqual(projected[0].OptionalID,row.OptionalID) || projected[0].Name!="" {t.Fatal("projected UUID codec disagrees with SelectAll")}
  if len(selected)!=1 || !reflect.DeepEqual(selected,queried) || !reflect.DeepEqual(selected[0],row) {t.Fatalf("UUID SelectAll/Query round trip disagrees: %#v / %#v",selected,queried)}
 }
 must(t,nativevalue.Delete(db,where("ID"),id))
 exists,err:=nativevalue.Exists(db,where("ID"),id);must(t,err)
 if exists {t.Fatal("UUID key delete failed")}
 {{ else }}t.Skip("SQL Server native UUID codec; other dialects do not use its mixed-endian wire format"){{ end }}
}

func TestGoogleUUIDMappingWithoutCasts(t *testing.T) {
 db:=openDB(t)
 optional:=uuid.MustParse("10213243-5465-7687-98a9-bacbdcedfe0f")
 zero:=uuid.Nil
 for _,id:=range []uuid.UUID{uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff"),uuid.Nil} {
  row:=&googlevalue.UUIDValue{ID:id,Name:"google",OptionalID:&optional};must(t,row.Insert(db))
  assertStoredUUID(t,db,id.String())
  for i,value:=range []*uuid.UUID{&optional,&zero,nil} {
   row.Name=fmt.Sprint("google-",i);row.OptionalID=value;must(t,row.Update(db))
   selected,err:=googlevalue.SelectAll(db,where("ID"),id);must(t,err)
   queried,err:=googlevalue.Query(db,uuidQuery(false),id);must(t,err)
   projected,err:=googlevalue.SelectCols(db,q("OptionalID")+", "+q("ID"),where("ID"),id);must(t,err)
   if len(projected)!=1 || projected[0].ID!=row.ID || !reflect.DeepEqual(projected[0].OptionalID,row.OptionalID) || projected[0].Name!="" {t.Fatal("projected UUID codec disagrees with SelectAll")}
   if len(selected)!=1 || !reflect.DeepEqual(selected,queried) || !reflect.DeepEqual(selected[0],row) {t.Fatalf("Google UUID round trip disagrees: %#v / %#v",selected,queried)}
   // Existing textual projections must also remain valid, without decoding twice.
   textual,err:=googlevalue.Query(db,uuidQuery(true),id);must(t,err)
   if !reflect.DeepEqual(selected,textual) {t.Fatal("text UUID projection changed the value")}
  }
  exists,err:=googlevalue.Exists(db,where("ID"),id);must(t,err)
  if !exists {t.Fatal("UUID lookup failed")}
  must(t,googlevalue.Delete(db,where("ID"),id))
  exists,err=googlevalue.Exists(db,where("ID"),id);must(t,err)
  if exists {t.Fatal("Google UUID key delete failed")}
 }
}

func TestQueryBuilderUUIDParameters(t *testing.T) {
 {{ if eq . "sqlserver" }}
 db := openDB(t)
 tx, err := db.Begin()
 must(t, err)
 defer tx.Rollback()
 id := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
 var native, zero mssql.UniqueIdentifier
 must(t, native.Scan(id.String()))
 row := &googlevalue.UUIDValue{ID: id, Name: "builder-uuid"}
 must(t, row.Insert(tx))
 for _, test := range []struct{ value any; count int }{
  {id, 1}, {native, 1},
  {[]uuid.UUID{id, uuid.Nil}, 2}, {[]mssql.UniqueIdentifier{native, zero}, 2},
 } {
  builder := query.New().Write("WHERE " + q("ID") + " IN (:ids) OR " + q("ID") + " IN (:ids)").Bind("ids", test.value)
  stmt, args, err := builder.Build()
  must(t, err)
  if len(args) != test.count { t.Fatalf("UUID parameter count=%d, want %d", len(args), test.count) }
  googleRows, err := googlevalue.SelectAll(tx, stmt, args...)
  must(t, err)
  nativeRows, err := nativevalue.SelectAll(tx, stmt, args...)
  must(t, err)
  if len(googleRows) != 1 || googleRows[0].ID != id || len(nativeRows) != 1 || nativeRows[0].ID != native {
   t.Fatalf("UUID binding failed for %T", test.value)
  }
 }
 {{ else }}t.Skip("native UUID fixture and codec mappings are specific to SQL Server"){{ end }}
}

func TestNullableUUIDWrapperAndColumnOverride(t *testing.T) {
 {{ if eq . "sqlserver" }}
 db:=openDB(t)
 var id mssql.UniqueIdentifier;must(t,id.Scan("00112233-4455-6677-8899-aabbccddeeff"))
 row:=&wrappedvalue.UUIDValue{ID:id,Name:"column override"};must(t,row.Insert(db))
 for _,value:=range []uuid.NullUUID{ {}, {UUID:uuid.MustParse("10213243-5465-7687-98a9-bacbdcedfe0f"),Valid:true},{UUID:uuid.Nil,Valid:true},{}} {
  row.OptionalID=value;must(t,row.Update(db))
  selected,err:=wrappedvalue.SelectAll(db,where("ID"),id);must(t,err)
  queried,err:=wrappedvalue.Query(db,uuidQuery(false),id);must(t,err)
  projected,err:=wrappedvalue.SelectCols(db,q("OptionalID")+", "+q("ID"),where("ID"),id);must(t,err)
  if len(projected)!=1 || projected[0].ID!=row.ID || !reflect.DeepEqual(projected[0].OptionalID,row.OptionalID) || projected[0].Name!="" {t.Fatal("projected UUID codec disagrees with SelectAll")}
  if len(selected)!=1 || !reflect.DeepEqual(selected,queried) || !reflect.DeepEqual(selected[0],row) {t.Fatal("nullable Scanner or column override changed UUID")}
 }
 must(t,wrappedvalue.Delete(db,where("ID"),id))
 for _,bad:=range []string{"'bad-uuid'","0x0102","42"} {
  if _,err:=googlevalue.Query(db,"SELECT "+bad+", N'bad', NULL");err==nil {t.Fatalf("accepted invalid UUID %s",bad)}
 }
 {{ else }}t.Skip("SQL Server adapter with nullable UUID Scanner and per-column override"){{ end }}
}

func uuidQuery(textual bool) string {
 id,optional:=q("ID"),q("OptionalID")
 if textual {id="CAST("+id+" AS CHAR(36))";optional="CAST("+optional+" AS CHAR(36))"}
 return "SELECT "+id+","+q("Name")+","+optional+" FROM "+table("UUIDValue")+" "+where("ID")
}
func assertStoredUUID(t *testing.T, db *sql.DB, canonical string) {
 t.Helper()
 var count int
 must(t,db.QueryRow("SELECT COUNT(*) FROM "+table("UUIDValue")+" "+where("ID"),canonical).Scan(&count))
 if count!=1 {t.Fatalf("database does not contain canonical UUID %s",canonical)}
}
