# ABOUTME: Shared on-disk attempt counter for the dotpowers-family budget gates.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/dotpowers/lib.

# bump_counter FILE — read the integer in FILE (0 if absent), reset a
# corrupted/non-numeric value to 0 so the arithmetic can't abort under
# `set -e` (dash: `Illegal number` rc 2 — #646 item 9), increment, write
# back, and leave the new value in COUNT. Callers create FILE's directory
# first. Shared by CheckImplementBudget (impl_count_<task>), CheckReworkBudget
# (rework_count) and ValidatePlanFormat (plan_validate_count).
#
# A FAILED WRITE (FILE is a directory, or its parent is unwritable) exits 1
# loudly naming the path — never a silent `set -e` abort with no diagnostic.
bump_counter() {
  COUNT=0
  if [ -f "$1" ]; then
    COUNT=$(cat "$1" 2>/dev/null || echo 0)
  fi
  case "$COUNT" in
    ''|*[!0-9]*) COUNT=0 ;;
  esac
  COUNT=$((COUNT + 1))
  if ! printf '%s\n' "$COUNT" > "$1" 2>/dev/null; then
    echo "ERROR: cannot write attempt counter $1 (a directory or an unwritable path is in the way) — remove it and re-run" >&2
    exit 1
  fi
}
