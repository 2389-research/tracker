// ABOUTME: Tests for the model catalog (ModelInfo registry).
// ABOUTME: Validates model lookup, listing, and filtering by provider/capability.
package llm

import "testing"

func TestGetModelInfo(t *testing.T) {
	info := GetModelInfo("claude-opus-4-6")
	if info == nil {
		t.Fatal("expected model info for claude-opus-4-6")
	}
	if info.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %q", info.Provider)
	}
	if !info.SupportsTools {
		t.Error("claude-opus-4-6 should support tools")
	}
}

func TestGetModelInfoUnknown(t *testing.T) {
	info := GetModelInfo("nonexistent-model")
	if info != nil {
		t.Error("expected nil for unknown model")
	}
}

func TestListModels(t *testing.T) {
	all := ListModels("")
	if len(all) == 0 {
		t.Error("expected at least one model")
	}

	anthropic := ListModels("anthropic")
	if len(anthropic) == 0 {
		t.Error("expected at least one anthropic model")
	}
	for _, m := range anthropic {
		if m.Provider != "anthropic" {
			t.Errorf("expected anthropic, got %q", m.Provider)
		}
	}
}

func TestGetModelInfo_Sonnet46(t *testing.T) {
	info := GetModelInfo("claude-sonnet-4-6")
	if info == nil {
		t.Fatal("claude-sonnet-4-6 not found in catalog")
	}
	if info.Provider != "anthropic" {
		t.Errorf("Provider = %q, want \"anthropic\"", info.Provider)
	}
	if info.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", info.ContextWindow)
	}
}

func TestGetLatestModel(t *testing.T) {
	m := GetLatestModel("anthropic", "")
	if m == nil {
		t.Fatal("expected a latest anthropic model")
	}
	if m.Provider != "anthropic" {
		t.Errorf("expected anthropic, got %q", m.Provider)
	}
}
