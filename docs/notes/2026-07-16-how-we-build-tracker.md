# What's actually working here — how we build Tracker

*A reflection on the development methodology behind Tracker, written 2026-07-16.
Honest rather than flattering, because the honest version is more useful — and
because a few of these techniques only work because we don't oversell them.*

## Design is gated before code — always

Nothing we ship goes straight to implementation. The #348 goal-gate fix went
spec → review → plan → execution, and even a two-line `.dip` edge change gets a
scoping decision first. This feels slow in the moment and is the single biggest
reason the diffs stay small and the rework stays near zero. The discipline is
baked into the setup (the superpowers skills, `CLAUDE.md`, the review squad):
a system that refuses to let implementation start before there's a written
thing to disagree with.

The payoff is concrete. The #348 design got written down, and *then* five
independent reviewers tore at it — and one found a genuine critical bug (a
never-run gate would be wrongly "covered"). That bug was cheap to fix in a
markdown spec. It would have been a shipped defect if we'd coded first and
reviewed after.

## Adversarial review is a first-class step, not a courtesy

The pattern shows up over and over: confidence alone isn't allowed. When the
escape-aware condition parser landed, an adversarial reviewer brute-forced the
decode against a reference implementation over 88,000+ inputs before we trusted
it. When the #348 design was ready, a five-expert panel reviewed it from
different lenses — control-flow, checkpoint/resume, audit-integrity, simplicity,
tests — and the *disagreements between them* were the value (one said "no Clear
method needed," another proved you need one for looping workflows; the second
won on merit).

"I don't review, squads review" changed how this works. It moves judgment from a
single fallible reviewer (or a skimmed diff) to a panel whose consensus gets
*acted on* rather than re-litigated. That only works because the reviews are
real — they cite `file:line`, construct failing inputs, and are told explicitly
not to have their findings pre-judged.

## Verification is the through-line

Every commit goes through the pre-commit hook; not once do we take the
`--no-verify` escape, even when it costs a decomposition detour. CI gets watched
to green on every push. Releases aren't "done" when the PR merges — they're done
when `gh release view` shows published assets and the live site actually serves
the new version.

And claims get verified, not just outputs. When a subagent reported "DONE" but
the diagnostics said a method was undefined, git ground truth settled it (the
diagnostic was stale). When another came back "DONE_WITH_CONCERNS" having quietly
changed a reproduction test, reading the actual test first showed the change was
correct and fixed a bug *in the plan*. Treating every report as an unverified
claim is tedious, and it's exactly what keeps the review layers from becoming
theater.

## Refusing to guess

The SWE-bench empty-patch investigation is the cleanest example of the debugging
discipline paying off by *not* producing a fix. Trace the data flow, rule out
the extraction plumbing and the path-convention hypothesis by reading code, then
stop — because the remaining answer lives in a diagnostic artifact, and the
honest move is to ask for it rather than ship a plausible-but-unfounded change.
"No fixes without root cause" sounds rigid until you've watched guess-and-check
thrash for hours.

## Guardrails you can lean on — including the ones that bite

The complexity ratchet, the filesize baseline, the lint rules: these catch real
things and also *cost* real time (the pre-commit hook, with no baseline
awareness, blocked the same class of edit three times this session). The honest
read is that the friction is mostly worth it — it forced burning down actual
debt (decomposing a 44-complexity `main()`, two grandfathered lint functions) as
a side effect of unrelated work. But the papercut also got recorded to memory so
the next session doesn't rediscover it, and the root-cause fix got flagged rather
than silently absorbed. Guardrails you're allowed to critique are better than
guardrails you route around.

## How the collaboration works

A few things that make this effective, from the receiving end:

- **Delegation with trust and terse direction** — "keep going," "go go go," a
  bare "1" on a fork. The destination is set; execution is delegated, which is
  where the leverage is.
- **The calls that are the human's get made quickly** — scope, direction,
  close-or-keep — via a structured choice, and then not re-litigated.
- **Corrections are sharp and structural.** When a subagent used
  `--amend --no-verify` early on, that didn't become a one-off scold; it became a
  hard rule in every subsequent dispatch. Corrections turn into system changes,
  not repeated reminders.
- **The human keeps the work honest about what's shipped** — noticing when the
  site is three releases stale, pushing for the actual release and not just the
  merge.

The short version: a system where design is forced before code, review is
adversarial and delegated, verification is non-negotiable, and tradeoffs get
surfaced rather than hidden — driven with high-trust, low-ceremony direction.
The techniques matter, but they mostly work because the environment makes the
disciplined path the path of least resistance. That's the real trick.
