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
#                              scoped-milestones.md
#   .tracker/turn_overrides/   #318 warm-continue cap + MaxTurns overrides
# Deliberately KEPT: SPEC.md, .ai/decisions/* (Decompose rewrites its own),
# the .ai/build runtime gate files (verify.sh, ci-probe.sh, rubric,
# build-context.md, run-base-sha — Setup re-seeds them) and the operator
# opt-in stamps (.ai/build/allow-dirty, .ai/build/no-tests-ok).
reset_plan_state() {
  rm -rf .ai/milestones .tracker/turn_overrides
  rm -f .ai/build/milestone-start-sha .ai/build/review_fix_attempts \
        .ai/build/declared-files.raw .ai/build/declared-files.list \
        .ai/build/scoped-milestones.md
  mkdir -p .ai/milestones
}
