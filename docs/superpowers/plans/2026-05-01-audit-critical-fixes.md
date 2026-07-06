# Audit Critical Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the 10 critical findings from the 13-expert panel audit of tracker v0.24.1.

**Architecture:** Surgical fixes to existing files — no new packages, no new abstractions. Each task addresses one critical finding, ordered by risk (security first, then correctness, then UX/docs). Tasks are independent and can be parallelized.

**Tech Stack:** Go 1.24, existing pipeline/handlers/llm packages.

---

## Task 1: ACP CreateTerminal command validation

**Why:** An LLM-directed ACP agent can execute any command on the host via `CreateTerminal`, completely bypassing the denylist/allowlist that protects `tool_command`. This is the highest-severity security gap.

**Files:**
- Modify: `pipeline/handlers/backend_acp_client.go:398-447`
- Modify: `pipeline/handlers/backend_acp_client.go:228-258` (RequestPermission)
- Test: `pipeline/handlers/backend_acp_client_test.go`

- [ ] **Step 1: Write failing test for command denylist enforcement**

```go
func TestCreateTerminal_DenylistedCommand(t *testing.T) {
	h := &acpClientHandler{
		workingDir:  t.TempDir(),
		toolSafety:  DefaultToolHandlerConfig(),
	}

	resp, err := h.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "eval",
		Args:    []string{"malicious"},
	})
	if err == nil {
		t.Fatal("expected error for denylisted command, got nil")
	}
	if resp.TerminalId != "" {
		t.Errorf("expected empty terminal ID, got %q", resp.TerminalId)
	}
}
```

- [ ] **Step 2: Write failing test for cwd containment**

```go
func TestCreateTerminal_CwdOutsideWorkDir(t *testing.T) {
	h := &acpClientHandler{
		workingDir: t.TempDir(),
	}

	outsideDir := "/tmp"
	resp, err := h.CreateTerminal(context.Background(), acp.CreateTerminalRequest{
		Command: "ls",
		Cwd:     &outsideDir,
	})
	if err == nil {
		t.Fatal("expected error for cwd outside workdir, got nil")
	}
	if resp.TerminalId != "" {
		t.Errorf("expected empty terminal ID, got %q", resp.TerminalId)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run "TestCreateTerminal_Denylist|TestCreateTerminal_Cwd" -v`
Expected: FAIL

- [ ] **Step 4: Add `ToolHandlerConfig` field to `acpClientHandler` and validate in `CreateTerminal`**

In `backend_acp_client.go`, add a `toolSafety ToolHandlerConfig` field to `acpClientHandler`. Then add validation at the top of `CreateTerminal`:

```go
func (h *acpClientHandler) CreateTerminal(_ context.Context, p acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	// Validate command against denylist (same rules as tool_command).
	fullCmd := p.Command
	if len(p.Args) > 0 {
		fullCmd += " " + strings.Join(p.Args, " ")
	}
	if denied, pattern := h.toolSafety.IsDenied(fullCmd); denied {
		return acp.CreateTerminalResponse{}, &acp.RequestError{
			Code:    -32600,
			Message: fmt.Sprintf("command denied by safety policy (matched %q): %s", pattern, p.Command),
		}
	}

	// Validate cwd is under workingDir.
	cwd := h.workingDir
	if p.Cwd != nil && *p.Cwd != "" {
		resolved, err := validatePathInWorkDir(*p.Cwd, h.workingDir)
		if err != nil {
			return acp.CreateTerminalResponse{}, &acp.RequestError{
				Code:    -32600,
				Message: fmt.Sprintf("terminal cwd rejected: %v", err),
			}
		}
		cwd = resolved
	}
	// ... rest of existing code, using validated cwd ...
```

- [ ] **Step 5: Wire `toolSafety` into `acpClientHandler` construction**

Find where `acpClientHandler` is created (in the ACP backend) and pass `ToolHandlerConfig` through.

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./pipeline/handlers/ -run "TestCreateTerminal" -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pipeline/handlers/backend_acp_client.go pipeline/handlers/backend_acp_client_test.go
git commit -m "security(acp): validate CreateTerminal commands against denylist and cwd"
```

---

## Task 2: ClaudeCode subprocess process-group kill on cancellation

**Why:** When the pipeline is cancelled, the `claude` subprocess keeps running in the background consuming API credits indefinitely because `exec.Command` is used without context-aware cancellation or process group kill.

**Files:**
- Modify: `pipeline/handlers/backend_claudecode.go:80-97`
- Test: `pipeline/handlers/backend_claudecode_test.go`

- [ ] **Step 1: Write failing test for context cancellation killing subprocess**

```go
func TestClaudeCodeBackend_ContextCancelKillsSubprocess(t *testing.T) {
	// Use a long-running command as a stand-in for the claude CLI.
	b := &ClaudeCodeBackend{claudePath: "sleep"}

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately after start to simulate pipeline cancellation.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Run should return promptly after cancellation, not hang for the full sleep duration.
	start := time.Now()
	_, _ = b.Run(ctx, AgentRunConfig{
		NodeID: "test",
		Extra:  &ClaudeCodeConfig{Prompt: "test"},
	})
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Errorf("subprocess was not killed on cancel; took %v", elapsed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (hangs or takes too long)**

Run: `go test ./pipeline/handlers/ -run "TestClaudeCodeBackend_ContextCancel" -v -timeout 30s`
Expected: FAIL or timeout

- [ ] **Step 3: Add process group and context-aware kill to `Run`**

In `backend_claudecode.go`, change the subprocess setup:

```go
cmd := exec.Command(b.claudePath, args...)
cmd.Env = buildEnv()

// Use process group for clean kill on cancellation.
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

// Kill process group when context is cancelled.
cmd.Cancel = func() error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
cmd.WaitDelay = 5 * time.Second
```

Add import `"syscall"` if not already present.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/handlers/ -run "TestClaudeCodeBackend_ContextCancel" -v`
Expected: PASS

- [ ] **Step 5: Run full handler tests**

Run: `go test ./pipeline/handlers/ -short -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/backend_claudecode.go pipeline/handlers/backend_claudecode_test.go
git commit -m "fix(claudecode): kill subprocess process group on pipeline cancellation"
```

---

## Task 3: Fix `TRACKER_PASS_API_KEYS` to check `== "1"`

**Why:** Any non-empty value (including `"false"`, `"0"`, `"no"`) triggers full environment passthrough, silently leaking all API keys.

**Files:**
- Modify: `pipeline/handlers/backend_claudecode.go:274`
- Test: `pipeline/handlers/backend_claudecode_env_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestBuildEnv_PassAPIKeysFalseDoesNotPassthrough(t *testing.T) {
	t.Setenv("TRACKER_PASS_API_KEYS", "false")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-key")

	env := buildEnv()
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			t.Error("TRACKER_PASS_API_KEYS=false should NOT pass through API keys")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers/ -run "TestBuildEnv_PassAPIKeysFalse" -v`
Expected: FAIL

- [ ] **Step 3: Fix the check**

In `backend_claudecode.go` line 274, change:

```go
// Before:
if os.Getenv("TRACKER_PASS_API_KEYS") != "" {

// After:
if os.Getenv("TRACKER_PASS_API_KEYS") == "1" {
```

- [ ] **Step 4: Run test to verify it passes, then run all env tests**

Run: `go test ./pipeline/handlers/ -run "TestBuildEnv" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/backend_claudecode.go pipeline/handlers/backend_claudecode_env_test.go
git commit -m "security(claudecode): require TRACKER_PASS_API_KEYS=1 not just non-empty"
```

---

## Task 4: Fail on unknown outcome status instead of treating as success

**Why:** An unknown/unexpected outcome status is silently promoted to success in `engine_run.go:659-668`. This masks handler bugs and allows broken nodes to appear completed.

**Files:**
- Modify: `pipeline/engine_run.go:659-668`
- Test: `pipeline/engine_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestEngine_UnknownOutcomeFailsNode(t *testing.T) {
	// Create a handler that returns an unrecognized outcome status.
	registry := NewHandlerRegistry()
	registry.Register("bogus_handler", HandlerFunc(func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: "totally_bogus_status"}, nil
	}))

	g := &Graph{
		Nodes: map[string]*Node{
			"Start": {ID: "Start", Handler: "bogus_handler"},
			"Done":  {ID: "Done", Handler: "passthrough"},
		},
		Edges:     []Edge{{From: "Start", To: "Done"}},
		StartNode: "Start",
		ExitNode:  "Done",
	}

	engine := NewEngine(g, registry)
	result, err := engine.Run(context.Background())

	if err == nil && result.Status == OutcomeSuccess {
		t.Fatal("expected unknown outcome to NOT be treated as success")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run "TestEngine_UnknownOutcome" -v`
Expected: FAIL (currently treated as success)

- [ ] **Step 3: Change the default case to return an error**

In `engine_run.go`, replace the `default:` case:

```go
	default:
		e.emit(PipelineEvent{
			Type:      EventNodeFailed,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("node %q returned unknown outcome status %q; treating as failure", currentNodeID, status),
		})
		s.pctx.Set(ContextKeyOutcome, OutcomeFail)
```

This changes the behavior from "unknown = success" to "unknown = fail". The node is NOT marked completed, and the pipeline will follow fail edges if available or stop.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run "TestEngine_UnknownOutcome" -v`
Expected: PASS

- [ ] **Step 5: Run full pipeline tests**

Run: `go test ./pipeline/... -short`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pipeline/engine_run.go pipeline/engine_test.go
git commit -m "fix(engine): fail on unknown outcome status instead of treating as success"
```

---

## Task 5: Add defer/recover to TUI pipeline goroutine

**Why:** `runPipelineAsync` launches a goroutine with no panic recovery. A panic crashes the process without checkpoint save or TUI cleanup.

**Files:**
- Modify: `cmd/tracker/run.go:561-573`

- [ ] **Step 1: Add defer/recover to the goroutine**

```go
func runPipelineAsync(engine *pipeline.Engine, ctx context.Context, prog *tea.Program) chan pipelineOutcome {
	outcomeCh := make(chan pipelineOutcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				pipelineErr := fmt.Errorf("pipeline panicked: %v", r)
				outcomeCh <- pipelineOutcome{err: pipelineErr}
				prog.Send(tui.MsgPipelineDone{Err: pipelineErr})
			}
		}()
		result, pipelineErr := engine.Run(ctx)
		if pipelineErr == nil && result.Status != pipeline.OutcomeSuccess {
			pipelineErr = fmt.Errorf("pipeline finished with status: %s", result.Status)
		}
		outcomeCh <- pipelineOutcome{result: result, err: pipelineErr}
		prog.Send(tui.MsgPipelineDone{Err: pipelineErr})
	}()
	return outcomeCh
}
```

- [ ] **Step 2: Build to verify compilation**

Run: `go build ./cmd/tracker/`
Expected: Success

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -short`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/tracker/run.go
git commit -m "fix(tui): add panic recovery to pipeline goroutine"
```

---

## Task 6: Fix `PinnedDippinVersion` constant

**Why:** `tracker doctor` tells users to install `v0.21.0` when `go.mod` requires `v0.23.0`. First-run experience is broken — doctor gives false confidence or sends users to install the wrong version.

**Files:**
- Modify: `tracker_doctor.go:25`
- Test: `tracker_doctor_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestPinnedDippinVersionMatchesGoMod(t *testing.T) {
	// Read go.mod to find actual dippin-lang version.
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Skip("cannot read go.mod")
	}
	// Find the dippin-lang require line.
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "dippin-lang") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				goModVersion := parts[len(parts)-1]
				if goModVersion != PinnedDippinVersion {
					t.Errorf("PinnedDippinVersion = %q but go.mod has %q — update the constant", PinnedDippinVersion, goModVersion)
				}
				return
			}
		}
	}
	t.Skip("dippin-lang not found in go.mod")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run "TestPinnedDippinVersion" -v`
Expected: FAIL — `PinnedDippinVersion = "v0.21.0" but go.mod has "v0.23.0"`

- [ ] **Step 3: Fix the constant**

In `tracker_doctor.go` line 25:

```go
// Before:
const PinnedDippinVersion = "v0.21.0"

// After:
const PinnedDippinVersion = "v0.23.0"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run "TestPinnedDippinVersion" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tracker_doctor.go tracker_doctor_test.go
git commit -m "fix(doctor): update PinnedDippinVersion to v0.23.0 to match go.mod"
```

---

## Task 7: Fix `DefaultModel` to `claude-sonnet-4-6`

**Why:** `DefaultModel` is `claude-sonnet-4-5` but docs/comments say 4-6 and autopilot already uses 4-6. When 4-5 is deprecated, every pipeline without an explicit model fails.

**Files:**
- Modify: `agent/config.go:88`

- [ ] **Step 1: Update the constant**

```go
// Before:
const (
	DefaultModel    = "claude-sonnet-4-5"
	DefaultProvider = "anthropic"
)

// After:
const (
	DefaultModel    = "claude-sonnet-4-6"
	DefaultProvider = "anthropic"
)
```

- [ ] **Step 2: Search for any test that hardcodes `claude-sonnet-4-5` as the expected default**

Run: `grep -r "claude-sonnet-4-5" --include="*_test.go" .`

Fix any tests that assert the old default.

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./... -short`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add agent/config.go
git commit -m "fix(agent): update DefaultModel to claude-sonnet-4-6"
```

---

## Task 8: Fix autopilot to respect pipeline context cancellation

**Why:** All 4 autopilot LLM call sites use `context.Background()` instead of the caller's context. Pipeline cancellation (ctrl-C, budget breach) doesn't stop autopilot from spending money for up to 30s.

**Files:**
- Modify: `pipeline/handlers/autopilot.go:178,188,348,361`
- Test: `pipeline/handlers/autopilot_test.go`

- [ ] **Step 1: Thread context through `callDecisionLLM`**

The `callDecisionLLM` and `callInterviewLLM` methods need a parent context parameter. Add `parentCtx context.Context` as the first parameter to both.

In `callDecisionLLM`, change:

```go
// Before:
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
// After:
ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)

// Before (retry):
ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
// After:
ctx2, cancel2 := context.WithTimeout(parentCtx, 30*time.Second)
```

Same pattern for `callInterviewLLM`.

- [ ] **Step 2: Update all callers to pass their context**

Find all call sites of `callDecisionLLM` and `callInterviewLLM` (in `Ask`, `AskFreeform`, `AskLabeled`, `AskInterview`) and pass the context parameter they receive.

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./pipeline/handlers/ -short -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add pipeline/handlers/autopilot.go
git commit -m "fix(autopilot): respect pipeline context cancellation in LLM calls"
```

---

## Task 9: Fix broken example `.dip` files after steer.* rename

**Why:** `manager_loop_child.dip` references `${ctx.hint}` which silently returns empty after PR #196's `steer.*` namespace change.

**Files:**
- Modify: `examples/subgraphs/manager_loop_child.dip`

- [ ] **Step 1: Fix the steer key references**

In `manager_loop_child.dip`, change `${ctx.hint}` to `${ctx.steer.hint}` and `${ctx.priority}` to `${ctx.steer.priority}` in the prompt.

- [ ] **Step 2: Validate the updated pipeline**

Run: `dippin doctor examples/manager_loop_demo.dip`
Expected: A grade (or at least no errors about the steer keys)

- [ ] **Step 3: Commit**

```bash
git add examples/subgraphs/manager_loop_child.dip
git commit -m "fix(examples): update manager_loop_child to use steer.* namespace"
```

---

## Task 10: Fix stale comment in human.go and osascript injection

**Why:** `human.go:700-709` has a stale comment claiming CLAUDE.md is wrong when it's actually correct. `tui/notify.go` doesn't escape newlines in osascript strings, allowing injection.

**Files:**
- Modify: `pipeline/handlers/human.go:700-709`
- Modify: `tui/notify.go:33-46`
- Test: `tui/notify_test.go`

- [ ] **Step 1: Remove the stale comment in human.go**

Delete lines 700-709 (the comment block about CLAUDE.md questions_key inaccuracy).

- [ ] **Step 2: Write failing test for osascript newline escaping**

```go
func TestEscapeOsascript_Newlines(t *testing.T) {
	input := "done\nevil command"
	escaped := escapeOsascript(input)
	if strings.Contains(escaped, "\n") {
		t.Error("newlines should be escaped in osascript strings")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./tui/ -run "TestEscapeOsascript_Newlines" -v`
Expected: FAIL

- [ ] **Step 4: Fix `escapeOsascript` to handle newlines and carriage returns**

```go
func escapeOsascript(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, ' ')
		case '\r':
			// skip
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./tui/ -run "TestEscapeOsascript" -v`
Expected: PASS

- [ ] **Step 6: Build and run full test suite**

Run: `go build ./... && go test ./... -short`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pipeline/handlers/human.go tui/notify.go tui/notify_test.go
git commit -m "fix: remove stale human.go comment, escape newlines in osascript notifications"
```

---

## Final Verification

- [ ] **Full build:** `go build ./...`
- [ ] **Full tests:** `go test ./... -short`
- [ ] **Dippin doctor on examples:** `dippin doctor examples/manager_loop_demo.dip`
