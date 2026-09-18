#!/usr/bin/env bash
# ABOUTME: Fixture tests for CommitScaffold.sh (#646 5a) — commits SPEC.md,
# ABOUTME: docs/execution-plan.md, docs/traceability.yaml (+ .gitignore, waivers)
# ABOUTME: BY NAME (never -A), refuses a missing file or a non-flat matrix,
# ABOUTME: is idempotent, and makes the first commit of a commitless repo.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/../build_product/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/CommitScaffold.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
last() { printf '%s' "$OUT" | tail -1; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
tracked() { G ls-files --error-unmatch -- "$1" >/dev/null 2>&1 && echo tracked || echo untracked; }
G -c init.defaultBranch=main init -q
mkdir -p "$WORK/docs"
echo spec > "$WORK/SPEC.md"; echo plan > "$WORK/docs/execution-plan.md"; echo '.ai/' > "$WORK/.gitignore"
echo secret > "$WORK/.env"   # must NEVER be swept in

# 1. Missing traceability → exit 1, nothing committed.
run
check "missing file: exit 1"           "1" "$RC"
check "missing file: names it"         "yes" "$(has 'docs/traceability.yaml is missing')"
check "missing file: no commit"        "0" "$(G rev-list --count HEAD 2>/dev/null || echo 0)"

# 2. Nested matrix → exit 1.
printf 'requirements:\n  FR-1:\n    status: pending\n' > "$WORK/docs/traceability.yaml"
run
check "nested: exit 1"                 "1" "$RC"
check "nested: flat-format message"    "yes" "$(has 'flat one-line-per-requirement format')"

# 3. Flat matrix, commitless repo → first commit with exactly the scaffold.
printf 'FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}\n' > "$WORK/docs/traceability.yaml"
run
check "commit: exit 0"                 "0" "$RC"
check "commit: marker last"            "scaffold-committed" "$(last)"
check "commit: subject"                "chore(superspec): commit spec, execution plan and traceability scaffold" "$(G log -1 --format=%s)"
check "commit: identity explicit"      "build_product_with_superspec" "$(G log -1 --format=%an)"
check "commit: SPEC tracked"           "tracked" "$(tracked SPEC.md)"
check "commit: plan tracked"           "tracked" "$(tracked docs/execution-plan.md)"
check "commit: matrix tracked"         "tracked" "$(tracked docs/traceability.yaml)"
check "commit: .gitignore tracked"     "tracked" "$(tracked .gitignore)"
check "commit: .env NOT swept in"      "untracked" "$(tracked .env)"

# 4. Idempotent: nothing staged → no new commit, still the marker.
run
check "again: exit 0"                  "0" "$RC"
check "again: already committed"       "yes" "$(has 'scaffold already committed')"
check "again: one commit"              "1" "$(G rev-list --count HEAD)"

# 5. An adjusted plan (ApprovePlan "adjust" loop) is committed again.
echo plan2 > "$WORK/docs/execution-plan.md"
run
check "adjusted: two commits"          "2" "$(G rev-list --count HEAD)"
check "adjusted: plan content"         "plan2" "$(G show HEAD:docs/execution-plan.md)"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
