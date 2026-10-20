# Pause at a human gate — design

Sub-project 2a of the competitive-analysis plan ("agent-driven Tracker"). It
lets anything without a TTY — Claude Code, other agents, CI, scripts — run a
workflow that has human gates: the run pauses at the gate, a person answers
whenever they like, and the run continues. Sub-project 2b (the Claude Code
skill + `install.md`) builds on it.

Revised after an adversarial review against the code (four critical, eleven
important findings); every finding is addressed below or listed as a stated
limit.

## Why

Without the TUI, Tracker answers human gates from stdin
(`handlers.ConsoleInterviewer`). An agent's shell tool can't type into a
running process, so from Claude Code `tracker explain --param …` works but
`quickfix`'s approve / revise / reject gate — and every other gated workflow —
can't be answered. The only non-TTY options today decide without a human
(`--auto-approve`, `--autopilot`) or need a webhook receiver.

Verified building blocks: every gate mode wraps the interviewer's error with
`%w` (`pipeline/handlers/human.go`, choice / freeform / yes_no / interview),
the engine detects a `PauseError` with `errors.As` and saves the checkpoint
without completing the node, `planResume` re-enters that node, parallel
branches propagate a branch's pause after all branches finish, and a resumed
run gets a fresh wall-time clock.

## Decisions (from brainstorming)

| # | Question | Decision |
|---|---|---|
| 1 | How pause mode turns on | **Automatically when stdin isn't a terminal** and no explicit gate mode is set; `--gates console` restores reading stdin |
| 2 | How a paused gate is answered | **A `tracker answer` subcommand** (validate, record, resume; show the gate; list waiting runs) |
| 3 | A gate's `timeout:` while paused | **No deadline while paused**; `tracker answer --default` takes the default explicitly |
| 4 | A gate inside a parallel branch | **Pause after siblings finish; answering re-runs the parallel node** (siblings run again, side effects included); the fingerprint only decides whether an answer still fits its question |
| 5 | A gate inside a subgraph / `manager_loop` child | **Fails closed in pause mode for v1** (routable, with a clear message); a child's *billing* pause is fixed to propagate. Child checkpoints come with #420 |

Rejected: a live file-based gate (the answer file would be writable by the
agent under review), a local webhook relay, pre-answered gates as their own
mode, refusing parallel-branch gates, building child checkpoints now.

## What a user sees

```
$ tracker quickfix --param change="fix the README typo" < /dev/null
… Start → Preflight → NeedChange → MakeChange → RunTests → ShowDiff → ReviewDiff
⏸ Paused at gate ReviewDiff — "Review the change — approve, revise, or reject"
  options: reject (default) · revise · approve · or free text
  ## Summary … ## Diff against 1a2b3c … (the gate's full prompt)
answer with:
  tracker answer 5d82a6d50f15 approve        # or: revise / reject / "free text"
$ echo $?
4
$ tracker answer 5d82a6d50f15 approve
… ReviewDiff (answered: approve) → Commit → Done
committed to branch tracker/quickfix/5d82a6d50f15 (your checkout is unchanged)
```

An agent does the same: run, read the gate (stdout, the `--json`
`gate_paused` event, or `<run dir>/gate.json`), ask its user, run
`tracker answer`.

## 1. When pause mode is on

- **CLI.** The stdin fallback is `interactiveInterviewer(isTerminal)`
  (`cmd/tracker/run_config.go`), reached only when no explicit mode is set.
  With stdin not a terminal it returns the **pausing interviewer** instead of
  `ConsoleInterviewer`. `--gates auto|console|pause` (default `auto`):
  `console` forces today's stdin prompt; `pause` forces pause mode, and with
  `pause` the TUI isn't started (it implies `--no-tui`). `--gates` combined
  with `--auto-approve` / `--webhook-url` / `--autopilot` is a usage error,
  like today's conflicting gate flags (`flags.go`).
- **`--json` implies pause** unless `--gates` says otherwise: an agent harness
  that allocates a PTY makes stdin a terminal, and a `--json` consumer can't
  drive an inline prompt.
- **Library.** `Config.GateMode` (`""` = today's behaviour, `"pause"`,
  `"console"`). Precedence: `Config.Interviewer` > `AutoApprove` >
  `WebhookGate` > `Autopilot` > `GateMode` — the same order the library uses
  today, with `GateMode` last. `GateMode: "pause"` requires a resumable run
  (an artifact dir and the secure checkpoint) and fails at construction
  otherwise. The library never auto-detects a TTY.

## 2. At a gate

The pausing interviewer (`handlers.PausingInterviewer`) implements the
labeled-freeform, interview, `GateAware` **and the context-aware variants**
(`interviewer_context.go`). The human handler passes the gate's full
`GateInfo` — extended with a **`ScopePath`** (parallel branch ID from
`ctx.branch_id`; subgraph nesting from a ctx value `SubgraphHandler` pushes)
— on the per-gate context, so concurrent branch gates never cross wires
(`BeginGate` and `Ask*` are separate calls on one shared interviewer today).

At a gate with no answer supplied in memory (§4):

1. If the gate is inside a subgraph or `manager_loop` child, it fails closed
   (decision 5): a routable, non-retryable `OutcomeFail` — "a gate inside a
   subgraph can't pause yet; answer it interactively or use `--auto-approve`
   / `--autopilot`".
2. It builds the **pending-gate record**: run ID, node ID, scope path, mode,
   label, the **actual `Ask*` arguments** (choices exactly as passed —
   including the unlabeled-edge targets a choice gate offers — labels,
   default as passed), interview questions for `mode: interview`, the full
   untruncated rendered prompt, a random **nonce**, the time, and a
   **fingerprint**: SHA-256 over scope path, node, mode, the `Ask*` arguments
   and the prompt (for interview gates, over the question list: text,
   context, options, yes/no flag).
3. It returns `pipeline.NewPausedGateError(record)` — a `PauseError` with
   `Status: OutcomePausedGate` and the record as a typed payload.

The engine's pause path then:

- stores the record in **`Checkpoint.PausedGates`** (authoritative; the
  checkpoint lives in the secure state dir) and saves the checkpoint;
- writes **`<run dir>/gate.json`**, a non-authoritative mirror for agents
  (0600, `O_NOFOLLOW`, atomic); `tracker answer` never trusts it;
- emits a per-status terminal event — **`gate_paused`** for gates,
  `billing_paused` stays for billing (`haltForPause` emits by status, not
  always `billing_paused`) — carrying the `GateDetail` fields plus scope
  path, fingerprint and the answer command;
- does **not** emit `stage_failed`: the pause path suppresses it for the
  engine and for paused parallel branches. The human handler recognises its
  interviewer's `PausedGateError` and emits `gate_resolved` with a new
  `GateDetail.Resolution: "paused"` and no `Error`, so `gate_opened` /
  `gate_resolved` stay paired.

Several branch gates in one parallel node each produce a record; the node
returns the pause with all of them, stored as a list in
`Checkpoint.PausedGates`, and the run reports the lowest branch index first.
`tracker answer` answers one gate per invocation (`--node <scope/node>` when
more than one is waiting).

**CLI result.** `interpretRunResult` checks `result.Status == paused_gate`
before any run error and returns a typed sentinel the exit path maps to
**exit 4** ("waiting for an answer"; 0 success, 1 failure, 2 warnings /
`--fail-on-override`; `paused_billing` stays at 1). The summary says
"Paused at gate `<node>`" with no failure banner and no `tracker -r` hint;
the last line is the exact `tracker answer` command, with `-w` /
`--artifact-dir` repeated when given (built like `resumeHintTarget`).

## 3. Answering — `tracker answer`

```
tracker answer                                   # list runs waiting on you
tracker answer <run-id>                          # show the waiting gate(s)
tracker answer <run-id> <label>                  # answer and resume
tracker answer <run-id> --text "free text"       # free text (also: -- "text")
tracker answer <run-id> --default                # the gate's default
tracker answer <run-id> --answers-file a.json    # interview gates
tracker answer <run-id> --cancel                 # interview: cancel → OutcomeFail
```

**Rebuilding the run.** At the first pause the engine writes an **invocation
record** beside the checkpoint in the secure dir: the source anchor
(`SourceRef` path or built-in name) with a SHA-256 digest of the source and
its sidecars, the effective `--param` values, `-w` / `--artifact-dir`,
backend, budgets, gateway, tool-safety flags, and output mode (`--json`,
`--verbose`, `--no-tui`). `tracker answer` loads it, refuses on a digest
mismatch ("the workflow changed since the run paused"), and accepts explicit
overrides for output flags only. Without this, a resume would lose
`--param change=…` (graph attrs re-seed from the live graph) and could pick
up an edited `<run dir>/workflow.dip`.

**Validation**, per mode, against the authoritative `Checkpoint.PausedGates`
— never `gate.json`:

| Mode | Accepted | `--default` |
|---|---|---|
| choice | exactly one of the recorded choices (as passed to `Ask`, incl. unlabeled-edge targets) | the recorded default; refused if none |
| freeform with labels | a label (case-insensitive, or its DIP150 choice key, as `matchFreeformLabel` does) or `--text` free text | the recorded default; refused if none |
| freeform | `--text` | refused |
| yes_no | `yes` / `no` (case-insensitive) | refused: yes_no passes no default to `Ask` today |
| interview | `--answers-file` (below), or `--cancel` | refused |

`--answers-file` schema: `{"answers": {"<question id>": "<option or text>"}}`
keyed by `GateQuestion.ID`; every question required; an option where the
question has options, free text otherwise; a zero-question interview falls
back to freeform (as today) and takes `--text`.

**Recording and resuming.** `tracker answer`:

1. takes an exclusive **run lock** in the secure dir for the whole resume
   (a second `answer` or a `-r` of the same run is refused while it's held;
   a stale lock from a crashed process is detected by PID and replaced);
2. resolves the run the way `-r` does (`ResolveCheckpointIn`, prefixes);
3. refuses, with the reason: a run whose checkpoint has no `PausedGates`
   (names its status), an answer that fails validation (lists the valid
   ones), `--node` missing when several gates wait;
4. resumes the run **in the same process**, handing the pausing interviewer
   the answer, the scope path and the fingerprint **in memory**. Nothing is
   written to disk for the run to read back.

**Listing.** `tracker answer` with no run ID lists runs whose authoritative
checkpoint has `PausedGates` set, in the artifact dir (`-w` /
`--artifact-dir`), with gate label, node and age. Library:
`tracker.WaitingRuns(artifactDir)` reads secure checkpoints, not the
non-authoritative snapshot `ListRuns` uses.

**Library.** `tracker.Answer(ctx, AnswerInput{RunID, ArtifactDir, Node,
Label, Text, Default, InterviewAnswers, Cancel}, overrides) (*Result, error)`
— the CLI is a thin client. `Result` and `RunSummary` gain `PausedGate`
(node, scope, label); `RunManager` maps `paused_gate` like `paused_billing`.

## 4. Applying the answer on resume

On resume the engine re-enters the paused node. The pausing interviewer,
holding the in-memory answer:

- **same scope path, same node, same fingerprint** → returns the answer once
  (`gate_resolved` with the answer, actor `human`); the checkpoint save after
  the node completes clears `Checkpoint.PausedGates` (no separate file to
  delete, so a crash before that save simply re-pauses at the same gate);
- **same node, different fingerprint** → never applies the stale answer;
  pauses again with the new record, and the output says "the question changed
  since you answered — answer again";
- every other gate pauses as usual. A revise loop costs one round trip per
  gate.

A plain `tracker -r` of a paused run (no answer) re-pauses at the same gate
off a terminal and prompts at one.

**Resume fidelity.** A resume of a run whose checkpoint has `PausedGates` set
restores the context **without compaction** (`compactResumeContext` is
skipped on that marker): the run was waiting, nothing failed. Under default
fidelity compaction already keeps every key; graphs that set a lower fidelity
would otherwise re-render a different prompt and re-pause on every answer.

**Budget across answers.** The checkpoint records cumulative usage at each
save; a resume of a `PausedGates` run seeds it through `WithBaselineUsage`,
so `max_cost_cents` and the token cap bound the whole run, not each answer
(today every resume restarts the budget — unchanged for other resumes).
Wall time still restarts per segment (decision 3).

**Run status across resumes.** Audit and listing status become
**last-terminal-wins**: `statusFromActivity` (`tracker_audit_status.go`)
returns the status of the most recent run segment, so a run that paused and
was later answered reads as finished. `TerminalStatus.IsPaused()` covers
both paused statuses and replaces the `== paused_billing` checks
(`statusClassFor`, `tui/statusbar.go`, `cmd/tracker/summary.go`,
`transport/chatops/delivery.go`, `RunManager`).

## 5. Parallel branches and subgraphs

- **Parallel (decision 4):** a gate in a branch returns its pause at once;
  siblings run to completion; the parallel node returns the pause with every
  waiting record. Answering re-runs the parallel node — siblings run again,
  side effects included (tool commands, commits), stated in the docs as a cost;
  nothing here makes that re-run idempotent — and each branch gate's
  fingerprint only decides whether its recorded answer still fits its question. A branch gate whose prompt is built
  only from the pre-fan-out snapshot (a gate that is the branch target) is
  stable across re-runs; one whose prompt depends on the branch's own LLM
  work can't be, which is why gates deeper than the branch target are out of
  reach until #420 (they live in subgraphs, decision 5).
- **Subgraphs and `manager_loop` (decision 5):** a pause inside a child
  engine is fatal today — `haltForPause` returns `pe.Err` instead of `pe`, so
  the parent sees a plain handler error. This sub-project:
  - returns the `PauseError` itself from `haltForPause`, and has
    `SubgraphHandler` / the `manager_loop` child path map a paused child to
    the parent's `PauseError`, so a **billing** pause in a child pauses the
    parent (resume re-runs the subgraph node, as a billing resume re-runs
    any paused node);
  - makes a **gate** inside a child fail closed in pause mode (§2 step 1),
    since re-running the subgraph would regenerate the question on every
    answer. Interactive, auto-approve, autopilot and webhook gates inside
    subgraphs are unchanged.

## 6. Security

The pending-gate record and invocation record live in the secure state dir
with the checkpoint; `gate.json` in the run dir is only a mirror. The answer
reaches the run only in memory from a `tracker answer` the user ran, and is
matched to the recorded scope path, node, fingerprint and nonce. So a file
an agent writes into the run dir can't answer a gate, and a sibling branch
rewriting `gate.json` can't change the question `tracker answer` shows or
validates against.

Residual risk, stated the way the threat model requires: the secure state
dir moves these files out of the tool-reachable workdir, it does not make
them tamper-proof. A same-UID, unjailed agent process that knows the run ID
and `$HOME` can still read or modify the checkpoint (#213, #559); the
`writable_paths` jail is the real boundary. Operator copy must not claim a
paused gate can't be forged.

## 7. Non-goals

TUI, webhook, autopilot and `--auto-approve` behaviour; a per-gate pause
attribute (needs dippin-lang); notifications and the attention-queue UI
(sub-project 4 — this ships its data); per-branch parallel resume and child
checkpoints (#420); answering from Slack.

## 8. Testing

Test-first. Engine and handlers:

- mode selection: TTY / no TTY × `--gates` × `--json` × explicit modes, and
  the usage errors; library precedence and the construction-time refusal;
- the pausing interviewer: record fields and fingerprint (incl. unlabeled
  choice targets, interview question lists), `PausedGateError` payload,
  in-memory answer applied once, fingerprint mismatch re-pauses with the
  note, subgraph / manager_loop gate fails closed;
- engine: `paused_gate` terminal, `Checkpoint.PausedGates`, `gate.json`
  mirror, `gate_paused` event, paired `gate_opened` / `gate_resolved`, no
  `stage_failed`, exit code 4, summary text;
- `haltForPause` returns the `PauseError`; a subgraph child's billing pause
  pauses the parent and resumes;
- resume: no compaction with `PausedGates`; baseline usage seeded (a revise
  loop can't exceed `max_cost_cents`); status last-terminal-wins in audit
  and listing; plain `-r` re-pauses off a TTY;
- parallel: two branch gates → two records, lowest index first, `--node`
  required; answer re-runs the node; matching fingerprint applies, changed
  fingerprint re-pauses;
- `tracker answer`: approve / revise via `--text` / reject / `--default` on
  quickfix's ReviewDiff end to end with the fake `claude` (first-win e2e);
  every mode's validation table; `--answers-file` and `--cancel`; listing;
  every refusal; the invocation record rebuilds params (quickfix's `change`)
  and refuses on a digest mismatch; the run lock refuses a concurrent
  resume and recovers a stale one;
- security: an answer file planted in the run dir is never consumed; a
  rewritten `gate.json` doesn't change what `answer` validates;
- library `Answer` round trip and `WaitingRuns`;
- golden traces and the activity schema regenerated.

## 9. Docs

`cli.html` (`tracker answer` — required by the docs gate — `--gates`, exit
code 4), `handlers/human.md`, `transport-boundary.md` (the pausing
interviewer), `engine.md` (`paused_gate`, `Checkpoint.PausedGates`,
last-terminal-wins), README, CHANGELOG (a "Changed" entry for the non-TTY
behaviour), glossary, `AGENTS.md` / `llms.txt`.

## 10. Risks

- **Behaviour change for piped stdin** (decision 1): a script that pipes
  answers into the console prompt now pauses. Mitigation: `--gates console`,
  the CHANGELOG entry, and the paused output naming the flag.
- **Parallel re-runs cost tokens** (decision 4): documented; #420.
- **Engine pause path changes** (`haltForPause` return value, per-status
  events, suppressed `stage_failed`) touch `paused_billing` too: its tests
  are the regression net, and the golden traces show the event changes.
