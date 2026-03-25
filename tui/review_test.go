package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSplitPromptAndPlan(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLabel string
		wantPlan  string
	}{
		{"with separator", "Review this\n\n---\nThe plan content", "Review this", "The plan content"},
		{"no separator", "Just a prompt", "", "Just a prompt"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, plan := splitPromptAndPlan(tt.input)
			if label != tt.wantLabel {
				t.Errorf("label: got %q, want %q", label, tt.wantLabel)
			}
			if plan != tt.wantPlan {
				t.Errorf("plan: got %q, want %q", plan, tt.wantPlan)
			}
		})
	}
}

func TestReviewContentCreatesViewport(t *testing.T) {
	longPlan := strings.Repeat("Line of plan content\n", 50)
	prompt := "Review\n\n---\n" + longPlan
	rc := NewReviewContent(prompt, nil, 80, 24)
	view := rc.View()
	if !strings.Contains(view, "Plan Review") {
		t.Error("expected 'Plan Review' in divider")
	}
	if rc.tmpFile == "" {
		t.Error("expected temp file to be created")
	}
	// Cleanup.
	rc.cleanup()
	if rc.tmpFile != "" {
		t.Error("expected tmpFile cleared after cleanup")
	}
}

func TestReviewContentSubmit(t *testing.T) {
	ch := make(chan string, 1)
	rc := NewReviewContent("Review\n\n---\nPlan", ch, 80, 24)
	for _, r := range "approve" {
		rc.Update(rune2key(r))
	}
	rc.Update(ctrlS())
	select {
	case got := <-ch:
		if got != "approve" {
			t.Errorf("expected 'approve', got %q", got)
		}
	default:
		t.Error("expected value on reply channel")
	}
}

func TestReviewContentCancelClosesChannel(t *testing.T) {
	ch := make(chan string, 1)
	rc := NewReviewContent("Review\n\n---\nPlan", ch, 80, 24)
	rc.Update(esc())
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed on cancel")
	}
}

// Helpers for key messages.
func rune2key(r rune) keyMsg { return keyMsg{Type: keyRunes, Runes: []rune{r}} }
func ctrlS() keyMsg          { return keyMsg{Type: keyCtrlS} }
func esc() keyMsg            { return keyMsg{Type: keyEsc} }

// Type aliases to avoid importing tea in test helpers.
type keyMsg = tea.KeyMsg

const (
	keyRunes = tea.KeyRunes
	keyCtrlS = tea.KeyCtrlS
	keyEsc   = tea.KeyEscape
)
