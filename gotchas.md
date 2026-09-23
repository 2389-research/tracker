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

## `SetupPhase1Worktrees_test.sh` rerun assertion is rarely flaky on CI

The superspec fixture `SetupPhase1Worktrees_test.sh` (and its `SetupPhaseN`
siblings) asserts that a re-run deletes a stream branch already merged into
HEAD, logging `deleted build/stream-b (already merged into HEAD)`. The runtime
decides this with `git merge-base --is-ancestor build/stream-b HEAD`
(`lib/worktrees.sh:29`), where `build/stream-b == HEAD` — a reflexive ancestor
that must return 0.

Under `make test-race` load on the Linux CI runner this returned non-zero at
least once (v0.76.0 release commit 29cbb38, Race-detector step), so the branch
took the "unmerged → rename" path and the log line never printed:
`FAIL: rerun: merged deleted logged — want 'yes' got 'no'`.

- It's a **flake**, not a defect: the ancestry of two equal commits is
  deterministic. Re-running the same job on the identical commit passed, and the
  next commit (byte-identical test tree) was green first try. Pre-existing
  (fixture + line from #646); independent of the hermetic-env change (f81cfb6) —
  that call reads no `HOME`/git-config.
- Not reproducible locally: 60/60 under the real `-race` harness (macOS git
  2.50.1) plus thousands of samples of the raw git sequence, all clean.
- **If it reddens CI on main: re-run the failed job.** CI on main is a backstop,
  not a merge gate — don't panic-debug a green-on-rerun failure. `worktrees.sh:29`
  hides the merge-base stderr (`2>/dev/null`), so the trigger isn't captured;
  un-swallowing that is the first step if it ever needs a real fix.

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
