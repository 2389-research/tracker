# `writable_paths` jail — 9-class audit checklist

Follow-up to [#285](https://github.com/2389-research/tracker/issues/285) /
[#275](https://github.com/2389-research/tracker/pull/275) /
[#272](https://github.com/2389-research/tracker/issues/272).

This is a **reviewer checklist**. When a PR touches the `writable_paths`
filesystem jail — anything under `agent/exec/jail*.go`,
`agent/exec/local.go`, `agent/exec/env.go`, or
`pipeline/handlers/codergen_jail.go` — walk it against these nine classes so
the security review is consistent instead of re-derived from PR-thread
archaeology each time.

Each class recurred across the #275 review rounds. The audit pass against
them caught a real bug (the `generate_code` / `write_enriched_sprint` jail
bypass — direct `os.WriteFile` never routed through the jailed env). The nine
classes below are the taxonomy from that sweep, each grounded in the current
jail code and residual-risk model.

## How this doc relates to the other jail docs

- **This doc** = the audit *lens* for reviewing a change to the jail — nine
  recurring failure modes, each with what to verify and what pass/fail looks
  like.
- [`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md) = the
  standing *invariant* (every `agent/tools/` tool routes filesystem mutations
  and subprocesses through `exec.ExecutionEnvironment`), its per-tool
  threat-model table, and the `make tools-jail-check` lint that enforces it.
  Class 6 and Class 7 below lean on it.
- `CLAUDE.md` → "Tool node safety", "`__jail-exec`", "Residual escape classes"
  = the operational contract and the accepted-residual list.
- [`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`](../superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md)
  = the full design + threat model.

Use the audit lens (this doc) when reviewing a diff; use the invariant doc
when adding a new tool.

## PR checklist snippet

Paste into the description of any PR touching the jail:

```
Audited against docs/architecture/writable-paths-audit-checklist.md:
- [ ] C1 symlink-blind string checks   - [ ] C2 validator/matcher divergence
- [ ] C3 EACCES vs escape classification - [ ] C4 refuse-to-start gate depth
- [ ] C5 vacuous jail tests            - [ ] C6 dropped write/Close/Exec errors
- [ ] C7 optional-hook contract        - [ ] C8 doc/signature lag
- [ ] C9 setup assumes absent state
(or note which classes are out of scope for this change)
```

---

## The nine classes

### C1 — String-based path checks blind to symlinks

**What it is.** A path check that reasons about the *string*
(`strings.HasPrefix`, `filepath.Clean`, `filepath.Join`, `filepath.Rel`)
without resolving symlinks. A string that looks contained can resolve to a
target outside the anchor through a symlinked intermediate, so the string
check passes while the write escapes.

**Example (#275).** The authoritative in-process check is *not* string-based:
`OpenForWrite` (`agent/exec/jail_linux.go`) opens the anchor dirfd and calls
`openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS`,
so a symlinked component makes the kernel return `EXDEV`/`ELOOP`. The
string-level `validateGlobEntry` / `validateExistingAncestorEscape` checks in
`jail.go` are a *backstop* over that, never the sole gate.

**Audit prompt.** For every new `HasPrefix` / `Clean` / `Join` / `Rel` check
against a path: is there an `openat2(RESOLVE_*)` / `EvalSymlinks` behind it
doing the authoritative containment test, or has the string check quietly
become the only gate?

**Pass.** Containment is decided by `openat2` under the anchor fd (or
`EvalSymlinks` before the compare). String checks are labelled as backstops.
**Fail.** A new write/delete path is admitted purely because its cleaned
string is under the anchor, with no symlink-resolving kernel check downstream.

### C2 — Validators accepting shapes the matcher won't honor

**What it is.** The validator (`validateGlobEntry` in `agent/exec/jail.go`)
and the runtime matcher (`matchOneGlob` / `path.Match`, and the Landlock
directory-prefix bounding) must agree on the glob grammar. If the validator
accepts a shape the matcher can't honor, the pattern silently never matches
(surprising denials) or bounds a broader directory than authored (surprising
grants).

**Example (#275).** Each of these was a separate bug and is now explicitly
rejected fail-closed in `validateGlobEntry`: brace patterns `{a,b}` (the
matcher never expands them), a `..` segment like `workspace/../**` (which
`path.Clean` collapses to a *broader* glob than authored), absolute / `~` /
Windows-absolute entries, and unsupported doublestar / malformed character
classes.

**Audit prompt.** For every glob shape the validator now accepts (or newly
accepts), what exactly does the matcher — and the Landlock static-prefix
bounding — do with it? Enumerate: braces, glued metachars, multiple `**`,
character classes, trailing/leading separators.

**Pass.** Every accepted shape has a matcher behavior that equals the author's
intent, and shapes the matcher can't honor are refused at validation with a
fail-closed error. **Fail.** The validator loosens to accept a shape whose
matcher/Landlock behavior is untested or diverges from the string it accepted.

### C3 — EACCES misclassified as `ErrPathEscape`

**What it is.** Kernel error sentinels must reflect what the kernel actually
signalled. Mapping an ordinary permission error to an escape sentinel (or vice
versa) corrupts both the operator-facing message and any `errors.Is`-based
routing.

**Example (#275).** In `OpenForWrite` (`agent/exec/jail_linux.go`) the
`openat2` switch maps **only** `unix.EXDEV` / `unix.ELOOP` to
`ErrPathEscape` ("write path escapes session root"). `EACCES` and every other
errno fall through to a plain wrapped `openat2 … : %w` — an ordinary
permission failure, *not* an escape. The same discipline holds in the
mkdir/parent-resolution paths.

**Audit prompt.** For each kernel errno the change handles: does the mapping
match the kernel's meaning? `EXDEV`/`ELOOP` under `RESOLVE_BENEATH` = escape;
`EACCES`/`ENOENT`/`EROFS` = ordinary permission/existence, not an escape.

**Pass.** Only boundary-violation errnos become `ErrPathEscape`; permission
errnos stay generic. **Fail.** A `default`/catch-all branch wraps every
`openat2` error as `ErrPathEscape`, so an `EACCES` reads to the operator as a
sandbox breach.

### C4 — Refuse-to-start gates firing too deep

**What it is.** A gate that refuses an unsafe configuration should fire at the
*earliest* layer that has the information — ideally the dispatcher — not deep
inside per-backend code that a given path might never reach.

**Example (#275, round 7).** `configureJail` runs only inside
`NativeBackend.Run`. For `claude-code` / `acp`, `buildRunConfig` switches
`Extra` away from `*agent.SessionConfig`, so the `writable_paths` signal is
dropped before `configureJail`'s G2 backend gate could ever fire — a node
with `writable_paths` + a non-native backend would start **unjailed**. The
fix added `refuseWritablePathsOnUnsupportedBackend` in `CodergenHandler.Execute`
(dispatcher layer, type-asserting `*NativeBackend`) so the refusal happens
before any `backend.Run`.

**Audit prompt.** For each new refuse-to-start condition: where is the
earliest layer that already has the info to refuse? Can any backend/selection
path reach "start" while skipping the gate you added?

**Pass.** The gate sits at the dispatcher (or the earliest common point) and
every selection path funnels through it. **Fail.** The gate lives inside one
backend's `Run` while another backend reaches start without passing it.

### C5 — Vacuous tests when the subprocess fails silently

**What it is.** A jail test can pass for the wrong reason: if the jailed
subprocess or write silently no-ops (wrong path, empty command, skipped on
this platform), the assertion still holds but the *contract was never
exercised*. Landlock is Linux-6.7+-only, so `jail_other.go` stubs and
build-tagged skips make this especially easy.

**Example (#275).** `jail_linux_test.go` asserts positively — an in-anchor
write **succeeds** and produces the expected file content, an out-of-anchor
write returns `errors.Is(err, ErrPathEscape)` — rather than only asserting "no
unexpected error". The property tests (`jail_property_test.go`) exercise the
matcher across generated globs so a shape can't pass by never being tried.

**Audit prompt.** Does each test prove the jail *did the thing* — the allowed
write landed with expected bytes, the denied write returned the specific
sentinel — or does it only prove an assertion didn't fire? Would the test
still pass if the subprocess silently no-oped or was skipped on the CI
platform?

**Pass.** Tests assert both a positive (allowed op observably succeeds) and a
negative (denied op returns the specific error), and platform skips are
explicit. **Fail.** A test would stay green if the jailed op quietly did
nothing.

### C6 — Short writes, `Close` errors, `ExecCommand` errors dropped

**What it is.** The jail's value is in its error returns; a dropped error
turns a refused/failed write into a silent success. `WriteFile` short writes,
`Close` errors on the jailed fd, and `ExecCommand` start/wait errors all carry
jail signal.

**Example (#275).** The `generate_code` / `write_enriched_sprint` fallback
was rewritten to *propagate* the `filepath.Rel` error rather than discard it
(see `agent-tool-jail-checklist.md` § env==nil fallback). CLAUDE.md's "Never
silently swallow errors" rule applies with force here: a swallowed jail error
is indistinguishable from an allowed write.

**Audit prompt.** Every error return in the changed write/delete/exec path —
is it checked and surfaced, or explicitly discarded with a comment justifying
why it's safe to drop? Watch `defer f.Close()` (loses the error) and `_, _ =`
on write.

**Pass.** Every write/`Close`/`ExecCommand` error is checked and surfaced (or
discarded with an explicit justification comment). **Fail.** A jailed op's
error is dropped so a denied/failed mutation reads as success.

### C7 — Optional-hook contracts not runtime-enforced

**What it is.** The jail wires optional hooks onto `LocalEnvironment`
(`WriteOpener`, `Remover`, `CommandWrapper`). These carry contracts (must be
populated together when a jail is active; must not be nil when
`WritablePathsSet` is true). A contract that's only documented, not enforced,
fails open.

**Example (#275).** `backend_native.go` **requires** `b.env` to be a
`*LocalEnvironment` when `WritablePathsSet` is true and refuses to start
otherwise — the env==nil invariant that makes the `generate_code` /
`write_enriched_sprint` `os.*` fallback safe (documented end-to-end in
`agent-tool-jail-checklist.md`). The invariant is *enforced at runtime* (a
refuse-to-start), not merely asserted in prose.

**Audit prompt.** For each hook/optional field the change relies on: is the
contract (populated-together, non-nil-when-jailed, pointer identity)
runtime-enforced with a loud failure, or only assumed? If a caller wires
`WriteOpener` but forgets `CommandWrapper`, does anything catch it?

**Pass.** Violating the hook contract triggers a refuse-to-start or panic with
a clear message. **Fail.** A half-wired env (writes jailed, subprocesses not,
or vice versa) starts and runs.

### C8 — Documentation lag on signatures and semantics

**What it is.** The jail's safety story lives across code, doc comments,
`CLAUDE.md`, the #272 spec, and these checklists. A signature or semantic
change that lands without updating its references leaves a reviewer trusting a
stale invariant.

**Example (#275).** When `configureJail`'s gate ordering / refusal points
changed (round 7 added the dispatcher-level backend refusal), the doc comment
on `configureJail` and `refuseWritablePathsOnUnsupportedBackend` in
`codergen_jail.go` were updated in the same change to describe the two-layer
gate. `CLAUDE.md`'s "`__jail-exec`" and "Residual escape classes" sections and
`agent-tool-jail-checklist.md`'s line-cited table are the other references
that must move in lockstep.

**Audit prompt.** Did any function signature, gate order, errno mapping, or
glob rule change? If so, is every referring doc comment, `CLAUDE.md` section,
spec, and checklist row updated in the *same* commit? Do line-number citations
still point at the right code?

**Pass.** Docs, comments, spec, and checklist match the new code in the same
commit. **Fail.** A comment or table still describes the pre-change signature
or gate order.

### C9 — Setup assumes state that doesn't exist

**What it is.** A jail setup function assumes some state was already
established (working_dir exists, is resolved, is absolute; the anchor dir is
present; `processCwd` is meaningful) that the caller may not have set up.

**Example (#275).** `configureJail` takes `processCwd` and passes it into
`ValidateWritablePaths` so a workspace-relative glob is validated against the
real process CWD rather than assuming one; G1 validates `working_dir` shape
and glob shape *before* the jail is wired, rather than assuming the resolved
session working_dir is already safe. The refuse-to-start gates exist precisely
because the pre-jail state can't be assumed.

**Audit prompt.** What state does the changed setup assume — resolved absolute
working_dir, existing anchor, meaningful CWD, non-nil env? Has the caller
actually established it, or is the function trusting an unvalidated input?

**Pass.** Setup validates (or is handed) the state it depends on, and refuses
loudly when it's absent. **Fail.** Setup dereferences/derives from state a
caller might not have established (empty working_dir, relative anchor, nil
env) and proceeds.

---

## See also

- [`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md) — the
  ExecutionEnvironment routing invariant, per-tool threat-model table, and
  `make tools-jail-check` lint.
- `CLAUDE.md` → "Tool node safety", "`__jail-exec`", "Residual escape classes".
- [`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`](../superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md).
- `agent/exec/jail.go`, `agent/exec/jail_linux.go`, `agent/exec/jail_errors.go`
  — validation, `openat2` enforcement, and error sentinels.
- `pipeline/handlers/codergen_jail.go` — `configureJail` gates and
  `refuseWritablePathsOnUnsupportedBackend`.
