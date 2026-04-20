# Manager Loop Handler (`stack.manager_loop`)

The manager loop implements Attractor spec 4.11 — a supervisor node that
launches a child pipeline asynchronously, polls at intervals, and optionally
steers the running child by injecting context mid-execution.

## Lifecycle

```
┌─────────────────────────────────────────────┐
│  Manager Node (parent pipeline)             │
│                                             │
│  1. Look up child graph by subgraph_ref     │
│  2. Build child Engine with scoped events   │
│  3. Launch child in goroutine               │
│  4. Enter poll loop ─────────────┐          │
│     │                            │          │
│     ├─ ctx.Done? → cancel+wait → fail      │
│     ├─ child finished? → return result      │
│     └─ poll tick (every 45s default):       │
│         ├─ increment cycle counter          │
│         ├─ check max_cycles → fail if hit   │
│         ├─ evaluate stop_condition → early  │
│         │   success if matched              │
│         └─ evaluate steer_condition →       │
│             inject keys into child channel  │
└─────────────────────────────────────────────┘
```

## Configuration (node attributes)

| Attribute | Default | Purpose |
|-----------|---------|---------|
| `subgraph_ref` | (required) | Name of the child graph to launch |
| `manager.poll_interval` | `45s` | How often to check conditions |
| `manager.max_cycles` | `1000` | Safety limit before forced stop |
| `manager.stop_condition` | (none) | Expression evaluated against parent context each tick |
| `manager.steer_condition` | (none) | When true, inject steer_context into child |
| `manager.steer_context` | (none) | `key=value,key=value` pairs sent to child |

## Key Design Decisions

### Isolation

The child gets a *snapshot* of the parent context (`pctx.Snapshot()`), not a
shared reference. Parent writes (`stack.child.status`, `stack.child.cycles`)
don't bleed into the child, and child writes don't pollute the parent until the
child finishes and its context is merged back via `ContextUpdates`.

### Steering Channel

The manager creates a buffered channel (`chan map[string]string`, cap 1) and
passes it to the child engine via `WithSteeringChan`. The engine drains this
channel *between* nodes (after one node's outcome is applied, before the next
node starts). This means steering takes effect at node boundaries, not
mid-execution.

The drain uses a non-blocking loop:

```go
func (e *Engine) drainSteering(s *runState) {
    if e.steeringCh == nil { return }
    for {
        select {
        case update, ok := <-e.steeringCh:
            if !ok { return }
            s.pctx.Merge(update)
        default:
            return
        }
    }
}
```

This mirrors the `agent/session_run.go:drainSteering()` pattern.

### Goroutine Safety

The child runs in a goroutine with `defer/recover` for panic protection. On
all early-exit paths (cancel, max_cycles, stop_condition), the manager calls
`cancelChild()` then `<-resultCh` to wait for the goroutine to finish — no
leaked goroutines.

### Status Preservation

If the child exits with `OutcomeBudgetExceeded`, that status propagates up
rather than being flattened to `OutcomeFail`. The parent pipeline can route on
the actual child exit status via `stack.child.exit_status`.

### Stop Condition Semantics

A stop condition match returns `OutcomeSuccess` — it's an intentional early
exit ("I've seen enough"), not a failure. Pipelines can distinguish this via
`stack.child.status = stop_condition_met`.

## Context Keys Written

| Key | Values | Description |
|-----|--------|-------------|
| `stack.child.status` | `running`, `success`, `failed`, `error`, `cancelled`, `max_cycles_exceeded`, `stop_condition_met` | Current child lifecycle state |
| `stack.child.cycles` | integer string | Number of poll ticks elapsed |
| `stack.child.exit_status` | outcome string | Raw outcome status from child engine |

## Example `.dip` Usage

```dot
Manager [shape=house handler="stack.manager_loop"
         subgraph_ref="agent_loop"
         manager.poll_interval="30s"
         manager.max_cycles="20"
         manager.stop_condition="stack.child.cycles = 10"
         manager.steer_condition="stack.child.cycles = 5"
         manager.steer_context="hint=speed_up,priority=high"]
```

This launches the `agent_loop` subgraph, checks every 30s, injects
`hint=speed_up` + `priority=high` into the child at cycle 5, and stops early
at cycle 10 if the child hasn't finished yet.

## Relationship to SubgraphHandler

The manager loop shares construction patterns with `SubgraphHandler`:
- Both use `NodeScopedPipelineHandler` for event scoping
- Both use `RegistryFactory` for child registry construction
- Both pass `WithInitialContext(pctx.Snapshot())` to isolate the child

The key difference: `SubgraphHandler` runs the child synchronously (blocking
the parent), while the manager loop runs it asynchronously and polls. This
enables long-running children that the parent can observe, steer, or
terminate.

## Events Emitted

| Event Type | When |
|------------|------|
| `EventStageStarted` | Child launched |
| `EventManagerCycleTick` | Each poll tick; also when steering is injected |
| `EventStageCompleted` | Child succeeded or stop_condition met |
| `EventStageFailed` | Child failed, max_cycles reached, or error |

## File Locations

- Handler: `pipeline/handlers/manager_loop.go`
- Engine steering: `pipeline/engine_run.go` (`drainSteering`)
- Engine option: `pipeline/engine.go` (`WithSteeringChan`)
- Tests: `pipeline/handlers/manager_loop_test.go`, `pipeline/engine_steering_test.go`
