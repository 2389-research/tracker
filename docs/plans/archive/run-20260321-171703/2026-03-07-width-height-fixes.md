# Width/Height Adaptive Fixes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make TUI components width-adaptive, fix NodeList overflow on tiny terminals, and prevent ANSI leakage in non-TTY console output.

**Architecture:** Thread terminal width from the dashboard through modal components. Add plain-text rendering for the console path. Harden NodeList for small heights.

**Tech Stack:** Go, bubbletea, lipgloss, glamour

---

### Task 1: PromptPlain — plain text word wrapping

**Files:**
- Modify: `tui/render/prompt.go`
- Test: `tui/render/prompt_test.go`

**Step 1: Write the failing test**

Add to `tui/render/prompt_test.go`:

```go
func TestPromptPlainWrapsWithoutANSI(t *testing.T) {
	long := strings.Repeat("word ", 40) // ~200 chars
	rendered := PromptPlain(long, 60)

	for _, line := range strings.Split(rendered, "\n") {
		if len(line) > 60 {
			t.Errorf("line too long (%d chars): %q", len(line), line)
		}
	}
	// Must not contain ANSI escape sequences
	if strings.Contains(rendered, "\033") {
		t.Error("PromptPlain output contains ANSI escape sequences")
	}
}

func TestPromptPlainHandlesEmptyString(t *testing.T) {
	rendered := PromptPlain("", 80)
	if strings.TrimSpace(rendered) != "" {
		t.Errorf("expected empty output for empty input, got %q", rendered)
	}
}

func TestPromptPlainPreservesShortText(t *testing.T) {
	input := "Just a question."
	rendered := PromptPlain(input, 80)
	if !strings.Contains(rendered, "Just a question.") {
		t.Errorf("expected input text preserved, got %q", rendered)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./tui/render/ -run TestPromptPlain -v`
Expected: FAIL — `PromptPlain` undefined

**Step 3: Write minimal implementation**

Add to `tui/render/prompt.go`:

```go
// PromptPlain renders a prompt string with word wrapping but no ANSI styling.
// Used by ConsoleInterviewer so piped/CI output stays free of escape sequences.
func PromptPlain(prompt string, width int) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}

	var lines []string
	for _, paragraph := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(paragraph) == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, wrapLine(paragraph, width)...)
	}
	return strings.Join(lines, "\n")
}

// wrapLine splits a single paragraph into lines that fit within width.
func wrapLine(line string, width int) []string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	lines = append(lines, current)
	return lines
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./tui/render/ -v`
Expected: PASS

**Step 5: Commit**

```
git add tui/render/prompt.go tui/render/prompt_test.go
git commit -m "feat(render): add PromptPlain for ANSI-free word wrapping"
```

---

### Task 2: ConsoleInterviewer uses PromptPlain

**Files:**
- Modify: `pipeline/handlers/human.go:119,168`
- Test: `pipeline/handlers/human_test.go`

**Step 1: Write the failing test**

Add to `pipeline/handlers/human_test.go`:

```go
func TestConsoleInterviewerAskOutputHasNoANSI(t *testing.T) {
	var out bytes.Buffer
	ci := &ConsoleInterviewer{
		Reader: strings.NewReader("approve\n"),
		Writer: &out,
	}
	_, err := ci.Ask("Pick one", []string{"approve", "reject"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\033") {
		t.Error("console output contains ANSI escape sequences")
	}
}

func TestConsoleInterviewerAskFreeformOutputHasNoANSI(t *testing.T) {
	var out bytes.Buffer
	ci := &ConsoleInterviewer{
		Reader: strings.NewReader("my response\n"),
		Writer: &out,
	}
	_, err := ci.AskFreeform("Tell me something")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\033") {
		t.Error("console output contains ANSI escape sequences")
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run TestConsoleInterviewer.*NoANSI -v`
Expected: FAIL — glamour output contains `\033` sequences

**Step 3: Switch to PromptPlain**

In `pipeline/handlers/human.go`, change line 119:
```go
// FROM:
fmt.Fprintf(c.Writer, "\n%s\n", render.Prompt(prompt, 76))
// TO:
fmt.Fprintf(c.Writer, "\n%s\n", render.PromptPlain(prompt, 76))
```

Change line 168:
```go
// FROM:
fmt.Fprintf(c.Writer, "\n%s\n> ", render.Prompt(prompt, 76))
// TO:
fmt.Fprintf(c.Writer, "\n%s\n> ", render.PromptPlain(prompt, 76))
```

**Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/handlers/ -v`
Expected: PASS

**Step 5: Commit**

```
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "fix(human): use plain text rendering in ConsoleInterviewer"
```

---

### Task 3: Width-adaptive ChoiceModel

**Files:**
- Modify: `tui/components/choice.go:29-57,109`
- Test: `tui/components/choice_test.go`

**Step 1: Write the failing test**

Add to `tui/components/choice_test.go`:

```go
func TestChoiceModelSetWidthAffectsPromptRendering(t *testing.T) {
	long := strings.Repeat("word ", 40)
	m := NewChoiceModel(long, []string{"yes", "no"}, "")
	m.SetWidth(40)
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		// Allow some margin for glamour/lipgloss padding
		plain := stripANSI(line)
		if len(plain) > 50 {
			t.Errorf("line too long (%d chars) for width=40: %q", len(plain), plain)
		}
	}
}

func TestChoiceModelDefaultWidthIs76(t *testing.T) {
	m := NewChoiceModel("test", []string{"a"}, "")
	if m.width != 76 {
		t.Errorf("expected default width=76, got %d", m.width)
	}
}
```

Also add this helper at the bottom of the test file:
```go
func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./tui/components/ -run TestChoiceModel.*Width -v`
Expected: FAIL — `SetWidth` undefined, `m.width` not exported

**Step 3: Add width field and SetWidth to ChoiceModel**

In `tui/components/choice.go`:

Add `width` field to ChoiceModel struct (after `selected`):
```go
width         int
```

Set default in `NewChoiceModel` return:
```go
width:        76,
```

Add method:
```go
// SetWidth updates the width used for rendering the prompt.
func (m *ChoiceModel) SetWidth(w int) { m.width = w }
```

Change line 109 from `render.Prompt(m.prompt, 76)` to `render.Prompt(m.prompt, m.width)`.

**Step 4: Run tests to verify they pass**

Run: `go test ./tui/components/ -v`
Expected: PASS

**Step 5: Commit**

```
git add tui/components/choice.go tui/components/choice_test.go
git commit -m "feat(choice): add SetWidth for adaptive prompt rendering"
```

---

### Task 4: Width-adaptive FreeformModel

**Files:**
- Modify: `tui/components/freeform.go:31-51,90`
- Test: `tui/components/freeform_test.go`

**Step 1: Write the failing test**

Add to `tui/components/freeform_test.go`:

```go
func TestFreeformModelSetWidthAffectsPromptRendering(t *testing.T) {
	long := strings.Repeat("word ", 40)
	m := NewFreeformModel(long)
	m.SetWidth(40)
	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		plain := stripANSI(line)
		if len(plain) > 50 {
			t.Errorf("line too long (%d chars) for width=40: %q", len(plain), plain)
		}
	}
}

func TestFreeformModelDefaultWidthIs76(t *testing.T) {
	m := NewFreeformModel("test")
	if m.width != 76 {
		t.Errorf("expected default width=76, got %d", m.width)
	}
}
```

Also add the `stripANSI` helper (same as choice_test.go).

**Step 2: Run tests to verify they fail**

Run: `go test ./tui/components/ -run TestFreeformModel.*Width -v`
Expected: FAIL — `SetWidth` undefined

**Step 3: Add width field and SetWidth to FreeformModel**

In `tui/components/freeform.go`:

Add `width` field to FreeformModel struct (after `err`):
```go
width     int
```

Set default in `NewFreeformModel` return and adapt textinput width:
```go
return FreeformModel{
	prompt: prompt,
	input:  ti,
	width:  76,
}
```

Add method:
```go
// SetWidth updates the width used for rendering the prompt and text input.
func (m *FreeformModel) SetWidth(w int) {
	m.width = w
	m.input.Width = w - 4 // account for freeformBorderStyle padding
}
```

Change line 90 from `render.Prompt(m.prompt, 76)` to `render.Prompt(m.prompt, m.width)`.

**Step 4: Run tests to verify they pass**

Run: `go test ./tui/components/ -v`
Expected: PASS

**Step 5: Commit**

```
git add tui/components/freeform.go tui/components/freeform_test.go
git commit -m "feat(freeform): add SetWidth for adaptive prompt rendering"
```

---

### Task 5: Wire width from dashboard to modal components

**Files:**
- Modify: `tui/dashboard/app.go:194,201,358`

**Step 1: No failing test needed**

This is pure wiring — the component tests already verify width behavior. Integration testing would require a full bubbletea harness.

**Step 2: Compute and pass modal content width**

In `tui/dashboard/app.go`, add a helper method:

```go
// modalContentWidth returns the width available inside the modal chrome.
// DoubleBorder = 2 chars + Padding(1,2) = 4 chars = 6 total horizontal.
func (a AppModel) modalContentWidth() int {
	w := a.width - 6
	if w < 20 {
		w = 20
	}
	return w
}
```

At line 194, after creating the choice modal, call SetWidth:
```go
a.choiceModal = components.NewChoiceModel(msg.Prompt, msg.Choices, msg.DefaultChoice)
a.choiceModal.SetWidth(a.modalContentWidth())
```

At line 201, after creating the freeform modal, call SetWidth:
```go
a.freeformModal = components.NewFreeformModel(msg.Prompt)
a.freeformModal.SetWidth(a.modalContentWidth())
```

**Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS

**Step 4: Commit**

```
git add tui/dashboard/app.go
git commit -m "feat(dashboard): wire terminal width into modal components"
```

---

### Task 6: NodeList graceful degradation for tiny heights

**Files:**
- Modify: `tui/dashboard/nodelist.go:77-116`
- Test: `tui/dashboard/nodelist_test.go`

**Step 1: Write the failing tests**

Add to `tui/dashboard/nodelist_test.go`:

```go
func TestNodeListHeight1ShowsOnlyHeader(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "a", Label: "Alpha", Status: NodePending},
		{ID: "b", Label: "Beta", Status: NodeRunning},
	}
	nl := NewNodeListModel(nodes)
	nl.SetHeight(1)
	nl.SetWidth(40)

	view := nl.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) > 1 {
		t.Errorf("height=1: expected at most 1 line, got %d:\n%s", len(lines), view)
	}
	if !strings.Contains(view, "PIPELINE") {
		t.Error("height=1: expected PIPELINE header")
	}
}

func TestNodeListHeight2ShowsHeaderAndOneNode(t *testing.T) {
	nodes := []NodeEntry{
		{ID: "a", Label: "Alpha", Status: NodePending},
		{ID: "b", Label: "Beta", Status: NodeRunning},
		{ID: "c", Label: "Gamma", Status: NodePending},
	}
	nl := NewNodeListModel(nodes)
	nl.SetHeight(2)
	nl.SetWidth(40)

	view := nl.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) > 2 {
		t.Errorf("height=2: expected at most 2 lines, got %d:\n%s", len(lines), view)
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./tui/dashboard/ -run TestNodeListHeight -v`
Expected: FAIL — too many lines rendered

**Step 3: Fix visibleWindow for tiny heights**

In `tui/dashboard/nodelist.go`, replace the `visibleWindow()` method:

Add early return for tiny heights at the top of `visibleWindow()`, after the empty-nodes check:

```go
// Not enough room for header + any nodes
if n.height > 0 && n.height < 2 {
	return 0, 0, false, false
}
```

And in `View()`, handle the empty window case when height is set:

After the header line, before the `if len(n.nodes) == 0` check, the existing code is fine. But we need to handle `start == end` from a height constraint:

After `start, end, upInd, downInd := n.visibleWindow()`, add:
```go
if start == end && len(n.nodes) > 0 {
	return sb.String()
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./tui/dashboard/ -v`
Expected: PASS

**Step 5: Commit**

```
git add tui/dashboard/nodelist.go tui/dashboard/nodelist_test.go
git commit -m "fix(nodelist): graceful degradation for tiny terminal heights"
```

---

### Task 7: Final integration test run

**Step 1: Run full test suite**

Run: `go test ./... -count=1`
Expected: All 15 packages PASS

**Step 2: Commit all remaining changes if any**

Should be clean if each task committed along the way.
