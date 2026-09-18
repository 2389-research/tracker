# ABOUTME: Shared milestone-plan parsing + per-plan state bookkeeping for build_product.
# ABOUTME: Sourced (never executed) via ${graph.workflow_dir}/scripts/build_product/lib.
#
# ONE parser for .ai/decisions/milestones.md (#640 E4/E5/E6). PickNextMilestone
# (count + extract), MarkMilestoneDone (number of the current section) and
# CheckMilestoneOutputs (declared **Files**) all go through these functions so
# the header regex and the Files-block grammar can never drift apart again
# (the pre-#640 count/extract regexes disagreed and mis-routed real plans).
#
# Portability: POSIX sh + POSIX awk (BSD awk, mawk, gawk). No `{n,m}`
# intervals, no `\.` inside `awk -v` strings, no `[[:class:]]` in dynamic
# regexes (a literal tab is threaded in instead).

# count_done_milestones DONE_DIR — number of completion markers
# PickNextMilestone/MarkMilestoneDone keep in DONE_DIR (one file per finished
# milestone). Prints the count; 0 when the dir is absent.
count_done_milestones() {
  ls "$1" 2>/dev/null | wc -l | tr -d ' '
}

_ms_tab=$(printf '\t')
_ms_ws="[ $_ms_tab]"

# milestone_header_re — the ONE anchored (case-insensitive) header regex, as
# an ERE usable by `grep -iE` and by awk against tolower($0). Accepts
# `##`..`####` (also a single `#`), optional tab/space, the word
# "milestone" in any case, an optional `#`, leading zeros, and then EITHER
# end of line (bare `## Milestone 1`) OR a non-digit/non-dot suffix
# (`: title`, ` — title`, `:`) OR a `.` followed by space/EOL (`1. Title`).
# `## Milestone 1.1` is therefore NOT a header (a sub-number inside milestone
# 1's body), and `## Milestone overview` (no number) is not one either.
milestone_header_re="^##?#?#?${_ms_ws}*milestone${_ms_ws}*#?${_ms_ws}*[0-9][0-9]*([^0-9.].*|[.](${_ms_ws}.*)?)?\$"

# milestone_number_of LINE — the header number (leading zeros stripped) when
# LINE is a milestone header, else nothing.
milestone_number_of() {
  printf '%s\n' "$1" | awk -v re="$milestone_header_re" '
    tolower($0) ~ re { match($0, /[0-9]+/); print substr($0, RSTART, RLENGTH) + 0 }'
}

# _milestone_header_numbers PLAN — every header number in file order
# (duplicates included).
_milestone_header_numbers() {
  awk -v re="$milestone_header_re" '
    tolower($0) ~ re { match($0, /[0-9]+/); print substr($0, RSTART, RLENGTH) + 0 }' "$1" 2>/dev/null
}

# milestone_numbers PLAN — sorted unique header numbers, one per line.
milestone_numbers() {
  _milestone_header_numbers "$1" | sort -n | uniq
}

# milestone_duplicates PLAN — header numbers that appear more than once.
milestone_duplicates() {
  _milestone_header_numbers "$1" | sort -n | uniq -d
}

# extract_milestone N PLAN — print milestone N's section: its header line
# through the line before the next milestone header (or EOF). Sub-numbered
# `## Milestone N.1` headings and un-numbered `## ...` headings stay inside
# the section.
extract_milestone() {
  awk -v re="$milestone_header_re" -v want="$1" '
    tolower($0) ~ re { match($0, /[0-9]+/); inside = (substr($0, RSTART, RLENGTH) + 0 == want + 0) }
    inside { print }' "$2"
}

# parse_files_block — read a plan fragment on stdin and print every declared
# path, one per line. Grammar (LLM-written, so deliberately loose):
#   header : optional list marker, `Files` in any bold/italic wrapping, an
#            optional parenthetical (`**Files (new)**:`), a colon inside or
#            outside the bold (`**Files:**`, `**Files**:`, `Files:`).
#   items  : the header's inline remainder (comma-separated), then following
#            lines while they are blank, `-`/`*`/`+` bullets (any depth) or
#            `1.`/`1)` numbered items. The block ends at a heading, at the
#            next `**Bold**` field (bulleted or not), or at any other
#            non-list line — so sibling fields never leak in as paths.
#   item   : every `[text](target)` link is a path (target); then `(...)`
#            annotations and ` — `/`: ` prose trailers are dropped, the item
#            is split on commas, and each piece yields ONE path — its first
#            backticked span, else its first whitespace token (after a
#            `# comment` is dropped). `N/A`, `none`, `-`, `—`, `(none)` are
#            empty declarations.
# Trailing `,.:;`, wrapping quotes/brackets/bold and a leading `./` are
# stripped. Globs are printed as-is (callers check the static dir prefix).
parse_files_block() {
  awk -v tab="$_ms_tab" -v q="'" -v emdash="—" -v endash="–" '
    function emit_tok(t,   lt) {
      if (t ~ /^\*\*.*\*\*$/) { sub(/^\*\*/, "", t); sub(/\*\*$/, "", t) }
      gsub(/["<>]/, "", t); gsub(q, "", t); gsub(/[][]/, "", t)
      sub(/^[ \t]+/, "", t); sub(/[ \t]+$/, "", t)
      sub(/^\.\//, "", t)
      sub(/[.:;,]+$/, "", t)
      lt = tolower(t)
      if (t == "" || lt == "n/a" || lt == "na" || lt == "none" || lt == "tbd" || t == "-" || t == emdash || t == endash) return
      print t
    }
    function emit_item(s,   t, n, parts, i) {
      while (match(s, /\[[^]]*\]\([^)]*\)/)) {
        t = substr(s, RSTART, RLENGTH)
        s = substr(s, 1, RSTART - 1) " " substr(s, RSTART + RLENGTH)
        sub(/^\[[^]]*\]\(/, "", t); sub(/\)$/, "", t)
        emit_tok(t)
      }
      # Annotations and prose trailers go FIRST, so a backticked type/import
      # inside them (`(new — wraps `net/http`)`, `: implements `io.Reader``)
      # can never surface as a phantom path.
      gsub(/\([^)]*\)/, " ", s)
      sub(trail, "", s)
      sub(/:[ \t].*$/, "", s)
      # Comma-separated pieces; each piece contributes ONE path: its first
      # backticked span, else (after dropping a `# comment`) its first
      # whitespace token. So `a.go`, `b.go` and a mixed `a.go`, c.go list
      # yield every path, while `x.go` and `Client` yields only x.go.
      n = split(s, parts, ",")
      for (i = 1; i <= n; i++) {
        t = parts[i]
        if (match(t, /`[^`]+`/)) {
          emit_tok(substr(t, RSTART + 1, RLENGTH - 2))
          continue
        }
        sub(/#.*$/, "", t)
        sub(/^[ \t]+/, "", t)
        if (t == "") continue
        sub(/[ \t].*$/, "", t)
        emit_tok(t)
      }
    }
    BEGIN {
      ws = "[ " tab "]"
      hdr = "^" ws "*([-*+]" ws "+)?[*_]*files(" ws "[^:*]*)?[*_]*(" ws "*\\([^)]*\\))?[*_]*" ws "*:"
      trail = ws "(" emdash "|" endash "|--)" ws ".*$"
      infiles = 0
    }
    {
      line = $0
      if (tolower(line) ~ hdr) {
        infiles = 1
        rest = line
        sub(/^[^:]*:/, "", rest)
        sub(/^[ \t*_]+/, "", rest)
        if (rest != "") emit_item(rest)
        next
      }
      if (!infiles) next
      if (line ~ /^[ \t]*$/) next
      if (line ~ /^[ \t]*#/) { infiles = 0; next }
      if (line ~ /^[ \t]*([-*+]|[0-9]+[.)])[ \t]+/) {
        item = line
        sub(/^[ \t]*([-*+]|[0-9]+[.)])[ \t]+/, "", item)
        if (item ~ /^(\*\*|__)/) { infiles = 0; next }
        emit_item(item)
        next
      }
      infiles = 0
    }'
}

# milestone_files N PLAN — the declared paths of milestone N (see
# parse_files_block).
milestone_files() {
  extract_milestone "$1" "$2" | parse_files_block
}

# parse_contract_tests_block — read a plan fragment on stdin and print every
# declared contract test name, one per line (tracker-runner #901: a
# milestone must NAME the tests that prove its done-when, so TestMilestone
# can reconcile them against verify.sh's executed-test manifest). Grammar
# (LLM-written, so as loose as parse_files_block's):
#   header : optional list marker, `Contract tests` (any case) in any
#            bold/italic wrapping, optional parenthetical, colon inside or
#            outside the bold (`**Contract tests:**`, `- **Contract Tests**:`).
#   items  : the header's inline remainder, then following lines while they
#            are blank, `-`/`*`/`+` bullets or `1.`/`1)` numbered items; the
#            block ends at a heading, the next `**Bold**` field, or any other
#            non-list line.
#   item   : a ` — `/` -- ` prose trailer is dropped first (so the reason
#            after `none —` never yields a name). Then EVERY backticked span
#            is one test name (a JS title with spaces or commas is declared
#            as `` `renders the help banner` ``); what remains outside the
#            backticks — and the whole item when it has none — loses its
#            `(...)` annotations and `: ...` trailer and is split on commas,
#            each piece trimmed to one name; an un-backticked piece counts
#            only when it is identifier-like (no whitespace — the prompt
#            mandates backticks for a JS title), so `` `TestA` proves the
#            parse `` yields TestA alone and `TestA and TestB` yields
#            nothing. A field whose first item (the inline remainder, or
#            the first bullet) STARTS with `none` / `n/a` / `no test(s)` /
#            `tbd` — after stripping `*_` wrapping and trailing `.:;,`, in
#            any case, with any reason after it (`none — docs`, `none,
#            scaffold`, `none (adds \`go.mod\`)`, `none yet`) — declares
#            NOTHING: the whole block is skipped so no word of the reason
#            can become a bogus, unfixable contract test.
# Names are printed verbatim (Go `TestX`/`TestX/sub`, Rust `mod::test_x`,
# pytest `path::test_x`, a JS describe/it title) — the reconciler in
# TestMilestone decides how each matches the manifest.
parse_contract_tests_block() {
  awk -v tab="$_ms_tab" -v emdash="—" -v endash="–" '
    function emit_tok(t,   lt) {
      if (t ~ /^\*\*.*\*\*$/) { sub(/^\*\*/, "", t); sub(/\*\*$/, "", t) }
      sub(/^[ \t]+/, "", t); sub(/[ \t]+$/, "", t)
      sub(/[.:;,]+$/, "", t)
      lt = tolower(t)
      if (t == "" || lt == "n/a" || lt == "na" || lt == "none" || lt == "tbd" || t == "-" || t == emdash || t == endash) return
      print t
    }
    # is_none_decl(s): the item reads as a "no contract tests" declaration.
    function is_none_decl(s,   t) {
      t = tolower(s)
      gsub(/^[*_ \t]+/, "", t); gsub(/[*_ \t]+$/, "", t)
      sub(/[.:;,]+$/, "", t)
      return (t ~ /^(none|n\/a|na|no tests?|tbd)([^a-z]|$)/)
    }
    function emit_item(s,   t, n, parts, i, ticked) {
      sub(trail, "", s)
      ticked = 0
      while (match(s, /`[^`]+`/)) {
        emit_tok(substr(s, RSTART + 1, RLENGTH - 2))
        s = substr(s, 1, RSTART - 1) " " substr(s, RSTART + RLENGTH)
        ticked = 1
      }
      gsub(/\([^)]*\)/, " ", s)
      sub(/:[ \t].*$/, "", s)
      n = split(s, parts, ",")
      for (i = 1; i <= n; i++) {
        t = parts[i]
        sub(/^[ \t]+/, "", t); sub(/[ \t]+$/, "", t)
        if (t ~ /[ \t]/) continue
        emit_tok(t)
      }
    }
    BEGIN {
      ws = "[ " tab "]"
      hdr = "^" ws "*([-*+]" ws "+)?[*_]*contract" ws "+tests(" ws "[^:*]*)?[*_]*(" ws "*\\([^)]*\\))?[*_]*" ws "*:"
      trail = ws "(" emdash "|" endash "|--)" ws ".*$"
      inblock = 0
    }
    {
      line = $0
      if (tolower(line) ~ hdr) {
        inblock = 1
        first = 1
        rest = line
        sub(/^[^:]*:/, "", rest)
        sub(/^[ \t]+/, "", rest)
        if (rest != "") {
          first = 0
          if (is_none_decl(rest)) { inblock = 0; next }
          sub(/^[*_]+/, "", rest)
          emit_item(rest)
        }
        next
      }
      if (!inblock) next
      if (line ~ /^[ \t]*$/) next
      if (line ~ /^[ \t]*#/) { inblock = 0; next }
      if (line ~ /^[ \t]*([-*+]|[0-9]+[.)])[ \t]+/) {
        item = line
        sub(/^[ \t]*([-*+]|[0-9]+[.)])[ \t]+/, "", item)
        if (item ~ /^(\*\*|__)/) { inblock = 0; next }
        if (first && is_none_decl(item)) { inblock = 0; next }
        first = 0
        emit_item(item)
        next
      }
      inblock = 0
    }'
}

# milestone_contract_tests N PLAN — the declared contract test names of
# milestone N (see parse_contract_tests_block); nothing for "none".
milestone_contract_tests() {
  extract_milestone "$1" "$2" | parse_contract_tests_block
}

# contract_test_executed NAME MANIFEST — true when the executed-test
# manifest (verify.sh's .ai/build/executed-tests.txt: `#` headers + one
# executed test name per line) records NAME as run. Matching is deliberately
# generous in ONE direction only (a declared name may be a prefix/suffix of
# an executed one, never the reverse):
#   exact                    TestX == TestX, mod::t == mod::t, a JS title
#   Go subtest prefix        declared TestX, executed TestX/sub
#   pytest param prefix      declared f.py::t, executed f.py::t[case]
#   `::`-path suffix         declared inspector::t, executed crate::inspector::t;
#                            declared t, executed tests/f.py::t
#   interior segments        declared a::b::leaf, executed contains `a::b`
#                            (in order) and ends in `::leaf` (or `::leaf[..]`):
#                            Rust's idiomatic `mod tests` (inspector::tests::t
#                            for declared inspector::t), a pytest method in a
#                            class (f.py::TestCls::t for declared f.py::t)
#   cargo bare leaf          declared a::leaf, executed exactly `leaf` — only
#                            under a `# executed tests (stack: cargo …)`
#                            section (a cargo integration test in tests/
#                            prints its bare name); never for pytest/Go
#   vitest `file > suite >`  declared "adds", executed "f.ts > calc > adds"
# Quoted case patterns are literal — a name with `*` or `[` never globs.
contract_test_executed() {
  [ -f "$2" ] || return 1
  _ct_pre=""; _ct_leaf="$1"
  case "$1" in *::*) _ct_pre="${1%::*}"; _ct_leaf="${1##*::}" ;; esac
  _ct_stack=""
  while IFS= read -r _ct_ex || [ -n "$_ct_ex" ]; do
    case "$_ct_ex" in
      '') continue ;;
      '# executed tests (stack: '*) _ct_stack="${_ct_ex#\# executed tests (stack: }"; _ct_stack="${_ct_stack%% *}"; continue ;;
      \#*) continue ;;
    esac
    case "$_ct_ex" in
      "$1"|"$1/"*|"$1["*|*"::$1"|*"::$1["*|*" > $1") return 0 ;;
    esac
    [ -n "$_ct_pre" ] || continue
    case "$_ct_ex" in
      *"$_ct_pre"*"::$_ct_leaf"|*"$_ct_pre"*"::$_ct_leaf["*) return 0 ;;
    esac
    if [ "$_ct_stack" = cargo ] && [ "$_ct_ex" = "$_ct_leaf" ]; then return 0; fi
  done < "$2"
  return 1
}

# reconcile_contract_tests DECLARED MANIFEST — tracker-runner #901: prove
# every contract test the milestone declared (DECLARED = PickNextMilestone's
# .ai/milestones/contract-tests, one name per line) actually EXECUTED in
# this verify run (MANIFEST = .ai/build/executed-tests.txt). Prints the
# `--- contract tests: N/M executed ---` tally and one `  MISSING: <name>`
# line per absentee; returns 1 with a `CONTRACT-TEST-MISSING:` line when
# any is missing (TestMilestone turns that into an ordinary red). An empty
# DECLARED ("none") passes — the milestone verifier judges whether "none"
# is justified by the done-when; a missing DECLARED (a resume from before
# the file existed) passes with an INFO line. When MANIFEST carries a
# `# names-unavailable` marker (verify.sh: an oracle ran but the runner
# listed no names — jest's default reporter, vitest per-file lines, pytest
# with a silenced summary, a Makefile-only oracle) a missing name is NOT
# provable either way: it is printed as `WARNING: <name> not provable from
# the manifest (runner lists no names) — verifier decides` and the
# function returns 0 — VerifyMilestone corroborates from the test source
# (file:line) instead. Otherwise every milestone on such a stack would be
# an unfixable CONTRACT-TEST-MISSING ×3 → escalate.
reconcile_contract_tests() {
  if [ ! -f "$1" ]; then
    echo "INFO: no $1 (PickNextMilestone did not write one — a pre-#901 resume?) — no contract tests to reconcile"
    return 0
  fi
  _rc_total=$(grep -c . "$1" 2>/dev/null || true)
  if [ "${_rc_total:-0}" -eq 0 ]; then
    echo "--- contract tests: none declared ---"
    return 0
  fi
  _rc_hit=0
  _rc_missing=""
  while IFS= read -r _rc_d || [ -n "$_rc_d" ]; do
    [ -n "$_rc_d" ] || continue
    if contract_test_executed "$_rc_d" "$2"; then
      _rc_hit=$((_rc_hit + 1))
    else
      _rc_missing="$_rc_missing
$_rc_d"
    fi
  done < "$1"
  echo "--- contract tests: $_rc_hit/$_rc_total executed ---"
  [ -n "$_rc_missing" ] || return 0
  if grep -q '^# names-unavailable' "$2" 2>/dev/null; then
    printf '%s\n' "$_rc_missing" | grep . | sed 's/^/WARNING: /; s/$/ not provable from the manifest (runner lists no names) — verifier decides/'
    grep '^# names-unavailable' "$2" | sed 's/^# /  manifest: /'
    return 0
  fi
  printf '%s\n' "$_rc_missing" | grep . | sed 's/^/  MISSING: /'
  printf '%s\n' "CONTRACT-TEST-MISSING: $(printf '%s\n' "$_rc_missing" | grep . | paste -sd',' - | sed 's/,/, /g') — declared in the milestone's **Contract tests** but absent from $2 (the executed-test manifest). Write the named test so it runs, or correct the declared name to the exact executed one; \`sh .ai/build/verify.sh\` refreshes the manifest."
  return 1
}

# glob_static_dir PATH — the directory prefix of PATH before its first glob
# component (`pkg/*.go` -> `pkg`, `**/*.go` -> `.`, `a/b/*/c` -> `a/b`).
# Never pathname-expands its argument.
glob_static_dir() {
  _gsd_out=""
  _gsd_ifs=$IFS
  set -f
  IFS=/
  for _gsd_c in $1; do
    case "$_gsd_c" in *[*?[]*) break ;; esac
    _gsd_out="${_gsd_out:+$_gsd_out/}$_gsd_c"
  done
  IFS=$_gsd_ifs
  set +f
  printf '%s' "${_gsd_out:-.}"
}

# reset_plan_state — wipe every per-PLAN state file so a fresh or re-planned
# build starts at milestone 1 with zero budgets spent (#640 B1). Called by
# Setup (fresh run; Setup is skipped on `tracker -r` resume so a resumed run
# keeps its state) and by ResetReviewBudget (EscalateReview `retry` ->
# Decompose re-plan). Removes:
#   .ai/milestones/            done/ markers, current.md, fix_attempts,
#                              verify_fail_attempts (group R's
#                              CheckVerifyFailBudget), known_failures,
#                              known_lint_failures and their .snapshot
#                              copies (group V's TestMilestone)
#   .ai/build/milestone-start-sha, review_fix_attempts, declared-files.*,
#                              scoped-milestones.md, executed-tests.txt
#                              (a prior plan's manifest can't satisfy a new
#                              plan's contract tests)
#   .tracker/turn_overrides/   #318 warm-continue cap + MaxTurns overrides
# Deliberately KEPT: SPEC.md, .ai/decisions/* (Decompose rewrites its own),
# the .ai/build runtime gate files (verify.sh, ci-probe.sh, rubric,
# build-context.md, run-base-sha — Setup re-seeds them) and the operator
# opt-in stamps (.ai/build/allow-dirty, .ai/build/no-tests-ok).
reset_plan_state() {
  rm -rf .ai/milestones .tracker/turn_overrides
  rm -f .ai/build/milestone-start-sha .ai/build/review_fix_attempts \
        .ai/build/declared-files.raw .ai/build/declared-files.list \
        .ai/build/scoped-milestones.md .ai/build/executed-tests.txt
  mkdir -p .ai/milestones
}
