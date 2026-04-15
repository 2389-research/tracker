// ABOUTME: Tests for the pipeline context thread-safe key-value store.
// ABOUTME: Validates Get, Set, Merge, Snapshot, and concurrent access safety.
package pipeline

import (
	"sync"
	"testing"
)

func TestContextSetAndGet(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("key1", "value1")

	val, ok := ctx.Get("key1")
	if !ok {
		t.Fatal("expected key1 to exist")
	}
	if val != "value1" {
		t.Errorf("expected 'value1', got %q", val)
	}
}

func TestContextGetMissing(t *testing.T) {
	ctx := NewPipelineContext()
	_, ok := ctx.Get("nonexistent")
	if ok {
		t.Error("expected key to not exist")
	}
}

func TestContextMerge(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("existing", "old")

	ctx.Merge(map[string]string{
		"existing": "new",
		"added":    "fresh",
	})

	val, _ := ctx.Get("existing")
	if val != "new" {
		t.Errorf("expected 'new', got %q", val)
	}

	val, _ = ctx.Get("added")
	if val != "fresh" {
		t.Errorf("expected 'fresh', got %q", val)
	}
}

func TestContextMergeNil(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("key", "val")
	ctx.Merge(nil)

	val, ok := ctx.Get("key")
	if !ok || val != "val" {
		t.Error("merge of nil should not affect existing values")
	}
}

func TestContextSnapshot(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "1")
	ctx.Set("b", "2")

	snap := ctx.Snapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 entries in snapshot, got %d", len(snap))
	}
	if snap["a"] != "1" || snap["b"] != "2" {
		t.Errorf("snapshot values incorrect: %v", snap)
	}

	// Mutating the snapshot should not affect the context.
	snap["a"] = "mutated"
	val, _ := ctx.Get("a")
	if val != "1" {
		t.Error("mutating snapshot should not affect context")
	}
}

func TestContextBuiltInKeys(t *testing.T) {
	if ContextKeyOutcome != "outcome" {
		t.Errorf("expected ContextKeyOutcome='outcome', got %q", ContextKeyOutcome)
	}
	if ContextKeyPreferredLabel != "preferred_label" {
		t.Errorf("expected ContextKeyPreferredLabel='preferred_label', got %q", ContextKeyPreferredLabel)
	}
	if ContextKeyGoal != "graph.goal" {
		t.Errorf("expected ContextKeyGoal='graph.goal', got %q", ContextKeyGoal)
	}
	if ContextKeyLastResponse != "last_response" {
		t.Errorf("expected ContextKeyLastResponse='last_response', got %q", ContextKeyLastResponse)
	}
}

func TestContextConcurrentAccess(t *testing.T) {
	ctx := NewPipelineContext()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			ctx.Set("key", "value")
		}(i)
		go func(n int) {
			defer wg.Done()
			ctx.Get("key")
		}(i)
	}

	wg.Wait()
}

func TestContextDiffFromNewKeys(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "1")
	ctx.Set("b", "2")

	baseline := map[string]string{"a": "1"}
	diff := ctx.DiffFrom(baseline)
	if len(diff) != 1 || diff["b"] != "2" {
		t.Errorf("expected {b:2}, got %v", diff)
	}
}

func TestContextDiffFromChangedKeys(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "new")

	baseline := map[string]string{"a": "old"}
	diff := ctx.DiffFrom(baseline)
	if len(diff) != 1 || diff["a"] != "new" {
		t.Errorf("expected {a:new}, got %v", diff)
	}
}

func TestContextDiffFromUnchangedExcluded(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("a", "same")

	baseline := map[string]string{"a": "same"}
	diff := ctx.DiffFrom(baseline)
	if len(diff) != 0 {
		t.Errorf("expected empty diff, got %v", diff)
	}
}

func TestContextDiffFromEmptyBaseline(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("x", "1")
	ctx.Set("y", "2")

	diff := ctx.DiffFrom(map[string]string{})
	if len(diff) != 2 {
		t.Errorf("expected 2 entries, got %v", diff)
	}
}

func TestContextDiffFromEmptyContext(t *testing.T) {
	ctx := NewPipelineContext()
	baseline := map[string]string{"a": "1"}
	diff := ctx.DiffFrom(baseline)
	if len(diff) != 0 {
		t.Errorf("expected empty diff, got %v", diff)
	}
}

func TestContextSetInternal(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.SetInternal("retry_count.node1", "3")

	val, ok := ctx.GetInternal("retry_count.node1")
	if !ok {
		t.Fatal("expected internal key to exist")
	}
	if val != "3" {
		t.Errorf("expected '3', got %q", val)
	}

	// Internal keys should not appear in regular Get.
	_, ok = ctx.Get("retry_count.node1")
	if ok {
		t.Error("internal keys should not be visible via Get")
	}

	// Internal keys should not appear in Snapshot.
	snap := ctx.Snapshot()
	if _, found := snap["retry_count.node1"]; found {
		t.Error("internal keys should not appear in Snapshot")
	}
}

func TestNewPipelineContextFrom(t *testing.T) {
	values := map[string]string{"key1": "val1", "key2": "val2"}
	ctx := NewPipelineContextFrom(values)
	if v, ok := ctx.Get("key1"); !ok || v != "val1" {
		t.Errorf("key1 = %q, want %q", v, "val1")
	}
	if v, ok := ctx.Get("key2"); !ok || v != "val2" {
		t.Errorf("key2 = %q, want %q", v, "val2")
	}
	// Should not share state with input map
	values["key3"] = "val3"
	if _, ok := ctx.Get("key3"); ok {
		t.Error("context should not share state with input map")
	}
}

// --- Per-node scoping tests ---

func TestScopeToNodeBasic(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "hello from A")
	ctx.ScopeToNode("A")

	// Bare key still present.
	v, ok := ctx.Get("last_response")
	if !ok || v != "hello from A" {
		t.Errorf("bare last_response = %q, want %q", v, "hello from A")
	}
	// Scoped key created.
	v, ok = ctx.Get("node.A.last_response")
	if !ok || v != "hello from A" {
		t.Errorf("node.A.last_response = %q, want %q", v, "hello from A")
	}
}

func TestScopeToNodeLastWriterWins(t *testing.T) {
	ctx := NewPipelineContext()

	// Node A writes last_response.
	ctx.Set("last_response", "from A")
	ctx.ScopeToNode("A")

	// Node B overwrites last_response.
	ctx.Set("last_response", "from B")
	ctx.ScopeToNode("B")

	// Global key holds last writer's value.
	v, _ := ctx.Get("last_response")
	if v != "from B" {
		t.Errorf("global last_response = %q, want %q", v, "from B")
	}
	// Per-node keys preserve each node's original value.
	va, _ := ctx.Get("node.A.last_response")
	if va != "from A" {
		t.Errorf("node.A.last_response = %q, want %q", va, "from A")
	}
	vb, _ := ctx.Get("node.B.last_response")
	if vb != "from B" {
		t.Errorf("node.B.last_response = %q, want %q", vb, "from B")
	}
}

func TestScopeToNodeDirtyCleared(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("outcome", "success")
	ctx.ScopeToNode("NodeX")

	// Write a new key after scoping.
	ctx.Set("new_key", "new_val")
	ctx.ScopeToNode("NodeY")

	// NodeX should not have new_key in its namespace.
	if _, ok := ctx.Get("node.NodeX.new_key"); ok {
		t.Error("node.NodeX.new_key should not exist — it was written after ScopeToNode(NodeX)")
	}
	// NodeY should have new_key.
	v, ok := ctx.Get("node.NodeY.new_key")
	if !ok || v != "new_val" {
		t.Errorf("node.NodeY.new_key = %q, want %q", v, "new_val")
	}
}

func TestScopeToNodeMerge(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Merge(map[string]string{
		"tool_stdout": "hello",
		"tool_stderr": "warn",
	})
	ctx.ScopeToNode("ToolNode")

	if v, ok := ctx.Get("node.ToolNode.tool_stdout"); !ok || v != "hello" {
		t.Errorf("node.ToolNode.tool_stdout = %q, want %q", v, "hello")
	}
	if v, ok := ctx.Get("node.ToolNode.tool_stderr"); !ok || v != "warn" {
		t.Errorf("node.ToolNode.tool_stderr = %q, want %q", v, "warn")
	}
}

func TestGetScoped(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("last_response", "scoped value")
	ctx.ScopeToNode("Agent1")

	v, ok := ctx.GetScoped("Agent1", "last_response")
	if !ok || v != "scoped value" {
		t.Errorf("GetScoped(Agent1, last_response) = %q, want %q", v, "scoped value")
	}

	// Missing key returns false.
	_, ok = ctx.GetScoped("Agent1", "nonexistent")
	if ok {
		t.Error("GetScoped on nonexistent key should return false")
	}
}

func TestScopeToNodeNoOp(t *testing.T) {
	// ScopeToNode with no dirty keys should not panic and should leave context unchanged.
	ctx := NewPipelineContext()
	ctx.Set("existing", "val")
	ctx.ScopeToNode("PriorNode") // clears dirty for "existing"

	// Now scope again with nothing dirty.
	ctx.ScopeToNode("EmptyNode")

	if _, ok := ctx.Get("node.EmptyNode.existing"); ok {
		t.Error("node.EmptyNode.existing should not exist — key was not dirty during EmptyNode's execution")
	}
}

func TestScopeToNodeBackwardCompatConditions(t *testing.T) {
	// Conditions like ctx.outcome = success must still work after scoping.
	ctx := NewPipelineContext()
	ctx.Set(ContextKeyOutcome, "success")
	ctx.Set(ContextKeyLastResponse, "the response")
	ctx.ScopeToNode("SomeNode")

	// Bare keys still resolve correctly.
	if v, ok := ctx.Get(ContextKeyOutcome); !ok || v != "success" {
		t.Errorf("outcome = %q after scoping, want %q", v, "success")
	}
	if v, ok := ctx.Get(ContextKeyLastResponse); !ok || v != "the response" {
		t.Errorf("last_response = %q after scoping, want %q", v, "the response")
	}
}
