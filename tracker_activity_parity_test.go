// ABOUTME: Mechanical parity guard between the activity-log reader and pipeline's jsonlLogEntry.
// ABOUTME: Sibling of the StreamEvent guard (E1) — three schemas for one datum must share key names.
package tracker

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// activityRawSource is the file holding the reader's private decode struct.
// Its tags are read from source (the type is unexported) so the check is
// mechanical rather than a human comparison of two field lists.
const (
	activityRawSource   = "tracker_activity_payload.go"
	activityRawTypeName = "activityRawLine"
)

// activityReaderOnlyFields are decode-struct fields with no jsonlLogEntry
// counterpart. Empty by design: the reader decodes the audit log, so a key it
// reads that the writer never emits is dead weight. Adding an entry here is a
// deliberate act.
var activityReaderOnlyFields = map[string]string{}

// TestActivityEntry_MirrorsActivityLogFieldNames asserts that the supported
// reader decodes every field name the on-disk schema writes, under the SAME
// JSON key — the read-side half of the E1 parity contract. A key the writer
// emits and the reader drops is a lossy reader (E2); a key the reader invents
// is a third spelling for the same datum.
func TestActivityEntry_MirrorsActivityLogFieldNames(t *testing.T) {
	jsonl := activityLogTagNames(t)
	if len(jsonl) < 30 {
		t.Fatalf("only parsed %d tags from %s — parser is not seeing the struct", len(jsonl), jsonlEntrySource)
	}
	reader := map[string]bool{}
	for _, n := range structTagNamesFromSource(t, activityRawSource, activityRawTypeName) {
		reader[n] = true
	}
	if len(reader) < 30 {
		t.Fatalf("only parsed %d tags from %s — parser is not seeing %s", len(reader), activityRawSource, activityRawTypeName)
	}

	var missing []string
	for _, name := range jsonl {
		if !reader[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("activity.jsonl carries %v which %s does not decode.\n"+
			"The supported reader (ParseActivityLine -> ActivityEntry) must be lossless:\n"+
			"add the field under the same JSON name, or document why it is dropped.", missing, activityRawTypeName)
	}

	jsonlSet := map[string]bool{}
	for _, n := range jsonl {
		jsonlSet[n] = true
	}
	for name := range reader {
		if jsonlSet[name] || activityReaderOnlyFields[name] != "" {
			continue
		}
		t.Errorf("%s decodes %q, which the activity.jsonl writer never emits.\n"+
			"If that is intended, add it to activityReaderOnlyFields with a reason.", activityRawTypeName, name)
	}
}

// TestActivityEntry_ExposesEveryDecodedField asserts that every field the
// private decode struct pulls off the line reaches the exported
// ActivityEntry. Catches the other half of the lossiness bug: a field that is
// decoded and then silently dropped on the way to the caller.
//
// The check is by Go field name, which is why activityRawLine deliberately
// uses the same Go names as ActivityEntry (and as StreamEvent).
func TestActivityEntry_ExposesEveryDecodedField(t *testing.T) {
	entryType := reflect.TypeOf(ActivityEntry{})
	var missing []string
	for _, name := range structFieldNamesFromSource(t, activityRawSource, activityRawTypeName) {
		if _, ok := entryType.FieldByName(name); !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%s decodes %v but ActivityEntry has no such field — the datum is\n"+
			"parsed and then dropped. Expose it under the same Go name.", activityRawTypeName, missing)
	}
}

// TestActivityEntry_EveryFieldIsPopulated feeds a synthetic line that sets
// EVERY key the decode struct knows and asserts that no ActivityEntry field is
// left zero. This is the guard the two name-parity checks cannot give: a field
// can be declared on both structs, under matching names, and still be dropped
// because toEntry never copies it.
func TestActivityEntry_EveryFieldIsPopulated(t *testing.T) {
	line := syntheticFullActivityLine(t)
	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatalf("ParseActivityLine rejected the synthetic full line: %s", line)
	}
	v := reflect.ValueOf(entry)
	var zeroed []string
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).IsZero() {
			zeroed = append(zeroed, v.Type().Field(i).Name)
		}
	}
	sort.Strings(zeroed)
	if len(zeroed) > 0 {
		t.Errorf("ActivityEntry fields %v stayed zero for a line that set every key —\n"+
			"the field is declared but never copied out of %s.\nline: %s", zeroed, activityRawTypeName, line)
	}
}

// syntheticFullActivityLine builds a JSON line assigning a non-zero value to
// every json-tagged field of activityRawLine, driven by reflection so a newly
// added field is covered without editing this test.
func syntheticFullActivityLine(t *testing.T) string {
	t.Helper()
	rt := reflect.TypeOf(activityRawLine{})
	parts := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		value := jsonSampleFor(t, f.Type)
		if name == "ts" {
			value = `"2026-07-28T12:00:00.000Z"`
		}
		parts = append(parts, strconv.Quote(name)+":"+value)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// jsonSampleFor returns a JSON literal that decodes to a non-zero value of typ.
// Container samples are non-empty (one element) so IsZero is false on the
// decoded field; struct elements are `{}` because only the container's
// emptiness is under test here.
func jsonSampleFor(t *testing.T, typ reflect.Type) string {
	t.Helper()
	switch typ.Kind() {
	case reflect.String:
		return `"x"`
	case reflect.Bool:
		return "true"
	case reflect.Int, reflect.Int64:
		return "7"
	case reflect.Float64:
		return "1.5"
	case reflect.Ptr:
		return jsonSampleFor(t, typ.Elem())
	case reflect.Slice:
		return "[" + jsonSampleFor(t, typ.Elem()) + "]"
	case reflect.Map:
		return `{"k":` + jsonSampleFor(t, typ.Elem()) + "}"
	case reflect.Struct:
		return "{}"
	default:
		t.Fatalf("no JSON sample for kind %s (%s) — extend jsonSampleFor", typ.Kind(), typ)
		return ""
	}
}

// TestActivityEntry_GoNamesMatchStreamEvent asserts the Go-level half of the
// three-way parity: where ActivityEntry and StreamEvent carry the same datum
// they use the same Go field name, so a caller moving between the live NDJSON
// stream and the replayed audit log writes the same field accesses.
func TestActivityEntry_GoNamesMatchStreamEvent(t *testing.T) {
	wire := map[string]bool{}
	wt := reflect.TypeOf(StreamEvent{})
	for i := 0; i < wt.NumField(); i++ {
		wire[wt.Field(i).Name] = true
	}
	// Timestamp is typed time.Time on ActivityEntry and string on StreamEvent
	// (see the ActivityEntry doc comment on the dual on-disk formats), but the
	// name is shared, so no exemption is needed.
	et := reflect.TypeOf(ActivityEntry{})
	var extra []string
	for i := 0; i < et.NumField(); i++ {
		if name := et.Field(i).Name; !wire[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Errorf("ActivityEntry fields %v have no same-named StreamEvent counterpart.\n"+
			"Same datum, same Go name — pick the StreamEvent spelling.", extra)
	}
}

// parseStructFromSource parses file and returns the named struct type. Shares
// findStructType with the StreamEvent parity guard.
func parseStructFromSource(t *testing.T, file, typeName string) *ast.StructType {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	st := findStructType(f, typeName)
	if st == nil {
		t.Fatalf("type %s not found in %s", typeName, file)
	}
	return st
}

// structTagNamesFromSource returns the JSON names of the named struct's
// tagged fields, parsed out of the given source file.
func structTagNamesFromSource(t *testing.T, file, typeName string) []string {
	t.Helper()
	st := parseStructFromSource(t, file, typeName)
	var names []string
	for _, field := range st.Fields.List {
		if name := jsonNameFromTag(field.Tag); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// structFieldNamesFromSource returns the Go field names of the named struct.
func structFieldNamesFromSource(t *testing.T, file, typeName string) []string {
	t.Helper()
	st := parseStructFromSource(t, file, typeName)
	var names []string
	for _, field := range st.Fields.List {
		for _, ident := range field.Names {
			names = append(names, ident.Name)
		}
	}
	sort.Strings(names)
	return names
}
