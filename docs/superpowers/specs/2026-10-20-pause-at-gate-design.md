# Pause at a human gate — design

Sub-project 2a of the competitive-analysis plan ("agent-driven Tracker"). It
lets anything without a TTY — Claude Code, other agents, CI, scripts — run a
workflow that has human gates: the run pauses at the gate, a person answers
whenever they like, and the run continues. Sub-project 2b (the Claude Code
skill + `install.md`) builds on it.

## Why

Without the TUI, Tracker answers human gates from stdin
(`handlers.ConsoleInterviewer`). An agent's shell tool can't type into a
running process, so from Claude Code `tracker explain --param …` works but
`quickfix`'s approve / revise / reject gate — and every other gated workflow —
can't be answered at all. The only non-TTY options today are deciding without
a human (`--auto-approve`, `--autopilot`) or standing up a webhook receiver.

The engine already has the parts: an interviewer is the documented seam for
answering gates (`docs/architecture/transport-boundary.md` §2), a
`PauseError` returned by an interviewer already reaches the engine's pause
path (the human handler wraps interviewer errors with `%w`; the engine
detects pauses with `errors.As`, as `paused_billing` does, #487), parallel
branches already propagate a branch's pause, checkpoints and `tracker -r`
already resume, and a resumed run gets a fresh wall-time clock.

## Decisions (from brainstorming)

| # | Question | Decision |
|---|---|---|
| 1 | How pause mode turns on | **Automatically when stdin isn't a terminal** and no explicit gate mode is set; `--gates console` restores reading stdin |
| 2 | How a paused gate is answered | **A `tracker answer` subcommand** (validate, record, resume; show the gate; list waiting runs) |
| 3 | A gate's `timeout:` while paused | **No deadline while paused**; `tracker answer --default` takes the default explicitly |
| 4 | A gate inside a parallel branch | **Pause after siblings finish; answering re-runs the parallel node** (siblings run again), made safe by the fingerprint check |

Rejected along the way: a live file-based gate (the answer file would be
writable by the agent under review), a local webhook relay (most moving parts,
needs a background process), pre-answered gates as a mode of their own (they
fall out of answering), refusing gates in parallel branches.

## What a user sees

```
$ tracker quickfix --param change="fix the README typo" < /dev/null
… Start → Preflight → NeedChange → MakeChange → RunTests → ShowDiff → ReviewDiff
⏸ Paused at gate ReviewDiff — "Review the change — approve, revise, or reject"
  options: reject (default) · revise · approve · or type free text
  ## Summary … ## Diff against 1a2b3c … (the gate's full prompt)
answer with:
  tracker answer 5d82a6d50f15 approve        # or: revise / reject / "free text"
$ echo $?
4
$ tracker answer 5d82a6d50f15 approve
… ReviewDiff (answered: approve) → Commit → Done
committed to branch tracker/quickfix/5d82a6d50f15 (your checkout is unchanged)
```

An agent does the same: run, read the gate (from stdout, `--json`'s
`gate_paused` event, or `<run dir>/gate.json`), ask its user, run
`tracker answer`.

## 1. When pause mode is on

- The CLI's only stdin fallback is `interactiveInterviewer(isTerminal)`
  (`cmd/tracker/run_config.go`), reached when no explicit mode is set:
  `--auto-approve`, `--webhook-url` and `--autopilot` win, as today. With
  stdin not a terminal it now returns the **pausing interviewer** instead of
  `ConsoleInterviewer`.
- `--gates auto|console|pause` (default `auto`): `console` forces today's
  stdin prompt (for anyone piping answers in), `pause` forces pause mode even
  at a terminal (e.g. a TUI-less session that wants to answer later). The TUI
  is unchanged: a terminal with no `--no-tui` keeps the TUI and its gates.
- Library: `Config.GateMode` (`""` = today's behaviour, `"pause"`,
  `"console"`); an explicit `Config.Interviewer` still wins. The library does
  not auto-detect a TTY (it has no stdin of its own).

## 2. At a gate

The pausing interviewer (`handlers.PausingInterviewer`, implementing the
labeled-freeform, interview and `GateAware` family):

1. Receives the gate exactly as other transports do (`BeginGate` +
   `Ask*`): node, mode, options, default, resolved prompt.
2. Looks for a recorded answer for this gate (§4). If one matches, returns it.
3. Otherwise writes **`<run dir>/gate.json`**: run ID, gate node ID and its
   scope path (parallel branch / subgraph), mode, label, options with
   meanings, default, the rendered prompt, interview questions when
   `mode: interview`, the time, and a **fingerprint** — SHA-256 over node
   path, mode, options, default and prompt. Written atomically, 0600,
   `O_NOFOLLOW`.
4. Returns `pipeline.NewPauseError(OutcomePausedGate, …)`.

The engine's existing pause path checkpoints and ends the run with terminal
status **`paused_gate`** (new, beside `paused_billing`). The CLI:

- prints the gate (prompt rendered as plain text, options, the default) and,
  last, the exact `tracker answer` command;
- exits **4** — "waiting for an answer", distinct from success (0), failure
  (1) and warnings / `--fail-on-override` (2);
- the summary says "Paused at gate `<node>`" instead of a failure banner.

`--json` and every library event stream gain **`gate_paused`**, carrying the
`GateDetail` the `gate_opened` event already has plus the fingerprint and the
answer command. `gate_opened` / `gate_resolved` pairing is preserved: a pause
emits `gate_resolved` with `resolution: paused` so no gate looks stuck open.

## 3. Answering — `tracker answer`

```
tracker answer                                   # list runs waiting on you
tracker answer <run-id>                          # show the waiting gate again
tracker answer <run-id> <label | "free text">    # answer and resume
tracker answer <run-id> --default                # take the gate's default
tracker answer <run-id> --answers-file a.json    # interview gates
```

- Resolves the run the same way `-r` does (`-w`, `--artifact-dir`,
  `ResolveCheckpointIn`; a unique prefix works).
- Refuses, with the reason and what to do: a run that isn't `paused_gate`
  (names its real status); a label that isn't an option (lists them); free
  text at a choice / yes_no gate; a malformed `--answers-file`; a missing or
  corrupt `gate.json` (names the path); a second answer before the run
  resumes (`--replace` overrides).
- Records the answer in **`<run dir>/gate-answer.json`** — the answer, the
  fingerprint it was given for, the time — 0600, `O_NOFOLLOW`, then resumes
  the run (same pipeline, same run ID, gate mode `pause`) in the foreground
  with the same output mode.
- Listing: every run under the artifact dir whose status is `paused_gate`,
  with its gate label and age — the seed of the attention queue
  (sub-project 4).
- Library: `tracker.Answer(ctx, AnswerInput{RunID, ArtifactDir, Answer,
  Default, InterviewAnswers, Replace}, cfg) (*Result, error)` and
  `tracker.WaitingRuns(artifactDir)`; the CLI is a thin client.
  `RunSummary` / `Result` gain `PausedGate` (node, label, fingerprint);
  `RunManager` maps `paused_gate` like `paused_billing`.

## 4. Applying the answer on resume

On resume the engine re-enters the paused gate node (the paused node never
completed). The pausing interviewer, constructed with the recorded answer:

- **same node path and same fingerprint** → returns the answer once
  (`gate_resolved` with the answer, actor `human`), deletes
  `gate-answer.json`, and the run continues;
- **same node, different fingerprint** (the question changed — a parallel
  re-run regenerated it, or context changed) → never applies the stale
  answer; writes the new `gate.json`, pauses again, and the output says "the
  question changed since you answered — answer again";
- any later gate pauses as usual. A revise loop costs one round trip per gate.

Binding is by node path + fingerprint because a `GateID` is random per
presentation (`newGateID`). The fingerprint is what makes decision 4 safe and
what makes a planted answer useless: an answer only ever applies to the exact
question it was given for, and only in a resume the user launched.

**Resume fidelity.** A gate's prompt must render the same after a resume, or
every resume would re-pause. Resume compaction (`compactResumeContext`) can
blank context a prompt interpolates. The plan verifies the built-in gates
render identically after pause → resume (quickfix ReviewDiff interpolates
`ctx.tool_stdout` and `ctx.node.RunTests.tool_stdout`) and, where they don't,
either restores those keys (`isResumeStructuralKey`) or fingerprints the
structural parts only (node, mode, options, default) plus the prompt *as
recorded at pause time* — the interviewer then re-shows the recorded prompt
rather than the re-rendered one. The plan picks one with a test.

## 5. Parallel branches and subgraphs

- **Parallel:** a gate in a branch pauses that branch; siblings finish; the
  parallel node returns the pause (existing #487 path). Answering resumes the
  run, which re-runs the parallel node — siblings run again (stated in the
  docs as a cost) — and the gate's fingerprint check decides whether the
  recorded answer applies. Proper per-branch resume is #420.
- **Subgraphs:** `pipeline/subgraph.go` has no pause handling today, so a
  pause inside a child engine — a gate, or `paused_billing` — likely surfaces
  to the parent as a failure. This sub-project must make a child's paused
  terminal propagate as the parent's `PauseError` and resume re-enter the
  child from its checkpoint. The plan first writes the test that shows
  today's behaviour.

## 6. Non-goals

- Changing the TUI, webhook, autopilot or `--auto-approve` behaviour.
- A per-gate attribute for pause behaviour (needs dippin-lang).
- Notifications and the attention-queue UI (sub-project 4; this ships its
  data: `paused_gate`, `gate.json`, `tracker answer` listing).
- Per-branch parallel resume (#420).
- Answering from Slack (trackerbot already has an in-process interviewer).

## 7. Testing

Test-first throughout. Engine and handlers:

- auto-detection matrix: TTY / no TTY × `--gates` × explicit modes;
- the pausing interviewer: writes `gate.json` (fields, 0600, no symlink
  follow), returns `PauseError(paused_gate)`; with a matching recorded answer
  returns it once; with a mismatched fingerprint re-pauses with the
  "question changed" note;
- engine: `paused_gate` terminal, checkpoint saved, exit code 4,
  `gate_paused` event, `gate_opened`/`gate_resolved` paired;
- resume fidelity for every built-in gate (render identically after resume);
- parallel: a gate in a branch pauses after siblings finish; answer re-runs
  the node; matching fingerprint applies, changed fingerprint re-pauses;
- subgraph: a child gate pause propagates and resumes into the child (and a
  child `paused_billing` too);
- `tracker answer`: approve / revise with free text / reject / `--default`
  on quickfix's ReviewDiff end to end with the fake `claude` from the
  first-win e2e; interview `--answers-file`; listing; every refusal path;
  `--replace`;
- library `Answer` round trip and `WaitingRuns`;
- golden traces and the activity schema regenerated for the new event.

## 8. Docs

`cli.html` (`tracker answer` entry — required by the docs gate — `--gates`,
exit code 4), `handlers/human.md`, `transport-boundary.md` (the pausing
interviewer as a transport), `engine.md` (the `paused_gate` terminal), README
(answering gates without a terminal), CHANGELOG, glossary, and the agent
files `AGENTS.md` / `llms.txt` (an agent can now run gated workflows).

## 9. Risks

- **Behaviour change for piped stdin** (decision 1): a script that pipes
  answers into today's console prompt now pauses. Mitigation: `--gates
  console`, a CHANGELOG "Changed" entry, and the paused output naming the
  flag.
- **Resume fidelity** (§4): mitigated by the per-gate render test.
- **Parallel re-runs cost tokens** (decision 4): documented; #420 is the fix.
- **Subgraph propagation** (§5) is the least-known part; the plan starts with
  its characterization test.
