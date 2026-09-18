set -eu
# Build log is per-workdir (.ai/semport/build.log — shared with TryBuild and
# LoadErrors), not a fixed /tmp path that concurrent runs clobbered (#646
# item 11). The error count is computed OUTSIDE the `|| { }` so a red build
# with zero `error:` lines (grep -c exits 1) still prints STILL_FAILING
# instead of aborting under set -e before the marker.
mkdir -p .ai/semport
LOG=.ai/semport/build.log
if swift build --target OmniAgentsSDK >"$LOG" 2>&1; then
  printf 'BUILD_CLEAN'
  exit 0
fi
errs=$(grep -c 'error:' "$LOG" || true)
printf 'STILL_FAILING (%s errors)' "$errs"
exit 1
