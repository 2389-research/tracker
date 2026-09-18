# Diagnostics for a failed EnsureEnv bootstrap hook (tracker-runner #846).
# A REPORTER, not the failure itself: it replays the status + hook log so
# they land in this node's stdout (the activity log / `tracker diagnose`
# tail), then exits 0 and the single unconditional edge routes to AbortRun —
# the workflow's fail-closed terminal — so the run ends `fail` uniformly
# with every other mechanical abort (#640 A2), never `success` at Done.
set -u
echo "ENVIRONMENT BOOTSTRAP FAILED — the seed's build-setup hook did not complete."
echo
echo "Status (.tracker/env-bootstrap.status):"
if [ -f .tracker/env-bootstrap.status ]; then cat .tracker/env-bootstrap.status; else echo "  (none written)"; fi
echo
echo "Hook output (.tracker/env-bootstrap.log):"
if [ -f .tracker/env-bootstrap.log ]; then cat .tracker/env-bootstrap.log; else echo "  (no log recorded)"; fi
echo
echo "A pinned toolchain/dependency setup failed, so the milestones would"
echo "build against a partial environment. Fix the build-setup hook and"
echo "re-run. Never let an agent improvise its own deps over a failed pin."
exit 0
