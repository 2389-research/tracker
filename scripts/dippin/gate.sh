#!/usr/bin/env bash
# ABOUTME: Fail-closed Dippin validation gate — one pinned version (go.mod) and
# ABOUTME: loud failure on command error, malformed JSON, or an empty input set.
set -euo pipefail

# The Dippin gate is the single authority for how tracker validates .dip files:
#   version      print the pinned dippin version derived from go.mod
#   lint <glob>… lint each matched file; FAIL LOUD on no matches, a command that
#                emits no parseable JSON, or a file with >0 errors
#
# Version is derived from go.mod so the gate, the module dependency, and CI can
# never disagree (the SIFT-15-02 finding: a stale second pin in ci.yml). The
# `DIPPIN` env var overrides the invocation for tests only (inject a fake).
#
# Dippin's contract (verified against v0.68.0): `dippin check` exits NONZERO for
# ordinary findings but STILL emits valid JSON on stdout. So we parse the JSON
# and read its `errors` field — we do NOT treat a nonzero exit as a crash. A
# crash / missing binary / malformed output yields no parseable JSON, which is
# the only "execution failure" signal, and it fails the gate loudly. Coercing
# any of these to zero errors (the old `|| echo 0`) is a silent-swallow that
# violates CLAUDE.md's loud-failure rule.

GO_MOD="${GO_MOD:-go.mod}"

dippin_version() {
  local ver
  ver=$(awk '$1=="github.com/2389-research/dippin-lang" {print $2}' "$GO_MOD")
  if [ -z "$ver" ]; then
    echo "FATAL: could not derive dippin version from $GO_MOD — refusing to run" >&2
    exit 1
  fi
  printf '%s' "$ver"
}

dippin_cmd() {
  if [ -n "${DIPPIN:-}" ]; then
    printf '%s' "$DIPPIN"
    return
  fi
  printf 'go run github.com/2389-research/dippin-lang/cmd/dippin@%s' "$(dippin_version)"
}

# json_errors reads stdin and prints the integer `errors` field. It exits
# nonzero on malformed JSON or a missing `errors` key — the decode-failure
# signal the caller turns into a loud gate failure.
json_errors() {
  python3 -c 'import sys, json; d = json.load(sys.stdin); print(int(d["errors"]))'
}

lint() {
  if [ "$#" -eq 0 ]; then
    echo "usage: gate.sh lint <glob>..." >&2
    exit 2
  fi
  local files=() pat
  shopt -s nullglob
  for pat in "$@"; do
    # shellcheck disable=SC2206  # deliberate glob expansion of the pattern
    files+=( $pat )
  done
  shopt -u nullglob
  if [ "${#files[@]}" -eq 0 ]; then
    echo "FAIL: dippin lint matched no input files (patterns: $*) — refusing to pass on an empty set" >&2
    exit 1
  fi

  local dippin fail=0 f out errfile errors
  dippin=$(dippin_cmd)
  errfile=$(mktemp)
  trap 'rm -f "$errfile"' RETURN
  for f in "${files[@]}"; do
    # Capture stdout (the JSON) separately from stderr (go-toolchain chatter /
    # crash traces). `|| true` because a findings run exits nonzero yet still
    # printed valid JSON we must read.
    # shellcheck disable=SC2086  # $dippin is an intentional multi-word command
    out=$($dippin check "$f" 2>"$errfile") || true
    if ! errors=$(printf '%s' "$out" | json_errors 2>/dev/null); then
      echo "FAIL: dippin check '$f' produced no parseable JSON (execution or decode failure)" >&2
      echo "  stdout: ${out:-<empty>}" >&2
      echo "  stderr: $(cat "$errfile")" >&2
      exit 1
    fi
    if [ "$errors" -gt 0 ]; then
      echo "  $f: $errors errors"
      fail=1
    fi
  done
  if [ "$fail" -ne 0 ]; then
    echo "FAIL: dippin lint errors in ${#files[@]} checked file(s)" >&2
    exit 1
  fi
  echo "dippin gate OK: ${#files[@]} file(s) pass lint (via $(dippin_version))"
}

cmd="${1:-}"
shift || true
case "$cmd" in
  version) dippin_version; echo ;;
  lint)    lint "$@" ;;
  *) echo "usage: gate.sh [version | lint <glob>...]" >&2; exit 2 ;;
esac
