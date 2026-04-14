# Library API Parity — Design Spec

**Date:** 2026-04-13
**Issues:** #59, #62, #60
**Scope:** Expose CLI-only features through the library, add token breakdowns to Result, unify validation

---

## Fix 1: Config struct parity (#59)

**Problem:** CLI flags `--backend`, `--autopilot`, `--auto-approve` have no library equivalents. Library consumers (factory worker, Slack bot) can't select agent backends or configure human gate behavior.

**Fix:** Add fields to `Config` in `tracker.go`:

```go
type Config struct {
    // ... existing fields ...
    Backend      string                        // "native" (default), "claude-code", "acp"
    Autopilot    string                        // "" (interactive), "lax", "mid", "hard", "mentor"
    AutoApprove  bool                          // auto-approve all human gates
    Interviewer  pipeline.Interviewer          // custom gate handler (overrides autopilot/auto-approve)
}
```

Thread these through to the engine/handler registry setup in `buildEngineOpts` and `buildHandlerRegistry`. The CLI flags become thin wrappers that set these Config fields.

Also add to `Result`:

```go
type Result struct {
    // ... existing fields ...
    Trace        *pipeline.Trace               // full execution trace
}
```

The `Trace` is already on `EngineResult` but not surfaced through the library's `Result`.

## Fix 2: Token/cost breakdowns in Result (#62)

**Problem:** Library exposes aggregate `UsageSummary` but not per-provider or per-tool breakdowns. CLI computes these in `summary.go` but the data doesn't flow back to library consumers.

**Fix:** Add breakdown fields to `Result`:

```go
type Result struct {
    // ... existing fields ...
    TokensByProvider map[string]llm.Usage        // per-provider token totals
    ToolCallsByName  map[string]int              // tool call counts by name
}
```

The `llm.TokenTracker` already collects per-provider data. Expose it through the result by reading from the tracker after the run completes.

## Fix 3: Validation API (#60)

**Problem:** `tracker.go` has its own validation path (`parsePipelineSource`) that can disagree with `dippin doctor`. No library function for "validate without running."

**Fix:** Add a public validation function:

```go
// ValidateSource parses and validates a pipeline source string.
// Returns validation diagnostics without executing the pipeline.
func ValidateSource(source string, opts ...ValidateOption) (*ValidationResult, error)

type ValidationResult struct {
    Graph       *pipeline.Graph
    Errors      []string
    Warnings    []string
    LintHints   []string
}

type ValidateOption func(*validateConfig)
func WithFormat(format string) ValidateOption
```

Internally, this calls dippin-lang's parser + validator + linter as the primary validation, then layers tracker-specific checks (handler resolution, attr validation) on top. The result includes both layers clearly separated.

---

## Files Changed

| File | Changes |
|------|---------|
| `tracker.go` | Add Config fields, Result fields, `ValidateSource()`, wire backend/autopilot/interviewer through engine setup |
| `tracker_test.go` | Test new Config fields, ValidateSource |
| `pipeline/handlers/registry.go` | Accept interviewer via registry options |
| `llm/client.go` | Expose TokenTracker data for Result population |

## Non-Goals

- Moving doctor/diagnose to library (separate PR)
- Moving audit/simulate to library (separate PR)
- TUI as a library component (CLI-specific)
