# Bubbletea TUI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the bare ConsoleInterviewer with a Bubbletea-powered UI, supporting headless-interactive (mode 1) and full TUI dashboard (mode 2).

**Architecture:** A `BubbleteaInterviewer` implements `Interviewer` + `FreeformInterviewer`. In mode 1 (default), it spins up inline `tea.Program` instances per gate. In mode 2 (`--tui`), it sends messages to a persistent TUI app that shows pipeline progress, token counts, and agent logs, with modal overlays for gates. A `TokenTrackingMiddleware` accumulates per-provider usage. A `TUIEventHandler` bridges pipeline events into bubbletea messages.

**Tech Stack:** Go, bubbletea, bubbles (textinput, list, viewport, spinner), lipgloss

---

## Task 1: Add Bubbletea Dependencies

**Files:**
- Modify: `go.mod`

**Step 1: Add dependencies**

Run:
```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/charmbracelet/bubbles@latest
```

**Step 2: Tidy**

Run: `go mod tidy`

**Step 3: Verify build**

Run: `go build ./...`
Expected: no errors

**Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add bubbletea, lipgloss, and bubbles dependencies"
```

---

## Task 2: Choice Component

Build the bubbletea model for arrow-key choice selection. This replaces the numbered-list approach in ConsoleInterviewer.Ask.

**Files:**
- Create: `tui/components/choice.go`
- Create: `tui/components/choice_test.go`

**Step 1: Write the failing test**

```go
// tui/components/choice_test.go
package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestChoiceModelInitialState(t *testing.T) {
	m := NewChoiceModel("Pick one", []string{"alpha", "beta", "gamma"}, "")
	if m.Prompt != "Pick one" {
		t.Errorf("expected prompt 'Pick one', got %q", m.Prompt)
	}
	if m.cursor != 0 {
		t.Error("expected cursor at 0")
	}
	if m.selected != "" {
		t.Error("expected no selection")
	}
}

func TestChoiceModelArrowDown(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"a", "b", "c"}, "")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	model := updated.(ChoiceModel)
	if model.cursor != 1 {
		t.Errorf("expected cursor at 1, got %d", model.cursor)
	}
}

func TestChoiceModelArrowUpWraps(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"a", "b", "c"}, "")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	model := updated.(ChoiceModel)
	if model.cursor != 2 {
		t.Errorf("expected cursor to wrap to 2, got %d", model.cursor)
	}
}

func TestChoiceModelEnterSelects(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"a", "b", "c"}, "")
	// Move down once, then press enter
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(ChoiceModel)
	if model.Selected() != "b" {
		t.Errorf("expected 'b', got %q", model.Selected())
	}
	if !model.Done() {
		t.Error("expected Done() to be true")
	}
}

func TestChoiceModelDefaultHighlighted(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"a", "b", "c"}, "b")
	if m.cursor != 1 {
		t.Errorf("expected cursor at 1 for default 'b', got %d", m.cursor)
	}
}

func TestChoiceModelQuitOnCtrlC(t *testing.T) {
	m := NewChoiceModel("Pick", []string{"a", "b"}, "")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("expected quit command on ctrl+c")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tui/components/ -run TestChoiceModel -v`
Expected: FAIL — package/types don't exist

**Step 3: Write minimal implementation**

```go
// tui/components/choice.go
// ABOUTME: Bubbletea model for arrow-key choice selection in human gates.
// ABOUTME: Renders a styled list of options with cursor navigation and enter to confirm.
package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	choicePromptStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	choiceSelectedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	choiceNormalStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	choiceCursorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
)

// ChoiceModel is a bubbletea model for selecting from a list of choices.
type ChoiceModel struct {
	Prompt   string
	choices  []string
	cursor   int
	selected string
	done     bool
	quitting bool
}

// NewChoiceModel creates a choice model. If defaultChoice matches a choice,
// the cursor starts on it.
func NewChoiceModel(prompt string, choices []string, defaultChoice string) ChoiceModel {
	cursor := 0
	for i, c := range choices {
		if c == defaultChoice {
			cursor = i
			break
		}
	}
	return ChoiceModel{
		Prompt:  prompt,
		choices: choices,
		cursor:  cursor,
	}
}

func (m ChoiceModel) Init() tea.Cmd { return nil }

func (m ChoiceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp, tea.KeyShiftTab:
			m.cursor--
			if m.cursor < 0 {
				m.cursor = len(m.choices) - 1
			}
		case tea.KeyDown, tea.KeyTab:
			m.cursor++
			if m.cursor >= len(m.choices) {
				m.cursor = 0
			}
		case tea.KeyEnter:
			m.selected = m.choices[m.cursor]
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m ChoiceModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString(choicePromptStyle.Render(m.Prompt))
	b.WriteString("\n\n")

	for i, choice := range m.choices {
		cursor := "  "
		style := choiceNormalStyle
		if i == m.cursor {
			cursor = choiceCursorStyle.Render("▸ ")
			style = choiceSelectedStyle
		}
		b.WriteString(fmt.Sprintf("%s%s\n", cursor, style.Render(choice)))
	}

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("↑/↓ navigate • enter select • esc cancel"))

	return b.String()
}

// Selected returns the chosen value, or empty string if not yet selected.
func (m ChoiceModel) Selected() string { return m.selected }

// Done returns true if the user has made a selection.
func (m ChoiceModel) Done() bool { return m.done }

// Quitting returns true if the user cancelled.
func (m ChoiceModel) Quitting() bool { return m.quitting }
```

**Step 4: Run tests**

Run: `go test ./tui/components/ -run TestChoiceModel -v`
Expected: all PASS

**Step 5: Commit**

```bash
git add tui/components/choice.go tui/components/choice_test.go
git commit -m "feat: add bubbletea choice selection component"
```

---

## Task 3: Freeform Input Component

Build the bubbletea model for styled freeform text input. Replaces ConsoleInterviewer.AskFreeform.

**Files:**
- Create: `tui/components/freeform.go`
- Create: `tui/components/freeform_test.go`

**Step 1: Write the failing test**

```go
// tui/components/freeform_test.go
package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFreeformModelInitialState(t *testing.T) {
	m := NewFreeformModel("What do you want?")
	if m.Prompt != "What do you want?" {
		t.Errorf("expected prompt, got %q", m.Prompt)
	}
	if m.Done() {
		t.Error("should not be done initially")
	}
}

func TestFreeformModelEnterSubmits(t *testing.T) {
	m := NewFreeformModel("Tell me")
	// Type some text
	for _, r := range "hello world" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(FreeformModel)
	if model.Value() != "hello world" {
		t.Errorf("expected 'hello world', got %q", model.Value())
	}
	if !model.Done() {
		t.Error("expected Done() after enter")
	}
}

func TestFreeformModelEmptyReject(t *testing.T) {
	m := NewFreeformModel("Tell me")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model := updated.(FreeformModel)
	if model.Done() {
		t.Error("should not accept empty input")
	}
	if model.Error() == "" {
		t.Error("expected error message for empty input")
	}
}

func TestFreeformModelCtrlCQuits(t *testing.T) {
	m := NewFreeformModel("Tell me")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Error("expected quit command on ctrl+c")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tui/components/ -run TestFreeformModel -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// tui/components/freeform.go
// ABOUTME: Bubbletea model for freeform text input in human gates.
// ABOUTME: Renders a styled prompt with a text input field, enter to submit.
package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	freeformPromptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	freeformErrorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	freeformHintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// FreeformModel is a bubbletea model for capturing freeform text input.
type FreeformModel struct {
	Prompt   string
	input    textinput.Model
	done     bool
	quitting bool
	err      string
}

// NewFreeformModel creates a freeform input model with the given prompt.
func NewFreeformModel(prompt string) FreeformModel {
	ti := textinput.New()
	ti.Placeholder = "Type your response..."
	ti.Focus()
	ti.CharLimit = 2048
	ti.Width = 60
	ti.Prompt = "▸ "
	return FreeformModel{
		Prompt: prompt,
		input:  ti,
	}
}

func (m FreeformModel) Init() tea.Cmd { return textinput.Blink }

func (m FreeformModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyEnter:
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				m.err = "Input cannot be empty"
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.err = ""
	return m, cmd
}

func (m FreeformModel) View() string {
	if m.done {
		return ""
	}

	var b strings.Builder
	b.WriteString(freeformPromptStyle.Render(m.Prompt))
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")

	if m.err != "" {
		b.WriteString(freeformErrorStyle.Render(fmt.Sprintf("  %s", m.err)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(freeformHintStyle.Render("enter submit • esc cancel"))

	return b.String()
}

// Value returns the submitted text.
func (m FreeformModel) Value() string { return strings.TrimSpace(m.input.Value()) }

// Done returns true if the user submitted input.
func (m FreeformModel) Done() bool { return m.done }

// Quitting returns true if the user cancelled.
func (m FreeformModel) Quitting() bool { return m.quitting }

// Error returns the current validation error message, or empty string.
func (m FreeformModel) Error() string { return m.err }
```

**Step 4: Run tests**

Run: `go test ./tui/components/ -run TestFreeformModel -v`
Expected: all PASS

**Step 5: Commit**

```bash
git add tui/components/freeform.go tui/components/freeform_test.go
git commit -m "feat: add bubbletea freeform text input component"
```

---

## Task 4: BubbleteaInterviewer (Mode 1 — Headless Inline)

Implement `BubbleteaInterviewer` that satisfies `Interviewer` and `FreeformInterviewer`. In mode 1 (`tuiProgram == nil`), it creates short-lived inline `tea.Program` instances.

**Files:**
- Create: `tui/interviewer.go`
- Create: `tui/interviewer_test.go`

**Step 1: Write the failing test**

```go
// tui/interviewer_test.go
package tui

import (
	"testing"
)

func TestBubbleteaInterviewerImplementsInterfaces(t *testing.T) {
	// Compile-time check that BubbleteaInterviewer implements both interfaces.
	var _ interface {
		Ask(string, []string, string) (string, error)
		AskFreeform(string) (string, error)
	} = &BubbleteaInterviewer{}
}

func TestBubbleteaInterviewerMode1IsDefault(t *testing.T) {
	bi := NewBubbleteaInterviewer()
	if bi.tuiProgram != nil {
		t.Error("expected nil tuiProgram for mode 1")
	}
}
```

Note: Full interactive testing of bubbletea programs requires `teatest` or manual testing. The unit tests verify interface compliance and construction. Integration testing happens with the showcase DOT file in Task 10.

**Step 2: Run test to verify it fails**

Run: `go test ./tui/ -run TestBubbletea -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// tui/interviewer.go
// ABOUTME: BubbleteaInterviewer implements Interviewer and FreeformInterviewer for pipeline human gates.
// ABOUTME: In mode 1 (headless), creates inline tea.Programs. In mode 2, delegates to the TUI modal.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/tui/components"
)

// BubbleteaInterviewer implements the pipeline handlers.Interviewer and
// handlers.FreeformInterviewer interfaces using bubbletea for styled input.
type BubbleteaInterviewer struct {
	tuiProgram *tea.Program   // nil in mode 1 (headless)
	responseCh chan string    // used in mode 2 for modal responses
}

// NewBubbleteaInterviewer creates a headless-mode (mode 1) interviewer.
func NewBubbleteaInterviewer() *BubbleteaInterviewer {
	return &BubbleteaInterviewer{}
}

// NewBubbleteaInterviewerWithTUI creates a mode 2 interviewer linked to a TUI program.
func NewBubbleteaInterviewerWithTUI(program *tea.Program, responseCh chan string) *BubbleteaInterviewer {
	return &BubbleteaInterviewer{
		tuiProgram: program,
		responseCh: responseCh,
	}
}

// Ask presents choices and returns the selected one.
func (bi *BubbleteaInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	if len(choices) == 0 {
		return "", fmt.Errorf("no choices available")
	}

	if bi.tuiProgram != nil {
		return bi.askViaTUI(prompt, choices, defaultChoice)
	}
	return bi.askInline(prompt, choices, defaultChoice)
}

// AskFreeform captures open-ended text input.
func (bi *BubbleteaInterviewer) AskFreeform(prompt string) (string, error) {
	if bi.tuiProgram != nil {
		return bi.askFreeformViaTUI(prompt)
	}
	return bi.askFreeformInline(prompt)
}

func (bi *BubbleteaInterviewer) askInline(prompt string, choices []string, defaultChoice string) (string, error) {
	model := components.NewChoiceModel(prompt, choices, defaultChoice)
	p := tea.NewProgram(model)
	result, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("choice input failed: %w", err)
	}
	final := result.(components.ChoiceModel)
	if final.Quitting() {
		return "", fmt.Errorf("user cancelled")
	}
	return final.Selected(), nil
}

func (bi *BubbleteaInterviewer) askFreeformInline(prompt string) (string, error) {
	model := components.NewFreeformModel(prompt)
	p := tea.NewProgram(model)
	result, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("freeform input failed: %w", err)
	}
	final := result.(components.FreeformModel)
	if final.Quitting() {
		return "", fmt.Errorf("user cancelled")
	}
	value := final.Value()
	if value == "" {
		return "", fmt.Errorf("empty input")
	}
	return value, nil
}

// Mode 2 methods — placeholder until Task 9 (TUI dashboard).
func (bi *BubbleteaInterviewer) askViaTUI(prompt string, choices []string, defaultChoice string) (string, error) {
	return "", fmt.Errorf("TUI mode not yet implemented")
}

func (bi *BubbleteaInterviewer) askFreeformViaTUI(prompt string) (string, error) {
	return "", fmt.Errorf("TUI mode not yet implemented")
}
```

**Step 4: Run tests**

Run: `go test ./tui/ -run TestBubbletea -v`
Expected: all PASS

**Step 5: Commit**

```bash
git add tui/interviewer.go tui/interviewer_test.go
git commit -m "feat: add BubbleteaInterviewer for mode 1 headless interaction"
```

---

## Task 5: Wire Mode 1 Into CLI

Replace `ConsoleInterviewer` with `BubbleteaInterviewer` in the tracker CLI. Add `--tui` flag (mode 2 stubbed for now).

**Files:**
- Modify: `cmd/tracker/main.go` (lines 23-30 for flags, line 121 for interviewer, lines 133-137 for event handler)

**Step 1: Update imports and add flag**

Add `--tui` flag alongside existing flags. Import `tui` package.

**Step 2: Replace interviewer creation**

Change line 121 from:
```go
interviewer := handlers.NewConsoleInterviewer()
```
to:
```go
interviewer := tui.NewBubbleteaInterviewer()
```

**Step 3: Stub the `--tui` flag**

```go
var tuiMode bool
flag.BoolVar(&tuiMode, "tui", false, "Full TUI dashboard mode")
```

If `tuiMode` is true, log a message that full TUI is not yet implemented and fall back to mode 1 for now.

**Step 4: Build and test manually**

Run: `go build -o ./bin/tracker ./cmd/tracker/ && ./bin/tracker examples/human_gate_showcase.dot`
Expected: styled arrow-key choice selection instead of numbered list

**Step 5: Commit**

```bash
git add cmd/tracker/main.go
git commit -m "feat: wire BubbleteaInterviewer as default in tracker CLI"
```

---

## Task 6: Token Tracking Middleware

Add middleware that accumulates per-provider token usage. This feeds the TUI header in mode 2 and is useful for mode 1 summary output too.

**Files:**
- Create: `llm/token_tracker.go`
- Create: `llm/token_tracker_test.go`

**Step 1: Write the failing test**

```go
// llm/token_tracker_test.go
package llm

import (
	"context"
	"sync"
	"testing"
)

func TestTokenTrackerAccumulatesUsage(t *testing.T) {
	tracker := NewTokenTracker()

	handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{
			Provider: "anthropic",
			Model:    "claude-sonnet-4-6",
			Text:     "hello",
			Usage:    Usage{InputTokens: 100, OutputTokens: 50},
		}, nil
	})

	req := &Request{Provider: "anthropic", Model: "claude-sonnet-4-6"}
	_, _ = handler(context.Background(), req)
	_, _ = handler(context.Background(), req)

	usage := tracker.ProviderUsage("anthropic")
	if usage.InputTokens != 200 {
		t.Errorf("expected 200 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 100 {
		t.Errorf("expected 100 output tokens, got %d", usage.OutputTokens)
	}
}

func TestTokenTrackerMultipleProviders(t *testing.T) {
	tracker := NewTokenTracker()

	for _, provider := range []string{"anthropic", "openai", "google"} {
		handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
			return &Response{
				Provider: provider,
				Usage:    Usage{InputTokens: 10, OutputTokens: 5},
			}, nil
		})
		_, _ = handler(context.Background(), &Request{Provider: provider})
	}

	providers := tracker.Providers()
	if len(providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(providers))
	}
}

func TestTokenTrackerConcurrentSafe(t *testing.T) {
	tracker := NewTokenTracker()
	handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{
			Provider: "anthropic",
			Usage:    Usage{InputTokens: 1, OutputTokens: 1},
		}, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = handler(context.Background(), &Request{Provider: "anthropic"})
		}()
	}
	wg.Wait()

	usage := tracker.ProviderUsage("anthropic")
	if usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", usage.InputTokens)
	}
}

func TestTokenTrackerTotalUsage(t *testing.T) {
	tracker := NewTokenTracker()

	for _, p := range []string{"anthropic", "openai"} {
		handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
			return &Response{Provider: p, Usage: Usage{InputTokens: 50, OutputTokens: 25}}
		})
		_, _ = handler(context.Background(), &Request{Provider: p})
	}

	total := tracker.TotalUsage()
	if total.InputTokens != 100 {
		t.Errorf("expected 100, got %d", total.InputTokens)
	}
	if total.OutputTokens != 50 {
		t.Errorf("expected 50, got %d", total.OutputTokens)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./llm/ -run TestTokenTracker -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// llm/token_tracker.go
// ABOUTME: Middleware that accumulates per-provider token usage across LLM calls.
// ABOUTME: Thread-safe; used by TUI dashboard to display real-time token counts.
package llm

import (
	"context"
	"sort"
	"sync"
)

// TokenTracker is a middleware that accumulates token usage per provider.
type TokenTracker struct {
	mu    sync.RWMutex
	usage map[string]*Usage
}

// NewTokenTracker creates a new token tracking middleware.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{usage: make(map[string]*Usage)}
}

// WrapComplete implements the Middleware interface.
func (t *TokenTracker) WrapComplete(next CompleteHandler) CompleteHandler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		resp, err := next(ctx, req)
		if err != nil {
			return resp, err
		}

		t.mu.Lock()
		defer t.mu.Unlock()

		provider := resp.Provider
		if provider == "" {
			provider = req.Provider
		}

		if _, ok := t.usage[provider]; !ok {
			t.usage[provider] = &Usage{}
		}
		t.usage[provider].Add(resp.Usage)

		return resp, nil
	}
}

// ProviderUsage returns accumulated usage for a specific provider.
func (t *TokenTracker) ProviderUsage(provider string) Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if u, ok := t.usage[provider]; ok {
		return *u
	}
	return Usage{}
}

// TotalUsage returns accumulated usage across all providers.
func (t *TokenTracker) TotalUsage() Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total Usage
	for _, u := range t.usage {
		total.Add(*u)
	}
	return total
}

// Providers returns sorted list of provider names with tracked usage.
func (t *TokenTracker) Providers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.usage))
	for name := range t.usage {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

**Step 4: Run tests**

Run: `go test ./llm/ -run TestTokenTracker -v`
Expected: all PASS

**Step 5: Wire into CLI**

In `cmd/tracker/main.go`, create a `TokenTracker` and add it as middleware to the LLM client:
```go
tokenTracker := llm.NewTokenTracker()
// Add WithMiddleware(tokenTracker) when creating the LLM client
```

**Step 6: Commit**

```bash
git add llm/token_tracker.go llm/token_tracker_test.go cmd/tracker/main.go
git commit -m "feat: add token tracking middleware for per-provider usage"
```

---

## Task 7: TUI Event Handler

Bridge pipeline events into bubbletea messages for the full TUI dashboard.

**Files:**
- Create: `tui/events.go`
- Create: `tui/events_test.go`

**Step 1: Write the failing test**

```go
// tui/events_test.go
package tui

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

func TestTUIEventHandlerImplementsInterface(t *testing.T) {
	var _ pipeline.PipelineEventHandler = &TUIEventHandler{}
}

func TestTUIEventHandlerSendsMessages(t *testing.T) {
	received := make(chan pipeline.PipelineEvent, 10)
	handler := NewTUIEventHandler(func(msg pipeline.PipelineEvent) {
		received <- msg
	})

	evt := pipeline.PipelineEvent{
		Type:    pipeline.EventStageStarted,
		NodeID:  "TestNode",
		Message: "running",
	}
	handler.HandlePipelineEvent(evt)

	select {
	case got := <-received:
		if got.NodeID != "TestNode" {
			t.Errorf("expected NodeID 'TestNode', got %q", got.NodeID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./tui/ -run TestTUIEventHandler -v`
Expected: FAIL

**Step 3: Write minimal implementation**

```go
// tui/events.go
// ABOUTME: Bridges pipeline events into the bubbletea TUI message loop.
// ABOUTME: Implements pipeline.PipelineEventHandler by forwarding events via a callback.
package tui

import (
	"github.com/2389-research/tracker/pipeline"
)

// TUIEventHandler implements pipeline.PipelineEventHandler and forwards events
// to the bubbletea program via a send function.
type TUIEventHandler struct {
	send func(pipeline.PipelineEvent)
}

// NewTUIEventHandler creates an event handler that forwards pipeline events
// via the given send function. In the full TUI, this calls program.Send().
func NewTUIEventHandler(send func(pipeline.PipelineEvent)) *TUIEventHandler {
	return &TUIEventHandler{send: send}
}

// HandlePipelineEvent forwards the event to the bubbletea program.
func (h *TUIEventHandler) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	if h.send != nil {
		h.send(evt)
	}
}
```

**Step 4: Run tests**

Run: `go test ./tui/ -run TestTUIEventHandler -v`
Expected: all PASS

**Step 5: Commit**

```bash
git add tui/events.go tui/events_test.go
git commit -m "feat: add TUI event handler bridging pipeline events to bubbletea"
```

---

## Task 8: Dashboard Components (Header, Node List, Agent Log)

Build the three visual components for the full TUI dashboard.

**Files:**
- Create: `tui/dashboard/header.go`
- Create: `tui/dashboard/header_test.go`
- Create: `tui/dashboard/nodelist.go`
- Create: `tui/dashboard/nodelist_test.go`
- Create: `tui/dashboard/agentlog.go`
- Create: `tui/dashboard/agentlog_test.go`

**Step 1: Write header tests**

Test that the header model renders pipeline name, elapsed time, and per-provider token counts. The header receives token data from `llm.TokenTracker` and pipeline status from events.

```go
// tui/dashboard/header_test.go
package dashboard

import (
	"strings"
	"testing"
	"time"
)

func TestHeaderRendersPipelineName(t *testing.T) {
	h := NewHeaderModel("MyPipeline")
	view := h.View(80)
	if !strings.Contains(view, "MyPipeline") {
		t.Errorf("expected pipeline name in view, got %q", view)
	}
}

func TestHeaderRendersProviderTokens(t *testing.T) {
	h := NewHeaderModel("Test")
	h.SetProviderUsage("anthropic", 1200, 300)
	h.SetProviderUsage("openai", 800, 200)
	view := h.View(80)
	if !strings.Contains(view, "anthropic") {
		t.Errorf("expected 'anthropic' in view")
	}
	if !strings.Contains(view, "1.2k") || !strings.Contains(view, "0.3k") {
		t.Errorf("expected formatted token counts, got %q", view)
	}
}

func TestHeaderRendersElapsedTime(t *testing.T) {
	h := NewHeaderModel("Test")
	h.SetElapsed(2*time.Minute + 14*time.Second)
	view := h.View(80)
	if !strings.Contains(view, "2m14s") {
		t.Errorf("expected elapsed time, got %q", view)
	}
}
```

**Step 2: Write node list tests**

```go
// tui/dashboard/nodelist_test.go
package dashboard

import (
	"strings"
	"testing"
)

func TestNodeListRendersNodes(t *testing.T) {
	nl := NewNodeListModel([]string{"Start", "Setup", "Implement", "Exit"})
	view := nl.View(20, 10)
	if !strings.Contains(view, "Start") {
		t.Error("expected 'Start' in view")
	}
}

func TestNodeListStatusIcons(t *testing.T) {
	nl := NewNodeListModel([]string{"A", "B", "C"})
	nl.SetStatus("A", NodeDone)
	nl.SetStatus("B", NodeRunning)
	view := nl.View(20, 10)
	if !strings.Contains(view, "✓") {
		t.Error("expected done icon")
	}
}
```

**Step 3: Write agent log tests**

```go
// tui/dashboard/agentlog_test.go
package dashboard

import (
	"strings"
	"testing"
)

func TestAgentLogAppendsEntries(t *testing.T) {
	al := NewAgentLogModel()
	al.Append("[ImplementClaude] Reading go.mod...")
	al.Append("[ImplementCodex] Running tests...")
	view := al.View(40, 10)
	if !strings.Contains(view, "Reading go.mod") {
		t.Error("expected log entry in view")
	}
}
```

**Step 4: Run tests — all should fail**

Run: `go test ./tui/dashboard/ -v`
Expected: FAIL

**Step 5: Implement all three components**

Implement `header.go`, `nodelist.go`, and `agentlog.go` with lipgloss styling. The header formats token counts as `1.2k` style. The node list uses status icons (`✓ ⟳ ✗ ○`). The agent log uses a bubbles viewport for scrolling.

Each component is a plain struct with a `View(width, height int) string` method — they are sub-models composed into the main TUI app, not standalone tea.Models.

**Step 6: Run tests**

Run: `go test ./tui/dashboard/ -v`
Expected: all PASS

**Step 7: Commit**

```bash
git add tui/dashboard/
git commit -m "feat: add TUI dashboard components — header, node list, agent log"
```

---

## Task 9: Modal Overlay Component

Build the modal that wraps choice/freeform components for mode 2 overlay presentation.

**Files:**
- Create: `tui/components/modal.go`
- Create: `tui/components/modal_test.go`

**Step 1: Write the failing test**

```go
// tui/components/modal_test.go
package components

import (
	"strings"
	"testing"
)

func TestModalRendersOverBackground(t *testing.T) {
	m := NewModal("Pick one", 60, 20)
	view := m.View("background content here")
	if !strings.Contains(view, "Pick one") {
		t.Error("expected modal content in view")
	}
}
```

**Step 2: Run test — should fail**

Run: `go test ./tui/components/ -run TestModal -v`

**Step 3: Implement**

The modal renders a lipgloss-bordered box centered over the background content. It takes inner content as a string and overlays it.

**Step 4: Run tests, commit**

```bash
git add tui/components/modal.go tui/components/modal_test.go
git commit -m "feat: add modal overlay component for TUI human gates"
```

---

## Task 10: Main TUI App (Mode 2)

Compose all dashboard components and modal into the main `tea.Model`. Wire up `BubbleteaInterviewer` mode 2 methods. Connect `TUIEventHandler`.

**Files:**
- Create: `tui/dashboard/app.go`
- Create: `tui/dashboard/app_test.go`
- Modify: `tui/interviewer.go` (implement `askViaTUI` and `askFreeformViaTUI`)

**Step 1: Write failing tests for app model**

Test that the app model initializes, handles pipeline events, shows modal on interview request, and hides modal on response.

**Step 2: Implement app.go**

The app model:
- Manages layout (header, split panes, optional modal)
- Receives `pipeline.PipelineEvent` as bubbletea messages via `Update`
- Updates header token counts, node list status, and agent log
- Shows/hides modal when interview events arrive
- Handles window resize via `tea.WindowSizeMsg`

**Step 3: Implement mode 2 interviewer methods**

In `tui/interviewer.go`, the `askViaTUI` methods:
1. Send a `ShowModalMsg` (choice or freeform) to the TUI program
2. Block on `responseCh` waiting for user input
3. Return the response

The TUI app model, on receiving `ShowModalMsg`, renders the modal. When the user submits, it sends the response back via `responseCh`.

**Step 4: Run tests**

Run: `go test ./tui/dashboard/ -v`

**Step 5: Commit**

```bash
git add tui/dashboard/app.go tui/dashboard/app_test.go tui/interviewer.go
git commit -m "feat: add main TUI dashboard app with modal interview support"
```

---

## Task 11: Wire Mode 2 Into CLI

Complete the `--tui` flag handling in main.go.

**Files:**
- Modify: `cmd/tracker/main.go`

**Step 1: Implement TUI mode startup**

When `--tui` is set:
1. Create the TUI `tea.Program` with the dashboard app model
2. Create `BubbleteaInterviewerWithTUI` linked to the program
3. Create `TUIEventHandler` that calls `program.Send()`
4. Run the pipeline inside a goroutine
5. Run `program.Run()` on the main thread (bubbletea owns the terminal)
6. When the pipeline goroutine finishes, send a completion message to the TUI

**Step 2: Test manually**

Run: `go build -o ./bin/tracker ./cmd/tracker/ && ./bin/tracker --tui examples/human_gate_showcase.dot`
Expected: full TUI with header, split panes, pipeline progress

**Step 3: Commit**

```bash
git add cmd/tracker/main.go
git commit -m "feat: wire --tui flag for full dashboard mode"
```

---

## Task 12: Integration Test With Showcase

Run the human gate showcase DOT file through both modes and verify everything works end-to-end.

**Files:**
- Existing: `examples/human_gate_showcase.dot`

**Step 1: Test mode 1 (headless)**

Run: `./bin/tracker examples/human_gate_showcase.dot`
Verify: All 5 gates work with styled bubbletea input — arrow keys for choices, text input for freeform.

**Step 2: Test mode 2 (full TUI)**

Run: `./bin/tracker --tui examples/human_gate_showcase.dot`
Verify: Dashboard shows pipeline progress, modals appear for gates, token counts update in header.

**Step 3: Test ask_and_execute pipeline**

Run: `./bin/tracker examples/ask_and_execute.dot`
Verify: Freeform gate captures input, pipeline proceeds to interpret and implement phases.

**Step 4: Verify all tests pass**

Run: `go test ./...`
Expected: all PASS

**Step 5: Final commit if any cleanup needed**

```bash
git commit -m "test: verify bubbletea TUI integration with showcase pipelines"
```

---

## Summary

| Task | Component | Estimated Scope |
|------|-----------|----------------|
| 1 | Add dependencies | Trivial |
| 2 | Choice component | Small |
| 3 | Freeform component | Small |
| 4 | BubbleteaInterviewer (mode 1) | Medium |
| 5 | Wire mode 1 into CLI | Small |
| 6 | Token tracking middleware | Medium |
| 7 | TUI event handler | Small |
| 8 | Dashboard components | Medium-Large |
| 9 | Modal overlay | Small |
| 10 | Main TUI app (mode 2) | Large |
| 11 | Wire mode 2 into CLI | Medium |
| 12 | Integration testing | Medium |
