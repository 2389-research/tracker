set -u
echo "# Milestone plan (.ai/decisions/milestones.md)"
echo
if [ -f .ai/decisions/milestones.md ]; then
  cat .ai/decisions/milestones.md
else
  echo "_(milestones.md not found — Decompose did not write a plan)_"
fi
echo
echo "# Requirement coverage (.ai/decisions/requirement-coverage.md)"
echo
if [ -f .ai/decisions/requirement-coverage.md ]; then
  cat .ai/decisions/requirement-coverage.md
else
  echo "_(requirement-coverage.md not found)_"
fi
echo
echo "# Spec ambiguity rulings (.ai/decisions/spec-ambiguities.md)"
echo
if [ -f .ai/decisions/spec-ambiguities.md ]; then
  cat .ai/decisions/spec-ambiguities.md
else
  echo "_(spec-ambiguities.md not found — ReadSpec did not write rulings)_"
fi
echo
echo "# Behavioral contracts (.ai/decisions/behavioral-contracts.md)"
echo
if [ -f .ai/decisions/behavioral-contracts.md ]; then
  cat .ai/decisions/behavioral-contracts.md
else
  echo "_(behavioral-contracts.md not found)_"
fi
echo
echo "# Spec coherence findings (.ai/decisions/spec-quality.md)"
echo
if [ -f .ai/decisions/spec-quality.md ]; then
  echo "_Warnings here (rules d/e/i) did not block the build — they are the spots where agents will guess. Enrich SPEC.md and choose \`adjust\` if a guess would be wrong._"
  echo
  cat .ai/decisions/spec-quality.md
else
  echo "_(spec-quality.md not found — SpecLint did not write findings)_"
fi
echo
echo "# Spec auto-hardening log (.ai/decisions/spec-forge-log.md)"
echo
if [ -f .ai/decisions/spec-forge-log.md ]; then
  echo "_NOTE: SPEC.md was auto-edited by the spec-forge loop. Each ruling below changed the source of truth — review before approving._"
  echo
  cat .ai/decisions/spec-forge-log.md
else
  echo "_(no auto-hardening — SPEC.md passed coherence as written)_"
fi