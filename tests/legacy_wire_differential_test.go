// TEMPORARY differential guard for #72 Slice A. It machine-checks the frozen
// workflow.Legacy* wire types against the original storage/timesheet types while
// BOTH still exist. It lives in package tests, not in internal/workflow, so the
// workflow package keeps zero imports of internal/storage and internal/timesheet
// (that decoupling is the point of the slice).
//
// DELETE THIS FILE in Slice B, together with internal/timesheet: once the
// originals are gone there is nothing to diff against, and the permanent pin
// (internal/workflow TestLegacyRecord_FrozenWireFormat plus
// TestJSONFileStore_LegacyUnknownKeysTolerated) carries the contract forward.
package tests

import (
	"encoding"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// TestLegacyWireTypes_DifferentialWithOriginals asserts that each frozen mirror
// has the same (field name, json tag, wire type class) triples as its original, so
// the frozen copy cannot drift silently while the original is still readable.
//
// The declared type name is compared too, except for structural fields (structs,
// slices, pointers), which are covered by their own pair entry. Exactly one named
// delta is allowed and asserted explicitly rather than skipped: TimeBlock.Category
// was timesheet.SemanticCategory, a defined string type; the mirror uses plain
// string. That equivalence is only sound because nothing on either side implements
// json.Marshaler/Unmarshaler or encoding.TextMarshaler — assertNoCustomCodec
// enforces that precondition per type, so a future custom marshaller on the
// original would fail here instead of quietly redefining the wire form.
func TestLegacyWireTypes_DifferentialWithOriginals(t *testing.T) {
	pairs := []struct {
		name   string
		orig   reflect.Type
		mirror reflect.Type
	}{
		{"TimesheetRecord/LegacyRecord", reflect.TypeOf(storage.TimesheetRecord{}), reflect.TypeOf(workflow.LegacyRecord{})},
		{"TimeBlock/LegacyBlock", reflect.TypeOf(timesheet.TimeBlock{}), reflect.TypeOf(workflow.LegacyBlock{})},
		{"SimpleEntry/LegacyEntry", reflect.TypeOf(timesheet.SimpleEntry{}), reflect.TypeOf(workflow.LegacyEntry{})},
		{"EntryMetadata/LegacyEntryMetadata", reflect.TypeOf(timesheet.EntryMetadata{}), reflect.TypeOf(workflow.LegacyEntryMetadata{})},
	}

	typeDelta := make(map[string]string)
	for _, pair := range pairs {
		assertNoCustomCodec(t, pair.name, pair.orig)
		assertNoCustomCodec(t, pair.name, pair.mirror)
		if got, want := pair.mirror.NumField(), pair.orig.NumField(); got != want {
			t.Fatalf("%s: field count: mirror has %d, original has %d", pair.name, got, want)
		}

		origFields := fieldsByName(pair.orig)
		mirrorFields := fieldsByName(pair.mirror)
		for name, of := range origFields {
			mf, ok := mirrorFields[name]
			if !ok {
				t.Errorf("%s: frozen mirror is missing field %q", pair.name, name)
				continue
			}
			if got, want := mf.Tag.Get("json"), of.Tag.Get("json"); got != want {
				t.Errorf("%s.%s: json tag drift: mirror %q, original %q", pair.name, name, got, want)
			}
			if got, want := wireClass(mf.Type), wireClass(of.Type); got != want {
				t.Errorf("%s.%s: wire type class drift: mirror %s, original %s", pair.name, name, got, want)
			}
			if on, mn := leafTypeName(of.Type), leafTypeName(mf.Type); on != mn {
				typeDelta[pair.name+"."+name] = on + " -> " + mn
			}
		}
		for name := range mirrorFields {
			if _, ok := origFields[name]; !ok {
				t.Errorf("%s: frozen mirror has field %q that the original does not", pair.name, name)
			}
		}
	}

	// The single allowed equivalence, asserted rather than skipped.
	const allowed = "TimeBlock/LegacyBlock.Category"
	if len(typeDelta) != 1 {
		t.Fatalf("expected exactly one declared-type delta (%s), got %v", allowed, typeDelta)
	}
	if got, want := typeDelta[allowed], "SemanticCategory -> string"; got != want {
		t.Fatalf("%s: expected %q, got %q", allowed, want, got)
	}
	if reflect.TypeOf(timesheet.TimeBlock{}).Field(2).Name != "Category" ||
		reflect.TypeOf(timesheet.TimeBlock{}).Field(2).Type.Kind() != reflect.String ||
		reflect.TypeOf(workflow.LegacyBlock{}).Field(2).Type.Kind() != reflect.String {
		t.Errorf("Category is no longer the SemanticCategory-vs-string delta this allowance describes; update the differential deliberately")
	}
}

func assertNoCustomCodec(t *testing.T, label string, typ reflect.Type) {
	t.Helper()
	marshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textMarshaler := reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshaler := reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()

	for _, candidate := range []reflect.Type{typ, reflect.PointerTo(typ)} {
		for _, iface := range []struct {
			label string
			typ   reflect.Type
		}{
			{"json.Marshaler", marshaler},
			{"json.Unmarshaler", unmarshaler},
			{"encoding.TextMarshaler", textMarshaler},
			{"encoding.TextUnmarshaler", textUnmarshaler},
		} {
			if candidate.Implements(iface.typ) {
				t.Errorf("%s: %s implements %s, so wire-class equality no longer implies wire equality", label, candidate, iface.label)
			}
		}
	}
}

func fieldsByName(typ reflect.Type) map[string]reflect.StructField {
	fields := make(map[string]reflect.StructField, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fields[field.Name] = field
	}
	return fields
}

// wireClass reduces a type to the encoding/json-visible class it maps onto. Named
// non-struct types collapse to their kind (SemanticCategory and string are both
// "string"); named structs collapse to "struct" and are compared pairwise instead.
func wireClass(typ reflect.Type) string {
	if typ == reflect.TypeOf(time.Time{}) {
		return "time.Time"
	}
	switch typ.Kind() {
	case reflect.Pointer:
		return "ptr<" + wireClass(typ.Elem()) + ">"
	case reflect.Slice:
		return "slice<" + wireClass(typ.Elem()) + ">"
	case reflect.Struct:
		return "struct"
	default:
		return typ.Kind().String()
	}
}

// leafTypeName returns the declared name of the scalar a field ultimately holds,
// or "" when the field is structural (struct, slice, or pointer) because the
// structural field is covered by its own pair entry.
func leafTypeName(typ reflect.Type) string {
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice:
		return ""
	case reflect.Struct:
		return ""
	default:
		return typ.Name()
	}
}
