// ABOUTME: Tests for the per-session tool result cache.
// ABOUTME: Verifies cache hit/miss behavior, invalidation, and stats tracking.
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
	if c.hits != 1 {
		t.Errorf("expected 1 hit, got %d", c.hits)
	}
	if c.misses != 1 {
		t.Errorf("expected 1 miss, got %d", c.misses)
	}
}
