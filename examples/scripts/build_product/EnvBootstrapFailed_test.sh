#!/usr/bin/env bash
# ABOUTME: Fixture tests for EnvBootstrapFailed.sh (tracker-runner #846) — the
# ABOUTME: reporter that replays .tracker/env-bootstrap.{status,log} on stdout
# ABOUTME: and exits 0 so its single edge routes to the AbortRun terminal.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
. "$DIR/test_helpers.sh"
SCRIPT="$(stage_script "$DIR/EnvBootstrapFailed.sh")"
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
has() { printf '%s' "$OUT" | grep -qF -- "$1" && echo yes || echo no; }

# 1. No status / log on disk (EnsureEnv crashed before writing): still
#    reports, with explicit placeholders, exit 0.
run
check "nothing on disk: exit 0"         "0" "$RC"
check "nothing on disk: headline"       "yes" "$(has 'ENVIRONMENT BOOTSTRAP FAILED')"
check "nothing on disk: status placeholder" "yes" "$(has '(none written)')"
check "nothing on disk: log placeholder" "yes" "$(has '(no log recorded)')"
check "nothing on disk: guidance"       "yes" "$(has 'Never let an agent improvise its own deps over a failed pin.')"

# 2. Status + log present: both replayed verbatim on stdout.
mkdir -p "$WORK/.tracker"
printf 'status=failed\nhook=build-setup.sh\nexit=7\n' > "$WORK/.tracker/env-bootstrap.status"
printf 'pip: no matching distribution for foo==9.9\n' > "$WORK/.tracker/env-bootstrap.log"
run
check "replay: exit 0"                  "0" "$RC"
check "replay: status lines"            "yes" "$(has 'hook=build-setup.sh')"
check "replay: exit line"               "yes" "$(has 'exit=7')"
check "replay: log line"                "yes" "$(has 'pip: no matching distribution for foo==9.9')"
check "replay: no placeholders"         "no"  "$(has '(none written)')"
check "replay: nothing on stderr"       "" "$(cat "$STATE/stderr")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
