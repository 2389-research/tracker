# Seed-driven environment bootstrap, fail-loud (tracker-runner #846). Runs
# the FIRST found of build-setup.sh / .build/setup.sh / scripts/build-setup.sh
# (a hook the seed repo or operator ships to pin toolchains and deps — a
# .venv, `npm ci`, `go mod download`, ...) via `sh`, BEFORE any agent runs.
# A failed pin must stop the run: letting the milestones build against a
# partial environment — or letting an agent improvise its own deps over a
# failed pin — wastes the whole run. No hook → env-ready, nothing to do.
#
# Output contract: stdout carries ONLY the routing marker (env-ready /
# env-failed; the .dip declares `marker_grep: '^(env-ready|env-failed)$'`,
# so a run that prints neither — a timeout, a crash before the printf —
# fails LOUD via tool_marker_missing instead of routing on a stale value).
# The hook's own stdout+stderr go to .tracker/env-bootstrap.log and are
# replayed on THIS node's stderr, so a chatty hook can never leak a line
# onto the marker stream. .tracker/env-bootstrap.status records
# status/hook/exit for EnvBootstrapFailed and `tracker diagnose`.
# .tracker/ is the engine's run-metadata dir (git-excluded by Setup, ignored
# by the dirty-tree preflight); what the hook itself writes to the tree is
# the seed's responsibility.
set -u
mkdir -p .tracker
status_file=.tracker/env-bootstrap.status
log_file=.tracker/env-bootstrap.log
: > "$log_file"
found=''
for hook in build-setup.sh .build/setup.sh scripts/build-setup.sh; do
  if [ -f "$hook" ]; then found="$hook"; break; fi
done
if [ -z "$found" ]; then
  printf 'status=ok\nhook=\nexit=0\n' > "$status_file"
  printf 'env-ready'
  exit 0
fi
echo "[EnsureEnv] running seed bootstrap hook: $found" >&2
sh "$found" > "$log_file" 2>&1 </dev/null
rc=$?
cat "$log_file" >&2
if [ "$rc" -eq 0 ]; then
  printf 'status=ok\nhook=%s\nexit=0\n' "$found" > "$status_file"
  printf 'env-ready'
else
  printf 'status=failed\nhook=%s\nexit=%s\n' "$found" "$rc" > "$status_file"
  echo "[EnsureEnv] seed bootstrap hook '$found' FAILED (exit $rc) — aborting; a partial/failed env would waste the whole run (#846)" >&2
  printf 'env-failed'
fi
exit 0
