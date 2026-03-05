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
