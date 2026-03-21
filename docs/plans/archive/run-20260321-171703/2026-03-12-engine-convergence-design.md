# Engine Convergence: Tracker as Canonical Pipeline Library

## Overview

Port mammoth's best engine features into tracker, then retire mammoth's internals (`attractor/`, `dot/`, `agent/`, `llm/`) in favor of tracker imports. Tracker becomes the canonical pipeline engine library; mammoth becomes a thin CLI/web consumer.

Mammoth is the legacy codebase — working but less reliable than tracker. Tracker is the active, working system. This is cherry-picking good ideas from mammoth, not a wholesale port.

## Architecture

After convergence, the dependency graph is:

```
mammoth (CLI, web server, MCP, conformance)
  └── tracker/pipeline   (engine, handlers, checkpointing, events, validation)
  └── tracker/llm        (multi-provider client, middleware, retry, tracing)
  └── tracker/agent      (session loop, tools, event system)
```

Tracker remains a standalone CLI (`tracker <pipeline.dot>`) AND a Go library importable by mammoth and other consumers.

## What We're Building (In Tracker)

### 1. Fidelity Modes

Control how much conversation history gets passed when resuming or re-entering nodes in loops. Critical for long-running pipelines that would blow context windows.

**Modes:**
- `full` — Complete conversation history (default for first execution)
- `summary:high` — Detailed summary of prior turns
- `summary:medium` — Key decisions and outcomes only
- `summary:low` — One-line summaries per node
- `compact` — Minimal context, just current task
- `truncate` — Drop oldest turns to fit window

**Where it lives:** `pipeline/fidelity.go` — fidelity mode is a graph-level or node-level attribute (`fidelity` or `default_fidelity`). The codergen handler reads it and adjusts the system prompt / conversation history accordingly.

**On resume:** Fidelity degrades one level (e.g., `full` → `summary:high`) since in-memory session state can't be serialized. This is mammoth's existing behavior and it works well.

### 2. Restart Loops

When the engine detects a loop (node re-entered via conditional edge), instead of failing, it can clear downstream completed nodes and restart from a configurable point.

**Configuration:**
- `graph.max_restarts` attribute (default: 5)
- `graph.restart_target` attribute — which node to restart from on loop detection
- Per-node retry counts reset when a restart occurs

**Where it lives:** `pipeline/engine.go` — the main execution loop gains restart detection and handling. Checkpoint tracks restart count.

### 3. Named Retry Policies

Richer than the current simple `max_retry` integer. Named strategies with different backoff and attempt counts.

**Policies:**
- `none` — No retries (fail immediately)
- `standard` — 2 retries (default)
- `aggressive` — 5 retries with shorter backoff
- `patient` — 3 retries with longer backoff (good for rate-limited providers)
- `linear` — 3 retries, no backoff increase

**Configuration:** Node attribute `retry_policy` or graph-level `default_retry_policy`. The existing `max_retry` attribute continues to work as an override.

**Where it lives:** `pipeline/retry_policy.go` — policy definitions and resolution. Engine consults the policy when a node returns `OutcomeRetry`.

### 4. Validation Improvements

Expand tracker's basic validation with mammoth's most useful rules:

- Missing fail edges on conditional nodes (with auto-fix)
- Unreferenced nodes (warning)
- Shape-to-handler mapping validation
- Edge label consistency checks
- Duplicate edge detection

**Where it lives:** `pipeline/validate.go` — expand existing `Validate()`. Add `AutoFix()` for fixable issues.

### 5. Audit Command

`tracker audit <runID>` — LLM-powered analysis of a completed run. Reads the activity log, checkpoint, and artifacts, then produces a structured report.

**Output includes:**
- Timeline of execution with duration per node
- Error analysis (what failed and why)
- Token usage breakdown
- Recommendations (retry strategy tuning, fidelity adjustments)
- Whether the pipeline achieved its stated goal

**Where it lives:** `cmd/tracker/audit.go` — new subcommand. Uses tracker's own LLM client to analyze the run artifacts.

## What We're NOT Building (v2 / Out of Scope)

- `CodergenBackend` abstraction (swappable agent implementations) — v2
- Web server / HTTP API — stays in mammoth as a consumer
- MCP server — stays in mammoth
- Mammoth's custom DOT parser — tracker keeps gographviz

## Phasing

### Phase 1: Fidelity Modes + Restart Loops
Highest value. Solves the real problems of long-running pipelines blowing context windows and getting stuck in infinite loops.

**Files touched:**
- `pipeline/fidelity.go` (new)
- `pipeline/engine.go` (restart loop detection, fidelity-aware resume)
- `pipeline/checkpoint.go` (restart count tracking)
- `pipeline/handlers/codergen.go` (fidelity-aware prompt construction)
- Tests for all of the above

### Phase 2: Named Retry Policies + Validation
Polish engine behavior. Retry policies replace the crude `max_retry` integer.

**Files touched:**
- `pipeline/retry_policy.go` (new)
- `pipeline/validate.go` (expanded rules, auto-fix)
- `pipeline/engine.go` (policy-aware retry logic)
- Tests for all of the above

### Phase 3: Audit Command
Independent feature. Reads run artifacts and produces LLM-powered analysis.

**Files touched:**
- `cmd/tracker/audit.go` (new)
- `cmd/tracker/main.go` (wire audit subcommand)
- Tests

### Phase 4: Mammoth Migration
Flip mammoth to import tracker. Drop mammoth's `attractor/`, `dot/`, `agent/`, `llm/` packages. Update mammoth's CLI, web server, and MCP to use tracker's types.

**This phase lives in mammoth-dev, not tracker.**

### Post-Migration: Verification
- Rerun mammoth conformance tests against tracker engine
- Rerun AttractorBench to verify benchmark scores hold
- Current best: 0.795 (v5), target: maintain or improve

## Tech Stack

- Language: Go 1.25
- DOT parser: gographviz (existing)
- LLM providers: Anthropic, OpenAI, Gemini (existing)
- Testing: `go test ./...` with table-driven tests
- Quality: existing pre-commit hooks

## Testing Strategy

- Unit tests for each new component (fidelity, restart, retry policy, validation)
- Integration tests: run sample pipelines through the engine with fidelity/restart scenarios
- Conformance tests: mammoth's existing conformance suite adapted for tracker
- Benchmark: AttractorBench score must not regress

## Pipeline Configuration

- **Phases:** Phase 1 (fidelity + restarts) → Phase 2 (retry + validation) → Phase 3 (audit) → Phase 4 (mammoth migration)
- **Tech Stack:** Go, gographviz, existing LLM client stack
- **Testing:** go test, table-driven, conformance suite, AttractorBench
- **Quality Gates:** existing pre-commit hooks, `go test ./...` must pass
- **Human Gates:** none — headless execution per phase, review at commit
- **Retry Strategy:** standard (2 retries per node)
- **Models:** not applicable (this is engine work, not a pipeline to run)
- **Parallelism:** Phases 1-3 are sequential (each builds on prior). Phase 4 depends on all three.
