// ABOUTME: Wire-stability guard: the NDJSON StreamEvent envelope is the contract
// ABOUTME: every transport/subscriber parses, so its field set must not drift silently.
package tracker

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestStreamEvent_WireEnvelopeStable pins the NDJSON envelope's JSON field names.
// A new or renamed field breaks this deliberately, forcing a conscious wire-
// contract change (update `want` when the format intentionally evolves) rather
// than silently breaking a subscriber compiled against the old shape.
func TestStreamEvent_WireEnvelopeStable(t *testing.T) {
	want := []string{
		"content", "error", "message", "model", "node_id", "provider",
		"run_id", "source", "terminal_status", "tool_name", "ts", "type",
	}
	got := jsonTagNames(reflect.TypeOf(StreamEvent{}))
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StreamEvent wire envelope changed.\n got: %v\nwant: %v\n"+
			"If this is an intended wire-format change, update `want` — this is the\n"+
			"contract every NDJSON subscriber parses.", got, want)
	}
}

// jsonTagNames returns the JSON field names (tag before the first comma,
// skipping "-") of a struct type.
func jsonTagNames(tp reflect.Type) []string {
	var names []string
	for i := 0; i < tp.NumField(); i++ {
		tag := tp.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			names = append(names, name)
		}
	}
	return names
}
