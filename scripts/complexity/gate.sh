#!/usr/bin/env bash
# ABOUTME: Complexity ratchet — grandfathers a baseline of gocyclo/gocognit/file-size
# ABOUTME: violations that may only shrink; fails on NEW or WORSE debt (#468).
set -euo pipefail

CYCLO_MAX="${CYCLO_MAX:-8}"
COGNITIVE_MAX="${COGNITIVE_MAX:-8}"
FILE_MAX_LINES="${FILE_MAX_LINES:-500}"
GOCYCLO_VERSION="${GOCYCLO_VERSION:-v0.6.0}"
GOCOGNIT_VERSION="${GOCOGNIT_VERSION:-v1.2.1}"
BASELINE="${BASELINE:-scripts/complexity/baseline.txt}"

# The one source of truth for what gets scanned: production Go only, excluding
# tests, vendored code, generated worktrees, and the research conformance harness.
list_files() {
  find . -name '*.go' \
    -not -name '*_test.go' \
    -not -path './vendor/*' \
    -not -path './.worktrees/*' \
    -not -path './.claude/*' \
    -not -path './cmd/tracker-conformance/*' | sort
}

# Verify a pinned analyzer actually builds and runs before we trust an empty
# result to mean "no violations". A trivial file with an impossible threshold
# exits 0 iff the tool ran; a module-fetch/build failure exits non-zero and we
# hard-fail loudly instead of silently scanning nothing (#468).
ensure_tool() { # <module> <version>
  local tmp; tmp=$(mktemp -d)
  printf 'package p\nfunc F() {}\n' > "$tmp/x.go"
  if ! go run "$1@$2" -over 999 "$tmp/x.go" >/dev/null 2>&1; then
    rm -rf "$tmp"
    echo "FATAL: cannot run $1@$2 (module fetch / build failure?) — the complexity gate cannot verify anything, refusing to pass" >&2
    exit 1
  fi
  rm -rf "$tmp"
}

# Emit current violations, normalized to metric|file|func|value (line-insensitive:
# file path only, no :line:col, so entries survive ordinary edits).
scan() {
  ensure_tool "github.com/fzipp/gocyclo/cmd/gocyclo" "$GOCYCLO_VERSION"
  ensure_tool "github.com/uudashr/gocognit/cmd/gocognit" "$GOCOGNIT_VERSION"
  local files; files=$(list_files)
  [ -n "$files" ] || { echo "FATAL: complexity scan matched no Go files — scope is empty, refusing to pass" >&2; exit 1; }

  # gocyclo/gocognit exit 1 when they find over-threshold functions (by design,
  # for use as a CI gate); under pipefail that would abort the scan early via
  # xargs's propagated failure, so tolerate it here — we WANT their findings.
  # But that same exit-1 is indistinguishable by code alone from a real abort
  # (unparseable file etc), which prints NOTHING to stdout and would silently
  # scan as "0 findings". The discriminator is stderr: normal findings runs
  # write nothing to stderr from the tool itself (go run's own "exit status N"
  # trailer, which it always emits when the wrapped binary exits non-zero, is
  # filtered out as expected noise); real errors (parse failure, bad args,
  # build failure) additionally write a message before that trailer. Capture
  # stderr, strip the trailer, and FATAL if anything real remains — instead of
  # letting `|| true` swallow a broken scan (#468).
  run_metric() { # <module> <version> <threshold> <metric-label>
    local err; err=$(mktemp)
    { printf '%s\n' "$files" | xargs go run "$1@$2" -over "$3" 2>"$err" | awk -v m="$4" '{ split($4,a,":"); print m "|" a[1] "|" $3 "|" $1 }'; } || true
    grep -vE '^exit status [0-9]+$' "$err" > "$err.real" || true
    if [ -s "$err.real" ]; then
      cat "$err.real" >&2
      rm -f "$err" "$err.real"
      echo "FATAL: $1 errored during scan (see above) — refusing to scan nothing (#468)" >&2
      exit 1
    fi
    rm -f "$err" "$err.real"
  }
  run_metric "github.com/fzipp/gocyclo/cmd/gocyclo" "$GOCYCLO_VERSION" "$CYCLO_MAX" cyclo
  run_metric "github.com/uudashr/gocognit/cmd/gocognit" "$GOCOGNIT_VERSION" "$COGNITIVE_MAX" cognitive
  printf '%s\n' "$files" | while IFS= read -r f; do
    n=$(wc -l < "$f" | tr -d ' ')
    if [ "$n" -gt "$FILE_MAX_LINES" ]; then printf 'filesize|%s|-|%s\n' "$f" "$n"; fi
  done
}

# compare <baseline-file> <current-file>: prints and exits 1 if any CURRENT entry
# is absent from BASELINE (NEW) or exceeds its baselined value (WORSE). Entries in
# BASELINE but not CURRENT (fixed / improved below threshold) are fine.
compare() {
  awk -F'|' '
    NR==FNR { if (NF<4) next; base[$1"|"$2"|"$3]=$4; next }
    {
      if (NF<4) next
      key=$1"|"$2"|"$3; val=$4+0
      if (!(key in base)) { print "  NEW    " $0; bad=1 }
      else if (val > base[key]+0) { print "  WORSE  " $0 "  (baseline " base[key] ")"; bad=1 }
    }
    END { exit bad?1:0 }
  ' "$1" "$2"
}

cmd="${1:-gate}"
case "$cmd" in
  scan)   scan | sort ;;
  update) scan | sort > "$BASELINE"; echo "wrote $BASELINE ($(wc -l < "$BASELINE" | tr -d ' ') grandfathered violations)" ;;
  check)  if compare "$2" "$3"; then echo "complexity gate OK: no new or worsened violations"; else
            echo "FAIL: new/worsened complexity or file-size violations above the grandfathered baseline."; echo "Fix them, or if a legitimate decomposition LOWERED a value, run 'make complexity-update' and commit the shrunk baseline."; exit 1; fi ;;
  gate)   tmp=$(mktemp); scan | sort > "$tmp"; rc=0; compare "$BASELINE" "$tmp" || rc=$?
          if [ "$rc" -eq 0 ]; then echo "complexity gate OK ($(wc -l < "$BASELINE" | tr -d ' ') grandfathered, 0 new)"; else
            echo "FAIL: new/worsened violations above the grandfathered baseline (see above)."; fi
          rm -f "$tmp"; exit "$rc" ;;
  baseline-shrinks) base=$(mktemp); git show "$2:$BASELINE" > "$base" 2>/dev/null || : > "$base"; rc=0; compare "$base" "$BASELINE" || rc=$?
          if [ "$rc" -eq 0 ]; then echo "baseline OK: did not grow vs $2"; else
            echo "FAIL: $BASELINE grew (new or raised entries) vs $2 — the baseline may only shrink."; fi
          rm -f "$base"; exit "$rc" ;;
  *) echo "usage: gate.sh [scan|update|gate|check <baseline> <current>|baseline-shrinks <ref>]"; exit 2 ;;
esac
