#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/build-context.sh (seed_build_context, #298) —
# ABOUTME: the seeded orientation file's shape (header, HEAD label, capped
# ABOUTME: sections, `## Milestones landed` LAST so MarkMilestoneDone's append
# ABOUTME: lands under it), Setup re-seed replacing a stale copy, and the
# ABOUTME: best-effort contract: no git / no .ai/build can never fail Setup.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../test_helpers.sh"
# A driver that sources the lib exactly as Setup does (set -eu, POSIX sh) and
# prints a trailer AFTER the seed — a `set -e` abort inside the helper would
# drop the trailer, which is the "can never fail Setup" contract (#298).
cat > "$STATE/driver.sh" <<DRIVER
set -eu
. "$LIB_DIR/build-context.sh"
seed_build_context
echo "seeded rc=\$?"
DRIVER
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$STATE/driver.sh") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
CTX="$WORK/.ai/build/build-context.md"
# section NAME — the body lines of the `## NAME` section (up to the next `## `).
section() { awk -v h="## $1" '$0==h{f=1;next} /^## /{f=0} f' "$CTX" | sed '/^$/d'; }
fhas() { grep -qF -- "$1" "$CTX" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
reset() { rm -rf "$WORK"; mkdir -p "$WORK/.ai/build"; }

# 1. Seeding when absent, in a directory that is not a git repo at all:
#    the skeleton is still written, labelled "no commits", every section
#    header present in order, and `## Milestones landed` is the LAST line
#    (MarkMilestoneDone appends its `## Milestone N` entries after it).
reset
run
check "non-git: exit 0"                  "0" "$RC"
check "non-git: trailer (no set -e abort)" "seeded rc=0" "$(last)"
check "non-git: nothing on stderr"       "" "$(cat "$STATE/stderr")"
check "non-git: file created"            "yes" "$([ -f "$CTX" ] && echo yes || echo no)"
check "non-git: header line"             "# Build Context (machine-written — do not edit by hand)" "$(head -1 "$CTX")"
check "non-git: no-commits label"        "yes" "$(fhas '_Architecture map as of Setup (no commits).')"
check "non-git: section order"           "Top-level layout|Languages|Entry points|Key interfaces (best-effort: Go / TS / Rust)|Milestones landed" "$(grep '^## ' "$CTX" | sed 's/^## //' | paste -sd'|' -)"
check "non-git: Milestones landed LAST"  "## Milestones landed" "$(tail -1 "$CTX")"
check "non-git: layout empty"            "" "$(section 'Top-level layout')"
check "non-git: entry points empty"      "" "$(section 'Entry points')"
check "non-git: interfaces empty"        "" "$(section 'Key interfaces (best-effort: Go / TS / Rust)')"

# 2. Idempotence: a second seed of the same tree is byte-identical.
cp "$CTX" "$STATE/first.md"
run
check "re-seed: exit 0"                  "0" "$RC"
check "re-seed: byte-identical"          "yes" "$(cmp -s "$CTX" "$STATE/first.md" && echo yes || echo no)"

# 3. Git repo with commits: short-SHA label; layout is top-level dirs (with a
#    trailing slash) + top-level files, sorted, deduped; languages counted by
#    extension, most common first; entry points by name / under cmd/; the
#    interface grep finds Go / TS / Rust declarations; UNTRACKED files never
#    appear (git ls-files is the source of truth, not the working tree).
reset
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/cmd/app" "$WORK/pkg/store" "$WORK/web/src" "$WORK/core/src"
printf 'package main\n' > "$WORK/cmd/app/main.go"
printf 'package store\n\ntype Store interface {\n\tGet() string\n}\n' > "$WORK/pkg/store/store.go"
printf 'package store\n' > "$WORK/pkg/store/other.go"
printf 'export interface Props { a: string }\n' > "$WORK/web/src/index.ts"
printf 'pub trait Render {}\npub(crate) trait Paint {}\ntrait Hidden {}\nfn main() {}\n' > "$WORK/core/src/main.rs"
printf 'module x\n' > "$WORK/go.mod"
printf '# readme\n' > "$WORK/README.md"
G add -A; G commit -q -m base
SHORT="$(G rev-parse --short HEAD)"
mkdir -p "$WORK/untracked"; printf 'package main\n' > "$WORK/untracked/main.go"
run
check "git: exit 0"                      "0" "$RC"
check "git: short-sha label"             "yes" "$(fhas "_Architecture map as of Setup ($SHORT).")"
check "git: layout"                      "README.md|cmd/|core/|go.mod|pkg/|web/" "$(section 'Top-level layout' | LC_ALL=C sort | paste -sd'|' -)"
check "git: layout dedupes pkg/"         "1" "$(section 'Top-level layout' | grep -cx 'pkg/')"
check "git: languages most-common first" "3 go" "$(section 'Languages' | head -1 | sed 's/^ *//')"
check "git: languages counts"            "1 md|1 mod|1 rs|1 ts|3 go" "$(section 'Languages' | sed 's/^ *//' | LC_ALL=C sort | paste -sd'|' -)"
check "git: entry points"                "cmd/app/main.go|core/src/main.rs|web/src/index.ts" "$(section 'Entry points' | paste -sd'|' -)"
check "git: Go interface found"          "yes" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | grep -q '^pkg/store/store.go:3:type Store interface {' && echo yes || echo no)"
check "git: TS interface found"          "yes" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | grep -q '^web/src/index.ts:1:export interface Props' && echo yes || echo no)"
check "git: Rust pub trait found"        "yes" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | grep -q '^core/src/main.rs:1:pub trait Render' && echo yes || echo no)"
check "git: Rust pub(crate) trait found" "yes" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | grep -q '^core/src/main.rs:2:pub(crate) trait Paint' && echo yes || echo no)"
check "git: Rust private trait found"    "yes" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | grep -q '^core/src/main.rs:3:trait Hidden' && echo yes || echo no)"
check "git: interface count"             "5" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | wc -l | tr -d ' ')"
check "git: untracked file absent"       "no" "$(fhas 'untracked')"
check "git: Milestones landed LAST"      "## Milestones landed" "$(tail -1 "$CTX")"

# 4. The append-on-milestone-done shape: MarkMilestoneDone appends
#    `\n## Milestone N: title\nFiles: …\nSummary: …` to the file, so the
#    entry must land under `## Milestones landed` and be findable by the
#    `^## Milestone N` grep the later agents / MarkMilestoneDone_test use.
{ echo; echo "## Milestone 1: Scaffold"; echo "Files: go.mod"; echo "Summary: base"; } >> "$CTX"
check "append: entry after landed header" "## Milestones landed|## Milestone 1: Scaffold" "$(grep -n '^## Milestone' "$CTX" | sed 's/^[0-9]*://' | paste -sd'|' -)"
check "append: entry is the last section" "Files: go.mod|Summary: base" "$(section 'Milestone 1: Scaffold' | paste -sd'|' -)"

# 5. Setup re-seed REPLACES a stale copy (a prior run's map + milestone log):
#    the map is frozen at Setup, and reset_plan_state deliberately keeps the
#    file only because Setup rewrites it — a stale milestone log must not
#    survive into a new plan (a new run starts at Setup with an empty done/).
run
check "re-seed: exit 0"                  "0" "$RC"
check "re-seed: stale milestone gone"    "no" "$(fhas '## Milestone 1: Scaffold')"
check "re-seed: fresh map"               "## Milestones landed" "$(tail -1 "$CTX")"

# 6. Caps keep the file SHORT (#298 §4): 40 layout lines, 15 languages,
#    20 entry points, 20 interfaces — a 60-package / 30-main tree is cut.
reset
G -c init.defaultBranch=main init -q
i=1; while [ "$i" -le 60 ]; do
  mkdir -p "$WORK/cmd/tool$i" "$WORK/p$i"
  printf 'package main\n' > "$WORK/cmd/tool$i/main.go"
  printf 'package p\ntype I%s interface{}\n' "$i" > "$WORK/p$i/p.go"
  printf 'x\n' > "$WORK/f$i.ext$i"
  i=$((i + 1))
done
G add -A; G commit -q -m many
run
check "caps: exit 0"                     "0" "$RC"
check "caps: layout 40"                  "40" "$(section 'Top-level layout' | wc -l | tr -d ' ')"
check "caps: languages 15"               "15" "$(section 'Languages' | wc -l | tr -d ' ')"
check "caps: entry points 20"            "20" "$(section 'Entry points' | wc -l | tr -d ' ')"
check "caps: interfaces 20"              "20" "$(section 'Key interfaces (best-effort: Go / TS / Rust)' | wc -l | tr -d ' ')"
check "caps: Milestones landed LAST"     "## Milestones landed" "$(tail -1 "$CTX")"

# 7. Commitless git repo: the label is "no commits", not an error from
#    rev-parse (--verify --quiet prints nothing), and the tree is still
#    mapped from the index.
reset
G -c init.defaultBranch=main init -q
printf 'package main\n' > "$WORK/main.go"; G add -A
run
check "commitless: exit 0"               "0" "$RC"
check "commitless: no-commits label"     "yes" "$(fhas 'as of Setup (no commits).')"
check "commitless: no fatal on stderr"   "" "$(cat "$STATE/stderr")"
check "commitless: staged file mapped"   "main.go" "$(section 'Entry points')"

# 8. Best-effort: .ai/build absent (Setup's scaffold did not run) — the
#    redirect fails, but the helper returns 0 under set -e and writes
#    nothing: it can never dead-stop Setup. The shell's own redirect error
#    (naming the path) still reaches stderr — `2>/dev/null` sits AFTER the
#    failing `>` so it never applies — which is the right side of "never
#    silently swallow": tool_stderr says why the map is missing.
rm -rf "$WORK"; mkdir -p "$WORK"
run
check "no .ai/build: exit 0"             "0" "$RC"
check "no .ai/build: trailer printed"    "seeded rc=0" "$(last)"
check "no .ai/build: no file"            "no" "$([ -e "$CTX" ] && echo yes || echo no)"
check "no .ai/build: stderr names path"  "yes" "$(grep -q 'build-context.md' "$STATE/stderr" && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
