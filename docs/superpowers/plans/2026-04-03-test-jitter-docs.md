# Test Coverage, Retry Jitter, DOT Deprecation Docs Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close test coverage gaps in the adapter, add jitter to retry backoff, and formalize DOT deprecation documentation.

**Architecture:** Three independent tasks. Task 1 adds tests. Task 2 modifies backoff functions. Task 3 adds docs/annotations.

**Tech Stack:** Go 1.25, standard library (`math/rand/v2`).

**Spec:** `docs/superpowers/specs/2026-04-03-test-jitter-docs-design.md`

---

### Task 1: Strengthen .dip test coverage (#11)

**Files:**
- Modify: `pipeline/dippin_adapter_e2e_test.go` (edge topology assertions)
- Modify: `pipeline/dippin_adapter_test.go` (zero-value config, exact subgraph params)
- Test-only changes — no production code modified.

- [ ] **Step 1: Add edge topology assertions to e2e test**

In `pipeline/dippin_adapter_e2e_test.go`, after the existing edge count check (line ~77), add edge-by-edge assertions:

```go
	// Verify edge topology (not just count)
	edgeSet := make(map[string]bool)
	for _, e := range graph.Edges {
		edgeSet[e.From+"->"+e.To] = true
	}
	wantEdges := []string{"start->generate", "generate->done"}
	for _, want := range wantEdges {
		if !edgeSet[want] {
			t.Errorf("missing edge %s", want)
		}
	}
```

- [ ] **Step 2: Add zero-value AgentConfig test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestExtractAgentAttrs_ZeroValueFieldsOmitted(t *testing.T) {
	// All-zero AgentConfig should produce no attrs.
	attrs := map[string]string{}
	extractAgentAttrs(ir.AgentConfig{}, attrs)
	if len(attrs) != 0 {
		t.Errorf("zero-value AgentConfig produced %d attrs, want 0: %v", len(attrs), attrs)
	}
}

func TestExtractHumanAttrs_ZeroValueFieldsOmitted(t *testing.T) {
	attrs := map[string]string{}
	extractHumanAttrs(ir.HumanConfig{}, attrs)
	if len(attrs) != 0 {
		t.Errorf("zero-value HumanConfig produced %d attrs, want 0: %v", len(attrs), attrs)
	}
}

func TestExtractToolAttrs_ZeroValueFieldsOmitted(t *testing.T) {
	attrs := map[string]string{}
	extractToolAttrs(ir.ToolConfig{}, attrs)
	if len(attrs) != 0 {
		t.Errorf("zero-value ToolConfig produced %d attrs, want 0: %v", len(attrs), attrs)
	}
}
```

- [ ] **Step 3: Strengthen subgraph params to exact match**

Find the existing subgraph params test in `pipeline/dippin_adapter_test.go` that uses `strings.Contains` for params. It should now use an exact match since params are sorted deterministically. Change the assertion from:

```go
if !strings.Contains(params, "env=prod") || !strings.Contains(params, "timeout=30s") {
```

To:

```go
want := "env=prod,timeout=30s"
if params != want {
    t.Errorf("subgraph_params = %q, want %q", params, want)
}
```

- [ ] **Step 4: Add edge Weight and Restart attr test**

Add to `pipeline/dippin_adapter_test.go`:

```go
func TestConvertEdge_WeightAndRestart(t *testing.T) {
	irEdge := &ir.Edge{
		From:    "a",
		To:      "b",
		Weight:  5,
		Restart: true,
	}
	gEdge := convertEdge(irEdge)
	if gEdge.Attrs["weight"] != "5" {
		t.Errorf("weight = %q, want %q", gEdge.Attrs["weight"], "5")
	}
	if gEdge.Attrs["restart"] != "true" {
		t.Errorf("restart = %q, want %q", gEdge.Attrs["restart"], "true")
	}
}

func TestConvertEdge_ZeroWeightOmitted(t *testing.T) {
	irEdge := &ir.Edge{From: "a", To: "b"}
	gEdge := convertEdge(irEdge)
	if _, ok := gEdge.Attrs["weight"]; ok {
		t.Error("zero weight should not be in attrs")
	}
	if _, ok := gEdge.Attrs["restart"]; ok {
		t.Error("false restart should not be in attrs")
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./pipeline/ -v`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter_e2e_test.go pipeline/dippin_adapter_test.go
git commit -m "test(adapter): edge topology, zero-value config, weight/restart attrs, exact subgraph params (#11)"
```

---

### Task 2: Retry backoff jitter (#29)

**Files:**
- Modify: `pipeline/retry_policy.go:4-7` (imports), `pipeline/retry_policy.go:122-145` (backoff functions)
- Test: `pipeline/retry_policy_test.go`

- [ ] **Step 1: Write failing test**

Add to `pipeline/retry_policy_test.go`:

```go
func TestExponentialBackoffHasJitter(t *testing.T) {
	base := 2 * time.Second
	attempt := 2 // base delay = 8s (2^2 * 2s)
	expectedBase := 8 * time.Second

	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		d := ExponentialBackoff(attempt, base)
		seen[d] = true
		// Must be within ±25% of expected base
		low := time.Duration(float64(expectedBase) * 0.75)
		high := time.Duration(float64(expectedBase) * 1.25)
		if d < low || d > high {
			t.Fatalf("backoff %v outside [%v, %v]", d, low, high)
		}
	}
	// With 100 samples, we should see more than 1 unique value (jitter)
	if len(seen) < 2 {
		t.Errorf("expected jitter variation, got %d unique values", len(seen))
	}
}

func TestLinearBackoffHasJitter(t *testing.T) {
	base := 2 * time.Second
	attempt := 2 // base delay = 6s ((2+1) * 2s)
	expectedBase := 6 * time.Second

	seen := make(map[time.Duration]bool)
	for i := 0; i < 100; i++ {
		d := LinearBackoff(attempt, base)
		seen[d] = true
		low := time.Duration(float64(expectedBase) * 0.75)
		high := time.Duration(float64(expectedBase) * 1.25)
		if d < low || d > high {
			t.Fatalf("backoff %v outside [%v, %v]", d, low, high)
		}
	}
	if len(seen) < 2 {
		t.Errorf("expected jitter variation, got %d unique values", len(seen))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./pipeline/ -run "TestExponentialBackoffHasJitter|TestLinearBackoffHasJitter" -v`
Expected: FAIL — `len(seen) < 2` since current backoff is deterministic.

- [ ] **Step 3: Add jitter to backoff functions**

In `pipeline/retry_policy.go`, add `"math/rand/v2"` to imports.

Add a jitter helper:

```go
// applyJitter adds ±25% random jitter to a duration, capped at maxBackoffDuration.
func applyJitter(d time.Duration) time.Duration {
	jitter := 0.75 + rand.Float64()*0.5 // [0.75, 1.25)
	result := time.Duration(float64(d) * jitter)
	if result > maxBackoffDuration {
		return maxBackoffDuration
	}
	return result
}
```

Update `ExponentialBackoff` — replace the final return:

```go
func ExponentialBackoff(attempt int, base time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= 2
		if delay > maxBackoffDuration {
			return applyJitter(maxBackoffDuration)
		}
	}
	if delay > maxBackoffDuration {
		return applyJitter(maxBackoffDuration)
	}
	return applyJitter(delay)
}
```

Update `LinearBackoff`:

```go
func LinearBackoff(attempt int, base time.Duration) time.Duration {
	delay := time.Duration(attempt+1) * base
	if delay > maxBackoffDuration {
		return applyJitter(maxBackoffDuration)
	}
	return applyJitter(delay)
}
```

- [ ] **Step 4: Fix existing deterministic tests**

Existing tests like `TestExponentialBackoff` check exact values. They'll fail with jitter. Update them to check within ±25% range instead of exact equality. For example, change:

```go
if d != 4*time.Second {
```
To:
```go
expected := 4 * time.Second
low := time.Duration(float64(expected) * 0.75)
high := time.Duration(float64(expected) * 1.25)
if d < low || d > high {
```

Apply this pattern to all existing backoff test assertions.

- [ ] **Step 5: Run all tests**

Run: `go test ./pipeline/ -run "TestExponential|TestLinear" -v -count=3`
Expected: All pass across 3 runs.

- [ ] **Step 6: Run full suite**

Run: `go test ./... -short`
Expected: All 14 packages pass.

- [ ] **Step 7: Commit**

```bash
git add pipeline/retry_policy.go pipeline/retry_policy_test.go
git commit -m "feat(pipeline): add ±25% jitter to retry backoff to prevent thundering herd (#29)"
```

---

### Task 3: DOT deprecation documentation (#12)

**Files:**
- Modify: `pipeline/parser.go:12-13` (add Deprecated annotation)
- Create: `pipeline/doc.go` (package documentation)
- Modify: `CHANGELOG.md` (add Deprecated section)

- [ ] **Step 1: Add Deprecated annotation to ParseDOT**

In `pipeline/parser.go`, replace the existing comment on `ParseDOT`:

```go
// ParseDOT parses a DOT-format string into a Graph.
// Returns an error if the DOT syntax is invalid or the input is empty.
```

With:

```go
// ParseDOT parses a DOT-format string into a Graph.
// Returns an error if the DOT syntax is invalid or the input is empty.
//
// Deprecated: Use .dip format with FromDippinIR instead.
// DOT support will be removed in v1.0.
```

- [ ] **Step 2: Create pipeline/doc.go**

Create `pipeline/doc.go`:

```go
// Package pipeline implements the core execution engine for multi-agent
// LLM workflows. Pipelines are directed graphs of nodes (agents, humans,
// tools, parallel fan-out) connected by conditional edges.
//
// # Pipeline Formats
//
// Pipelines can be defined in two formats:
//
//   - .dip (Dippin format) — the current format, parsed by dippin-lang.
//     Use FromDippinIR to convert parsed IR to a Graph.
//
//   - .dot (DOT/Graphviz format) — deprecated, will be removed in v1.0.
//     Use ParseDOT for backward compatibility only.
//
// New pipelines should use .dip format exclusively.
package pipeline
```

- [ ] **Step 3: Add CHANGELOG entry**

In `CHANGELOG.md`, add a `### Deprecated` section under the current version (after the existing `### Fixed` section in `[0.14.0]`):

```markdown
### Deprecated

- **DOT format support**: `ParseDOT` is deprecated. Use `.dip` format with `FromDippinIR` instead. DOT support will be removed in v1.0. Run `tracker doctor` on `.dip` files to validate.
```

- [ ] **Step 4: Build and test**

Run: `go build ./... && go test ./... -short`
Expected: All pass (doc.go has no test impact).

- [ ] **Step 5: Commit**

```bash
git add pipeline/parser.go pipeline/doc.go CHANGELOG.md
git commit -m "docs: formalize DOT deprecation with annotations, package doc, and changelog (#12)"
```

---

## Task Dependency Graph

```text
Task 1 (test coverage — independent)
Task 2 (retry jitter — independent)
Task 3 (DOT deprecation docs — independent)
```

All three tasks are fully independent.
