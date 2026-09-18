#!/bin/sh
# Objective gates before review. stdout is ONLY the routing marker
# (the .dip matches `ctx.tool_stdout = gates_pass|gates_fail`); every
# gate's full output is captured to .ai/gates/<gate>.log and the tail of a
# red gate is echoed to stderr, so a failure is diagnosable from the
# activity log / `tracker diagnose` instead of being discarded with
# `2>/dev/null` (#646 item 11).
mkdir -p .ai/gates
gate_results=""
all_pass=true

# run_gate NAME LABEL CMD... — capture, record, and surface a red tail.
run_gate() {
  name=$1; label=$2; shift 2
  if "$@" > ".ai/gates/$name.log" 2>&1; then
    gate_results="$gate_results\n[PASS] $label"
  else
    gate_results="$gate_results\n[FAIL] $label (see .ai/gates/$name.log)"
    all_pass=false
    { echo "--- [FAIL] $label — tail of .ai/gates/$name.log ---"; tail -n 40 ".ai/gates/$name.log"; } >&2
  fi
}

run_gate build "go build" go build ./...
run_gate test  "go test"  go test ./...
run_gate vet   "go vet"   go vet ./...

# Gate 4: No TODOs in new code — diff against the base CreateBranches
# recorded (.ai/base-ref.txt), else the remote default branch, else `main`;
# with none of those the gate is a WARN, never a silent empty diff.
base=""
for cand in "$(cat .ai/base-ref.txt 2>/dev/null || true)" "$(git rev-parse --abbrev-ref origin/HEAD 2>/dev/null || true)" main; do
  [ -n "$cand" ] || continue
  if git rev-parse --verify --quiet "$cand^{commit}" >/dev/null 2>&1; then base=$cand; break; fi
done
if [ -z "$base" ]; then
  gate_results="$gate_results\n[WARN] no base ref for the TODO gate (.ai/base-ref.txt, origin/HEAD, main all absent) — skipped"
else
  todo_count=$(git diff "$base" --unified=0 2>/dev/null | grep -c '^\+.*TODO' || true)
  case "$todo_count" in ''|*[!0-9]*) todo_count=0 ;; esac
  if [ "$todo_count" -eq 0 ]; then
    gate_results="$gate_results\n[PASS] no TODOs in new code"
  else
    gate_results="$gate_results\n[WARN] $todo_count TODOs in new code"
  fi
fi

printf '%b\n' "$gate_results" > .ai/gate-results.txt

if [ "$all_pass" = true ]; then
  printf 'gates_pass'
else
  printf 'gates_fail'
fi
