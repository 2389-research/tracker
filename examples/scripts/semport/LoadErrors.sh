set -eu
LOG=.ai/semport/build.log   # written by TryBuild / VerifyBuild (#646 item 11)
[ -f "$LOG" ] || { echo "ERROR: $LOG is missing — TryBuild did not run" >&2; exit 1; }
# `grep -c` prints 0 AND exits 1 on no match, so `|| echo 0` printed "0\n0"
# (#646 item 12) — `|| true` keeps the single count.
total=$(grep -c 'error:' "$LOG" || true)
printf '=== Total errors: %s ===\n\n' "$total"
printf '=== Error count by file ===\n'
grep 'error:' "$LOG" | grep -o 'OmniAgentsSDK/[^:]*' | sort | uniq -c | sort -rn || true
printf '\n=== Top errors (first 80) ===\n'
grep 'error:' "$LOG" | head -80 || true
printf '\n=== Source of top 5 files with most errors ===\n'
for f in $(grep 'error:' "$LOG" | grep -o 'Sources/OmniAgentsSDK/[^:]*' | sort | uniq -c | sort -rn | head -5 | awk '{print $2}'); do
  printf '\n--- %s (%s lines) ---\n' "$f" "$(wc -l < "$f" 2>/dev/null || echo '?')"
  cat "$f" 2>/dev/null
done
