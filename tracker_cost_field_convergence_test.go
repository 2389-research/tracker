// ABOUTME: Regression guard for #520 — the per-turn cost fields have ONE name set.
// ABOUTME: cache_read_tokens / cache_write_tokens / estimated_cost survive; the
// ABOUTME: token_cache_read / token_cache_write / turn_cost_usd duplicates are gone.
package tracker

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// droppedCostTags are the JSON keys #520 removed. They must not appear on any of
// the three schemas (StreamEvent wire, jsonlLogEntry log, activityRawLine reader).
var droppedCostTags = []string{"token_cache_read", "token_cache_write", "turn_cost_usd"}

// survivingCostTags are the keys #519 introduced that #520 converged on.
var survivingCostTags = []string{"cache_read_tokens", "cache_write_tokens", "estimated_cost"}

// TestCostFields_SurvivingSetOnStreamEvent asserts the surviving keys serialize
// on the wire and the dropped keys do not.
func TestCostFields_SurvivingSetOnStreamEvent(t *testing.T) {
	evt := StreamEvent{CacheReadTokens: 900, CacheWriteTokens: 12, EstimatedCost: 0.0033}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	line := string(data)
	for _, tag := range survivingCostTags {
		if !strings.Contains(line, `"`+tag+`"`) {
			t.Errorf("StreamEvent JSON missing surviving key %q: %s", tag, line)
		}
	}
	for _, tag := range droppedCostTags {
		if strings.Contains(line, `"`+tag+`"`) {
			t.Errorf("StreamEvent JSON still carries dropped key %q: %s", tag, line)
		}
	}
}

// TestCostFields_DroppedTagsGoneFromAllSchemas asserts none of the three schemas
// declares a dropped-key JSON tag: the wire struct (reflection), the pipeline log
// entry, and the reader decode struct (both parsed from source).
func TestCostFields_DroppedTagsGoneFromAllSchemas(t *testing.T) {
	wire := map[string]bool{}
	for _, n := range jsonTagNames(reflect.TypeOf(StreamEvent{})) {
		wire[n] = true
	}
	log := map[string]bool{}
	for _, n := range activityLogTagNames(t) {
		log[n] = true
	}
	reader := map[string]bool{}
	for _, n := range structTagNamesFromSource(t, activityRawSource, activityRawTypeName) {
		reader[n] = true
	}
	for _, tag := range droppedCostTags {
		if wire[tag] {
			t.Errorf("StreamEvent still declares dropped tag %q", tag)
		}
		if log[tag] {
			t.Errorf("jsonlLogEntry still declares dropped tag %q", tag)
		}
		if reader[tag] {
			t.Errorf("activityRawLine still declares dropped tag %q", tag)
		}
	}
	// Sanity: the surviving keys must be present on all three so this test can't
	// pass by the fields being deleted wholesale.
	for _, tag := range survivingCostTags {
		if !wire[tag] || !log[tag] || !reader[tag] {
			t.Errorf("surviving tag %q missing (wire=%v log=%v reader=%v)", tag, wire[tag], log[tag], reader[tag])
		}
	}
}

// TestCostFields_SurvivingSetReaderRoundTrip asserts a log line carrying the
// surviving keys decodes onto the exported ActivityEntry.
func TestCostFields_SurvivingSetReaderRoundTrip(t *testing.T) {
	line := `{"ts":"2026-07-28T12:00:00.000Z","source":"agent","type":"turn_metrics",` +
		`"node_id":"gen_code","token_input":100,"token_output":40,` +
		`"cache_read_tokens":900,"cache_write_tokens":12,"estimated_cost":0.0033}`
	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatalf("ParseActivityLine rejected a well-formed line: %s", line)
	}
	if entry.CacheReadTokens != 900 {
		t.Errorf("CacheReadTokens = %d, want 900", entry.CacheReadTokens)
	}
	if entry.CacheWriteTokens != 12 {
		t.Errorf("CacheWriteTokens = %d, want 12", entry.CacheWriteTokens)
	}
	if entry.EstimatedCost < 0.00329 || entry.EstimatedCost > 0.00331 {
		t.Errorf("EstimatedCost = %f, want 0.0033", entry.EstimatedCost)
	}
}
