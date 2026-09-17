set -u
# Fail-closed terminal for the forge loop: budget exhaustion, a too-thin/
# unhardenable spec, or a fidelity violation. A TOOL that exits non-zero —
# NOT a human gate — so it fails closed in every mode (interactive,
# --auto-approve, --autopilot, --webhook); a gate's default would
# auto-advance headlessly and ship a broken spec. Strict-failure-edge
# rule: the single unconditional edge to Done means this fail HALTS the
# pipeline before Done runs.
echo "SPEC-FORGE FAILED — the spec could not be autonomously hardened to pass SpecLint."
echo
echo "Residual coherence findings (.ai/decisions/spec-quality.md):"
if [ -f .ai/decisions/spec-quality.md ]; then cat .ai/decisions/spec-quality.md; else echo "  (none written)"; fi
echo
echo "What the forge attempted (.ai/decisions/spec-forge-log.md):"
if [ -f .ai/decisions/spec-forge-log.md ]; then cat .ai/decisions/spec-forge-log.md; else echo "  (no forge edits recorded)"; fi
echo
echo "Original spec preserved at .ai/decisions/SPEC.original.md."
echo "Fix SPEC.md by hand and re-run; Setup resets the forge budget on a fresh run."
exit 1