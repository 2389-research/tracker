# Pipeline Context Flow

Tracker pipelines pass data between nodes through a shared key-value store called the **pipeline context**, not through LLM chat history. Agent nodes start a fresh LLM session each time they run — prior conversation turns are not replayed. Tool, human, parallel, and fan-in nodes produce outputs by writing specific keys into the context. Downstream nodes read those keys to see what happened upstream.

## Core Concept

- **Fresh LLM sessions**: each agent node runs a brand-new conversation with its own system prompt. No chat history leaks between agent nodes.
- **Shared context store**: all nodes read from and write to a single `PipelineContext` (a string-keyed map) for the whole run.
- **Per-node scoping** (upcoming — currently on `main`, unreleased): after each node completes, every key it wrote is also copied into `node.<nodeID>.<key>` so later nodes can reference a specific upstream node's output by name.
- **Fidelity levels** control how much context an agent node sees in its prompt to prevent context-window bloat.

## Why sessions are fresh

Each node solves a single well-scoped problem. Carrying a full conversation history across nodes would push the LLM into a context window built for a different task, wasting tokens and confusing attention. By resetting the session every node, tracker matches how the best Claude Code workflows operate — clear the context before big work, pass only the inputs that matter.

## Built-in context keys

| Key | Written by | Purpose |
|---|---|---|
| `last_response` | agent (codergen) handler | The most recent agent node's final message content |
| `response.<nodeID>` | agent and human handlers | Per-node response snapshot addressable by node ID |
| `outcome` | every handler | `success` / `fail` / `retry` / `escalate` for the node that just ran |
| `preferred_label` | human handler | Label the user selected on a labeled gate |
| `human_response` | human handler | Freeform text the user typed at a gate |
| `tool_stdout` / `tool_stderr` | tool handler | Subprocess output captured from `tool_command` |
| `graph.goal` | engine (from `workflow.Goal`) | The workflow's stated objective, always available |
| `parallel.results` | parallel handler | JSON array with one entry per branch: `{node_id, status, context_updates, stats}` |

Handlers do NOT let user content set arbitrary context keys. The table above lists every key that each handler writes. Agent, tool, and human nodes each populate a fixed set — agents write `last_response` and `response.<nodeID>`, tools write `tool_stdout`/`tool_stderr`, humans write `human_response` and `preferred_label`. If you need to propagate a custom value from one node to the next, see **[Returning custom data from a node](#returning-custom-data-from-a-node)** below.

## Referencing context in prompts and commands

Inside an agent `prompt:`, a tool `command:`, or an edge `when:` condition, refer to a context key with `${ctx.<key>}`:

```dip
agent Step2
  prompt: |
    The planner said: ${ctx.last_response}
    Goal: ${ctx.graph.goal}
```

For per-node scoped reads (addressing a specific earlier node):

```dip
agent Synthesize
  prompt: |
    Design from Architect: ${ctx.node.Architect.last_response}
    Review from Critic:    ${ctx.node.Critic.last_response}
```

Note that `ctx.` is the user-facing prefix; internally the engine stores bare keys (`last_response`) and scoped aliases (`node.Architect.last_response`). Conditions strip the prefix before lookup.

## Per-node scoping details

After a node finishes, the engine calls `PipelineContext.ScopeToNode(nodeID)`, which copies the keys marked dirty by `Set` or `Merge` since the previous scope into `node.<nodeID>.<key>` aliases. Bootstrap writes that happen before execution begins (via `NewPipelineContextFrom` and the `ClearDirty` call in `initRunState`) are excluded — only keys dirtied during the node's actual execution are scoped. Keys that already start with `node.` are skipped to avoid creating doubly-nested aliases. Earlier scoped aliases are preserved — only the bare keys get last-writer-wins semantics.

**Sequential pipelines:** `${ctx.node.<id>.last_response}` is the reliable way to reference a specific agent's output later in the run, instead of relying on `last_response` (which changes every node).

**Parallel branches:** parallel branch handlers run in their own isolated context snapshots. After all branches complete, the parallel node writes a single `parallel.results` JSON array that surfaces each branch's status and context updates. The parent context does NOT gain per-branch scoped keys — there is no `${ctx.node.<BranchID>.*}` namespace populated by parallel execution. If you need per-branch outputs after a fan-in, the robust patterns today are:

1. **Filesystem hand-off** (used by `examples/ask_and_execute.dip`): each branch writes files under `.ai/` and the fan-in node reads them.
2. **Parse `parallel.results`**: the parallel handler writes a JSON array to `${ctx.parallel.results}` containing each branch's status and any `ContextUpdates` it produced.
3. **Explicit per-branch keys**: a branch's agent prompt can instruct the model to write a specific key (e.g. `branch_a_summary`), and the fan-in node reads `${ctx.branch_a_summary}`.

Reading `${ctx.node.<BranchID>.last_response}` after a parallel block will NOT automatically contain each branch's final output — use one of the patterns above instead.

## Returning custom data from a node

**Handlers write only a fixed set of built-in keys** (see the table above). There is no "write any key you want from the agent's response" feature — no `$TRACKER_CONTEXT` environment variable, no magic response prefix. If you need a custom value from one node to be available under a specific key for downstream nodes, pick one of these three supported patterns:

### 1. Encode structured output in `last_response` and reference it downstream

The easiest pattern when the next node is also an agent. The producer emits JSON or a key-value block inside its response; the consumer reads `${ctx.last_response}` (or `${ctx.node.<id>.last_response}` for a specific ancestor) and instructs the LLM to parse it.

```dip
agent Planner
  prompt: |
    Plan the work. Respond with JSON:
    { "milestone": "m1-auth", "files": ["auth.go", "session.go"] }
  response_format: json_object

agent Builder
  prompt: |
    Plan from Planner: ${ctx.last_response}
    Parse the JSON, extract "milestone", and build the listed files.
```

Force the structure at the API level with `response_format: json_object` so the producer is guaranteed to emit valid JSON (Anthropic, OpenAI, and Gemini all support this via their native JSON modes — see CLAUDE.md's "Structured output" section).

### 2. Write to a file from one node, read from the file in the next

The robust pattern when the handoff is large, binary, or needs to survive across tool and human nodes. The producer writes to a known path under the working directory or `.ai/` scratch dir; downstream nodes read from the same path.

```dip
agent GenerateSpec
  prompt: |
    Write the spec to .ai/spec.md. Your `write` tool is available.

tool ExtractMilestone
  command: |
    set -eu
    jq -r '.milestone' < .ai/spec.md > .ai/milestone.txt
    cat .ai/milestone.txt      # goes to tool_stdout

agent Builder
  prompt: |
    Implementing milestone: ${ctx.tool_stdout}
    Full spec lives at .ai/spec.md — read it with your tools.
```

The filesystem is the source of truth; `tool_stdout` / `last_response` just carry a small reference. This scales to megabyte-sized intermediate artifacts without blowing up the context window.

### 3. Use `auto_status` to route on a structured cue from an agent response

When the "custom value" you actually need is just the node's outcome (success/fail/retry), set `auto_status: true` on the agent. The handler scans the response for a `STATUS: success|fail|retry` line and sets the outcome accordingly. This drives edge routing via `when ctx.outcome = ...` without needing a separate key.

```dip
agent Critic
  auto_status: true
  prompt: |
    Review the diff. End your response with exactly one line:
    STATUS: success   (if the change is ready to merge)
    STATUS: retry     (if the author needs to fix something)
    STATUS: fail      (if the whole approach is wrong)

edges
  Critic -> Merge      when ctx.outcome = success
  Critic -> FixReview  when ctx.outcome = retry
  Critic -> Abandon    when ctx.outcome = fail
```

### What about tool nodes writing custom keys?

A `tool` handler only writes `tool_stdout` and `tool_stderr`. There is no `echo FOO=bar` pattern that sets `${ctx.FOO}`. Use pattern 2 (filesystem) for large values, or make the command emit JSON on stdout and have a downstream agent parse `${ctx.tool_stdout}`.

### What about human gates?

A `human` node (`mode: freeform`) writes exactly `human_response` (the free text) and `response.<nodeID>`. Labeled gates also write `preferred_label`. There is no way for the human to set additional keys — if you need structured data from a human, run an agent after the gate that parses `${ctx.human_response}` into the keys you want, or use `mode: interview` for multi-field form collection.

## Fidelity levels

An agent node receives a compacted view of the pipeline context as part of its prompt construction. The amount of context injected is controlled by the `fidelity` attribute.

### Levels (from `pipeline/fidelity.go`)

- **`full`** — every key in the pipeline context is passed through. Expensive; use for nodes that genuinely need the whole picture.
- **`summary:high`** — all keys plus a `summary.<nodeID>` entry per completed node, containing the first 2000 characters of each node's artifact `response.md`.
- **`summary:medium`** — only these keys: `outcome`, `last_response`, `human_response`, `preferred_label`, `tool_stdout`, `tool_stderr`, `graph.goal`.
- **`summary:low`** — `graph.goal` plus a single `completed_summary` key listing which nodes have finished.
- **`compact`** — `graph.goal` and `outcome` only.
- **`truncate`** — same keys as `summary:medium`, but each value is word-boundary-truncated to 500 characters and `"..."` is appended when truncation occurs.

On resume from a checkpoint, the engine applies `DegradeFidelity()` to drop one level automatically so the replayed context doesn't blow out the window.

### How to configure fidelity

Set it as a node attribute (highest precedence):

```dip
agent Planner
  fidelity: summary:high
  prompt: ...
```

Or set a graph-level default that every node inherits unless it overrides:

```dip
workflow build_product
  defaults:
    fidelity: summary:medium
```

Lookup order: node `fidelity` attribute → graph `default_fidelity` attribute → hardcoded default (`full`).

## Common patterns

### Sequential pipe

```dip
agent Analyze
  prompt: |
    Analyze this spec: ${ctx.human_response}

agent Build
  prompt: |
    Based on: ${ctx.last_response}
    Build the described feature.
```

### Reference a specific earlier node

```dip
agent Review
  prompt: |
    Plan from Architect: ${ctx.node.Architect.last_response}
    Code from Builder:   ${ctx.node.Builder.last_response}
    Review both and report.
```

### Conditional routing on outcome

```dip
edges
  Build -> Test        when ctx.outcome = success
  Build -> FixFailure  when ctx.outcome = fail
```

## Common mistakes

| Mistake | Fix |
|---|---|
| Expecting the chat history from one agent node to carry into the next. | Reset your mental model: each agent node sees only its own prompt. Pass data explicitly via `${ctx.<key>}`. |
| Reading `${ctx.last_response}` after a parallel fan-in and expecting the results of all branches. | Use `parallel.results`, filesystem hand-off, or explicit per-branch keys. |
| Setting huge prompt content that blows past the context window. | Choose a lower fidelity, or use `truncate` / `summary:low` on downstream nodes. |
| Relying on a custom key written by one node without declaring it in the workflow. | Pick a key name, write it in the upstream node's prompt/command, and read it downstream with `${ctx.<key>}`. |

## See also

- `CLAUDE.md` — "Token usage flows through three layers" explains how token accounting aggregates across nodes
- `pipeline/context.go` — `PipelineContext`, `ScopeToNode`, `GetScoped`
- `pipeline/fidelity.go` — fidelity level implementation and compaction
- `examples/ask_and_execute.dip` — real-world parallel + fan-in pattern using filesystem hand-off
