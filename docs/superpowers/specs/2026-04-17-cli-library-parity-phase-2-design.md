# CLI ↔ Library Parity — Phase 2 + NDJSON Writer

**Date:** 2026-04-17
**Issue:** #76 (Phase 1 already shipped; this closes the Phase 1 NDJSON gap and the full Phase 2 set)

## Goal

Lift the reusable pieces of four CLI-private commands — `diagnose`, `audit`, `doctor`, `simulate` — into the top-level `tracker` package as structured, JSON-serializable reports. Also promote the private NDJSON event writer (`cmd/tracker/json_stream.go`) into a public `tracker.NewNDJSONWriter`.

After this PR, every library consumer (the factory worker, a Slack bot, custom orchestrators) can reuse what the CLI does without shelling to a binary and parsing printed output.

## Non-goals

- Changing any CLI output (`--json` wire format or human-readable). Byte-for-byte compatible.
- Phase 3 (`tracker.DescribeNodes`). Tracked in #76 but out of scope here — handled as a follow-up.
- `tracker setup`, `tracker update`, `tracker version`. Inherently CLI-binary concerns.
- Refactoring `pipeline.Engine` internals (#19).

## Current state

Phase 1 of #76 is mostly complete already:

- ✅ `tracker.Workflows()` / `LookupWorkflow()` / `OpenWorkflow()` in `tracker_workflows.go`
- ✅ `tracker.ResolveSource()` / `ResolveCheckpoint()` in `tracker_resolve.go`
- ✅ `Config.ResumeRunID` in `tracker.go` with auto-resolution
- ❌ **Public NDJSON event writer** — still private in `cmd/tracker/json_stream.go`

Phase 2 is fully open: no library equivalents for `Diagnose`, `Audit`, `Doctor`, or `Simulate`.

## Approach

Chosen over two alternatives: **split data / presentation cleanly**.

Report-building logic moves to the `tracker` package and returns pure structured values — no lipgloss, no `fmt.Printf`, no pre-formatted lines. All printing stays in `cmd/tracker/` as `print<X>Report` functions on the public report types.

The whole point of the refactor is that a library consumer can't reuse CLI output because it's `fmt.Printf`. Lifting the structures but leaving formatted strings mixed in only partly fixes it. The split is cheap now (both live in the same package today) and expensive later.

## Package layout

```
tracker/                             # promoted library APIs
  tracker_events.go                  # NDJSON writer + public Event type
  tracker_events_test.go
  tracker_activity.go                # shared activity.jsonl / status.json / run-dir helpers
  tracker_activity_test.go
  tracker_diagnose.go                # Diagnose(runDir), DiagnoseMostRecent(workDir)
  tracker_diagnose_test.go
  tracker_audit.go                   # Audit(runDir), ListRuns(workDir)
  tracker_audit_test.go
  tracker_doctor.go                  # Doctor(cfg)
  tracker_doctor_test.go
  tracker_simulate.go                # Simulate(source)
  tracker_simulate_test.go
  testdata/
    runs/
      ok/                            # successful run fixture
      failed/                        # one failed tool node, one retry
      budget_halted/                 # tripped EventBudgetExceeded

cmd/tracker/                         # CLI printers over library output
  diagnose.go                        # ~100 lines of print* functions + thin runDiagnose
  audit.go                           # ditto
  doctor.go                          # ditto, retains gitignore-fixup/workdir-create writes
  simulate.go                        # ditto
  json_stream.go                     # DELETED — CLI uses tracker.NewNDJSONWriter
```

Single `tracker` package. No sub-packages. Library consumers do one import. File names mirror CLI counterparts so reviewers can diff side-by-side.

## API surface

### NDJSON writer (closes Phase 1)

```go
// NDJSONEvent is the wire format for --json mode. Field tags are stable.
type NDJSONEvent struct {
    Timestamp string `json:"ts"`
    Source    string `json:"source"`     // "pipeline" | "llm" | "agent"
    Type      string `json:"type"`
    RunID     string `json:"run_id,omitempty"`
    NodeID    string `json:"node_id,omitempty"`
    Message   string `json:"message,omitempty"`
    Error     string `json:"error,omitempty"`
    Provider  string `json:"provider,omitempty"`
    Model     string `json:"model,omitempty"`
    ToolName  string `json:"tool_name,omitempty"`
    Content   string `json:"content,omitempty"`
}

type NDJSONWriter struct { /* mu, w */ }

func NewNDJSONWriter(w io.Writer) *NDJSONWriter

func (s *NDJSONWriter) PipelineHandler() pipeline.PipelineEventHandler
func (s *NDJSONWriter) AgentHandler()    agent.EventHandler
func (s *NDJSONWriter) TraceObserver()   llm.TraceObserver

// Write is exported so consumers can emit their own synthetic events onto
// the same stream (matches the factory worker's existing need).
func (s *NDJSONWriter) Write(evt NDJSONEvent)
```

### Diagnose

```go
type DiagnoseReport struct {
    RunID          string         `json:"run_id"`
    CompletedNodes int            `json:"completed_nodes"`
    BudgetHalt     *BudgetHalt    `json:"budget_halt,omitempty"`
    Failures       []NodeFailure  `json:"failures"`
    Suggestions    []Suggestion   `json:"suggestions"`
}

type NodeFailure struct {
    NodeID           string        `json:"node_id"`
    Outcome          string        `json:"outcome"`
    Handler          string        `json:"handler,omitempty"`
    Duration         time.Duration `json:"duration_ns,omitempty"`
    RetryCount       int           `json:"retry_count,omitempty"`
    IdenticalRetries bool          `json:"identical_retries,omitempty"`
    Stdout           string        `json:"stdout,omitempty"`
    Stderr           string        `json:"stderr,omitempty"`
    Errors           []string      `json:"errors,omitempty"`
}

type BudgetHalt struct {
    TotalTokens   int     `json:"total_tokens"`
    TotalCostUSD  float64 `json:"total_cost_usd"`
    WallElapsedMs int64   `json:"wall_elapsed_ms"`
    Message       string  `json:"message"`
}

type Suggestion struct {
    NodeID  string `json:"node_id,omitempty"`
    Kind    string `json:"kind"`    // "retry_pattern" | "escalate_limit" | "no_output"
                                    // | "shell_command" | "go_test" | "suspicious_timing" | "budget"
    Message string `json:"message"` // human-readable, same string the CLI prints today
}

func Diagnose(runDir string) (*DiagnoseReport, error)
func DiagnoseMostRecent(workDir string) (*DiagnoseReport, error)
```

`Suggestion.Kind` is typed so programmatic consumers can filter (e.g. "show me only escalation-limit hits across all runs"). `Message` is the existing prose.

### Audit

```go
type AuditReport struct {
    RunID               string          `json:"run_id"`
    Status              string          `json:"status"`          // "success" | "fail"
    TotalDuration       time.Duration   `json:"total_duration_ns"`
    Timeline            []TimelineEntry `json:"timeline"`
    Retries             []RetryRecord   `json:"retries,omitempty"`
    Errors              []ActivityError `json:"errors,omitempty"`
    Recommendations     []string        `json:"recommendations,omitempty"`
    // Header fields used by the CLI printer.
    CompletedNodes      int             `json:"completed_nodes"`
    RestartCount        int             `json:"restart_count"`
    CheckpointTimestamp time.Time       `json:"checkpoint_timestamp"`
}

type TimelineEntry struct {
    Timestamp time.Time     `json:"ts"`
    Type      string        `json:"type"`
    NodeID    string        `json:"node_id,omitempty"`
    Message   string        `json:"message,omitempty"`
    Duration  time.Duration `json:"duration_ns,omitempty"`
}

type RetryRecord   struct { NodeID string; Attempts int }
type ActivityError struct { Timestamp time.Time; NodeID string; Message string }
type RunSummary    struct {
    RunID     string
    Status    string        // "success" | "fail"
    Nodes     int
    Retries   int
    Restarts  int
    Timestamp time.Time
    Duration  time.Duration
    FailedAt  string        // node ID where the run failed, if any
}

func Audit(runDir string) (*AuditReport, error)
func ListRuns(workDir string) ([]RunSummary, error)
```

### Doctor

```go
type DoctorConfig struct {
    WorkDir        string
    Backend        string   // gates claude-code binary check
    ProbeProviders bool     // default false — makes real API calls when true
    PipelineFile   string   // optional: also validate this .dip
}

type DoctorReport struct {
    Checks   []CheckResult `json:"checks"`
    OK       bool          `json:"ok"`
    Warnings int           `json:"warnings"`
    Errors   int           `json:"errors"`
}

type CheckResult struct {
    Name    string `json:"name"`
    Status  string `json:"status"`   // "ok" | "warn" | "error" | "skip"
    Message string `json:"message,omitempty"`
    Hint    string `json:"hint,omitempty"`
}

func Doctor(cfg DoctorConfig) (*DoctorReport, error)
```

### Simulate

```go
type SimulateReport struct {
    Format        string      `json:"format"`   // "dip" | "dot"
    Nodes         []SimNode   `json:"nodes"`
    Edges         []SimEdge   `json:"edges"`
    ExecutionPlan []PlanStep  `json:"execution_plan"`
    Unreachable   []string    `json:"unreachable,omitempty"`
}

type SimNode  struct { ID, Handler, Label string }
type SimEdge  struct { From, To, Condition, Label string }
type PlanStep struct { Step int; NodeID string; Edges []SimEdge }

func Simulate(source string) (*SimulateReport, error)
```

## Doctor's opt-in probe (the one side-effect edge case)

Every library call in this PR is read-only **except** Doctor. One current check makes 1-token requests to every configured provider to verify keys actually authenticate — a real network call at real cost.

- `DoctorConfig.ProbeProviders` defaults to **false** when called from the library.
- When `ProbeProviders == false`, the provider check returns `Status: "ok"` for a well-formed env key and `Status: "skip"` with a hint otherwise.
- CLI (`cmd/tracker/doctor.go`) sets `ProbeProviders: true` to preserve today's UX — `tracker doctor` still probes.

Doctor also has two *write* side effects today: the gitignore auto-add prompt and the workdir `.tracker/` auto-create. Both stay **CLI-only**. `cmd/tracker/doctor.go` keeps the prompting and writing and calls `tracker.Doctor()` only for the pure checks. The library report flags the gitignore gap as a `warn` with a hint — it does not mutate the workdir.

## CLI migration

Every CLI `run<X>` function collapses to three lines:

```go
// cmd/tracker/diagnose.go — after
func runDiagnose(workdir, runID string) error {
    runDir, err := tracker.ResolveRunDir(workdir, runID)
    if err != nil { return err }

    report, err := tracker.Diagnose(runDir)
    if err != nil { return err }

    printDiagnoseReport(report)
    return nil
}

func printDiagnoseReport(r *tracker.DiagnoseReport) { /* current print* funcs, unchanged */ }
```

Printing is all lipgloss. Rendering of `r.Failures`, `r.BudgetHalt`, `r.Suggestions` is the existing code, moved inside `print<X>Report` wrappers. Style constants (`bannerStyle`, `mutedStyle`, `colorHot`, etc.) stay in `cmd/tracker/branding.go`.

## Backward compatibility

- **CLI stdout**: byte-for-byte identical. `--json` wire format is unchanged (the event struct moves, the field tags stay).
- **Library API**: net additive. No existing `tracker.*` export changes signature.
- **Versioning**: pre-1.0 (`go.mod` is `v0.x`). Report shapes are additive-stable; new optional fields may be added but existing ones won't change without a MINOR bump. CHANGELOG documents this.

## Shared helpers getting promoted alongside

These live in `cmd/tracker/audit.go` today and are used by multiple commands:

- `resolveRunDir(workdir, runID)` → `tracker.ResolveRunDir`
- `findMostRecentRunID(runsDir)` → `tracker.MostRecentRunID`
- `loadActivityLog(runDir)` / `parseActivityLine` → private helpers in `tracker_activity.go`

## Testing

**Library unit tests** (per report type, table-driven over fixtures in `tracker/testdata/runs/`):

- `tracker_diagnose_test.go` — clean run, single-node failure, multi-retry identical, multi-retry varying, budget halt, missing activity.jsonl, malformed activity lines. Asserts handler/duration/retry count and that suggestions of expected `Kind` fire.
- `tracker_audit_test.go` — timeline ordering, status derivation (completed / in_progress / failed), recommendations.
- `tracker_doctor_test.go` — `ProbeProviders: false` against a controlled env; asserts check kinds/statuses. Probe path gets a separate test with a fake provider, skipped in `-short`.
- `tracker_simulate_test.go` — given a `.dip` source string, asserts node order, edge annotations, unreachable detection. Uses the three example pipelines as golden inputs.
- `tracker_events_test.go` — round-trip `NDJSONEvent` through JSON, assert stable tags; assert `Write` is thread-safe (race test).

**Shared fixtures** under `tracker/testdata/runs/`:
- `ok/` — successful run (checkpoint + activity.jsonl).
- `failed/` — one failed tool node, one retry.
- `budget_halted/` — tripped `EventBudgetExceeded`.

**CLI tests** stay where they are but assertion shifts: "does stdout contain X" becomes "calling `runDiagnose` on fixture succeeds and prints non-empty." Heavy assertion moves to the library.

**Pre-commit gates** (per CLAUDE.md — never bypass):
- `go build ./...` must pass
- `go test ./... -short` must pass for all 14 packages
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` must be A grade

## Build sequence

Order matters for review — each phase should be independently reviewable:

1. **Shared activity helpers** (`tracker_activity.go` + tests) — promotes `ResolveRunDir`, `MostRecentRunID`, activity parsing. No behavior change. Prerequisite for the rest.
2. **NDJSON writer** — `tracker_events.go` + tests; delete `cmd/tracker/json_stream.go`; wire CLI to `tracker.NewNDJSONWriter`. Closes Phase 1.
3. **Simulate** — pure graph introspection, no run-dir state. Smallest of the four.
4. **Diagnose** — builds on shared activity helpers.
5. **Audit** — builds on shared activity helpers.
6. **Doctor** — has the probe opt-in concern; does last so the earlier reviews set the pattern.
7. **CHANGELOG** + docs update in the final commit.

## Risks

- **Doctor's gitignore-fixup write path is CLI-only**. If a library consumer wants parity, they'd have to reimplement the prompt. Acceptable for this PR — can add `tracker.FixGitignore(workDir)` later if asked.
- **Public report shapes become semver-relevant.** Pre-1.0 gives us flexibility; CHANGELOG documents the additive-stable policy.
- **activity.jsonl timestamp formats vary** (RFC3339 with and without nanoseconds, some legacy runs). Centralizing the parser in the library surfaces any edge cases uniformly — but also means one bug hits all callers. Mitigate with table-driven tests over real fixture data.
- **Simulate's BFS ordering must be deterministic** across consumers. Already is in today's code via sorted iteration; tests lock it in.

## Out of scope / follow-ups

- Phase 3: `tracker.DescribeNodes(source)` — graph introspection for pipeline authors. ~70% overlap with Simulate's graph walk; handle as a follow-up PR.
- `tracker.FixGitignore` / workdir-create helpers if library consumers ask for write parity with CLI doctor.
- A `tracker.RunLister` streaming API for paginating very large `.tracker/runs/` directories.
