# ABOUTME: Shared on-disk attempt counter for build_product's budget gates.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.

# bump_counter FILE — read the integer in FILE (0 if absent), reset a
# corrupted/non-numeric value to 0 so the arithmetic can't error under
# `set -e` (and a fresh decision isn't denied its budget by stale junk),
# increment, write back, and leave the new value in ATTEMPTS. Callers create
# FILE's directory first. Shared by TestMilestone (fix_attempts),
# CheckSpecForgeBudget (spec_forge_attempts) and ContinueWithMoreTurns
# (continue_attempts).
bump_counter() {
  ATTEMPTS=0
  if [ -f "$1" ]; then
    ATTEMPTS=$(cat "$1" 2>/dev/null || echo 0)
  fi
  case "$ATTEMPTS" in
    ''|*[!0-9]*) ATTEMPTS=0 ;;
  esac
  ATTEMPTS=$((ATTEMPTS + 1))
  echo "$ATTEMPTS" > "$1"
}
