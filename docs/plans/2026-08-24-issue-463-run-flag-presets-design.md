# Run-flag presets — design proposal (#463)

Status: proposal (design-only). The progressive-disclosure half of #463
(grouped `--help` / `--help-all`) shipped alongside this doc; the `--profile`
preset SET below is a product/UX call and is left for a decision before
implementation.

## Problem

`newRunFlagSet` (`cmd/tracker/flags.go`) registers ~30 run flags. Two audiences
are hurt:

- **Newcomers** hit a wall on `tracker --help` and can't tell the two flags they
  need from the twenty they don't.
- **Evaluators** read the surface as complexity, not confidence — the opposite of
  tracker's "safe to run unattended" positioning.

The shipped `--help`/`--help-all` split addresses *discoverability*. It does not
address the second, distinct ask in the issue: **bundling sane combinations
behind a named `--profile`** so an operator types one word instead of five flags.

## Why this half is design-only

A preset is a *policy* statement ("this is what an overnight run should look
like"), and the exact bundle each named profile expands to is a product call,
not a mechanical refactor. Guessing the bundles risks shipping a `--profile ci`
that quietly enables `--auto-approve` (a safety-relevant default) or budget caps
that surprise a user. The INVESTIGATE contract for this item says: when the set
is a genuine product call, propose rather than guess. This doc is that proposal.

## Proposed profiles

Three named profiles cover the observed usage modes. Each is a **default
overlay**: it sets flag values that an explicitly-passed flag still overrides
(precedence below). None of them touch a security-relevant escape hatch
(`--bypass-denylist`, `--tool-allowlist`, `--tool-denylist-add`,
`--force-bundle-mismatch` are never set by a profile — those stay opt-in).

| Flag | `interactive` (default) | `overnight` | `ci` |
|------|-------------------------|-------------|------|
| TUI | on | `--no-tui` | `--no-tui` |
| `--json` | off | off | on |
| Human gates | interactive (human) | `--autopilot mid` | `--autopilot mid` |
| `--max-wall-time` | 0 (none) | `8h` | `1h` |
| `--sleep-aware-budget` | off | on | off |
| `--fail-on-override` | off | off | on |

Notes on the choices:

- **`interactive`** is exactly today's default behavior — naming it lets `--help`
  document "what you get with no flags" and makes the set closed/complete.
- **`overnight`** favors forward progress unattended: no human at the keyboard, a
  generous but bounded wall-time cap, and sleep-aware budgeting so a closed
  laptop doesn't burn the wall clock. It deliberately does **not** set a token or
  cost cap — those are workload-specific and belong to the operator or the
  workflow's `defaults:` block, not a profile.
- **`ci`** favors deterministic, machine-readable, fail-loud runs: NDJSON on
  stdout, a tight wall-time cap, and `--fail-on-override` so a
  `validation_overridden` terminal becomes a non-zero exit the CI system catches.
  It uses `--autopilot mid` (not `--auto-approve`) so gates get a reasoned
  judgment rather than a blind yes — but see the open question below.

## Precedence

Lowest to highest, last wins:

1. Flag zero-values (Go `flag` defaults).
2. Workflow `defaults:` block (budget caps declared in the `.dip`).
3. **`--profile` overlay** (this proposal).
4. Explicit CLI flags.

So `tracker wf.dip --profile ci --max-wall-time 30m` runs the `ci` bundle but
with a 30-minute cap. This requires detecting *which* flags the user set
explicitly, which `flag.FlagSet.Visit` already provides (it visits only set
flags) — the overlay is applied to the un-visited flags after parse. That keeps
the profile from clobbering an explicit value and avoids re-deriving "was this
the default or did the user type it?" heuristically.

## Implementation sketch (when approved)

1. Add `--profile string` to `newRunFlagSet` (validated against
   `{interactive, overnight, ci}` in `validateRunConfig`, same shape as
   `validateGitFlag`).
2. After `parseArgsMultiPass`, build a `set map[string]bool` from `fs.Visit`,
   then apply the profile's overlay to each field whose flag name is absent from
   `set`. Keep the overlay table as a `map[string]func(*runConfig)` or a small
   struct-per-profile — one place, easy to audit.
3. Document the profiles in `--help` (one line) and `--help-all` (the table),
   and add a `cli.html` note. No new `commandMode`, so the docs-coverage gate is
   unaffected.
4. Tests: one per profile asserting the resolved `runConfig`, plus an
   override-precedence test (`--profile ci --max-wall-time 30m` → 30m).

Estimated surface: ~40 lines in `flags.go` + a table + tests. Low mechanical
risk; the risk is entirely in the *values*, which is why it needs sign-off.

## Open questions for the product decision

1. **`ci` gate automation** — `--autopilot mid` (reasoned, costs tokens, needs a
   provider) vs `--auto-approve` (deterministic, free, no LLM). CI often has no
   interactive budget and wants determinism → `--auto-approve` may be the better
   `ci` default. This is the single most consequential choice in the table.
2. **Wall-time defaults** — are `8h` / `1h` the right magnitudes, or should
   `overnight`/`ci` leave wall-time unset and rely on the workflow `defaults:`?
3. **Naming** — the issue floats `--profile interactive|overnight|ci`. Confirm
   `--profile` over `--preset` (the flag surface already uses `--git <policy>`;
   `--profile <name>` matches that idiom).
4. **Env/config demotion (issue's third option)** — out of scope here; a profile
   overlay subsumes most of the ergonomic win without a new config-file format.

## Recommendation

Ship the three profiles above with `ci` using **`--auto-approve`** (determinism
beats a token-spending judge in CI) and `overnight` using `--autopilot mid`.
Leave token/cost caps to the workflow or operator. Gate on a one-line product
sign-off of the table, then implement the ~40-line overlay.
