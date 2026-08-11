# Switching embedded built-in delivery from raw `.dip` embed to `.dipx` bundle embed

**Status:** FROZEN DESIGN — review before any engine code is written.
**Date:** 2026-08-11
**Branch context:** `feat/dip-file-directives`
**Depends on:** dippin-lang v0.63.0 (file-directives + `dipx.Pack`) — already pinned (`go.mod`).

---

## 1. Problem statement

dippin v0.63 lets an agent/tool node externalize its prompt/script bodies via
file-directives (`prompt_file:`, `system_prompt_file:`, `command_file:`,
`prompt_prefix_file:`, `prompt_suffix_file:`, `suffix_file:`) and lets a
workflow reference child graphs via `subgraph_ref:`. Both resolve **at load
time relative to the `.dip`'s directory on the real OS filesystem** — dippin's
`parser.ResolveFileDirectives` uses `os.ReadFile` / `filepath.EvalSymlinks`
(verified: `dippin-lang@v0.63.0/parser/resolve.go:28`, `:250`, `:288`), never an
`fs.FS`.

tracker embeds 4 of its 29 examples into the binary as **raw `.dip` bytes**:

```
//go:embed examples/ask_and_execute.dip
//go:embed examples/build_product.dip
//go:embed examples/build_product_with_superspec.dip
//go:embed examples/deep_review.dip
var embeddedWorkflows embed.FS          // tracker_workflows.go:18-22
```

An embedded built-in has **no directory on disk**, so both load paths anchor
file-directive resolution at a location where the referenced files do not exist:

| Path | Call site | Anchor dir | Result if the `.dip` used `prompt_file:` |
|---|---|---|---|
| **Library** `tracker.Run(name-bytes)` | `parseDIPSource` → `LoadDippinWorkflow(source, "inline.dip")` (`tracker.go:620`) | `filepath.Dir("inline.dip")` = `.` (caller CWD) | reads `./<prompt>` — wrong / missing → fatal |
| **CLI** `tracker build_product` | `loadEmbeddedPipeline` → `loadDippinPipeline(string(data), info.File)` (`cmd/tracker/loading.go:211-216`), `info.File = "examples/build_product.dip"` | `examples/` **relative to CWD** | reads `./examples/<prompt>` — only exists if run from repo root → fatal everywhere else |

The same non-existent-anchor problem blocks `subgraph_ref:` in an embedded
built-in — the documented **subgraph built-in delivery gap** (MEMORY:
`project_subgraph_builtin_delivery_gap`) and a prerequisite for cross-workflow
dedup (#307). `run.go:422` even hard-codes the assumption: *"Embedded workflows
have no subgraphs (none of the 3 core pipelines use them)."*

**Net:** as long as built-ins ship as raw `.dip`, they can never externalize a
prompt/script or reference a subgraph. This spec switches the *delivery format*
of the 4 embedded built-ins to a self-contained **`.dipx` bundle** so their
authored source can use directives + subgraph refs while the embedded artifact
resolves with **zero filesystem anchor**.

The other 25 examples are disk-only (run via `tracker examples/<x>.dip`, which
anchors on `examples/` correctly) and are **out of scope** — they already
resolve directives today and need no change.

---

## 2. Why `.dipx`, and why the *default-inline* pack

`dipx.Pack` (`dippin-lang@v0.63.0/dipx/dipx.go:226`) has two modes:

- **default (`PackOptions{}`)** → **`format_version 1`**, self-contained: every
  `*_file:` directive target is **inlined** into the packed `.dip` text; the ZIP
  carries the entry `.dip` (with prompts already baked in) plus every
  transitively-reachable subgraph. Doc: *"The zero-value PackOptions reproduces
  the default inline behavior exactly (format_version 1)"* and *"produces a
  deterministic byte stream."*
- **`NoInline: true`** → **`format_version 2`**: keeps the `*_file:` directives
  in the packed text and ships the fragment files as opaque asset entries.

**Recommendation: pack the 4 built-ins with the default (inline) mode.**
Justification:

1. **Zero anchor at load.** An inline bundle has no residual `*_file:` directive
   to resolve — the prompt/script bytes are already in the node attrs. Loading
   it needs no directory, which is exactly the property the embedded case lacks.
   A `format_version 2` bundle re-introduces directive resolution at load time
   and would depend on dipx resolving assets from inside the ZIP — an unverified
   behavior we should not build on.
2. **Graph identity (golden-trace safety, §5).** After inlining, the entry
   workflow's IR is identical to the fully-inline `.dip` tracker embeds today,
   so `pipeline.FromDippinIR` produces a byte-identical `*pipeline.Graph`.
3. **Determinism → an exact drift guard (§6).** A deterministic pack lets the
   sync check be a byte-compare, not a fuzzy match.

`NoInline` is only worth revisiting if `tracker init` must reproduce the
*externalized* file layout on disk (see §7 open question); it is not needed for
the run path.

---

## 3. The load-path constraint that shapes the whole design

`pipeline.LoadDipxBundle(ctx, path)` (`pipeline/dipx_load.go:43`) is **path-based**
— it calls `dipx.Open(ctx, path)`, which is `os.Open(path)`
(`dippin-lang@v0.63.0/dipx/dipx.go:14`, `:51`). **There is no exported
reader-based entry point in the `dipx` package** — the internal
`openFromReader(ctx, io.ReaderAt, size int64)` (`dipx.go:72`) that does the real
work is unexported.

Consequence: embedded `.dipx` bytes (an `embed.FS` blob) **cannot be handed
directly to the loader**. Two ways to bridge:

- **(A) Materialize to a temp file** (tracker-only, ships today). Write the
  embedded bytes to `os.CreateTemp`, `LoadDipxBundle(tempPath)`, remove the
  temp. ~10 lines, no upstream dependency. Cost: a temp-file write per embedded
  run; the file is a public artifact for its lifetime (mode 0600 mitigates).
- **(B) Add a public reader API upstream** (clean, needs a dippin release).
  `dipx.OpenReader(ctx, io.ReaderAt, size)` (a 3-line exported wrapper over the
  existing `openFromReader`) + `pipeline.LoadDipxBundleReader(ctx, r, size)`.
  No temp file; loads straight from the `embed.FS` `io.ReaderAt`.

**Recommendation: ship on (A) now, and open a dippin issue for (B) as the
target end-state.** (A) unblocks tracker without waiting on a dippin cut; (B) is
the eventual clean seam and lets us delete the temp-file dance. This is the one
place the design touches an external dependency — flagged as an open question
(§8), because the maintainer may prefer to block on (B) rather than land a
temp-file bridge.

---

## 4. What gets embedded, and the two load paths that change

### 4.1 Embed the `.dipx`, keep the source `.dip` reachable for `init`

`tracker.OpenWorkflow(name)` currently returns the **raw `.dip` source**, which
`tracker init <name>` writes verbatim to `./<name>.dip`
(`cmd/tracker/commands.go:219-226`) and `parseWorkflowHeader` scans for the
`goal:`/`requires:` catalog fields (`tracker_workflows.go:75-82`). A `.dipx` is a
binary ZIP — it satisfies neither.

Two sub-options:

- **(4.1a) Embed `.dipx` only; `init` and header-scan go through the bundle.**
  `init` calls `dipx.Extract` (`dipx.go:267`) to write the `.dip` (inline pack →
  a single self-contained `.dip`); `parseWorkflowHeader` reads the entry `.dip`
  out of the bundle instead of the `embed.FS` directly. Smaller binary; one
  source of truth.
- **(4.1b) Embed BOTH the source `.dip` (for `init` + header scan) and the
  packed `.dipx` (for run).** No change to `init`/header logic; the `.dipx` is a
  run-only artifact. Simpler diff, slightly larger binary, and the two must be
  kept in sync (which the §6 drift guard already does by construction — it packs
  the source `.dip`).

**Recommendation: (4.1b).** It keeps `OpenWorkflow`/`init`/`parseWorkflowHeader`
untouched (the header scan and `init` fidelity are load-bearing UX), confines
the change to the *run/graph-load* path, and the drift guard makes the pair
authoritative-together. Revisit (4.1a) only if binary size becomes a concern.

### 4.2 Load-path change — CLI

Today (`cmd/tracker/run.go:409-432`, `loadGraphAndSubgraphs`, embedded branch):

```go
graph, err := loadEmbeddedPipeline(info)              // loading.go:211 → loadDippinPipeline(data, info.File)
subgraphs, err := loadSubgraphs(graph, info.File)     // walks disk for subgraph_ref — never finds any
return graph, subgraphs, pipeline.BundleInfo{}, nil   // BundleInfo zero
```

Change `loadEmbeddedPipeline` (`cmd/tracker/loading.go:209-217`) to read the
embedded **`.dipx`** bytes and load them as a bundle:

```go
func loadEmbeddedBundle(info WorkflowInfo) (*pipeline.Graph, map[string]*pipeline.Graph, pipeline.BundleInfo, error) {
    data, err := tracker.OpenWorkflowBundle(info.Name)         // new: returns embedded .dipx bytes
    if err != nil { ... }
    tmp, cleanup, err := writeTempBundle(data)                 // approach (A); or LoadDipxBundleReader under (B)
    if err != nil { ... }
    defer cleanup()
    return pipeline.LoadDipxBundle(context.Background(), tmp)   // returns graph, subgraphs, BundleInfo, diags, err
}
```

and rewire the embedded branch of `loadGraphAndSubgraphs`
(`cmd/tracker/run.go:421-431`) to take `subgraphs` **and** the populated
`BundleInfo` straight from the bundle — deleting the now-dead
`loadSubgraphs(graph, info.File)` disk walk and the stale "*embedded workflows
have no subgraphs*" comment. The `.dipx` loader already pre-resolves and
canonicalizes subgraph refs (`pipeline/dipx_load.go:62`, `:166`), so
`validateSubgraphRefs` (`run.go:400`) then passes against the bundle's map.

`validate`/`simulate` embedded paths (`cmd/tracker/validate.go:46`,
`simulate.go`) route through the same `loadEmbeddedBundle` for parity.

### 4.3 Load-path change — Library

The library has **no embedded-aware `Run`**: a consumer does
`OpenWorkflow(name)` → `Run(ctx, string(bytes), cfg)`, and `Run` →
`parseDIPSource` → `LoadDippinWorkflow(source, "inline.dip")` (`tracker.go:620`).
Feeding it `.dipx` bytes as a `source string` breaks — `parseDIPSource` expects
`.dip` text.

Add a first-class library seam that mirrors the CLI:

```go
// tracker_workflows.go (new)
func OpenWorkflowBundle(name string) ([]byte, WorkflowInfo, error)   // embedded .dipx bytes
func RunWorkflow(ctx, name string, cfg Config) (*Result, error)      // load embedded .dipx → NewEngineFromGraph → run
```

`RunWorkflow` internally does the §4.2 bundle load (graph + subgraphs +
`BundleInfo`), seeds `cfg.Subgraphs` and `cfg.BundleIdentity`, and calls the
existing `NewEngineFromGraph`. This also gives embedded library runs the
subgraph support they lack today and is the seam to document in
`embedding.md §1`. `Run(ctx, source, cfg)` for **hand-supplied `.dip` content**
is unchanged — `"inline.dip"` anchoring stays correct for a caller-supplied
inline source with no directives.

---

## 5. Golden-trace / graph-identity safety

**The resolved `*pipeline.Graph` is identical**, for two independent reasons:

1. **The 4 built-ins are not conformance fixtures.** The golden fixtures are
   hand-authored `.dip` files under `cmd/tracker-conformance/testdata/golden/`
   (`agent_linear`, `control_flow`, `subgraph`, `manager_loop`, `interview`,
   `budget_exceeded`, `parallel_fanin`, `paused_billing`, `retry_exhausted`,
   `tool_failure`, `validation_overridden`) — none is `build_product` et al. The
   harness loads them via `LoadDippinWorkflow` from disk
   (`golden_stubs.go:118`) and runs through `NewEngineFromGraph`
   (`golden.go:123`, `:154`). **No golden fixture, and no golden regeneration,
   is touched by this change.**
2. **Inline pack is graph-preserving.** For a built-in migrated to use
   directives, the packed entry IR has the prompt/script bytes inlined into the
   same node attrs the fully-inline `.dip` would carry, so `FromDippinIR`
   emits the same nodes/edges/attrs. The migration verification (§7) proves this
   per built-in by diffing the pre- and post-migration resolved graph.

**One intended, documented behavior delta:** embedded runs today produce an
**empty** `BundleIdentity` (`run.go:154`, `BundleInfo{}`); after the switch they
carry the bundle's content-addressed `sha256:…` identity. That identity flows to
`activity.jsonl` / `run.json` / audit (`ActivityEntry.BundleIdentity`,
`embedding.md §4`) and to resume identity-matching (`commands.go:421-440`). This
does **not** touch the golden schema (goldens never assert `BundleIdentity`,
built from `Result` alone at `golden.go:201`) and does **not** change the
`embedding.md §5` graph contract. It is a net improvement — embedded runs become
content-addressed like `.dipx` runs — and must be called out in `CHANGELOG.md`
and `embedding.md §4`. The `guardPackedWorkflowDir` (#430,
`loading.go:102`) check for `${graph.workflow_dir}` in a packed run applies to
embedded bundles too: none of the 4 built-ins references `workflow_dir`
(verified by grep), so the guard is a no-op — but any built-in that later
externalizes a script must read it via `command_file:` (inlined) rather than
`${graph.workflow_dir}/script.sh`, which is unavailable in a bundle.

---

## 6. Keeping the `.dipx` in sync with the source `.dip` (drift guard)

**Recommendation: a committed `.dipx` artifact per built-in, produced by a
`make pack-builtins` target, guarded by a deterministic byte-compare wired into
the pre-commit hook and `make ci`.** Rationale for each half:

- **Committed artifact (not pure `go:generate`).** `//go:embed` requires the
  `.dipx` to exist at build time, and `go build` does **not** run
  `go generate` — so the artifact must be committed regardless. A bare
  `//go:generate` directive would silently rot. Commit the `.dipx`, and make its
  regeneration a named, enforced step.
- **`make pack-builtins` producer.** A thin target (`go run
  ./cmd/tracker-pack …` or a tiny `internal/pack` helper) that calls
  `dipx.Pack(ctx, "examples/<name>.dip", w, dipx.PackOptions{})` for each of the
  4 built-ins and writes `examples/<name>.dipx`. No new heavy binary — it is the
  `dipx.Pack` API already imported transitively.
- **Deterministic drift guard.** Because `dipx.Pack` *"produces a deterministic
  byte stream"* (`dipx.go:224`), the guard is exact: repack each source `.dip`
  to a temp buffer and byte-compare against the committed `.dipx`; any mismatch
  fails. Wire it as `scripts/docs/gate.sh`-style check invoked from the
  pre-commit hook and `make ci` (`Makefile:154`), mirroring the existing
  `make docs-check` cli-coverage gate that the project already enforces in both
  places (per CLAUDE.md *Before committing*). Add `dippin doctor
  examples/<name>.dip` on the **source** to the release checklist so the
  authored form keeps its A grade.

This makes the source `.dip` the single edited artifact; the `.dipx` is a
derived, verified-in-sync output — the same discipline as generated docs.

`go:generate` is **not** recommended as the primary mechanism (not auto-run, so
it cannot *guarantee* sync), though a `//go:generate make pack-builtins` comment
is a fine convenience alias on top of the committed-artifact + guard scheme.

---

## 7. Migration plan for the 4 embedded built-ins

Per-built-in, the mechanical steps are identical:

1. **Author** the source `examples/<name>.dip`. Optionally externalize large
   inline bodies into sibling files (`examples/<name>/…`) via
   `prompt_file:` / `system_prompt_file:` / `command_file:` /
   `prompt_prefix_file:` / `prompt_suffix_file:`, and/or a `defaults:` block
   that cascades `system_prompt_file:` to every agent. Externalization is
   **optional** — a fully-inline source packs fine; the point is that the source
   *may now* use directives.
2. **Pack** → `examples/<name>.dipx` (`make pack-builtins`, inline mode).
3. **Embed** the `.dipx` (extend the `//go:embed` list at
   `tracker_workflows.go:18-22`; keep the `.dip` embedded too under option 4.1b).
4. **Prove graph identity** (§5): capture the resolved `FromDippinIR` graph
   before and after migration and diff — must be byte-identical for a
   graph-preserving change (or the diff must be exactly the intended new
   directive/subgraph structure).
5. **Verify**: `dippin doctor examples/<name>.dip` A-grade; `go test ./... -short`;
   the §6 drift guard green; a real `tracker <name>` bare-name run from an
   arbitrary CWD resolves (the whole point).

**Recommended order** (smallest blast radius first, prove the machinery before
the hard one):

1. **`deep_review`** or **`ask_and_execute`** — smallest; first through the
   pipeline to validate pack → embed → load → run end-to-end and the drift guard.
2. **`build_product`** — the payoff case. It carries the largest inline agent
   prompts and shell-script tool bodies in the repo and is the natural first
   consumer of `command_file:` (scripts) and `prompt_file:` (agent prompts).
   Migrate the *delivery* first (pack the existing inline `.dip` unchanged →
   identical graph, prove the switch), **then** externalize bodies in a
   follow-up so the risky content move is isolated from the format switch. It is
   also the intended first `subgraph_ref:` consumer once delivery lands
   (unblocks #307 dedup / the subgraph delivery gap).
3. **`build_product_with_superspec`** — shares structure with `build_product`;
   migrate last and, where it duplicates `build_product`, move the shared body
   to a subgraph the bundle carries.

Keep `build_product`'s edge-routing invariants intact during any externalization
(CLAUDE.md *Edge routing* / *Strict failure edges*: `EscalateMilestone` /
`EscalateReview`, `fix_attempts` per-milestone circuit breaker) — externalizing
a prompt body does not touch edges, but a careless `subgraph` split could.

---

## 8. Risks

- **Temp-file materialization (approach A)** writes bundle bytes to disk on every
  embedded run and leaves a public artifact for the load window. Mitigate with
  `os.CreateTemp` mode 0600 + `defer` cleanup; prefer the upstream reader API
  (B) to eliminate it. This is a behavior change for embedded runs (a transient
  temp file where there was none).
- **`OpenWorkflow` semantics.** Under 4.1b `OpenWorkflow` stays `.dip` and a new
  `OpenWorkflowBundle` returns `.dipx`; the two embeds must not drift (the §6
  guard covers this, since it packs the very `.dip` that `OpenWorkflow` returns).
- **Binary size.** Embedding both `.dip` and `.dipx` (4.1b) roughly doubles the
  built-in payload. Negligible today (4 small workflows); reconsider 4.1a if it
  grows.
- **BundleIdentity now populated for embedded runs** changes resume
  identity-matching and audit output for embedded workflows — intended, but a
  resume of a pre-switch embedded run against a post-switch binary will see an
  identity mismatch (empty → sha256). `commands.go:400-440` already treats
  identity change as a resume abort with a `--force-bundle-mismatch` escape;
  document it.
- **Dependency on dipx pack determinism.** The drift guard's exactness relies on
  `dipx.Pack` staying byte-deterministic across dippin bumps. A dippin release
  that changes pack encoding will fail the guard on the next `make pack-builtins`
  — which is the guard working (forces intentional repack + review), but a
  bump-time chore to note in *dippin-lang updates*.
- **Subgraph externalization in `build_product`** must not violate the
  no-unconditional-fallback-to-loop-target edge rule; isolate content moves from
  the format switch (per §7 ordering).

## 9. Open questions (maintainer's call)

- **Bridge (A) vs block on upstream (B).** Ship the temp-file bridge now, or
  hold the whole feature until dippin exposes `dipx.OpenReader(ctx, io.ReaderAt,
  size)` (a 3-line wrapper over the existing unexported `openFromReader`)? (A)
  unblocks immediately; (B) is cleaner and avoids any temp file. Recommendation:
  (A) now + file the dippin issue for (B), but this is a dependency-sequencing
  decision.
- **4.1a vs 4.1b** — embed `.dipx` only (and drive `init`/header-scan through
  `dipx.Extract`) vs embed both `.dip` and `.dipx`. Recommendation 4.1b for the
  smaller, safer diff; 4.1a if binary size or single-source-of-truth wins.
- **Inline vs `NoInline` pack** — inline is required for the *run* path. Do we
  also want `tracker init` to reproduce the externalized file layout (which
  needs `NoInline` + asset extraction)? If `init` fidelity to the authored
  layout matters, that is a separate, larger `NoInline` follow-up.
- **Producer shape** — `make pack-builtins` as `go run ./cmd/tracker-pack` (a new
  tiny command, discoverable) vs an `internal/pack` helper invoked from the
  Makefile (no new user-facing binary). Both call `dipx.Pack`; the choice is
  packaging taste.
