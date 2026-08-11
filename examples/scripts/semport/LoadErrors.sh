set -eu
total=$(grep -c 'error:' /tmp/semport-build.log || echo 0)
printf '=== Total errors: %s ===\n\n' "$total"
printf '=== Error count by file ===\n'
grep 'error:' /tmp/semport-build.log | grep -o 'OmniAgentsSDK/[^:]*' | sort | uniq -c | sort -rn
printf '\n=== Top errors (first 80) ===\n'
grep 'error:' /tmp/semport-build.log | head -80
printf '\n=== Source of top 5 files with most errors ===\n'
for f in $(grep 'error:' /tmp/semport-build.log | grep -o 'Sources/OmniAgentsSDK/[^:]*' | sort | uniq -c | sort -rn | head -5 | awk '{print $2}'); do
  printf '\n--- %s (%s lines) ---\n' "$f" "$(wc -l < "$f" 2>/dev/null || echo '?')"
  cat "$f" 2>/dev/null
done