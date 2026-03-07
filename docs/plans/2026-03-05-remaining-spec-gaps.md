# Remaining Spec Gaps Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Close the remaining 5 gaps between tracker and the attractor reference specs.

**Architecture:** Each feature extends existing patterns (middleware chain, handler interface, tool registry, validation aggregation). No new architectural paradigms needed.

**Tech Stack:** Go, existing tracker interfaces

---

### Task 1: Message Transform Middleware

Adds a reusable middleware that transforms messages before they reach the LLM provider. The existing `Middleware` interface already supports this pattern perfectly — we just need a concrete implementation.

**Files:**
- Create: `llm/transform.go`
- Create: `llm/transform_test.go`

**Step 1: Write the failing test**

```go
// llm/transform_test.go
func TestTransformMiddleware_ModifiesRequest(t *testing.T) {
    called := false
    transform := NewTransformMiddleware(func(req *Request) {
        req.Messages = append(req.Messages, SystemMessage("injected"))
        called = true
    })

    next := func(ctx context.Context, req *Request) (*Response, error) {
        // Verify the injected message is present.
        if len(req.Messages) != 2 {
            t.Fatalf("expected 2 messages, got %d", len(req.Messages))
        }
        return &Response{Message: AssistantMessage("ok")}, nil
    }

    handler := transform.WrapComplete(next)
    _, err := handler(context.Background(), &Request{
        Messages: []Message{UserMessage("hello")},
    })
    if err != nil {
        t.Fatal(err)
    }
    if !called {
        t.Fatal("transform was not called")
    }
}
```

Additional tests:
- `TestTransformMiddleware_NoOp` — nil/empty transform passes through unchanged
- `TestTransformMiddleware_ResponseTransform` — post-call response transform
- `TestTransformMiddleware_ChainMultiple` — two transforms compose correctly
- `TestTransformMiddleware_Interface` — compile-time interface check

**Step 2: Run test to verify it fails**

Run: `go test ./llm/ -run TestTransform -v`
Expected: FAIL — `NewTransformMiddleware` not defined

**Step 3: Write minimal implementation**

```go
// llm/transform.go
// ABOUTME: Middleware that transforms LLM requests and responses.
// ABOUTME: Applies user-defined functions to messages before/after provider calls.

type RequestTransformFunc func(req *Request)
type ResponseTransformFunc func(resp *Response)

type TransformMiddleware struct {
    requestTransform  RequestTransformFunc
    responseTransform ResponseTransformFunc
}

func NewTransformMiddleware(reqFn RequestTransformFunc, opts ...TransformOption) *TransformMiddleware
func WithResponseTransform(fn ResponseTransformFunc) TransformOption

func (m *TransformMiddleware) WrapComplete(next CompleteHandler) CompleteHandler {
    return func(ctx context.Context, req *Request) (*Response, error) {
        if m.requestTransform != nil {
            m.requestTransform(req)
        }
        resp, err := next(ctx, req)
        if err != nil {
            return resp, err
        }
        if m.responseTransform != nil {
            m.responseTransform(resp)
        }
        return resp, nil
    }
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./llm/ -run TestTransform -v`
Expected: PASS

**Step 5: Commit**

```bash
git add llm/transform.go llm/transform_test.go
git commit -m "feat(llm): add message transform middleware"
```

---

### Task 2: Mid-Session Steering

Adds the ability to inject steering messages into an active agent session. Uses a channel that the Run() loop checks between turns.

**Files:**
- Create: `agent/steering.go`
- Create: `agent/steering_test.go`
- Modify: `agent/session.go` — add steering channel + check in Run loop
- Modify: `agent/events.go` — add `EventSteeringInjected`

**Step 1: Write the failing test**

```go
// agent/steering_test.go
func TestSession_SteeringInjection(t *testing.T) {
    // Create session with steering channel.
    steer := make(chan string, 1)
    sess, _ := NewSession(mockClient, DefaultConfig(),
        WithSteering(steer),
    )

    // Send steering message before Run.
    steer <- "focus on error handling"

    result, err := sess.Run(ctx, "hello")
    // Verify steering message was injected into conversation.
    // (check via event handler that EventSteeringInjected was emitted)
}
```

Additional tests:
- `TestSession_SteeringNoChannel` — session works normally without steering
- `TestSession_SteeringMultipleMessages` — multiple steering messages across turns
- `TestSession_SteeringEventEmitted` — EventSteeringInjected event contains the injected text

**Step 2: Run test to verify it fails**

Run: `go test ./agent/ -run TestSession_Steering -v`
Expected: FAIL — `WithSteering` not defined

**Step 3: Write minimal implementation**

```go
// agent/steering.go
// ABOUTME: Mid-session steering allows injecting instructions into an active agent loop.
// ABOUTME: Steering messages are checked between turns via a non-blocking channel read.

// WithSteering attaches a steering channel to receive mid-session instructions.
func WithSteering(ch <-chan string) SessionOption {
    return func(s *Session) {
        s.steering = ch
    }
}
```

In `session.go` Run() loop, at top of each iteration (after ctx.Err check):
```go
// Check for steering messages (non-blocking).
if s.steering != nil {
    select {
    case msg := <-s.steering:
        s.messages = append(s.messages, llm.UserMessage("[STEERING] " + msg))
        s.emit(Event{Type: EventSteeringInjected, SessionID: s.id, Text: msg})
    default:
    }
}
```

Add `EventSteeringInjected EventType = "steering_injected"` to events.go.
Add `steering <-chan string` field to Session struct.

**Step 4: Run test to verify it passes**

Run: `go test ./agent/ -run TestSession_Steering -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/steering.go agent/steering_test.go agent/session.go agent/events.go
git commit -m "feat(agent): add mid-session steering via channel injection"
```

---

### Task 3: Semantic Linting for Pipelines

Extends pipeline validation with semantic checks: handler registration, condition syntax, and node attribute validation.

**Files:**
- Create: `pipeline/validate_semantic.go`
- Create: `pipeline/validate_semantic_test.go`
- Modify: `pipeline/handler.go` — add `RegisteredHandlers()` method to HandlerRegistry

**Step 1: Write the failing test**

```go
// pipeline/validate_semantic_test.go
func TestValidateSemantic_UnregisteredHandler(t *testing.T) {
    g := buildTestGraph() // graph with node whose Handler is "nonexistent"
    registry := NewHandlerRegistry()
    // Don't register the handler.

    err := ValidateSemantic(g, registry)
    if err == nil {
        t.Fatal("expected validation error for unregistered handler")
    }
    ve := err.(*ValidationError)
    if !containsError(ve, "unregistered handler") {
        t.Fatalf("expected unregistered handler error, got: %v", ve)
    }
}
```

Additional tests:
- `TestValidateSemantic_InvalidConditionSyntax` — edge condition has bad syntax
- `TestValidateSemantic_AllValid` — fully valid graph passes
- `TestValidateSemantic_InvalidMaxRetries` — non-integer max_retries attribute
- `TestValidateSemantic_EmptyGraph` — nil/empty graph handled gracefully
- `TestValidateSemantic_MixedErrors` — multiple semantic errors collected together

**Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestValidateSemantic -v`
Expected: FAIL — `ValidateSemantic` not defined

**Step 3: Write minimal implementation**

```go
// pipeline/validate_semantic.go
// ABOUTME: Semantic validation for pipeline graphs beyond structural checks.
// ABOUTME: Verifies handler registration, condition syntax, and node attribute types.

func ValidateSemantic(g *Graph, registry *HandlerRegistry) error {
    ve := &ValidationError{}
    validateHandlerRegistration(g, registry, ve)
    validateConditionSyntax(g, ve)
    validateNodeAttributes(g, ve)
    if ve.hasErrors() {
        return ve
    }
    return nil
}
```

- `validateHandlerRegistration`: for each node (skip start/exit), check `registry.Get(node.Handler) != nil`
- `validateConditionSyntax`: for each edge with Condition, try to parse/compile it (use a dry-run of EvaluateCondition with empty context — if it panics/errors on parse, report)
- `validateNodeAttributes`: check `max_retries` is parseable as int if present

Add `RegisteredHandlers() []string` to HandlerRegistry for introspection, and expose a `Has(name string) bool` method.

**Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run TestValidateSemantic -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/validate_semantic.go pipeline/validate_semantic_test.go pipeline/handler.go
git commit -m "feat(pipeline): add semantic validation for handlers and conditions"
```

---

### Task 4: Subagent Spawning

Adds a built-in tool that allows the agent to spawn child agent sessions for subtasks. The child runs to completion and returns its output as the tool result.

**Files:**
- Create: `agent/tools/spawn.go`
- Create: `agent/tools/spawn_test.go`
- Modify: `agent/session.go` — register spawn tool when environment is set

**Step 1: Write the failing test**

```go
// agent/tools/spawn_test.go
func TestSpawnAgentTool_Name(t *testing.T) {
    tool := NewSpawnAgentTool(nil, agent.SessionConfig{})
    if tool.Name() != "spawn_agent" {
        t.Fatalf("expected spawn_agent, got %s", tool.Name())
    }
}
```

Additional tests:
- `TestSpawnAgentTool_Parameters` — JSON schema has task, system_prompt, max_turns fields
- `TestSpawnAgentTool_Execute` — spawns child session, returns result text
- `TestSpawnAgentTool_ChildInheritsEnvironment` — child gets parent's execution env
- `TestSpawnAgentTool_MaxTurnsDefault` — defaults to 10 if not specified
- `TestSpawnAgentTool_ContextCancellation` — respects parent context cancellation
- `TestSpawnAgentTool_ErrorPropagation` — child errors bubble up as tool error

**Step 2: Run test to verify it fails**

Run: `go test ./agent/tools/ -run TestSpawnAgent -v`
Expected: FAIL — `NewSpawnAgentTool` not defined

**Step 3: Write minimal implementation**

```go
// agent/tools/spawn.go
// ABOUTME: Built-in tool that spawns a child agent session for delegated subtasks.
// ABOUTME: The child runs to completion and its final text output becomes the tool result.

type SpawnAgentTool struct {
    client     agent.Completer
    baseConfig agent.SessionConfig
    env        exec.ExecutionEnvironment
}

func NewSpawnAgentTool(client agent.Completer, baseConfig agent.SessionConfig, env exec.ExecutionEnvironment) *SpawnAgentTool

func (t *SpawnAgentTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
    // Parse params: task (required), system_prompt (optional), max_turns (optional, default 10)
    // Create child SessionConfig from baseConfig with overrides
    // Create child Session with NewSession(t.client, childConfig, WithEnvironment(t.env))
    // Run child session with task as input
    // Return final text from child session result
}
```

Note: The spawn tool needs access to the `agent` package's `Completer` interface. To avoid circular imports (agent/tools → agent), define a `SessionRunner` interface in the tools package:

```go
type SessionRunner interface {
    RunChild(ctx context.Context, task string, systemPrompt string, maxTurns int) (string, error)
}
```

Pass a closure from Session that creates and runs child sessions.

**Step 4: Run test to verify it passes**

Run: `go test ./agent/tools/ -run TestSpawnAgent -v`
Expected: PASS

**Step 5: Commit**

```bash
git add agent/tools/spawn.go agent/tools/spawn_test.go agent/session.go
git commit -m "feat(agent): add spawn_agent tool for child session delegation"
```

---

### Task 5: Subgraph Support in Pipeline

Adds a `subgraph` node shape that executes a referenced sub-pipeline inline. Uses the existing handler interface with recursive engine execution.

**Files:**
- Create: `pipeline/subgraph.go`
- Create: `pipeline/subgraph_test.go`
- Modify: `pipeline/graph.go` — add `"subgraph"` shape to handler map, add `SubgraphRef` field to Node
- Modify: `pipeline/engine.go` — add `SubGraphs` field to EngineConfig for graph registry

**Step 1: Write the failing test**

```go
// pipeline/subgraph_test.go
func TestSubgraphHandler_Execute(t *testing.T) {
    // Build a sub-pipeline: start -> step -> exit
    subGraph := buildSimpleGraph("sub")

    // Build main pipeline: start -> subgraph_node -> exit
    // subgraph_node has shape "subgraph", attr subgraph_ref="sub"
    mainGraph := buildGraphWithSubgraphNode("sub")

    registry := NewHandlerRegistry()
    registry.Register("start", StartHandler{})
    registry.Register("exit", ExitHandler{})
    registry.Register("subgraph", NewSubgraphHandler(map[string]*Graph{"sub": subGraph}, registry))

    engine := NewEngine(mainGraph, registry)
    result, err := engine.Run(ctx, nil)
    if err != nil {
        t.Fatal(err)
    }
    if result.Status != "completed" {
        t.Fatalf("expected completed, got %s", result.Status)
    }
}
```

Additional tests:
- `TestSubgraphHandler_ContextPropagation` — child context merges back to parent
- `TestSubgraphHandler_MissingSubgraph` — returns error if referenced graph not found
- `TestSubgraphHandler_NestedSubgraphs` — subgraph containing another subgraph (2 levels)
- `TestSubgraphHandler_SubgraphFailure` — child failure propagates as OutcomeFail
- `TestSubgraphHandler_CheckpointIntegration` — completed subgraph nodes are checkpointed

**Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestSubgraph -v`
Expected: FAIL — `NewSubgraphHandler` not defined

**Step 3: Write minimal implementation**

```go
// pipeline/subgraph.go
// ABOUTME: Handler that executes a referenced sub-pipeline as a single node step.
// ABOUTME: Enables composition of pipelines via the "subgraph" node shape.

type SubgraphHandler struct {
    graphs   map[string]*Graph
    registry *HandlerRegistry
}

func NewSubgraphHandler(graphs map[string]*Graph, registry *HandlerRegistry) *SubgraphHandler

func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    ref := node.Attrs["subgraph_ref"]
    subGraph, ok := h.graphs[ref]
    if !ok {
        return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
    }

    // Create sub-engine and run.
    engine := NewEngine(subGraph, h.registry)
    result, err := engine.Run(ctx, pctx.Snapshot())
    if err != nil {
        return Outcome{Status: OutcomeFail}, err
    }

    // Merge context from sub-pipeline back to parent.
    return Outcome{
        Status:         result.Status,
        ContextUpdates: result.Context,
    }, nil
}
```

Add to graph.go `shapeHandlerMap`: `"tab": "subgraph"` (tab shape = subgraph in DOT).
Add `SubgraphRef string` field to `Node` struct (populated from attrs during parse).

**Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run TestSubgraph -v`
Expected: PASS

**Step 5: Commit**

```bash
git add pipeline/subgraph.go pipeline/subgraph_test.go pipeline/graph.go pipeline/engine.go
git commit -m "feat(pipeline): add subgraph node type for nested pipeline execution"
```
