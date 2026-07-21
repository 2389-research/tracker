// ABOUTME: Unit tests for the pure App Home view builder. The slash-command and
// ABOUTME: app_home_opened event plumbing is verified against a live workspace.
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// blocksText marshals the Block Kit blocks and returns the JSON, so tests can
// assert on rendered content without depending on slack-go's block structs.
func blocksText(t *testing.T, blocks interface{}) string {
	t.Helper()
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	return string(b)
}

func TestHomeBlocks_EmptyState(t *testing.T) {
	txt := blocksText(t, homeBlocks(nil))
	if !strings.Contains(txt, "trackerbot") {
		t.Error("home view should have the header")
	}
	if !strings.Contains(txt, "none right now") {
		t.Errorf("empty state should say there are no active runs:\n%s", txt)
	}
}

func TestHomeBlocks_ListsActiveRuns(t *testing.T) {
	runs := []RunView{
		{Key: "1720000000.000100", State: "running"},
		{Key: "1720000000.000200", State: "starting"},
	}
	txt := blocksText(t, homeBlocks(runs))
	for _, r := range runs {
		if !strings.Contains(txt, r.Key) {
			t.Errorf("home view missing run key %q:\n%s", r.Key, txt)
		}
		if !strings.Contains(txt, r.State) {
			t.Errorf("home view missing run state %q", r.State)
		}
	}
	if strings.Contains(txt, "none right now") {
		t.Error("non-empty run list should not show the empty state")
	}
}
