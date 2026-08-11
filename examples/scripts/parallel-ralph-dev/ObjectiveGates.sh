#!/bin/sh
gate_results=""
all_pass=true

# Gate 1: Build
if go build ./... 2>/dev/null; then
  gate_results="$gate_results\n[PASS] go build"
else
  gate_results="$gate_results\n[FAIL] go build"
  all_pass=false
fi

# Gate 2: Tests
if go test ./... 2>/dev/null; then
  gate_results="$gate_results\n[PASS] go test"
else
  gate_results="$gate_results\n[FAIL] go test"
  all_pass=false
fi

# Gate 3: Vet
if go vet ./... 2>/dev/null; then
  gate_results="$gate_results\n[PASS] go vet"
else
  gate_results="$gate_results\n[FAIL] go vet"
  all_pass=false
fi

# Gate 4: No TODOs in new code
todo_count=$(git diff main --unified=0 2>/dev/null | grep -c '^\+.*TODO' || true)
if [ "$todo_count" -eq 0 ]; then
  gate_results="$gate_results\n[PASS] no TODOs in new code"
else
  gate_results="$gate_results\n[WARN] $todo_count TODOs in new code"
fi

printf '%b' "$gate_results" > .ai/gate-results.txt

if [ "$all_pass" = true ]; then
  printf 'gates_pass'
else
  printf 'gates_fail'
fi