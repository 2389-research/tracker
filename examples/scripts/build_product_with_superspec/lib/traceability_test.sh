#!/usr/bin/env bash
# ABOUTME: Fixture tests for lib/traceability.sh (#646 5b/5f) — the flat
# ABOUTME: one-line-per-requirement contract (trace_lint), exact counts (no
# ABOUTME: "0\n0"), overlay merge (overlay line wins, unknown ID appended with a
# ABOUTME: stdout WARNING, a same-phase clash FAILS), overlays removed + committed,
# ABOUTME: and the test_ref waiver lookup.
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
fail=0
check() { # name expected actual
  if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — want '$2' got '$3'"; fail=1; fi
}
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
SH=${TEST_SH:-sh}
# run_lib CODE — run CODE under $SH in $WORK with the library sourced.
run_lib() { OUT_STDOUT="$( (cd "$WORK" && $SH -c ". '$DIR/traceability.sh'; $1") 2>"$WORK/.stderr")"; RC=$?; OUT="$OUT_STDOUT$(cat "$WORK/.stderr")"; }
G() { git -C "$WORK" -c user.name=t -c user.email=t@t "$@"; }
mkdir -p "$WORK/docs"
M="$WORK/docs/traceability.yaml"

# 1. trace_lint: flat format accepted (comments/blanks ok); nested rejected;
#    a prose line rejected; missing file rejected.
printf '# matrix\nFR-1: {status: pending, impl_ref: null, test_ref: null, note: null}\n\nQG-2: {status: pending, impl_ref: null, test_ref: null, note: null}\n' > "$M"
run_lib 'trace_lint docs/traceability.yaml'
check "lint flat: rc 0"                 "0" "$RC"
printf 'requirements:\n  FR-1:\n    status: pending\n' > "$WORK/docs/nested.yaml"
run_lib 'trace_lint docs/nested.yaml'
check "lint nested: rc 1"               "1" "$RC"
check "lint nested: message"            "yes" "$(printf '%s' "$OUT" | grep -q 'no requirement lines' && echo yes || echo no)"
printf 'FR-1: {status: pending}\nsome prose here\n' > "$WORK/docs/prose.yaml"
run_lib 'trace_lint docs/prose.yaml'
check "lint prose: rc 1"                "1" "$RC"
check "lint prose: offending line"      "yes" "$(printf '%s' "$OUT" | grep -q '  some prose here' && echo yes || echo no)"
run_lib 'trace_lint docs/missing.yaml'
check "lint missing: rc 1"              "1" "$RC"

# 2. Counts are exact single numbers, including zero (the old
#    `grep -c … || echo 0` printed "0\n0").
run_lib 'trace_count "status: pending"'
check "count 2"                         "2" "$OUT"
run_lib 'trace_count "impl_ref: \"x\""'
check "count 0 is one line"             "0" "$OUT"
run_lib 'trace_count "" docs/missing.yaml'
check "count missing file → 0"          "0" "$OUT"
run_lib 'trace_ids_matching "status: pending" | paste -sd, -'
check "ids matching"                    "FR-1,QG-2" "$OUT"

# 3. Overlay merge: the overlay line replaces the master line by ID; other
#    lines, comments and order are untouched; an unknown ID is appended with
#    a WARNING.
printf 'FR-1: {status: done, impl_ref: "a.go:F", test_ref: "a_test.go:TestF", note: "ok"}\nNFR-9: {status: done, impl_ref: "n.go", test_ref: "n_test.go", note: null}\n' > "$WORK/docs/traceability.stream-a.yaml"
run_lib 'trace_merge_overlay docs/traceability.yaml docs/traceability.stream-a.yaml'
check "merge: rc 0"                     "0" "$RC"
check "merge: FR-1 replaced"            'FR-1: {status: done, impl_ref: "a.go:F", test_ref: "a_test.go:TestF", note: "ok"}' "$(sed -n 2p "$M")"
check "merge: comment kept first"       "# matrix" "$(sed -n 1p "$M")"
check "merge: QG-2 untouched"           "QG-2: {status: pending, impl_ref: null, test_ref: null, note: null}" "$(sed -n 4p "$M")"
check "merge: NFR-9 appended"           "NFR-9: {status: done, impl_ref: \"n.go\", test_ref: \"n_test.go\", note: null}" "$(tail -1 "$M")"
check "merge: WARNING on stdout"        "yes" "$(printf '%s' "$OUT_STDOUT" | grep -q 'WARNING: overlay entry NFR-9 is not in the master' && echo yes || echo no)"
check "merge: line count 5"             "5" "$(grep -c '' "$M")"

# 4. merge_traceability_overlays in a repo: a same-phase CLASH (two overlays
#    set one ID) is a FAILURE with the master untouched and the overlays kept
#    (a silent "later wins" would drop a stream's refs); after the human
#    removes the duplicate, the fold merges both, git-rm's the overlays and
#    commits with the phase message; nothing to merge → no commit; a
#    malformed overlay → rc 1 and the master untouched.
rm -f "$WORK/docs/traceability.stream-a.yaml"
printf 'FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}\nFR-2: {status: pending, impl_ref: null, test_ref: null, note: null}\n' > "$M"
G init -q; G add -A; G commit -q -m scaffold
printf 'FR-1: {status: done, impl_ref: "a.go", test_ref: "a_test.go", note: "from a"}\n' > "$WORK/docs/traceability.stream-a.yaml"
printf 'FR-1: {status: done, impl_ref: "b.go", test_ref: "b_test.go", note: "from b"}\nFR-2: {status: done, impl_ref: "b2.go", test_ref: "b2_test.go", note: null}\n' > "$WORK/docs/traceability.stream-b.yaml"
G add -A; G commit -q -m overlays
run_lib 'merge_traceability_overlays 1'
check "clash: rc 1"                     "1" "$RC"
check "clash: ERROR names the ID"       "yes" "$(printf '%s' "$OUT" | grep -q 'ERROR: FR-1 is set by more than one overlay this phase (docs/traceability.stream-b.yaml' && echo yes || echo no)"
check "clash: master untouched"         'FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}' "$(sed -n 1p "$M")"
check "clash: overlays kept"            "2" "$(ls "$WORK"/docs/traceability.*.yaml | wc -l | tr -d ' ')"
check "clash: no commit"                "overlays" "$(G log -1 --format=%s)"
check "clash: tree clean"               "" "$(G status --porcelain)"
printf 'FR-2: {status: done, impl_ref: "b2.go", test_ref: "b2_test.go", note: null}\n' > "$WORK/docs/traceability.stream-b.yaml"
G add -A; G commit -q -m "drop duplicate"
run_lib 'merge_traceability_overlays 1'
check "fold: rc 0"                      "0" "$RC"
check "fold: both overlays merged"      "2" "$(printf '%s\n' "$OUT" | grep -c 'merged overlay docs/traceability.stream-')"
check "fold: FR-1 from a"               'FR-1: {status: done, impl_ref: "a.go", test_ref: "a_test.go", note: "from a"}' "$(sed -n 1p "$M")"
check "fold: FR-2 from b"               "yes" "$(grep -q 'impl_ref: "b2.go"' "$M" && echo yes || echo no)"
check "fold: overlays removed"          "0" "$(ls "$WORK"/docs/traceability.*.yaml 2>/dev/null | wc -l | tr -d ' ')"
check "fold: overlays gone from index"  "" "$(G ls-files docs/ | grep 'traceability\.stream' || true)"
check "fold: committed"                 "chore(traceability): merge phase 1 stream overlays" "$(G log -1 --format=%s)"
check "fold: tree clean"                "" "$(G status --porcelain)"
run_lib 'merge_traceability_overlays 2'
check "fold none: rc 0"                 "0" "$RC"
check "fold none: message"              "yes" "$(printf '%s' "$OUT" | grep -q 'no traceability overlays to merge' && echo yes || echo no)"
check "fold none: no new commit"        "chore(traceability): merge phase 1 stream overlays" "$(G log -1 --format=%s)"
printf 'not a requirement line\n' > "$WORK/docs/traceability.stream-c.yaml"; G add -A; G commit -q -m bad
run_lib 'merge_traceability_overlays 2'
check "fold malformed: rc 1"            "1" "$RC"
check "fold malformed: message"         "yes" "$(printf '%s' "$OUT" | grep -q 'overlay docs/traceability.stream-c.yaml is malformed' && echo yes || echo no)"
check "fold malformed: master untouched" 'FR-1: {status: done, impl_ref: "a.go", test_ref: "a_test.go", note: "from a"}' "$(sed -n 1p "$M")"
rm -f "$WORK/docs/traceability.stream-c.yaml"

# 5. Waivers: `<ID>  <reason>` lines; prefix matches (FR-1 vs FR-10) don't.
printf '# waivers\nFR-1  verified by the runbook\nNFR-3 no unit test possible\n' > "$WORK/docs/traceability-waivers.txt"
run_lib 'trace_waived FR-1'
check "waived FR-1"                     "0" "$RC"
run_lib 'trace_waived FR-10'
check "FR-10 not waived by FR-1"        "1" "$RC"
run_lib 'trace_waived NFR-3'
check "waived NFR-3"                    "0" "$RC"
rm -f "$WORK/docs/traceability-waivers.txt"
run_lib 'trace_waived FR-1'
check "no waiver file → not waived"     "1" "$RC"

[ "$fail" = 0 ] && echo "ALL PASS" || { echo "SOME FAILED"; exit 1; }
