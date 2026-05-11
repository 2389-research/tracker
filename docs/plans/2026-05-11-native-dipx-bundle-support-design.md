# Design: Native `.dipx` Bundle Support in Tracker

**Date:** 2026-05-11
**Status:** Approved (design phase)
**Driver:** [Pipelines team feature request](../requests/native-dipx-bundle-support.md)
**Target version:** Unreleased (post v0.25.0)

---

## Goal

Make tracker accept `.dipx` bundles natively wherever it accepts a pipeline file
today. A `.dipx` is the content-addressed, SHA-256-verified ZIP bundle introduced
in dippin v0.24 — entry `.dip` plus every transitive `subgraph ref:` file, sealed
into one artifact with `manifest.json` and per-file hashes.

The full vision: loader support, audit-trail provenance, and strict bundle
identity verification on resume. Plain `.dip` flows are unchanged.

## Non-Goals

- No `tracker pack` / `tracker unpack` commands. That's dippin's job. `dippin pack -o foo.dipx foo.dip` stays the canonical producer.
- Embedded built-in workflows (`tracker workflows` / `tracker init`) remain plain `.dip`. Bundling is opt-in for user-supplied files.
- `FromDippinIR` is untouched — bundles still go through it.
- No magic-byte format sniffing. Extension is the dispatch trigger.

## Decisions

| Question | Decision |
|---|---|
| Loader approach | (C) **IR-direct.** Use `bundle.Entry() *ir.Workflow` and `bundle.Lookup(path) *ir.Workflow`; skip re-parsing. Refactor `LoadDippinWorkflow` to expose a `LoadDippinWorkflowFromIR` half shared by both `.dip` and `.dipx` paths. |
| Scope | Full vision: loader + audit trail + strict resume verification. |
| Resume policy | **Strict.** Identity mismatch aborts with `--force-bundle-mismatch` escape hatch. |
| Identity landing spots | `Checkpoint.BundleIdentity`, `jsonlLogEntry.BundleIdentity` (every line), `RunSummary.BundleIdentity`, `tracker.Result.BundleIdentity`. |
| Display format | `sha256:efb5648d28e6c2...` — match `dippin inspect`. Truncate to 16 hex chars + `...` in table columns; full hash internally. |
| Format detection | Extension only (`.dipx`). |
| dippin-lang bump | v0.23.0 → v0.24.0 as the first commit in the same PR. |

## Architecture

A `.dipx` is a sealed, content-addressed bundle: `dipx.Open()` verifies all
SHA-256 hashes, parses every transitive workflow into `*ir.Workflow`, and exposes
them via `bundle.Entry()` / `bundle.Lookup(path)`. Tracker's role narrows to
"convert IR to Graph and thread identity into observability."

### Entry split

The single chokepoint stays `cmd/tracker/loading.go:loadPipeline()`. Add one
detect-and-dispatch branch *before* the existing `os.ReadFile`:

```
loadPipeline(filename, formatOverride):
    if ext(filename) == ".dipx":
        return loadDipxPipeline(filename)        # NEW path
    bytes = os.ReadFile(filename)
    switch format { case "dip": ...; case "dot": ... }   # existing
```

### Loader split inside the `.dip` family

Refactor `pipeline.LoadDippinWorkflow(source, filename)` in
`pipeline/dippin_load.go` into two halves:

- `parser.NewParser(source, filename).Parse()` → `*ir.Workflow` (existing; stays
  in `LoadDippinWorkflow`)
- `LoadDippinWorkflowFromIR(workflow, filename) → (*Graph, diags, error)` —
  runs `validator.Validate` + `validator.Lint` + `FromDippinIR`. Shared tail.

The `.dip` path calls both halves; the `.dipx` path calls only the second half,
feeding it `bundle.Entry()`.

### Subgraph handling

`loadDipxPipeline` walks the bundle's workflows (`bundle.Manifest().Files`),
converts each non-entry workflow via `LoadDippinWorkflowFromIR(bundle.Lookup(path), path)`,
and builds the `subgraphs` map keyed by the canonical bundle path. The existing
`loadSubgraphsRecursive` filesystem walker is **bypassed entirely** when the
source is a bundle — no recursion, no cycle check, no candidate path resolution.
dipx did all of that during `Open`.

### Identity propagation

`loadDipxPipeline` returns `(*Graph, map[string]*Graph, BundleInfo, error)`
where `BundleInfo{Identity, EntryPath, Manifest}`. `loadAndValidatePipeline` in
`cmd/tracker/run.go` threads `BundleInfo` into:

1. `Config.BundleIdentity` → engine constructor → stamped on every
   `PipelineEvent` → lands on every `jsonlLogEntry`.
2. `Checkpoint.BundleIdentity` → written on first save; checked on resume.
3. `tracker.Result.BundleIdentity` → set at run completion for library callers.
4. `RunSummary.BundleIdentity` → read back from checkpoint by `tracker.ListRuns`;
   surfaces in `tracker list`.

Plain `.dip` runs leave all four fields empty — backward compatible.

## Components

### New code

1. **`pipeline/dipx_load.go`** (~80 lines).
   - `LoadDipxBundle(ctx, path) (*Graph, map[string]*Graph, BundleInfo, error)`.
   - `BundleInfo{Identity string, EntryPath string, Manifest dipx.Manifest}`.
   - `formatIdentity(b *dipx.Bundle) string` → `"sha256:" + hex.EncodeToString(b.Identity()[:])`.

2. **`pipeline/dippin_load.go`** — split.
   - Public façade `LoadDippinWorkflow(source, filename)` unchanged in behavior.
   - New `LoadDippinWorkflowFromIR(workflow *ir.Workflow, filename string) (*Graph, []validator.Diagnostic, error)`.

3. **`cmd/tracker/loading.go`** — dispatch.
   - `loadDipxPipeline(filename) (*Graph, map[string]*Graph, BundleInfo, error)`.
   - `.dipx` branch in `loadPipeline()` *before* `os.ReadFile`.

### Modified code

4. **`cmd/tracker/run.go:loadAndValidatePipeline`** — return `BundleInfo`
   alongside graph + subgraphs map.
5. **`pipeline/checkpoint.go`** — `BundleIdentity string` field. Zero-value
   for old checkpoints stays compatible.
6. **`pipeline/events.go` + `pipeline/events_jsonl.go`** —
   `PipelineEvent.BundleIdentity` and the matching JSONL field. Engine
   constructor accepts a `WithBundleIdentity(string)` option and stamps every
   event.
7. **`tracker_audit.go`** — `RunSummary.BundleIdentity`; summary builder reads
   from checkpoint.
8. **`tracker.go`** — `tracker.Result.BundleIdentity` from `Config.BundleIdentity`.
9. **`cmd/tracker/audit.go`** — `Bundle` column in `printRunList`; `Bundle:`
   header in `printAuditHeader`.
10. **`cmd/tracker/commands.go:resolveRunCheckpoint`** — bundle identity check
    on resume; fail unless `--force-bundle-mismatch`.
11. **`go.mod`** — bump `github.com/2389-research/dippin-lang` v0.23.0 → v0.24.0.

### New flag

12. **`--force-bundle-mismatch`** in `cmd/tracker/flags.go`. Off by default.

## Data Flow

### First run: `tracker run sprint_runner_dr.dipx`

```
CLI args
  │
  ▼
resolvePipelineSource("sprint_runner_dr.dipx")    cmd/tracker/resolve.go
  │  isExplicitFilePath()=true (matches .dipx)
  ▼
loadAndValidatePipeline()                          cmd/tracker/run.go
  │
  ▼
loadPipeline(filename)                             cmd/tracker/loading.go
  │  ext == ".dipx"? → branch
  ▼
loadDipxPipeline(filename)                         cmd/tracker/loading.go
  │
  ▼
pipeline.LoadDipxBundle(ctx, filename)             pipeline/dipx_load.go
  │
  ├─► dipx.Open(ctx, filename)        ── verifies SHA-256 hashes,
  │                                       parses every workflow,
  │                                       returns *dipx.Bundle
  │
  ├─► entryGraph = LoadDippinWorkflowFromIR(
  │                  bundle.Entry(),
  │                  bundle.Manifest().Entry)
  │                                     ── validate + lint + FromDippinIR
  │
  ├─► for each non-entry path in Manifest.Files:
  │      sub = LoadDippinWorkflowFromIR(
  │              bundle.Lookup(path), path)
  │      subgraphs[path] = sub
  │
  └─► identity = "sha256:" + hex(bundle.Identity())
       return (entryGraph, subgraphs, BundleInfo{identity, entryPath, manifest})

BundleInfo travels up:
  ├─► Config.BundleIdentity
  ├─► Engine via WithBundleIdentity → every PipelineEvent
  ├─► Checkpoint.BundleIdentity (persisted each save)
  ├─► jsonlLogEntry on every activity.jsonl line
  └─► tracker.Result.BundleIdentity on completion
```

`loadSubgraphsRecursive` is skipped — bundle workflows are pre-resolved.

### Resume: `tracker -r aff6262f sprint_runner_dr.dipx`

```
resolveRunCheckpoint(cfg)                          cmd/tracker/commands.go
  │
  ├─► load Checkpoint from disk
  ├─► open the .dipx (dipx.Open) → currentIdentity
  ├─► if checkpoint.BundleIdentity != currentIdentity:
  │      if !cfg.forceBundleMismatch:
  │         return error showing both hashes
  │      else:
  │         log warning, emit bundle_mismatch_forced event,
  │         update Checkpoint.BundleIdentity to currentIdentity on next save
  └─► proceed to normal run flow
```

### `tracker validate` / `tracker simulate`

Same loader entry; no checkpoint; no engine. Bundle identity is computed and
printed in the success line (`validate ok: bundle sha256:efb5648d...`) and
otherwise discarded.

### `tracker list` and `tracker audit`

Read each run's `Checkpoint.BundleIdentity`. `printRunList` truncates to 16 hex
chars in the new `Bundle` column. `printAuditHeader` adds a `Bundle:` row when
identity is non-empty. `.dip` runs show empty/blank.

## Error Handling

### Load-time (`pipeline.LoadDipxBundle`)

Strict mode (`dipx.Open`, not `OpenLax`). Wrap with context, no swallowing.

| dipx error class | User-facing message |
|---|---|
| not a ZIP / `ErrManifestMissing` | `load bundle %s: not a valid .dipx (manifest.json missing or unreadable)` |
| hash mismatch | `load bundle %s: integrity check failed for %s — bundle is corrupt or tampered` |
| `ErrFileUnexpected` (extras in strict) | `load bundle %s: contains files not listed in manifest — repack with 'dippin pack'` |
| `ErrPathUnsafe` | `load bundle %s: rejected unsafe path %s` |
| ref closure / acyclicity | `load bundle %s: %w` (dipx's own message is good) |
| context cancelled | propagate `context.Canceled` |

After `dipx.Open` succeeds, `LoadDippinWorkflowFromIR` runs `validator.Validate`
(DIP001-DIP009) and `validator.Lint` (DIP101-DIP115) per workflow — **new
surface**, since dipx does not run these. Diagnostics print to stderr with the
bundle-relative path (`workflows/foo.dip: DIP001 ...`). Error message tells
users to fix source and `dippin pack` to rebuild.

### Resume-time (`resolveRunCheckpoint`)

Three mismatch shapes, all failures unless `--force-bundle-mismatch`:

1. **Identity differs** — explicit error with both hashes and `--force-bundle-mismatch` hint.
2. **Downgrade** (started from `.dipx`, resuming from `.dip`) — empty identity ≠ original; same error shape.
3. **Upgrade** (started from `.dip`, resuming from `.dipx`) — original identity empty ≠ current; same error shape.

Force-override behavior:
- Warning to stderr with both hashes.
- Emit `bundle_mismatch_forced` event to `activity.jsonl` carrying both hashes.
- Update `Checkpoint.BundleIdentity` to current on next save (user has explicitly accepted the new bundle as source of truth).

### Out-of-band

Corrupted `.dipx` mid-run is not a thing — `dipx.Open` reads the bundle fully
into memory at load time. No subsequent disk reads of the `.dipx` during
execution.

## Testing

**Per CLAUDE.md: real data, real APIs, no mocking.** Test bundles are produced
via `dipx.Pack` at test setup. No hand-crafted ZIPs.

### Unit tests (`pipeline/dipx_load_test.go`)

- Happy path: pack entry + 1 subgraph → `LoadDipxBundle` → assert entry graph, subgraphs map keyed by canonical path, identity matches `bundle.Identity()`.
- Hash mismatch: tamper one byte in ZIP → hash-mismatch error.
- Missing manifest: plain ZIP without `manifest.json` → `not a valid .dipx`.
- `.dip`-with-`.dipx`-extension → clean dipx error.
- DIP001 in packed workflow → validator error (proves post-dipx validation runs).
- Closure: every non-entry path appears in subgraphs map.

### Refactor regression (`pipeline/dippin_load_test.go`)

- Existing `LoadDippinWorkflow` tests stay green.
- Add direct test for `LoadDippinWorkflowFromIR` — parse externally, pass IR, assert byte-equivalent Graph output vs source-string path.

### CLI integration (`cmd/tracker/*_test.go`)

- `tracker validate sprint.dipx` → exit 0, prints `bundle sha256:...`.
- `tracker simulate sprint.dipx` → exit 0.
- `tracker run sprint.dipx --autopilot lax` on a trivial pipeline → checkpoint has `BundleIdentity`; activity.jsonl carries `bundle_identity` on every line; `Result.BundleIdentity` set.
- `tracker list` includes Bundle column; truncated hash for `.dipx` runs, empty for `.dip`.
- `tracker audit <runID>` prints `Bundle: sha256:...`.

### Resume tests

- Matching identity → success.
- Tampered/repacked bundle (different identity) → abort with both-hashes error mentioning `--force-bundle-mismatch`.
- `--force-bundle-mismatch` → warning + continue; `bundle_mismatch_forced` event emitted; checkpoint identity updates.
- Downgrade (resume `.dip` when checkpoint has identity) → abort.
- Upgrade (resume `.dipx` when checkpoint has no identity) → abort.
- Pure `.dip` resume (no identity on either side) → succeeds unchanged.

### Test fixture helper

- `PackTestBundle(t, entryPath, subgraphPaths...) string` — writes a real `.dipx` to `t.TempDir()` via `dipx.Pack`. Reused across integration tests.

### Backward-compat gate (CLAUDE.md)

- `go build ./...` clean.
- `go test ./... -short` clean across all 17 packages.
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` A grade after the v0.23→v0.24 module bump.

### dippin-lang v0.24 bump verification (first commit)

- `go get github.com/2389-research/dippin-lang@v0.24.0`
- `go build ./...` clean
- `go test ./... -short` clean
- Any breakage from v0.23→v0.24 fixed in the same commit before adding `.dipx` code on top.

## Out of Scope

- Fuzzing `dipx` (dippin owns).
- Stress tests on huge bundles (existing dipx tests cover).
- New conformance tests against the dipx spec (dippin owns).
- IR-direct optimization for plain `.dip` files (would require routing `.dip` through `dipx.Open`-as-degenerate-single-file; out of scope).
