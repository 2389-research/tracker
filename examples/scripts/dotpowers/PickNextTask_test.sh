#!/usr/bin/env bash
# ABOUTME: Fixture tests for PickNextTask.sh (#646 item 4) — task ids are
# ABOUTME: anchored on `task-N:` so task-1 never matches task-10..19, and a
# ABOUTME: missing/empty plan.md is a loud `no_tasks_found` (routable to the
# ABOUTME: help/recover node), never `all_complete` → ship.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/PickNextTask.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
PLAN="$WORK/docs/plans/plan.md"
mkdir -p "$WORK/docs/plans"

# 1. Missing plan.md -> no_tasks_found, exit 0 (routes to HumanHelp/AutoRecover),
#    NOT all_complete (which would route to ValidateBuild -> reviews -> ship).
run
check "missing plan exit 0"        "0" "$RC"
check "missing plan marker"        "no_tasks_found" "$OUT"
check "missing plan diagnostic"    "yes" "$(grep -q 'plan.md' "$STATE/stderr" && echo yes || echo no)"

# 2. Empty plan.md -> same.
: > "$PLAN"
run
check "empty plan marker"          "no_tasks_found" "$OUT"

# 3. task-1 done, task-10 open: the old unanchored `^- \[ \] task-1` matched
#    task-10 too; the anchored form picks task-10 exactly once and never
#    re-picks a finished task.
cat > "$PLAN" <<'PLAN'
# Plan
- [x] task-1: scaffold
- [ ] task-10: the tenth thing
- [ ] task-11: the eleventh thing
PLAN
run
check "picks task-10 not task-1"   "next_task-task-10" "$OUT"
check "writes current_task_id"     "task-10" "$(cat "$WORK/docs/plans/current_task_id.txt")"

# 4. Prose mentioning a task id must not be picked (only checkbox lines count).
cat > "$PLAN" <<'PLAN'
See task-3 for context.
- [x] task-1: a
- [x] task-2: b
- [ ] task-3: c
PLAN
run
check "picks first open checkbox"  "next_task-task-3" "$OUT"

# 5. All checked -> all_complete.
cat > "$PLAN" <<'PLAN'
- [x] task-1: a
- [x] task-10: b
PLAN
run
check "all complete"               "all_complete" "$OUT"
check "all complete exit 0"        "0" "$RC"

# 6. implementer-status.txt is cleared on every pick.
: > "$WORK/docs/plans/implementer-status.txt"
run
check "status file cleared"        "no" "$([ -f "$WORK/docs/plans/implementer-status.txt" ] && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
