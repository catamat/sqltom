package UUIDValue

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	mssql "github.com/microsoft/go-mssqldb"
)

func TestUUIDAdapterInputRepresentations(t *testing.T) {
	const canonical = "00112233-4455-6677-8899-aabbccddeeff"
	expected := uuid.MustParse(canonical)
	raw := []byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	original := bytes.Clone(raw)
	for _, input := range []any{raw, canonical, strings.ToUpper(canonical), []byte(canonical)} {
		var text string
		if err := (sqltomScanValue{destination: &text, kind: "uuid"}).Scan(input); err != nil {
			t.Fatal(err)
		}
		if text != canonical {
			t.Fatalf("string value = %q, want %q", text, canonical)
		}
		var native mssql.UniqueIdentifier
		if err := (sqltomScanValue{destination: &native, kind: "uuid"}).Scan(input); err != nil {
			t.Fatal(err)
		}
		if uuid.UUID(native) != expected {
			t.Fatalf("native value = %s", native)
		}
		var google uuid.UUID
		if err := (sqltomScanValue{destination: &google, kind: "uuid"}).Scan(input); err != nil {
			t.Fatal(err)
		}
		if google != expected {
			t.Fatalf("Google value = %s", google)
		}
	}
	if !bytes.Equal(raw, original) {
		t.Fatal("adapter modified the driver's row buffer")
	}
}

func TestUUIDAdapterNullAndZero(t *testing.T) {
	var text *string
	var native *mssql.UniqueIdentifier
	var google *uuid.UUID
	var nullable uuid.NullUUID
	for _, destination := range []any{&text, &native, &google, &nullable} {
		scanner := sqltomScanValue{destination: destination, kind: "uuid"}
		if err := scanner.Scan(make([]byte, 16)); err != nil {
			t.Fatal(err)
		}
	}
	if native == nil || google == nil || !nullable.Valid || uuid.UUID(*native) != uuid.Nil || *google != uuid.Nil || nullable.UUID != uuid.Nil {
		t.Fatal("zero UUID became NULL")
	}
	if text == nil || *text != "00000000-0000-0000-0000-000000000000" {
		t.Fatal("zero UUID string became NULL or empty")
	}
	for _, destination := range []any{&text, &native, &google, &nullable} {
		scanner := sqltomScanValue{destination: destination, kind: "uuid"}
		if err := scanner.Scan(nil); err != nil {
			t.Fatal(err)
		}
	}
	if text != nil || native != nil || google != nil || nullable.Valid {
		t.Fatal("NULL did not reset a reused destination")
	}
}

func TestUUIDAdapterRejectsInvalidInputs(t *testing.T) {
	expected := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	for _, input := range []any{"", "bad-uuid", "00112233-4455-6677-8899-aabbccddeefg", []byte{1, 2}, int64(7)} {
		value := expected
		if err := (sqltomScanValue{destination: &value, kind: "uuid"}).Scan(input); err == nil {
			t.Fatalf("accepted %v", input)
		}
		if value != expected {
			t.Fatal("failed conversion changed destination")
		}
	}
}
