# Conditional (Edge Condition Evaluator)

## Purpose

A "conditional" node in tracker is a diamond-shaped router. The node itself does
no real work — its handler is a no-op that returns `OutcomeSuccess` — but its
outgoing edges carry `when` conditions that the engine evaluates to pick the
next step. The actual routing logic lives in
[`pipeline/condition.go`](../../../pipeline/condition.go), and every handler
(not just conditional nodes) is routed through the same evaluator. Use a
conditional node when you want an explicit branch point that doesn't execute an
agent or a tool — just inspects context and picks a path.

## Node attributes

Conditional nodes have no specific attributes. The routing lives on outgoing
edges via the `when` clause (see the dippin-lang spec). The handler
implementation is a single-file [`ConditionalHandler`](../../../pipeline/handlers/conditional.go)
that always returns success; everything below is about the evaluator that runs
on every edge, not just on edges out of a conditional node.

## Operators and syntax

Supported forms, parsed in priority order: `||` (lowest) → `&&` → clause.

| Operator | Form | Semantics |
|----------|------|-----------|
| `=` / `==` | `ctx.outcome = success` | String equality; surrounding quotes stripped on RHS |
| `!=` | `ctx.outcome != fail` | String inequality; surrounding quotes stripped on RHS |
| `contains` | `ctx.last_response contains error` | Substring match |
| `startswith` | `ctx.tool_stdout startswith ERROR` | Prefix match |
| `endswith` | `ctx.last_response endswith .json` | Suffix match |
| `in` | `ctx.outcome in success,retry` | Membership against comma-separated set |
| `not <op>` | `ctx.x not contains foo` | Negated form of `contains`, `startswith`, `endswith`, `in` |
| `not <clause>` | `not ctx.outcome = success` | Clause-level negation |
| `&&` | `ctx.a = 1 && ctx.b = 2` | Logical AND, short-circuited |
| `\|\|` | `ctx.a = 1 \|\| ctx.b = 2` | Logical OR, short-circuited |

Empty or whitespace-only conditions evaluate to `true` (equivalent to an
unconditional edge).

### Known limitations

Documented at the top of
[`pipeline/condition.go`](../../../pipeline/condition.go):

- Operator splitting uses `strings.Split` on `||` and `&&` before clause
  parsing — a quoted value that contains these literals is still split.
- No parentheses for grouping. Precedence is fixed: `||` < `&&` < clause.
- Quote stripping applies only to `=`, `==`, `!=` comparisons — the word
  operators (`contains`, `startswith`, etc.) take their RHS verbatim.

## Namespace handling

The evaluator resolves variable references on the LHS of a clause with
[`resolveVariable`](../../../pipeline/condition.go), which walks four lookup
strategies:

1. Bare key: `outcome`, `last_response` — looks up directly in the pipeline
   context.
2. `ctx.<key>`: strips the `ctx.` prefix, then falls through to the bare-key
   lookup. This is the form emitted by dippin-lang IR.
3. `context.<key>`: alias for `ctx.` — same behavior.
4. `internal.<key>` (or `ctx.internal.<key>` / `context.internal.<key>`):
   routed to the engine-only `internal` namespace, which stores bookkeeping
   like `_artifact_dir`.

Unresolved references log a warning and default to the empty string — not a
silent success, but execution continues. See
[`resolveAndWarnVar`](../../../pipeline/condition.go).

## Outcomes produced

The `ConditionalHandler` itself always returns
`pipeline.Outcome{Status: OutcomeSuccess}`. No `ContextUpdates`,
`PreferredLabel`, or `SuggestedNextNodes` are written. Routing is entirely the
engine's job, so the handler exists only to satisfy the graph-traversal
contract.

## Events emitted

Emitted by the engine (not by the handler) in
[`pipeline/engine_edges.go`](../../../pipeline/engine_edges.go) while it walks
outgoing edges:

| Event | When |
|-------|------|
| `EventDecisionCondition` | One per conditional edge evaluated — records the expression, match result, and a context snapshot |
| `EventDecisionEdge` | Emitted once when an edge is selected; records `EdgePriority` (condition / label / suggested / weight / lexical) |
| `EventEdgeTiebreaker` | Emitted when two or more equal-weight unconditional edges force a lexical tiebreak |
| `EventStageFailed` | Emitted by the engine on strict-failure-edge enforcement — node outcome was `fail` and no outgoing edge had a condition (see below) |

## Strict failure edges

[`checkStrictFailure`](../../../pipeline/engine.go) halts the pipeline when the
current node's `outcome` is `fail` and every outgoing edge is unconditional.
This is deliberate: an unconditional edge from a failed node almost always
means the pipeline author forgot to handle failure. To route on failure,
explicitly add `when ctx.outcome = fail` on one of the outgoing edges.

From
[`hasAnyConditionalEdge`](../../../pipeline/engine.go):

> When a node has conditional edges, the pipeline author has intentionally
> designed routing for different outcomes. When all edges are unconditional,
> a failure outcome would blindly continue — which is almost always a bug.

## Edge selection priority

Evaluated top-down in [`selectEdge`](../../../pipeline/engine_edges.go):

1. **Condition**: first edge whose `when` expression evaluates to true.
2. **Preferred label**: matches `pctx[preferred_label]` against `edge.Label`
   (human gate choice-mode).
3. **Suggested next nodes**: matches `pctx[suggested_next_nodes]` against
   `edge.To` (parallel handler's join hint).
4. **Weight**: highest `weight` attr wins.
5. **Lexical**: tie-break among equal-weight unconditional edges.

## Example

```dip
agent Verify
  prompt: "Check that the build succeeds."
  auto_status: true

conditional Gate

Verify -> Gate
Gate -> Retry when: ctx.outcome = retry
Gate -> Escalate when: ctx.outcome = fail
Gate -> Done when: ctx.outcome = success
```

The `Verify` agent emits a `STATUS:` directive that gets parsed into the
`outcome` context key (via `auto_status: true`). `Gate` is a conditional node
— its handler is a no-op, but the three outgoing edges are mutually exclusive
and exhaustive across the three outcomes. No unconditional fallback is needed
because the three conditions cover every value `auto_status` can produce.

## Gotchas

- **Dippin-lang emits `ctx.` prefix.** The evaluator strips it. Always use
  `ctx.X` in `.dip` files; bare `X` works in tests but is inconsistent with
  the IR.
- **The adapter synthesizes implicit edges.** For parallel/fan-in nodes, the
  adapter in
  [`pipeline/dippin_adapter_edges.go`](../../../pipeline/dippin_adapter_edges.go)
  generates unconditional edges from `ParallelConfig.Targets` /
  `FanInConfig.Sources`. These implicit edges participate in the same
  routing priority but are not visible in the source `.dip` file.
- **No unconditional fallback to a loop target.** Pairing
  `when ctx.outcome = fail -> FixX` with an unconditional `-> FixX` creates
  an infinite loop when `outcome` is not `fail` (the fallback catches the
  success case and routes right back). Send fallbacks to an escalation gate
  or Done instead.

## See also

- [`pipeline/condition.go`](../../../pipeline/condition.go) — evaluator
- [`pipeline/engine_edges.go`](../../../pipeline/engine_edges.go) — edge
  selection priority
- [`pipeline/handlers/conditional.go`](../../../pipeline/handlers/conditional.go)
  — the no-op handler
- [Parallel and Fan-In](./parallel-fan-in.md) — how `suggested_next_nodes`
  works
- [Human Gate](./human.md) — how `preferred_label` works
- `CLAUDE.md` — `### Edge routing — no unconditional fallbacks to loop targets`
  and `### Strict failure edges (v0.13.0)`
