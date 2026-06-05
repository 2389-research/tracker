# #303 PR1 — Turn-Limit Guard (verify → classify) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On native-backend turn-limit exhaustion, run an explicit verify pass and classify the breach: verified-green → `OutcomeSuccess` (advances through the pipeline's own success-path commit node), `LoopDetected` → pathological `OutcomeFail`, anything else → `OutcomeFail` + `turn_breach_class=operator_decision`. A `turn_breach_policy: fail` opt-out reproduces today's guillotine exactly.

**Architecture:** Three layers, each with one job. **agent/** is mechanism: when `SessionConfig.VerifyOnBreach` is set and the loop exhausted without a detected loop, run one verify pass (explicit command only) and record `SessionResult.BreachVerify` (tri-state). **codergen** is policy: `buildConfig` sets `VerifyOnBreach = (policy != "fail")`; a new `classifyBreach` helper maps facts → (`Outcome.Status`, `turn_breach_class`). **engine** is unchanged — green takes the success edge; non-green takes the fail edge (`fallback_target`/strict-failure). Persisting the green tree is the pipeline's success-path commit node (build_product already has `CommitIfDirty`, #297); the engine never commits product code.

**Tech Stack:** Go. Tests with the standard `testing` package. Native agent loop in `agent/`, handler dispatch in `pipeline/handlers/`, engine in `pipeline/`. Complexity gates `gocyclo`/`gocognit` ≤8 via `make complexity` (CI-only; NOT in the pre-commit hook).

**Spec:** `docs/superpowers/specs/2026-06-05-issue-303-turn-limit-guard-design.md`

**Scope:** PR1 only. The operator-decision `wait.human` node + warm `continue +N` are **PR2** (separate plan). No `.dip` edits in PR1.

**Windows note:** `agent/verify.go` is `//go:build !windows`, and `agent/session.go` already calls `newVerifier` unconditionally — so `agent/` is already non-compiling on Windows today (pre-existing; the verification matrix is linux+darwin). PR1 adds `resolveBreachVerifier` alongside `newVerifier` in the same `!windows` file and calls it the same way; it does not change the Windows status and adds no stub (a stub for one function wouldn't make the package compile while `newVerifier` remains unstubbed).

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `agent/result.go` | `SessionResult` + new `BreachVerifyState` enum & field | Modify |
| `agent/result_test.go` | zero-value/enum test | Modify |
| `agent/config.go` | `SessionConfig.VerifyOnBreach` flag | Modify |
| `agent/verify.go` | `resolveBreachVerifier` (explicit-command-only verifier) | Modify |
| `agent/session.go` | run verify-on-breach at the exhaustion site | Modify |
| `agent/session_test.go` | agent-layer verify-on-breach tests | Modify |
| `pipeline/context.go` | `ContextKeyTurnBreachClass` + class value constants | Modify |
| `pipeline/node_config.go` | `AgentNodeConfig.TurnBreachPolicy` + accessor read | Modify |
| `pipeline/trace.go` | `SessionStats.BreachVerify` field | Modify |
| `pipeline/handlers/transcript.go` | carry `BreachVerify` into `buildSessionStats` | Modify |
| `pipeline/handlers/codergen.go` | `buildConfig` flag; `classifyBreach`; rewire `buildSuccessOutcome`; thread `native` | Modify |
| `pipeline/handlers/codergen_test.go` | codergen classification tests | Modify |
| `pipeline/handlers/codergen_breach_test.go` | new: focused breach-classification tests | Create |
| `CHANGELOG.md` | [Unreleased] entry | Modify |

---

## Task 1: `BreachVerifyState` enum + `SessionResult.BreachVerify`

**Files:**
- Modify: `agent/result.go:14-34`
- Test: `agent/result_test.go`

- [ ] **Step 1: Write the failing test**

Add to `agent/result_test.go`:

```go
func TestBreachVerifyState_ZeroValueIsNotRun(t *testing.T) {
	var r SessionResult
	if r.BreachVerify != BreachVerifyNotRun {
		t.Errorf("zero-value BreachVerify = %v, want BreachVerifyNotRun", r.BreachVerify)
	}
	if BreachVerifyNotRun != 0 {
		t.Errorf("BreachVerifyNotRun = %d, want 0 (safe default)", BreachVerifyNotRun)
	}
	if BreachVerifyPassed == BreachVerifyFailed {
		t.Error("BreachVerifyPassed and BreachVerifyFailed must be distinct")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/ -run TestBreachVerifyState_ZeroValueIsNotRun`
Expected: FAIL — `undefined: BreachVerifyNotRun` / `r.BreachVerify undefined`.

- [ ] **Step 3: Write minimal implementation**

In `agent/result.go`, add the type + constants above the `SessionResult` struct (after the imports/`package` block, before `type SessionResult struct`):

```go
// BreachVerifyState records the result of the verify-on-breach pass (#303).
// The zero value is BreachVerifyNotRun, so an unset field is always the safe
// "could not verify" state — never mistaken for a pass.
type BreachVerifyState int

const (
	BreachVerifyNotRun BreachVerifyState = iota // no verify ran (not a breach, no explicit command, or loop detected)
	BreachVerifyPassed                          // verify command exited 0
	BreachVerifyFailed                          // verify failed (non-zero exit) or errored
)
```

In the `SessionResult` struct, add the field directly after `LoopDetected bool` (result.go:21):

```go
	LoopDetected       bool
	BreachVerify       BreachVerifyState // #303: result of the verify-on-breach pass
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./agent/ -run TestBreachVerifyState_ZeroValueIsNotRun`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/result.go agent/result_test.go
git commit -m "feat(agent): add BreachVerifyState tri-state to SessionResult (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `SessionConfig.VerifyOnBreach` flag

**Files:**
- Modify: `agent/config.go:42-51`

This flag is the seam: codergen (which knows the policy) sets it; the agent (mechanism only) reads it. No test of its own — it is a plain config field exercised in Task 4.

- [ ] **Step 1: Add the field**

In `agent/config.go`, immediately after the `VerifyCommand` field (config.go:51), add:

```go
	// VerifyOnBreach, when true, makes the session run one verify pass after
	// the turn loop exhausts (MaxTurns reached without a detected loop), using
	// VerifyCommand only (never auto-detection — see resolveBreachVerifier).
	// The pipeline layer sets this to (turn_breach_policy != "fail") so the
	// opt-out path pays no verify cost. Independent of VerifyAfterEdit. (#303)
	VerifyOnBreach bool
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./agent/`
Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
git add agent/config.go
git commit -m "feat(agent): add SessionConfig.VerifyOnBreach flag (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `resolveBreachVerifier` (explicit-command-only)

**Files:**
- Modify: `agent/verify.go` (after `newVerifier`, ~verify.go:141)
- Test: `agent/verify_test.go`

`newVerifier` returns nil when `!VerifyAfterEdit` and falls back to auto-detection. The breach verifier must (a) ignore the `VerifyAfterEdit` gate and (b) use the **explicit** `VerifyCommand` ONLY — auto-detected commands must NOT grant a breach success (spec decision #5: closes the coarse/empty-suite hole).

- [ ] **Step 1: Write the failing test**

Add to `agent/verify_test.go`:

```go
func TestResolveBreachVerifier_ExplicitCommandOnly(t *testing.T) {
	// Explicit command → a verifier is returned regardless of VerifyAfterEdit.
	v := resolveBreachVerifier(SessionConfig{
		VerifyAfterEdit: false,
		VerifyCommand:   "true",
		WorkingDir:      t.TempDir(),
	})
	if v == nil {
		t.Fatal("expected a verifier when VerifyCommand is set, got nil")
	}
	if v.cmd != "true" {
		t.Errorf("verifier.cmd = %q, want %q", v.cmd, "true")
	}

	// No explicit command → nil, even in a Go module dir (NO auto-detection).
	goModDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(goModDir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveBreachVerifier(SessionConfig{VerifyCommand: "", WorkingDir: goModDir}); got != nil {
		t.Errorf("expected nil (no auto-detect for breach verify), got verifier cmd=%q", got.cmd)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/ -run TestResolveBreachVerifier_ExplicitCommandOnly`
Expected: FAIL — `undefined: resolveBreachVerifier`.

- [ ] **Step 3: Write minimal implementation**

In `agent/verify.go`, after `newVerifier` (verify.go:141), add:

```go
// resolveBreachVerifier returns a verifier for the verify-on-breach pass (#303),
// or nil when no EXPLICIT verify command is configured. Unlike newVerifier it
// ignores the VerifyAfterEdit gate (a breach should be able to rescue green work
// even when in-loop verification was off) but it deliberately does NOT fall back
// to detectVerifyCommand: only an author-specified command may grant a breach
// success, so a coarse/auto-detected suite can never silently advance incomplete
// work. broadCmd is intentionally empty — the breach check is a single focused
// pass.
func resolveBreachVerifier(cfg SessionConfig) *verifier {
	cmd := strings.TrimSpace(cfg.VerifyCommand)
	if cmd == "" {
		return nil
	}
	return &verifier{cmd: cmd, workDir: cfg.WorkingDir}
}
```

(`strings` and `filepath`/`os` are already imported in verify.go / verify_test.go.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./agent/ -run TestResolveBreachVerifier_ExplicitCommandOnly`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/verify.go agent/verify_test.go
git commit -m "feat(agent): resolveBreachVerifier (explicit command only) for #303

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Wire verify-on-breach into `session.Run`

**Files:**
- Modify: `agent/session.go:205-212`
- Test: `agent/session_test.go`

When the loop exhausts (`!stoppedNaturally` → `MaxTurnsUsed = true`) and no loop was detected, run one verify pass (when `VerifyOnBreach` + explicit command) and record `BreachVerify`. A real execution error (binary missing, bad workdir — distinct from a test failure) maps to `BreachVerifyFailed` and is surfaced via `EventVerify` (never swallowed). The block is reached only when `runTurnLoop` returned `err == nil` (provider errors return early at session.go:206-208), so verify-on-breach can never mask a provider error.

- [ ] **Step 1: Write the failing tests**

Add to `agent/session_test.go`. Helper to drive plain exhaustion without tripping loop detection (varying tool-call names):

```go
// exhaustingResponses returns n tool-call responses with VARYING names so the
// loop-detector never fires (each turn's signature differs), forcing the loop
// to run to MaxTurns → plain exhaustion (MaxTurnsUsed=true, LoopDetected=false).
func exhaustingResponses(n int) []*llm.Response {
	resps := make([]*llm.Response, n)
	for i := range resps {
		resps[i] = makeToolCallResponse(fmt.Sprintf("tool_%d", i))
	}
	return resps
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSession_VerifyOnBreach_Green_RecordsPassed(t *testing.T) {
	dir := t.TempDir()
	pass := writeScript(t, dir, "pass.sh", "#!/bin/sh\nexit 0\n")
	client := &mockCompleter{responses: exhaustingResponses(3)}
	cfg := DefaultConfig()
	cfg.MaxTurns = 3
	cfg.WorkingDir = dir
	cfg.VerifyOnBreach = true
	cfg.VerifyCommand = pass
	sess := mustNewSession(t, client, cfg)

	res, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.MaxTurnsUsed || res.LoopDetected {
		t.Fatalf("want plain exhaustion, got MaxTurnsUsed=%v LoopDetected=%v", res.MaxTurnsUsed, res.LoopDetected)
	}
	if res.BreachVerify != BreachVerifyPassed {
		t.Errorf("BreachVerify = %v, want BreachVerifyPassed", res.BreachVerify)
	}
}

func TestSession_VerifyOnBreach_Red_RecordsFailed(t *testing.T) {
	dir := t.TempDir()
	fail := writeScript(t, dir, "fail.sh", "#!/bin/sh\nexit 1\n")
	client := &mockCompleter{responses: exhaustingResponses(3)}
	cfg := DefaultConfig()
	cfg.MaxTurns = 3
	cfg.WorkingDir = dir
	cfg.VerifyOnBreach = true
	cfg.VerifyCommand = fail
	sess := mustNewSession(t, client, cfg)

	res, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BreachVerify != BreachVerifyFailed {
		t.Errorf("BreachVerify = %v, want BreachVerifyFailed", res.BreachVerify)
	}
}

func TestSession_VerifyOnBreach_NoExplicitCommand_NotRun(t *testing.T) {
	client := &mockCompleter{responses: exhaustingResponses(3)}
	cfg := DefaultConfig()
	cfg.MaxTurns = 3
	cfg.WorkingDir = t.TempDir()
	cfg.VerifyOnBreach = true
	cfg.VerifyCommand = "" // no explicit command → no breach verify
	sess := mustNewSession(t, client, cfg)

	res, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BreachVerify != BreachVerifyNotRun {
		t.Errorf("BreachVerify = %v, want BreachVerifyNotRun", res.BreachVerify)
	}
}

func TestSession_VerifyOnBreach_DisabledFlag_NoRun(t *testing.T) {
	dir := t.TempDir()
	pass := writeScript(t, dir, "pass.sh", "#!/bin/sh\nexit 0\n")
	client := &mockCompleter{responses: exhaustingResponses(3)}
	cfg := DefaultConfig()
	cfg.MaxTurns = 3
	cfg.WorkingDir = dir
	cfg.VerifyOnBreach = false // flag off → mechanism never runs
	cfg.VerifyCommand = pass
	sess := mustNewSession(t, client, cfg)

	res, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BreachVerify != BreachVerifyNotRun {
		t.Errorf("BreachVerify = %v, want BreachVerifyNotRun (flag off)", res.BreachVerify)
	}
}

func TestSession_LoopDetected_SkipsBreachVerify(t *testing.T) {
	dir := t.TempDir()
	// A verify that would create a sentinel file IF it ran. Loop detection must
	// skip verify, so the sentinel must be absent.
	sentinel := filepath.Join(dir, "verify_ran")
	script := writeScript(t, dir, "touch.sh", "#!/bin/sh\ntouch "+sentinel+"\nexit 0\n")
	// Identical tool-call names every turn → loop detector fires.
	resps := make([]*llm.Response, 10)
	for i := range resps {
		resps[i] = makeToolCallResponse("read")
	}
	client := &mockCompleter{responses: resps}
	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	cfg.WorkingDir = dir
	cfg.VerifyOnBreach = true
	cfg.VerifyCommand = script
	sess := mustNewSession(t, client, cfg)

	res, err := sess.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.LoopDetected {
		t.Fatalf("expected LoopDetected=true")
	}
	if res.BreachVerify != BreachVerifyNotRun {
		t.Errorf("BreachVerify = %v, want NotRun (verify must skip on loop)", res.BreachVerify)
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Error("verify ran on a detected loop (sentinel exists) — must be skipped")
	}
}
```

Ensure `fmt`, `os`, `filepath` are imported in `agent/session_test.go` (add any missing).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./agent/ -run 'TestSession_VerifyOnBreach|TestSession_LoopDetected_SkipsBreachVerify'`
Expected: FAIL — `BreachVerify` stays `BreachVerifyNotRun` (no wiring yet), so the Green/Red assertions fail. (The skip test passes vacuously now — it becomes a real control after Step 3.)

- [ ] **Step 3: Write minimal implementation**

In `agent/session.go`, replace the exhaustion block (session.go:210-212):

```go
	if !stoppedNaturally {
		result.MaxTurnsUsed = true
	}
```

with:

```go
	if !stoppedNaturally {
		result.MaxTurnsUsed = true
		// #303 verify-on-breach: only on plain exhaustion (not a detected
		// loop), only when the pipeline asked for it via VerifyOnBreach, and
		// only against an explicit command. Reached only when runTurnLoop
		// returned err==nil above, so a provider error is never masked.
		if s.config.VerifyOnBreach && !result.LoopDetected {
			result.BreachVerify = s.runBreachVerify(ctx)
		}
	}
```

Add the method (place near `runVerifyLoop`, session.go:~408):

```go
// runBreachVerify runs a single verify pass after turn exhaustion and maps the
// result to a BreachVerifyState. A real execution error (binary missing, bad
// workdir — NOT a test failure) is surfaced via EventVerify and treated as
// Failed (non-green): per CLAUDE.md we never swallow it, and a breach must never
// advance on an unverifiable tree.
func (s *Session) runBreachVerify(ctx context.Context) BreachVerifyState {
	v := resolveBreachVerifier(s.config)
	if v == nil {
		return BreachVerifyNotRun
	}
	res, err := v.run(ctx)
	if err != nil {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-on-breach: execution error: %v", err)})
		return BreachVerifyFailed
	}
	if res.Passed {
		s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-on-breach: passed (%s)", res.Command)})
		return BreachVerifyPassed
	}
	s.emit(Event{Type: EventVerify, SessionID: s.id, Text: fmt.Sprintf("verify-on-breach: failed (exit %d, %s)", res.ExitCode, res.Command)})
	return BreachVerifyFailed
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./agent/ -run 'TestSession_VerifyOnBreach|TestSession_LoopDetected_SkipsBreachVerify'`
Expected: PASS (all five).

- [ ] **Step 5: Run the agent race + full agent suite**

Run: `go test -race -short ./agent/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add agent/session.go agent/session_test.go
git commit -m "feat(agent): run verify-on-breach at turn-exhaustion, record BreachVerify (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Context key + class constants

**Files:**
- Modify: `pipeline/context.go:52-55`

- [ ] **Step 1: Add constants**

In `pipeline/context.go`, after the `ContextKeyTurnLimitMsg` block (context.go:55), add:

```go
	// ContextKeyTurnBreachClass classifies a turn-limit breach under the
	// guard policy (#303). Set by the codergen handler on a breach; read by
	// pipeline edge conditions (e.g. `when ctx.turn_breach_class = operator_decision`).
	// Absent on normal success and on the turn_breach_policy: fail opt-out path
	// (which reproduces today's guillotine exactly).
	ContextKeyTurnBreachClass = "turn_breach_class"

	// Turn-breach classification values (#303).
	TurnBreachClassPathological     = "pathological"     // loop / no-progress → stop
	TurnBreachClassVerifiedGreen    = "verified_green"   // breach verify passed → advance as success
	TurnBreachClassOperatorDecision = "operator_decision" // steady progress, non-green → operator/fallback
)
```

Wait — these must go INSIDE the existing `const (` block. Add them as the final entries before the closing `)` of that block (do not open a new `const`). Verify the block boundaries when editing.

- [ ] **Step 2: Verify it compiles**

Run: `go build ./pipeline/`
Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
git add pipeline/context.go
git commit -m "feat(pipeline): add turn_breach_class context key + class constants (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: `AgentNodeConfig.TurnBreachPolicy` + accessor

**Files:**
- Modify: `pipeline/node_config.go` (struct ~line 48; accessor default ~line 106; read ~line 150)
- Test: `pipeline/node_config_test.go`

Default is `"guard"`; `"fail"` is the opt-out; an unrecognized value warns and defaults to guard (the classify helper treats non-"fail" as guard, so a typo is safe by construction — Task 8 adds the parse-time guard).

- [ ] **Step 1: Write the failing test**

Add to `pipeline/node_config_test.go`:

```go
func TestAgentConfig_TurnBreachPolicy(t *testing.T) {
	// Default when unset.
	n := &pipeline.Node{Attrs: map[string]string{}}
	if got := n.AgentConfig(nil).TurnBreachPolicy; got != "guard" {
		t.Errorf("default TurnBreachPolicy = %q, want %q", got, "guard")
	}
	// Node override.
	n2 := &pipeline.Node{Attrs: map[string]string{"turn_breach_policy": "fail"}}
	if got := n2.AgentConfig(nil).TurnBreachPolicy; got != "fail" {
		t.Errorf("TurnBreachPolicy = %q, want %q", got, "fail")
	}
	// Graph default, node-overridable.
	n3 := &pipeline.Node{Attrs: map[string]string{}}
	if got := n3.AgentConfig(map[string]string{"turn_breach_policy": "fail"}).TurnBreachPolicy; got != "fail" {
		t.Errorf("graph TurnBreachPolicy = %q, want %q", got, "fail")
	}
}
```

(Confirm the test package matches the file — node_config_test.go is `package pipeline_test`; the snippet uses `pipeline.Node`. If the file is `package pipeline`, drop the `pipeline.` qualifiers.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestAgentConfig_TurnBreachPolicy`
Expected: FAIL — `cfg.TurnBreachPolicy undefined`.

- [ ] **Step 3: Write minimal implementation**

(a) Add the field to `AgentNodeConfig` (node_config.go, near MaxTurns ~line 59):

```go
	MaxTurns        int
	TurnBreachPolicy string // #303: "guard" (default) or "fail" (opt-out)
```

(b) Set the default in the struct literal in `AgentConfig` (node_config.go:100-108), alongside `ReflectOnError: true`:

```go
	cfg := AgentNodeConfig{
		ReflectOnError:   true,
		TurnBreachPolicy: "guard", // #303 default: graduated guard
	}
```

(c) Add the graph-default-then-node-override read. Place it next to the `max_turns` read (node_config.go:126-130). Note: `turn_breach_policy` arrives via a dippin `params:` block, which the adapter spills into `n.Attrs`; read both graph and node attrs:

```go
	if v, ok := graphAttrs["turn_breach_policy"]; ok && v != "" {
		cfg.TurnBreachPolicy = v
	}
	if v, ok := n.Attrs["turn_breach_policy"]; ok && v != "" {
		cfg.TurnBreachPolicy = v
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run TestAgentConfig_TurnBreachPolicy`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/node_config.go pipeline/node_config_test.go
git commit -m "feat(pipeline): AgentNodeConfig.TurnBreachPolicy (guard default / fail opt-out) (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Carry `BreachVerify` into the trace

**Files:**
- Modify: `pipeline/trace.go:13-38` (`SessionStats`)
- Modify: `pipeline/handlers/transcript.go:87-114` (`buildSessionStats`)
- Test: `pipeline/handlers/transcript_test.go` (or codergen test in Task 8)

The AC "trace shows verify+commit, not fail" requires the verify result to reach the trace. `SessionResult.BreachVerify` reaches the trace only via `buildSessionStats`.

- [ ] **Step 1: Write the failing test**

Add to `pipeline/handlers/transcript_test.go` (create if absent; `package handlers`):

```go
func TestBuildSessionStats_CarriesBreachVerify(t *testing.T) {
	stats := buildSessionStats(agent.SessionResult{BreachVerify: agent.BreachVerifyPassed})
	if stats.BreachVerify != int(agent.BreachVerifyPassed) {
		t.Errorf("SessionStats.BreachVerify = %d, want %d", stats.BreachVerify, int(agent.BreachVerifyPassed))
	}
}
```

(Imports: `testing`, `github.com/2389-research/tracker/agent`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestBuildSessionStats_CarriesBreachVerify`
Expected: FAIL — `stats.BreachVerify undefined`.

- [ ] **Step 3: Write minimal implementation**

(a) In `pipeline/trace.go`, add to `SessionStats` (after `EstimateSource`):

```go
	// BreachVerify is the verify-on-breach result (#303): 0=not-run, 1=passed,
	// 2=failed. Mirrors agent.BreachVerifyState as an int so the trace JSON is
	// self-contained. Non-zero only on a turn-limit breach under guard policy.
	BreachVerify int `json:"breach_verify,omitempty"`
```

(b) In `pipeline/handlers/transcript.go` `buildSessionStats`, add to the returned struct literal:

```go
		BreachVerify:     int(r.BreachVerify),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/handlers/ -run TestBuildSessionStats_CarriesBreachVerify`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/trace.go pipeline/handlers/transcript.go pipeline/handlers/transcript_test.go
git commit -m "feat(pipeline): carry BreachVerify into SessionStats trace (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: `classifyBreach` + rewire `buildSuccessOutcome` (the core)

**Files:**
- Modify: `pipeline/handlers/codergen.go` (`buildConfig` ~668; `buildOutcome` 482/109; `buildSuccessOutcome` 543-587; new `classifyBreach`)
- Test: `pipeline/handlers/codergen_breach_test.go` (create)

This is the keystone. It (1) sets `VerifyOnBreach` from policy in `buildConfig`, (2) threads `native` from `Execute` down to `buildSuccessOutcome`, (3) replaces the flat breach→fail with `classifyBreach`, (4) ensures `auto_status` cannot manufacture a breach success and `applyDeclaredWrites` demotion clears the green marker. The classification is extracted into a helper to keep `buildSuccessOutcome` under the ≤8 complexity gate.

- [ ] **Step 1: Write the failing tests**

Create `pipeline/handlers/codergen_breach_test.go`:

```go
package handlers

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

func TestClassifyBreach_VerifiedGreenAdvancesAsSuccess(t *testing.T) {
	status, class := classifyBreach("guard", agent.SessionResult{BreachVerify: agent.BreachVerifyPassed}, true)
	if status != pipeline.OutcomeSuccess {
		t.Errorf("status = %q, want success", status)
	}
	if class != pipeline.TurnBreachClassVerifiedGreen {
		t.Errorf("class = %q, want %q", class, pipeline.TurnBreachClassVerifiedGreen)
	}
}

func TestClassifyBreach_LoopDetectedAlwaysPathological(t *testing.T) {
	// Even a green verify cannot rescue a detected loop.
	status, class := classifyBreach("guard", agent.SessionResult{LoopDetected: true, BreachVerify: agent.BreachVerifyPassed}, true)
	if status != pipeline.OutcomeFail {
		t.Errorf("status = %q, want fail", status)
	}
	if class != pipeline.TurnBreachClassPathological {
		t.Errorf("class = %q, want %q", class, pipeline.TurnBreachClassPathological)
	}
}

func TestClassifyBreach_RedAndNotRunRouteToOperator(t *testing.T) {
	for _, bv := range []agent.BreachVerifyState{agent.BreachVerifyFailed, agent.BreachVerifyNotRun} {
		status, class := classifyBreach("guard", agent.SessionResult{BreachVerify: bv}, true)
		if status != pipeline.OutcomeFail || class != pipeline.TurnBreachClassOperatorDecision {
			t.Errorf("bv=%v: got (%q,%q), want (fail,operator_decision)", bv, status, class)
		}
	}
}

func TestClassifyBreach_FailPolicyIsGuillotine(t *testing.T) {
	status, class := classifyBreach("fail", agent.SessionResult{BreachVerify: agent.BreachVerifyPassed}, true)
	if status != pipeline.OutcomeFail {
		t.Errorf("opt-out status = %q, want fail", status)
	}
	if class != "" {
		t.Errorf("opt-out class = %q, want empty (no marker)", class)
	}
}

func TestClassifyBreach_NonNativeIsGuillotine(t *testing.T) {
	status, class := classifyBreach("guard", agent.SessionResult{BreachVerify: agent.BreachVerifyPassed}, false)
	if status != pipeline.OutcomeFail || class != "" {
		t.Errorf("non-native got (%q,%q), want (fail, \"\")", status, class)
	}
}
```

Add an Execute-level test in `pipeline/handlers/codergen_breach_test.go` proving the green breach surfaces as a success outcome with the marker (drives the full native path with `alwaysToolCallCompleter` + an explicit passing verify command):

```go
func TestExecute_BreachGreen_AdvancesAsSuccessWithMarker(t *testing.T) {
	workdir := t.TempDir()
	pass := workdir + "/pass.sh"
	if err := writeFile755(pass, "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt":         "build it",
			"max_turns":      "3",
			"verify_command": pass,
			// turn_breach_policy defaults to "guard"
		},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != string(pipeline.OutcomeSuccess) {
		t.Errorf("status = %q, want success (verified-green breach)", out.Status)
	}
	if out.ContextUpdates[pipeline.ContextKeyTurnBreachClass] != pipeline.TurnBreachClassVerifiedGreen {
		t.Errorf("turn_breach_class = %q, want verified_green", out.ContextUpdates[pipeline.ContextKeyTurnBreachClass])
	}
}

func TestExecute_BreachRed_FailsWithOperatorMarker(t *testing.T) {
	workdir := t.TempDir()
	fail := workdir + "/fail.sh"
	if err := writeFile755(fail, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatal(err)
	}
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "build it", "max_turns": "3", "verify_command": fail},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != string(pipeline.OutcomeFail) {
		t.Errorf("status = %q, want fail", out.Status)
	}
	if out.ContextUpdates[pipeline.ContextKeyTurnBreachClass] != pipeline.TurnBreachClassOperatorDecision {
		t.Errorf("turn_breach_class = %q, want operator_decision", out.ContextUpdates[pipeline.ContextKeyTurnBreachClass])
	}
}

func TestExecute_TurnBreachPolicyFail_PinsGuillotine(t *testing.T) {
	workdir := t.TempDir()
	pass := workdir + "/pass.sh"
	if err := writeFile755(pass, "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "build it", "max_turns": "3",
			"verify_command":     pass,
			"turn_breach_policy": "fail", // opt-out
		},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Byte-for-byte today's behavior: fail + exact message, NO marker.
	if out.Status != string(pipeline.OutcomeFail) {
		t.Errorf("opt-out status = %q, want fail", out.Status)
	}
	wantMsg := `node "Implement": agent exhausted turn limit (3 turns) without completing`
	if got := out.ContextUpdates[pipeline.ContextKeyTurnLimitMsg]; got != wantMsg {
		t.Errorf("turn_limit_msg = %q, want %q", got, wantMsg)
	}
	if _, present := out.ContextUpdates[pipeline.ContextKeyTurnBreachClass]; present {
		t.Error("opt-out must NOT set turn_breach_class")
	}
}

func writeFile755(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o755)
}
```

(Add imports `context`, `os` to the test file.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/handlers/ -run 'TestClassifyBreach|TestExecute_Breach|TestExecute_TurnBreachPolicyFail'`
Expected: FAIL — `undefined: classifyBreach`; Execute green test fails (today returns fail, no marker).

- [ ] **Step 3: Write the implementation**

(a) `buildConfig` — set the flag from policy. After the verify-command copy (codergen.go:~691, after `config.VerifyCommand = cfg.VerifyCommand`), add:

```go
	config.VerifyOnBreach = cfg.TurnBreachPolicy != "fail"
```

(b) Add the helper (place after `buildTurnLimitMsg`, codergen.go:~624):

```go
// classifyBreach maps a turn-limit breach to (status, turn_breach_class) under
// the turn_breach_policy (#303). Called only when buildTurnLimitMsg != "".
//   - policy "fail" or non-native backend → today's guillotine (fail, no marker).
//   - LoopDetected → pathological (fail).
//   - BreachVerifyPassed → verified-green (success; the pipeline's success edge
//     persists the tree).
//   - everything else (Failed / NotRun) → operator_decision (fail; routes to
//     fallback / an operator gate). Never silently advances.
func classifyBreach(policy string, r agent.SessionResult, native bool) (pipeline.TerminalStatus, string) {
	if policy == "fail" || !native {
		return pipeline.OutcomeFail, ""
	}
	switch {
	case r.LoopDetected:
		return pipeline.OutcomeFail, pipeline.TurnBreachClassPathological
	case r.BreachVerify == agent.BreachVerifyPassed:
		return pipeline.OutcomeSuccess, pipeline.TurnBreachClassVerifiedGreen
	default:
		return pipeline.OutcomeFail, pipeline.TurnBreachClassOperatorDecision
	}
}
```

(c) Thread `native` through. Change `buildOutcome` signature (codergen.go:482) and call (codergen.go:109), and `buildSuccessOutcome` signature (codergen.go:543) and call (codergen.go:494), adding a trailing `native bool` param. In `Execute` (codergen.go:74), compute it from the resolved backend (mirrors `trackExternalBackendUsage`'s type switch):

```go
	_, native := backend.(*NativeBackend)
```

and pass `native` into `h.buildOutcome(node, prompt, artifactRoot, sessResult, &collector, priorEpisodes, native)`, then `h.buildSuccessOutcome(node, prompt, artifactRoot, responseText, responseArtifact, sessResult, priorEpisodes, native)`.

(d) Rewire `buildSuccessOutcome`. Replace the current breach + auto_status block (codergen.go:552-573) with the guarded version. The key safety change: on a breach, `auto_status` may NOT upgrade to success.

Replace:

```go
	status := pipeline.OutcomeSuccess
	turnLimitMsg := buildTurnLimitMsg(node, sessResult)
	if turnLimitMsg != "" {
		status = pipeline.OutcomeFail
	}

	if node.Attrs["auto_status"] == "true" {
		status = parseAutoStatus(responseText)
	}
```

with:

```go
	status := pipeline.OutcomeSuccess
	turnLimitMsg := buildTurnLimitMsg(node, sessResult)
	var breachClass string
	if turnLimitMsg != "" {
		// #303: a breach is classified, not unconditionally failed.
		policy := node.AgentConfig(h.graphAttrs).TurnBreachPolicy
		status, breachClass = classifyBreach(policy, sessResult, native)
	}

	// auto_status: an explicit STATUS line is authoritative on NORMAL completion.
	// On a breach it must NOT manufacture success — a missing/early STATUS line
	// defaults parseAutoStatus to success, which would silently advance
	// unverified work (#303 decision #5). So apply it only when not a breach.
	if node.Attrs["auto_status"] == "true" && turnLimitMsg == "" {
		status = parseAutoStatus(responseText)
	}
```

(e) Marker write + declared-writes precedence. After `applyDeclaredWrites` (codergen.go:571-573) and before the `turnLimitMsg` context write (codergen.go:574-576), set the marker using the FINAL status:

```go
	if applyDeclaredWrites(node, outcome.ContextUpdates, responseText, "Response JSON") {
		outcome.Status = string(pipeline.OutcomeFail)
	}
	// #303: write the breach class only after the final status is known, and
	// never leave a verified_green marker on a Fail (a declared-writes failure
	// can demote a green breach). Absent on the opt-out / non-native path
	// (breachClass == "").
	if breachClass != "" {
		if outcome.Status == string(pipeline.OutcomeFail) && breachClass == pipeline.TurnBreachClassVerifiedGreen {
			breachClass = pipeline.TurnBreachClassOperatorDecision
		}
		outcome.ContextUpdates[pipeline.ContextKeyTurnBreachClass] = breachClass
	}
```

Note: the existing `outcome.Status` is set from `status` at the struct literal (codergen.go:562-563) — leave that as-is; it now carries the classified status. The `applyEpisodeContextUpdates` call (codergen.go:570) stays before this block so episodes are carried on the green path (warm advance).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/handlers/ -run 'TestClassifyBreach|TestExecute_Breach|TestExecute_TurnBreachPolicyFail'`
Expected: PASS.

- [ ] **Step 5: Add the auto_status + declared-writes guard tests**

Append to `pipeline/handlers/codergen_breach_test.go`:

```go
func TestExecute_BreachRed_AutoStatusCannotForceSuccess(t *testing.T) {
	workdir := t.TempDir()
	fail := workdir + "/fail.sh"
	if err := writeFile755(fail, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatal(err)
	}
	// alwaysToolCallCompleter emits no STATUS line → parseAutoStatus would
	// default to success. The breach guard must prevent that.
	h := NewCodergenHandler(&alwaysToolCallCompleter{}, workdir)
	node := &pipeline.Node{
		ID: "Implement", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{
			"prompt": "build it", "max_turns": "3",
			"verify_command": fail,
			"auto_status":    "true",
		},
	}
	out, err := h.Execute(context.Background(), node, pipeline.NewPipelineContext())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Status != string(pipeline.OutcomeFail) {
		t.Errorf("status = %q, want fail (auto_status must not rescue a red breach)", out.Status)
	}
}
```

Run: `go test ./pipeline/handlers/ -run TestExecute_BreachRed_AutoStatusCannotForceSuccess`
Expected: PASS (fails-first if the auto_status guard is omitted).

- [ ] **Step 6: Verify the complexity gate**

Run: `gocyclo -over 8 pipeline/handlers/codergen.go; gocognit -over 8 pipeline/handlers/codergen.go`
Expected: no output for `buildSuccessOutcome` / `classifyBreach` (both ≤8). If `buildSuccessOutcome` exceeds 8, extract the marker-write block into a helper `applyBreachMarker(outcome, breachClass)` and re-run.

- [ ] **Step 7: Commit**

```bash
git add pipeline/handlers/codergen.go pipeline/handlers/codergen_breach_test.go
git commit -m "feat(codergen): classify turn-limit breach (verify-green→success, loop→fail, else→operator) (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Non-native backend regression guard

**Files:**
- Test: `pipeline/handlers/codergen_breach_test.go`

Prove a non-native backend never gets the marker / never auto-advances on a breach. `classifyBreach(..., native=false)` already returns `(fail, "")` — this is the unit guard (the `TestClassifyBreach_NonNativeIsGuillotine` from Task 8 covers it). Add an explicit comment-test asserting the contract is intentional so a future refactor that drops the `native` guard fails here.

- [ ] **Step 1: Confirm the guard test exists and passes**

Run: `go test ./pipeline/handlers/ -run TestClassifyBreach_NonNativeIsGuillotine`
Expected: PASS. (No new code; this task is the explicit checkpoint that the native guard is covered. If it is missing, add it per Task 8 Step 1.)

- [ ] **Step 2: Commit (if any change)**

```bash
git add pipeline/handlers/codergen_breach_test.go
git commit -m "test(codergen): pin non-native breach to today's behavior (#303)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: build_product case-study rescue (routing-level proof)

**Files:**
- Test: `pipeline/build_product_failure_routing_test.go`

No `.dip` change. Prove that build_product's existing graph routes a green breach (`OutcomeSuccess`) through `Implement -> CommitIfDirty` (which persists the product tree, #297), NOT `-> EscalateMilestone`. This is where "green work is persisted" is verified — at the routing level, not via the (default-off) artifact repo.

- [ ] **Step 1: Write the failing test**

Add to `pipeline/build_product_failure_routing_test.go` (study the existing helpers there for loading the embedded graph and reading edges; reuse them):

```go
// #303: a verified-green breach returns OutcomeSuccess, so Implement takes its
// SUCCESS edge to CommitIfDirty (which commits the product tree, #297) rather
// than the failure edge to EscalateMilestone. This is the case-study rescue.
func TestBuildProduct_Implement_SuccessEdge_GoesToCommitIfDirty(t *testing.T) {
	g := loadBuildProductGraph(t) // existing helper in this test file
	var successTarget string
	for _, e := range g.OutgoingEdges("Implement") {
		if edgeMatchesSuccess(e) { // condition contains outcome = success
			successTarget = e.To
		}
	}
	if successTarget != "CommitIfDirty" {
		t.Errorf("Implement success edge → %q, want CommitIfDirty (green-breach persistence path)", successTarget)
	}
}
```

If `loadBuildProductGraph` / `edgeMatchesSuccess` helpers don't exist verbatim, adapt to the file's actual helpers (it already asserts `Implement -> CommitIfDirty -> TestMilestone` per #297's `TestBuildProductCommitIfDirtyCheckpoint` — model the new test on it; this test may already be partly covered there, in which case extend that test with a comment tying it to #303 rather than duplicating).

- [ ] **Step 2: Run test to verify it passes (regression, not fail-first)**

Run: `go test ./pipeline/ -run TestBuildProduct_Implement_SuccessEdge_GoesToCommitIfDirty`
Expected: PASS — the edge already exists (#297). This test documents the #303 dependency so a future `.dip` change that reroutes the success edge can't silently break the rescue.

- [ ] **Step 3: Commit**

```bash
git add pipeline/build_product_failure_routing_test.go
git commit -m "test(build_product): pin Implement success edge → CommitIfDirty as the #303 green-breach rescue

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: CHANGELOG + full verification

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add CHANGELOG entry**

Under `## [Unreleased]`, in `### Added` (create the subsection if absent):

```markdown
### Added
- **Graduated turn-limit guard (#303, Phase-1 turn-limit track).** On native-backend
  turn-limit exhaustion, the engine no longer fails unconditionally: it runs an
  explicit verify pass and classifies the breach. A verified-green tree advances as
  success (routing through the pipeline's commit-on-success node, e.g. build_product's
  `CommitIfDirty`); a detected loop is classified pathological and stops; anything
  else fails with `ctx.turn_breach_class = operator_decision` for routing. A
  `turn_breach_policy: fail` node attribute (declare under a `params:` block) opts
  back into the previous always-fail behavior. Builds on #302/#295/#297.
```

- [ ] **Step 2: Full verification matrix**

```bash
go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
go test ./... -short
go test -race -short ./pipeline/ ./agent/
make complexity
dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip
```

Expected: all green; `make complexity` reports nothing over 8; `dippin doctor` shows build_product A (90), ask_and_execute A (95), build_product_with_superspec A (100) — unchanged (no `.dip` touched).

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: changelog for #303 graduated turn-limit guard (PR1)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 4: Adversarial self-review before PR**

Re-read the diff against the spec's invariants. Confirm by inspection + test:
- No path to `OutcomeSuccess` on a breach except `BreachVerify==Passed` (green) — incl. `auto_status` and declared-writes paths.
- `LoopDetected` always fails (never success), even with a green verify.
- Opt-out (`turn_breach_policy: fail`) reproduces the exact `turn_limit_msg` and sets no marker.
- Non-native backends are unchanged.
- Provider errors still hard-fail (verify-on-breach is behind `err==nil`).

---

## Self-Review (plan vs spec)

**Spec coverage:**
- verify-on-breach (agent) → Tasks 1–4. ✓
- policy seam via `SessionConfig.VerifyOnBreach` → Tasks 2, 8(a). ✓
- strict green bar (explicit command, auto_status can't force success, auto-detect→operator) → Tasks 3, 8(d). ✓
- classify (green/loop/non-green) + native guard → Task 8(b). ✓
- complexity helper extraction + `make complexity` in verification → Tasks 8(6), 11. ✓
- `turn_breach_policy` typed accessor, guard default, params-block note → Task 6. ✓
- context marker + class constants → Task 5. ✓
- trace carries BreachVerify → Task 7. ✓
- opt-out byte-for-byte pin (msg + marker absent) → Task 8 `TestExecute_TurnBreachPolicyFail_PinsGuillotine`. ✓
- case-study rescue at routing level (not artifact repo) → Task 10. ✓
- CHANGELOG → Task 11. ✓
- PR2 items (operator node, warm continue) → explicitly deferred. ✓

**Type consistency:** `BreachVerifyState`/`BreachVerifyNotRun/Passed/Failed` (Task 1) used in Tasks 3/4/7/8. `VerifyOnBreach` (Task 2) set in 8(a), read in 4. `TurnBreachPolicy` (Task 6) read in 8(d). `classifyBreach(policy string, r agent.SessionResult, native bool) (pipeline.TerminalStatus, string)` (Task 8) called consistently. `ContextKeyTurnBreachClass` + `TurnBreachClass*` (Task 5) used in 8/tests. `SessionStats.BreachVerify int` (Task 7). Consistent.

**Placeholder scan:** none — every code step shows full code; the only "adapt to existing helpers" note is Task 10, which points at the concrete existing test (`TestBuildProductCommitIfDirtyCheckpoint`) to model.
