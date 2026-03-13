# Tool Result Cache Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate redundant tool executions within agent sessions by caching results of read-only tools and invalidating the cache on mutating tool calls.

**Architecture:** An optional `CachePolicyProvider` interface allows tools to declare themselves cacheable or mutating without changing the existing `Tool` interface. A per-session `toolCache` struct stores results keyed on `(toolName, argsJSON)`. The cache is consulted before tool execution in `session.go` and invalidated when any mutating tool runs. The feature is opt-in via DOT attribute `cache_tool_results="true"`.

**Tech Stack:** Go, no new dependencies

---

## Chunk 1: Cache Policy Interface and Tool Implementations

### Task 1: CachePolicyProvider interface and types

**Files:**
- Modify: `agent/tools/registry.go`
- Test: `agent/tools/registry_test.go`

- [ ] **Step 1: Write failing test for CachePolicy types**

In `agent/tools/registry_test.go`, add:

```go
func TestCachePolicyProviderInterface(t *testing.T) {
	// Verify the CachePolicy constants exist and have distinct values.
	if CachePolicyNone == CachePolicyCacheable {
		t.Fatal("CachePolicyNone and CachePolicyCacheable must differ")
	}
	if CachePolicyCacheable == CachePolicyMutating {
		t.Fatal("CachePolicyCacheable and CachePolicyMutating must differ")
	}
}

func TestGetCachePolicy_DefaultsToNone(t *testing.T) {
	// A tool that does not implement CachePolicyProvider should return CachePolicyNone.
	policy := GetCachePolicy(mockTool{})
	if policy != CachePolicyNone {
		t.Errorf("expected CachePolicyNone, got %d", policy)
	}
}

func TestGetCachePolicy_RespectsProvider(t *testing.T) {
	policy := GetCachePolicy(cacheableMockTool{})
	if policy != CachePolicyCacheable {
		t.Errorf("expected CachePolicyCacheable, got %d", policy)
	}
}

// mockTool does NOT implement CachePolicyProvider.
type mockTool struct{}
func (m mockTool) Name() string                                              { return "mock" }
func (m mockTool) Description() string                                       { return "mock tool" }
func (m mockTool) Parameters() json.RawMessage                               { return json.RawMessage(`{}`) }
func (m mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "ok", nil }

// cacheableMockTool implements CachePolicyProvider.
type cacheableMockTool struct{ mockTool }
func (c cacheableMockTool) CachePolicy() CachePolicy { return CachePolicyCacheable }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/tools/ -run TestCachePolicy -v`
Expected: Compilation errors — types don't exist yet.

- [ ] **Step 3: Implement CachePolicy types and GetCachePolicy**

In `agent/tools/registry.go`, add after the `Tool` interface (around line 20):

```go
// CachePolicy describes how a tool's results interact with the session cache.
type CachePolicy int

const (
	// CachePolicyNone means the tool is not cached and does not affect the cache.
	CachePolicyNone CachePolicy = iota
	// CachePolicyCacheable means identical arguments produce identical results.
	CachePolicyCacheable
	// CachePolicyMutating means the tool has side effects; invalidates all cached results.
	CachePolicyMutating
)

// CachePolicyProvider is an optional interface tools can implement to declare
// their caching behavior. Tools that don't implement it default to CachePolicyNone.
type CachePolicyProvider interface {
	CachePolicy() CachePolicy
}

// GetCachePolicy returns the CachePolicy for a tool. If the tool does not
// implement CachePolicyProvider, returns CachePolicyNone.
func GetCachePolicy(t Tool) CachePolicy {
	if cp, ok := t.(CachePolicyProvider); ok {
		return cp.CachePolicy()
	}
	return CachePolicyNone
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/tools/ -run TestCachePolicy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/tools/registry.go agent/tools/registry_test.go
git commit -m "feat(tools): add CachePolicyProvider optional interface"
```

---

### Task 2: Implement CachePolicy on cacheable tools (read, glob, grep_search)

**Files:**
- Modify: `agent/tools/read.go`
- Modify: `agent/tools/glob.go`
- Modify: `agent/tools/grep.go`
- Test: `agent/tools/registry_test.go`

- [ ] **Step 1: Write failing test**

In `agent/tools/registry_test.go`, add:

```go
func TestBuiltinTools_CachePolicy(t *testing.T) {
	// These tools should be cacheable.
	cacheableTools := []Tool{
		&ReadTool{},
		&GlobTool{},
		&GrepSearchTool{},
	}
	for _, tool := range cacheableTools {
		policy := GetCachePolicy(tool)
		if policy != CachePolicyCacheable {
			t.Errorf("%s: expected CachePolicyCacheable, got %d", tool.Name(), policy)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/tools/ -run TestBuiltinTools_CachePolicy -v`
Expected: FAIL — tools don't implement CachePolicyProvider yet.

- [ ] **Step 3: Add CachePolicy() to read, glob, grep_search**

In `agent/tools/read.go`, add:
```go
func (t *ReadTool) CachePolicy() CachePolicy { return CachePolicyCacheable }
```

In `agent/tools/glob.go`, add:
```go
func (t *GlobTool) CachePolicy() CachePolicy { return CachePolicyCacheable }
```

In `agent/tools/grep.go`, add:
```go
func (t *GrepSearchTool) CachePolicy() CachePolicy { return CachePolicyCacheable }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/tools/ -run TestBuiltinTools_CachePolicy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/tools/read.go agent/tools/glob.go agent/tools/grep.go agent/tools/registry_test.go
git commit -m "feat(tools): mark read, glob, grep_search as cacheable"
```

---

### Task 3: Implement CachePolicy on mutating tools (bash, write, edit, apply_patch, spawn_agent)

**Files:**
- Modify: `agent/tools/bash.go`
- Modify: `agent/tools/write.go`
- Modify: `agent/tools/edit.go`
- Modify: `agent/tools/apply_patch.go`
- Modify: `agent/tools/spawn.go`
- Test: `agent/tools/registry_test.go`

- [ ] **Step 1: Write failing test**

In `agent/tools/registry_test.go`, add:

```go
func TestBuiltinTools_MutatingPolicy(t *testing.T) {
	mutatingTools := []Tool{
		&BashTool{},
		&WriteTool{},
		&EditTool{},
		&ApplyPatchTool{},
		&SpawnAgentTool{},
	}
	for _, tool := range mutatingTools {
		policy := GetCachePolicy(tool)
		if policy != CachePolicyMutating {
			t.Errorf("%s: expected CachePolicyMutating, got %d", tool.Name(), policy)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/tools/ -run TestBuiltinTools_MutatingPolicy -v`
Expected: FAIL

- [ ] **Step 3: Add CachePolicy() to mutating tools**

In `agent/tools/bash.go`:
```go
func (t *BashTool) CachePolicy() CachePolicy { return CachePolicyMutating }
```

In `agent/tools/write.go`:
```go
func (t *WriteTool) CachePolicy() CachePolicy { return CachePolicyMutating }
```

In `agent/tools/edit.go`:
```go
func (t *EditTool) CachePolicy() CachePolicy { return CachePolicyMutating }
```

In `agent/tools/apply_patch.go`:
```go
func (t *ApplyPatchTool) CachePolicy() CachePolicy { return CachePolicyMutating }
```

In `agent/tools/spawn.go`:
```go
func (t *SpawnAgentTool) CachePolicy() CachePolicy { return CachePolicyMutating }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/tools/ -run TestBuiltinTools_MutatingPolicy -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/tools/bash.go agent/tools/write.go agent/tools/edit.go agent/tools/apply_patch.go agent/tools/spawn.go agent/tools/registry_test.go
git commit -m "feat(tools): mark bash, write, edit, apply_patch, spawn_agent as mutating"
```

---

## Chunk 2: Tool Cache Struct and Session Integration

### Task 4: toolCache struct

**Files:**
- Create: `agent/tool_cache.go`
- Create: `agent/tool_cache_test.go`

- [ ] **Step 1: Write failing tests for toolCache**

Create `agent/tool_cache_test.go`:

```go
package agent

import "testing"

func TestToolCache_MissOnEmpty(t *testing.T) {
	c := newToolCache()
	_, hit := c.get("read", `{"path":"main.go"}`)
	if hit {
		t.Fatal("expected miss on empty cache")
	}
	if c.misses != 1 {
		t.Errorf("expected 1 miss, got %d", c.misses)
	}
}

func TestToolCache_HitAfterStore(t *testing.T) {
	c := newToolCache()
	c.store("read", `{"path":"main.go"}`, "file contents")

	result, hit := c.get("read", `{"path":"main.go"}`)
	if !hit {
		t.Fatal("expected cache hit")
	}
	if result != "file contents" {
		t.Errorf("expected 'file contents', got %q", result)
	}
	if c.hits != 1 {
		t.Errorf("expected 1 hit, got %d", c.hits)
	}
}

func TestToolCache_MissOnDifferentArgs(t *testing.T) {
	c := newToolCache()
	c.store("read", `{"path":"main.go"}`, "file contents")

	_, hit := c.get("read", `{"path":"other.go"}`)
	if hit {
		t.Fatal("expected miss for different args")
	}
}

func TestToolCache_MissOnDifferentTool(t *testing.T) {
	c := newToolCache()
	c.store("read", `{"path":"main.go"}`, "file contents")

	_, hit := c.get("glob", `{"path":"main.go"}`)
	if hit {
		t.Fatal("expected miss for different tool name")
	}
}

func TestToolCache_InvalidateAll(t *testing.T) {
	c := newToolCache()
	c.store("read", `{"path":"a.go"}`, "a")
	c.store("glob", `{"pattern":"*.go"}`, "b")

	c.invalidateAll()

	_, hit := c.get("read", `{"path":"a.go"}`)
	if hit {
		t.Fatal("expected miss after invalidation")
	}
	_, hit = c.get("glob", `{"pattern":"*.go"}`)
	if hit {
		t.Fatal("expected miss after invalidation")
	}
}

func TestToolCache_StatsPreservedAfterInvalidation(t *testing.T) {
	c := newToolCache()
	c.store("read", `{"path":"a.go"}`, "a")
	c.get("read", `{"path":"a.go"}`) // hit
	c.get("read", `{"path":"b.go"}`) // miss

	c.invalidateAll()

	// Stats should survive invalidation.
	if c.hits != 1 {
		t.Errorf("expected 1 hit, got %d", c.hits)
	}
	if c.misses != 1 {
		t.Errorf("expected 1 miss, got %d", c.misses)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestToolCache -v`
Expected: Compilation errors — `newToolCache` doesn't exist.

- [ ] **Step 3: Implement toolCache**

Create `agent/tool_cache.go`:

```go
// ABOUTME: Per-session cache for tool results, keyed on (tool name, arguments JSON).
// ABOUTME: Supports store, get, invalidateAll, and tracks hit/miss stats.
package agent

type cacheKey struct {
	toolName string
	argsJSON string
}

type toolCache struct {
	results map[cacheKey]string
	hits    int
	misses  int
}

func newToolCache() *toolCache {
	return &toolCache{
		results: make(map[cacheKey]string),
	}
}

// get looks up a cached result. Returns the result and true on hit,
// or empty string and false on miss. Updates hit/miss counters.
func (c *toolCache) get(toolName, argsJSON string) (string, bool) {
	key := cacheKey{toolName: toolName, argsJSON: argsJSON}
	if result, ok := c.results[key]; ok {
		c.hits++
		return result, true
	}
	c.misses++
	return "", false
}

// store saves a tool result in the cache.
func (c *toolCache) store(toolName, argsJSON, result string) {
	key := cacheKey{toolName: toolName, argsJSON: argsJSON}
	c.results[key] = result
}

// invalidateAll clears all cached results but preserves hit/miss stats.
func (c *toolCache) invalidateAll() {
	clear(c.results)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestToolCache -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/tool_cache.go agent/tool_cache_test.go
git commit -m "feat(agent): add toolCache struct for per-session tool result caching"
```

---

### Task 5: Add CacheToolResults to SessionConfig

**Files:**
- Modify: `agent/config.go`
- Modify: `agent/config_test.go` (if it exists, create if not)

- [ ] **Step 1: Check for existing config test file**

Run: `ls /Users/harper/Public/src/2389/tracker/agent/config_test.go 2>/dev/null || echo "no test file"`

- [ ] **Step 2: Write failing test**

In the config test file (create if needed as `agent/config_test.go`):

```go
func TestDefaultConfig_CacheToolResultsIsFalse(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.CacheToolResults {
		t.Fatal("CacheToolResults should default to false")
	}
}

func TestValidate_CacheToolResultsAcceptsBothValues(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	if err := cfg.Validate(); err != nil {
		t.Errorf("CacheToolResults=true should be valid: %v", err)
	}
	cfg.CacheToolResults = false
	if err := cfg.Validate(); err != nil {
		t.Errorf("CacheToolResults=false should be valid: %v", err)
	}
}
```

- [ ] **Step 3: Add CacheToolResults field to SessionConfig**

In `agent/config.go`, add the field to the `SessionConfig` struct:

```go
CacheToolResults bool
```

No change to `DefaultConfig()` needed (zero value `false` is correct default). No validation needed (bool is always valid).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestDefaultConfig_CacheToolResults -v && go test ./agent/ -run TestValidate_CacheToolResults -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/config.go agent/config_test.go
git commit -m "feat(agent): add CacheToolResults to SessionConfig"
```

---

### Task 6: Add EventToolCacheHit event type

**Files:**
- Modify: `agent/events.go`
- Test: (compile check is sufficient — event types are constants)

- [ ] **Step 1: Add EventToolCacheHit constant**

In `agent/events.go`, add to the const block after `EventSteeringInjected`:

```go
EventToolCacheHit EventType = "tool_cache_hit"
```

- [ ] **Step 2: Verify compilation**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./agent/`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add agent/events.go
git commit -m "feat(agent): add EventToolCacheHit event type"
```

---

### Task 7: Wire cache into session.go tool execution loop

**Files:**
- Modify: `agent/session.go`
- Test: `agent/session_test.go`

This is the core integration. The session needs to:
1. Create a `toolCache` if `config.CacheToolResults` is true.
2. Before executing a tool, check the cache.
3. After executing, store cacheable results.
4. On mutating tool execution, invalidate the cache.

- [ ] **Step 1: Write failing integration tests**

In `agent/session_test.go`, add these test helpers first:

```go
// countingReadTool tracks how many times Execute is called.
type countingReadTool struct {
	count *int
}

func (t *countingReadTool) Name() string               { return "read" }
func (t *countingReadTool) Description() string         { return "counting read" }
func (t *countingReadTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *countingReadTool) CachePolicy() tools.CachePolicy {
	return tools.CachePolicyCacheable
}
func (t *countingReadTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	*t.count++
	return "file contents", nil
}

// noopMutatingTool is a mutating tool that does nothing.
type noopMutatingTool struct{}

func (t *noopMutatingTool) Name() string               { return "noop_write" }
func (t *noopMutatingTool) Description() string         { return "mutating noop" }
func (t *noopMutatingTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *noopMutatingTool) CachePolicy() tools.CachePolicy {
	return tools.CachePolicyMutating
}
func (t *noopMutatingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "wrote", nil
}
```

Then add the tests. Note: these use `mockCompleter` (existing pattern), `"tool_calls"` finish reason, `"stop"` for end-of-turn, and unique tool call IDs per response:

```go
func TestSession_ToolCacheHit(t *testing.T) {
	// Mock LLM that calls read twice with same args, then stops.
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if callCount != 1 {
		t.Errorf("expected read to execute once (second call cached), got %d", callCount)
	}
}

func TestSession_CacheInvalidatedByMutatingTool(t *testing.T) {
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}
	writeTool := &noopMutatingTool{}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "noop_write",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_3",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool, writeTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// read called twice: once initially, once after write invalidated cache.
	if callCount != 2 {
		t.Errorf("expected read to execute twice (invalidated by write), got %d", callCount)
	}
}

func TestSession_NoCacheWhenDisabled(t *testing.T) {
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = false // disabled
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Errorf("expected read to execute twice (no cache), got %d", callCount)
	}
}

func TestSession_CacheNotStoredOnError(t *testing.T) {
	// A tool that fails on first call, succeeds on second.
	callCount := 0
	failOnceTool := &failOnceReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(failOnceTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// Both calls should execute: first returns error (not cached), second succeeds.
	if callCount != 2 {
		t.Errorf("expected 2 executions (error result not cached), got %d", callCount)
	}
}

func TestSession_BatchToolCallsWithMidBatchInvalidation(t *testing.T) {
	// LLM returns multiple tool calls in one response: read, write, read (same args).
	// The write in the middle should invalidate cache, so the second read executes.
	readCount := 0
	countingTool := &countingReadTool{count: &readCount}
	writeTool := &noopMutatingTool{}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_1",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"main.go"}`),
							},
						},
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_2",
								Name:      "noop_write",
								Arguments: json.RawMessage(`{}`),
							},
						},
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_3",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"main.go"}`),
							},
						},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool, writeTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// Both reads should execute: first populates cache, write invalidates, second re-executes.
	if readCount != 2 {
		t.Errorf("expected 2 read executions (mid-batch invalidation), got %d", readCount)
	}
}
```

Also add this extra test helper:

```go
// failOnceReadTool fails on first call, succeeds on subsequent calls.
type failOnceReadTool struct {
	count *int
}

func (t *failOnceReadTool) Name() string               { return "read" }
func (t *failOnceReadTool) Description() string         { return "fail-once read" }
func (t *failOnceReadTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *failOnceReadTool) CachePolicy() tools.CachePolicy {
	return tools.CachePolicyCacheable
}
func (t *failOnceReadTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	*t.count++
	if *t.count == 1 {
		return "", fmt.Errorf("file not found")
	}
	return "file contents", nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run "TestSession_ToolCache|TestSession_CacheInvalidated|TestSession_NoCache|TestSession_CacheNotStored|TestSession_BatchToolCalls" -v`
Expected: Compilation errors or FAIL — session doesn't have cache logic.

- [ ] **Step 3: Implement cache integration in session.go**

In `agent/session.go`, make these changes:

1. Add `cache *toolCache` field to `Session` struct (after `ran bool`):
```go
cache *toolCache
```

2. In `NewSession`, after the line `s.ran = false` (or at end of initialization before returning), add:
```go
if s.config.CacheToolResults {
	s.cache = newToolCache()
}
```

3. In the `Run` method's tool execution loop (around line 261-283), replace the current tool execution block with cache-aware logic. Replace:

```go
for _, call := range toolCalls {
	s.emit(Event{
		Type:      EventToolCallStart,
		SessionID: s.id,
		ToolName:  call.Name,
		ToolInput: string(call.Arguments),
	})

	toolResult := s.registry.Execute(ctx, call)
	result.ToolCalls[call.Name]++

	s.emit(Event{
		Type:       EventToolCallEnd,
		SessionID:  s.id,
		ToolName:   call.Name,
		ToolOutput: toolResult.Content,
		ToolError:  boolToErrStr(toolResult.IsError),
	})

	toolResults = append(toolResults, llm.ContentPart{
		Kind:       llm.KindToolResult,
		ToolResult: &toolResult,
	})
}
```

With:

```go
for _, call := range toolCalls {
	s.emit(Event{
		Type:      EventToolCallStart,
		SessionID: s.id,
		ToolName:  call.Name,
		ToolInput: string(call.Arguments),
	})

	// Determine cache policy for this tool.
	tool := s.registry.Get(call.Name)
	policy := tools.CachePolicyNone
	if tool != nil {
		policy = tools.GetCachePolicy(tool)
	}

	// Check cache before executing.
	var toolResult llm.ToolResultData
	cacheHit := false
	if s.cache != nil && policy == tools.CachePolicyCacheable {
		if cached, hit := s.cache.get(call.Name, string(call.Arguments)); hit {
			toolResult = llm.ToolResultData{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    cached,
				IsError:    false,
			}
			cacheHit = true
			s.emit(Event{
				Type:      EventToolCacheHit,
				SessionID: s.id,
				ToolName:  call.Name,
				ToolInput: string(call.Arguments),
			})
		}
	}

	// Execute if no cache hit.
	if !cacheHit {
		toolResult = s.registry.Execute(ctx, call)

		// Invalidate cache on mutating tool.
		if s.cache != nil && policy == tools.CachePolicyMutating {
			s.cache.invalidateAll()
		}

		// Cache successful results from cacheable tools.
		if s.cache != nil && policy == tools.CachePolicyCacheable && !toolResult.IsError {
			s.cache.store(call.Name, string(call.Arguments), toolResult.Content)
		}
	}

	result.ToolCalls[call.Name]++

	s.emit(Event{
		Type:       EventToolCallEnd,
		SessionID:  s.id,
		ToolName:   call.Name,
		ToolOutput: toolResult.Content,
		ToolError:  boolToErrStr(toolResult.IsError),
	})

	toolResults = append(toolResults, llm.ContentPart{
		Kind:       llm.KindToolResult,
		ToolResult: &toolResult,
	})
}
```

Note: `s.registry.Get(name)` must exist. Check `agent/tools/registry.go` for a `Get` method. If it doesn't exist, add one:

```go
// Get returns the tool with the given name, or nil if not found.
func (r *Registry) Get(name string) Tool {
	t, ok := r.tools[name]
	if !ok {
		return nil
	}
	return t
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run "TestSession_ToolCache|TestSession_CacheInvalidated|TestSession_NoCache|TestSession_CacheNotStored|TestSession_BatchToolCalls" -v`
Expected: All five tests PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -v`
Expected: All existing tests still pass.

- [ ] **Step 6: Commit**

```bash
git add agent/session.go agent/session_test.go agent/tools/registry.go
git commit -m "feat(agent): wire tool result cache into session loop"
```

---

## Chunk 3: DOT Attribute Plumbing and Validation

### Task 8: Wire DOT attribute through codergen.go

**Files:**
- Modify: `pipeline/handlers/codergen.go`
- Test: `pipeline/handlers/codergen_test.go`

- [ ] **Step 1: Write failing test**

In `pipeline/handlers/codergen_test.go`, add a test that verifies `buildConfig` reads `cache_tool_results` from both graph-level and node-level attributes:

```go
func TestBuildConfig_CacheToolResults_FromGraphAttrs(t *testing.T) {
	h := &CodergenHandler{
		graphAttrs: map[string]string{"cache_tool_results": "true"},
	}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	config := h.buildConfig(node)
	if !config.CacheToolResults {
		t.Error("expected CacheToolResults=true from graph attrs")
	}
}

func TestBuildConfig_CacheToolResults_NodeOverridesGraph(t *testing.T) {
	h := &CodergenHandler{
		graphAttrs: map[string]string{"cache_tool_results": "true"},
	}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"cache_tool_results": "false"},
	}
	config := h.buildConfig(node)
	if config.CacheToolResults {
		t.Error("expected CacheToolResults=false (node override)")
	}
}

func TestBuildConfig_CacheToolResults_DefaultFalse(t *testing.T) {
	h := &CodergenHandler{}
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	config := h.buildConfig(node)
	if config.CacheToolResults {
		t.Error("expected CacheToolResults=false by default")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/handlers/ -run TestBuildConfig_CacheToolResults -v`
Expected: FAIL — `buildConfig` doesn't read the attribute yet.

- [ ] **Step 3: Add attribute reading to buildConfig**

In `pipeline/handlers/codergen.go`, add to the `buildConfig` method (after the existing attribute reads):

```go
// Cache tool results: graph-level default, node-level override.
if v, ok := h.graphAttrs["cache_tool_results"]; ok && v == "true" {
	config.CacheToolResults = true
}
if v, ok := node.Attrs["cache_tool_results"]; ok {
	config.CacheToolResults = (v == "true")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/handlers/ -run TestBuildConfig_CacheToolResults -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/codergen.go pipeline/handlers/codergen_test.go
git commit -m "feat(pipeline): wire cache_tool_results DOT attribute to session config"
```

---

### Task 9: Add cache_tool_results to semantic validation

**Files:**
- Modify: `pipeline/validate_semantic.go`
- Modify: `pipeline/validate_semantic_test.go`

- [ ] **Step 1: Write failing test**

In `pipeline/validate_semantic_test.go`, add. Note: use `NewGraph`/`AddNode` pattern (not direct struct construction):

```go
func TestValidateNodeAttributes_CacheToolResults_Valid(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "box", Attrs: map[string]string{"cache_tool_results": "true"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "e"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "start"})
	reg.Register(&semanticStubHandler{name: "exit"})
	reg.Register(&semanticStubHandler{name: "codergen"})

	err := ValidateSemantic(g, reg)
	if err != nil {
		t.Errorf("cache_tool_results='true' should be valid, got: %v", err)
	}
}

func TestValidateNodeAttributes_CacheToolResults_Invalid(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "box", Attrs: map[string]string{"cache_tool_results": "banana"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "e"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "start"})
	reg.Register(&semanticStubHandler{name: "exit"})
	reg.Register(&semanticStubHandler{name: "codergen"})

	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Error("cache_tool_results='banana' should be invalid")
	}
	if err != nil && !strings.Contains(err.Error(), "cache_tool_results") {
		t.Errorf("error should mention cache_tool_results, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/ -run TestValidateNodeAttributes_CacheToolResults -v`
Expected: FAIL — "banana" is not validated.

- [ ] **Step 3: Add validation**

In `pipeline/validate_semantic.go`, add to `validateNodeAttributes` (inside the `for _, node := range g.Nodes` loop, after the `max_retries` check):

```go
if v, ok := node.Attrs["cache_tool_results"]; ok {
	if v != "true" && v != "false" {
		ve.add(fmt.Sprintf("node %q has invalid cache_tool_results %q: must be \"true\" or \"false\"", node.ID, v))
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/ -run TestValidateNodeAttributes_CacheToolResults -v`
Expected: PASS

- [ ] **Step 5: Run full pipeline test suite**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/ -v`
Expected: All pass

- [ ] **Step 6: Commit**

```bash
git add pipeline/validate_semantic.go pipeline/validate_semantic_test.go
git commit -m "feat(pipeline): validate cache_tool_results DOT attribute"
```

---

### Task 10: Full integration test and final verification

**Files:**
- Test: (run full suite)

- [ ] **Step 1: Run the complete test suite**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./... -v`
Expected: All tests pass, no regressions.

- [ ] **Step 2: Build the binary**

Run: `cd /Users/harper/Public/src/2389/tracker && go build ./cmd/tracker/`
Expected: Compiles successfully.

- [ ] **Step 3: Verify with a sample DOT file**

Create a quick test DOT file to confirm the attribute is accepted:

```bash
cat > /tmp/test-cache.dot << 'EOF'
digraph test {
  graph [
    goal="test cache",
    cache_tool_results="true"
  ];
  Start [shape=Mdiamond];
  Exit [shape=Msquare];
  Agent [
    shape=box,
    label="Test Agent",
    llm_provider="anthropic",
    llm_model="claude-sonnet-4-6",
    prompt="Say hello"
  ];
  Start -> Agent;
  Agent -> Exit;
}
EOF
```

Run: `cd /Users/harper/Public/src/2389/tracker && go run ./cmd/tracker/ /tmp/test-cache.dot --no-tui 2>&1 | head -5`
Expected: Pipeline starts without validation errors about `cache_tool_results`.

- [ ] **Step 4: Final commit if any cleanup needed**

```bash
git add -A && git status
# Only commit if there are changes
```
