# Agent Checkpoints & Loop Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve agent session effectiveness by adding turn-budget awareness, strategy reflection checkpoints, grep match counting, and two-phase verification — targeting the failure patterns found in SWE-bench v2 (70.3% → aiming for 75%+).

**Architecture:** The turn loop in `agent/session.go` gains a checkpoint system that injects user messages at configurable turn thresholds. The grep tool gains total match counting. The verify loop gains an optional broad-test second phase. All features are opt-in via `SessionConfig` fields with zero behavior change when unconfigured.

**Tech Stack:** Go 1.24, tracker agent package

---

## File Structure

| File | Responsibility |
|------|---------------|
| `agent/config.go` | New config fields: `Checkpoints`, `VerifyBroadCommand` |
| `agent/checkpoint.go` | **New.** Checkpoint evaluation logic — decides which message to inject at which turn |
| `agent/checkpoint_test.go` | **New.** Tests for checkpoint logic |
| `agent/session.go` | Wire checkpoint evaluation into `executeTurn` |
| `agent/session_test.go` | Integration test for checkpoint injection |
| `agent/verify.go` | Add broad-test second phase after focused verify passes |
| `agent/verify_test.go` | Tests for two-phase verify |
| `agent/tools/grep.go` | Count total matches before truncating |
| `agent/tools/grep_test.go` | Test for total match count in output |
| `agent/events.go` | New `EventCheckpoint` event type |
| `cmd/tracker-swebench/agent-runner/main.go` | Wire checkpoint config for SWE-bench runs |

---

### Task 1: Checkpoint Config and Types

**Files:**
- Modify: `agent/config.go:18-55`
- Create: `agent/checkpoint.go`
- Create: `agent/checkpoint_test.go`

- [ ] **Step 1: Write the failing test for checkpoint evaluation**

Create `agent/checkpoint_test.go`:

```go
// ABOUTME: Tests for turn-budget checkpoint evaluation.
// ABOUTME: Validates that checkpoints fire at the right turn thresholds.
package agent

import "testing"

func TestEvalCheckpoints_NoCheckpoints(t *testing.T) {
	msg := evalCheckpoint(nil, 10, 80)
	if msg != "" {
		t.Errorf("expected empty, got %q", msg)
	}
}

func TestEvalCheckpoints_BudgetWarning(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.6, Message: "60% budget used"},
		{Fraction: 0.85, Message: "85% budget used"},
	}
	// Turn 48 of 80 = 0.6 exactly — should fire first checkpoint
	msg := evalCheckpoint(cps, 48, 80)
	if msg != "60% budget used" {
		t.Errorf("expected 60%% warning, got %q", msg)
	}
}

func TestEvalCheckpoints_CriticalWarning(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.6, Message: "60% budget used"},
		{Fraction: 0.85, Message: "85% budget used"},
	}
	// Turn 68 of 80 = 0.85 — should fire second checkpoint
	msg := evalCheckpoint(cps, 68, 80)
	if msg != "85% budget used" {
		t.Errorf("expected 85%% warning, got %q", msg)
	}
}

func TestEvalCheckpoints_BeforeFirstThreshold(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.6, Message: "60% budget used"},
	}
	msg := evalCheckpoint(cps, 10, 80)
	if msg != "" {
		t.Errorf("expected empty before threshold, got %q", msg)
	}
}

func TestEvalCheckpoints_ExactlyOnTurn(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.5, Message: "halfway"},
	}
	// Turn 40 of 80 = 0.5 — fires exactly at threshold
	msg := evalCheckpoint(cps, 40, 80)
	if msg != "halfway" {
		t.Errorf("expected halfway, got %q", msg)
	}
	// Turn 41 — past the threshold turn, should not fire again
	msg = evalCheckpoint(cps, 41, 80)
	if msg != "" {
		t.Errorf("expected empty on turn after threshold, got %q", msg)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./agent/ -run TestEvalCheckpoints -v`
Expected: FAIL — `evalCheckpoint` undefined

- [ ] **Step 3: Add Checkpoint type to config.go and implement evalCheckpoint**

Add to `agent/config.go` inside `SessionConfig` (after the `MaxVerifyRetries` field):

```go
	// Checkpoints are messages injected at specific turn-budget fractions.
	// Each checkpoint fires exactly once, on the turn where the fraction is
	// first reached. Fraction is in [0, 1] — e.g. 0.6 means "at 60% of MaxTurns".
	Checkpoints []Checkpoint

	// VerifyBroadCommand is an optional second verification command run after
	// the focused VerifyCommand passes. Use this for regression detection
	// (e.g. run the full test module without -x). Empty means disabled.
	VerifyBroadCommand string
```

Add the `Checkpoint` type below `SessionConfig`:

```go
// Checkpoint defines a message to inject at a specific turn-budget fraction.
type Checkpoint struct {
	Fraction float64 // 0.0–1.0 fraction of MaxTurns
	Message  string  // message injected as a user message
}
```

Create `agent/checkpoint.go`:

```go
// ABOUTME: Turn-budget checkpoint evaluation for the agent session loop.
// ABOUTME: Returns the message to inject (if any) when a turn threshold is exactly hit.
package agent

// evalCheckpoint returns the checkpoint message to inject at the given turn,
// or "" if no checkpoint fires. Each checkpoint fires exactly once: on the
// turn number that equals ceil(fraction * maxTurns). Callers do not need to
// track fired state — the turn-number match is deterministic.
func evalCheckpoint(checkpoints []Checkpoint, turn, maxTurns int) string {
	for _, cp := range checkpoints {
		// Compute the exact turn this checkpoint fires on.
		// Use ceiling so fraction=0.5 with maxTurns=80 fires on turn 40.
		triggerTurn := int(cp.Fraction * float64(maxTurns))
		if triggerTurn < 1 {
			triggerTurn = 1
		}
		if turn == triggerTurn {
			return cp.Message
		}
	}
	return ""
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./agent/ -run TestEvalCheckpoints -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/config.go agent/checkpoint.go agent/checkpoint_test.go
git commit -m "feat(agent): add Checkpoint type and evalCheckpoint logic

Checkpoints fire exactly once at a deterministic turn number derived
from a fraction of MaxTurns. No mutable state needed — the turn number
match is the trigger."
```

---

### Task 2: Wire Checkpoints into the Turn Loop

**Files:**
- Modify: `agent/session.go:224-264` (executeTurn)
- Modify: `agent/events.go:14-37`
- Modify: `agent/session_test.go`

- [ ] **Step 1: Write integration test for checkpoint injection**

Add to `agent/session_test.go`:

```go
func TestCheckpointInjection(t *testing.T) {
	// Set up a mock that tracks injected messages.
	var capturedMessages []string
	client := &mockCompleter{
		onComplete: func(req *llm.Request) {
			for _, msg := range req.Messages {
				if msg.Role == llm.RoleUser {
					for _, part := range msg.Content {
						if part.Kind == llm.KindText && strings.Contains(part.Text, "CHECKPOINT") {
							capturedMessages = append(capturedMessages, part.Text)
						}
					}
				}
			}
		},
	}

	// Respond with tool calls for 10 turns, then stop.
	for i := 0; i < 10; i++ {
		client.responses = append(client.responses, &llm.Response{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{{
					Kind:     llm.KindToolCall,
					ToolCall: &llm.ToolCallData{ID: fmt.Sprintf("tc_%d", i), Name: "stub", Arguments: json.RawMessage(`{}`)},
				}},
			},
			FinishReason: llm.FinishReason{Reason: "tool_use"},
			Usage:        llm.Usage{InputTokens: 100, OutputTokens: 50},
		})
	}

	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.Checkpoints = []Checkpoint{
		{Fraction: 0.5, Message: "[CHECKPOINT] halfway there"},
	}

	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "stub", output: "ok"}))
	sess.Run(context.Background(), "do work")

	found := false
	for _, msg := range capturedMessages {
		if strings.Contains(msg, "halfway there") {
			found = true
		}
	}
	if !found {
		t.Errorf("checkpoint message not injected; captured: %v", capturedMessages)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./agent/ -run TestCheckpointInjection -v`
Expected: FAIL — checkpoint message never appears in captured messages

- [ ] **Step 3: Add EventCheckpoint to events.go**

Add after the `EventVerify` line in `agent/events.go`:

```go
	EventCheckpoint EventType = "checkpoint"
```

- [ ] **Step 4: Wire checkpoint into executeTurn in session.go**

In `agent/session.go`, in `executeTurn`, add checkpoint evaluation after `s.drainSteering()` and before `s.emit(Event{Type: EventTurnStart, ...})`:

```go
func (s *Session) executeTurn(ctx context.Context, turn int, start time.Time, tracker *ContextWindowTracker, result *SessionResult, ts *turnState) (bool, bool, error) {
	s.drainSteering()

	// Check if a turn-budget checkpoint should fire on this turn.
	if cpMsg := evalCheckpoint(s.config.Checkpoints, turn, s.config.MaxTurns); cpMsg != "" {
		s.messages = append(s.messages, llm.UserMessage(cpMsg))
		s.emit(Event{Type: EventCheckpoint, SessionID: s.id, Turn: turn, Text: cpMsg})
	}

	s.emit(Event{Type: EventTurnStart, SessionID: s.id, Turn: turn})
	turnStart := time.Now()
	// ... rest unchanged
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./agent/ -run TestCheckpointInjection -v`
Expected: PASS

- [ ] **Step 6: Run the full agent test suite**

Run: `go test ./agent/ -short -count=1`
Expected: All tests pass

- [ ] **Step 7: Commit**

```bash
git add agent/session.go agent/events.go agent/session_test.go
git commit -m "feat(agent): wire checkpoint injection into turn loop

Checkpoints inject user messages at turn-budget thresholds. The agent
sees these as regular user messages and can adjust its strategy. Events
are emitted for TUI/logging visibility."
```

---

### Task 3: Grep Total Match Count

**Files:**
- Modify: `agent/tools/grep.go:17-19,125-134,156-198`
- Modify: `agent/tools/grep_test.go`

- [ ] **Step 1: Write the failing test**

Add to `agent/tools/grep_test.go`:

```go
func TestGrepSearchTotalMatchCount(t *testing.T) {
	dir := t.TempDir()
	// Create a file with 150 matching lines — exceeds maxGrepResults (100).
	var content strings.Builder
	for i := 0; i < 150; i++ {
		content.WriteString(fmt.Sprintf("match line %d\n", i))
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content.String()), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "match line", "path": "big.txt"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should mention that there are 150 total matches, not just "more matches".
	if !strings.Contains(result, "150") {
		t.Errorf("expected total match count 150 in output, got:\n%s", result)
	}
	if !strings.Contains(result, "showing first 100") {
		t.Errorf("expected truncation message, got:\n%s", result)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./agent/tools/ -run TestGrepSearchTotalMatchCount -v`
Expected: FAIL — output says "more matches" but not "150"

- [ ] **Step 3: Implement total match counting**

The approach: when the 100-match limit is hit in `walkEntry`, continue walking to count remaining matches without collecting them. This requires changing the walk to separate counting from collecting.

In `agent/tools/grep.go`, change the `searchDir` method to track total matches:

```go
// searchDir walks a directory recursively, searching each regular file for matches.
func (t *GrepSearchTool) searchDir(ctx context.Context, root string, re *regexp.Regexp, contextLines int) ([]string, bool, int, error) {
	var matches []string
	truncated := false
	totalMatches := 0

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			return shouldSkipDir(info, path, root)
		}
		if isBinaryExtension(info.Name()) {
			return nil
		}
		if !truncated {
			fileMatches, fileTruncated, fileErr := t.searchFile(path, re, contextLines)
			if fileErr != nil {
				return nil
			}
			matches = append(matches, fileMatches...)
			totalMatches += len(fileMatches)
			if fileTruncated || len(matches) >= maxGrepResults {
				truncated = true
				matches = matches[:min(len(matches), maxGrepResults)]
			}
		} else {
			// Already truncated — just count remaining matches.
			count, countErr := t.countFileMatches(path, re)
			if countErr != nil {
				return nil
			}
			totalMatches += count
		}
		return nil
	})

	if err != nil && ctx.Err() == nil {
		return matches, truncated, totalMatches, err
	}

	return matches, truncated, totalMatches, nil
}
```

Add the `countFileMatches` helper:

```go
// countFileMatches counts matching lines in a file without collecting them.
func (t *GrepSearchTool) countFileMatches(absPath string, re *regexp.Regexp) (int, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			count++
		}
	}
	return count, scanner.Err()
}
```

Update `searchFile` to return 3 values (add totalMatches):

No change needed to `searchFile` — the total is computed in `searchDir`. But `searchFile` is also called directly from `runSearch` for single-file searches. Update `runSearch`:

```go
func (t *GrepSearchTool) runSearch(ctx context.Context, searchRoot, displayPath string, re *regexp.Regexp, contextLines int) ([]string, bool, int, error) {
	info, err := os.Stat(searchRoot)
	if err != nil {
		return nil, false, 0, fmt.Errorf("path not found: %s", displayPath)
	}
	if info.IsDir() {
		return t.searchDir(ctx, searchRoot, re, contextLines)
	}
	matches, truncated, err := t.searchFile(searchRoot, re, contextLines)
	total := len(matches)
	if truncated {
		// For single-file search, count remaining matches.
		count, countErr := t.countFileMatches(searchRoot, re)
		if countErr == nil {
			total = count
		}
	}
	return matches, truncated, total, err
}
```

Update `Execute` to pass `totalMatches` to `formatGrepResults`:

```go
func (t *GrepSearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	pattern, path, contextLines, err := parseGrepInput(input)
	if err != nil {
		return "", err
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	searchRoot, err := t.safePath(path)
	if err != nil {
		return "", err
	}

	matches, truncated, totalMatches, err := t.runSearch(ctx, searchRoot, path, re, contextLines)
	if err != nil {
		return "", err
	}

	return formatGrepResults(pattern, matches, truncated, totalMatches), nil
}
```

Update `formatGrepResults`:

```go
func formatGrepResults(pattern string, matches []string, truncated bool, totalMatches int) string {
	if len(matches) == 0 {
		return fmt.Sprintf("no matches for pattern %q", pattern)
	}
	result := strings.Join(matches, "\n")
	if truncated {
		result += fmt.Sprintf("\n\n(showing first %d of %d total matches — narrow your search pattern)", maxGrepResults, totalMatches)
	}
	return result
}
```

Remove the `walkEntry` method entirely — its logic is now inlined in `searchDir`.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./agent/tools/ -run TestGrepSearchTotalMatchCount -v`
Expected: PASS

- [ ] **Step 5: Run the full tools test suite**

Run: `go test ./agent/tools/ -short -count=1`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add agent/tools/grep.go agent/tools/grep_test.go
git commit -m "feat(agent/tools): show total match count when grep results truncated

When grep_search hits the 100-match cap, continue walking to count
remaining matches and report the total. This tells the agent whether
to narrow the search (150 matches) vs just getting unlucky with the
cap (102 matches)."
```

---

### Task 4: Two-Phase Verification

**Files:**
- Modify: `agent/verify.go:109-132,139-178`
- Modify: `agent/verify_test.go`
- Modify: `agent/config.go` (already done in Task 1)

- [ ] **Step 1: Write the failing test**

Add to `agent/verify_test.go`:

```go
func TestTwoPhaseVerify_BroadRunsAfterFocusedPasses(t *testing.T) {
	dir := t.TempDir()

	// Create a script that always passes for focused, fails for broad.
	focusedScript := filepath.Join(dir, "focused.sh")
	os.WriteFile(focusedScript, []byte("#!/bin/sh\nexit 0\n"), 0755)

	broadScript := filepath.Join(dir, "broad.sh")
	os.WriteFile(broadScript, []byte("#!/bin/sh\necho 'FAIL: test_regression'\nexit 1\n"), 0755)

	v := &verifier{
		cmd:      focusedScript,
		broadCmd: broadScript,
		workDir:  dir,
	}

	passed, exitCode, output, err := v.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Broad test fails, so overall verification should fail.
	if passed {
		t.Error("expected verification to fail due to broad test failure")
	}
	if exitCode == 0 {
		t.Error("expected non-zero exit code from broad test")
	}
	if !strings.Contains(output, "test_regression") {
		t.Errorf("expected broad test output, got %q", output)
	}
}

func TestTwoPhaseVerify_NoBroadCommand(t *testing.T) {
	dir := t.TempDir()

	script := filepath.Join(dir, "pass.sh")
	os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0755)

	v := &verifier{
		cmd:     script,
		workDir: dir,
	}

	passed, _, _, err := v.run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passed {
		t.Error("expected verification to pass with no broad command")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./agent/ -run TestTwoPhaseVerify -v`
Expected: FAIL — `verifier` has no `broadCmd` field

- [ ] **Step 3: Add broadCmd to verifier and update run()**

In `agent/verify.go`, add `broadCmd` field to the `verifier` struct:

```go
type verifier struct {
	cmd      string // focused verification command (never empty)
	broadCmd string // optional broad regression command (empty = skip)
	workDir  string
}
```

Update `newVerifier` to read `VerifyBroadCommand`:

```go
func newVerifier(cfg SessionConfig) *verifier {
	if !cfg.VerifyAfterEdit {
		return nil
	}
	cmd := cfg.VerifyCommand
	if cmd == "" {
		cmd = detectVerifyCommand(cfg.WorkingDir)
	}
	if cmd == "" {
		return nil
	}
	return &verifier{
		cmd:      cmd,
		broadCmd: cfg.VerifyBroadCommand,
		workDir:  cfg.WorkingDir,
	}
}
```

Update the `run` method to add a second phase:

```go
func (v *verifier) run(ctx context.Context) (passed bool, exitCode int, output string, err error) {
	// Phase 1: focused test.
	passed, exitCode, output, err = v.runCommand(ctx, v.cmd)
	if err != nil || !passed {
		return passed, exitCode, output, err
	}

	// Phase 2: broad regression test (optional).
	if v.broadCmd == "" {
		return true, 0, output, nil
	}
	return v.runCommand(ctx, v.broadCmd)
}

// runCommand executes a single verification command and returns results.
func (v *verifier) runCommand(ctx context.Context, command string) (passed bool, exitCode int, output string, err error) {
	if strings.TrimSpace(command) == "" {
		return false, 0, "", fmt.Errorf("empty verify command")
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = v.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	out, runErr := cmd.CombinedOutput()

	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	outStr := truncateTail(string(out), verifyOutputCap)

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return false, exitErr.ExitCode(), outStr, nil
		}
		return false, -1, outStr, runErr
	}
	return true, 0, outStr, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./agent/ -run TestTwoPhaseVerify -v`
Expected: PASS

- [ ] **Step 5: Run the full agent test suite**

Run: `go test ./agent/ -short -count=1`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add agent/verify.go agent/verify_test.go agent/config.go
git commit -m "feat(agent): two-phase verification — focused test then broad regression check

When VerifyBroadCommand is set, run the focused test first (fast, -x)
then the broad test (slower, catches regressions). If the focused test
fails, skip the broad test. This prevents patches that pass the target
test but break unrelated tests."
```

---

### Task 5: Wire SWE-bench Checkpoints

**Files:**
- Modify: `cmd/tracker-swebench/agent-runner/main.go`

- [ ] **Step 1: Read the current agent-runner config to find the exact insertion point**

Run: `head -160 cmd/tracker-swebench/agent-runner/main.go`

The checkpoint config goes after the existing `VerifyCommand` setup.

- [ ] **Step 2: Add checkpoint configuration**

Add after the `VerifyAfterEdit`/`VerifyCommand` lines in `agent-runner/main.go`:

```go
	sessionCfg.Checkpoints = []agent.Checkpoint{
		{
			Fraction: 0.5,
			Message: `CHECKPOINT: You've used half your turn budget. Before continuing:
1. List what approaches you've tried so far and their results.
2. If none of your approaches have made the failing tests pass, STOP and re-read the issue description and test file from scratch.
3. Consider whether you're editing the right file/function. The fix might be elsewhere.
4. Commit to ONE approach and test it thoroughly before trying alternatives.`,
		},
		{
			Fraction: 0.75,
			Message: `URGENT: You have 25% of your turn budget remaining.
1. If you have a partially working fix, focus on making it complete.
2. If nothing has worked, apply your best-guess minimal fix NOW and run the tests.
3. Do NOT start exploring new files or refactoring — focus on testing what you have.`,
		},
	}

	sessionCfg.VerifyBroadCommand = "python -m pytest --tb=short -q 2>&1 | tail -100"
```

- [ ] **Step 3: Build and verify compilation**

Run: `go build ./cmd/tracker-swebench/...`
Expected: BUILD OK

- [ ] **Step 4: Run the swebench harness tests**

Run: `go test ./cmd/tracker-swebench/... -short -count=1`
Expected: All tests pass

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker-swebench/agent-runner/main.go
git commit -m "feat(swebench): wire checkpoints and broad verify for agent runs

Add two turn-budget checkpoints (50% and 75%) with strategy-reflection
prompts. Add broad pytest verification (without -x) to catch regressions
after the focused test passes."
```

---

### Task 6: Full Integration Verification

**Files:**
- No new files — verification only

- [ ] **Step 1: Build all packages**

Run: `go build ./...`
Expected: BUILD OK

- [ ] **Step 2: Run full test suite**

Run: `go test ./... -short -count=1`
Expected: All 17 packages pass

- [ ] **Step 3: Verify no regressions in existing behavior**

Run: `go test ./agent/ -run TestSession -v -count=1`
Expected: All existing session tests pass (no checkpoint injection when Checkpoints is nil/empty)

- [ ] **Step 4: Final commit if any cleanup needed**

Only commit if Steps 1-3 revealed issues that needed fixing.
