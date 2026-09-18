# ABOUTME: Shared plan.md task bookkeeping for the dotpowers-family workflows.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/dotpowers/lib.
#
# plan.md lines look like `- [ ] task-N: title` (ValidatePlanFormat enforces
# the `task-N:` shape). Every match here is anchored on the FULL id plus the
# colon — `task-1:` — because a bare `task-1` prefix-matched task-10..19
# (#646 item 4): completing task-1 flipped task-10/11 to [x] (never
# implemented) and picking re-selected finished tasks.
#
# Routing markers are the ONLY stdout (the .dip edges compare
# ctx.tool_stdout with `=`); diagnostics go to stderr.

PLAN='docs/plans/plan.md'

# pick_next_task — print `next_task-task-N` (and record the id in
# docs/plans/current_task_id.txt), `all_complete` when every task-N: line
# is checked, or `no_tasks_found` when plan.md is missing/empty/has no task
# checkboxes. All three exit 0: no_tasks_found routes to the workflow's
# HumanHelp/AutoRecover node — it must never degrade to all_complete, which
# routes to ValidateBuild → reviews → ship with nothing built.
pick_next_task() {
  rm -f docs/plans/implementer-status.txt
  if [ ! -s "$PLAN" ]; then
    echo "pick_next_task: $PLAN is missing or empty — nothing to pick" >&2
    printf 'no_tasks_found'
    exit 0
  fi
  total=$(grep -c '^- \[.\] task-[0-9][0-9]*:' "$PLAN" 2>/dev/null || true)
  case "$total" in ''|*[!0-9]*) total=0 ;; esac
  if [ "$total" -eq 0 ]; then
    echo "pick_next_task: $PLAN has no '- [ ] task-N:' checkbox lines" >&2
    printf 'no_tasks_found'
    exit 0
  fi
  target=$(grep -m1 '^- \[ \] task-[0-9][0-9]*:' "$PLAN" | sed 's/^- \[ \] \(task-[0-9]*\):.*/\1/')
  if [ -z "$target" ]; then
    printf 'all_complete'
    exit 0
  fi
  printf '%s' "$target" > docs/plans/current_task_id.txt
  printf 'next_task-%s' "$target"
}

# mark_task_complete — flip the current task's checkbox to [x] and print
# `completed-task-N`. Fails loud (exit 1) when the id is malformed or has no
# `- [ ] task-N:` / `- [x] task-N:` line in plan.md — a silent no-op would
# let PickNextTask re-pick the same task forever.
mark_task_complete() {
  target=$(cat docs/plans/current_task_id.txt)
  case "$target" in
    task-[0-9]*) ;;
    *) echo "mark_task_complete: current_task_id.txt holds '$target', not a task-N id" >&2; exit 1 ;;
  esac
  if ! grep -q "^- \[.\] ${target}:" "$PLAN"; then
    echo "mark_task_complete: no '- [ ] ${target}:' line in $PLAN — cannot mark it complete" >&2
    exit 1
  fi
  sed "s/^- \[ \] ${target}:/- [x] ${target}:/" "$PLAN" > "$PLAN.tmp"
  mv "$PLAN.tmp" "$PLAN"
  printf 'completed-%s' "$target"
}
