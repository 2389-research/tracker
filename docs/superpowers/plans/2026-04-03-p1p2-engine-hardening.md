# P1/P2 Engine Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove validation bypass, replace stderr with log.Printf in library code, harden auto_status parsing, and improve fidelity truncation.

**Architecture:** Four independent fixes. Task 1 removes a field and guard. Task 2 swaps fprintf calls. Task 3 changes a parser function. Task 4 improves a truncation function. Each is one commit.

**Tech Stack:** Go 1.25, standard library only (`log`, `strings`).

**Spec:** `docs/superpowers/specs/2026-04-03-p1p2-engine-hardening-design.md`

---

### Task 1: Remove DippinValidated validation bypass (#4)

**Files:**
- Modify: `pipeline/graph.go:40-44` (remove field)
- Modify: `pipeline/validate.go:104-112` (remove guard)
- Modify: `pipeline/dippin_adapter.go:97-104` (remove assignment)
- Test: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Remove DippinValidated field from Graph struct**

In `pipeline/graph.go`, delete lines 40-44 (the `DippinValidated` field and its comment):

```go
	// DippinValidated is set by the Dippin adapter when the source IR has
	// already been validated by dippin-lang's validator. When true, Tracker
	// skips structural checks (start/exit, edge endpoints, reachability,
	// cycles, exit outgoing edges) that Dippin already covers.
	DippinValidated bool
```

- [ ] **Step 2: Remove the validation guard**

In `pipeline/validate.go`, replace lines 104-112:

```go
	// Structural checks covered by Dippin's validator (DIP001–DIP006).
	// Skip when the graph was produced from already-validated Dippin IR.
	if !g.DippinValidated {
		validateStartExit(g, ve)
		validateEdgeEndpoints(g, ve)
		validateExitOutgoingEdges(g, ve)
		validateReachability(g, ve)
		validateNoCycles(g, ve)
	}
```

With:

```go
	// Structural checks (always run — defense in depth).
	validateStartExit(g, ve)
	validateEdgeEndpoints(g, ve)
	validateExitOutgoingEdges(g, ve)
	validateReachability(g, ve)
	validateNoCycles(g, ve)
```

- [ ] **Step 3: Remove the assignment in the adapter**

In `pipeline/dippin_adapter.go`, delete the lines that set `DippinValidated`:

```go
	// Mark that this graph was produced from validated Dippin IR.
	// Tracker's validateGraph will skip structural checks that
	// Dippin already covers (DIP001–DIP006).
	g.DippinValidated = true
```

- [ ] **Step 4: Fix any compilation errors**

Run: `go build ./...`
If any code references `DippinValidated`, fix it. Grep: `grep -r DippinValidated pipeline/ tracker.go`

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -short`
Expected: All 14 packages pass. The adapter e2e tests should still pass since dippin-produced graphs are structurally valid.

- [ ] **Step 6: Commit**

```bash
git add pipeline/graph.go pipeline/validate.go pipeline/dippin_adapter.go
git commit -m "fix(pipeline): remove DippinValidated bypass — always run structural validation (#4)"
```

---

### Task 2: Replace os.Stderr with log.Printf in library code (#7)

**Files:**
- Modify: `tracker.go:199,215,222` (3 stderr calls)
- Modify: `pipeline/condition.go:116` (1 stderr call)
- Modify: `pipeline/handlers/autopilot.go:149` (1 stderr call)
- Modify: `pipeline/handlers/autopilot_claudecode.go:71` (1 stderr call)

- [ ] **Step 1: Fix tracker.go**

In `tracker.go`, replace the 3 stderr calls:

Line 199 — replace:
```go
		fmt.Fprintln(os.Stderr, "WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
```
With:
```go
		log.Println("WARNING: DOT format is deprecated. Migrate pipelines to .dip format.")
```

Line 215 — replace:
```go
				fmt.Fprintln(os.Stderr, d.String())
```
With:
```go
				log.Println(d.String())
```

Line 222 — replace:
```go
			fmt.Fprintln(os.Stderr, d.String())
```
With:
```go
			log.Println(d.String())
```

Add `"log"` to the import block. Remove `"os"` if no longer used (check other references first).

- [ ] **Step 2: Fix pipeline/condition.go**

In `pipeline/condition.go`, line 116 — replace:
```go
		fmt.Fprintf(os.Stderr, "warning: unresolved condition variable %q (defaulting to empty string)\n", name)
```
With:
```go
		log.Printf("warning: unresolved condition variable %q (defaulting to empty string)", name)
```

Add `"log"` to imports. Remove `"os"` if no longer used. Note: `"fmt"` is still needed by other functions.

- [ ] **Step 3: Fix pipeline/handlers/autopilot.go**

In `pipeline/handlers/autopilot.go`, line 149 — replace:
```go
		fmt.Fprintf(os.Stderr, "WARNING: autopilot chose %q which doesn't match any option, using default\n", decision.Choice)
```
With:
```go
		log.Printf("WARNING: autopilot chose %q which doesn't match any option, using default", decision.Choice)
```

Add `"log"` to imports. Remove `"os"` if no longer used.

- [ ] **Step 4: Fix pipeline/handlers/autopilot_claudecode.go**

In `pipeline/handlers/autopilot_claudecode.go`, line 71 — replace:
```go
		fmt.Fprintf(os.Stderr, "WARNING: claude-code autopilot chose %q which doesn't match any option, using default\n", decision.Choice)
```
With:
```go
		log.Printf("WARNING: claude-code autopilot chose %q which doesn't match any option, using default", decision.Choice)
```

Add `"log"` to imports. Remove `"os"` if no longer used.

- [ ] **Step 5: Verify build and tests**

Run: `go build ./... && go test ./... -short`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add tracker.go pipeline/condition.go pipeline/handlers/autopilot.go pipeline/handlers/autopilot_claudecode.go
git commit -m "fix(pipeline): replace os.Stderr writes with log.Printf in library code (#7)"
```

---

### Task 3: Harden auto_status parsing (#23)

**Files:**
- Modify: `pipeline/handlers/codergen.go:471-489` (parseAutoStatus)
- Test: `pipeline/handlers/codergen_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/handlers/codergen_test.go`:

```go
func TestParseAutoStatus_CaseInsensitive(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"STATUS: Success\nDone.", pipeline.OutcomeSuccess},
		{"STATUS: FAIL\nBroken.", pipeline.OutcomeFail},
		{"STATUS: Retry\nNeed more.", pipeline.OutcomeRetry},
		{"STATUS: SUCCESS\nAll good.", pipeline.OutcomeSuccess},
	}
	for _, tt := range tests {
		got := parseAutoStatus(tt.input)
		if got != tt.want {
			t.Errorf("parseAutoStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseAutoStatus_SkipsCodeBlock(t *testing.T) {
	input := "Here is how to set status:\n```\nSTATUS:fail\n```\nSTATUS:success\nDone."
	got := parseAutoStatus(input)
	if got != pipeline.OutcomeSuccess {
		t.Errorf("parseAutoStatus with code block = %q, want %q", got, pipeline.OutcomeSuccess)
	}
}

func TestParseAutoStatus_OnlyCodeBlockDefaultsToSuccess(t *testing.T) {
	input := "Some output.\n```\nSTATUS:fail\n```\nNo real status here."
	got := parseAutoStatus(input)
	if got != pipeline.OutcomeSuccess {
		t.Errorf("parseAutoStatus code-block-only = %q, want %q", got, pipeline.OutcomeSuccess)
	}
}
```

- [ ] **Step 2: Run tests to verify failures**

Run: `go test ./pipeline/handlers/ -run "TestParseAutoStatus_CaseInsensitive|TestParseAutoStatus_SkipsCodeBlock" -v`
Expected: FAIL — case-insensitive tests fail because `"Success"` != `"success"`.

- [ ] **Step 3: Implement case-insensitive + code-fence-aware parsing**

Replace `parseAutoStatus` in `pipeline/handlers/codergen.go`:

```go
// parseAutoStatus scans the response text for STATUS: directives and returns
// the last one found. Case-insensitive matching. Lines inside code fences
// (``` blocks) are skipped to avoid matching hallucinated STATUS lines.
// Falls back to success if no valid STATUS line is found.
func parseAutoStatus(text string) string {
	result := pipeline.OutcomeSuccess
	inCodeBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}
		if !strings.HasPrefix(trimmed, "STATUS:") {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "STATUS:")))
		switch status {
		case "success":
			result = pipeline.OutcomeSuccess
		case "fail":
			result = pipeline.OutcomeFail
		case "retry":
			result = pipeline.OutcomeRetry
		}
	}
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/handlers/ -run "TestParseAutoStatus|TestCodergenHandlerAutoStatus" -v`
Expected: All pass (new and existing tests).

- [ ] **Step 5: Run full handler tests**

Run: `go test ./pipeline/handlers/ -v -short`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/handlers/codergen.go pipeline/handlers/codergen_test.go
git commit -m "fix(handlers): case-insensitive auto_status parsing, skip code fences (#23)"
```

---

### Task 4: Honest fidelity truncation (#34)

**Files:**
- Modify: `pipeline/fidelity.go:145-158` (truncation logic)
- Test: `pipeline/fidelity_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/fidelity_test.go`:

```go
func TestTruncateAtWordBoundary(t *testing.T) {
	long := strings.Repeat("word ", 200) // 1000 chars
	result := truncateAtWordBoundary(long, DefaultTruncateLimit)
	if len(result) > DefaultTruncateLimit+3 { // +3 for "..."
		t.Errorf("len = %d, want <= %d", len(result), DefaultTruncateLimit+3)
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("expected ... suffix on truncated string")
	}
	// Should not cut mid-word
	beforeEllipsis := strings.TrimSuffix(result, "...")
	if strings.HasSuffix(beforeEllipsis, "wor") {
		t.Error("truncated mid-word")
	}
}

func TestTruncateAtWordBoundary_ShortString(t *testing.T) {
	short := "hello world"
	result := truncateAtWordBoundary(short, DefaultTruncateLimit)
	if result != short {
		t.Errorf("short string should not be truncated: got %q", result)
	}
}

func TestTruncateAtWordBoundary_NoSpaces(t *testing.T) {
	noSpaces := strings.Repeat("x", 600)
	result := truncateAtWordBoundary(noSpaces, DefaultTruncateLimit)
	if len(result) != DefaultTruncateLimit+3 {
		t.Errorf("no-space truncation: len = %d, want %d", len(result), DefaultTruncateLimit+3)
	}
}
```

Add `"strings"` to imports if not already present.

- [ ] **Step 2: Run tests to verify failures**

Run: `go test ./pipeline/ -run "TestTruncateAtWordBoundary" -v`
Expected: FAIL — `truncateAtWordBoundary` and `DefaultTruncateLimit` undefined.

- [ ] **Step 3: Implement word-boundary truncation**

In `pipeline/fidelity.go`, add the constant and function (before `compactMedium`):

```go
// DefaultTruncateLimit is the maximum character length for context values
// in truncate fidelity mode. Truncation is character-based (not token-based).
const DefaultTruncateLimit = 500

// truncateAtWordBoundary truncates s to approximately limit characters,
// cutting at the last word boundary before the limit. Appends "..." when
// truncation occurs.
func truncateAtWordBoundary(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := strings.LastIndex(s[:limit], " ")
	if cut <= 0 {
		cut = limit
	}
	return s[:cut] + "..."
}
```

Then update `compactMedium` to use it:

```go
func compactMedium(ctx *PipelineContext, truncate bool) map[string]string {
	result := make(map[string]string)
	for _, key := range mediumKeys {
		if val, ok := ctx.Get(key); ok {
			if truncate {
				val = truncateAtWordBoundary(val, DefaultTruncateLimit)
			}
			result[key] = val
		}
	}
	return result
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pipeline/ -run "TestTruncateAtWordBoundary|TestCompactContextTruncate" -v`
Expected: All pass. Note: the existing `TestCompactContextTruncate` checks `len(result["last_response"]) == 500` — with word-boundary truncation the length may be slightly less than 500 plus `...`. If it fails, update the existing test assertion to check `len <= 503` (500 + "...") instead of exact equality.

- [ ] **Step 5: Run full pipeline tests**

Run: `go test ./pipeline/ -v`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/fidelity.go pipeline/fidelity_test.go
git commit -m "fix(pipeline): word-boundary-aware fidelity truncation with named constant (#34)"
```

---

## Task Dependency Graph

```text
Task 1 (remove DippinValidated — independent)
Task 2 (stderr → log.Printf — independent)
Task 3 (auto_status hardening — independent)
Task 4 (fidelity truncation — independent)
```

All four tasks are fully independent and can be implemented in any order.
