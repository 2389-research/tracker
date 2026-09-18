#!/usr/bin/env bash
# ABOUTME: Fixture tests for MarkTaskComplete.sh (#646 item 4) — flipping
# ABOUTME: task-1 to [x] must not also flip task-10..19 (anchored on
# ABOUTME: `task-N:`), and a task id absent from the plan fails loud.
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
SCRIPT="$(stage_script "$DIR/MarkTaskComplete.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
PLAN="$WORK/docs/plans/plan.md"
mkdir -p "$WORK/docs/plans"

# 1. Completing task-1 leaves task-10 and task-11 open.
cat > "$PLAN" <<'PLAN'
- [ ] task-1: first
- [ ] task-10: tenth
- [ ] task-11: eleventh
PLAN
printf 'task-1' > "$WORK/docs/plans/current_task_id.txt"
run
check "exit 0"                     "0" "$RC"
check "marker"                     "completed-task-1" "$OUT"
check "task-1 checked"             "yes" "$(grep -q '^- \[x\] task-1: first' "$PLAN" && echo yes || echo no)"
check "task-10 still open"         "yes" "$(grep -q '^- \[ \] task-10: tenth' "$PLAN" && echo yes || echo no)"
check "task-11 still open"         "yes" "$(grep -q '^- \[ \] task-11: eleventh' "$PLAN" && echo yes || echo no)"
check "no .bak left behind"        "no" "$([ -f "$PLAN.bak" ] && echo yes || echo no)"

# 2. Idempotent: marking an already-checked task is fine.
run
check "idempotent exit 0"          "0" "$RC"
check "still exactly one x"        "1" "$(grep -c '^- \[x\] task-1:' "$PLAN")"

# 3. A task id that is not in the plan fails loud (never a silent no-op that
#    lets PickNextTask re-pick the same task forever).
printf 'task-99' > "$WORK/docs/plans/current_task_id.txt"
run
check "unknown task exit 1"        "1" "$RC"
check "unknown task message"       "yes" "$(grep -q 'task-99' "$STATE/stderr" && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
