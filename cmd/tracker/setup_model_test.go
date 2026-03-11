package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSetupModelShowsConfiguredStateWithoutExposingSecrets(t *testing.T) {
	m := newSetupModel(map[string]string{
		"OPENAI_API_KEY": "super-secret-openai-key",
	})

	view := m.View()
	if !strings.Contains(view, "configured") {
		t.Fatalf("expected configured hint in view, got %q", view)
	}
	if strings.Contains(view, "super-secret-openai-key") {
		t.Fatalf("view exposed existing secret: %q", view)
	}
	if got := m.fields[0].input.Value(); got != "" {
		t.Fatalf("expected masked field to start empty, got %q", got)
	}
}

func TestSetupModelEmptyConfiguredFieldProducesNoUpdate(t *testing.T) {
	m := newSetupModel(map[string]string{
		"OPENAI_API_KEY": "existing-openai-key",
	})
	m.focusIndex = len(m.fields)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save command")
	}

	msg := cmd()
	doneMsg, ok := msg.(setupSaveMsg)
	if !ok {
		t.Fatalf("expected setupSaveMsg, got %T", msg)
	}
	if _, exists := doneMsg.Values["OPENAI_API_KEY"]; exists {
		t.Fatal("did not expect empty configured field to be included in updates")
	}
}

func TestSetupModelTypingNewValueIncludesProviderUpdate(t *testing.T) {
	m := newSetupModel(nil)

	for _, ch := range "new-openai-key" {
		model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = model.(setupModel)
	}
	m.focusIndex = len(m.fields)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save command")
	}

	msg := cmd()
	doneMsg, ok := msg.(setupSaveMsg)
	if !ok {
		t.Fatalf("expected setupSaveMsg, got %T", msg)
	}
	if got := doneMsg.Values["OPENAI_API_KEY"]; got != "new-openai-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", got, "new-openai-key")
	}
}

func TestSetupModelEscCancels(t *testing.T) {
	m := newSetupModel(nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cancel command")
	}

	msg := cmd()
	if _, ok := msg.(setupCancelMsg); !ok {
		t.Fatalf("expected setupCancelMsg, got %T", msg)
	}
}

func TestSetupModelCancelActionEmitsCancelMsg(t *testing.T) {
	m := newSetupModel(nil)
	m.focusIndex = len(m.fields) + 1

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cancel command")
	}

	msg := cmd()
	if _, ok := msg.(setupCancelMsg); !ok {
		t.Fatalf("expected setupCancelMsg, got %T", msg)
	}
}
