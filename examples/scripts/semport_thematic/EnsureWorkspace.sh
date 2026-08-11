set -eu
mkdir -p .ai/semport/$target_name
if [ ! -f .ai/semport/$target_name/thematic-spec.md ]; then
  cat > .ai/semport/$target_name/thematic-spec.md <<'SPECEOF'
# $target_name Thematic Semport Spec

## Scope
- Semantic port of $source_ref into $target_module.

## Checklist
<!-- planner maintains checklist here -->

## Validation Feedback
<!-- validator/appraiser writes concise latest loop feedback here -->
SPECEOF
fi
if [ ! -f .ai/semport/$target_name/validation-feedback.md ]; then
  cat > .ai/semport/$target_name/validation-feedback.md <<'FBEOF'
# Validation Feedback
- none yet
FBEOF
fi
printf 'workspace ready'