# API Stability Policy

This document states the stability commitment for embedding Tracker as a Go
library. It complements [`docs/architecture/embedding.md`](architecture/embedding.md)
(the how-to for the seam) and [`docs/architecture/transport-boundary.md`](architecture/transport-boundary.md)
(how front-ends plug in). The mechanical guard behind everything here is the
exported-surface golden snapshot — see [§4](#4-the-mechanical-guard).

## 1. The supported surface

The **root `github.com/2389-research/tracker` package** is the one supported
embedding surface. Import only it — LLM clients, registries, and environments
are auto-wired from `Config`. Its stable entry points are:

- **Run / construct:** `Run`, `NewEngine`, `NewEngineWithContext`,
  `NewEngineFromGraph`, and the `Config` wiring struct.
- **Inspect:** `Diagnose` / `DiagnoseMostRecent`, `Doctor`, `Audit`,
  `ListRuns`, `Simulate` / `SimulateGraph`, `EstimateRun`, `AnalyzeTestFidelity`,
  `ClassifyFailure`.
- **Resolve:** `ResolveRunDir`, `ResolveBudgetLimits`, `ResolveProviderBaseURL`
  (+ `…Strict`), `ResolveActivityLogPath`, `ResolveCheckpoint`, `ResolveSource`,
  `ResolveGitConfig`.
- **Stream / read:** `NewNDJSONWriter` (`StreamEvent` envelope), `LoadActivityLog`
  / `ParseActivityLine` / `ScanActivityLog` (`ActivityEntry`), `SetDiagnosticLogger`.
- **Concurrency:** `NewRunManager` → `RunManager` / `ManagedRun` / `RunState`.
- **Workflows:** `Workflows`, `LookupWorkflow`, `OpenWorkflow`.

Do **not** build on `pipeline.NewEngine` directly — hand-composing the engine is
exactly how stale runners re-accrued missing budget/cost/gateway/backend wiring.

**Selected sub-package types are part of the boundary too**, referenced from the
root surface: the `handlers.Interviewer` family (human-gate transport seam),
`pipeline` event/config types (`pipeline.PipelineEvent`, `pipeline.Config`,
`pipeline.BudgetLimits`, `pipeline.UsageSummary`, …), and `llm` types
(`llm.Client`, `llm.Usage`, `llm.TokenTracker`). These are pinned indirectly —
they appear in root signatures, and the golden-trace fixtures
([`embedding.md` §5](architecture/embedding.md)) pin the cost/usage shapes.
Extending the surface snapshot ([§4](#4-the-mechanical-guard)) to enumerate these
sub-packages directly is a planned follow-up; for now the root snapshot plus the
golden traces are the guard.

## 2. Open enums — never switch exhaustively

Several string/int "enum" values are **open**: a future minor may add a new
value. Consumers must classify, not switch exhaustively, and must treat an
unrecognized value fail-closed (as "not a success/state I understand" — not as an
error to report).

- **`Result.Status`** — `success`, `fail`, `budget_exceeded`,
  `validation_overridden`, `paused_billing` today; more may be added. Classify
  with `pipeline.TerminalStatus(r.Status).IsSuccess()`. Note `paused_billing` is
  *recoverable* (resume via `Config.ResumeRunID`), and `Run` returns both the
  result and the billing error — `err != nil` alone is not "dead".
- **`RunManager` `RunState`** — `RunStarting`, `RunRunning`, `RunSucceeded`,
  `RunFailed`, `RunPaused`, `RunCanceled` today. Gate on `RunState.Terminal()`
  rather than enumerating states.
- **Audit `AuditReport.Status`** is the same open terminal-status string as
  above; classify with the *stable* 3-value `AuditReport.StatusClass`
  (`succeeded` / `failed` / `paused`) rather than switching on `Status`.
- The diagnose `SuggestionKind` and `CheckStatus` families are also open — new
  suggestions/check states land in minors, so handle an unknown value gracefully.

## 3. Deprecation policy

Tracker is **pre-1.0**. The contract until 1.0:

- The root surface is production-usable, but a **minor release may make a
  breaking change** to it when the finalization work requires one.
- **Every breaking change is called out in `CHANGELOG.md`** under a `Changed` or
  `Removed` heading in the release that ships it. Read the changelog before
  upgrading. The surface snapshot ([§4](#4-the-mechanical-guard)) guarantees no
  breaking change ships *silently* — it forces a golden update, and that diff is
  the prompt for the changelog note.
- Where practical, a removed or renamed symbol is kept as a deprecated alias for
  one minor with a `// Deprecated:` doc comment before removal — but pre-1.0 this
  is a courtesy, not a guarantee.

**Intent toward 1.0:** at v1.0 the root surface becomes covered by semantic
versioning in the usual sense — no breaking changes within a major. Benchmark
cadence and the API-stability bar are the gating questions for v1.0 (tracked in
`ROADMAP.md`, issue #462).

## 4. The mechanical guard

`api_surface_test.go` + `testdata/api_surface.golden` are a golden snapshot of
every exported identifier of the root `tracker` package — functions with
signatures, exported types with their exported fields/methods, and exported
vars/consts — enumerated deterministically from the package's non-test source via
`go/ast`. `TestAPISurface` fails with a readable diff whenever that surface
changes.

- Run it: `go test . -run APISurface -count=1`.
- Regenerate after an *intended* change: `go test . -run APISurface -update`,
  then commit the golden **and** add the `CHANGELOG.md` note.

A failing `TestAPISurface` on an unrelated change means you exported (or removed,
or re-signed) something by accident — revert it, don't `-update` past it.
