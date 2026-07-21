# Phase 1 — Safe by Construction: Source Containment, Authz, Budget, Lifecycle

**Date:** 2026-07-20
**Status:** proposed
**Ship:** 1 (source containment) / 2 (authz, budget, reaper)
**Depends on:** nothing hard; complements Phase 0
**Related:** transport-boundary security review

## Problem

`cmd/trackerbot` accepts untrusted input from any Slack channel member and turns it into
paid, host-side pipeline execution. Three classes of control are missing or partial:
**what may run** (source containment), **who may run it** (authorization), and **how much
it may consume** (budget + disk lifecycle). Where possible, the control belongs at the
**boundary** (so a future web/mobile transport inherits it), not only in trackerbot.

## Findings

### F1.1 — Path-traversal: grammar fallback loads an arbitrary `.dip` (CONFIRMED, HIGH)
`cmd/trackerbot/intent.go` → `runner.go:67` → `tracker.ResolveSource`. The LLM intent
resolver validates the workflow against the catalog (`inCatalog`), but the deterministic
grammar fallback (used with `--backend claude-code` / no API key) passes the workflow
token **verbatim**. `ResolveSource` treats any name with a path separator or `.dip`
suffix as a filesystem path — `filepath.Join(workDir, name)` with no containment, and
absolute paths used as-is (verified at `tracker_resolve.go:31-40`). So
`@trackerbot run ../../../etc/hosts` or `run /abs/evil.dip` loads and executes
attacker-chosen files.

### F1.2 — No authorization on starting runs (CONFIRMED, HIGH — policy)
`cmd/trackerbot/slack.go`. Any member of any channel the bot is in can trigger paid work
or DoS the capacity cap. The origin plan lists "workspace/user allowlist + mandatory
per-run Budget" as a guardrail; Budget is wired, the **allowlist is not built**. Framed
as a known-intended control that isn't implemented yet, not an oversight.

### F1.3 — Run workdirs never cleaned (CONFIRMED, MEDIUM)
`runner.watch` removes only `checkpoint.json` on completion; the per-thread workdir
(artifacts, agent scratch) accumulates unboundedly under `RunsBase`.

### F1.4 — Cross-user gate answering within a thread (CONFIRMED, MEDIUM — policy)
`SlackInterviewer.Resolve` routes any inbound interaction for a thread's pending gate,
regardless of which user clicked/typed — so any thread participant can answer another's
gate. Acceptable for some teams; should be a policy hook, not hard-coded.

### F1.5 — Untrusted `${params.*}` reach prompts (CONFIRMED, LOW — mitigated)
Params parsed from mention text flow to `Config.Params`. Mitigated: no built-in
interpolates `params.*` into `tool_command` (the safe-key allowlist in CLAUDE.md blocks
LLM-origin `ctx.*`; `params.*` is author-controlled but here author == Slack user), and
undeclared param keys are rejected by `ApplyGraphParamOverrides`. Residual: a param value
can shape a prompt. README caveat suffices; no code change required.

## Tasks

### T1.1 — Source containment at the resolver (scalable) + trackerbot opt-in
Add a strict/catalog-only resolution mode to the library rather than only guarding in
trackerbot, so every transport that resolves user free-text is safe. Two options,
pick during implementation:
- (a) `ResolveSourceStrict(name, workDir)` that rejects path-shaped names (separators /
  `..` / absolute / `.dip` suffix) and resolves **only** local-by-bare-name + built-in
  catalog; **or**
- (b) a `SourceResolver` policy value on `RunnerDeps` / `Config` with a "catalog + bare
  local names only" default.

trackerbot's runner validates the resolved workflow name against
`^[A-Za-z0-9_-]+$` **before** `ResolveSource`, covering both the LLM and grammar
resolvers uniformly (the ~8-line guard drafted during review). Reject with a helpful
message pointing at `runs`/built-ins.
- Files: `tracker_resolve.go` (new strict entry), `cmd/trackerbot/runner.go` (guard).

### T1.2 — Pluggable `Authorizer` seam
Interface `Authorizer{ Allowed(user, channel string) bool }` checked in `OnMention`
before a run starts, reusable by a future web transport. trackerbot supplies an
env-driven allowlist (`TRACKERBOT_ALLOWED_USERS`, comma-separated; empty = open, but
log a loud "unrestricted" warning at startup so open-by-default is a visible choice).
- Files: `cmd/trackerbot/runner.go` (+ new `authz.go`), `main.go` (wire env).

### T1.3 — Fail-closed per-run Budget for chat-triggered runs
Require a per-run `Budget` (tokens/cost/wall-time) for any run started from a mention;
if the base config sets none, apply a conservative default and post it in the ack so the
cost ceiling is visible. Paid-work-from-chat must never run unbounded.
- Files: `cmd/trackerbot/runner.go:launch`, `main.go` (default budget env).

### T1.4 — Workdir lifecycle / reaper
On terminal, after delivery, remove (or archive then remove) the per-thread workdir under
a retention policy (`TRACKERBOT_RETAIN=<n runs|duration>`), plus a startup sweep of
orphaned dirs with no live run and no store record. Keep artifacts long enough for
delivery + `Diagnose`.
- Files: `cmd/trackerbot/runner.go:watch` + new sweep in `main.go`.

### T1.5 — Gate-answer authorization hook
Optional `GateAuthorizer` so a deployment can restrict who may answer a thread's gate
(default: any thread participant, preserving today's behavior).
- Files: `cmd/trackerbot/interviewer.go`.

## Verification / gates

- `go build ./...`, `go test ./... -short` green.
- New tests: path-shaped names (`../x`, `/abs`, `x.dip`) rejected before `ResolveSource`;
  a non-allowlisted user is refused; a run without an explicit budget gets the default;
  the reaper removes a completed run's dir but not a live one.
- `dippin doctor` A-grade unaffected; `make complexity` green.

## Out of scope

- Slack Socket Mode transport internals (`slack.go`) — unchanged; safety lives above the
  `ThreadUI` seam.
- Sandboxing the pipeline's own tool execution — already covered by tracker's tool-node
  safety + `writable_paths` jail (CLAUDE.md); this phase is about the *bot's* front door.
