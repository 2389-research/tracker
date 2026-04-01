# Interview Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `mode: interview` for human nodes — parse upstream agent markdown for questions, present each as a terminal form field with inline options + freeform escape hatches, store structured JSON answers + markdown summary to context.

**Architecture:** New `InterviewInterviewer` interface extends the existing interviewer hierarchy. The handler reads questions from context, parses them with a pure-function parser, and delegates presentation to the interviewer. A new `InterviewContent` TUI modal (fullscreen, bubbles-based) renders multi-field forms with pagination. A new `MsgGateInterview` message type bridges handler→TUI, following the established `MsgGateChoice`/`MsgGateFreeform` pattern.

**Tech Stack:** Go, charmbracelet/bubbles (textarea, viewport), lipgloss. No new dependencies (NOT huh — the codebase uses bubbles throughout).

**Worktree:** `/home/clint/code/tracker/.worktrees/feat-interview-mode` (branch `feat/interview-mode` from `fix/p0-critical-safety-fixes`)

**dippin-lang:** v0.15.0-beta.1 (already updated in go.mod)

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `pipeline/handlers/interview_parse.go` | Create | Question parser — pure functions, no side effects |
| `pipeline/handlers/interview_parse_test.go` | Create | Parser tests |
| `pipeline/handlers/interview_result.go` | Create | Answer/Result types, JSON serialization, markdown summary builder |
| `pipeline/handlers/interview_result_test.go` | Create | Serialization + summary tests |
| `pipeline/handlers/human.go` | Modify | Add `InterviewInterviewer` interface, `executeInterview()` method, routing |
| `pipeline/handlers/human_test.go` | Modify | Add interview handler tests with mock interviewers |
| `pipeline/dippin_adapter.go:281-288` | Modify | Extract `QuestionsKey`, `AnswersKey`, `Prompt` attrs |
| `pipeline/dippin_adapter_test.go` | Modify | Test new attrs extraction |
| `tui/interview_content.go` | Create | Fullscreen multi-field interview form (bubbles-based) |
| `tui/interview_content_test.go` | Create | Content type tests |
| `tui/messages.go` | Modify | Add `MsgGateInterview` message type |
| `tui/interviewer.go` | Modify | Add `AskInterview` to `BubbleteaInterviewer` (Mode 1 + Mode 2) |
| `tui/app.go:220-237` | Modify | Handle `MsgGateInterview` in `handleModalMsg` |
| `pipeline/handlers/autopilot.go` | Modify | Add `AskInterview` to `AutopilotInterviewer` |
| `pipeline/handlers/autopilot_claudecode.go` | Modify | Add `AskInterview` to `ClaudeCodeAutopilotInterviewer` |
| `tui/autopilot_interviewer.go` | Modify | Add `AskInterview` to `AutopilotTUIInterviewer` |

---

## Data Types

All defined in `pipeline/handlers/interview_result.go`:

```go
// Question represents a parsed interview question.
type Question struct {
    Index   int      // 1-based ordinal
    Text    string   // Question text (markdown stripped for labels, code spans preserved)
    Options []string // Inline options from trailing parentheticals; empty for open-ended
    IsYesNo bool     // Detected yes/no pattern
}

// InterviewAnswer represents a user's response to one question.
type InterviewAnswer struct {
    ID          string   `json:"id"`          // "q1", "q2", ...
    Text        string   `json:"text"`        // Original question text
    Options     []string `json:"options,omitempty"`
    Answer      string   `json:"answer"`
    Elaboration string   `json:"elaboration,omitempty"`
}

// InterviewResult is the complete response serialized to context.
type InterviewResult struct {
    Questions  []InterviewAnswer `json:"questions"`
    Incomplete bool              `json:"incomplete"`
    Canceled   bool              `json:"canceled"`
}
```

---

## Task 1: Question Parser

**Files:**
- Create: `pipeline/handlers/interview_parse.go`
- Test: `pipeline/handlers/interview_parse_test.go`

- [ ] **Step 1: Write parser tests**

Test `ParseQuestions(markdown string) []Question` with these cases:
- Numbered items: `1. Who are the consumers? (internal, external, both)`
- Bullet items ending in `?`: `- What auth model?`
- Imperative prompts: `Describe any existing integrations.`
- Trailing parenthetical extraction: `Scale? (low <1k/day, medium 1k-100k/day)` → 3 options
- Yes/no detection: `Need real-time? (yes, no)` → `IsYesNo: true`
- Skip fenced code blocks: questions inside ``` blocks are ignored
- Skip preamble/commentary: non-question lines between questions are ignored
- Empty input → empty slice
- No parseable questions → empty slice (triggers fallback in handler)
- Multi-line numbered item: entire item is one question
- Mixed formats in same document

```go
func TestParseQuestions_Numbered(t *testing.T) {
    md := "1. Who are the API consumers? (internal services, third-party devs, mobile app)\n2. What operations go beyond CRUD?\n3. Describe any existing integrations."
    qs := ParseQuestions(md)
    assert(len(qs) == 3)
    assert(qs[0].Text == "Who are the API consumers?")
    assert(qs[0].Options == ["internal services", "third-party devs", "mobile app"])
    assert(qs[1].Text == "What operations go beyond CRUD?")
    assert(len(qs[1].Options) == 0)
    assert(qs[2].Text == "Describe any existing integrations.")
    assert(len(qs[2].Options) == 0)
}

func TestParseQuestions_SkipCodeBlocks(t *testing.T) {
    md := "1. What format?\n```\n2. This is code not a question?\n```\n3. What scale?"
    qs := ParseQuestions(md)
    assert(len(qs) == 2) // skips line inside code block
}

func TestParseQuestions_YesNo(t *testing.T) {
    md := "1. Need real-time updates? (yes, no)"
    qs := ParseQuestions(md)
    assert(qs[0].IsYesNo == true)
}

func TestParseQuestions_Empty(t *testing.T) {
    qs := ParseQuestions("")
    assert(len(qs) == 0)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/clint/code/tracker/.worktrees/feat-interview-mode && go test ./pipeline/handlers/ -run TestParseQuestions -v`
Expected: compilation error (functions don't exist yet)

- [ ] **Step 3: Implement parser**

```go
// pipeline/handlers/interview_parse.go
package handlers

import (
    "regexp"
    "strings"
)

var (
    reNumbered   = regexp.MustCompile(`^\s*\d+[.)]\s+(.+)`)
    reBulletQ    = regexp.MustCompile(`^\s*[-*]\s+(.+\?)\s*$`)
    reImperative = regexp.MustCompile(`(?i)^\s*[-*]?\s*(describe|explain|list|specify|provide|choose|select|confirm|rate|rank)\b`)
    reOptions    = regexp.MustCompile(`\(([^)]+)\)\s*$`)
    reFence      = regexp.MustCompile("^\\s*```")
)

func ParseQuestions(markdown string) []Question {
    // Split lines, skip fenced code blocks, match patterns, extract options
}

func extractInlineOptions(text string) (string, []string) {
    // Match trailing (opt1, opt2, opt3), split by comma
    // Return cleaned text + options slice
}

func isYesNoQuestion(options []string) bool {
    // Check if options are exactly [yes, no] or [yes, no, maybe] etc.
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/clint/code/tracker/.worktrees/feat-interview-mode && go test ./pipeline/handlers/ -run TestParseQuestions -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/interview_parse.go pipeline/handlers/interview_parse_test.go
git commit -m "feat: add markdown question parser for interview mode"
```

---

## Task 2: Answer Types and Serialization

**Files:**
- Create: `pipeline/handlers/interview_result.go`
- Test: `pipeline/handlers/interview_result_test.go`

- [ ] **Step 1: Write serialization tests**

```go
func TestSerializeInterviewResult(t *testing.T) {
    result := InterviewResult{
        Questions: []InterviewAnswer{
            {ID: "q1", Text: "Auth model?", Options: []string{"API key", "OAuth"}, Answer: "OAuth", Elaboration: "Google SSO"},
            {ID: "q2", Text: "Describe integrations", Answer: "Salesforce sync"},
        },
    }
    json := SerializeInterviewResult(result)
    // verify valid JSON, round-trips correctly
    back, err := DeserializeInterviewResult(json)
    assert(err == nil)
    assert(len(back.Questions) == 2)
    assert(back.Questions[0].Elaboration == "Google SSO")
}

func TestBuildMarkdownSummary(t *testing.T) {
    result := InterviewResult{...}
    md := BuildMarkdownSummary(result)
    assert(strings.Contains(md, "**Q1: Auth model?**"))
    assert(strings.Contains(md, "OAuth — Google SSO"))
}

func TestDeserializeInterviewResult_Invalid(t *testing.T) {
    _, err := DeserializeInterviewResult("not json")
    assert(err != nil)
}
```

- [ ] **Step 2: Run tests to verify they fail**

- [ ] **Step 3: Implement types and serialization**

```go
// pipeline/handlers/interview_result.go
package handlers

import "encoding/json"

type InterviewAnswer struct { ... }
type InterviewResult struct { ... }

func SerializeInterviewResult(r InterviewResult) string {
    b, _ := json.Marshal(r)
    return string(b)
}

func DeserializeInterviewResult(s string) (InterviewResult, error) {
    var r InterviewResult
    err := json.Unmarshal([]byte(s), &r)
    return r, err
}

func BuildMarkdownSummary(r InterviewResult) string {
    // Format: **Q1: question?**\nAnswer — elaboration\n\n
}
```

- [ ] **Step 4: Run tests to verify they pass**

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/interview_result.go pipeline/handlers/interview_result_test.go
git commit -m "feat: add interview answer types and JSON serialization"
```

---

## Task 3: Dippin Adapter — Extract Interview Attrs

**Files:**
- Modify: `pipeline/dippin_adapter.go:281-288`
- Modify: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Write adapter test**

```go
func TestExtractHumanAttrs_Interview(t *testing.T) {
    cfg := ir.HumanConfig{
        Mode:         "interview",
        QuestionsKey: "my_questions",
        AnswersKey:   "my_answers",
        Prompt:       "Answer the questions below",
    }
    attrs := map[string]string{}
    extractHumanAttrs(cfg, attrs)
    assert(attrs["mode"] == "interview")
    assert(attrs["questions_key"] == "my_questions")
    assert(attrs["answers_key"] == "my_answers")
    assert(attrs["prompt"] == "Answer the questions below")
}
```

- [ ] **Step 2: Run test to verify it fails**

- [ ] **Step 3: Implement**

In `extractHumanAttrs` (`pipeline/dippin_adapter.go:281`), add after the existing lines:

```go
if cfg.QuestionsKey != "" {
    attrs["questions_key"] = cfg.QuestionsKey
}
if cfg.AnswersKey != "" {
    attrs["answers_key"] = cfg.AnswersKey
}
if cfg.Prompt != "" {
    attrs["prompt"] = cfg.Prompt
}
```

- [ ] **Step 4: Run test to verify it passes**

- [ ] **Step 5: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "feat: extract interview mode attrs in dippin adapter"
```

---

## Task 4: InterviewInterviewer Interface + Handler Routing

**Files:**
- Modify: `pipeline/handlers/human.go`
- Modify: `pipeline/handlers/human_test.go`

- [ ] **Step 1: Write handler tests**

Create a `mockInterviewInterviewer` that implements the new interface, then test:

```go
// Mock that records calls and returns canned answers
type mockInterviewInterviewer struct {
    AutoApproveFreeformInterviewer
    questions []Question
    result    *InterviewResult
    err       error
}

func (m *mockInterviewInterviewer) AskInterview(qs []Question, prev *InterviewResult) (*InterviewResult, error) {
    m.questions = qs
    return m.result, m.err
}

func TestHumanHandler_InterviewMode_HappyPath(t *testing.T) {
    // Setup: graph with interview node, questions in context
    // Execute: handler calls AskInterview, stores JSON in answersKey + markdown in human_response
}

func TestHumanHandler_InterviewMode_ZeroQuestions(t *testing.T) {
    // Context has "No further questions needed" — parser returns 0 questions
    // Handler falls back to freeform with cfg.Prompt
}

func TestHumanHandler_InterviewMode_MissingQuestionsKey(t *testing.T) {
    // QuestionsKey not in context — falls back to last_response, then to freeform
}

func TestHumanHandler_InterviewMode_RetryPreFill(t *testing.T) {
    // Previous answers already in context at answersKey — passed to AskInterview
}

func TestHumanHandler_InterviewMode_NotInterviewInterviewer(t *testing.T) {
    // Interviewer only implements basic Interviewer — returns error
}

func TestHumanHandler_InterviewMode_Canceled(t *testing.T) {
    // AskInterview returns result with Canceled=true — still stores partial answers
}
```

- [ ] **Step 2: Run tests to verify they fail**

- [ ] **Step 3: Implement**

Add to `pipeline/handlers/human.go`:

```go
// InterviewInterviewer extends FreeformInterviewer with structured interview support.
// Used by human gate nodes with mode="interview" to present parsed questions
// as individual form fields with inline options.
type InterviewInterviewer interface {
    FreeformInterviewer
    AskInterview(questions []Question, previousAnswers *InterviewResult) (*InterviewResult, error)
}
```

In `Execute()`, add before the freeform check:

```go
if node.Attrs["mode"] == "interview" {
    return h.executeInterview(ctx, node, pctx)
}
```

New method `executeInterview`:

```go
func (h *HumanHandler) executeInterview(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
    ii, ok := h.interviewer.(InterviewInterviewer)
    if !ok {
        return pipeline.Outcome{}, fmt.Errorf("human gate node %q has mode=interview but interviewer does not support interviews", node.ID)
    }

    // Read config from node attrs (with defaults per spec)
    questionsKey := node.Attrs["questions_key"]
    if questionsKey == "" {
        questionsKey = "interview_questions"
    }
    answersKey := node.Attrs["answers_key"]
    if answersKey == "" {
        answersKey = "interview_answers"
    }

    // Read upstream markdown from context
    markdown, _ := pctx.Get(questionsKey)
    if markdown == "" {
        markdown, _ = pctx.Get(pipeline.ContextKeyLastResponse)
    }

    // Parse questions
    questions := ParseQuestions(markdown)

    // 0 questions or malformed → fall back to freeform with prompt
    if len(questions) == 0 {
        prompt := node.Attrs["prompt"]
        if prompt == "" {
            prompt = node.Label
        }
        if prompt == "" {
            prompt = "No questions were generated. Please provide any input."
        }
        // Show the raw upstream text for context if available
        if markdown != "" {
            prompt = prompt + "\n\n---\n" + markdown
        }
        return h.executeFreeform(node, prompt)
    }

    // Check for previous answers (retry pre-fill)
    var previous *InterviewResult
    if prevJSON, ok := pctx.Get(answersKey); ok && prevJSON != "" {
        if prev, err := DeserializeInterviewResult(prevJSON); err == nil {
            previous = &prev
        }
    }

    // Present interview
    result, err := ii.AskInterview(questions, previous)
    if err != nil {
        return pipeline.Outcome{}, fmt.Errorf("interview failed for node %q: %w", node.ID, err)
    }

    // Store answers
    jsonStr := SerializeInterviewResult(*result)
    summary := BuildMarkdownSummary(*result)

    return pipeline.Outcome{
        Status: pipeline.OutcomeSuccess,
        ContextUpdates: map[string]string{
            answersKey:                       jsonStr,
            pipeline.ContextKeyHumanResponse: summary,
        },
    }, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

- [ ] **Step 5: Run full test suite**

Run: `go test ./pipeline/handlers/ -v`

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "feat: add InterviewInterviewer interface and executeInterview handler"
```

---

## Task 5: Auto-Approve + Queue Interview Interviewers

**Files:**
- Modify: `pipeline/handlers/human.go`
- Modify: `pipeline/handlers/human_test.go`

- [ ] **Step 1: Write tests**

```go
func TestAutoApproveInterviewInterviewer(t *testing.T) {
    ai := &AutoApproveFreeformInterviewer{}
    // Type-assert to InterviewInterviewer should work
    ii, ok := ai.(InterviewInterviewer) // Will fail until implemented
    result, err := ii.AskInterview(questions, nil)
    // All answers empty, incomplete=false, canceled=false
}
```

- [ ] **Step 2: Implement**

Add `AskInterview` method to `AutoApproveFreeformInterviewer`:

```go
func (a *AutoApproveFreeformInterviewer) AskInterview(questions []Question, prev *InterviewResult) (*InterviewResult, error) {
    answers := make([]InterviewAnswer, len(questions))
    for i, q := range questions {
        ans := InterviewAnswer{
            ID:   fmt.Sprintf("q%d", q.Index),
            Text: q.Text,
        }
        if len(q.Options) > 0 {
            ans.Answer = q.Options[0] // pick first option
        } else if q.IsYesNo {
            ans.Answer = "yes"
        } else {
            ans.Answer = "auto-approved"
        }
        answers[i] = ans
    }
    return &InterviewResult{Questions: answers}, nil
}
```

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "feat: add interview support to auto-approve interviewer"
```

---

## Task 6: TUI InterviewContent Modal

**Files:**
- Create: `tui/interview_content.go`
- Create: `tui/interview_content_test.go`

This is the most complex new file. It follows the `ReviewHybridContent` pattern but is fullscreen and multi-field.

- [ ] **Step 1: Write content type tests**

Test that `InterviewContent`:
- Implements `ModalContent`, `Cancellable`, `FullscreenContent`
- Renders question text with field below
- Select questions show options as radio + "Other" entry
- Yes/no questions show confirm toggle
- Open questions show textarea
- Up/down navigates between questions
- Enter on select option selects it
- Ctrl+S submits all answers via replyCh as JSON
- Esc triggers cancel, sends partial answers with `canceled: true`
- Pre-filled answers show in fields on init
- Pagination: >10 questions shows page N/M, PgUp/PgDn navigates

- [ ] **Step 2: Implement InterviewContent**

Structure:
```go
type InterviewContent struct {
    questions []Question
    fields    []interviewField
    cursor    int           // focused question index
    page      int           // current page (0-indexed)
    pageSize  int           // default 10
    replyCh   chan<- string  // JSON reply
    cancelled bool
    submitted bool
    width     int
    height    int
}

type interviewField struct {
    question     Question
    // Select fields
    selectCursor int
    isOther      bool
    otherInput   textarea.Model
    // Confirm fields
    confirmed    *bool
    // Text fields
    textInput    textarea.Model
    // Common
    elaboration  textarea.Model // for select+elaborate
}
```

Key behaviors:
- **Rendering:** Each question renders as a labeled block. Focused question is highlighted with accent color. Select shows `●`/`○` radio markers + "Other" entry. Confirm shows `[Y]`/`[N]` toggle. TextArea shows wrapping input.
- **Navigation:** `↑`/`↓` moves between questions. For select: `↑`/`↓` also cycles options when inside a select field. `Tab` moves from option selection to elaboration textarea. `Enter` on a select option confirms it and moves to next question.
- **Pagination:** Questions `[page*pageSize .. (page+1)*pageSize-1]` are visible. Status line shows `Page 1/3 — ↑↓ navigate, Ctrl+S submit, Esc cancel`. `PgUp`/`PgDn` change pages.
- **Submit:** `Ctrl+S` collects all field values into `InterviewResult`, serializes to JSON, sends on `replyCh`.
- **Cancel:** `Esc` calls `Cancel()` — sends partial `InterviewResult{Canceled: true}` with filled answers so far, then closes channel.
- **Pre-fill:** On init, if `previous` is provided, match answers by question ID or text similarity and populate fields.

- [ ] **Step 3: Run tests to verify they pass**

- [ ] **Step 4: Commit**

```bash
git add tui/interview_content.go tui/interview_content_test.go
git commit -m "feat: add InterviewContent TUI modal for multi-field interview forms"
```

---

## Task 7: TUI Message Type + BubbleteaInterviewer Wiring

**Files:**
- Modify: `tui/messages.go`
- Modify: `tui/interviewer.go`
- Modify: `tui/app.go`

- [ ] **Step 1: Add MsgGateInterview message**

In `tui/messages.go`, after `MsgGateFreeform`:

```go
type MsgGateInterview struct {
    NodeID   string
    Questions []handlers.Question
    Previous  *handlers.InterviewResult
    ReplyCh   chan<- string // JSON string
}
```

- [ ] **Step 2: Add AskInterview to BubbleteaInterviewer**

In `tui/interviewer.go`:

```go
// Compile-time assertion
var _ handlers.InterviewInterviewer = (*BubbleteaInterviewer)(nil)

func (b *BubbleteaInterviewer) AskInterview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
    if b.send != nil {
        return b.askMode2Interview(questions, prev)
    }
    return b.askMode1Interview(questions, prev)
}

func (b *BubbleteaInterviewer) askMode2Interview(questions []handlers.Question, prev *handlers.InterviewResult) (*handlers.InterviewResult, error) {
    ch := make(chan string, 1)
    b.send(MsgGateInterview{Questions: questions, Previous: prev, ReplyCh: ch})
    reply, ok := <-ch
    if !ok {
        return &handlers.InterviewResult{Canceled: true}, nil
    }
    result, err := handlers.DeserializeInterviewResult(reply)
    if err != nil {
        return nil, fmt.Errorf("failed to deserialize interview reply: %w", err)
    }
    return &result, nil
}

// Mode 1: interviewRunner wrapping InterviewContent in inline tea.Program
```

- [ ] **Step 3: Wire into app.go handleModalMsg**

In `tui/app.go`, add to the switch in `handleModalMsg`:

```go
case MsgGateInterview:
    content := NewInterviewContent(m.Questions, m.Previous, m.ReplyCh, a.lay.width, a.lay.height)
    a.modal.Show(content)
    return a, nil, true
```

- [ ] **Step 4: Run full test suite**

Run: `go test ./tui/ -v && go test ./pipeline/handlers/ -v`

- [ ] **Step 5: Commit**

```bash
git add tui/messages.go tui/interviewer.go tui/app.go
git commit -m "feat: wire interview mode through TUI message bus and BubbleteaInterviewer"
```

---

## Task 8: Autopilot Interview Support

**Files:**
- Modify: `pipeline/handlers/autopilot.go`
- Modify: `pipeline/handlers/autopilot_claudecode.go`
- Modify: `tui/autopilot_interviewer.go`

- [ ] **Step 1: Write autopilot interview test**

```go
func TestAutopilotInterviewer_AskInterview(t *testing.T) {
    // Mock LLM client returns structured JSON answers
    // Verify all questions answered, answers match LLM response
}
```

- [ ] **Step 2: Implement AutopilotInterviewer.AskInterview**

```go
func (a *AutopilotInterviewer) AskInterview(questions []Question, prev *InterviewResult) (*InterviewResult, error) {
    prompt := buildInterviewPrompt(questions)
    decision, err := a.callLLM(prompt, nil, "")
    if err != nil {
        return nil, fmt.Errorf("autopilot interview failed: %w", err)
    }
    // Parse JSON from decision.Reasoning or decision.Choice
    // Map answers back to questions
}

func buildInterviewPrompt(questions []Question) string {
    // Format: "Answer each question. For questions with options, pick one.\n\n1. ...\n2. ..."
    // Request JSON output: {"answers": [{"id": "q1", "answer": "...", "elaboration": "..."}]}
}
```

- [ ] **Step 3: Implement ClaudeCodeAutopilotInterviewer.AskInterview**

Same pattern using `callClaude` subprocess.

- [ ] **Step 4: Implement AutopilotTUIInterviewer.AskInterview**

Delegates to inner autopilot, flashes result summary in TUI modal.

- [ ] **Step 5: Run tests, verify pass**

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/autopilot.go pipeline/handlers/autopilot_claudecode.go tui/autopilot_interviewer.go
git commit -m "feat: add interview support to autopilot interviewers"
```

---

## Task 9: Console Interviewer Interview Support

**Files:**
- Modify: `pipeline/handlers/human.go`
- Modify: `pipeline/handlers/human_test.go`

- [ ] **Step 1: Write console interview test**

```go
func TestConsoleInterviewer_AskInterview(t *testing.T) {
    input := "OAuth\nGoogle SSO\nSalesforce sync\n"
    ci := &ConsoleInterviewer{Reader: strings.NewReader(input), Writer: &bytes.Buffer{}}
    questions := []Question{
        {Index: 1, Text: "Auth model?", Options: []string{"API key", "OAuth", "JWT"}},
        {Index: 2, Text: "Describe integrations"},
    }
    result, err := ci.AskInterview(questions, nil)
    assert(result.Questions[0].Answer == "OAuth")
    assert(result.Questions[1].Answer == "Salesforce sync")
}
```

- [ ] **Step 2: Implement ConsoleInterviewer.AskInterview**

Iterate questions, print each with options, read answer line. For select questions, match by name or index. For yes/no, accept y/n. For text, read until blank line.

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "feat: add interview support to console interviewer"
```

---

## Task 10: Integration Test

**Files:**
- Create: `pipeline/handlers/interview_integration_test.go`

- [ ] **Step 1: Write end-to-end test**

Build a graph with:
- Agent node → interview human node → downstream node
- Set `interview_questions` in context with sample markdown
- Use `AutoApproveFreeformInterviewer` (which now implements `InterviewInterviewer`)
- Run through handler registry
- Assert: `interview_answers` key in context contains valid JSON
- Assert: `human_response` key contains markdown summary
- Assert: outcome is success

- [ ] **Step 2: Run test**

Run: `go test ./pipeline/handlers/ -run TestInterview_Integration -v`

- [ ] **Step 3: Run full suite**

Run: `go build ./... && go test ./... -short`

- [ ] **Step 4: Commit**

```bash
git add pipeline/handlers/interview_integration_test.go
git commit -m "test: add integration test for interview mode end-to-end"
```

---

## Edge Cases Matrix

| Case | Where handled | Behavior |
|------|--------------|----------|
| 0 questions (agent said "No further questions") | `executeInterview` in human.go | Falls back to `executeFreeform` with `cfg.Prompt` |
| Malformed markdown / no parseable questions | `ParseQuestions` returns `[]` → same as 0 questions | Falls back to freeform |
| Ctrl-C mid-form | `InterviewContent.Cancel()` | Sends `InterviewResult{Canceled: true}` with partial answers |
| Retry loop (node visited again) | `executeInterview` reads previous from `pctx.Get(answersKey)` | Passes `previous` to `AskInterview` for pre-fill |
| 20+ questions | `InterviewContent` pagination | Soft cap 10 per page, PgUp/PgDn navigation |
| User skips question (blank) | `InterviewContent` allows moving past unfilled fields | Records `answer: ""` |
| Select + elaborate | `InterviewContent` shows elaboration textarea after select | Stores both `answer` and `elaboration` |
| User picks "Other" on select | `InterviewContent` shows text input | Stores custom text as answer |
| `QuestionsKey` missing from context | `executeInterview` | Falls back to `last_response` key, then to freeform |
| `AnswersKey` missing from attrs | `executeInterview` | Defaults to `"interview_answers"` |
| Interview node without `InterviewInterviewer` | `executeInterview` type assertion | Returns error: "interviewer does not support interviews" |

---

## Verification

After all tasks complete:

1. **Build:** `go build ./...` — must pass
2. **Tests:** `go test ./... -short` — all packages must pass
3. **Dippin doctor:** `dippin doctor examples/*.dip` — verify no regressions
4. **Manual smoke test:** Create a minimal test pipeline with an interview node, run `tracker run` and verify the form appears correctly

---

## Dependency Graph

```text
Task 1 (parser) ────────────┐
Task 2 (serialization) ─────┤
Task 3 (adapter) ───────────┼── Task 4 (interface + handler) ──┬── Task 5 (auto-approve)
                             │                                   ├── Task 6 (TUI content) ── Task 7 (TUI wiring)
                             │                                   ├── Task 8 (autopilot)
                             │                                   ├── Task 9 (console)
                             │                                   └── Task 10 (integration)
```

Tasks 1, 2, 3 can run in parallel. Task 4 depends on 1+2. Tasks 5-9 depend on 4 and can be partially parallelized. Task 10 is last.
