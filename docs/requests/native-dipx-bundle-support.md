# Feature Request: Native `.dipx` Bundle Support in Tracker

**From:** Pipelines team (`2389-research/pipelines`)
**To:** Tracker team
**Date:** 2026-05-11
**Priority:** Medium — enables content-addressed pipeline distribution + integrity-verified CI
**Status:** Open

---

## Summary

Dippin v0.24 introduced the `.dipx` bundle format — a deterministic, content-addressed ZIP that packages a `.dip` entry plus every transitive `subgraph ref:` file, with SHA-256 verification on open. `dippin` already accepts `.dipx` everywhere it accepts `.dip` (`validate`, `simulate`, `doctor`, `lint`, etc.). **Tracker doesn't yet** — passing a `.dipx` to any tracker command errors out as if the ZIP bytes were `.dip` source.

We'd like tracker to accept `.dipx` natively wherever it accepts a pipeline file today.

## Reproduction (tracker v0.25.0, dippin v0.24.0)

```sh
$ dippin pack -o sprint_runner_dr.dipx sprint_runner_dr.dip
$ dippin inspect sprint_runner_dr.dipx
format: 1
entry:  workflows/sprint_runner_dr.dip
identity: sha256:efb5648d28e6c250dfad5411651d427f4f62ca24e185ce6cfc51478a4c6711ab
files:
  workflows/sprint_runner_dr.dip                     sha256:0bdc227e...
  workflows/sprint_exec_dr.dip                       sha256:62e6d646...
  workflows/spec_to_sprints_dr.dip                   sha256:555b6815...
status: VALID (3 files, format_version 1)

$ tracker validate sprint_runner_dr.dipx
error[DIP001]: workflow has no start node declared
  --> <unknown>:0:0
  = help: add a start: field to the workflow
error[DIP002]: workflow has no exit node declared
  --> <unknown>:0:0
  = help: add an exit: field to the workflow
error: load pipeline: 2 validation error(s) in sprint_runner_dr.dipx
```

Tracker reads the bundle's ZIP bytes as if they were `.dip` source.

## Why This Matters

The value of `.dipx` only lands if the *runtime* understands the format:

1. **Content-addressed pinning in CI.** "We ran workflow `sha256:efb5648d…` against this client" becomes verifiable. With separate `.dip` files, you compare directory contents by hand.
2. **Integrity verification on every load.** Dippin's `dipx.Open` verifies file hashes against `manifest.json` before any content reaches the parser. Tracker users currently miss this guarantee.
3. **Single-artifact distribution.** Ship one `.dipx` per release instead of "make sure all 3 `.dip` files match." Important for our pipelines repo where one runner pulls in two subgraph files.
4. **Audit-trail provenance.** A `tracker list` entry that links a run to a bundle identity makes "which version of the pipeline produced this run" answerable without filesystem archaeology.

## Workaround in Use Today

We wrap every invocation:

```sh
#!/bin/sh
# tracker-dipx: tracker wrapper that accepts a .dipx bundle
set -eu
BUNDLE="$1"; shift
OUT=$(mktemp -d)
trap "rm -rf '$OUT'" EXIT
dippin unpack -o "$OUT" "$BUNDLE" >/dev/null
ENTRY=$(dippin inspect "$BUNDLE" | awk '/^entry:/{print $2}')
exec tracker "$OUT/$ENTRY" "$@"
```

Workable, but:
- Splits "what got run" across tracker logs + a transient temp directory
- Loses the bundle identity from the audit trail
- Rules out users dropping a `.dipx` into a step that calls `tracker` directly
- No integrity guarantee on resume (`tracker -r <run-id>` won't know the bundle hash that originally ran)

## Suggested Implementation

The bundle loader is already a separate, importable package: `github.com/2389-research/dippin-lang/dipx` exports `Pack`, `Open`, `Extract`. Suggested gate:

1. **Detect `.dipx`** by file extension (or magic bytes — ZIP signature `PK\x03\x04`) at the pipeline-load entry point.
2. **Call `dipx.Open`** to verify hashes and materialize the bundle. Two options:
   - In-memory `fs.FS` — cleanest, requires loader plumbing to accept a virtual filesystem.
   - Managed temp-dir extraction — simpler, just point existing loader at the extracted directory.
3. **Resolve `entry`** from `manifest.json` and proceed with the normal load path.
4. **Log bundle identity** at run start so `activity.jsonl` and `tracker list` carry the SHA-256 alongside the run id.

Affected commands (all flow through one pipeline-load entry point):
- `tracker <pipeline>`
- `tracker validate`
- `tracker simulate`
- `tracker doctor`
- `tracker -r <run-id> <pipeline>` (resume)
- `tracker audit` (when associating a recorded run to its pipeline)

The dippin-lang spec notes (from the v0.24 launch post):

> "The Go library encodes this in the type system: the parser only accepts `verifiedBytes`, an unexported wrapper that the verification step is the only place allowed to construct."

So tracker gets parse-safety automatically by going through `dipx.Open` instead of reading bytes directly.

## Bonus: Bundle Identity in Audit Trail

If the loader logs the bundle's content-addressed identity to the run's `activity.jsonl` (and surfaces it in `tracker list`), we get bundle-pinned audit trails for free:

```
$ tracker list
  Run ID          Status    Duration   Bundle
  ──────          ──────    ────────   ──────
  aff6262f85bb    ok        2h45m      sha256:efb5648d28e6c2...
  5668fef9e3f6    FAIL      1h37m      sha256:efb5648d28e6c2...
  41d1fb8bc75d    FAIL      1h16m      sha256:a13f0c8c11ee9d...   ← different bundle
```

"Run `5668fef9e3f6` was executed against bundle `sha256:efb5648d…`" becomes verifiable across machines without filesystem archaeology.

## What We Can Offer

Happy to PR if there's interest and an approach you'd prefer (in-memory `fs.FS` vs. managed temp-dir extraction). We've been using `.dipx` packaging on top of our `_dr` pipeline fork for a few weeks and the rough edges with tracker integration are well-mapped.

## References

- Dippin v0.24 launch post: https://dippin.org/blog/whats-new-v024
- Dippin source for the bundle loader: `github.com/2389-research/dippin-lang/dipx` (package `dipx`)
- Existing `dipx`-aware commands in dippin: `cmd/dippin/cmd_pack.go`, `cmd_unpack.go`, `cmd_inspect.go`
