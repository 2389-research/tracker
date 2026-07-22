# Embedding Tracker as a Library

This is the **supported embedding surface** for downstream products (e.g.
`tracker-runner`) that run Tracker as a library instead of shelling out to the
`tracker` CLI. Everything here is a stable seam — build on it rather than
hand-composing `pipeline.NewEngine`, which is exactly how stale runners
re-accrued missing budget/cost/gateway/backend wiring.

All of it lives in the top-level `tracker` package (`tracker.go`,
`tracker_*.go`). Import only that package — LLM clients, registries, and
environments are auto-wired from `Config`.

## 1. Engine construction

| Entry point | Use |
|---|---|
| `tracker.Run(ctx, source, cfg) (*Result, error)` | One-call convenience: parse → wire → execute → `Close()`. `source` is pipeline **content** (a `.dip` string, or a `strict digraph` DOT string — DOT is deprecated), not a path. |
| `tracker.NewEngineWithContext(ctx, source, cfg) (*Engine, error)` | Construct without running (call `engine.Run(ctx)` yourself); `defer engine.Close()`. |
| `tracker.NewEngineFromGraph(ctx, graph, cfg) (*Engine, error)` | Construct from an already-parsed `*pipeline.Graph`, skipping the parse step. **This exists** (`tracker.go`) — use it when you hold a graph in memory. |
| `tracker.Config` | The single wiring struct. Zero value is usable; every field is optional. |

Key `Config` fields for embedders: `WorkingDir`, `ArtifactDir`, `CheckpointDir`,
`ResumeRunID`, `Provider`/`Model`, `Backend`, `LLMClient` (inject a custom
`agent.Completer`), `EventHandler`/`AgentEvents` (live streams), `TokenTracker`,
`Budget`, `GatewayURL`/`GatewayKind`, `Interviewer` (the human-gate transport
seam), `Subgraphs`, `Git`, `GitArtifacts`, `SteeringChan`.

## 2. Git artifacts & bundle export (branch-per-run / PR delivery)

- **`Config.GitArtifacts bool`** — makes the artifact run dir a git repo and
  commits after **every terminal node outcome**. Requires `git` in PATH and is a
  no-op unless `Config.ArtifactDir` is set. This is the seam for branch-per-run
  and PR-as-deliverable. *(Before this field existed, `WithGitArtifacts` was an
  engine-only option unreachable from the library `Config` — embedders could not
  enable git artifacts at all.)*
- **`Result.ArtifactRunDir`** — the canonical run-dir locator
  (`<artifactDir>/<runID>`). Pass it to `ExportBundle`.
- **`tracker.ExportBundle(runDir, outPath) error`** — wraps
  `git bundle create --all` for portable, single-file run history. The canonical
  hand-off pattern for a remote worker → `git clone <bundle>` on the user's
  machine. Populates nothing on `Result` unless you call it; set
  `Result.BundlePath` yourself if you track it.

## 3. Cost & budget primitives

- **Per-run usage:** `Result.Trace.AggregateUsage() *pipeline.UsageSummary`
  aggregates all session stats + child-run rollups. `UsageSummary` carries
  `TotalInputTokens`/`TotalOutputTokens`/`TotalTokens`/`TotalCostUSD`,
  cache/reasoning totals, `SessionCount`, and
  `ProviderTotals map[string]pipeline.ProviderUsage` (per-provider breakdown).
  Per-node stats hang off `Trace.Entries[].Stats` (`pipeline.SessionStats`).
- **Dollar cost:** `Result.Cost *CostReport` (`TotalUSD`,
  `ByProvider map[string]llm.ProviderCost`, `LimitsHit`); `Result.TokensByProvider`
  for raw `llm.Usage` per provider.
- **Budget enforcement:** set `Config.Budget pipeline.BudgetLimits`
  (`MaxTotalTokens`, `MaxCostCents`, `MaxWallTime`, `StallTimeout`).
  `tracker.ResolveBudgetLimits(cfg.Budget, graph)` folds in workflow-level
  `defaults:` (Config wins). A breach yields terminal status `budget_exceeded`
  and `EventBudgetExceeded`.
- The exact field shapes of `SessionStats` / `UsageSummary` / `ProviderUsage`
  are pinned by the golden-trace fixtures (§5) — a rename or retype breaks them.

## 4. Terminal status & events

- **Status is an open enum.** `Result.Status` is one of `success`, `fail`,
  `budget_exceeded`, `validation_overridden` today; future minors may add more.
  **Never switch on the raw string** — classify with
  `pipeline.TerminalStatus(r.Status).IsSuccess()` (fail-closed).
- **Events:** `Config.EventHandler pipeline.PipelineEventHandler` receives the
  `pipeline.PipelineEvent` stream (types in `pipeline/events.go`);
  `Config.AgentEvents agent.EventHandler` receives per-session `agent.Event`s.
  `tracker.NewNDJSONWriter(io.Writer)` yields the same stream shape as
  `tracker --json`.

## 5. Verifying against version drift — golden-trace conformance fixtures

Downstream ports cross many engine versions; tests written against old shapes
pass while behavior silently drifts. The `tracker-conformance` binary ships
**golden-trace fixtures** to catch that mechanically:

```
tracker-conformance golden <fixture.dip>
```

emits a **normalized, deterministic** JSON document — event sequence,
per-node `SessionStats`, aggregate `UsageSummary`, and terminal status/class —
generated via a stub completer (no API keys, no wall-clock/run-id/duration
noise). Committed goldens live at `cmd/tracker-conformance/testdata/golden/*.golden.json`
and are versioned in lockstep with the `tracker` tag (GoReleaser ships the
binary). A downstream port pins a `tracker` version and diffs the binary's
stdout (or the committed file) against its own expectation; any event-schema,
handler-contract, or usage-shape change surfaces as a diff.

Regenerate after an intentional engine change:

```
go test ./cmd/tracker-conformance -run TestGoldenTraces -update-golden
```

**Coverage (what is and isn't pinned).** The fixtures pin the `start`/`exit`,
`tool`, `wait.human` (yes_no, auto-approved), conditional-edge routing, `codergen`,
and `parallel`/`parallel.fan_in` handler contracts; the
`SessionStats`/`UsageSummary`/`ProviderUsage` shapes (single- and multi-session);
the `EventStageRetrying` retry path; and the `success`, `fail`, and
`budget_exceeded` terminal statuses/classes (`agent_linear`, `control_flow`,
`tool_failure`, `parallel_fanin`, `budget_exceeded`, `retry_exhausted`).

Because parallel branches emit events from concurrent goroutines with no stable
cross-node order, the schema (v2) groups events **per node** (`node_events`) plus
a node-less `pipeline_events` stream, and sorts trace entries / completed nodes by
node id. Only *within-node* event order is pinned — that's the only order the
engine reproduces under parallelism. A downstream harness must normalize the same
way or parallel fixtures will flake.

**Not yet pinned:** `validation_overridden` terminal, `subgraph`,
`stack.manager_loop`, and `interview` modes — treat these as unverified-by-golden
until fixtures land.

## Related

- Transport boundary (how front-ends plug in): [`transport-boundary.md`](transport-boundary.md)
- System orientation: [`../../ARCHITECTURE.md`](../../ARCHITECTURE.md)
