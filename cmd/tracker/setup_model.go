// ABOUTME: Multi-page setup wizard TUI for configuring LLM provider credentials.
// ABOUTME: Checkbox provider selection, per-provider API key + base URL forms, branded exit banner.
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Messages ────────────────────────────────────────────────────────────────

type setupSaveMsg struct {
	Values map[string]string
}

type setupCancelMsg struct{}

// ── Pages ───────────────────────────────────────────────────────────────────

type setupPage int

const (
	pageProviderSelect setupPage = iota
	pageProviderConfig
)

// ── Provider definition ─────────────────────────────────────────────────────

type providerEntry struct {
	name          string
	apiKeyEnv     string
	baseURLEnv    string
	enabled       bool
	apiKeyInput   textinput.Model
	baseURLInput  textinput.Model
	keyConfigured bool
	urlConfigured bool
}

// newGeminiProviderEntry handles Gemini's legacy GOOGLE_API_KEY fallback.
// The runtime (llm/client.go) accepts both GEMINI_API_KEY and GOOGLE_API_KEY,
// so setup should recognize the legacy key as "configured" while always
// writing new values under the canonical GEMINI_API_KEY name.
func newGeminiProviderEntry(existing map[string]string) providerEntry {
	p := newProviderEntry("Gemini", "GEMINI_API_KEY", "GEMINI_BASE_URL", existing)
	if !p.keyConfigured && existing["GOOGLE_API_KEY"] != "" {
		p.keyConfigured = true
		p.enabled = true
	}
	return p
}

func newProviderEntry(name, apiKeyEnv, baseURLEnv string, existing map[string]string) providerEntry {
	apiKey := textinput.New()
	apiKey.CharLimit = 512
	apiKey.Width = 48
	apiKey.EchoMode = textinput.EchoPassword
	apiKey.EchoCharacter = '•'
	apiKey.Placeholder = "sk-..."

	baseURL := textinput.New()
	baseURL.CharLimit = 512
	baseURL.Width = 48
	baseURL.Placeholder = "https://api.example.com (optional)"

	return providerEntry{
		name:          name,
		apiKeyEnv:     apiKeyEnv,
		baseURLEnv:    baseURLEnv,
		enabled:       existing[apiKeyEnv] != "",
		apiKeyInput:   apiKey,
		baseURLInput:  baseURL,
		keyConfigured: existing[apiKeyEnv] != "",
		urlConfigured: existing[baseURLEnv] != "",
	}
}

// ── Setup model ─────────────────────────────────────────────────────────────

type setupModel struct {
	providers   []providerEntry
	page        setupPage
	cursor      int // provider selection cursor
	providerIdx int // index into enabled providers list
	fieldFocus  int // 0=apiKey, 1=baseURL on config page
	done        bool
	cancelled   bool

	// ordered list of provider indices that are enabled (built on page transition)
	enabledOrder []int
}

func newSetupModel(existing map[string]string) setupModel {
	if existing == nil {
		existing = map[string]string{}
	}

	providers := []providerEntry{
		newProviderEntry("OpenAI", "OPENAI_API_KEY", "OPENAI_BASE_URL", existing),
		newProviderEntry("Anthropic", "ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", existing),
		newGeminiProviderEntry(existing),
		newProviderEntry("OpenAI-Compat", "OPENAI_COMPAT_API_KEY", "OPENAI_COMPAT_BASE_URL", existing),
	}

	return setupModel{
		providers: providers,
		page:      pageProviderSelect,
	}
}

func (m setupModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case setupSaveMsg, setupCancelMsg:
		return m, tea.Quit
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.cancelled = true
			return m, func() tea.Msg { return setupCancelMsg{} }
		}

		switch m.page {
		case pageProviderSelect:
			return m.updateProviderSelect(msg)
		case pageProviderConfig:
			return m.updateProviderConfig(msg)
		}
	}

	// Forward to focused input on config page
	if m.page == pageProviderConfig && len(m.enabledOrder) > 0 {
		return m.updateConfigInput(msg)
	}

	return m, nil
}

// ── Provider selection page logic ───────────────────────────────────────────

func (m setupModel) updateProviderSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyUp, tea.KeyShiftTab:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.KeyDown, tea.KeyTab:
		if m.cursor < len(m.providers)-1 {
			m.cursor++
		}
	case tea.KeySpace:
		m.providers[m.cursor].enabled = !m.providers[m.cursor].enabled
	case tea.KeyEnter:
		m.enabledOrder = m.buildEnabledOrder()
		if len(m.enabledOrder) == 0 {
			m.done = true
			values := m.collectValues()
			return m, func() tea.Msg { return setupSaveMsg{Values: values} }
		}
		m.providerIdx = 0
		m.page = pageProviderConfig
		m.fieldFocus = 0
		m.focusConfigField()
	}
	return m, nil
}

// ── Provider config page logic ──────────────────────────────────────────────

func (m setupModel) updateProviderConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyTab, tea.KeyShiftTab:
		m.fieldFocus = (m.fieldFocus + 1) % 2
		m.focusConfigField()
		return m, nil
	case tea.KeyEnter:
		return m.advanceProvider()
	}
	// Forward rune input and other keys to the focused text input.
	return m.updateConfigInput(msg)
}

func (m setupModel) updateConfigInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	idx := m.enabledOrder[m.providerIdx]
	var cmd tea.Cmd
	if m.fieldFocus == 0 {
		m.providers[idx].apiKeyInput, cmd = m.providers[idx].apiKeyInput.Update(msg)
	} else {
		m.providers[idx].baseURLInput, cmd = m.providers[idx].baseURLInput.Update(msg)
	}
	return m, cmd
}

func (m setupModel) focusConfigField() {
	if len(m.enabledOrder) == 0 {
		return
	}
	idx := m.enabledOrder[m.providerIdx]
	if m.fieldFocus == 0 {
		m.providers[idx].apiKeyInput.Focus()
		m.providers[idx].baseURLInput.Blur()
	} else {
		m.providers[idx].apiKeyInput.Blur()
		m.providers[idx].baseURLInput.Focus()
	}
}

func (m setupModel) advanceProvider() (tea.Model, tea.Cmd) {
	if m.providerIdx < len(m.enabledOrder)-1 {
		m.providerIdx++
		m.fieldFocus = 0
		m.focusConfigField()
		return m, nil
	}
	m.done = true
	values := m.collectValues()
	return m, func() tea.Msg { return setupSaveMsg{Values: values} }
}

func (m setupModel) buildEnabledOrder() []int {
	var order []int
	for i, p := range m.providers {
		if p.enabled {
			order = append(order, i)
		}
	}
	return order
}

func (m setupModel) collectValues() map[string]string {
	values := make(map[string]string)
	for _, p := range m.providers {
		if !p.enabled {
			continue
		}
		if v := strings.TrimSpace(p.apiKeyInput.Value()); v != "" {
			values[p.apiKeyEnv] = v
		}
		if v := strings.TrimSpace(p.baseURLInput.Value()); v != "" {
			values[p.baseURLEnv] = v
		}
	}
	return values
}

// ── pendingUpdates (kept for compatibility with main.go) ────────────────────

func (m setupModel) pendingUpdates() map[string]string {
	return m.collectValues()
}

// ── View ────────────────────────────────────────────────────────────────────

// ── Setup-specific styles (brand colors and shared styles live in branding.go) ──

var (
	subtitleStyle = lipgloss.NewStyle().
			Foreground(colorSky).
			Italic(true)

	checkboxStyle = lipgloss.NewStyle().
			Foreground(colorElec)

	labelStyle = lipgloss.NewStyle().
			Foreground(colorSky).
			Bold(true)

	configuredHint = lipgloss.NewStyle().
			Foreground(colorWarm).
			Italic(true)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorElec).
			Padding(1, 2)

	providerBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorSky).
			Padding(1, 2)
)

func (m setupModel) View() string {
	if m.done {
		return m.viewFinishBanner()
	}
	if m.cancelled {
		return ""
	}

	switch m.page {
	case pageProviderSelect:
		return m.viewProviderSelect()
	case pageProviderConfig:
		return m.viewProviderConfig()
	}
	return ""
}

func (m setupModel) viewProviderSelect() string {
	var b strings.Builder

	b.WriteString(renderHeader())
	b.WriteByte('\n')

	b.WriteString(subtitleStyle.Render("  Select providers to configure"))
	b.WriteString("\n\n")

	for i, p := range m.providers {
		cursor := "  "
		if i == m.cursor {
			cursor = selectedStyle.Render("▸ ")
		}

		check := checkboxStyle.Render("○")
		if p.enabled {
			check = checkboxStyle.Render("●")
		}

		name := p.name
		if i == m.cursor {
			name = selectedStyle.Render(name)
		}

		status := ""
		if p.keyConfigured {
			status = configuredHint.Render(" (configured)")
		}

		b.WriteString(fmt.Sprintf("  %s %s  %s%s\n", cursor, check, name, status))
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("  ↑/↓ navigate  space toggle  enter continue  esc cancel"))
	b.WriteString("\n")

	return borderStyle.Render(b.String())
}

func (m setupModel) viewProviderConfig() string {
	if len(m.enabledOrder) == 0 {
		return ""
	}
	idx := m.enabledOrder[m.providerIdx]
	p := m.providers[idx]

	var b strings.Builder

	b.WriteString(renderHeader())
	b.WriteByte('\n')

	progress := fmt.Sprintf("  %d / %d", m.providerIdx+1, len(m.enabledOrder))
	b.WriteString(subtitleStyle.Render(fmt.Sprintf("  Configure %s", p.name)))
	b.WriteString(mutedStyle.Render(progress))
	b.WriteString("\n\n")

	// API Key field
	keyLabel := labelStyle.Render("  API Key")
	keyCursor := "  "
	if m.fieldFocus == 0 {
		keyCursor = selectedStyle.Render("▸ ")
	}
	b.WriteString(fmt.Sprintf("  %s%s\n", keyCursor, keyLabel))
	b.WriteString(fmt.Sprintf("    %s\n", p.apiKeyInput.View()))
	if p.keyConfigured {
		b.WriteString(fmt.Sprintf("    %s\n", configuredHint.Render("configured")))
	}
	b.WriteByte('\n')

	// Base URL field
	urlLabel := labelStyle.Render("  Base URL")
	urlCursor := "  "
	if m.fieldFocus == 1 {
		urlCursor = selectedStyle.Render("▸ ")
	}
	b.WriteString(fmt.Sprintf("  %s%s\n", urlCursor, urlLabel))
	b.WriteString(fmt.Sprintf("    %s\n", p.baseURLInput.View()))
	if p.urlConfigured {
		b.WriteString(fmt.Sprintf("    %s\n", configuredHint.Render("configured")))
	}
	b.WriteByte('\n')

	b.WriteString(mutedStyle.Render("  tab switch field  enter next  esc cancel"))
	b.WriteString("\n")

	return providerBorder.Render(b.String())
}

func (m setupModel) viewFinishBanner() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(bannerStyle.Render(logo()))
	b.WriteString("\n\n")
	b.WriteString(taglineStyle.Render("  " + randomTagline()))
	b.WriteString("\n\n")

	return b.String()
}
