# Pipeline Context Flow

Each node in a Tracker pipeline runs a fresh LLM session with a clean context window. There is no conversation history that automatically carries from one node to the next. Data flows between nodes via context keys in a shared key-value store.

## Core Concept

- **Fresh session per node**: Each node gets a new LLM conversation, not a continuation of the previous one.
- **Data flows via context keys**: Values like `last_response`, `outcome`, and custom keys persist in the pipeline context.
- **Per-node scoping** (v0.17.0+): After each node, outputs are stored in `node.<nodeID>.<key>` to prevent collisions in parallel pipelines.
- **Fidelity levels**: Control how much prior context is injected into each node's prompt to manage token usage.

## Why This Design

As noted in the project meeting: "The DIP files have a weird context window situation where it doesn't necessarily have previous context in... which has turned out to be pretty positive because if you look at how people are doing Claude Code the best, it's like clear context before doing big work."

Each node gets only the context it needs, not accumulated noise from all prior steps.

## Built-in Context Keys

| Key | Set By | Purpose |
|-----|--------|---------|
| `last_response` | Agent/tool | Most recent node's output |
| `response.<nodeID>` | Agents | Per-node response snapshot |
| `outcome` | All handlers | success/fail/retry status |
| `preferred_label` | Human gates | User's choice from options |
| `human_response` | Human gates | Freeform user input |
| `tool_stdout` / `tool_stderr` | Tools | Command output |
| `graph.goal` | Engine | Pipeline's goal |

## Per-Node Scoping

In parallel pipelines, the last-writer-wins model causes collisions. Scoped keys prevent this:

```dip
fan_in Review <- BranchA, BranchB

agent Review
  prompt: |
    BranchA result: ${ctx.node.BranchA.last_response}
    BranchB result: ${ctx.node.BranchB.last_response}
```

Without scoped keys, `last_response` would only contain whichever branch finished last.

## Fidelity Levels

Control context window size by specifying how much prior context to inject:

- `full` — all context (default)
- `summary:high` — all keys + trimmed artifacts
- `summary:medium` — only: outcome, last_response, human_response, tool_stdout, graph.goal
- `summary:low` — only: goal + completed node list
- `compact` — goal + outcome only
- `truncate` — summary:medium keys capped at ~500 chars

## Common Patterns

### Sequential Pipe
```dip
agent Step1
  prompt: Analyze...

agent Step2
  prompt: Based on ${ctx.last_response}, now...
```

### Reference Specific Node
```dip
agent Later
  prompt: |
    From Step1: ${ctx.node.Step1.last_response}
    From Step2: ${ctx.node.Step2.last_response}
```

### Parallel with Fan-in
```dip
parallel Work -> BranchA, BranchB

fan_in Join <- BranchA, BranchB

agent Synthesize
  prompt: |
    A: ${ctx.node.BranchA.last_response}
    B: ${ctx.node.BranchB.last_response}
    Combine these...
```

## Common Mistakes

❌ Expecting chat history: Each node is a fresh session.
✅ Pass data explicitly: Use context keys and scoped keys.

❌ Using `last_response` in parallel: Gets the last branch to finish.
✅ Use scoped keys: `${ctx.node.Branch1.output}`, `${ctx.node.Branch2.output}`.

## See Also

- [CLAUDE.md](../CLAUDE.md): "Token usage flows through three layers"
- `pipeline/context.go`: `PipelineContext`, `ScopeToNode`, `GetScoped`
- `pipeline/fidelity.go`: Fidelity levels implementation
- `examples/ask_and_execute.dip`: Real-world parallel example
