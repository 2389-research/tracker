package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type setupSaveMsg struct {
	Values map[string]string
}

type setupCancelMsg struct{}

type setupField struct {
	key        string
	label      string
	input      textinput.Model
	configured bool
}

type setupModel struct {
	fields     []setupField
	focusIndex int
	done       bool
	cancelled  bool
}

func newSetupModel(existing map[string]string) setupModel {
	fields := []setupField{
		newSetupField("OPENAI_API_KEY", "OpenAI", existing["OPENAI_API_KEY"] != ""),
		newSetupField("ANTHROPIC_API_KEY", "Anthropic", existing["ANTHROPIC_API_KEY"] != ""),
		newSetupField("GEMINI_API_KEY", "Gemini", existing["GEMINI_API_KEY"] != ""),
	}
	fields[0].input.Focus()

	return setupModel{
		fields: fields,
	}
}

func newSetupField(key, label string, configured bool) setupField {
	input := textinput.New()
	input.CharLimit = 512
	input.Width = 48
	input.EchoMode = textinput.EchoPassword
	input.EchoCharacter = '•'
	input.Placeholder = "Enter API key"

	return setupField{
		key:        key,
		label:      label,
		input:      input,
		configured: configured,
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
		case tea.KeyTab, tea.KeyDown:
			m.setFocus((m.focusIndex + 1) % (len(m.fields) + 2))
			return m, nil
		case tea.KeyShiftTab, tea.KeyUp:
			total := len(m.fields) + 2
			m.setFocus((m.focusIndex - 1 + total) % total)
			return m, nil
		case tea.KeyEnter:
			if m.focusIndex == len(m.fields) {
				m.done = true
				values := m.pendingUpdates()
				return m, func() tea.Msg { return setupSaveMsg{Values: values} }
			}
			if m.focusIndex == len(m.fields)+1 {
				m.cancelled = true
				return m, func() tea.Msg { return setupCancelMsg{} }
			}

			m.setFocus(m.focusIndex + 1)
			return m, nil
		}
	}

	if m.focusIndex < len(m.fields) {
		var cmd tea.Cmd
		m.fields[m.focusIndex].input, cmd = m.fields[m.focusIndex].input.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *setupModel) setFocus(index int) {
	m.focusIndex = index
	for i := range m.fields {
		if i == m.focusIndex {
			m.fields[i].input.Focus()
			continue
		}
		m.fields[i].input.Blur()
	}
}

func (m setupModel) pendingUpdates() map[string]string {
	values := make(map[string]string)
	for _, field := range m.fields {
		value := strings.TrimSpace(field.input.Value())
		if value == "" {
			continue
		}
		values[field.key] = value
	}
	return values
}

func (m setupModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	var b strings.Builder
	b.WriteString("Tracker setup\n\n")
	for i, field := range m.fields {
		cursor := " "
		if i == m.focusIndex {
			cursor = ">"
		}
		b.WriteString(fmt.Sprintf("%s %s\n", cursor, field.label))
		b.WriteString(fmt.Sprintf("  %s\n", field.input.View()))
		if field.configured {
			b.WriteString("  configured\n")
		}
		b.WriteByte('\n')
	}

	saveCursor := " "
	cancelCursor := " "
	if m.focusIndex == len(m.fields) {
		saveCursor = ">"
	}
	if m.focusIndex == len(m.fields)+1 {
		cancelCursor = ">"
	}
	b.WriteString(fmt.Sprintf("%s Save\n", saveCursor))
	b.WriteString(fmt.Sprintf("%s Cancel\n", cancelCursor))
	b.WriteString("\n tab next  shift+tab previous  enter select  esc cancel")
	return b.String()
}
