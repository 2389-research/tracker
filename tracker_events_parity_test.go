// ABOUTME: Mechanical field-name parity guard between StreamEvent and pipeline's jsonlLogEntry.
// ABOUTME: Two NDJSON schemas for the same datum must never use different key names (E1).
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

// jsonlEntrySource is the file holding pipeline's private activity.jsonl entry
// type. It is unexported, so this test reads its tags from source rather than
// via reflection (importing it is impossible; pipeline cannot import tracker).
const (
	jsonlEntrySource   = "pipeline/events_jsonl.go"
	jsonlEntryTypeName = "jsonlLogEntry"
)

// wireOnlyFields are StreamEvent fields with no activity.jsonl counterpart, by
// design. Adding to this list is a deliberate act: it means the public wire
// carries a datum the audit log does not.
var wireOnlyFields = map[string]string{
	"terminal_status":          "run terminal status; predates E1, never mirrored to activity.jsonl",
	"snapshot_nodes":           "pipeline_started run snapshot has no activity.jsonl counterpart",
	"snapshot_start_node":      "pipeline_started run snapshot has no activity.jsonl counterpart",
	"snapshot_exit_node":       "pipeline_started run snapshot has no activity.jsonl counterpart",
	"snapshot_current_node":    "pipeline_started run snapshot has no activity.jsonl counterpart",
	"snapshot_completed_nodes": "pipeline_started run snapshot has no activity.jsonl counterpart",
}

// TestStreamEvent_MirrorsActivityLogFieldNames asserts that every field name in
// the private activity.jsonl schema is also present on the public NDJSON wire
// under the SAME name. The repo has two NDJSON-shaped schemas; the whole point
// of E1 is that a consumer can decode both with one struct. Divergent names for
// the same datum would be worse than the original gap.
func TestStreamEvent_MirrorsActivityLogFieldNames(t *testing.T) {
	jsonl := activityLogTagNames(t)
	if len(jsonl) < 30 {
		t.Fatalf("only parsed %d tags from %s — parser is not seeing the struct", len(jsonl), jsonlEntrySource)
	}
	wire := map[string]bool{}
	for _, n := range jsonTagNames(reflect.TypeOf(StreamEvent{})) {
		wire[n] = true
	}

	var missing []string
	for _, name := range jsonl {
		if !wire[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("activity.jsonl carries %v which the public StreamEvent wire does not.\n"+
			"Either mirror the field onto StreamEvent under the same JSON name, or\n"+
			"document why the audit log is intentionally richer.", missing)
	}

	jsonlSet := map[string]bool{}
	for _, n := range jsonl {
		jsonlSet[n] = true
	}
	for name := range wire {
		if jsonlSet[name] || wireOnlyFields[name] != "" {
			continue
		}
		t.Errorf("StreamEvent field %q has no activity.jsonl counterpart and is not\n"+
			"listed in wireOnlyFields. If that is intended, add it there with a reason.", name)
	}
}

// activityLogTagNames parses jsonlEntrySource and returns the JSON field names
// of the jsonlLogEntry struct.
func activityLogTagNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), jsonlEntrySource, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", jsonlEntrySource, err)
	}
	st := findStructType(f, jsonlEntryTypeName)
	if st == nil {
		t.Fatalf("type %s not found in %s", jsonlEntryTypeName, jsonlEntrySource)
	}
	var names []string
	for _, field := range st.Fields.List {
		name := jsonNameFromTag(field.Tag)
		if name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// findStructType returns the struct type declared as `type <name> struct`.
func findStructType(f *ast.File, name string) *ast.StructType {
	var found *ast.StructType
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != name {
			return true
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			found = st
		}
		return false
	})
	return found
}

// jsonNameFromTag extracts the JSON name from a struct field tag literal.
func jsonNameFromTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	unquoted, err := strconv.Unquote(tag.Value)
	if err != nil {
		return ""
	}
	value := reflect.StructTag(unquoted).Get("json")
	if value == "" || value == "-" {
		return ""
	}
	name, _, _ := strings.Cut(value, ",")
	return name
}
