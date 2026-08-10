# Issue #553 — Pipeline inputs: engine collect / validate / inject contract (tracker side)

**Status:** Design v1 — **Phases 1–3 + D8 shipped** (2026-08-07/10). Introspect/validate/inject, the adapter mapping, file-input staging (with `build_product` / `build_product_with_superspec` wired to a `spec` file input), `secret` inputs (#555 — staged to a 0600 file, `${inputs.<name>}` is the path only, so the value never enters prompt/wire/trace/checkpoint; `.tracker/` git-excluded), and **subgraph call-site binding (D8, #556)** are implemented on dippin ≥ v0.51 (subgraph binding requires the DIP160 arity lint, dippin ≥ v0.58). Subgraph binding validates a parent's `subgraph_params` against the child's declared value-kind inputs and seeds the child's `inputs.*`; `file`/`secret` inputs are not bindable from a subgraph call site (a params string can't be staged) and are resolved by the child itself. The declared-inputs epic is complete.

**Author:** Claude (Opus 4.8) + Clint Ecker

**Date:** 2026-08-06

**Closes:** [#553](https://github.com/2389-research/tracker/issues/553). Companion to [dippin-lang #190](https://github.com/2389-research/dippin-lang/issues/190) (grammar / IR / declaration lints).

**Dippin spec (normative for the declaration):** dippin-lang #190 owns the `inputs` grammar, IR (`ir.Workflow.Inputs`), the declaration-side lints, and the typed JSON introspection projection. Its § "Namespace", § "Types", and § "Introspection" are the source of truth for what tracker's adapter consumes.

**Likely release:** tracker minor (new public API: `Pipeline.Inputs`, `DescribeInputs`, `ValidateInputs`, `Config.Inputs`/`WithInputs`, `inputs.` expansion namespace). Two of the three phases land on the **current** dippin (v0.50, no `inputs` IR); the adapter mapping pins **dippin ≥ v0.51** — the release that carries the `inputs` block (#190 Phase 1). tracker tags first with the contract + runtime; the adapter-mapping change tags after dippin v0.51 is available.

---

## 1. Problem

A pipeline that needs a human/caller to supply data at run time has **no way to declare it**. `idea-to-pr` needs a free-text "idea"; today an author reads it out of the `goal` string or an undeclared `ctx.*` key with no schema, no prompt, no validation, no required/optional semantics. The downstream symptom (reported by tracker-runner): a run submitted with nothing proceeds anyway and the agent **invents work** — "build random stuff" — because a missing `${...}` silently expands to empty string deep in the run instead of failing at t=0.

The plumbing that exists is string-only and schema-blind:

- `PipelineContext` is entirely `string`-valued — `Get`/`Set`/`Snapshot` (`pipeline/context.go:150`).
- `Config.Context` is `map[string]string` (`tracker.go:60`), injected via `WithInitialContext` (`tracker.go:501`).
- tracker-runner unmarshals its `CONTEXT_JSON` into `map[string]string`, so a **non-string input (number/bool/nested) fails the run at start**.
- The tool_command safe-key allowlist and namespace expansion live in `pipeline/expand.go` (LLM-origin `ctx.*` keys already blocked, lines 10–22); #177 namespaces steer values under `steer.*` so they can never reach a shell.

dippin #190 adds the **declaration** (grammar + IR + declaration lints + a typed JSON projection). This spec is the **engine/runtime half**: once a pipeline declares its inputs, the tracker engine introspects the schema, validates supplied values, and injects them — typed and untrusted-aware — so a host embedder (tracker-runner) doesn't reverse-engineer context plumbing.

## 2. Goals

1. **Introspect** the declared input schema without running — `Pipeline.Inputs()` + top-level `tracker.DescribeInputs(source, format)`. This is #553's #1 ask.
2. **Validate** supplied values against the schema, standalone (callable before `Run`), returning **structured** per-input errors — required present, types coerced/checked, enum/pattern/min/max/max_length enforced, defaults applied.
3. **Inject** validated values into a dedicated, closed `${inputs.<name>}` expansion namespace — **typed** (fixing the string-only rigidity) and **untrusted by construction** (never on the tool_command safe-key allowlist).
4. **Stage file inputs** into the run's working dir so they are jail-reachable, travel with the run dir for bundle/remote-worker hand-off, and are captured by the checkpoint.
5. **Unify with subgraph call sites** — a `subgraph … params:` binding is validated against the child's declared `inputs` signature by the same code path as a top-level run.
6. **Backward compatible + robust** — a pipeline with no declared inputs behaves byte-for-byte as today; unknown kinds validate-error rather than crash; omitted optional inputs apply defaults.

## 3. Non-goals

- **The grammar / IR / declaration lints.** dippin #190 owns them. tracker consumes the parsed schema; it never parses `.dip` input syntax itself.
- **Host collection surfaces / the `needs_input` parked state.** tracker-runner #210 owns cross-surface collection (web form / Slack). The engine only provides standalone `ValidateInputs` so a host can gate *before* it calls `Run`.
- **Mid-run input fulfillment.** Inputs are the run's signature, bound before the first node executes. A `human` node does **not** satisfy an input (confirmed with dippin #190 Q4). Human gates produce `human_response`/interview answers into `ctx.*`, an orthogonal mechanism.
- **A `bundle` (multi-file) input type.** No concrete engine story; `file` + staging covers today's needs. Deferred (dippin #190 Q3).
- **Retrofitting `${vars.x}` accessors.** dippin's `vars`-has-no-accessor wart is its own issue; out of scope here.
- **Secret storage/escrow.** tracker redacts and refuses to persist secrets; it does not manage a secret store — the caller supplies the value per run.

## 4. Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **Dedicated closed `${inputs.<name>}` namespace.** Not folded into `ctx.`, not merged into `params.`. | The taint must ride on the namespace so a host cannot forget it. `ctx.` is open (any node write lands there) — folding inputs in loses the trust distinction *and* the undeclared-reference lint. `params.` is author-set (author-trusted) — mixing caller input in destroys exactly the distinction #553.4 asks us to expose. A closed namespace lets the engine keep the **entire** `inputs.*` set off the tool_command safe-key allowlist by construction (same mechanism as #177 `steer.*`). Joint decision confirmed on dippin #190. |
| D2 | **Untrusted by construction — `inputs.*` is never on the `expand.go` safe-key allowlist.** | "These values are external" becomes structural, not a convention. An author who needs an input in a shell command uses the documented write-file-then-`cat` pattern (same as LLM-origin `ctx.*`). Directly relevant to #554 (untrusted data must stay away from shell sinks). |
| D3 | **Run-start binding only; no mid-run fulfillment.** | Inputs are the run's signature. Allowing a `human` node to satisfy an input would blur inputs (fixed at bind) with mid-run interaction (`human_response`) and make the untrusted-namespace guarantee time-dependent. `needs_input` parking is pre-run in the host (#210). Confirmed dippin #190 Q4. |
| D4 | **Typed values via a canonical side-table, not a rewrite of `PipelineContext`.** `WithInputs(values, specs)` keeps a typed JSON payload the expander consults for `inputs.*`; every existing string context path is untouched. | Fixes the `map[string]string` failure mode (`${inputs.count}` renders `42` unquoted; `number = 42` condition eval works) with minimal blast radius. A full typed-context refactor would touch every handler; this is surgical. |
| D5 | **`ValidateInputs` is standalone and lives on the tracker side.** dippin lints the *declaration*; tracker validates *values*. | dippin never sees a value. A host must be able to validate a request and re-prompt precisely **before** committing a run (#553 #2 + #5). Structured `[]InputError`, never a generic failure. |
| D6 | **File inputs are staged into `<workDir>/.tracker/inputs/<name>`; the caller supplies a path OR inline bytes.** `${inputs.<name>}` binds to the staged **relative** path. | The Landlock `writable_paths` jail + `cmd.Dir=workDir` mean only in-workdir paths are reachable by a node; an out-of-workdir `SPEC.md` is invisible to a jailed tool. Staging also makes inputs travel with the run dir for `ExportBundle`/remote-worker and get captured by the checkpoint, so a resumed run needs nothing re-supplied. Inline bytes are essential for tracker-runner, where values arrive over the wire. |
| D7 | **Unknown kind: preserved verbatim in `InputSpec`, validate-error on a supplied value.** | Matches dippin's parser-records-verbatim / validator-errors layering: a `.dip` using a future kind still introspects and round-trips on an older tracker; only value-validation against the unknown kind errors. Satisfies "validate-error, not crash" *and* "don't break older consumers" honestly. |
| D8 | **Subgraph call sites share the validation path.** The subgraph handler binds the child's `inputs.*` from the parent's `params:` map and runs the same `ValidateInputs` against the child's signature. | dippin's insight: `inputs` is the callee-side signature; a top-level run and a `subgraph params:` call are one thing with two value sources. dippin's cross-file lint is the compile-time half; this is the runtime half. |
| D9 | **`secret` non-persistence is tracker's responsibility.** Redact in the activity log and `--json` stream; exclude from the checkpoint snapshot and the `ExportBundle` run dir; never stage to disk in the clear. | dippin marks the kind, refuses to inline it, and lints secret-into-`command:`. The runtime guarantee has to live where the values do. Confirmed dippin #190 Q5. |

## 5. Public contract (freeze target)

The types below are the stable surface tracker-runner builds against. The **only** piece that waits on dippin v0.51 is the adapter mapping in §6.

```go
// pipeline/inputs.go (new)

type InputKind string

const (
    InputText   InputKind = "text"
    InputNumber InputKind = "number"
    InputBool   InputKind = "bool"
    InputEnum   InputKind = "enum"
    InputFile   InputKind = "file"
    InputSecret InputKind = "secret"
)

// InputSpec is one declared input. Field names mirror dippin #190's per-input
// metadata (snake_case in the .dip / IR JSON; Go-cased here).
type InputSpec struct {
    Name        string
    Kind        InputKind
    Required    bool
    Default     string    // raw source text; coerced by Kind at validation
    Prompt      string    // what a host asks the user
    Description string
    Multiline   bool      // text
    Secret      bool      // redaction obligation (implied for InputSecret)
    Options     []string  // enum
    Pattern     string    // regex (text)
    Min, Max    *float64  // number
    MaxLength   *int      // text
}

// InputErrorKind classifies a validation failure so a host can re-prompt precisely.
type InputErrorKind string

const (
    ErrMissingRequired InputErrorKind = "missing_required"
    ErrTypeMismatch    InputErrorKind = "type_mismatch"
    ErrPattern         InputErrorKind = "pattern"
    ErrRange           InputErrorKind = "range"
    ErrLength          InputErrorKind = "length"
    ErrEnum            InputErrorKind = "enum"
    ErrUnknownKind     InputErrorKind = "unknown_kind"
    ErrUnknownInput    InputErrorKind = "unknown_input" // supplied key not declared (non-fatal by default)
)

type InputError struct {
    Name   string
    Kind   InputErrorKind
    Detail string
}
```

```go
// tracker.go — library API additions

// DescribeInputs parses source and returns its declared input schema WITHOUT
// running. Read-only sibling of Validate/Simulate. Returns nil for a pipeline
// with no inputs block.
func DescribeInputs(source, format string) ([]pipeline.InputSpec, error)

// ValidateInputs checks caller-supplied values against specs. Standalone —
// callable before Run so a host can gate on collection. Returns nil when valid.
func ValidateInputs(specs []pipeline.InputSpec, values []Input) []pipeline.InputError

// Input is a caller-supplied value. Constructors keep the call sites readable.
type Input struct {
    Name      string
    String    string // text/number/bool/enum (canonical string form)
    FilePath  string // file: an existing path to stage
    FileBytes []byte // file: inline contents to stage (mutually exclusive with FilePath)
}

func StringInput(name, v string) Input          // number/bool pass their canonical string form
func FileInput(name, path string) Input
func FileInputBytes(name string, b []byte) Input

// Config gains:
//   Inputs []Input   // caller-supplied workflow inputs, bound at run start
```

`Pipeline.Inputs() []InputSpec` is the same schema surfaced on the parsed handle for callers that already hold one.

## 6. Adapter mapping (waits on dippin v0.51)

Per the "adapter is the bridge" rule, dippin's `ir.Workflow.Inputs` → `pipeline.Graph.Inputs []InputSpec` translation lands in `pipeline/dippin_adapter.go`, the same way every IR field does. dippin's typed JSON projection (raw text + declared type, coerced) is what we read; we do **not** re-flatten to string. Until v0.51, `Graph.Inputs` is empty and every API above degrades to the no-inputs case (§8).

One function, isolated: `func inputsFromIR(wf *ir.Workflow) []pipeline.InputSpec`. Unknown kinds map to `InputKind(rawString)` (D7).

## 7. Runtime: bind → validate → inject

`pipeline.BindInputs(graph, values) (seed, error)` is folded into engine pre-run setup, analogous to `ResolveBudgetLimits`:

1. **Validate** against `graph.Inputs` (the `ValidateInputs` core). On any error, **fail closed before any node runs** with a `ConfigurationError`-class failure carrying the full `[]InputError` (hard-fail per CLAUDE.md, not a retry). This is the single biggest reliability win: a missing required input fails at t=0 with an actionable list instead of an empty-string expansion deep in the run.
2. **Apply defaults** for omitted non-required inputs. (`required` + `default` coexist: `default` is a form prefill; a *required* input with no supplied value still errors `missing_required` regardless of default — dippin #190 Q2.)
3. **Stage file inputs** (D6): write path/bytes to `<workDir>/.tracker/inputs/<name>`, validating the name like `validateRunID` (no `..`/separators), resolving through the jail's `openat2(RESOLVE_BENEATH)` session-root fd, size-capping contents.
4. **Seed the `inputs.` namespace** from the typed side-table (D4). For files, `${inputs.<name>}` = staged relative path; `${inputs.<name>.text}` = contents for small text files.

**Expansion** (`pipeline/expand.go`): add `inputs` to the namespace switch (`keysForNamespace`, `expandNextVariable`), reading typed values for rendering. Add `inputs` to the **blocked** set for tool_command interpolation — mirror the existing LLM-origin `ctx.*` block, emitting the same "references unsafe variable" error (expand.go:146) so an author who tries `command: "… ${inputs.x} …"` gets a clear failure pointing at the write-file-then-`cat` pattern.

**Subgraph** (D8): the subgraph handler maps the parent's `params:` into the child's `inputs.` namespace and calls the same `BindInputs`/`ValidateInputs` against the child graph's `Inputs`, so a required child input omitted at the call site fails the same way a top-level run does.

## 8. Backward compatibility

- No `inputs` block → `Graph.Inputs` empty → `Inputs()`/`DescribeInputs` return `nil`, `ValidateInputs(nil, …)` passes, `BindInputs` is a no-op. Behavior byte-for-byte identical to today.
- Additive on `Graph` and `Config`. `Config.Context` (`map[string]string`) stays for undeclared/legacy injection; `params.` stays the legacy undeclared pass-through path. **One rule to teach:** declared → read as `inputs.`; undeclared → the old `params.`/`ctx.` forms.
- Additive/omitempty in the dippin IR means a tracker on an older dippin still loads new `.dip` files (dippin's BC guarantee).

## 9. Security

- **Untrusted namespace (D1/D2)** — the load-bearing control. `inputs.*` never reaches a shell via `tool_command` expansion; enforced structurally in `expand.go`, tested (§10).
- **File staging (D6)** — name validated like a runID; path resolved through the jail fd (`RESOLVE_BENEATH`), so a crafted name/path can't escape the workdir; contents size-capped (reuse the tool-output ceiling constants).
- **Secret (D9)** — redaction wired into the activity-log writer and the `--json` NDJSON writer; the checkpoint serializer skips `Secret` keys; `ExportBundle` excludes `<workDir>/.tracker/inputs/<name>` for secret inputs.
- **Injection framing** — the engine exposes which context keys are input-derived (an `InputKeys()` accessor) so a host that nonce-delimits DATA preambles (tracker-runner #177) can wrap them without guessing.

## 10. Testing

- **Introspection:** `DescribeInputs` on a fixture with every kind returns the full schema; no-inputs fixture returns `nil`.
- **Validation table:** missing required (with and without default) → `missing_required`; number/bool coercion; enum ∈/∉ options; pattern match/mismatch; min/max/max_length bounds; unknown supplied key → `unknown_input`; unknown declared kind → `unknown_kind`. Assert the exact `InputError.Kind` per case.
- **Typed injection:** `${inputs.count}` (number) renders unquoted; a `number = N` edge condition evaluates true — a direct regression for the `map[string]string` failure mode.
- **Untrusted (invariant test):** `command: "echo ${inputs.x}"` fails to expand with the unsafe-variable error, for every kind. Neuter check: adding `inputs` to the allowlist makes the test fail.
- **File staging:** path input and bytes input both land under `.tracker/inputs/`; a name containing `..`/separator is rejected; staged path is workdir-relative and jail-reachable.
- **Subgraph:** a child with a required input, invoked by a parent `params:` that omits it, fails at the call site with `missing_required`.
- **Secret:** a secret value never appears in the activity log, the `--json` stream, the checkpoint snapshot, or the exported bundle.
- **BC:** golden traces unchanged for all example pipelines (none declare inputs yet).

## 11. Phasing & release coordination

- **Phase 1 (now, on dippin v0.50) — freeze + runtime that stands alone.** Ship the §5 public contract, the `inputs.` expansion namespace + untrusted-allowlist wiring, the typed side-table injection, and `ValidateInputs`. `Graph.Inputs` is empty (no IR yet), so everything degrades to the no-inputs case — but the typed-injection + untrusted-namespace machinery is exercisable via a test-only `WithInputs` and fixes the string-only rigidity on its own merit. tracker-runner builds against the frozen API.
- **Phase 2 (on dippin v0.51) — adapter mapping.** Add `inputsFromIR` (§6), pin `dippin-lang ≥ v0.51`, bump `PinnedDippinVersion`, re-run `dippin doctor` on all examples. This is the tag that makes declared inputs live end-to-end.
- **Phase 3 — file staging + secret + subgraph binding.** D6/D8/D9. Can land with Phase 2 or immediately after; the subgraph binding pairs with dippin's cross-file lint (their Phase 3).

Release sequence: tracker tags Phase 1 independently. dippin tags v0.51 with the `inputs` IR (their Phase 1). tracker then tags Phase 2 pinning dippin ≥ v0.51. Downstream: tracker-runner #210 bumps to the tracker/dippin pair that carries Phases 2–3.

## 12. Residual risks

- **Content within an allowed input.** A `file` input's *contents* are untrusted and may be read by a node; `inputs.*` keeps them out of *shell* sinks, not out of an LLM prompt (that's the point — the agent must read the spec). Prompt-injection via a malicious `SPEC.md` remains the author's/LLM's problem, framed via the DATA-preamble convention (#177), not solved here.
- **Secret in agent output.** tracker redacts the *input* value at known sinks; it cannot prevent an agent from echoing a secret it was given into its own response. Same honest limit as every secret-handling boundary.
- **Unknown-kind drift.** A `.dip` authored against a future dippin kind introspects but cannot be *validated* by an older tracker — by design (D7). A host that wants full validation pins the tracker/dippin pair.
- **Typed side-table vs. string context.** Conditions and handlers that read `inputs.*` through the string `Snapshot()` path see the string projection, not the typed value. Documented; the typed path is `inputs.*`-specific and does not promise a typed context model elsewhere.
