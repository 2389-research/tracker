# Scrollable Modal Prompts Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make modal prompt text scrollable so that long prompts don't overflow the terminal, keeping interactive elements (choices, text input, hints) always visible at the bottom.

**Architecture:** Add a `height` field and `SetHeight()` to ChoiceModel and FreeformModel. When height is set, render the prompt text inside a `bubbles/viewport` for scrolling, and render interactive elements (choice list / text input / hints) in a fixed region below. The dashboard computes available modal content height and passes it via `SetHeight()`. When height=0, behavior is unchanged (no viewport, backward-compatible).

**Tech Stack:** Go, bubbletea, bubbles/viewport, lipgloss

---

### Task 1: ChoiceModel — add SetHeight and viewport scrolling

**Files:**
- Modify: `tui/components/choice.go:28-131`
- Test: `tui/components/choice_test.go`

**Step 1: Write the failing tests**

Add to `tui/components/choice_test.go`:

```go
func TestChoiceModelSetHeightClampsOutput(t *testing.T) {
	// Long prompt that would normally produce many lines.
	long := strings.Repeat("This is a long prompt sentence. ", 20)
	m := NewChoiceModel(long, []string{"yes", "no", "maybe"}, "")
	m.SetWidth(40)
	m.SetHeight(10)
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 10 {
		t.Errorf("height=10: expected at most 10 lines, got %d", len(lines))
	}
}

func TestChoiceModelSetHeightZeroIsUnbounded(t *testing.T) {
	long := strings.Repeat("word ", 40)
	m := NewChoiceModel(long, []string{"a", "b"}, "")
	m.SetWidth(40)
	// height=0 means no viewport, backward-compat.
	view := m.View()
	if !strings.Contains(view, "word") {
		t.Error("expected prompt content in view")
	}
}

func TestChoiceModelScrollKeysUpdateViewport(t *testing.T) {
	long := strings.Repeat("This is a long prompt sentence. ", 30)
	m := NewChoiceModel(long, []string{"yes", "no"}, "")
	m.SetWidth(40)
	m.SetHeight(8)
	// Render once to initialize viewport content.
	_ = m.View()
	// pgdown should scroll the viewport without selecting.
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	cm := m2.(ChoiceModel)
	if cm.IsDone() {
		t.Error("pgdown should not select a choice")
	}
	// Choices and hints should still be visible.
	view := cm.View()
	if !strings.Contains(view, "yes") {
		t.Error("expected choices visible after scroll")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./tui/components/ -run "TestChoiceModelSetHeight|TestChoiceModelScrollKeys" -v`
Expected: FAIL — `SetHeight` undefined, viewport not used

**Step 3: Implement SetHeight and viewport in ChoiceModel**

In `tui/components/choice.go`:

Add import for `"github.com/charmbracelet/bubbles/viewport"`.

Add `height` and `viewport` fields to `ChoiceModel`:
```go
type ChoiceModel struct {
	prompt        string
	choices       []string
	cursor        int
	defaultChoice string
	done          bool
	cancelled     bool
	selected      string
	width         int
	height        int
	vp            viewport.Model
	vpReady       bool
}
```

Add `SetHeight` method:
```go
// SetHeight sets the total height available for the component.
// When height > 0, the prompt text is rendered in a scrollable viewport
// with interactive elements fixed below. When height = 0, no viewport
// is used (backward-compatible).
func (m *ChoiceModel) SetHeight(h int) { m.height = h }
```

Update `Update` to route scroll keys to the viewport and other keys to choice navigation:
```go
func (m ChoiceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Scroll keys go to viewport when height-constrained.
		if m.height > 0 && m.vpReady {
			switch msg.Type {
			case tea.KeyPgUp, tea.KeyPgDown:
				var cmd tea.Cmd
				m.vp, cmd = m.vp.Update(msg)
				return m, cmd
			}
		}
		switch msg.String() {
		// ... existing navigation/selection cases unchanged ...
		}
	case tea.MouseMsg:
		if m.height > 0 && m.vpReady {
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}
	return m, nil
}
```

Update `View` to use viewport when `height > 0`:
```go
func (m ChoiceModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	// Build interactive elements (choices + hints).
	var interSB strings.Builder
	for i, choice := range m.choices {
		if i == m.cursor {
			cursor := choiceCursorStyle.Render("▶ ")
			interSB.WriteString(choiceSelectedStyle.Render(cursor + choice))
		} else {
			interSB.WriteString(choiceNormalStyle.Render("  " + choice))
		}
		interSB.WriteString("\n")
	}
	interSB.WriteString("\n")
	scrollHint := ""
	if m.height > 0 {
		scrollHint = "pgup/pgdn scroll  "
	}
	interSB.WriteString(lipgloss.NewStyle().Faint(true).Render(scrollHint + "↑/↓ navigate  enter select  esc cancel"))
	interactive := interSB.String()

	// No height constraint — render everything inline (backward-compat).
	if m.height <= 0 {
		var sb strings.Builder
		sb.WriteString(render.Prompt(m.prompt, m.width))
		sb.WriteString("\n\n")
		sb.WriteString(interactive)
		return sb.String()
	}

	// Height-constrained — prompt in viewport, interactive fixed below.
	interactiveLines := strings.Count(interactive, "\n") + 1
	vpHeight := m.height - interactiveLines - 1 // -1 for blank separator line
	if vpHeight < 1 {
		vpHeight = 1
	}

	if !m.vpReady {
		m.vp = viewport.New(m.width, vpHeight)
		m.vp.SetContent(render.Prompt(m.prompt, m.width))
		m.vpReady = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpHeight
	}

	var sb strings.Builder
	sb.WriteString(m.vp.View())
	sb.WriteString("\n")
	sb.WriteString(interactive)
	return sb.String()
}
```

**Important:** The `View()` method in bubbletea must be pure (no side effects on the receiver). Since `ChoiceModel` is a value type in the Update/View interface, the viewport initialization inside View won't persist. The viewport needs to be initialized on the *first* call to `Update` or in a dedicated method. A practical approach: initialize the viewport lazily in `View()` — since `View()` operates on a copy, the `vpReady` flag will be set on each call. This is fine because `viewport.New` + `SetContent` is cheap, and the viewport position (`YOffset`) is updated via `Update()` where mutations DO persist.

**Step 4: Run tests to verify they pass**

Run: `go test ./tui/components/ -v`
Expected: PASS

**Step 5: Commit**

```
git add tui/components/choice.go tui/components/choice_test.go
git commit -m "feat(choice): add SetHeight with viewport scrolling for long prompts"
```

---

### Task 2: FreeformModel — add SetHeight and viewport scrolling

**Files:**
- Modify: `tui/components/freeform.go:30-119`
- Test: `tui/components/freeform_test.go`

**Step 1: Write the failing tests**

Add to `tui/components/freeform_test.go`:

```go
func TestFreeformModelSetHeightClampsOutput(t *testing.T) {
	long := strings.Repeat("This is a long prompt sentence. ", 20)
	m := NewFreeformModel(long)
	m.SetWidth(40)
	m.SetHeight(10)
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) > 10 {
		t.Errorf("height=10: expected at most 10 lines, got %d", len(lines))
	}
}

func TestFreeformModelSetHeightZeroIsUnbounded(t *testing.T) {
	long := strings.Repeat("word ", 40)
	m := NewFreeformModel(long)
	m.SetWidth(40)
	view := m.View()
	if !strings.Contains(view, "word") {
		t.Error("expected prompt content in view")
	}
}

func TestFreeformModelScrollKeysUpdateViewport(t *testing.T) {
	long := strings.Repeat("This is a long prompt sentence. ", 30)
	m := NewFreeformModel(long)
	m.SetWidth(40)
	m.SetHeight(8)
	_ = m.View()
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	fm := m2.(FreeformModel)
	if fm.IsDone() {
		t.Error("pgdown should not submit")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./tui/components/ -run "TestFreeformModelSetHeight|TestFreeformModelScrollKeys" -v`
Expected: FAIL — `SetHeight` undefined

**Step 3: Implement SetHeight and viewport in FreeformModel**

In `tui/components/freeform.go`:

Add import for `"github.com/charmbracelet/bubbles/viewport"`.

Add `height` and viewport fields to `FreeformModel`:
```go
type FreeformModel struct {
	prompt    string
	input     textinput.Model
	done      bool
	cancelled bool
	err       string
	width     int
	height    int
	vp        viewport.Model
	vpReady   bool
}
```

Add `SetHeight`:
```go
// SetHeight sets the total height available for the component.
// When height > 0, the prompt text is rendered in a scrollable viewport
// with the text input fixed below.
func (m *FreeformModel) SetHeight(h int) { m.height = h }
```

Update `Update` to route pgup/pgdown and mouse to viewport:
```go
func (m FreeformModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.height > 0 && m.vpReady {
			switch msg.Type {
			case tea.KeyPgUp, tea.KeyPgDown:
				var cmd tea.Cmd
				m.vp, cmd = m.vp.Update(msg)
				return m, cmd
			}
		}
		switch msg.Type {
		case tea.KeyEnter:
			// ... existing enter logic unchanged ...
		case tea.KeyEsc, tea.KeyCtrlC:
			// ... existing esc logic unchanged ...
		}
	case tea.MouseMsg:
		if m.height > 0 && m.vpReady {
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
```

Update `View` to use viewport when height > 0:
```go
func (m FreeformModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	// Build fixed interactive elements.
	var interSB strings.Builder
	interSB.WriteString(freeformBorderStyle.Width(m.width - 4).Render(m.input.View()))
	interSB.WriteString("\n")
	if m.err != "" {
		interSB.WriteString(freeformErrorStyle.Render(m.err))
		interSB.WriteString("\n")
	}
	scrollHint := ""
	if m.height > 0 {
		scrollHint = "pgup/pgdn scroll  "
	}
	interSB.WriteString(lipgloss.NewStyle().Faint(true).Render(scrollHint + "enter submit  esc cancel"))
	interactive := interSB.String()

	if m.height <= 0 {
		var sb strings.Builder
		sb.WriteString(render.Prompt(m.prompt, m.width))
		sb.WriteString("\n\n")
		sb.WriteString(interactive)
		return sb.String()
	}

	interactiveLines := strings.Count(interactive, "\n") + 1
	vpHeight := m.height - interactiveLines - 1
	if vpHeight < 1 {
		vpHeight = 1
	}

	if !m.vpReady {
		m.vp = viewport.New(m.width, vpHeight)
		m.vp.SetContent(render.Prompt(m.prompt, m.width))
		m.vpReady = true
	} else {
		m.vp.Width = m.width
		m.vp.Height = vpHeight
	}

	var sb strings.Builder
	sb.WriteString(m.vp.View())
	sb.WriteString("\n")
	sb.WriteString(interactive)
	return sb.String()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./tui/components/ -v`
Expected: PASS

**Step 5: Commit**

```
git add tui/components/freeform.go tui/components/freeform_test.go
git commit -m "feat(freeform): add SetHeight with viewport scrolling for long prompts"
```

---

### Task 3: Wire height from dashboard into modal components

**Files:**
- Modify: `tui/dashboard/app.go:191-205,447-455`

**Step 1: No failing test needed**

This is pure wiring — the component tests already verify height behavior. The dashboard test would require a full bubbletea harness.

**Step 2: Add modalContentHeight helper**

In `tui/dashboard/app.go`, add a helper method near `modalContentWidth()`:

```go
// modalContentHeight returns the height available inside the modal chrome.
// DoubleBorder = 2 chars + Padding(1,0) = 2 chars = 4 total vertical,
// plus 1 for the title line and 1 for the title margin.
func (a AppModel) modalContentHeight() int {
	h := a.height - 6
	if h < 5 {
		h = 5
	}
	return h
}
```

**Step 3: Call SetHeight when creating modals**

At line ~194-195, after creating the choice modal:
```go
a.choiceModal = components.NewChoiceModel(msg.Prompt, msg.Choices, msg.DefaultChoice)
a.choiceModal.SetWidth(a.modalContentWidth())
a.choiceModal.SetHeight(a.modalContentHeight())
```

At line ~202-203, after creating the freeform modal:
```go
a.freeformModal = components.NewFreeformModel(msg.Prompt)
a.freeformModal.SetWidth(a.modalContentWidth())
a.freeformModal.SetHeight(a.modalContentHeight())
```

**Step 4: Route mouse events to modal when active**

In `Update`, ensure `tea.MouseMsg` is forwarded to the active modal. Check the existing key routing — if mouse events are already forwarded via the modal's `Update`, this may already work. If not, add a case for `tea.MouseMsg` in the main `Update` that delegates to `updateModal` equivalent for mouse.

**Step 5: Run full test suite**

Run: `go test ./...`
Expected: PASS

**Step 6: Commit**

```
git add tui/dashboard/app.go
git commit -m "feat(dashboard): wire terminal height into modal components for scrollable prompts"
```

---

### Task 4: Final integration test run

**Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: All packages PASS

**Step 2: Manual smoke test**

Run a pipeline with `--tui` that has a long prompt and verify:
- Prompt text scrolls with pgup/pgdn
- Choice list / text input stays fixed at the bottom
- Mouse wheel scrolls the prompt viewport
- Enter/Esc still work normally
- Short prompts that fit in the viewport render without scrollbars
