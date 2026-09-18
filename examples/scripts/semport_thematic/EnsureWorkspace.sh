set -eu
# $target_name / $source_ref / $target_module are operator-supplied environment
# variables (see the semport_thematic.dip header). Fail with a clear message
# rather than the bare `set -u` abort (#646 item 11).
for v in target_name source_ref target_module; do
  eval "val=\${$v:-}"
  [ -n "$val" ] || { echo "ERROR: $v is not set — export target_name, source_ref and target_module before running semport_thematic" >&2; exit 1; }
done
mkdir -p .ai/semport/"$target_name"
if [ ! -f .ai/semport/"$target_name"/thematic-spec.md ]; then
  cat > .ai/semport/"$target_name"/thematic-spec.md <<'SPECEOF'
# $target_name Thematic Semport Spec

## Scope
- Semantic port of $source_ref into $target_module.

## Checklist
<!-- planner maintains checklist here -->

## Validation Feedback
<!-- validator/appraiser writes concise latest loop feedback here -->
SPECEOF
fi
if [ ! -f .ai/semport/"$target_name"/validation-feedback.md ]; then
  cat > .ai/semport/"$target_name"/validation-feedback.md <<'FBEOF'
# Validation Feedback
- none yet
FBEOF
fi
printf 'workspace ready'