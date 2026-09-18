#!/usr/bin/env bash
# ABOUTME: Fixture tests for megaplan's DetermineSprintId.sh and SyncLedger.sh
# ABOUTME: (#646 item 6) — the next sprint id after 001/007/008/009/099 is
# ABOUTME: computed portably (dash has no `10#`, and 008/009 are invalid octal
# ABOUTME: on every shell), and a non-numeric ledger id fails loud.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
STATE="$(mktemp -d)"
trap 'rm -rf "$WORK" "$STATE"' EXIT
run() { OUT="$( (cd "$WORK" && ${TEST_SH:-sh} "$DIR/$1") 2>"$STATE/stderr")"; RC=$?; }
LEDGER="$WORK/.ai/ledger.tsv"
seed() { # ids...
  mkdir -p "$WORK/.ai"; rm -f "$WORK/.ai/current_sprint_id.txt"
  printf 'sprint_id\ttitle\tstatus\tcreated_at\tupdated_at\n' > "$LEDGER"
  for id in "$@"; do printf '%s\tSprint %s\tdone\tt0\tt0\n' "$id" "$id" >> "$LEDGER"; done
}

# 1. No ledger -> 001.
run DetermineSprintId.sh
check "no ledger exit 0"           "0" "$RC"
check "no ledger -> sprint-001"    "sprint-001" "$OUT"
check "writes current id"          "001" "$(cat "$WORK/.ai/current_sprint_id.txt")"

# 2. After 001 -> 002 (dash: `arithmetic expression: expecting EOF` on 10#).
seed 001; run DetermineSprintId.sh
check "after 001 exit 0"           "0" "$RC"
check "after 001 -> 002"           "sprint-002" "$OUT"

# 3. 007 -> 008 and 008 -> 009 and 009 -> 010 (invalid-octal territory).
seed 001 007; run DetermineSprintId.sh
check "after 007 -> 008"           "sprint-008" "$OUT"
seed 008; run DetermineSprintId.sh
check "after 008 -> 009"           "sprint-009" "$OUT"
seed 009; run DetermineSprintId.sh
check "after 009 -> 010"           "sprint-010" "$OUT"

# 4. Highest wins numerically, not lexically (009 vs 010 vs 099).
seed 010 099 009; run DetermineSprintId.sh
check "after 099 -> 100"           "sprint-100" "$OUT"

# 4b. A trailing blank line (or blank rows) in the ledger is ignored — it
#     used to read as sprint_id '' and fail as "not numeric".
seed 001; printf '\n' >> "$LEDGER"; run DetermineSprintId.sh
check "trailing blank exit 0"      "0" "$RC"
check "trailing blank -> 002"      "sprint-002" "$OUT"
seed 003; printf '\n\n' >> "$LEDGER"; run SyncLedger.sh
check "sync trailing blank"        "synced-004" "$OUT"

# 5. A non-numeric sprint id in the ledger fails loud (single clean message,
#    no garbled END output).
seed 001 abc; run DetermineSprintId.sh
check "garbage id exit 1"          "1" "$RC"
check "garbage id message"         "ERROR: .ai/ledger.tsv sprint_id 'abc' is not numeric — fix the ledger" "$(cat "$STATE/stderr")"

# 6. SyncLedger with no current id derives the same next id and appends it.
seed 008; run SyncLedger.sh
check "sync exit 0"                "0" "$RC"
check "sync marker"                "synced-009" "$OUT"
check "sync appended row"          "1" "$(grep -c '^009	' "$LEDGER")"
# 7. SyncLedger with a current id updates the existing row in place.
printf '008' > "$WORK/.ai/current_sprint_id.txt"; run SyncLedger.sh
check "sync existing marker"       "synced-008" "$OUT"
check "sync existing status"       "planned" "$(awk -F '\t' '$1=="008"{print $3}' "$LEDGER")"
check "sync existing no dup"       "1" "$(grep -c '^008	' "$LEDGER")"

if [ "$fail" = 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; exit 1; fi
