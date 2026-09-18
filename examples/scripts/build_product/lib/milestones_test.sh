#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/milestones.sh's `**Contract tests**` parser
# ABOUTME: (tracker-runner #901): milestone_contract_tests N PLAN yields the exact
# ABOUTME: test names a milestone declares — tolerant of the LLM-written forms
# ABOUTME: milestone_files accepts — and "none" (with its reason) yields nothing.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
PLAN="$WORK/milestones.md"
# TEST_SH=dash runs the parser under dash (the node scripts run via `sh -c`).
ct() { "${TEST_SH:-sh}" -c ". '$DIR/milestones.sh'; milestone_contract_tests '$1' '$PLAN'" 2>&1 | paste -sd'|' -; }

cat > "$PLAN" <<'PLAN'
## Plan summary
- one line per milestone

## Milestone 1: Scaffold
**Depends on**: none
**Files**:
- `go.mod` (new)
**Contract tests**: none — scaffold only, `go build ./...` is the done-when
**Done when**: `go build ./...` passes

## Milestone 2: Inspector
**Depends on**: 1
**Files**:
- `src/inspect.rs` (new)
**Contract tests**:
- `inspector::test_inspect_contract`
- `inspector::test_inspect_rejects_empty` (new)
**Done when**: inspect() returns the parsed header

## Milestone 3: Parser
**Files**: `pkg/parse/parse.go`
**Contract tests**: `TestParse`, `TestParse/empty_input`, `tests/test_parse.py::test_roundtrip`
**Done when**: TestParse passes

## Milestone 4: CLI
- **Files:** `cmd/app/main.go`
- **Contract Tests:** TestMain (Go), `renders the help banner` (jest)
- **Done when**: the binary prints help

## Milestone 5: Docs
**Files**: `README.md`
**Contract tests**: N/A (docs only)
**Done when**: README documents the flags

## Milestone 6: Numbered
**Files**: `a.go`
**Contract tests**:
1. TestOne — proves the parse
2) TestTwo: proves the render
**Verify command**: go test ./...

## Milestone 7: Missing field
**Files**: `b.go`
**Done when**: TestB passes
PLAN

check "none with reason -> empty"        ""  "$(ct 1)"
check "bulleted backticked names"        "inspector::test_inspect_contract|inspector::test_inspect_rejects_empty" "$(ct 2)"
check "inline comma list, Go subtest, pytest nodeid" "TestParse|TestParse/empty_input|tests/test_parse.py::test_roundtrip" "$(ct 3)"
check "bold-inside-colon, capitalized, plain + backticked JS title (backticked first)" "renders the help banner|TestMain" "$(ct 4)"
check "N/A -> empty"                     ""  "$(ct 5)"
check "numbered items, trailers dropped" "TestOne|TestTwo" "$(ct 6)"
check "absent field -> empty"            ""  "$(ct 7)"
check "unknown milestone -> empty"       ""  "$(ct 42)"
# The block ends at the next bold field: Done-when text never becomes a test.
check "m2 done-when not a test"          "no" "$(ct 2 | grep -q 'inspect()' && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
