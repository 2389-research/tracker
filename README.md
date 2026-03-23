# Tracker

Pipeline orchestration engine for multi-agent LLM workflows. Define pipelines in `.dip` files (Dippin language), execute them with parallel agents, and watch progress in a TUI dashboard.

Built by [2389.ai](https://2389.ai).

## Quick Start

```bash
# Install
go install github.com/2389-research/tracker/cmd/tracker@latest

# Run a pipeline
tracker examples/ask_and_execute.dip

# Validate a pipeline without running it
tracker validate examples/build_product.dip

# Resume a stopped run
tracker -r <run-id> examples/build_product.dip
```

## Pipeline Examples

### `ask_and_execute.dip`
Competitive implementation: ask the user what to build, fan out to 3 agents (Claude/Codex/Gemini) in isolated git worktrees, cross-critique the implementations, select the best one, apply it, clean up the rest.

### `build_product.dip`
Sequential milestone builder: read a SPEC.md, decompose into milestones, implement each with verification loops, cross-review the complete result, verify full spec compliance.

### `build_product_with_superspec.dip`
Parallel stream execution for large structured specs: reads the spec's work streams and dependency graph, executes independent streams in parallel (with git worktree isolation), enforces quality gates between phases, cross-reviews with 3 specialized reviewers (architect/QA/product), and audits traceability.

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
| `human` | hexagon | Human-in-the-loop gate (choice or freeform) |
| `tool` | parallelogram | Shell command execution |
| `parallel` | component | Fan-out to concurrent branches |
| `fan_in` | tripleoctagon | Join parallel branches |
| `subgraph` | tab | Execute a referenced sub-pipeline |

### Variable Interpolation

Three namespaces for `${...}` syntax in prompts:

- `${ctx.outcome}` — runtime pipeline context (outcome, last_response, human_response, tool_stdout)
- `${params.model}` — subgraph parameters passed from parent
- `${graph.goal}` — workflow-level attributes

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

### Per-Node Working Directory

For git worktree isolation in parallel implementations:

```dip
agent ImplementClaude
  working_dir: .ai/worktrees/claude
  model: claude-sonnet-4-6
  prompt: Implement the spec in this isolated worktree.
```

### Human Gates

Freeform mode captures text input. If the response matches an edge label (case-insensitive), it routes to that edge. Otherwise it's stored as `ctx.human_response`.

```dip
human ApproveSpec
  label: "Review the spec. Approve, refine, or reject."
  mode: freeform

edges
  ApproveSpec -> Build  label: "approve"
  ApproveSpec -> Revise label: "refine"  restart: true
  ApproveSpec -> Done   label: "reject"
```

Submit with **Ctrl+S**. Enter inserts newlines.

## TUI

The terminal UI shows:

- **Pipeline panel**: node list with status lamps (pending/running/done/failed), thinking spinners, and tool execution indicators
- **Activity log**: streaming LLM output with line-level formatting (headers, code blocks, bullets), tool call display, and node change separators
- **Subgraph nodes**: dynamically inserted and indented under their parent

### Keyboard

| Key | Action |
|-----|--------|
| Ctrl+O | Toggle expand/collapse tool output |
| Ctrl+S | Submit human gate input |
| Esc | Submit human gate input (quick approval) |

## Architecture

```
Layer 1: LLM Client (anthropic, openai, google providers)
Layer 2: Agent Session (tool execution, context compaction, event streaming)
Layer 3: Pipeline Engine (graph execution, edge routing, checkpoints)
    ├── Handlers: codergen, tool, human, parallel, fan_in, subgraph, conditional
    ├── Dippin Adapter: converts IR to Graph, synthesizes implicit edges
    └── TUI: bubbletea app with node list, activity log, modal overlays
```

## Development

```bash
# Run tests
go test ./... -short

# Validate all example pipelines
for f in examples/*.dip; do tracker validate "$f"; done

# Check with dippin-lang tools
dippin check examples/build_product_with_superspec.dip
dippin lint examples/build_product_with_superspec.dip
dippin simulate -all-paths examples/build_product_with_superspec.dip
```

## License

See [LICENSE](LICENSE).
