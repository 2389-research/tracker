# Tracker

Pipeline orchestration engine for multi-agent LLM workflows. Define pipelines in `.dip` files (Dippin language), execute them with parallel agents, and watch progress in a TUI dashboard.

Built by [2389.ai](https://2389.ai).

## Quick Start

```bash
# Install
go install github.com/2389-research/tracker/cmd/tracker@latest

# See what's built in
tracker workflows

# Run a built-in pipeline by name — no file needed
tracker build_product

# Or copy it locally to customize
tracker init build_product
tracker build_product.dip

# Run fully autonomous with an LLM judge
tracker --autopilot mid build_product

# Use Claude Code backend for file editing + terminal (coming v0.12.0)
tracker --backend claude-code build_product

# Check your setup (API keys, dippin binary, working directory)
tracker doctor

# Configure LLM providers interactively
tracker setup

# Validate a pipeline without running it
tracker validate build_product

# Resume a stopped run
tracker -r <run-id> build_product.dip

# When something goes wrong
tracker diagnose
```

## Pipeline Examples

Four pipelines are embedded in the binary and available via `tracker workflows`:

### `ask_and_execute`
Competitive implementation: ask the user what to build, fan out to 3 agents (Claude/Codex/Gemini) in isolated git worktrees, cross-critique the implementations, select the best one, apply it, clean up the rest.

### `build_product`
Sequential milestone builder: read a SPEC.md, decompose into milestones, implement each with verification loops (opus-powered fix agent with 50 turns), cross-review the complete result, verify full spec compliance. Context-specific escalation gates let you override flaky tests or skip milestones without aborting the build.

```mermaid
graph LR
    ReadSpec --> Decompose --> ApprovePlan
    ApprovePlan -->|approve| PickNext
    PickNext -->|milestone N| Implement --> Test
    Test -->|pass| Verify --> MarkDone --> PickNext
    Test -->|fail| Fix --> Test
    Test -->|escalate| EscalateMilestone
    EscalateMilestone -->|mark done| MarkDone
    EscalateMilestone -->|retry| Implement
    PickNext -->|all done| CrossReview --> FinalBuild --> FinalSpec --> Cleanup --> Done
```

### `build_product_with_superspec`
Parallel stream execution for large structured specs: reads the spec's work streams and dependency graph, executes independent streams in parallel (with git worktree isolation), enforces quality gates between phases, cross-reviews with 3 specialized reviewers (architect/QA/product), and audits traceability.

### `deep_review`
Interview-driven codebase review: describe what you want reviewed, answer structured interview questions to scope the analysis, then three parallel agents analyze correctness, security, and design. A second interview presents findings for your context (is this intentional? known issue?), a third prioritizes remediation, and the pipeline produces an actionable remediation plan.

```mermaid
graph LR
    DescribeGoal --> Explore --> ScopeInterview
    ScopeInterview --> AnalyzeParallel
    AnalyzeParallel --> Correctness & Security & Design
    Correctness & Security & Design --> Join
    Join --> Synthesize --> FindingsInterview
    FindingsInterview --> PriorityInterview
    PriorityInterview --> RemediationPlan --> ReviewPlan
    ReviewPlan -->|approve| Finalize --> Done
    ReviewPlan -->|revise| RemediationPlan
```

## Built-in Workflows

Pipelines are embedded in the binary so `brew` and `go install` users can run them without cloning the repo:

```bash
tracker workflows              # List all built-in workflows
tracker build_product          # Run directly by name
tracker validate build_product # Validate works too
tracker simulate build_product # Simulate too
tracker init build_product     # Copy to ./build_product.dip for editing
```

Local `.dip` files always take precedence over built-ins. After `tracker init build_product`, running `tracker build_product` uses your local copy.

## Dippin Language

Pipelines are defined in `.dip` files using the [Dippin language](https://github.com/2389-research/dippin-lang):

```dip
workflow MyPipeline
  goal: "Build something great"
  start: Begin
  exit: Done

  defaults
    model: claude-sonnet-4-6
    provider: anthropic

  agent Begin
    label: Start

  human AskUser
    label: "What should we build?"
    mode: freeform

  agent Implement
    label: "Build It"
    prompt: |
      The user wants: ${ctx.human_response}
      Implement it following the project's conventions.

  agent Done
    label: Done

  edges
    Begin -> AskUser
    AskUser -> Implement
    Implement -> Done
```

### Node Types

| Type | Shape | Description |
|------|-------|-------------|
| `agent` | box | LLM agent session (codergen) |
| `human` | hexagon | Human-in-the-loop gate (choice, freeform, or hybrid) |
| `tool` | parallelogram | Shell command execution |
| `parallel` | component | Fan-out to concurrent branches |
| `fan_in` | tripleoctagon | Join parallel branches |
| `subgraph` | tab | Execute a referenced sub-pipeline |
| `manager_loop` | house | Managed iteration loop |
| `conditional` | diamond | Condition-based routing |

### Variable Interpolation

Three namespaces for `${...}` syntax in prompts:

- `${ctx.outcome}` — runtime pipeline context (outcome, last_response, human_response, tool_stdout)
- `${params.model}` — subgraph parameters passed from parent
- `${graph.goal}` — workflow-level attributes

Variables are expanded in a single pass — resolved values are never re-scanned, preventing recursive expansion.

### Edge Conditions

```dip
edges
  Check -> Pass  when ctx.outcome = success
  Check -> Fail  when ctx.outcome = fail
  Check -> Retry when ctx.outcome = retry
  Gate -> Next   when ctx.tool_stdout contains all-done
  Gate -> Loop   when ctx.tool_stdout not contains all-done
```

Supported operators: `=`, `!=`, `contains`, `not contains`, `startswith`, `not startswith`, `endswith`, `not endswith`, `in`, `not in`, `&&`, `||`, `not`.

Conditions support the `ctx.` namespace prefix (dippin convention) and `internal.*` references for engine-managed state.

### Per-Node Working Directory

For git worktree isolation in parallel implementations:

```dip
agent ImplementClaude
  working_dir: .ai/worktrees/claude
  model: claude-sonnet-4-6
  prompt: Implement the spec in this isolated worktree.
```

The `working_dir` attribute is validated against path traversal and shell metacharacters.

### Human Gates

Four gate modes:

- **Choice mode** (default): presents outgoing edge labels as a radio list. Arrow keys navigate, Enter selects.
- **Freeform mode** (`mode: freeform`): captures text input. If the response matches an edge label (case-insensitive), it routes to that edge. Otherwise it's stored as `ctx.human_response`.
- **Hybrid mode** (automatic): when a freeform gate has labeled outgoing edges, the TUI presents a radio list of labels plus an "other" option for custom feedback. Selecting a label submits it directly; selecting "other" opens a textarea for specific instructions.
- **Interview mode** (`mode: interview`): structured multi-field form driven by upstream agent output. An agent generates markdown questions with inline options; the handler parses them into individual form fields and presents a fullscreen interview form. Answers are stored as JSON and markdown summary.

Long prompts with labels (e.g., escalation gates with agent output) automatically use a fullscreen **review hybrid view**: glamour-rendered scrollable viewport on top (PgUp/PgDn to scroll), radio label selection in the middle, and an "other" freeform option at the bottom for custom retry instructions. Long prompts without labels use a **split-pane review**: scrollable viewport on top, textarea on bottom.

```dip
human ApproveSpec
  label: "Review the spec. Approve, refine, or reject."
  mode: freeform

edges
  ApproveSpec -> Build  label: "approve"
  ApproveSpec -> Revise label: "refine"  restart: true
  ApproveSpec -> Done   label: "reject"
```

#### Interview Mode

Interview gates let an agent generate structured questions that the user answers via a form:

```dip
human ScopeInterview
  label: "Help us focus the review."
  mode: interview
  questions_key: interview_questions
  answers_key: scope_answers
```

The upstream agent writes markdown questions to the `questions_key` context variable. The parser extracts:
- **Numbered/bulleted questions** ending in `?` or imperative prompts ("Describe...", "List...")
- **Inline options** from trailing parentheticals: `Auth model? (API key, OAuth, JWT)` becomes a select field
- **Yes/no patterns** detected automatically as confirm toggles

The TUI presents a fullscreen form with per-field navigation (arrow keys), pagination (PgUp/PgDn for 10+ questions), elaboration textareas (Tab), and pre-fill from previous answers on retry. Answers are stored as JSON at `answers_key` and as a markdown summary at `human_response`. If zero questions are parsed, the gate falls back to freeform. Cancellation returns `outcome=fail`.

A reusable interview loop pattern is available in `examples/subgraphs/interview-loop.dip` — embed it via `subgraph` nodes with `topic` and `focus` parameters.

Submit with **Ctrl+S**. Enter inserts newlines. Esc cancels (empty) or submits (with content). Ctrl+C cancels and unblocks the pipeline (no deadlock).

### Providers

Tracker supports three LLM providers: `anthropic`, `openai`, `gemini`. Set up with:

```bash
# Interactive setup wizard
tracker setup

# Verify your configuration
tracker doctor
```

Keys are stored in `~/.config/2389/tracker/.env`. You can also export them directly:

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export OPENAI_API_KEY=sk-...
export GEMINI_API_KEY=...
```

**Important**: Use `gemini` (not `google`) as the provider name in `.dip` files.

Non-retryable provider errors (quota exceeded, auth failure, model not found) immediately fail the pipeline with a clear message instead of silently retrying.

### Cloudflare AI Gateway

Tracker supports the **Cloudflare AI Gateway** to handle rate limiting and route requests through Cloudflare's high-capacity infrastructure. This is particularly useful if you're hitting 429 rate limit errors from direct API calls.

#### Setup

First, [create an AI Gateway](https://developers.cloudflare.com/ai-gateway/) in your Cloudflare dashboard. Note your account ID and gateway slug.

Then set the gateway URL:

```bash
export TRACKER_GATEWAY_URL="https://gateway.ai.cloudflare.com/v1/<account_id>/<gateway_slug>"
```

Tracker will automatically synthesize provider-specific URLs for each provider:
- Anthropic → `<gateway>/anthropic`
- OpenAI → `<gateway>/openai`
- Gemini → `<gateway>/google-ai-studio`

Your API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`) still need to be set — Cloudflare passes authentication through to the underlying providers.

You can also use the CLI flag:

```bash
tracker --gateway-url "https://gateway.ai.cloudflare.com/v1/<account_id>/<gateway_slug>" build_product
```

#### Provider-Specific Overrides

If you want to route only certain providers through the gateway, set provider-specific base URLs instead:

```bash
export TRACKER_GATEWAY_URL="https://gateway.ai.cloudflare.com/v1/account/gateway"
export ANTHROPIC_BASE_URL="https://api.anthropic.com"  # Direct, not through gateway
# OpenAI and Gemini will use the gateway
```

Provider-specific `*_BASE_URL` env vars always take precedence over `TRACKER_GATEWAY_URL`.

#### Learn More

See the [Cloudflare AI Gateway documentation](https://developers.cloudflare.com/ai-gateway/) for setup, rate limiting, analytics, and advanced routing options.


## Architecture

```mermaid
graph TB
    subgraph "Layer 3: Pipeline Engine"
        Engine["Graph Execution<br/>Edge Routing<br/>Checkpoints<br/>Decision Audit"]
        Handlers["Handlers: codergen, tool,<br/>human, parallel, fan_in,<br/>subgraph, conditional"]
        Adapter["Dippin Adapter<br/>IR → Graph"]
        TUI["TUI: node list,<br/>activity log, modals"]
    end
    subgraph "Layer 2: Agent Session"
        Session["Tool Execution<br/>Context Compaction<br/>Event Streaming"]
    end
    subgraph "Layer 1: LLM Client"
        Anthropic & OpenAI & Gemini
    end
    Engine --> Handlers
    Engine --> Adapter
    Engine --> TUI
    Handlers --> Session
    Session --> Anthropic & OpenAI & Gemini
```

## TUI

The terminal UI shows:

- **Pipeline panel**: node list in topological execution order (Kahn's algorithm) with status lamps, thinking spinners, and tool execution indicators
- **Activity log**: per-node streaming with line-level formatting (headers, code blocks, bullets), node change separators, multi-node activity indicators for parallel execution, and inline `FAILED:`/`RETRYING:` messages when nodes fail or retry
- **Subgraph nodes**: dynamically inserted and indented under their parent

### Status Icons

| Icon | Meaning |
|------|---------|
| ○ | Pending — not yet reached |
| 🟡 (spinner) | Running — LLM thinking |
| ⚡ | Running — tool executing |
| ● (green) | Completed successfully |
| ✗ (red) | Failed |
| ↻ (amber) | Retrying |
| ⊘ (dim) | Skipped — pipeline took a different path |

### Keyboard

| Key | Action |
|-----|--------|
| Ctrl+O | Toggle expand/collapse tool output |
| Ctrl+S | Submit human gate input |
| Esc | Cancel (empty) or submit (with content) |
| PgUp/PgDn | Scroll review viewport (plan approval) |
| q | Quit |

## Decision Audit Trail

Every run produces an `activity.jsonl` log in `.tracker/runs/<id>/` that captures:

- **Pipeline events**: node start/complete/fail, checkpoint saves
- **Agent events**: LLM turns, tool calls, text output
- **Decision events**: edge selection (with priority level and context snapshot), condition evaluations (with match results), node outcomes (with token counts), restart detections

Reconstruct any routing decision after the fact:

```bash
# See all edge decisions
grep 'decision_edge' .tracker/runs/<id>/activity.jsonl | python3 -m json.tool

# See condition evaluations
grep 'decision_condition' .tracker/runs/<id>/activity.jsonl | python3 -m json.tool

# See node outcomes with token counts
grep 'decision_outcome' .tracker/runs/<id>/activity.jsonl | python3 -m json.tool
```

## Troubleshooting

When a pipeline run doesn't go as expected, tracker gives you tools to understand what happened:

### `tracker diagnose`

Analyzes a run's failures and surfaces the information you need — tool stdout/stderr, error messages, timing anomalies — without manually grepping through JSONL files.

```bash
# Diagnose the most recent run
tracker diagnose

# Diagnose a specific run (prefix matching works)
tracker diagnose 7813b
```

The output shows each failed node with its output, stderr, errors, and actionable suggestions. For example, it will tell you if a tool node failed because of a stale counter file, or if a node completed suspiciously fast (suggesting a configuration issue).

### `tracker audit`

For a broader view of a run's timeline, retries, and recommendations:

```bash
# List all runs
tracker list

# Full audit report for a specific run
tracker audit <run-id>
```

### Common issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| "no LLM providers configured" | Missing API keys | `tracker setup` or export env vars |
| TestMilestone instantly escalates | Stale `fix_attempts` counter | `rm .ai/milestones/fix_attempts` |
| Node fails with no visible error | Tool stderr not surfaced | `tracker diagnose` shows full output |
| Human gate shows raw markdown | Old version before glamour fix | Update to v0.9.2+ |
| Pipeline loops forever | Unconditional fallback to loop target | Ensure fallbacks go to an exit node (Done, escalation gate), not back into the loop |
| Tool retries same error 5 times | Deterministic command bug | `tracker diagnose` flags identical retries — fix the command in the .dip file |
| Every milestone needs fixing | known_failures has comments or bad format | Ensure bare test names only, no comments — v0.11.2 strips them automatically |
| Build loop skips all milestones | Milestone headers don't match expected format | Use `## Milestone N: Title` format — v0.11.2 is flexible + fails loudly |

## Cost Governance

Tracker exposes per-provider token and dollar cost from every run, and can halt
pipelines that exceed configured ceilings.

**Library consumers** read cost via `Result.Cost`:

```go
result, _ := tracker.Run(ctx, source, tracker.Config{
    Budget: pipeline.BudgetLimits{
        MaxTotalTokens: 100_000,
        MaxCostCents:   500,           // $5.00
        MaxWallTime:    30 * time.Minute,
    },
})
if result.Status == pipeline.OutcomeBudgetExceeded {
    log.Printf("halt: %s, spent $%.4f", result.Cost.LimitsHit, result.Cost.TotalUSD)
}
for provider, pc := range result.Cost.ByProvider {
    log.Printf("%s: %d tokens, $%.4f", provider, pc.Usage.InputTokens+pc.Usage.OutputTokens, pc.USD)
}
```

**CLI users** pass flags directly to `tracker`:

```bash
tracker --max-tokens 100000 --max-cost 500 --max-wall-time 30m \
    examples/ask_and_execute.dip
```

A halted run prints a `HALTED: budget exceeded` section naming the dimension
that tripped. Run `tracker diagnose` to see the per-provider breakdown and
remediation guidance.

**Streaming consumers** subscribe to `EventCostUpdated` via
`tracker.Config.EventHandler`. Each terminal-node outcome emits a
`CostSnapshot` with aggregate tokens, dollar cost, per-provider totals,
and wall-clock elapsed time.

Reading budget limits directly from `.dip` workflow attrs is a follow-up
tracked in #67.

## CLI Reference

```
tracker [flags] <pipeline>       Run a pipeline (file path or built-in name)
tracker workflows                List built-in workflows
tracker init <workflow>          Copy a built-in to current directory
tracker setup                    Interactive provider configuration
tracker validate <pipeline>      Check pipeline structure
tracker simulate <pipeline>      Dry-run execution plan
tracker doctor                   Preflight health check
tracker diagnose [runID]         Analyze failures in a run
tracker audit <runID>            Full audit report for a run
tracker list                     List recent pipeline runs
tracker version                  Show version information
```

**Flags:**
- `-w, --workdir` — working directory (default: current)
- `-r, --resume` — resume a previous run by ID
- `--format` — pipeline format override: `dip` (default) or `dot` (deprecated)
- `--json` — stream events as NDJSON to stdout
- `--no-tui` — disable TUI dashboard, use plain console
- `--verbose` — show raw provider stream events
- `--backend` — agent backend: `native` (default) or `claude-code` (coming v0.12.0)

## Development

```bash
# Run tests
go test ./... -short

# Validate all example pipelines
for f in examples/*.dip; do tracker validate "$f"; done

# Run dippin simulation tests
for f in examples/*.dip; do dippin test "$f"; done

# Check with dippin-lang tools
dippin doctor examples/build_product.dip
dippin simulate -all-paths examples/build_product.dip
```

## License

See [LICENSE](LICENSE).
