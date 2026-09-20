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

## Complexity gate ignores `.scratch/`

`scripts/complexity/gate.sh` excludes the gitignored `.scratch/` tree (alongside
`.worktrees/` and `.claude/`). Local experiments left in `.scratch/` used to
register as phantom "new" violations and break local `make complexity`, even
though CI (a fresh checkout with no `.scratch/`) never saw them.

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
