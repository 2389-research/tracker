# P1/P2 Engine Hardening Fixes — Design Spec

**Date:** 2026-04-03
**Issues:** #4, #7, #23, #34
**Scope:** Validation bypass removal, stderr cleanup, auto_status hardening, fidelity truncation honesty

---

## Fix 1: Remove DippinValidated validation bypass (#4)

**Problem:** `Graph.DippinValidated` is an exported bool that, when true, skips 5 structural validation checks (start/exit, edge endpoints, exit outgoing edges, reachability, cycles). Any consumer can set it to `true` and bypass validation. It trusts dippin-lang's pre-1.0 validator without defense-in-depth.

**Fix:** Delete `DippinValidated` entirely.

1. Remove the `DippinValidated bool` field from the `Graph` struct in `pipeline/graph.go`
2. Remove the `g.DippinValidated = true` line in `pipeline/dippin_adapter.go`
3. Remove the `if !g.DippinValidated` guard in `pipeline/validate.go` so all 5 checks always run
4. Remove the comment about DippinValidated in CLAUDE.md if present

The 5 validation functions are cheap (BFS, map lookups). Running them unconditionally adds negligible cost and provides defense-in-depth.

**Test:** Existing validation tests should still pass. Add a test that `FromDippinIR` output passes validation (it already should, but now it's explicitly checked).

---

## Fix 2: Replace os.Stderr with log.Printf in library code (#7)

**Problem:** Library code in `tracker.go` and `pipeline/condition.go` writes warnings directly to `os.Stderr` via `fmt.Fprintln`/`fmt.Fprintf`. Consumers embedding tracker cannot capture, suppress, or redirect these messages.

**Fix:** Replace `fmt.Fprintf(os.Stderr, ...)` with `log.Printf(...)` in library code only. The standard `log` package can be redirected by callers via `log.SetOutput()`. CLI code (`cmd/tracker/`) is fine using stderr.

**Scope — library code only:**

| File | Line(s) | Current | Replacement |
|------|---------|---------|-------------|
| `tracker.go` | 199 | `fmt.Fprintln(os.Stderr, "WARNING: DOT format...")` | `log.Println("WARNING: DOT format...")` |
| `tracker.go` | 215 | `fmt.Fprintln(os.Stderr, d.String())` | `log.Println(d.String())` |
| `tracker.go` | 222 | `fmt.Fprintln(os.Stderr, d.String())` | `log.Println(d.String())` |
| `pipeline/condition.go` | 116 | `fmt.Fprintf(os.Stderr, "warning: unresolved...")` | `log.Printf("warning: unresolved...")` |
| `pipeline/handlers/autopilot.go` | 149 | `fmt.Fprintf(os.Stderr, "WARNING: autopilot...")` | `log.Printf("WARNING: autopilot...")` |
| `pipeline/handlers/autopilot_claudecode.go` | 71 | `fmt.Fprintf(os.Stderr, "WARNING: claude-code...")` | `log.Printf("WARNING: claude-code...")` |

Remove unused `"os"` imports where applicable after the change.

**Test:** No behavioral test needed — this is a logging channel change. Verify build and existing tests pass.

---

## Fix 3: Harden auto_status parsing (#23)

**Problem:** `parseAutoStatus` in `codergen.go` is case-sensitive (`"Success"` is missed) and matches STATUS lines inside code fences that LLMs might hallucinate.

**Fix:** Two changes:

### 3a: Case-insensitive status matching

Lowercase the status value before the switch:

```go
status := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "STATUS:")))
```

### 3b: Skip lines inside code fences

Track whether we're inside a ``` block and skip STATUS lines found there:

```go
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
    // ... existing STATUS parsing
}
```

**Test:**
- `TestParseAutoStatus_CaseInsensitive` — `STATUS: Success`, `STATUS: FAIL`, `STATUS: Retry` all work
- `TestParseAutoStatus_SkipsCodeBlock` — STATUS inside ``` block is ignored

---

## Fix 4: Honest fidelity truncation (#34)

**Problem:** `FidelityTruncate` caps values at 500 characters. The name implies token-level truncation. A proper token-aware approach requires a tokenizer dependency (violates "standard library only" constraint).

**Fix:** Make the behavior honest and slightly smarter:

1. Extract `500` to a named constant `DefaultTruncateLimit = 500`
2. Truncate at word boundary instead of cutting mid-word: find the last space before the limit and cut there, appending `"..."` to indicate truncation
3. Document that truncation is character-based (not token-based) in the fidelity.go doc comment

```go
const DefaultTruncateLimit = 500

func truncateAtWordBoundary(s string, limit int) string {
    if len(s) <= limit {
        return s
    }
    // Find last space before limit to avoid cutting mid-word.
    cut := strings.LastIndex(s[:limit], " ")
    if cut <= 0 {
        cut = limit // No space found — hard cut
    }
    return s[:cut] + "..."
}
```

**Test:**
- `TestTruncateAtWordBoundary` — cuts at word boundary, appends `...`
- `TestTruncateAtWordBoundary_ShortString` — no truncation for short strings
- `TestTruncateAtWordBoundary_NoSpaces` — hard cut when no spaces

---

## Files Changed

| File | Changes |
|------|---------|
| `pipeline/graph.go` | Remove `DippinValidated` field |
| `pipeline/validate.go` | Remove `if !g.DippinValidated` guard |
| `pipeline/dippin_adapter.go` | Remove `g.DippinValidated = true` |
| `tracker.go` | Replace `fmt.Fprintln(os.Stderr, ...)` with `log.Println(...)` |
| `pipeline/condition.go` | Replace `fmt.Fprintf(os.Stderr, ...)` with `log.Printf(...)` |
| `pipeline/handlers/autopilot.go` | Replace `fmt.Fprintf(os.Stderr, ...)` with `log.Printf(...)` |
| `pipeline/handlers/autopilot_claudecode.go` | Replace `fmt.Fprintf(os.Stderr, ...)` with `log.Printf(...)` |
| `pipeline/handlers/codergen.go` | Case-insensitive + code-fence-aware `parseAutoStatus` |
| `pipeline/fidelity.go` | Named constant, word-boundary truncation |
| Various test files | New tests for each fix |

## Non-Goals

- Token-based truncation (requires external tokenizer dependency)
- Custom logger interface (log.Printf is sufficient; callers use log.SetOutput)
- Replacing stderr in CLI code (cmd/tracker/ is fine)
- Structured output mode for auto_status (future enhancement)
