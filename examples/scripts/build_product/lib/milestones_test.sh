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

## Milestone 8: none underscored
**Contract tests**: _none_
## Milestone 9: none bold
**Contract tests**: **none**
## Milestone 10: none comma reason
**Contract tests**: none, scaffold only
## Milestone 11: none paren backtick
**Contract tests**: none (this milestone only adds `go.mod`)
## Milestone 12: no tests dash
**Contract tests**: no tests — scaffold
## Milestone 13: none yet
**Contract tests**: none yet
## Milestone 14: prose and
**Contract tests**: TestA and TestB
## Milestone 15: prose command
**Contract tests**: go test ./... passes
## Milestone 16: none as first bullet
**Contract tests**:
- none — docs only
## Milestone 17: N/A trailing dot
**Contract tests**: N/A.
## Milestone 18: mixed prose and identifiers
**Contract tests**: TestA, the smoke test, TestB
## Milestone 19: no test
**Contract tests**: No test: README only
PLAN

check "none with reason -> empty"        ""  "$(ct 1)"
check "bulleted backticked names"        "inspector::test_inspect_contract|inspector::test_inspect_rejects_empty" "$(ct 2)"
check "inline comma list, Go subtest, pytest nodeid" "TestParse|TestParse/empty_input|tests/test_parse.py::test_roundtrip" "$(ct 3)"
check "bold-inside-colon, capitalized, plain + backticked JS title (backticked first)" "renders the help banner|TestMain" "$(ct 4)"
check "N/A -> empty"                     ""  "$(ct 5)"
check "numbered items, trailers dropped" "TestOne|TestTwo" "$(ct 6)"
check "absent field -> empty"            ""  "$(ct 7)"
check "unknown milestone -> empty"       ""  "$(ct 42)"
# "none" in every LLM phrasing declares NOTHING (a bogus name would be an
# unfixable CONTRACT-TEST-MISSING); un-backticked prose is never a name.
check "_none_ -> empty"                  ""  "$(ct 8)"
check "**none** -> empty"                ""  "$(ct 9)"
check "none, reason -> empty"            ""  "$(ct 10)"
check "none (backticked reason) -> empty" "" "$(ct 11)"
check "no tests — reason -> empty"       ""  "$(ct 12)"
check "none yet -> empty"                ""  "$(ct 13)"
check "TestA and TestB -> prose dropped" ""  "$(ct 14)"
check "go test ./... passes -> dropped"  ""  "$(ct 15)"
check "none as first bullet -> empty"    ""  "$(ct 16)"
check "N/A. -> empty"                    ""  "$(ct 17)"
check "prose piece dropped, ids kept"    "TestA|TestB" "$(ct 18)"
check "No test: reason -> empty"         ""  "$(ct 19)"
# The block ends at the next bold field: Done-when text never becomes a test.
check "m2 done-when not a test"          "no" "$(ct 2 | grep -q 'inspect()' && echo yes || echo no)"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
