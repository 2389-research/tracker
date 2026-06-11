# Fresh-Eyes Review Fixes — 2026-06-10

Full-project review (7 parallel subagent reviewers, findings personally verified).
Branch: `review/fresh-eyes-fixes`. 36 raw findings → 19 confirmed fixes, 3 policy
questions held for Doctor Biz, rest documented as skips/false-positives.

## Phase A — LLM stream errors (CLAUDE.md: never silently swallow errors)

| ID | File | Bug | Fix |
|----|------|-----|-----|
| A1 | `llm/anthropic/adapter.go` | `handleSSEData` switch has no `case "error"` — mid-stream `overloaded_error`/`rate_limit_error` events silently dropped | add error case; map error type → HTTP status → `llm.ErrorFromStatusCode` |
| A2 | `llm/google/adapter.go` + `translate.go` | `geminiResponse` has no `Error` field; `{"error":{...}}` chunks unmarshal to empty struct and vanish | add `Error *geminiAPIError`; emit typed error in `processSSELine` |
| A3 | `llm/openai/adapter_sse.go` | `handleSSEError` emits only when payload parses; unparseable error payload → nothing | else-branch emits `EventError` with raw payload context |
| A4 | `llm/openaicompat/adapter.go` | `tryEmitSSEError` requires `Message != ""`; error with only code/type falls through as normal chunk | fire on any of Message/Code/Type; synthesize message |
| A5 | `llm/stream.go` | `processToolCallStart` overwrites in-flight call — interleaved Start(0),Start(1),End(0),End(1) (openaicompat) loses tool 0 | finalize active call at top of `processToolCallStart` |

## Phase B — pipeline core

| ID | File | Bug | Fix |
|----|------|-----|-----|
| B1 | `pipeline/transforms.go:41` | `ExpandGraphVariables` map iteration + `ReplaceAll`: `$target` vs `$target_name` prefix collision, nondeterministic | iterate keys sorted by descending length |
| B2 | `pipeline/expand.go:276` | `InjectParamsIntoGraph` clone drops `DippinValidated` (consulted by validate.go:132, validate_semantic.go:33) and never populates adjacency maps (appends `clone.Edges` directly) | copy flag; build edges via `clone.AddEdge` |
| B3 | `pipeline/engine_run.go:822` | `handleRetryExhausted` fallback branch skips `emitCostUpdate`+budget check → ceiling overshoot | mirror the retry path's emit+check |

~~B4 (commitWIPBeforeRouting warning on non-git runs)~~ — DROPPED: `TestEngine_CommitWIP_NoGitAdapterWarns`
encodes this as intentional #302 behavior (graceful skip with actionable message). Not a bug; UX call
for Doctor Biz if the warning proves noisy.

## Phase C — CLI / library

| ID | File | Bug | Fix |
|----|------|-----|-----|
| C1 | `tracker_resolve.go:85` | library `isExplicitFilePath` missing `.dipx` (CLI has it) | add extension + test |
| C2 | `cmd/tracker/update.go:440` | fixed `.tracker-new` temp name + `os.Create` — concurrent updates corrupt | `os.CreateTemp(destDir, ".tracker-new-*")` |
| C3 | `cmd/tracker/update.go:291` | fixed `.tracker-update-test` probe name, same class | `os.CreateTemp` + remove |
| C4 | `cmd/tracker/main.go:139` | `printUpdateHint` (≤2s block) runs before error print on failed runs | only hint when `err == nil` |
| C5 | `tracker_doctor.go:330-379` | doctor probes use lax `ResolveProviderBaseURL` → doctor green where run hard-fails (`ErrGatewayRouteRefused`) | use `ResolveProviderBaseURLStrict`, surface error |

## Phase D — handlers / agent / TUI

| ID | File | Bug | Fix |
|----|------|-----|-----|
| D1 | `pipeline/handlers/backend_acp.go:277,295` | initSession failure: `killProcess` never `Wait()`s → zombie + leaked pipe fds | `_ = proc.cmd.Wait()` after the two initSession killProcess sites (NOT inside killProcess — waitForProcess has concurrent Wait) |
| D2 | `pipeline/handlers/webhook_interviewer.go:256` | POST uses `context.Background()` — ignores Cancel() | ctx wired to `w.canceled` |
| D3a | `pipeline/handlers/human.go:913` | `executeChoice` lacks nil-graph guard (siblings have it) | add guard |
| D3b | `agent/session.go:320` | error msg reports N retries, N+1 empties occurred | `maxEmptyResponseRetries+1` |
| D4 | `tui/search.go:220-250` | `HighlightLine`: byte offsets from `lowerPlain` sliced into `plain`; `ToLower` changes UTF-8 widths → panic/garble | lowered→plain offset map |
| D5 | `agent/session_run.go:316` | dangling tool_use on restricted-tool early return — VERIFY reachability first; skip if tools stripped from request |

## Phase E — aux binaries / examples

| ID | File | Bug | Fix |
|----|------|-----|-----|
| E1 | `cmd/tracker-conformance/main.go:1030` | dead `available` filter loop (result discarded; comment says full list intended) | remove dead code |
| E2 | `cmd/tracker-swebench/agent-runner/main.go:226` | `classifyTerminationReason`: DeadlineExceeded → "tool_error" | map to "timeout" |
| E3 | `examples/ask_and_execute.dip` | `printf "$RESULTS"` format-injection; `printf "applied: $WINNER"`; FinalVerify `retry_target: ImplementClaude` re-enters torn-down worktrees | `printf '%b'` / `'%s'`; drop retry_target (exhaustion → EscalateToHuman) |
| E4 | `examples/build_product.dip:~875` | TestMilestone reads `fix_attempts` without numeric guard (ContinueWithMoreTurns has it) | add same guard |

## Phase F — discovered during verification

| ID | File | Bug | Fix |
|----|------|-----|-----|
| F1 | `pipeline/handlers/backend_claudecode_env_test.go:84` | `TestBuildEnvPreservesNonAPIKeys` leaks `PATH=/usr/bin` + `HOME=/test/home` via raw `os.Setenv` (no restore) — on macOS `sh` is in `/bin`, so every later subprocess-spawning test in the package fails (`TestExecute_BreachGreen_AdvancesAsSuccessWithMarker` flaked suite-wide, passed isolated). Pre-existing on main (confirmed via stash). | convert all `os.Setenv`/bare `os.Unsetenv` in the file to `t.Setenv` (+ register-restore-then-unset idiom for the Noop test) |
| F2 | `agent/exec/env_test.go:249,267,299,302` | three jail-hook tests hardcode `/bin/true` — absent on macOS (`/usr/bin/true` only), so `agent/exec` fails on every Mac. Pre-existing (shipped with #272; my diff touches nothing in `agent/exec`). | `"sh", []string{"-c", "true"}` — PATH-resolved, matches the file's existing idiom |
| F3 | `examples/build_product.dip:904` | `paste -sd'\|'` with no file operand — BSD/POSIX paste requires one (GNU defaults to stdin), so TestMilestone's SKIP_PATTERN breaks at runtime on macOS and `TestMilestoneKnownFailuresSkipPreserved` fails. Shipped in 726f7d3 (#345); line 1244 already does it right (`paste -sd, -`). | add the `-` stdin operand |

## Held for Doctor Biz (security policy — 🔴 ask-first)

1. `agent/tool_safety.go` denylist gaps: `bash <(curl …)` process substitution; backtick command-substitution gadgets.
2. `cmd/tracker/update.go` proceeds with warning when `checksums.txt` missing — hard-fail option.

## Documented skips (verified not-bugs or out-of-scope polish)

- `agent/session.go:309` empty-response trigger includes `TotalToolCalls()==0` — matches CLAUDE.md spec ("0 prior tool calls"), intentional.
- `manager_loop` pollTimer drain branch — unreachable today, zero behavioral risk.
- TUI P2 cosmetics: capPartialText wide-rune overflow, HighlightLine ANSI styling loss, modal zero-dim, autopilot flash race, choiceRunner ordering fragility.
- `dippin_adapter` parenthesized-condition handling — only reachable programmatically, not from `.dip`.
- Reviewer claims rejected: replaceContextValues dirty-marking (clean), parallel fan-in dup (by design).
