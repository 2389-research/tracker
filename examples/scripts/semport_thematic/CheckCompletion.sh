set -eu
if rg -n '^- \[ \]' .ai/semport/$target_name/thematic-spec.md >/dev/null 2>&1; then
  printf 'INCOMPLETE'
  exit 1
fi
printf 'COMPLETE'