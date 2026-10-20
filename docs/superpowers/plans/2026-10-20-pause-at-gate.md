# Pause at a Human Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A run without a TTY pauses at a human gate in a resumable `paused_gate` terminal, and `tracker answer` validates an answer and resumes it.

**Architecture:** A new `handlers.PausingInterviewer` returns a typed `PauseError` carrying a gate record; the engine's existing pause path stores the record in the checkpoint (secure dir), mirrors it to `gate.json`, and halts. `tracker answer` rebuilds the run from a recorded invocation and resumes it with the answer held in memory by a `PausingInterviewer`, which applies it once to the gate whose scope, node and fingerprint match.

**Tech Stack:** Go; existing `pipeline` engine, `pipeline/handlers`, `tracker` library, `cmd/tracker` CLI.

**Spec:** `docs/superpowers/specs/2026-10-20-pause-at-gate-design.md`

## Global Constraints

- Never `--no-verify`; every commit passes the pre-commit gates (fmt, vet, build, tests, complexity ratchet, dippin lint/scenarios, docs, shell).
- Complexity ratchet: no new or worse cyclo/cognitive/file-size violation; `make complexity-update` may only shrink the baseline.
- Exit codes: 0 success, 1 failure (incl. `paused_billing`), 2 warnings / `--fail-on-override`, **4 = paused at a gate**.
- Status string `paused_gate`; event type `gate_paused`; `billing_paused` / `paused_billing` keep their names and meaning.
- `${inputs.*}` / LLM-origin `ctx.*` never reach tool commands — nothing in this plan adds an interpolation.
- Every new env var registers in `internal/envpolicy` (this plan adds none).
- Operator copy must not claim a paused gate can't be forged or that the secure dir is tamper-proof.
- `gate.json` is never read by Tracker to make a decision.
- `make docs-check`: `tracker answer` must be documented in `site/content/cli.html` in the same change that adds the command.
- CHANGELOG entry in the same branch as each change (Keep a Changelog; Added / Changed / Fixed).
- PR flow: branch → PR → bots → fix + reply → merge when green (`gh pr merge <n> --merge --delete-branch`).

## Review Focus

1. A gate whose prompt embeds `last_response` re-renders after resume: with no compaction the prompt is byte-identical, so the answer applies on the first try (no "question changed" loop).
2. `tracker -r` of a gate-paused run at a TTY prompts interactively, and the gate's completion clears `PausedGates` so `tracker answer` then refuses ("not waiting").
3. Labels typed with different case / surrounding spaces (`"  Approve "`) validate and route like the canonical label.
4. Ctrl-C during an answer-resume releases the run lock; a lock left by a killed process (dead PID) is replaced, not a permanent refusal.
5. A `--json` consumer sees exactly one terminal event (`gate_paused`) carrying the answer command, and no `stage_failed` / `pipeline_failed`.

Each item's test is placed in its owning task (marked **RF1–RF5**).

---

## PR 1 — engine pause plumbing (branch `feat/pause-gate-engine`)

### Task 1: `paused_gate` status, `IsPaused()`, per-status terminal event

**Files:**
- Modify: `pipeline/pause.go`, `pipeline/terminal_status.go`, `pipeline/events.go`
- Modify: `tracker_audit_status.go` (`statusClassFor`, `terminalHaltStatus`), `tracker_runmanager.go:306`, `cmd/tracker/summary.go:214`, `tui/statusbar.go:112`, `transport/chatops/delivery.go:159`
- Test: `pipeline/engine_pause_test.go`, `pipeline/terminal_status_test.go`, `tracker_audit_status_test.go`, `tracker_runmanager_test.go`

**Interfaces:**
- Produces: `const OutcomePausedGate TerminalStatus = "paused_gate"`; `func (s TerminalStatus) IsPaused() bool` (true for `paused_billing`, `paused_gate`); `EventGatePaused PipelineEventType = "gate_paused"`; `func pauseEventType(s TerminalStatus) PipelineEventType`.

- [ ] **Step 1: Write the failing tests**
  - `TestTerminalStatus_IsPaused`: `paused_billing`, `paused_gate` → true; `success`, `fail`, `budget_exceeded`, `""` → false.
  - `TestHaltForPause_EmitsPerStatusEvent`: a handler returning `NewPauseError(OutcomePausedGate, errors.New("x"))` → exactly one terminal event of type `gate_paused` with `TerminalStatus == "paused_gate"`, and `result.Status == OutcomePausedGate`; the existing billing test still sees `billing_paused`.
  - `TestStatusClassFor_PausedGate`: `statusClassFor("paused_gate") == "paused"`.
  - `TestTerminalHaltStatus_GatePaused`: `terminalHaltStatus("gate_paused")` → `("paused_gate", true)`.
  - `TestRunManager_PausedGateIsPaused`: RunManager maps a `paused_gate` result the way it maps `paused_billing`.
- [ ] **Step 2: Run** `go test ./pipeline/ ./ -run 'IsPaused|PerStatusEvent|PausedGate|GatePaused' -short` — Expected: FAIL (undefined `OutcomePausedGate`).
- [ ] **Step 3: Implement.** `haltForPause` emits `pauseEventType(pe.Status)` (`gate_paused` for `OutcomePausedGate`, else `billing_paused`). Replace every `== OutcomePausedBilling` classification listed in Files with `.IsPaused()`; `summary.go` and `delivery.go` keep a billing-specific branch for the 💳 text and add a `paused_gate` branch ("⏸ Paused at gate"). Document `paused_gate` in the `TerminalStatus` doc comment and the `tracker.go:215` / `tracker_audit.go:36,103,108` enum comments.
- [ ] **Step 4: Run** the Step 2 command plus `go test ./tui/ ./transport/... ./cmd/tracker/ -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(pipeline): paused_gate terminal status and gate_paused event`

### Task 2: pauses propagate out of subgraph and manager_loop children

**Files:**
- Modify: `pipeline/pause.go` (`haltForPause` returns `pe`), `pipeline/subgraph.go:97-101`, `pipeline/handlers/manager_loop.go` (`handleChildResult`)
- Test: `pipeline/subgraph_pause_test.go` (new), `pipeline/handlers/manager_loop_pause_test.go` (new)

**Interfaces:**
- Consumes: Task 1.
- Produces: `Engine.Run` returns the `*PauseError` itself (its `Unwrap` still yields the provider error, so `llm.IsBillingError(err)` and `ClassifyFailure` are unchanged).

- [ ] **Step 1: Write the failing tests**
  - `TestSubgraph_ChildBillingPausePausesParent`: parent `Start → Sub(subgraph) → Exit`; child node returns `NewPauseError(OutcomePausedBilling, …)` → parent `result.Status == OutcomePausedBilling`, one `billing_paused` event from the parent, checkpoint `CurrentNode == "Sub"` and `Sub` not completed; resuming with a child that succeeds completes the run.
  - `TestManagerLoop_ChildPausePropagates`: a `manager_loop` child that billing-pauses → the handler returns an error for which `errors.As(err, &*pipeline.PauseError)` holds with `Status == OutcomePausedBilling`.
  - `TestHaltForPause_ReturnsPauseError`: `errors.As(runErr, &pe)` succeeds and `llm.IsBillingError(runErr)` still holds for a wrapped billing error.
- [ ] **Step 2: Run** `go test ./pipeline/ ./pipeline/handlers/ -run 'ChildBillingPause|ChildPausePropagates|ReturnsPauseError' -short` — Expected: FAIL (parent sees a fatal handler error).
- [ ] **Step 3: Implement.** `haltForPause` returns `err: pe`. `SubgraphHandler.Execute`: when `result != nil && result.Status.IsPaused()`, return `(Outcome{Status: OutcomeFail}, err)` with `err` the child's `*PauseError` unwrapped-as-is (wrap with `%w` so `asPauseError` finds it). `handleChildResult`: before `handleChildEngineResult`, if `msg.result != nil && msg.result.Status.IsPaused()`, return the child `*PauseError` from `msg.err` the same way.
- [ ] **Step 4: Run** Step 2 command, then `go test ./... -short` — Expected: PASS.
- [ ] **Step 5: Commit** `fix(pipeline): a paused subgraph or manager_loop child pauses the parent`

### Task 3: last-terminal-wins run status

**Files:**
- Modify: `tracker_audit_status.go` (`statusFromActivity`)
- Test: `tracker_audit_status_test.go`

**Interfaces:**
- Produces: `func lastSegment(activity []ActivityEntry) []ActivityEntry` — entries after the last `pipeline_started`.

- [ ] **Step 1: Write the failing tests** — `TestStatusFromActivity_LastSegmentWins` table:
  - `[started, billing_paused, started, completed]` → `success`
  - `[started, gate_paused, started, gate_paused]` → `paused_gate`
  - `[started, gate_paused, started, pipeline_failed]` → `fail`
  - `[started, completed, validation_overridden]` (single segment, existing order) → unchanged result from today.
- [ ] **Step 2: Run** `go test . -run LastSegmentWins -short` — Expected: FAIL on row 1 (`paused_billing`).
- [ ] **Step 3: Implement** `statusFromActivity` scanning `lastSegment(activity)`; a log with no `pipeline_started` is scanned whole (today's behavior).
- [ ] **Step 4: Run** `go test . -short` — Expected: PASS.
- [ ] **Step 5: Commit** `fix(audit): a resumed run's status comes from its latest segment`

### Task 4: no `stage_failed` for a pause

**Files:**
- Modify: `pipeline/engine_run.go:578-600` (handler-error trace/event path), `pipeline/handlers/parallel.go:~404` (branch result path)
- Test: `pipeline/engine_pause_test.go`, `pipeline/handlers/parallel_branch_test.go`; regenerate `cmd/tracker-conformance` golden traces if they change.

- [ ] **Step 1: Write the failing tests** — `TestPause_NoStageFailed` (engine, billing and gate status): no `stage_failed` and no `pipeline_failed` event for the paused node; `TestParallel_BranchPauseNoStageFailed`: no `stage_failed` for the paused branch.
- [ ] **Step 2: Run** `go test ./pipeline/... -run 'NoStageFailed' -short` — Expected: FAIL.
- [ ] **Step 3: Implement**: skip the `EventStageFailed` emission when `asPauseError(err)` matches (handlers package: `errors.As(err, &pe)`); trace entry status becomes `"paused"` instead of `"error"`.
- [ ] **Step 4: Run** `go test ./... -short` and `go test ./cmd/tracker-conformance -run TestGoldenTraces`; if a golden fixture changes only by the dropped `stage_failed`, regenerate with `-update-golden` and commit it. Expected: PASS.
- [ ] **Step 5: Commit** `fix(pipeline): a paused node does not emit stage_failed`; CHANGELOG (Fixed: subgraph billing pause, resumed-run status, no stage_failed on pause). Open PR 1 (`Part of` the 2a tracking issue — file it first: "Pause at a human gate (agent-driven Tracker 2a)").

---

## PR 2 — pausing interviewer, checkpoint record, library (branch `feat/pause-gate-core`, from main after PR 1)

### Task 5: gate record in the checkpoint, `gate.json` mirror, fingerprint

**Files:**
- Create: `pipeline/paused_gate.go`, `pipeline/paused_gate_test.go`
- Modify: `pipeline/pause.go`, `pipeline/checkpoint.go` (fields), `pipeline/engine.go` (`handleNodeError`), `pipeline/engine.go` / `finishNode` (clear), `pipeline/engine_run.go` (stale mirror removal at start)

**Interfaces:**
- Produces:
  ```go
  type PausedGate struct {
      TopNode     string           `json:"top_node"`   // node the engine paused at (the gate, or its parallel node)
      NodeID      string           `json:"node_id"`    // the gate node
      ScopePath   []string         `json:"scope_path,omitempty"` // parallel branch id(s)
      Mode        string           `json:"mode"`
      Label       string           `json:"label"`
      Prompt      string           `json:"prompt"`     // full, untruncated
      Choices     []string         `json:"choices,omitempty"`   // exactly as passed to Ask*
      Default     string           `json:"default,omitempty"`   // exactly as passed to Ask*
      Questions   []GateQuestion   `json:"questions,omitempty"`
      BranchIndex int              `json:"branch_index"`
      Fingerprint string           `json:"fingerprint"`
      Nonce       string           `json:"nonce"`
      Note        string           `json:"note,omitempty"` // "the question changed since you answered — answer again"
      At          time.Time        `json:"at"`
  }
  func GateFingerprint(g PausedGate) string // sha256 hex over ScopePath, NodeID, Mode, Prompt, Choices, Default, Questions (text, context, options, yes/no)
  func NewPausedGateError(gates ...PausedGate) *PauseError // Status OutcomePausedGate
  // PauseError gains: Gates []PausedGate
  // Checkpoint gains: PausedGates []PausedGate `json:"paused_gates,omitempty"`; PriorUsage *UsageSummary `json:"prior_usage,omitempty"`
  const GateMirrorFile = "gate.json"
  ```

- [ ] **Step 1: Write the failing tests**
  - `TestGateFingerprint_StableAndSensitive`: same record → same hash; changing any of Prompt / Choices order / Default / ScopePath / a question's option → different; changing `Nonce`, `At`, `Note` → same.
  - `TestPausedGate_StoredInCheckpoint`: a handler returning `NewPausedGateError(g)` → secure checkpoint has `PausedGates == [g]` with `TopNode` set by the engine to the paused node, `CurrentNode` that node, node not completed; `PriorUsage` equals the run's aggregate usage.
  - `TestPausedGate_MirrorWritten`: `<artifactRunDir>/gate.json` exists, mode 0600, JSON round-trips to the same records; when it pre-exists as a symlink the write refuses (`O_NOFOLLOW`) and the run still pauses (warning event).
  - `TestPausedGate_ClearedOnCompletion`: resuming with a handler that now succeeds → after the node completes, `PausedGates` is nil in the saved checkpoint and `gate.json` is removed.
  - `TestRunStart_RemovesStaleMirror`: a fresh run removes a stale `gate.json` in its run dir.
  - `TestGatePausedEvent_CarriesGate`: the `gate_paused` event's `Gate` is a `GateDetail` built from the first record (`Resolution: "paused"`), plus `Fingerprint`.
- [ ] **Step 2: Run** `go test ./pipeline/ -run 'GateFingerprint|PausedGate|StaleMirror|GatePausedEvent' -short` — Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** In `handleNodeError`, before `saveCheckpoint`: if the pause carries gates, stamp `TopNode = currentNodeID` and set `s.cp.PausedGates`; for any pause set `s.cp.PriorUsage = e.combinedUsageForBudget(s)`. `haltForPause` writes the mirror (atomic temp + rename, `O_NOFOLLOW`, 0600) and puts the gate on the event. Clear in `finishNode` when a record's `TopNode == currentNodeID`. Add `GateDetail.Resolution string \`json:"resolution,omitempty"\`` and `GateDetail.Fingerprint string \`json:"fingerprint,omitempty"\`` in `pipeline/events_gate.go`; regenerate the activity-log reader schema (`make` target the docs gate names) if it covers `GateDetail`.
- [ ] **Step 4: Run** `go test ./pipeline/... -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(pipeline): paused gate record in the checkpoint with a gate.json mirror`

### Task 6: gate scope on the context

**Files:**
- Create: `pipeline/scope.go` (+ test)
- Modify: `pipeline/subgraph.go` (`Execute`, before `engine.Run`), `pipeline/handlers/manager_loop.go` (`startChildRun`), `pipeline/handlers/human_gate_aware.go` (`GateInfo`), `pipeline/handlers/human.go` (`Execute`)

**Interfaces:**
- Produces:
  ```go
  func WithSubgraphScope(ctx context.Context, nodeID string) context.Context // appends
  func SubgraphScope(ctx context.Context) []string
  // handlers:
  // GateInfo gains: ScopePath []string; Prompt string; BranchIndex int
  func WithGateInfo(ctx context.Context, info GateInfo) context.Context
  func GateInfoFrom(ctx context.Context) (GateInfo, bool)
  ```
  `ScopePath` = `SubgraphScope(ctx)` + `ctx.branch_id` (when set). `BranchIndex` from a new pctx internal key `pipeline.InternalKeyBranchIndex = "_branch_index"` that `ParallelHandler` sets per branch.

- [ ] **Step 1: Write the failing tests**
  - `TestSubgraphScope_Nested`: two nested `WithSubgraphScope` → `["outer","inner"]`.
  - `TestHumanHandler_PutsGateInfoOnContext`: a test interviewer implementing `AskContext` reads `GateInfoFrom(ctx)` → NodeID, Mode, Label, full Prompt, ScopePath `["B1"]` inside a parallel branch whose `branch_id` is `B1`, BranchIndex 0/1 per branch.
  - `TestSubgraph_SetsScope`: a gate inside a subgraph sees `ScopePath[0] == "<subgraph node id>"`.
- [ ] **Step 2: Run** `go test ./pipeline/... -run 'SubgraphScope|GateInfoOnContext|SetsScope' -short` — Expected: FAIL.
- [ ] **Step 3: Implement.** `HumanHandler.Execute` builds the `GateInfo` once (reuse `notifyGateAware`'s fields) and passes `WithGateInfo(ctx, info)` to `dispatchHumanMode`.
- [ ] **Step 4: Run** `go test ./pipeline/... -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(handlers): gate identity and scope ride on the per-gate context`

### Task 7: `PausingInterviewer`

**Files:**
- Create: `pipeline/handlers/interviewer_pausing.go`, `pipeline/handlers/interviewer_pausing_test.go`
- Modify: `pipeline/handlers/human.go` (subgraph fail-closed; `gate_resolved` on pause)

**Interfaces:**
- Consumes: Tasks 5–6.
- Produces:
  ```go
  type PendingAnswer struct {
      NodeID      string
      ScopePath   []string
      Fingerprint string
      Nonce       string
      Label       string            // choice / labeled freeform / yes_no
      Text        string            // free text
      Interview   map[string]string // GateQuestion.ID -> answer
      Cancel      bool              // interview cancel
  }
  func NewPausingInterviewer(answer *PendingAnswer) *PausingInterviewer
  func (p *PausingInterviewer) PausesGates() bool // marker; also define interface GatePauser{ PausesGates() bool }
  // implements Interviewer, FreeformInterviewer, LabeledFreeformInterviewer, InterviewInterviewer
  // and their *Context variants; Actor() returns pipeline.ActorHuman
  ```
  Each Ask* builds a `pipeline.PausedGate` from `GateInfoFrom(ctx)` + its own arguments, computes the fingerprint and a fresh 16-byte hex nonce, and either returns the held answer (match on ScopePath, NodeID, Fingerprint; consumed once, guarded by a mutex) or `pipeline.NewPausedGateError(record)`. A held answer for the same NodeID+ScopePath with a different fingerprint is discarded and the record gets `Note: "the question changed since you answered — answer again"`. Legacy (non-context) methods use `context.Background()` and so carry no GateInfo — the handler always calls the context variants, so they only exist for interface satisfaction.

- [ ] **Step 1: Write the failing tests**
  - `TestPausing_NoAnswerPauses` (each mode: choice, labeled freeform, plain freeform, yes_no, interview): error is a `*pipeline.PauseError` with `Status == OutcomePausedGate` and one gate whose `Choices` / `Default` equal the Ask* args, `Questions` set for interview.
  - `TestPausing_AnswerAppliedOnce`: matching answer returned on first call; a second call on the same gate pauses.
  - `TestPausing_FingerprintMismatchRepauses`: changed prompt → pause with the `Note`.
  - `TestPausing_OtherScopeUnaffected`: an answer for scope `["B1"]` does not answer the same node in `["B2"]`.
  - `TestPausing_InterviewAnswersAndCancel`: `Interview` map → `InterviewResult` in question order; `Cancel` → canceled result (handler maps to `OutcomeFail`).
  - `TestHumanHandler_PauseResolvedEvent`: on pause, `gate_resolved` carries `Resolution: "paused"` and empty `Error`; `gate_opened`/`gate_resolved` share a GateID.
  - `TestHumanHandler_SubgraphGateFailsClosedInPauseMode`: GatePauser interviewer + `SubgraphScope` non-empty → `Outcome{Status: OutcomeFail}` with `FailureReason` containing "a gate inside a subgraph can't pause yet", nil error (routable), `gate_resolved` with that Error; a non-pausing interviewer inside a subgraph is unaffected.
- [ ] **Step 2: Run** `go test ./pipeline/handlers/ -run 'Pausing|PauseResolvedEvent|SubgraphGateFailsClosed' -short` — Expected: FAIL.
- [ ] **Step 3: Implement** per Interfaces. In `HumanHandler.Execute`, when `dispatchHumanMode` returns an error matching `*pipeline.PauseError` with `OutcomePausedGate`, emit `gate_resolved` with `Resolution: "paused"` and return the error unwrapped-compatible (keep the mode's `%w` wrap). Subgraph fail-closed check runs before `dispatchHumanMode`.
- [ ] **Step 4: Run** `go test ./pipeline/... -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(handlers): PausingInterviewer pauses a gate and applies a held answer once`

### Task 8: several branch gates in one parallel node

**Files:**
- Modify: `pipeline/handlers/parallel.go` (`executeBranches` collect)
- Test: `pipeline/handlers/parallel_branch_test.go`

- [ ] **Step 1: Write the failing tests**
  - `TestParallel_TwoGatePausesMerged`: branches 0 and 2 gate-pause, branch 1 succeeds → one `PauseError{Status: OutcomePausedGate}` with two gates ordered by `BranchIndex` (0, 2).
  - `TestParallel_BillingBeatsGate`: one branch billing-pauses, one gate-pauses → status `OutcomePausedBilling` (answering cannot help an empty balance) and `Gates` still carries the gate record so the follow-up pause is not lost.
- [ ] **Step 2: Run** `go test ./pipeline/handlers/ -run 'TwoGatePausesMerged|BillingBeatsGate' -short` — Expected: FAIL (first branch pause wins).
- [ ] **Step 3: Implement**: merge every branch `pauseErr` — gates concatenated and sorted by `BranchIndex`, status billing if any branch billing-paused, else gate.
- [ ] **Step 4: Run** `go test ./pipeline/... -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(parallel): every waiting branch gate is recorded, lowest branch first`

### Task 9: resume of a gate-paused run

**Files:**
- Modify: `pipeline/engine_run.go` (`loadCheckpointAndMerge`), `pipeline/engine.go` (baseline seeding)
- Test: `pipeline/engine_resume_paused_gate_test.go` (new)

- [ ] **Step 1: Write the failing tests**
  - `TestResume_PausedGateSkipsCompaction`: a graph with `fidelity` below `full`, paused at a gate whose prompt reads `${ctx.last_response}`; resume → the re-rendered prompt equals the recorded `Prompt` and a `PendingAnswer` built from the record applies on the first call (**RF1**).
  - `TestResume_PausedGateSeedsBudget`: `PriorUsage` cost 0.9 × `max_cost_cents`; the resumed segment spends 0.2 → `budget_exceeded`. Without `PausedGates` (ordinary resume) the budget starts fresh (unchanged).
  - `TestResume_PlainResumeRepausesAndTTYAnswers`: resuming with a fresh `PausingInterviewer(nil)` re-pauses at the same gate; resuming with a `QueueInterviewer` answers it and clears `PausedGates` (**RF2**, engine half).
- [ ] **Step 2: Run** `go test ./pipeline/ -run 'Resume_PausedGate|PlainResumeRepauses' -short` — Expected: FAIL.
- [ ] **Step 3: Implement**: in `loadCheckpointAndMerge` skip `compactResumeContext` when `len(cp.PausedGates) > 0`; after load, when `len(cp.PausedGates) > 0 && cp.PriorUsage != nil && e.baselineUsage == nil`, set `e.baselineUsage = cp.PriorUsage`. (`PriorUsage` is stamped on every pause by Task 5 and folds the baseline in, so it stays cumulative across repeated answers.)
- [ ] **Step 4: Run** `go test ./pipeline/... -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(pipeline): a gate-paused resume keeps its context and its budget`

### Task 10: library — `GateMode`, `Answer`, `WaitingRuns`, run lock

**Files:**
- Create: `tracker_answer.go`, `tracker_answer_test.go`, `pipeline/run_lock.go` (+ `run_lock_unix.go`, `run_lock_windows.go`, test)
- Modify: `tracker.go` (`Config.GateMode`, `Result.PausedGate`), `tracker_interviewer.go`, `tracker_audit.go` (`RunSummary.PausedGate`), `pipeline/audit_path.go`

**Interfaces:**
- Produces:
  ```go
  // tracker
  Config.GateMode string // "", "pause", "console"
  type PausedGateInfo struct{ Node string; Scope []string; Label string; Mode string }
  Result.PausedGate *PausedGateInfo; RunSummary.PausedGate *PausedGateInfo
  type AnswerInput struct {
      RunID, Node, Label, Text string
      Default, Cancel bool
      InterviewAnswers map[string]string
  }
  func ValidateAnswer(g pipeline.PausedGate, in AnswerInput) (handlers.PendingAnswer, error)
  func PendingGates(artifactDir, runID string) (runDir string, gates []pipeline.PausedGate, err error) // authoritative (secure checkpoint)
  func Answer(ctx context.Context, source string, cfg Config, in AnswerInput) (*Result, error)
  type WaitingRun struct{ RunID string; Gate pipeline.PausedGate; Since time.Time }
  func WaitingRuns(artifactDir string) ([]WaitingRun, error)
  // pipeline
  func SecureRunLockPath(runID string) (string, error)
  func AcquireRunLock(runID string) (release func(), err error) // ErrRunLocked when held by a live PID
  var ErrRunLocked = errors.New("run is in use by another tracker process")
  ```
  `resolveInterviewer` order: `Interviewer > AutoApprove > WebhookGate > Autopilot > GateMode`; `"pause"` → `NewPausingInterviewer(nil)`; `"console"` → `NewConsoleInterviewer()`; other values → error. `GateMode: "pause"` with an empty `ArtifactDir` → construction error `GateMode "pause" requires Config.ArtifactDir`. `Answer` resolves the run with `ResolveCheckpointIn(<ArtifactDir or default>, RunID)`, loads the secure checkpoint, picks the gate (`Node` required when >1; matches `NodeID` or `strings.Join(ScopePath,"/")+"/"+NodeID`), validates, acquires the lock, re-reads the checkpoint and refuses when the chosen record's `Nonce` no longer equals the validated one ("the run changed while you were answering — look again"), then runs with `ResumeRunID` set and `Interviewer = NewPausingInterviewer(&pa)`. Every `Config.ResumeRunID` run also takes the lock (released by `Engine.Close`).

  `ValidateAnswer` table (spec §3), matching after `strings.TrimSpace` + case-fold, returning the canonical recorded spelling:
  - choice: one of `Choices`; `Default` → recorded default or error `this gate has no default`.
  - freeform with `Choices` (labels): `Label` must be one of them (a typo is an error, not silent free text); free text goes in `Text` (the handler's `matchFreeformLabel` still routes a DIP150 choice key typed as text); `Default` as choice.
  - freeform without labels: `Text` required; `Default` → error.
  - yes_no: `yes`/`no` → `Yes`/`No`; `Default` → error.
  - interview: every question ID answered (option where the question has options, any text otherwise) or `Cancel`; zero questions → treated as freeform (`Text`).
  - Errors list the valid answers.

- [ ] **Step 1: Write the failing tests**
  - `TestValidateAnswer_Table` covering every row above incl. `"  Approve "` → `approve` (**RF3**), unknown label error text lists the choices, missing interview question error names its ID.
  - `TestResolveInterviewer_GateModePrecedence`: AutoApprove + GateMode pause → auto-approve; GateMode `"bogus"` → error; pause without ArtifactDir → error.
  - `TestAnswer_RoundTrip`: a graph `Start → Gate(choice: approve/reject) → Done`, run with `GateMode: "pause"` → `Status == paused_gate`, `Result.PausedGate.Node == "Gate"`; `Answer(..., Label: "approve")` → success, the `approve` edge taken, `gate_resolved` actor human.
  - `TestAnswer_RefusesNonceChanged`: the checkpoint's record is replaced (new nonce) between validation and lock → refused.
  - `TestAnswer_RefusesNotWaiting` (completed run → error naming status `success`), `TestAnswer_NodeRequiredWithTwoGates`, `TestAnswer_PlantedFileIgnored`: writing `<runDir>/answer.json` and a rewritten `gate.json` changes nothing — `PendingGates` and validation use the checkpoint.
  - `TestWaitingRuns_ListsOnlyPaused`.
  - `TestRunLock_ConcurrentRefusedStaleReplaced`: second acquire → `ErrRunLocked`; a lock file holding a dead PID is replaced; release removes it; a canceled `Answer` (context canceled mid-run) leaves no lock (**RF4**).
- [ ] **Step 2: Run** `go test . ./pipeline/ -run 'ValidateAnswer|GateModePrecedence|Answer_|WaitingRuns|RunLock' -short` — Expected: FAIL.
- [ ] **Step 3: Implement** per Interfaces; `Result.PausedGate` / `RunSummary.PausedGate` from the first record.
- [ ] **Step 4: Run** `go test ./... -short`, `make complexity` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(tracker): GateMode pause, Answer, WaitingRuns and a run lock`; CHANGELOG (Added: library seam). Update `docs/architecture/embedding.md` (§ library seam), `docs/architecture/handlers/human.md`, `docs/architecture/transport-boundary.md`, `docs/architecture/engine.md` (`paused_gate`, `Checkpoint.PausedGates`, last-terminal-wins). Open PR 2.

---

## PR 3 — CLI `--gates`, exit 4, `tracker answer`, docs (branch `feat/tracker-answer`, after PR 2)

### Task 11: `--gates` and automatic pause mode

**Files:**
- Modify: `cmd/tracker/flags.go`, `cmd/tracker/run_config.go`, `cmd/tracker/commands.go` (`selectAndRunMode`)
- Test: `cmd/tracker/run_config_test.go`, `cmd/tracker/flags_test.go`

**Interfaces:**
- Produces: `runConfig.gates string` (`auto` default | `console` | `pause`); `func gateModeFor(gates string, stdinTTY, jsonOut bool) string` → `"pause"` / `"console"` / `"tui"`; `runOptions.pendingAnswer *handlers.PendingAnswer`.

- [ ] **Step 1: Write the failing tests**
  - `TestGateModeFor` table: (auto, tty, !json) → tui; (auto, !tty) → pause; (auto, tty, json) → pause; (console, !tty) → console; (pause, tty) → pause.
  - `TestFlags_GatesConflicts`: `--gates pause --auto-approve`, `--gates console --webhook-url x`, `--gates pause --autopilot mid` → usage errors; `--gates bogus` → error.
  - `TestApplyInterviewer_PauseAndPendingAnswer`: pause → `*handlers.PausingInterviewer`; `pendingAnswer` set → a `PausingInterviewer` holding it.
  - `TestSelectAndRunMode_PauseImpliesNoTUI`.
- [ ] **Step 2: Run** `go test ./cmd/tracker/ -run 'GateModeFor|GatesConflicts|PauseAndPendingAnswer|PauseImpliesNoTUI' -short` — Expected: FAIL.
- [ ] **Step 3: Implement**: `interactiveInterviewer` takes the resolved mode; conflicts join the existing mutual-exclusion checks (`flags.go:244-253`). Flag help: `--gates auto|console|pause — how human gates are answered without automation (auto: TUI at a terminal, pause otherwise)`.
- [ ] **Step 4: Run** `go test ./cmd/tracker/ -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(cli): --gates, and pause at gates when stdin is not a terminal`

### Task 12: paused summary, exit 4, invocation record

**Files:**
- Create: `cmd/tracker/invocation.go`, `cmd/tracker/invocation_test.go`, `pipeline/graph_digest.go` (+ test)
- Modify: `cmd/tracker/run.go` (`interpretRunResult`, `finishRun`), `cmd/tracker/main.go` (`exitWithError`), `cmd/tracker/summary.go` (`printResumeHint`, paused-gate block), `pipeline/audit_path.go`

**Interfaces:**
- Produces:
  ```go
  var errPausedAtGate = errors.New("paused at a human gate") // cmd/tracker; exitWithError → os.Exit(4)
  type invocationRecord struct {
      Version int      `json:"version"` // 1
      Cwd     string   `json:"cwd"`
      Args    []string `json:"args"`    // the original run argv after the program name, minus -r/--resume
      Digest  string   `json:"digest"`
  }
  func writeInvocation(runID string, rec invocationRecord) error // pipeline.SecureInvocationPath, 0600, O_NOFOLLOW
  func readInvocation(runID string) (invocationRecord, error)
  func answerCommand(runID string, opts *runOptions) string // "tracker answer <id>" + -w / --artifact-dir when given (built like resumeHintTarget)
  // pipeline
  func GraphDigest(g *Graph, subgraphs map[string]*Graph) string // sha256 over sorted graph attrs, nodes (id, attrs), edges, recursively for subgraphs
  func SecureInvocationPath(runID string) (string, error)
  ```
  `runOptions` gains `argv []string`, `cwd string` (filled in `main` from `os.Args[1:]` / `os.Getwd()`), `graphDigest string` (from `loadAndPreflightPipeline`).

- [ ] **Step 1: Write the failing tests**
  - `TestInterpretRunResult_PausedGate`: status `paused_gate` with a non-nil runErr → `errPausedAtGate` (checked before runErr); `paused_billing` unchanged (exit 1 path).
  - `TestExitCode_PausedGateIs4` (via a helper `exitCodeFor(err) int` extracted from `exitWithError`).
  - `TestSummary_PausedGate`: output contains `⏸ Paused at gate ReviewDiff`, the options line, the full prompt, and the exact `tracker answer` command as the last line; no `tracker -r` hint, no failure banner; with `-w /x` the command carries `-w /x`.
  - `TestGraphDigest_Sensitive`: changing a node prompt attr / an edge / a subgraph node → different digest; map iteration order irrelevant.
  - `TestInvocation_RoundTrip`: written 0600 in the secure dir; `Args` lacks `-r <id>`.
- [ ] **Step 2: Run** `go test ./cmd/tracker/ ./pipeline/ -run 'PausedGate|PausedGateIs4|GraphDigest|Invocation_' -short` — Expected: FAIL.
- [ ] **Step 3: Implement**; `finishRun` writes the invocation record when the status is `paused_gate` (the first pause and every re-pause, args from `opts.argv`).
- [ ] **Step 4: Run** `go test ./cmd/tracker/ -short` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(cli): exit 4 and an answer command when a run pauses at a gate`

### Task 13: `tracker answer`

**Files:**
- Create: `cmd/tracker/answer.go`, `cmd/tracker/answer_test.go`, `cmd/tracker/answer_e2e_test.go`
- Modify: `cmd/tracker/main.go` (`modeAnswer commandMode = "answer"`, dispatch), `cmd/tracker/flags.go` (answer flags), `site/content/cli.html`

**Interfaces:**
- Consumes: Tasks 10–12.
- Produces: `func runAnswer(cfg runConfig, deps commandDeps) error`. Flags: positional `<run-id> [<label>]`; `--text`, `--default`, `--answers-file`, `--cancel`, `--node`, `-w`, `--artifact-dir`, `--json`, `--verbose`; `--` ends flags so a label/text starting with `-` is positional text.

  Flow: no run ID → print `WaitingRuns` table (run ID, node, label, age) or "no runs are waiting for an answer". Run ID only → print each pending gate (from `PendingGates`, never `gate.json`) with its answer command. With an answer → `readInvocation`; `os.Chdir(rec.Cwd)`; re-parse `rec.Args` with the run flag parser; append `--resume <id>`, force `noTUI`, apply `--json`/`--verbose` overrides; load the pipeline and refuse when `GraphDigest` ≠ `rec.Digest` ("the workflow changed since the run paused; run `tracker -r <id> …` to resume it with the new workflow"); `ValidateAnswer`; set `opts.pendingAnswer`; `run(opts)`. `--answers-file` schema `{"answers": {"<question id>": "…"}}`, unknown keys → error.

- [ ] **Step 1: Write the failing tests**
  - `TestAnswer_ListAndShow`, `TestAnswer_RefusesDigestMismatch`, `TestAnswer_DashText` (`tracker answer <id> -- "-1 is fine"`), `TestAnswer_AnswersFileSchema` (missing / unknown key errors), `TestAnswer_FlagsConflict` (`--default` with a label, `--text` with `--cancel` → usage error).
  - `TestAnswerE2E_Quickfix` (fake `claude` from the first-win e2e): `tracker quickfix --param change=… < /dev/null` → exit 4 and `gate.json`; `tracker answer <id> --text "use a shorter title"` → re-pauses at ReviewDiff after the revision (exit 4); `tracker answer <id> approve` → exit 0, commit on `tracker/quickfix/<id>`, user checkout untouched; `--param change` survived both resumes (the revision prompt contains it). A second run answered `reject` ends via AbortRun; a third answered `--default` takes `reject`.
  - `TestAnswerE2E_JSONStream`: `--json` run → exactly one terminal event, `gate_paused`, whose message contains the answer command; no `stage_failed` / `pipeline_failed` lines (**RF5**).
  - `TestAnswerE2E_TTYResumeAfterPause` (**RF2**, CLI half): after a pause, `tracker -r <id>` with a queued console interviewer answers; `tracker answer <id> approve` then refuses "not waiting (status success)".
- [ ] **Step 2: Run** `go test ./cmd/tracker/ -run 'Answer' -short` — Expected: FAIL.
- [ ] **Step 3: Implement**; add the `tracker answer` block to `site/content/cli.html` (usage, flags, exit 4, examples from the spec's "What a user sees").
- [ ] **Step 4: Run** `go test ./cmd/tracker/ -short && make docs-check` — Expected: PASS, "all 16 CLI commands documented".
- [ ] **Step 5: Commit** `feat(cli): tracker answer lists, shows and answers paused gates`

### Task 14: docs, website, agent files

**Files:**
- Modify: `README.md` (agent / CI section: pausing + `tracker answer`), `CHANGELOG.md` (Added: `tracker answer`, `--gates`, exit 4, `paused_gate`; **Changed:** "without a terminal, human gates now pause instead of reading stdin — pass `--gates console` for the old behavior"), `site/content/glossary.html` (paused gate), `site/content/architecture.html` (pause path), `site/content/transport.html`, `site/static/AGENTS.md`, `site/static/llms.txt`, `docs/architecture/handlers/human.md`, `CLAUDE.md` (Human gate UX: pause mode + `tracker answer`; CLI UX commands entry).
- [ ] **Step 1:** Write the docs; every claim checked against the code (operator copy: no "tamper-proof" / "can't be forged").
- [ ] **Step 2: Run** `make docs-check && make doctor && go test ./... -short && go test ./cmd/tracker-conformance -run TestGoldenTraces` — Expected: PASS; HTML tag balance check on edited site pages passes.
- [ ] **Step 3: Commit** `docs: pause at a human gate and tracker answer`; open PR 3 (`Closes` the 2a issue).

After PR 3 merges: release v0.82.0 per CLAUDE.md "Before releasing" (CHANGELOG cut, website changelog, ROADMAP: Agent-driven Tracker 2a shipped → 2b next, golden traces, tag the merge commit, verify `gh release view v0.82.0` assets and the Homebrew formula).
