# Gotchas

Distilled, team-readable notes. Add your own entries; don't regenerate the file.

## Herdr agent-state reporting

Herdr (herdr.dev) is a terminal **pane manager**, not an HTTP/webhook target.
Agents report state by shelling out to the herdr binary — there is no network API.

- Pane env vars herdr injects: `HERDR_ENV=1`, `HERDR_PANE_ID`, `HERDR_BIN_PATH`,
  `HERDR_SOCKET_PATH`. Report only when `HERDR_ENV=1`.
- CLI: `"$HERDR_BIN_PATH" pane report-agent "$HERDR_PANE_ID" --source custom:tracker --agent tracker --state <working|idle|blocked> --seq <n>`; `--message` for a block; `--seq` strictly increasing (stale is ignored). Release: `pane release-agent ...` with **no `--seq`**.
- Tracker's integration: package `herdr/` (`Reporter` implements `pipeline.PipelineEventHandler`), wired via `attachHerdr` in `cmd/tracker/run_config.go` — composed into `cfg.EventHandler` with `pipeline.PipelineMultiHandler` at both `run()` and `runTUI()`, released on defer. `herdr.Detect` is a total no-op outside a pane; `TRACKER_HERDR=0` opts out inside one.
- `blocked` is reported ONLY for human gates. Autopilot / `--auto-approve` / `--webhook-url` never block the pane (nobody is waiting). `humanGates` is fixed at construction from the interviewer selection, because a gate's Actor isn't known at `gate_opened`.
- Open gates are tracked as a **set of GateIDs**, so parallel-branch gates don't flip the pane back to `working` until all resolve.
- Top-level finish = `TerminalStatus != "" && !strings.Contains(NodeID, "/")`. A scoped `parent/child` terminal is a subgraph child hitting the budget guard — it must NOT report idle.
- Best-effort: runner errors are swallowed, 2s timeout per call, no shell (args can't be injected by gate text). A failing herdr binary never fails the run.

## Local gates ignore `.scratch/`

`scripts/complexity/gate.sh` excludes the gitignored `.scratch/` tree (alongside
`.worktrees/` and `.claude/`). `make fmt` and `make fmt-check` prune the same
three trees through the Makefile's `LIST_GO_FILES`, and the `.pre-commit` hook
script runs `make fmt-check`. Local experiments left in `.scratch/` used to
register as phantom "new" violations and break local `make complexity`, and a
gofmt-dirty file there failed `make ci` at its first step and blocked commits
through the hook script, even though CI (a fresh checkout with no `.scratch/`)
never saw them. `make fmt` could also rewrite files in other agents' worktrees.

- A new gate that walks the tree must skip these three trees too. In the
  Makefile, reuse `LIST_GO_FILES` rather than a bare `gofmt .` or `find .`.

## Local `dippin` CLI can lag the pinned `dippin-lang` module

The `dippin` binary on `PATH` (built from your local dippin-lang checkout) and the
`dippin-lang` Go module in `go.mod` are two separate versions. When the binary is
older, the release gate `dippin doctor examples/build_product.dip` fails to *parse*
a field the pinned library supports — e.g. `error: unrecognized agent field
"writable_paths_mode"` (typed since dippin-lang v0.75.0; used by build_product's
`FinalCommit`). This is a stale-binary artifact, **not** a pipeline defect.

- Do NOT `go install` dippin to "fix" it (Critical Rule — it clobbers your local
  build). Update the local checkout's binary instead, or verify another way.
- Verify pipeline health through the shipping library: `tracker validate
  examples/<f>.dip` and `tracker simulate examples/<f>.dip` use the pinned
  `dippin-lang`, plus `go test ./cmd/tracker-conformance -run TestGoldenTraces`.
  If those pass and the `.dip` files are unchanged since the last verified tag
  (`git diff <tag> HEAD -- examples/*.dip`), the pipelines are fine.

## Fixture suites: never `printf | grep -q` (or `| head`) under `pipefail` (#658)

The superspec fixture `SetupPhase1Worktrees_test.sh` failed on CI three times
with `FAIL: rerun: merged deleted logged — want 'yes' got 'no'` although the
line it looks for was in the output. The earlier note here blamed a flaky
`git merge-base --is-ancestor` and prescribed re-running the job. That was
**wrong**: the runtime was fine; the *assertion* was lossy.

Root cause, reproduced on Linux (ubuntu 24.04, bash 5.2) in the triage:

- The suites run `set -uo pipefail` and asserted with
  `has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }`.
- bash's builtin `printf` flushes at every newline: `strace` shows **4
  `write(2)` calls for the 5-line, 353-byte `OUT`** of the rerun step.
- `grep -q` exits on its FIRST match. If the needle is on an early line, the
  producer's next `write` hits a closed pipe → SIGPIPE → exit 141 → under
  `pipefail` the pipeline is 141 → `|| echo no`. A present needle reads as
  absent. Below pipe capacity the race is load dependent: **52/60,000
  helper-level false negatives and 5/720 suite-level failures at 12-way
  parallelism; 0/60,000 and 0/720 after the fix** (`case "$OUT" in *"$1"*)`
  or `grep -F … >/dev/null` without `-q`). At or above pipe capacity it is
  deterministic on every platform. macOS rarely shows it (bash 3.2 buffers
  differently), which is why "not reproducible locally" was so convincing.

What exists now so this class cannot come back:

- `examples/scripts/build_product/test_helpers.sh` and
  `examples/scripts/dotpowers/test_helpers.sh` ship pipe-free assertion
  helpers — `has` / `has_line` / `has_re` (and `contains` / `has_line_in` /
  `has_re_in` for another haystack, `first_line` / `last_line` instead of
  `| head -1` / `| tail -1`). All 134 `| grep -q` sites and the 8 latent
  `| head -N` sites in the 46 suites were migrated.
- `make shell-check` (`scripts/shell/gate.sh pipe-consumers`; pre-commit hook
  and CI) fails any pipefail script that pipes into `grep -q/-l/-L/-m`,
  `head`, `sed … q`, `read` or `cmp -s` unless the line ends in `|| true` or
  carries `# pipefail-ok: <reason>`.
- `TestExampleScripts` and `make test-scripts` run every suite under
  `scripts/shell/pipe-stress.bash` (`BASH_ENV`), which makes printf/echo emit
  one write per line with a yield between — a `printf | grep -q` assertion
  then fails *every* run, not one in a few hundred
  (`TRACKER_SCRIPT_PIPE_STRESS=0` opts out). `TestPipeStressPreambleReproducesSIGPIPE`
  proves the amplifier bites.
- Runtime scripts run via `sh -c` without pipefail, so their `| head` sites
  were never affected; `lib/build-context.sh` now carries `|| true` on each
  anyway so a future pipefail caller cannot be aborted by a long listing.

If a fixture reddens CI with a `want 'yes' got 'no'` on text that is plainly
in the log, do not re-run the job: look for a pipe into an early-exiting
consumer (the gate lists the shapes) and reach for the helpers.

## `dippin doctor` grades only its first file

`dippin doctor` and `dippin lint` take one workflow (`usage: dippin doctor
[--extra-models spec] <file>`) and ignore any extra file arguments without a
word. `dippin doctor a.dip b.dip c.dip` prints `a.dip`'s report card, never
mentions the other two, and exits 0: it looks like a three-pipeline gate and
checks one. Measured with dippin 0.76.0.

- Run `make doctor`. It calls doctor once per core pipeline at the
  dippin-lang version pinned in `go.mod` and fails on any grade below A.
  `make lint` (`scripts/dippin/gate.sh`) loops per file too.
- Older plan docs under `docs/` show the multi-file form. Don't copy it.

## Checking a gate or TUI change against main

To show a change to `tui/` or the human handler leaves behavior alone, build
main and the branch, then drive one gate-only `.dip` (no LLM: human nodes, each
followed by a tool node that echoes `${ctx.human_response}` or
`${ctx.interview_answers}`) through both binaries with the same
`tmux send-keys` script, in the TUI and with `--no-tui`.

- Normalize before diffing. Strip the `\x1f\x1e` sentinel
  (`LC_ALL=C tr -d '\037\036'`), drop `ts`, `run_id`, `gate_id` and
  `context_snapshot` from `activity.jsonl`, and drop `run_id` and `timestamp`
  from `checkpoint.json`. The rest should match byte for byte.
- Screens also differ in durations, clock times, the run ID and the banner
  tagline, which `cmd/tracker/branding.go` picks at random. Mask those.
- `--no-tui` gates are inline Bubble Tea programs and pane history keeps their
  old frames, so wait on the visible pane (`tmux capture-pane -p`) or on the
  `gate_opened  node=<ID>` log line.
- Not a regression: when a `--no-tui` timed gate expires, its inline program
  keeps running (Mode 1 ignores the gate context), the terminal stays raw, and
  the closing summary prints as a staircase. main has the same bug.
