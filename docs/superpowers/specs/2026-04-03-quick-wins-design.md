# Quick Wins Batch — Design Spec

**Date:** 2026-04-03
**Issues:** #9, #10, #31, #2
**Scope:** Auto-detection unification, code cleanup, edge lookup performance, DIP005 cycle fixes

---

## Fix 1: Auto-detection divergence (#9)

**Problem:** CLI uses extension-based detection (`.dip` → dip, else → dot). Library uses content-based detection (starts with `digraph` → dot, else → dip). Default fallbacks disagree: CLI defaults to "dot", library to "dip".

**Fix:** Make CLI default to "dip" instead of "dot" (matching the library and the current format). In `cmd/tracker/loading.go:detectPipelineFormat`, change the default return from `"dot"` to `"dip"`. Also add a `FormatDip`/`FormatDOT` constant pair to `tracker.go` Config documentation.

The library's content-based detection is correct for raw strings. The CLI's extension-based detection is correct for files. The only divergence that matters is the default fallback.

## Fix 2: Code cleanup (#10)

Items to fix (already-fixed items skipped):
1. Unexport `NodeKindToShape` → `nodeKindToShape` (only used internally)
2. Remove `Edges: make([]*Edge, 0)` → just omit it (nil is fine for slices)
3. Replace custom `contains`/`findSubstring` helpers in `dippin_adapter_test.go` with `strings.Contains`
4. Replace bubble sort in `cmd/tracker/summary.go` with `slices.SortFunc`
5. Add `FormatDip`/`FormatDOT` typed constants in `tracker.go`

Skip: gofmt (already clean), nil guards (already fixed in PR #49).

## Fix 3: O(E) edge lookup → adjacency index (#31)

**Problem:** `OutgoingEdges`/`IncomingEdges` scan the full edge slice on every call.

**Fix:** Add `outgoing map[string][]*Edge` and `incoming map[string][]*Edge` to Graph. Populate in `AddEdge`. Return indexed slices from `OutgoingEdges`/`IncomingEdges`.

## Fix 4: DIP005 cycle validation in examples (#2)

**Problem:** 3 example .dip files have retry/rework cycle edges missing `restart: true`.

**Fix:** Add `restart: true` to the cycle-forming edges. Validate with `dippin doctor`.

---

## Non-Goals

- Rewriting the content-based format detection (works fine for its use case)
- Restructuring the Graph type beyond adding adjacency indexes
