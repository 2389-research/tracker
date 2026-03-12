package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// ── Provider selection page ─────────────────────────────────────────────────

func TestSetupModelStartsOnProviderSelectionPage(t *testing.T) {
	m := newSetupModel(nil)
	if m.page != pageProviderSelect {
		t.Fatalf("initial page = %d, want pageProviderSelect (%d)", m.page, pageProviderSelect)
	}
}

func TestSetupModelShowsThreeProviders(t *testing.T) {
	m := newSetupModel(nil)
	if got := len(m.providers); got != 3 {
		t.Fatalf("expected 3 providers, got %d", got)
	}
	wantNames := []string{"OpenAI", "Anthropic", "Gemini"}
	for i, want := range wantNames {
		if m.providers[i].name != want {
			t.Errorf("providers[%d].name = %q, want %q", i, m.providers[i].name, want)
		}
	}
}

func TestSetupModelProviderPreselectedWhenExistingKeyPresent(t *testing.T) {
	m := newSetupModel(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-existing",
	})
	if m.providers[1].enabled != true {
		t.Fatal("expected Anthropic to be pre-selected when key exists")
	}
	if m.providers[0].enabled != false {
		t.Fatal("expected OpenAI to not be pre-selected without existing key")
	}
}

func TestSetupModelGeminiRecognizesLegacyGoogleAPIKey(t *testing.T) {
	m := newSetupModel(map[string]string{
		"GOOGLE_API_KEY": "legacy-google-key",
	})
	// Gemini is index 2.
	if !m.providers[2].keyConfigured {
		t.Fatal("expected Gemini to show as configured with GOOGLE_API_KEY")
	}
	if !m.providers[2].enabled {
		t.Fatal("expected Gemini to be pre-selected with GOOGLE_API_KEY")
	}
}

func TestSetupModelToggleProviderCheckbox(t *testing.T) {
	m := newSetupModel(nil)
	if m.providers[0].enabled {
		t.Fatal("expected OpenAI initially disabled")
	}

	// Press space to toggle
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)

	if !m.providers[0].enabled {
		t.Fatal("expected OpenAI enabled after space toggle")
	}

	// Toggle back
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)

	if m.providers[0].enabled {
		t.Fatal("expected OpenAI disabled after second toggle")
	}
}

func TestSetupModelNavigateProviderListWithArrows(t *testing.T) {
	m := newSetupModel(nil)
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(setupModel)
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.cursor)
	}

	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(setupModel)
	if m.cursor != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.cursor)
	}
}

func TestSetupModelCursorClampedAtBounds(t *testing.T) {
	m := newSetupModel(nil)

	// Up at 0 stays at 0
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(setupModel)
	if m.cursor != 0 {
		t.Fatalf("cursor after up-at-zero = %d, want 0", m.cursor)
	}

	// Navigate to last provider
	for i := 0; i < len(m.providers); i++ {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = model.(setupModel)
	}
	last := len(m.providers) - 1
	if m.cursor != last {
		t.Fatalf("cursor after max downs = %d, want %d", m.cursor, last)
	}

	// Down at max stays at max
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(setupModel)
	if m.cursor != last {
		t.Fatalf("cursor after down-at-max = %d, want %d", m.cursor, last)
	}
}

func TestSetupModelEnterAdvancesToProviderConfigPage(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)

	// Press enter to advance
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	if m.page != pageProviderConfig {
		t.Fatalf("page = %d, want pageProviderConfig (%d)", m.page, pageProviderConfig)
	}
}

func TestSetupModelEnterWithNoProvidersSelectedSkipsToFinish(t *testing.T) {
	m := newSetupModel(nil)
	// Press enter without selecting any
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	if !m.done {
		t.Fatal("expected done=true when no providers selected")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}
	msg := cmd()
	saveMsg, ok := msg.(setupSaveMsg)
	if !ok {
		t.Fatalf("expected setupSaveMsg, got %T", msg)
	}
	if len(saveMsg.Values) != 0 {
		t.Fatalf("expected empty values, got %v", saveMsg.Values)
	}
}

// ── Provider config pages ───────────────────────────────────────────────────

func TestSetupModelProviderConfigShowsAPIKeyAndBaseURL(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI, advance
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	if m.page != pageProviderConfig {
		t.Fatalf("page = %d, want pageProviderConfig", m.page)
	}

	view := m.View()
	if !strings.Contains(view, "API Key") {
		t.Fatalf("expected 'API Key' in provider config view, got:\n%s", view)
	}
	if !strings.Contains(view, "Base URL") {
		t.Fatalf("expected 'Base URL' in provider config view, got:\n%s", view)
	}
}

func TestSetupModelAPIKeyFieldIsMasked(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI, advance
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	p := m.providers[m.enabledOrder[m.providerIdx]]
	if p.apiKeyInput.EchoMode != textinput.EchoPassword {
		t.Fatalf("API key echo mode = %d, want EchoPassword (%d)", p.apiKeyInput.EchoMode, textinput.EchoPassword)
	}
}

func TestSetupModelBaseURLFieldIsNotMasked(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI, advance
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	p := m.providers[m.enabledOrder[m.providerIdx]]
	if p.baseURLInput.EchoMode != textinput.EchoNormal {
		t.Fatalf("base URL echo mode = %d, want EchoNormal (%d)", p.baseURLInput.EchoMode, textinput.EchoNormal)
	}
}

func TestSetupModelTabSwitchesFieldsOnConfigPage(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI, advance
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	p := m.providers[m.providerIdx]
	if !p.apiKeyInput.Focused() {
		t.Fatal("expected API key input focused initially")
	}

	// Tab to base URL
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = model.(setupModel)
	p = m.providers[m.providerIdx]
	if !p.baseURLInput.Focused() {
		t.Fatal("expected base URL input focused after tab")
	}
}

func TestSetupModelExistingKeyShowsConfigured(t *testing.T) {
	m := newSetupModel(map[string]string{
		"OPENAI_API_KEY": "sk-existing",
	})
	// Enable OpenAI, advance
	m.providers[0].enabled = true
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	view := m.View()
	if !strings.Contains(view, "configured") {
		t.Fatalf("expected 'configured' hint in view, got:\n%s", view)
	}
	// Must not expose the actual key
	if strings.Contains(view, "sk-existing") {
		t.Fatal("view exposed existing secret")
	}
}

func TestSetupModelExistingBaseURLShowsConfigured(t *testing.T) {
	m := newSetupModel(map[string]string{
		"OPENAI_BASE_URL": "https://custom.example.com",
	})
	m.providers[0].enabled = true
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	view := m.View()
	if !strings.Contains(view, "configured") {
		t.Fatalf("expected 'configured' hint in view:\n%s", view)
	}
}

// ── Multi-provider navigation ───────────────────────────────────────────────

func TestSetupModelAdvancesThroughMultipleProviders(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI and Anthropic
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace}) // toggle OpenAI
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // toggle Anthropic
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // advance
	m = model.(setupModel)

	if m.page != pageProviderConfig {
		t.Fatalf("expected pageProviderConfig, got %d", m.page)
	}
	if m.providers[m.providerIdx].name != "OpenAI" {
		t.Fatalf("first provider = %q, want OpenAI", m.providers[m.providerIdx].name)
	}

	// Enter advances to next provider
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	if m.page != pageProviderConfig {
		t.Fatalf("expected pageProviderConfig for second provider, got %d", m.page)
	}
	if m.providers[m.providerIdx].name != "Anthropic" {
		t.Fatalf("second provider = %q, want Anthropic", m.providers[m.providerIdx].name)
	}
}

func TestSetupModelEnterOnLastProviderFinishes(t *testing.T) {
	m := newSetupModel(nil)
	// Enable only Gemini
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace}) // toggle Gemini
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // advance to config
	m = model.(setupModel)

	currentIdx := m.enabledOrder[m.providerIdx]
	if m.providers[currentIdx].name != "Gemini" {
		t.Fatalf("expected Gemini config, got %q", m.providers[currentIdx].name)
	}

	// Enter on last provider finishes
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	if !m.done {
		t.Fatal("expected done=true after last provider")
	}
	if cmd == nil {
		t.Fatal("expected save command")
	}
	msg := cmd()
	if _, ok := msg.(setupSaveMsg); !ok {
		t.Fatalf("expected setupSaveMsg, got %T", msg)
	}
}

// ── Output / save values ────────────────────────────────────────────────────

func TestSetupModelCollectsTypedValues(t *testing.T) {
	m := newSetupModel(nil)
	// Enable OpenAI
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	// Type an API key
	for _, ch := range "sk-test-key" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = model.(setupModel)
	}

	// Tab to base URL field
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = model.(setupModel)

	for _, ch := range "https://proxy.example.com" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = model.(setupModel)
	}

	// Enter to finish (single provider)
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	if cmd == nil {
		t.Fatal("expected save command")
	}
	msg := cmd()
	saveMsg, ok := msg.(setupSaveMsg)
	if !ok {
		t.Fatalf("expected setupSaveMsg, got %T", msg)
	}
	if got := saveMsg.Values["OPENAI_API_KEY"]; got != "sk-test-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want %q", got, "sk-test-key")
	}
	if got := saveMsg.Values["OPENAI_BASE_URL"]; got != "https://proxy.example.com" {
		t.Fatalf("OPENAI_BASE_URL = %q, want %q", got, "https://proxy.example.com")
	}
}

func TestSetupModelEmptyFieldsNotIncludedInValues(t *testing.T) {
	m := newSetupModel(map[string]string{
		"OPENAI_API_KEY": "existing-key",
	})
	m.providers[0].enabled = true
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	// Don't type anything, just press enter
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	msg := cmd()
	saveMsg := msg.(setupSaveMsg)
	if _, exists := saveMsg.Values["OPENAI_API_KEY"]; exists {
		t.Fatal("did not expect empty configured field in updates")
	}
	if _, exists := saveMsg.Values["OPENAI_BASE_URL"]; exists {
		t.Fatal("did not expect empty base URL field in updates")
	}
}

// ── Cancel behavior ─────────────────────────────────────────────────────────

func TestSetupModelEscCancelsFromProviderSelect(t *testing.T) {
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

func TestSetupModelEscCancelsFromProviderConfig(t *testing.T) {
	m := newSetupModel(nil)
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = model.(setupModel)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(setupModel)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected cancel command")
	}
	msg := cmd()
	if _, ok := msg.(setupCancelMsg); !ok {
		t.Fatalf("expected setupCancelMsg, got %T", msg)
	}
}

// ── Branding ────────────────────────────────────────────────────────────────

func TestSetupModelViewContainsBranding(t *testing.T) {
	m := newSetupModel(nil)
	view := m.View()
	if !strings.Contains(view, "2389") {
		t.Fatalf("expected 2389 branding in view, got:\n%s", view)
	}
}

func TestSetupModelFinishBanner(t *testing.T) {
	m := newSetupModel(nil)
	m.done = true
	view := m.View()
	if !strings.Contains(view, "2389") {
		t.Fatalf("expected '2389' in finish banner, got:\n%s", view)
	}
	// The ASCII art block letters spell TRACKER
	if !strings.Contains(view, "██") {
		t.Fatalf("expected block-letter ASCII art in finish banner, got:\n%s", view)
	}
}

