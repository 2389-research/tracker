set -eu
# $target_name is an operator-supplied environment variable (see the
# semport_thematic.dip header). Fail with a clear message rather than the
# bare `set -u` abort (#646 item 11).
[ -n "${target_name:-}" ] || { echo "ERROR: target_name is not set — export target_name=<SwiftTargetName> before running semport_thematic" >&2; exit 1; }
SPEC=".ai/semport/$target_name/thematic-spec.md"
# grep, not rg (#646 item 7): an absent ripgrep made `if rg …` false and
# reported COMPLETE on an unchecked (or missing) checklist.
if [ ! -f "$SPEC" ]; then
  echo "CheckCompletion: $SPEC is missing — nothing has been planned yet" >&2
  printf 'INCOMPLETE'
  exit 1
fi
if grep -q '^- \[ \]' "$SPEC"; then
  printf 'INCOMPLETE'
  exit 1
fi
printf 'COMPLETE'
