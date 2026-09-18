#!/usr/bin/env bash
# ABOUTME: Fixture tests for EnsureEnv.sh (tracker-runner #846) — runs the first
# ABOUTME: found seed bootstrap hook, logs it to .tracker/env-bootstrap.log,
# ABOUTME: records .tracker/env-bootstrap.status, and prints ONLY env-ready /
# ABOUTME: env-failed on stdout (the marker_grep channel); hook output on stderr.
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
SCRIPT="$(stage_script "$DIR/EnsureEnv.sh")"
# TEST_SH=dash runs the node script under dash (the .dip runs it via `sh -c`).
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$SCRIPT") 2>"$STATE/stderr")"; RC=$?; }
ehas() { grep -qF -- "$1" "$STATE/stderr" && echo yes || echo no; }
status() { cat "$WORK/.tracker/env-bootstrap.status" 2>/dev/null | paste -sd';' -; }

# 1. No hook anywhere: env-ready is the ENTIRE stdout, status records no
#    hook, an empty log is created, exit 0.
run
check "no hook: exit 0"                 "0" "$RC"
check "no hook: stdout is the marker"   "env-ready" "$OUT"
check "no hook: status"                 "status=ok;hook=;exit=0" "$(status)"
check "no hook: log exists (empty)"     "yes" "$([ -f "$WORK/.tracker/env-bootstrap.log" ] && [ ! -s "$WORK/.tracker/env-bootstrap.log" ] && echo yes || echo no)"
check "no hook: nothing on stderr"      "" "$(cat "$STATE/stderr")"

# 2. Hook succeeds: its stdout AND stderr go to the log and are replayed on
#    THIS node's stderr — never on stdout, which stays exactly the marker.
printf 'echo "installing deps"\necho "warn: slow mirror" >&2\ntouch .venv-made\nexit 0\n' > "$WORK/build-setup.sh"
run
check "ok hook: exit 0"                 "0" "$RC"
check "ok hook: stdout is the marker"   "env-ready" "$OUT"
check "ok hook: status"                 "status=ok;hook=build-setup.sh;exit=0" "$(status)"
check "ok hook: ran in workdir"         "yes" "$([ -f "$WORK/.venv-made" ] && echo yes || echo no)"
check "ok hook: stdout captured in log" "yes" "$(grep -qF 'installing deps' "$WORK/.tracker/env-bootstrap.log" && echo yes || echo no)"
check "ok hook: stderr captured in log" "yes" "$(grep -qF 'warn: slow mirror' "$WORK/.tracker/env-bootstrap.log" && echo yes || echo no)"
check "ok hook: log replayed on stderr" "yes" "$(ehas 'installing deps')"
check "ok hook: running line on stderr" "yes" "$(ehas '[EnsureEnv] running seed bootstrap hook: build-setup.sh')"

# 3. Hook fails: env-failed is the entire stdout, exit 0 (routing is the
#    marker's job, not the exit code), status records the hook exit, the
#    log holds the failure output, the FAILED line names the hook and exit.
printf 'echo "pip: no matching distribution"\nexit 7\n' > "$WORK/build-setup.sh"
run
check "failed hook: exit 0"             "0" "$RC"
check "failed hook: stdout is the marker" "env-failed" "$OUT"
check "failed hook: status"             "status=failed;hook=build-setup.sh;exit=7" "$(status)"
check "failed hook: log captured"       "yes" "$(grep -qF 'pip: no matching distribution' "$WORK/.tracker/env-bootstrap.log" && echo yes || echo no)"
check "failed hook: FAILED line"        "yes" "$(ehas "seed bootstrap hook 'build-setup.sh' FAILED (exit 7)")"

# 4. A hook that prints to stdout without a trailing newline, then fails —
#    nothing of it may reach the marker stream (marker_grep is ^…$ anchored).
printf 'printf "partial line no newline"\nexit 1\n' > "$WORK/build-setup.sh"
run
check "chatty hook: marker only"        "env-failed" "$OUT"
check "chatty hook: text in log"        "yes" "$(grep -qF 'partial line no newline' "$WORK/.tracker/env-bootstrap.log" && echo yes || echo no)"

# 5. Lookup order: build-setup.sh beats .build/setup.sh beats
#    scripts/build-setup.sh; a hook that is not executable still runs (via sh).
rm -f "$WORK/build-setup.sh"
mkdir -p "$WORK/.build" "$WORK/scripts"
printf 'echo second\n' > "$WORK/.build/setup.sh"
printf 'echo third\nexit 3\n' > "$WORK/scripts/build-setup.sh"
chmod -x "$WORK/.build/setup.sh"
run
check "order: .build/setup.sh chosen"   "status=ok;hook=.build/setup.sh;exit=0" "$(status)"
check "order: third not run"            "no" "$(grep -qF third "$WORK/.tracker/env-bootstrap.log" && echo yes || echo no)"
rm -f "$WORK/.build/setup.sh"
run
check "order: scripts/build-setup.sh last" "status=failed;hook=scripts/build-setup.sh;exit=3" "$(status)"
check "order: marker env-failed"        "env-failed" "$OUT"

# 6. A previous run's log is truncated on every run (no stale lines).
rm -f "$WORK/scripts/build-setup.sh"
run
check "stale log cleared"               "yes" "$([ ! -s "$WORK/.tracker/env-bootstrap.log" ] && echo yes || echo no)"
check "stale status replaced"           "status=ok;hook=;exit=0" "$(status)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
