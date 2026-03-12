// ABOUTME: Tests for fidelity mode types, parsing, degradation, resolution, and context compaction.
// ABOUTME: Covers ParseFidelity, DegradeFidelity, ResolveFidelity, and CompactContext functions.
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFidelity(t *testing.T) {
	tests := []struct {
		input   string
		want    Fidelity
		wantErr bool
	}{
		{"full", FidelityFull, false},
		{"summary:high", FidelitySummaryHigh, false},
		{"summary:medium", FidelitySummaryMedium, false},
		{"summary:low", FidelitySummaryLow, false},
		{"compact", FidelityCompact, false},
		{"truncate", FidelityTruncate, false},
		{"", "", true},
		{"unknown", "", true},
		{"summary:", "", true},
		{"FULL", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseFidelity(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseFidelity(%q) expected error, got %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseFidelity(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("ParseFidelity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDegradeFidelity(t *testing.T) {
	tests := []struct {
		input Fidelity
		want  Fidelity
	}{
		{FidelityFull, FidelitySummaryHigh},
		{FidelitySummaryHigh, FidelitySummaryMedium},
		{FidelitySummaryMedium, FidelitySummaryLow},
		{FidelitySummaryLow, FidelityCompact},
		{FidelityCompact, FidelityTruncate},
		{FidelityTruncate, FidelityTruncate},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := DegradeFidelity(tt.input)
			if got != tt.want {
				t.Errorf("DegradeFidelity(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveFidelity(t *testing.T) {
	tests := []struct {
		name       string
		nodeAttrs  map[string]string
		graphAttrs map[string]string
		want       Fidelity
	}{
		{
			name:       "default when no attrs",
			nodeAttrs:  map[string]string{},
			graphAttrs: map[string]string{},
			want:       FidelityFull,
		},
		{
			name:       "node attr takes precedence",
			nodeAttrs:  map[string]string{"fidelity": "compact"},
			graphAttrs: map[string]string{"default_fidelity": "summary:high"},
			want:       FidelityCompact,
		},
		{
			name:       "graph default used when no node attr",
			nodeAttrs:  map[string]string{},
			graphAttrs: map[string]string{"default_fidelity": "summary:medium"},
			want:       FidelitySummaryMedium,
		},
		{
			name:       "invalid node attr falls through to graph default",
			nodeAttrs:  map[string]string{"fidelity": "bogus"},
			graphAttrs: map[string]string{"default_fidelity": "summary:low"},
			want:       FidelitySummaryLow,
		},
		{
			name:       "invalid both falls to default",
			nodeAttrs:  map[string]string{"fidelity": "bogus"},
			graphAttrs: map[string]string{"default_fidelity": "also_bogus"},
			want:       FidelityFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{ID: "test", Attrs: tt.nodeAttrs}
			got := ResolveFidelity(node, tt.graphAttrs)
			if got != tt.want {
				t.Errorf("ResolveFidelity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompactContextFull(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")
	pctx.Set("outcome", "success")
	pctx.Set("last_response", "some response")

	result := CompactContext(pctx, []string{"node1"}, FidelityFull, "", "run1")

	// Full fidelity returns snapshot as-is.
	if result["graph.goal"] != "build something" {
		t.Errorf("expected graph.goal, got %q", result["graph.goal"])
	}
	if result["outcome"] != "success" {
		t.Errorf("expected outcome, got %q", result["outcome"])
	}
	if result["last_response"] != "some response" {
		t.Errorf("expected last_response, got %q", result["last_response"])
	}
}

func TestCompactContextSummaryHigh(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")
	pctx.Set("outcome", "success")
	pctx.Set("extra_key", "extra_value")

	// Create artifact dir with a response.md for the completed node.
	dir := t.TempDir()
	artifactDir := filepath.Join(dir, "run1")
	nodeDir := filepath.Join(artifactDir, "node1")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	longResponse := strings.Repeat("x", 3000)
	if err := os.WriteFile(filepath.Join(nodeDir, "response.md"), []byte(longResponse), 0o644); err != nil {
		t.Fatal(err)
	}

	result := CompactContext(pctx, []string{"node1"}, FidelitySummaryHigh, dir, "run1")

	// Should include all context keys.
	if result["graph.goal"] != "build something" {
		t.Errorf("expected graph.goal")
	}
	if result["extra_key"] != "extra_value" {
		t.Errorf("expected extra_key")
	}

	// Should include trimmed artifact summary.
	summary, ok := result["summary.node1"]
	if !ok {
		t.Fatal("expected summary.node1 key")
	}
	if len(summary) > 2000 {
		t.Errorf("expected summary to be trimmed to 2000 chars, got %d", len(summary))
	}
}

func TestCompactContextSummaryMedium(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")
	pctx.Set("outcome", "success")
	pctx.Set("last_response", "response text")
	pctx.Set("human_response", "human input")
	pctx.Set("preferred_label", "next")
	pctx.Set("tool_stdout", "stdout text")
	pctx.Set("tool_stderr", "stderr text")
	pctx.Set("unrelated_key", "should be dropped")

	result := CompactContext(pctx, []string{"node1"}, FidelitySummaryMedium, "", "run1")

	// Should include only the medium-fidelity keys.
	expectedKeys := []string{"outcome", "last_response", "human_response", "preferred_label", "tool_stdout", "tool_stderr", "graph.goal"}
	for _, key := range expectedKeys {
		if _, ok := result[key]; !ok {
			t.Errorf("expected key %q in medium summary", key)
		}
	}
	if _, ok := result["unrelated_key"]; ok {
		t.Error("unrelated_key should be excluded in medium summary")
	}
}

func TestCompactContextSummaryLow(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")
	pctx.Set("outcome", "success")
	pctx.Set("last_response", "long response")

	result := CompactContext(pctx, []string{"node1", "node2"}, FidelitySummaryLow, "", "run1")

	if result["graph.goal"] != "build something" {
		t.Errorf("expected graph.goal in low summary")
	}

	summary, ok := result["completed_summary"]
	if !ok {
		t.Fatal("expected completed_summary key")
	}
	if !strings.Contains(summary, "node1: completed") {
		t.Errorf("expected 'node1: completed' in summary, got %q", summary)
	}
	if !strings.Contains(summary, "node2: completed") {
		t.Errorf("expected 'node2: completed' in summary, got %q", summary)
	}

	// Should not have last_response.
	if _, ok := result["last_response"]; ok {
		t.Error("last_response should be excluded in low summary")
	}
}

func TestCompactContextCompact(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")
	pctx.Set("outcome", "success")
	pctx.Set("last_response", "long response")

	result := CompactContext(pctx, []string{"node1"}, FidelityCompact, "", "run1")

	if result["graph.goal"] != "build something" {
		t.Errorf("expected graph.goal in compact")
	}
	if result["outcome"] != "success" {
		t.Errorf("expected outcome in compact")
	}
	if _, ok := result["last_response"]; ok {
		t.Error("last_response should be excluded in compact")
	}
	if len(result) != 2 {
		t.Errorf("expected exactly 2 keys in compact, got %d: %v", len(result), result)
	}
}

func TestCompactContextTruncate(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")
	pctx.Set("outcome", "success")
	longValue := strings.Repeat("a", 1000)
	pctx.Set("last_response", longValue)
	pctx.Set("human_response", "short")

	result := CompactContext(pctx, []string{"node1"}, FidelityTruncate, "", "run1")

	// Should have same keys as medium.
	if _, ok := result["outcome"]; !ok {
		t.Error("expected outcome in truncate")
	}

	// Values should be truncated to 500 chars.
	if len(result["last_response"]) > 500 {
		t.Errorf("expected last_response truncated to 500, got %d", len(result["last_response"]))
	}
}

func TestCompactContextSummaryHighMissingArtifact(t *testing.T) {
	pctx := NewPipelineContext()
	pctx.Set("graph.goal", "build something")

	// No artifact dir exists — should not error, just skip summary.
	result := CompactContext(pctx, []string{"node1"}, FidelitySummaryHigh, "/nonexistent", "run1")

	if _, ok := result["summary.node1"]; ok {
		t.Error("should not include summary.node1 when artifact file is missing")
	}
	if result["graph.goal"] != "build something" {
		t.Errorf("expected graph.goal to still be present")
	}
}
