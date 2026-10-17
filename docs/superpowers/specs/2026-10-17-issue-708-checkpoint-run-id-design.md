# Issue #708 — checkpoint path elements are validated on resume

Process: the freeze-and-prove pattern (`docs/architecture/security-pr-process.md`),
sized to a small fix. Approved scope: "validate the checkpoint's RunID on load
and refuse to resume if it fails, test-first, through the security-PR process"
(Doctor Biz, 2026-10-17). The audit sweep below found one more checkpoint field
in the same class, which this change also covers.

## Threat

On resume the engine loads a checkpoint and adopts `cp.RunID` as the run's ID
(`loadCheckpointAndMerge`, `pipeline/engine_run.go`). The run ID has no other
outside source: a fresh run uses `generateRunID()`. Every artifact path joins
the ID under the artifact base, and none of those joins validates it:

- `TRACKER_RUN_DIR` (`seedRunIdentity`, `pipeline/engine_run_identity.go`), whose
  `MkdirAll` runs before the first node since #686 (v0.80.0);
- the checkpoint snapshot (`engine_checkpoint.go`) and the legacy checkpoint
  path (`engine_run.go`);
- the git-artifacts repo (`engine_run.go`) and the events snapshot
  (`events_jsonl_snapshot.go`);
- resume compaction's response reads (`fidelity.go`), `Result.ArtifactRunDir`
  (`tracker.go`) and run capture (`tracker_capture.go`).

The secure builders (`SecureActivityLogPath`, `SecureCheckpointPath`,
`SecureTurnCheckpointPath`) already call `validateRunID`. So a checkpoint whose
`RunID` is `../x` makes the engine create directories, and later write files,
outside the artifact base.

Who can supply that checkpoint: resume reads the secure copy first (#696). It
falls back to the artifact-dir copy under the work tree, which tool nodes and
agents can write, and an explicit `--checkpoint` / `Config.CheckpointDir` path is
used as given.

The sweep found a second checkpoint field used in a path. Resume compaction
(`compactSummaryHigh`) reads `<artifactDir>/<runID>/<nodeID>/response.md` for
every `cp.CompletedNodes` entry, so a node ID like `../../x` reads a
`response.md` from outside the run directory into the run's context.

## Invariants

1. A resumed run's ID passes `validateRunID` (one path element, no separator,
   not `.` or `..`). Otherwise the resume fails before any node runs and before
   any directory is created.
2. Resume compaction reads a response file only for a completed-node ID that is
   a single path element. It skips any other ID.

## Public API

No change. A rejected resume returns an error from `Engine.Run` / `tracker.Run`
that wraps the validation failure and names the checkpoint's run ID.

## Audit sweep

| Checkpoint field | Reaches a path or command? | Status |
| --- | --- | --- |
| `RunID` | every artifact-dir join above | addressed (invariant 1) |
| `CompletedNodes` | `compactSummaryHigh` response reads | addressed (invariant 2) |
| `CurrentNode`, `EdgeSelections`, `GateStates`, `HaltedAt` | graph lookups and routing, not paths | not applicable |
| `RetryCounts`, `RestartCount(s)` | counters | not applicable |
| `WIPRefs` | recorded and shown to the user; never passed to git | not applicable |
| `Context` | restored into the run context | not covered; see below |

## Not covered (residual risks)

- A same-UID process that can write the secure checkpoint can still change its
  contents. This is the documented run-state integrity residual
  (`docs/architecture/engine.md`, run-state integrity).
- Context values restored from a checkpoint stay trusted. The tool_command
  allowlist (`human_response`, `interview_answers`, `branch_id`, …) interpolates
  them unquoted, so a tampered checkpoint can still steer a later tool command.
  That is the checkpoint's trust model, not path construction, and it is raised
  separately rather than changed here.

## Tests

- `TestResumeRejectsCheckpointRunIDOutsideBase`: `../x`, `a/b`, `a\b`, `..`,
  `.`, `/abs`. Each run fails, no node runs, and nothing is created outside the
  artifact base.
- `TestResumeRunIDNeverEscapesArtifactBase` (rapid): for generated IDs built from
  `.`, `/`, `\` and `a`, the resume either fails (when `validateRunID` rejects
  the ID) or runs with its directory inside the base. Nothing is ever created
  outside the base.
- `TestCompactSummaryHighSkipsCompletedNodeIDsOutsideRunDir`: a valid node's
  response is still read, and a traversal ID's planted `response.md` is not.
